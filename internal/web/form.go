package web

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"receipts-db/internal/models"
)

// parseCents parses "12.34", "-1.5", "$3" into cents; "" is 0.
func parseCents(s string) (int64, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	if s == "" {
		return 0, nil
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 2 || len(whole) > 10 || strings.ContainsAny(whole+frac, "+-eE ") {
		return 0, errors.New("bad amount")
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil {
		return 0, errors.New("bad amount")
	}
	c := w*100 + f
	if neg {
		c = -c
	}
	return c, nil
}

// applyForm copies the edit form onto rc. Item rows are matched by position to the existing
// items (keeping fields the form does not show, e.g. sku/upc/product), extra rows are new items,
// and rows ticked "remove" are dropped. A row with an empty name is an error unless it is an
// untouched blank new row. Adjustments are kept as-is.
func applyForm(rc *models.Receipt, f url.Values) error {
	money := func(field, label string) (int64, error) {
		c, err := parseCents(f.Get(field))
		if err != nil {
			return 0, fmt.Errorf("%s: enter an amount like 12.34", label)
		}
		return c, nil
	}
	rc.StoreName = strings.TrimSpace(f.Get("store_name"))
	// The form has no category field; the loaded value is the (old) merchant's, so sending it
	// back could clash with a renamed-to merchant's category (ErrStoreField). Leave it alone.
	rc.StoreCategory = ""
	rc.Date = strings.TrimSpace(f.Get("date"))
	rc.PaymentMethod = strings.TrimSpace(f.Get("payment_method"))
	var err error
	if rc.SubtotalCents, err = money("subtotal", "Subtotal"); err != nil {
		return err
	}
	if rc.TaxCents, err = money("tax", "Tax"); err != nil {
		return err
	}
	if rc.TotalCents, err = money("total", "Total"); err != nil {
		return err
	}
	if rc.SavingsCents, err = money("savings", "Savings"); err != nil {
		return err
	}
	names := f["item_name"]
	qtys, prices, lines, discs := f["item_qty"], f["item_price"], f["item_total"], f["item_discount"]
	at := func(v []string, i int) string {
		if i < len(v) {
			return v[i]
		}
		return ""
	}
	remove := map[int]bool{}
	for _, v := range f["item_remove"] {
		if n, err := strconv.Atoi(v); err == nil {
			remove[n] = true
		}
	}
	var items []models.ReceiptItem
	for i, name := range names {
		name = strings.TrimSpace(name)
		if remove[i] {
			continue
		}
		if name == "" {
			blank := strings.TrimSpace(at(qtys, i)+at(prices, i)+at(lines, i)+at(discs, i)) == ""
			if i >= len(rc.Items) && blank {
				continue // the empty "new item" row
			}
			return fmt.Errorf("item %d: name is required (tick Remove to delete the item)", i+1)
		}
		if utf8.RuneCountInString(name) > models.MaxNameLen {
			return fmt.Errorf("item %d: name is too long (max %d characters)", i+1, models.MaxNameLen)
		}
		var it models.ReceiptItem
		if i < len(rc.Items) {
			it = rc.Items[i]
		}
		it.Name = name
		q := strings.TrimSpace(at(qtys, i))
		if q == "" {
			q = "1"
		}
		qty, err := strconv.ParseFloat(q, 64)
		if err != nil || qty <= 0 || math.IsNaN(qty) || math.IsInf(qty, 0) || qty > models.MaxQuantity {
			return fmt.Errorf("item %d (%s): quantity must be a number between 0 and %d", i+1, name, models.MaxQuantity)
		}
		it.Quantity = qty
		if it.UnitPriceCents, err = money2(at(prices, i), i, name, "price"); err != nil {
			return err
		}
		if it.LineTotalCents, err = money2(at(lines, i), i, name, "line total"); err != nil {
			return err
		}
		if it.DiscountCents, err = money2(at(discs, i), i, name, "discount"); err != nil {
			return err
		}
		if it.LineTotalCents == 0 {
			it.LineTotalCents = it.ComputedLineTotalCents()
		}
		items = append(items, it)
	}
	rc.Items = items
	return nil
}

func money2(v string, i int, name, label string) (int64, error) {
	c, err := parseCents(v)
	if err != nil {
		return 0, fmt.Errorf("item %d (%s): %s must be an amount like 1.99", i+1, name, label)
	}
	return c, nil
}

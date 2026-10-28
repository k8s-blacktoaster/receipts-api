package web

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

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
	if len(frac) > 2 || strings.ContainsAny(whole+frac, "+-eE ") {
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
// and rows with an empty name are removed. Adjustments are kept as-is.
func applyForm(rc *models.Receipt, f url.Values) error {
	money := func(field, label string) (int64, error) {
		c, err := parseCents(f.Get(field))
		if err != nil {
			return 0, fmt.Errorf("%s: enter an amount like 12.34", label)
		}
		return c, nil
	}
	rc.StoreName = strings.TrimSpace(f.Get("store_name"))
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
	var items []models.ReceiptItem
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
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
		if err != nil || qty <= 0 {
			return fmt.Errorf("item %d (%s): quantity must be a positive number", i+1, name)
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

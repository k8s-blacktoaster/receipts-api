package repository

import (
	"context"
	"errors"
	"testing"

	"receipts-db/internal/models"
)

func TestQueryRepository(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	receipts, products, merchants, q := NewReceiptRepository(db), NewProductRepository(db), NewMerchantRepository(db), NewQueryRepository(db)

	mk := func(store, date string, total int64, items ...models.ReceiptItem) *models.Receipt {
		r := &models.Receipt{StoreName: store, Date: date, SubtotalCents: total, TotalCents: total,
			Source: models.SourceManual, Items: items}
		if err := receipts.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	coffee := &models.Product{CanonicalName: "Coffee beans 2lb"}
	if err := products.CreateProduct(ctx, coffee); err != nil {
		t.Fatal(err)
	}
	r1 := mk("Trader Joe's", "2026-01-10", 1500, models.ReceiptItem{Name: "COFFEE BEANS", Quantity: 1, UnitPriceCents: 1000, NetTotalCents: 1000})
	r2 := mk("Trader Joe's", "2026-02-12", 2500, models.ReceiptItem{Name: "Coffee beans", Quantity: 1, UnitPriceCents: 1200, NetTotalCents: 1200})
	r3 := mk("Corner Grocer", "2026-02-20", 900, models.ReceiptItem{Name: "coffee beans", Quantity: 1, UnitPriceCents: 1100, NetTotalCents: 1100})
	busy := mk("Corner Grocer", "2026-02-21", 99999, models.ReceiptItem{Name: "Coffee beans", Quantity: 1, UnitPriceCents: 1, NetTotalCents: 1})
	if _, err := db.ExecContext(ctx, `UPDATE receipts SET status = 'failed' WHERE id = $1`, busy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE receipt_items SET product_id = $1`, coffee.ID); err != nil {
		t.Fatal(err)
	}

	// MCP-05: list with counts/last visit; failed receipt not counted.
	ms, err := q.ListMerchants(ctx)
	if err != nil || len(ms) != 2 {
		t.Fatalf("merchants %+v %v", ms, err)
	}
	if ms[0].Name != "corner_grocer" || ms[0].ReceiptCount != 1 || ms[0].TotalSpentCents != 900 || ms[0].LastReceiptID != int64(r3.ID) {
		t.Fatalf("corner grocer %+v", ms[0])
	}
	if ms[1].Name != "trader_joes" || ms[1].ReceiptCount != 2 || ms[1].LastVisit != "2026-02-12" {
		t.Fatalf("tj %+v", ms[1])
	}

	// MCP-04: inputs normalized; MCP-06: fuzzy candidates when no exact match.
	m, _, err := q.ResolveMerchant(ctx, "TRADER JOE’S", 5)
	if err != nil || m == nil || m.Name != "trader_joes" {
		t.Fatalf("resolve exact %+v %v", m, err)
	}
	m, cands, err := q.ResolveMerchant(ctx, "Trader Joes Market", 5)
	if err != nil || m != nil || len(cands) == 0 || cands[0].Name != "trader_joes" {
		t.Fatalf("fuzzy %+v %+v %v", m, cands, err)
	}
	if _, _, err := q.ResolveMerchant(ctx, "!!!", 5); !errors.Is(err, ErrInvalidMerchantName) {
		t.Fatalf("empty key err %v", err)
	}
	// merged-away names resolve via redirects.
	tj, cg := ms[1].ID, ms[0].ID
	other := mk("TJs Old", "2026-01-01", 100)
	om, _, _ := q.ResolveMerchant(ctx, "TJs Old", 1)
	if _, err := merchants.Merge(ctx, om.ID, tj); err != nil {
		t.Fatal(err)
	}
	_ = other
	m, _, err = q.ResolveMerchant(ctx, "tjs old", 5)
	if err != nil || m == nil || m.ID != tj {
		t.Fatalf("redirect %+v %v", m, err)
	}

	// search purchases: case-insensitive, merchant + date filters, failed excluded.
	lines, err := q.SearchPurchases(ctx, PurchaseFilter{Query: "Coffee"})
	if err != nil || len(lines) != 3 || lines[0].ReceiptID != int64(r3.ID) || lines[0].PurchaseDate != "2026-02-20" {
		t.Fatalf("search %+v %v", lines, err)
	}
	lines, _ = q.SearchPurchases(ctx, PurchaseFilter{Query: "coffee", MerchantID: tj, From: "2026-02-01"})
	if len(lines) != 1 || lines[0].ReceiptID != int64(r2.ID) {
		t.Fatalf("filtered search %+v", lines)
	}

	// price history oldest first.
	h, err := q.PriceHistory(ctx, coffee.ID, 0, 0)
	if err != nil || len(h) != 3 || h[0].UnitPriceCents != 1000 || h[2].UnitPriceCents != 1100 {
		t.Fatalf("history %+v %v", h, err)
	}
	h, _ = q.PriceHistory(ctx, coffee.ID, cg, 0)
	if len(h) != 1 {
		t.Fatalf("history by merchant %+v", h)
	}
	last, err := q.LastPurchases(ctx, []int64{coffee.ID, 999})
	if err != nil || len(last) != 1 || last[coffee.ID].ReceiptID != int64(r3.ID) {
		t.Fatalf("last %+v %v", last, err)
	}

	// spend by merchant / month with receipt ids.
	g, err := q.Spend(ctx, SpendFilter{GroupBy: "month", To: "2026-02-28"})
	if err != nil || len(g) != 2 || g[1].Key != "2026-02" || g[1].TotalCents != 3400 || len(g[1].ReceiptIDs) != 2 {
		t.Fatalf("spend month %+v %v", g, err)
	}
	g, _ = q.Spend(ctx, SpendFilter{MerchantID: tj, From: "2026-01-05"})
	if len(g) != 1 || g[0].TotalCents != 4000 || g[0].ReceiptIDs[0] != int64(r2.ID) || g[0].ReceiptIDs[1] != int64(r1.ID) {
		t.Fatalf("spend tj %+v", g)
	}
	if _, err := q.Spend(ctx, SpendFilter{GroupBy: "year"}); !errors.Is(err, ErrBadGroupBy) {
		t.Fatalf("group by err %v", err)
	}
}

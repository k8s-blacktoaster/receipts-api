package repository_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"receipts-db/internal/mcp"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

// End to end over HTTP against the real DB: auth -> tools/call -> SQL -> JSON with receipt ids.
func TestMCPEndToEnd(t *testing.T) {
	db := repository.TestDB(t)
	ctx := context.Background()
	receipts, products := repository.NewReceiptRepository(db), repository.NewProductRepository(db)
	tea := &models.Product{CanonicalName: "Green tea"}
	if err := products.CreateProduct(ctx, tea); err != nil {
		t.Fatal(err)
	}
	r := &models.Receipt{StoreName: "Barnes & Noble", Date: "2026-04-02", SubtotalCents: 450, TotalCents: 450,
		Source: models.SourceManual, Items: []models.ReceiptItem{{Name: "Green tea", Quantity: 1, UnitPriceCents: 450, NetTotalCents: 450}}}
	if err := receipts.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE receipt_items SET product_id = $1`, tea.ID); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mcp.RequireBearer("tok", &mcp.Server{Name: "receipts-api", Version: "test",
		Tools: mcp.Tools(mcp.Deps{Query: repository.NewQueryRepository(db), Products: products})}))
	defer srv.Close()

	rpc := func(tok, body string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(res.Body).Decode(&m)
		return res.StatusCode, m
	}
	if code, _ := rpc("wrong", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 401 {
		t.Fatalf("unauthenticated = %d", code)
	}
	call := func(name, args string) map[string]any {
		code, m := rpc("tok", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
		res, _ := m["result"].(map[string]any)
		if code != 200 || res == nil || res["isError"] != false {
			t.Fatalf("%s: %d %v", name, code, m)
		}
		return res["structuredContent"].(map[string]any)
	}
	rid := float64(r.ID)
	if s := call("spend", `{"merchant":"barnes and noble"}`); s["total_cents"] != 450.0 || s["groups"].([]any)[0].(map[string]any)["receipt_ids"].([]any)[0] != rid {
		t.Fatalf("spend %v", s)
	}
	if s := call("where_bought", `{"product_id":`+jsonInt(tea.ID)+`}`); s["receipt_ids"].([]any)[0] != rid ||
		s["locations"].([]any)[0].(map[string]any)["merchant_name"] != "barnes_and_noble" {
		t.Fatalf("where_bought %v", s)
	}
	if s := call("search_purchases", `{"query":"TEA"}`); s["purchases"].([]any)[0].(map[string]any)["purchase_date"] != "2026-04-02" {
		t.Fatalf("search %v", s)
	}
	_, m := rpc("tok", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spend","arguments":{"merchant":"Barnes Nobel"}}}`)
	res := m["result"].(map[string]any)
	cands := res["structuredContent"].(map[string]any)["details"].(map[string]any)["candidates"].([]any)
	if res["isError"] != true || cands[0].(map[string]any)["name"] != "barnes_and_noble" {
		t.Fatalf("fuzzy %v", m)
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

// QA repro: 250 daily purchases from 2020-01-01, the last 50 at costco. where_bought must report
// costco 2020-09-06 as the newest location (it used to read only the oldest 200 rows).
func TestWhereBoughtPast200Purchases(t *testing.T) {
	db := repository.TestDB(t)
	ctx := context.Background()
	receipts, products := repository.NewReceiptRepository(db), repository.NewProductRepository(db)
	p := &models.Product{CanonicalName: "Paper towels"}
	if err := products.CreateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 250; i++ {
		store := "Trader Joe's"
		if i >= 200 {
			store = "Costco"
		}
		price := int64(1000 + i)
		r := &models.Receipt{StoreName: store, Date: day.AddDate(0, 0, i).Format("2006-01-02"), SubtotalCents: price, TotalCents: price,
			Source: models.SourceManual, Items: []models.ReceiptItem{{Name: "Paper towels", Quantity: 1, UnitPriceCents: price, NetTotalCents: price}}}
		if err := receipts.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE receipt_items SET product_id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	srv := &mcp.Server{Tools: mcp.Tools(mcp.Deps{Query: repository.NewQueryRepository(db), Products: products})}
	h := mcp.RequireBearer("tok", srv)
	call := func(name, args string) map[string]any {
		req := httptest.NewRequest("POST", "http://x/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		res := m["result"].(map[string]any)
		if res["isError"] != false {
			t.Fatalf("%s: %v", name, m)
		}
		return res["structuredContent"].(map[string]any)
	}
	out := call("where_bought", `{"product_id":`+jsonInt(p.ID)+`,"limit":5}`)
	b, _ := json.Marshal(map[string]any{"locations": out["locations"], "truncated": out["truncated"], "receipt_ids": out["receipt_ids"]})
	t.Logf("where_bought: %s", b)
	locs := out["locations"].([]any)
	if len(locs) != 2 {
		t.Fatalf("locations %v", locs)
	}
	c, tj := locs[0].(map[string]any), locs[1].(map[string]any)
	if c["merchant_name"] != "costco" || c["last_date"] != "2020-09-06" || c["purchases"] != 50.0 || c["last_unit_price_cents"] != 1249.0 ||
		len(c["receipt_ids"].([]any)) != 50 || c["receipt_ids_truncated"] != false {
		t.Fatalf("costco %v", c)
	}
	if tj["merchant_name"] != "trader_joes" || tj["last_date"] != "2020-07-18" || tj["purchases"] != 200.0 ||
		len(tj["receipt_ids"].([]any)) != repository.LocationReceiptIDs || tj["receipt_ids_truncated"] != true {
		t.Fatalf("tj %v", tj)
	}
	ps := out["purchases"].([]any)
	if len(ps) != 5 || out["truncated"] != true || ps[0].(map[string]any)["purchase_date"] != "2020-09-06" {
		t.Fatalf("purchases %v", out)
	}

	// price_history with a limit: newest rows, shown oldest first.
	out = call("price_history", `{"product_id":`+jsonInt(p.ID)+`,"limit":3}`)
	ps = out["purchases"].([]any)
	if len(ps) != 3 || out["truncated"] != true || ps[0].(map[string]any)["purchase_date"] != "2020-09-04" ||
		ps[2].(map[string]any)["purchase_date"] != "2020-09-06" {
		t.Fatalf("price_history %v", out)
	}
	out = call("price_history", `{"product_id":`+jsonInt(p.ID)+`,"merchant":"costco","limit":50}`)
	if len(out["purchases"].([]any)) != 50 || out["truncated"] != false {
		t.Fatalf("exactly limit rows must not be truncated: %d %v", len(out["purchases"].([]any)), out["truncated"])
	}
	out = call("search_purchases", `{"query":"towels","merchant":"costco","limit":49}`)
	if len(out["purchases"].([]any)) != 49 || out["truncated"] != true {
		t.Fatalf("search truncated %v", out["truncated"])
	}

	// spend: receipt ids capped per group, flagged.
	out = call("spend", `{}`)
	gs := out["groups"].([]any)
	g0, g1 := gs[0].(map[string]any), gs[1].(map[string]any) // costco, trader_joes
	if len(g0["receipt_ids"].([]any)) != 50 || g0["receipt_ids_truncated"] != false ||
		g1["receipt_count"] != 200.0 || len(g1["receipt_ids"].([]any)) != repository.SpendReceiptIDs || g1["receipt_ids_truncated"] != true {
		t.Fatalf("spend %v", gs)
	}
}

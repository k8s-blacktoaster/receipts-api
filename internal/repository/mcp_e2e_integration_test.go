package repository_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

type fakeQuery struct {
	repository.QueryRepository
	resolved []string
	spendF   repository.SpendFilter
	searchF  repository.PurchaseFilter
	lines    []models.PurchaseLine
}

func (f *fakeQuery) ResolveMerchant(_ context.Context, name string, _ int) (*models.MerchantSummary, []models.MerchantCandidate, error) {
	key := models.NormalizeMerchantName(name)
	f.resolved = append(f.resolved, key)
	switch key {
	case "":
		return nil, nil, repository.ErrInvalidMerchantName
	case "trader_joes":
		return &models.MerchantSummary{ID: 7, Name: key}, nil, nil
	}
	return nil, []models.MerchantCandidate{{ID: 7, Name: "trader_joes", Similarity: 0.4}}, nil
}
func (f *fakeQuery) ListMerchants(context.Context) ([]models.MerchantSummary, error) {
	return []models.MerchantSummary{{ID: 7, Name: "trader_joes", ReceiptCount: 2, LastVisit: "2026-02-01", LastReceiptID: 11}}, nil
}
func (f *fakeQuery) SearchPurchases(_ context.Context, pf repository.PurchaseFilter) ([]models.PurchaseLine, error) {
	f.searchF = pf
	return f.lines, nil
}
func (f *fakeQuery) PriceHistory(_ context.Context, _, _ int64, _ int) ([]models.PurchaseLine, error) {
	return f.lines, nil
}
func (f *fakeQuery) Spend(_ context.Context, sf repository.SpendFilter) ([]models.SpendGroup, error) {
	f.spendF = sf
	return []models.SpendGroup{{Key: "trader_joes", ReceiptCount: 2, TotalCents: 4000, ReceiptIDs: []int64{11, 10}}}, nil
}
func (f *fakeQuery) LastPurchases(context.Context, []int64) (map[int64]models.PurchaseLine, error) {
	return map[int64]models.PurchaseLine{1: {ReceiptID: 11, PurchaseDate: "2026-02-01"}}, nil
}

type fakeProducts struct{}

func (fakeProducts) SearchProducts(context.Context, string, int) ([]models.Product, error) {
	return []models.Product{{ID: 1, CanonicalName: "Coffee"}, {ID: 2, CanonicalName: "Tea"}}, nil
}
func (fakeProducts) GetProduct(_ context.Context, id int64) (*models.Product, error) {
	if id != 1 {
		return nil, repository.ErrProductNotFound
	}
	return &models.Product{ID: 1, CanonicalName: "Coffee"}, nil
}

func call(t *testing.T, fq *fakeQuery, name, args string) (map[string]any, bool) {
	t.Helper()
	s := &Server{Tools: Tools(Deps{Query: fq, Products: fakeProducts{}})}
	res, rerr := s.dispatch(context.Background(), rpcRequest{Method: "tools/call",
		Params: json.RawMessage(`{"name":"` + name + `","arguments":` + args + `}`)})
	if rerr != nil {
		t.Fatalf("%s: rpc error %+v", name, rerr)
	}
	m := res.(map[string]any)
	var out map[string]any
	_ = json.Unmarshal([]byte(m["content"].([]map[string]any)[0]["text"].(string)), &out)
	return out, m["isError"].(bool)
}

func store(id int64) *int64 { return &id }

func TestToolsList(t *testing.T) {
	want := []string{"list_merchants", "search_products", "search_purchases", "where_bought", "spend", "price_history"}
	tools := Tools(Deps{})
	if len(tools) != len(want) {
		t.Fatalf("%d tools", len(tools))
	}
	for i, tl := range tools {
		if tl.Name != want[i] || tl.Description == "" || tl.InputSchema["type"] != "object" {
			t.Errorf("tool %d: %+v", i, tl)
		}
	}
}

func TestMerchantInputsNormalizedAndFuzzy(t *testing.T) {
	fq := &fakeQuery{}
	out, isErr := call(t, fq, "spend", `{"merchant":"TRADER JOE'S","group_by":"month","from":"2026-01-01"}`)
	if isErr || fq.spendF.MerchantID != 7 || fq.spendF.GroupBy != "month" || out["total_cents"] != 4000.0 {
		t.Fatalf("spend %v %+v", out, fq.spendF)
	}
	if ids := out["groups"].([]any)[0].(map[string]any)["receipt_ids"].([]any); len(ids) != 2 {
		t.Fatalf("receipt ids %v", out)
	}
	out, isErr = call(t, fq, "search_purchases", `{"query":"coffee","merchant":"TJ's"}`)
	if !isErr || !strings.Contains(out["error"].(string), `"tjs"`) {
		t.Fatalf("fuzzy %v", out)
	}
	if c := out["details"].(map[string]any)["candidates"].([]any); len(c) != 1 || c[0].(map[string]any)["name"] != "trader_joes" {
		t.Fatalf("candidates %v", out)
	}
	if _, isErr := call(t, fq, "price_history", `{"product_id":1,"merchant":"!!"}`); !isErr {
		t.Fatal("empty merchant key accepted")
	}
	if strings.Join(fq.resolved, ",") != "trader_joes,tjs," {
		t.Fatalf("resolve inputs %q", fq.resolved)
	}
}

func TestToolValidation(t *testing.T) {
	fq := &fakeQuery{}
	for name, args := range map[string]string{
		"search_purchases": `{"query":"x","from":"2026-13-01"}`,
		"spend":            `{"from":"2026-02-01","to":"2026-01-01"}`,
		"where_bought":     `{"product_id":99}`,
		"price_history":    `{}`,
		"search_products":  `{"query":" "}`,
		"list_merchants":   `{"sql":"drop table receipts"}`,
	} {
		if out, isErr := call(t, fq, name, args); !isErr || out["error"] == "" {
			t.Errorf("%s %s accepted: %v", name, args, out)
		}
	}
	if out, isErr := call(t, fq, "spend", `{"group_by":"year"}`); !isErr {
		t.Errorf("group_by year accepted %v", out)
	}
	if out, isErr := call(t, fq, "search_purchases", `{"query":"x","limit":500}`); !isErr {
		t.Errorf("limit 500 accepted %v", out)
	}
}

func TestToolResultsCarryReceiptIDs(t *testing.T) {
	fq := &fakeQuery{lines: []models.PurchaseLine{
		{ReceiptID: 10, PurchaseDate: "2026-01-10", MerchantID: 7, MerchantName: "trader_joes", StoreID: store(3), UnitPriceCents: 1000},
		{ReceiptID: 11, PurchaseDate: "2026-02-01", MerchantID: 7, MerchantName: "trader_joes", StoreID: store(3), UnitPriceCents: 1200},
		{ReceiptID: 12, PurchaseDate: "2026-02-20", MerchantID: 8, MerchantName: "corner_grocer", UnitPriceCents: 1100},
	}}
	out, isErr := call(t, fq, "where_bought", `{"product_id":1}`)
	if isErr {
		t.Fatal(out)
	}
	locs := out["locations"].([]any)
	if len(locs) != 2 {
		t.Fatalf("locations %v", locs)
	}
	l0, l1 := locs[0].(map[string]any), locs[1].(map[string]any)
	if l0["merchant_name"] != "corner_grocer" || l1["purchases"] != 2.0 || l1["last_date"] != "2026-02-01" ||
		l1["last_unit_price_cents"] != 1200.0 || len(l1["receipt_ids"].([]any)) != 2 {
		t.Fatalf("locations %v", locs)
	}
	if ids := out["receipt_ids"].([]any); len(ids) != 3 || ids[0] != 12.0 {
		t.Fatalf("ids %v", ids)
	}
	out, _ = call(t, fq, "price_history", `{"product_id":1,"merchant":"Trader Joe's"}`)
	if len(out["purchases"].([]any)) != 3 || out["merchant"].(map[string]any)["id"] != 7.0 {
		t.Fatalf("history %v", out)
	}
	out, _ = call(t, fq, "search_purchases", `{"query":"coffee","from":"2026-01-01","to":"2026-02-28","limit":2}`)
	if fq.searchF.Limit != 2 || fq.searchF.From != "2026-01-01" || out["truncated"] != true {
		t.Fatalf("search %v %+v", out, fq.searchF)
	}
	out, _ = call(t, fq, "search_products", `{"query":"coff"}`)
	ps := out["products"].([]any)
	if ps[0].(map[string]any)["last_purchase"].(map[string]any)["receipt_id"] != 11.0 || ps[1].(map[string]any)["last_purchase"] != nil {
		t.Fatalf("products %v", ps)
	}
	out, _ = call(t, fq, "list_merchants", `{}`)
	if m := out["merchants"].([]any)[0].(map[string]any); m["last_receipt_id"] != 11.0 || m["last_visit"] != "2026-02-01" {
		t.Fatalf("merchants %v", out)
	}
}

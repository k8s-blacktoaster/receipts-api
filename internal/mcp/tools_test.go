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
	resolved  []string
	spendF    repository.SpendFilter
	searchF   repository.PurchaseFilter
	lines     []models.PurchaseLine
	truncated bool
	wbLimit   int
	scopes    []repository.Scope
}

func (f *fakeQuery) ResolveMerchant(_ context.Context, name string, _ int, sc repository.Scope) (*models.MerchantSummary, []models.MerchantCandidate, error) {
	key := models.NormalizeMerchantName(name)
	f.resolved = append(f.resolved, key)
	f.scopes = append(f.scopes, sc)
	switch key {
	case "":
		return nil, nil, repository.ErrInvalidMerchantName
	case "trader_joes":
		return &models.MerchantSummary{ID: 7, Name: key}, nil, nil
	}
	return nil, []models.MerchantCandidate{{ID: 7, Name: "trader_joes", Similarity: 0.4}}, nil
}
func (f *fakeQuery) ListMerchants(_ context.Context, sc repository.Scope) ([]models.MerchantSummary, error) {
	f.scopes = append(f.scopes, sc)
	return []models.MerchantSummary{{ID: 7, Name: "trader_joes", ReceiptCount: 2, LastVisit: "2026-02-01", LastReceiptID: 11}}, nil
}
func (f *fakeQuery) SearchPurchases(_ context.Context, pf repository.PurchaseFilter) ([]models.PurchaseLine, bool, error) {
	f.searchF = pf
	return f.lines, f.truncated, nil
}
func (f *fakeQuery) PriceHistory(_ context.Context, _, _ int64, _ int, sc repository.Scope) ([]models.PurchaseLine, bool, error) {
	f.scopes = append(f.scopes, sc)
	return f.lines, f.truncated, nil
}
func (f *fakeQuery) WhereBought(_ context.Context, _ int64, limit int, sc repository.Scope) (*models.WhereBought, error) {
	f.wbLimit = limit
	f.scopes = append(f.scopes, sc)
	return &models.WhereBought{Locations: []models.ProductLocation{{MerchantID: 8, MerchantName: "corner_grocer", Purchases: 1,
		LastDate: "2026-02-20", LastUnitPriceCents: 1100, ReceiptIDs: []int64{12}}}, Purchases: f.lines, Truncated: f.truncated}, nil
}
func (f *fakeQuery) Spend(_ context.Context, sf repository.SpendFilter) ([]models.SpendGroup, error) {
	f.spendF = sf
	return []models.SpendGroup{{Key: "trader_joes", ReceiptCount: 2, TotalCents: 4000, ReceiptIDs: []int64{11, 10}}}, nil
}
func (f *fakeQuery) LastPurchases(_ context.Context, _ []int64, sc repository.Scope) (map[int64]models.PurchaseLine, error) {
	f.scopes = append(f.scopes, sc)
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
	out, isErr := call(t, fq, "where_bought", `{"product_id":1,"limit":5}`)
	if isErr || fq.wbLimit != 5 || out["locations"].([]any)[0].(map[string]any)["last_date"] != "2026-02-20" {
		t.Fatalf("where_bought %v", out)
	}
	if ids := out["receipt_ids"].([]any); len(ids) != 3 || ids[0] != 10.0 {
		t.Fatalf("ids %v", ids)
	}
	out, _ = call(t, fq, "price_history", `{"product_id":1,"merchant":"Trader Joe's"}`)
	if len(out["purchases"].([]any)) != 3 || out["merchant"].(map[string]any)["id"] != 7.0 {
		t.Fatalf("history %v", out)
	}
	out, _ = call(t, fq, "search_purchases", `{"query":"coffee","from":"2026-01-01","to":"2026-02-28","limit":3}`)
	if fq.searchF.Limit != 3 || fq.searchF.From != "2026-01-01" || out["truncated"] != false {
		t.Fatalf("search exactly limit rows must not be truncated %v %+v", out, fq.searchF)
	}
	fq.truncated = true
	out, _ = call(t, fq, "search_purchases", `{"query":"coffee","limit":3}`)
	if out["truncated"] != true {
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

func TestArgumentValidation(t *testing.T) {
	fq := &fakeQuery{}
	for name, args := range map[string]string{
		"year 0 from":   `["search_purchases", {"query":"x","from":"0000-01-01"}]`,
		"year 0 to":     `["spend", {"to":"0000-12-31"}]`,
		"NUL query":     `["search_purchases", {"query":"cof\u0000fee"}]`,
		"NUL merchant":  `["spend", {"merchant":"tj\u0000s"}]`,
		"NUL product q": `["search_products", {"query":"\u0000"}]`,
		"case key":      `["search_purchases", {"Query":"x"}]`,
		"case key 2":    `["price_history", {"product_id":1,"MERCHANT":"x"}]`,
		"case group_by": `["spend", {"Group_By":"month"}]`,
		"not object":    `["spend", [1]]`,
	} {
		var pair [2]json.RawMessage
		if err := json.Unmarshal([]byte(args), &pair); err != nil {
			t.Fatal(name, err)
		}
		var tool string
		_ = json.Unmarshal(pair[0], &tool)
		out, isErr := call(t, fq, tool, string(pair[1]))
		if msg, _ := out["error"].(string); !isErr || msg == "" || msg == "internal error" {
			t.Errorf("%s: want validation error, got %v", name, out)
		}
	}
	if out, isErr := call(t, fq, "search_purchases", `{"query":"x","from":"0001-01-01"}`); isErr {
		t.Errorf("year 1 rejected %v", out)
	}
}

// Phase 5.1: needs_review receipts are excluded by default; every tool takes
// include_needs_review to opt in, and reports the statuses it counted.
func TestIncludeNeedsReviewScope(t *testing.T) {
	cases := []struct{ tool, args string }{
		{"list_merchants", `{}`},
		{"search_products", `{"query":"coffee"}`},
		{"search_purchases", `{"query":"coffee","merchant":"Trader Joe's"}`},
		{"where_bought", `{"product_id":1}`},
		{"spend", `{"merchant":"Trader Joe's"}`},
		{"price_history", `{"product_id":1,"merchant":"Trader Joe's"}`},
	}
	for _, c := range cases {
		for _, incl := range []bool{false, true} {
			fq := &fakeQuery{}
			args := c.args
			if incl {
				args = strings.TrimSuffix(args, "}")
				if args != "{" {
					args += ","
				}
				args += `"include_needs_review":true}`
			}
			out, isErr := call(t, fq, c.tool, args)
			if isErr {
				t.Fatalf("%s %s: error %v", c.tool, args, out)
			}
			scopes := append([]repository.Scope{fq.spendF.Scope, fq.searchF.Scope}, fq.scopes...)
			seen := false
			for _, sc := range scopes {
				if sc.IncludeNeedsReview {
					seen = true
				}
			}
			if seen != incl {
				t.Fatalf("%s %s: scopes %+v, want include=%v", c.tool, args, scopes, incl)
			}
			if c.tool != "search_purchases" && c.tool != "spend" && len(fq.scopes) == 0 {
				t.Fatalf("%s: no scoped query", c.tool)
			}
			st, _ := json.Marshal(out["counted_statuses"])
			if strings.Contains(string(st), "needs_review") != incl {
				t.Fatalf("%s include=%v counted_statuses %s", c.tool, incl, st)
			}
		}
		bad := `{"include_needs_review":"yes"}`
		if c.args != `{}` {
			bad = strings.Replace(c.args, "{", `{"include_needs_review":"yes",`, 1)
		}
		if out, isErr := call(t, &fakeQuery{}, c.tool, bad); !isErr {
			t.Fatalf("%s: string include_needs_review accepted: %v", c.tool, out)
		}
	}
}

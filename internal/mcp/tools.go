package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

// Deps are the read-only repositories behind the tools.
type Deps struct {
	Query    repository.QueryRepository
	Products interface {
		SearchProducts(ctx context.Context, q string, limit int) ([]models.Product, error)
		GetProduct(ctx context.Context, id int64) (*models.Product, error)
	}
}

const maxLimit = 200

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var (
	pMerchant = map[string]any{"type": "string", "description": "Merchant name as the user says it (normalized server-side; e.g. \"Trader Joe's\" -> trader_joes). No exact match returns ranked candidates."}
	pFrom     = map[string]any{"type": "string", "format": "date", "description": "Inclusive start date YYYY-MM-DD"}
	pTo       = map[string]any{"type": "string", "format": "date", "description": "Inclusive end date YYYY-MM-DD"}
	pLimit    = map[string]any{"type": "integer", "minimum": 1, "maximum": maxLimit, "description": "Max rows (default 50)"}
	pProduct  = map[string]any{"type": "integer", "minimum": 1, "description": "Product id (from search_products)"}
)

// Tools returns the read-only tool set (MCP-01, MCP-05).
func Tools(d Deps) []Tool {
	return []Tool{
		{Name: "list_merchants", Description: "List merchants (normalized names) with counted receipt count, total spent (cents), last visit date and last receipt id.",
			InputSchema: obj(map[string]any{}), Call: d.listMerchants},
		{Name: "search_products", Description: "Find canonical products by name, alias text or UPC. Each hit includes its most recent purchase (receipt id + date) when there is one.",
			InputSchema: obj(map[string]any{"query": map[string]any{"type": "string", "minLength": 1}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}}, "query"), Call: d.searchProducts},
		{Name: "search_purchases", Description: "Search purchased line items by text (item name, printed text or mapped product name), optionally by merchant and date range. Newest first, with receipt ids and dates.",
			InputSchema: obj(map[string]any{"query": map[string]any{"type": "string", "minLength": 1}, "merchant": pMerchant, "from": pFrom, "to": pTo, "limit": pLimit}, "query"), Call: d.searchPurchases},
		{Name: "where_bought", Description: "Where did I buy a product: each merchant + store location with last purchase date/price and receipt ids, plus the individual purchases newest first.",
			InputSchema: obj(map[string]any{"product_id": pProduct, "limit": pLimit}, "product_id"), Call: d.whereBought},
		{Name: "spend", Description: "Total spend (receipt totals, cents) over an optional merchant and date range, grouped by merchant or month, with the receipt ids in each group.",
			InputSchema: obj(map[string]any{"merchant": pMerchant, "from": pFrom, "to": pTo, "group_by": map[string]any{"type": "string", "enum": []string{"merchant", "month"}}}), Call: d.spend},
		{Name: "price_history", Description: "Unit price paid for a product over time (oldest first), optionally at one merchant, with receipt ids and dates.",
			InputSchema: obj(map[string]any{"product_id": pProduct, "merchant": pMerchant, "limit": pLimit}, "product_id"), Call: d.priceHistory},
	}
}

func bad(format string, a ...any) error { return &ToolError{Message: fmt.Sprintf(format, a...)} }

func decodeArgs(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return bad("invalid arguments: %v", err)
	}
	return nil
}

func checkDate(name, s string) error {
	if s == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return bad("%s must be YYYY-MM-DD", name)
	}
	return nil
}

func checkRange(from, to string) error {
	if err := checkDate("from", from); err != nil {
		return err
	}
	if err := checkDate("to", to); err != nil {
		return err
	}
	if from != "" && to != "" && from > to {
		return bad("from is after to")
	}
	return nil
}

func checkLimit(n, max int) (int, error) {
	if n < 0 || n > max {
		return 0, bad("limit must be between 1 and %d", max)
	}
	if n == 0 {
		n = 50
	}
	return n, nil
}

// merchantID resolves an optional merchant argument (MCP-04/06). Empty -> 0 (any).
func (d Deps) merchantID(ctx context.Context, name string) (int64, *models.MerchantSummary, error) {
	if strings.TrimSpace(name) == "" {
		return 0, nil, nil
	}
	m, cands, err := d.Query.ResolveMerchant(ctx, name, 5)
	if errors.Is(err, repository.ErrInvalidMerchantName) {
		return 0, nil, bad("merchant name must contain a letter or digit")
	}
	if err != nil {
		return 0, nil, err
	}
	if m == nil {
		return 0, nil, &ToolError{Message: fmt.Sprintf("no merchant named %q (normalized %q); pick one of the candidates or call list_merchants",
			name, models.NormalizeMerchantName(name)), Details: map[string]any{"candidates": cands}}
	}
	return m.ID, m, nil
}

func (d Deps) listMerchants(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := decodeArgs(raw, &struct{}{}); err != nil {
		return nil, err
	}
	ms, err := d.Query.ListMerchants(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"merchants": ms, "counted_statuses": repository.CountedStatuses}, nil
}

type productHit struct {
	models.Product
	LastPurchase *models.PurchaseLine `json:"last_purchase,omitempty"`
}

func (d Deps) searchProducts(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Query) == "" {
		return nil, bad("query is required")
	}
	limit, err := checkLimit(a.Limit, 50)
	if err != nil {
		return nil, err
	}
	ps, err := d.Products.SearchProducts(ctx, a.Query, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	last, err := d.Query.LastPurchases(ctx, ids)
	if err != nil {
		return nil, err
	}
	hits := make([]productHit, len(ps))
	for i, p := range ps {
		hits[i].Product = p
		if l, ok := last[p.ID]; ok {
			hits[i].LastPurchase = &l
		}
	}
	return map[string]any{"products": hits}, nil
}

func linesResult(key string, lines []models.PurchaseLine, truncated bool, extra map[string]any) map[string]any {
	out := map[string]any{key: lines, "truncated": truncated, "receipt_ids": receiptIDs(lines)}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func receiptIDs(lines []models.PurchaseLine) []int64 {
	ids := []int64{}
	for _, l := range lines {
		if !slices.Contains(ids, l.ReceiptID) {
			ids = append(ids, l.ReceiptID)
		}
	}
	return ids
}

func (d Deps) searchPurchases(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Query    string `json:"query"`
		Merchant string `json:"merchant"`
		From     string `json:"from"`
		To       string `json:"to"`
		Limit    int    `json:"limit"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Query) == "" {
		return nil, bad("query is required")
	}
	if err := checkRange(a.From, a.To); err != nil {
		return nil, err
	}
	limit, err := checkLimit(a.Limit, maxLimit)
	if err != nil {
		return nil, err
	}
	mid, m, err := d.merchantID(ctx, a.Merchant)
	if err != nil {
		return nil, err
	}
	lines, truncated, err := d.Query.SearchPurchases(ctx, repository.PurchaseFilter{Query: a.Query, MerchantID: mid, From: a.From, To: a.To, Limit: limit})
	if err != nil {
		return nil, err
	}
	return linesResult("purchases", lines, truncated, map[string]any{"merchant": m}), nil
}

func (d Deps) product(ctx context.Context, id int64) (*models.Product, error) {
	if id <= 0 {
		return nil, bad("product_id is required")
	}
	p, err := d.Products.GetProduct(ctx, id)
	if errors.Is(err, repository.ErrProductNotFound) {
		return nil, bad("no product with id %d; use search_products", id)
	}
	return p, err
}

func (d Deps) whereBought(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		ProductID int64 `json:"product_id"`
		Limit     int   `json:"limit"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	limit, err := checkLimit(a.Limit, maxLimit)
	if err != nil {
		return nil, err
	}
	p, err := d.product(ctx, a.ProductID)
	if err != nil {
		return nil, err
	}
	wb, err := d.Query.WhereBought(ctx, p.ID, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"product": p, "locations": wb.Locations, "purchases": wb.Purchases,
		"truncated": wb.Truncated, "receipt_ids": receiptIDs(wb.Purchases)}, nil
}

func (d Deps) spend(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Merchant string `json:"merchant"`
		From     string `json:"from"`
		To       string `json:"to"`
		GroupBy  string `json:"group_by"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkRange(a.From, a.To); err != nil {
		return nil, err
	}
	if a.GroupBy != "" && a.GroupBy != "merchant" && a.GroupBy != "month" {
		return nil, bad(`group_by must be "merchant" or "month"`)
	}
	mid, m, err := d.merchantID(ctx, a.Merchant)
	if err != nil {
		return nil, err
	}
	groups, err := d.Query.Spend(ctx, repository.SpendFilter{MerchantID: mid, From: a.From, To: a.To, GroupBy: a.GroupBy})
	if err != nil {
		return nil, err
	}
	var total int64
	n := 0
	for _, g := range groups {
		total += g.TotalCents
		n += g.ReceiptCount
	}
	return map[string]any{"merchant": m, "from": a.From, "to": a.To, "group_by": cmpOr(a.GroupBy, "merchant"),
		"total_cents": total, "receipt_count": n, "groups": groups, "counted_statuses": repository.CountedStatuses}, nil
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (d Deps) priceHistory(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		ProductID int64  `json:"product_id"`
		Merchant  string `json:"merchant"`
		Limit     int    `json:"limit"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	limit, err := checkLimit(a.Limit, maxLimit)
	if err != nil {
		return nil, err
	}
	p, err := d.product(ctx, a.ProductID)
	if err != nil {
		return nil, err
	}
	mid, m, err := d.merchantID(ctx, a.Merchant)
	if err != nil {
		return nil, err
	}
	lines, truncated, err := d.Query.PriceHistory(ctx, p.ID, mid, limit)
	if err != nil {
		return nil, err
	}
	return linesResult("purchases", lines, truncated, map[string]any{"product": p, "merchant": m}), nil
}

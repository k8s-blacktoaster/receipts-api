package extraction

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"receipts-db/internal/models"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeXAI is an httptest stand-in for api.x.ai; it records the last request.
type fakeXAI struct {
	mu      sync.Mutex
	status  int
	content string
	lastReq map[string]any
	auth    string
	path    string
}

func (f *fakeXAI) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth, f.path = r.Header.Get("Authorization"), r.URL.Path
	body, _ := io.ReadAll(r.Body)
	f.lastReq = map[string]any{}
	_ = json.Unmarshal(body, &f.lastReq)
	w.Header().Set("Content-Type", "application/json")
	if f.status != 0 && f.status != 200 {
		w.WriteHeader(f.status)
		if f.status == 401 { // a misbehaving upstream echoing credentials back
			_, _ = w.Write([]byte(`{"error":{"message":"invalid key ` + strings.TrimPrefix(f.auth, "Bearer ") + `; Authorization: ` + f.auth + `"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"error":{"message":"upstream exploded"}}`))
		return
	}
	resp := map[string]any{"model": "grok-test-model", "choices": []any{map[string]any{
		"message": map[string]any{"role": "assistant", "content": f.content}, "finish_reason": "stop"}}}
	_ = json.NewEncoder(w).Encode(resp)
}

func newFake(t *testing.T, content string, status int) (*fakeXAI, *XAIProvider) {
	t.Helper()
	f := &fakeXAI{content: content, status: status}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	p, err := NewXAIProvider(srv.URL+"/v1", "test-key-not-real", "grok-test", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return f, p
}

var jpeg = []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F'}

func TestXAIRequestShape(t *testing.T) {
	f, p := newFake(t, fixture(t, "receipt_ok.json"), 200)
	if _, err := p.Extract(context.Background(), []Image{{MIME: "image/jpeg", Data: jpeg}}); err != nil {
		t.Fatal(err)
	}
	if f.path != "/v1/chat/completions" || f.auth != "Bearer test-key-not-real" {
		t.Fatalf("path=%q auth=%q", f.path, f.auth)
	}
	if f.lastReq["model"] != "grok-test" || f.lastReq["temperature"].(float64) != 0 {
		t.Fatalf("model/temperature: %v %v", f.lastReq["model"], f.lastReq["temperature"])
	}
	rf := f.lastReq["response_format"].(map[string]any)
	js := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["strict"] != true || js["name"] != "receipt" {
		t.Fatalf("response_format: %v", rf)
	}
	schema := js["schema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	for _, k := range []string{"merchant_name", "subtotal_cents", "tax_cents", "total_cents", "savings_cents", "items", "adjustments"} {
		if _, ok := props[k]; !ok {
			t.Fatalf("schema missing %s", k)
		}
	}
	itemProps := props["items"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	for _, k := range []string{"sku", "item_number", "upc", "unit_price_cents", "discount_cents"} {
		if _, ok := itemProps[k]; !ok {
			t.Fatalf("item schema missing %s", k)
		}
	}
	msgs := f.lastReq["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Fatal("first message must be system")
	}
	parts := msgs[1].(map[string]any)["content"].([]any)
	img := parts[1].(map[string]any)
	url := img["image_url"].(map[string]any)["url"].(string)
	if img["type"] != "image_url" || url != "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(jpeg) {
		t.Fatalf("image part: %v", img)
	}
}

func TestXAIParsesResponse(t *testing.T) {
	_, p := newFake(t, fixture(t, "receipt_ok.json"), 200)
	res, err := p.Extract(context.Background(), []Image{{MIME: "image/jpeg", Data: jpeg}})
	if err != nil {
		t.Fatal(err)
	}
	e := res.Receipt
	if res.Model != "grok-test-model" || *e.MerchantName != "Sprouts Farmers Market" || *e.TotalCents != 1963 || len(e.Items) != 3 {
		t.Fatalf("parsed: model=%s %+v", res.Model, e)
	}
	if *e.Items[1].UPC != "0071260002015" || *e.Items[0].ItemNumber != "4129" || *e.Items[0].Quantity != 1.52 {
		t.Fatalf("items: %+v", e.Items)
	}
	if !json.Valid(res.Raw) {
		t.Fatal("raw must be valid JSON")
	}
}

func TestXAIErrors(t *testing.T) {
	_, p := newFake(t, "", 500)
	_, err := p.Extract(context.Background(), []Image{{MIME: "image/jpeg", Data: jpeg}})
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("want 500 error, got %v", err)
	}
	if strings.Contains(err.Error(), "test-key-not-real") {
		t.Fatal("error leaks API key")
	}
	_, p = newFake(t, "not json", 200)
	if _, err := p.Extract(context.Background(), []Image{{MIME: "image/jpeg", Data: jpeg}}); err == nil {
		t.Fatal("want invalid JSON error")
	}
	if _, err := p.Extract(context.Background(), nil); err == nil {
		t.Fatal("want no-images error")
	}
}

func TestProviderSelection(t *testing.T) {
	if _, err := NewProvider(Config{Provider: "xai"}); err == nil {
		t.Fatal("xai without key must error")
	}
	p, err := NewProvider(Config{XAIAPIKey: "k"}) // default provider
	if err != nil || p.Name() != "xai" || p.Model() != DefaultXAIModel {
		t.Fatalf("default: %v %v", p, err)
	}
	p, _ = NewProvider(Config{Provider: "xai", XAIAPIKey: "k", Model: "grok-x"})
	if p.Model() != "grok-x" {
		t.Fatalf("model override: %s", p.Model())
	}
	p, err = NewProvider(Config{Provider: "Anthropic", Model: "claude-x"})
	if err != nil || p.Name() != "anthropic" {
		t.Fatalf("anthropic: %v %v", p, err)
	}
	if p, err := NewProvider(Config{Provider: "none"}); p != nil || err != nil {
		t.Fatalf("none: %v %v", p, err)
	}
	if _, err := NewProvider(Config{Provider: "bogus"}); err == nil {
		t.Fatal("unknown provider must error")
	}
}

func TestAnthropicStub(t *testing.T) {
	_, err := NewAnthropicProvider("m").Extract(context.Background(), []Image{{MIME: "image/jpeg", Data: jpeg}})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("got %v", err)
	}
}

func parse(t *testing.T, name string) *Extracted {
	t.Helper()
	var e Extracted
	if err := json.Unmarshal([]byte(fixture(t, name)), &e); err != nil {
		t.Fatal(err)
	}
	return &e
}

func TestApplyMapping(t *testing.T) {
	doc := int64(7)
	r := &models.Receipt{ID: 5, Source: models.SourcePhoto, SourceDocumentID: &doc}
	Apply(r, parse(t, "receipt_ok.json"))
	if r.StoreAddress != "27769 Santa Margarita Parkway" || r.StoreCity != "Mission Viejo" || r.StoreState != "CA" ||
		r.StoreZip != "92691" || r.StorePhone == "" {
		t.Fatalf("store location: %q %q %q %q %q", r.StoreAddress, r.StoreCity, r.StoreState, r.StoreZip, r.StorePhone)
	}
	if r.StoreName != "Sprouts Farmers Market" || r.StoreNumber != "0412" || r.Date != "2025-08-10" || r.Currency != "USD" {
		t.Fatalf("header: %+v", r)
	}
	if r.SubtotalCents != 2165 || r.TaxCents != 98 || r.TotalCents != 1963 || r.SavingsCents != 300 || r.PaymentLast4 != "1234" {
		t.Fatalf("money: %+v", r)
	}
	if r.PurchasedAt == nil || r.PurchasedAt.Hour() != 17 || r.Confidence == nil || *r.Confidence != 0.93 {
		t.Fatalf("time/confidence: %v %v", r.PurchasedAt, r.Confidence)
	}
	if r.ID != 5 || *r.SourceDocumentID != 7 || r.Source != models.SourcePhoto {
		t.Fatal("provenance must be preserved")
	}
	it := r.Items
	if len(it) != 3 || it[0].ItemNumber != "4129" || it[0].Quantity != 1.52 || it[0].Unit != "lb" ||
		it[1].SKU != "SPR-MILK2" || it[1].UPC != "0071260002015" || it[1].DiscountCents != 100 {
		t.Fatalf("items: %+v", it)
	}
	if len(r.Adjustments) != 1 || r.Adjustments[0].Kind != "coupon" || r.Adjustments[0].AmountCents != -200 {
		t.Fatalf("adjustment sign must be negative: %+v", r.Adjustments)
	}
}

func TestCheck(t *testing.T) {
	r := &models.Receipt{}
	e := parse(t, "receipt_ok.json")
	Apply(r, e)
	if reasons := Check(r, e, 2); len(reasons) != 0 {
		t.Fatalf("ok receipt flagged: %v", reasons)
	}
	// within tolerance
	r.TotalCents += 2
	if reasons := Check(r, e, 2); len(reasons) != 0 {
		t.Fatalf("tolerance: %v", reasons)
	}
	r.TotalCents += 1
	if reasons := Check(r, e, 2); len(reasons) != 1 || !strings.Contains(reasons[0], "total mismatch") {
		t.Fatalf("mismatch: %v", reasons)
	}
	m := &models.Receipt{}
	em := parse(t, "receipt_mismatch.json")
	Apply(m, em)
	if reasons := Check(m, em, 2); len(reasons) != 1 || !strings.Contains(reasons[0], "printed total 1500") {
		t.Fatalf("mismatch fixture: %v", reasons)
	}
	// missing required fields
	empty := &Extracted{}
	er := &models.Receipt{}
	Apply(er, empty)
	reasons := strings.Join(Check(er, empty, 2), "|")
	for _, want := range []string{"missing merchant", "missing or invalid purchase date", "missing total", "missing subtotal", "no line items"} {
		if !strings.Contains(reasons, want) {
			t.Fatalf("want %q in %q", want, reasons)
		}
	}
}

func TestXAIErrorRedactsEchoedKey(t *testing.T) {
	_, p := newFake(t, "", 401)
	_, err := p.Extract(context.Background(), []Image{{MIME: "image/jpeg", Data: jpeg}})
	if err == nil || strings.Contains(err.Error(), "test-key-not-real") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("key not redacted: %v", err)
	}
}

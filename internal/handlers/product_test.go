package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

type fakeProducts struct {
	repository.ProductRepository
	created []models.Product
	mapped  [][2]int64
	mapErr  error
}

func (f *fakeProducts) CreateProduct(_ context.Context, p *models.Product) error {
	p.ID = int64(100 + len(f.created))
	f.created = append(f.created, *p)
	return nil
}

func (f *fakeProducts) MapItem(ctx context.Context, item, product int64, np *models.Product) (*models.MapResult, error) {
	if f.mapErr != nil {
		return nil, f.mapErr
	}
	if np != nil {
		_ = f.CreateProduct(ctx, np)
		product = np.ID
	}
	f.mapped = append(f.mapped, [2]int64{item, product})
	return &models.MapResult{ItemID: item, ProductID: product, AliasID: 1, AlsoMapped: 2}, nil
}

func (f *fakeProducts) ListReviewItems(_ context.Context, limit, offset int) ([]models.ReviewItem, int, error) {
	return []models.ReviewItem{{ItemID: 1, Name: "x"}}, 7, nil
}

func (f *fakeProducts) SearchProducts(_ context.Context, q string, limit int) ([]models.Product, error) {
	return []models.Product{{ID: 1, CanonicalName: q}}, nil
}

func productRouter(f *fakeProducts) http.Handler {
	h := &ProductHandler{Products: f, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := chi.NewRouter()
	r.Get("/products", h.SearchProducts)
	r.Post("/products", h.CreateProduct)
	r.Get("/review", h.ListReviewItems)
	r.Post("/review/{id}/map", h.MapItem)
	return r
}

func TestMapItemHandler(t *testing.T) {
	cases := []struct {
		path, body string
		err        error
		code       int
	}{
		{"/review/5/map", `{"product_id":9}`, nil, 200},
		{"/review/5/map", `{"new_product":{"canonical_name":"Bananas"}}`, nil, 200},
		{"/review/5/map", `{}`, nil, 400},
		{"/review/5/map", `{"product_id":9,"new_product":{"canonical_name":"x"}}`, nil, 400},
		{"/review/5/map", `{"product_id":-1}`, nil, 400},
		{"/review/5/map", `{"new_product":{"canonical_name":"  "}}`, nil, 400},
		{"/review/5/map", `{"new_product":{"canonical_name":"` + strings.Repeat("a", 300) + `"}}`, nil, 400},
		{"/review/abc/map", `{"product_id":9}`, nil, 400},
		{"/review/5/map", `not json`, nil, 400},
		{"/review/5/map", `{"product_id":9}`, repository.ErrReceiptBusy, 409},
		{"/review/5/map", `{"product_id":9}`, repository.ErrNotFound, 404},
		{"/review/5/map", `{"product_id":9}`, repository.ErrProductNotFound, 404},
		{"/review/5/map", `{"product_id":9}`, repository.ErrConcurrentUpdate, 409},
		{"/review/5/map", `{"product_id":9}`, context.DeadlineExceeded, 500},
	}
	for _, c := range cases {
		f := &fakeProducts{mapErr: c.err}
		rec := httptest.NewRecorder()
		productRouter(f).ServeHTTP(rec, httptest.NewRequest("POST", c.path, strings.NewReader(c.body)))
		if rec.Code != c.code {
			t.Errorf("%s %s: %d want %d (%s)", c.path, c.body, rec.Code, c.code, rec.Body)
		}
	}
	f := &fakeProducts{}
	rec := httptest.NewRecorder()
	productRouter(f).ServeHTTP(rec, httptest.NewRequest("POST", "/review/5/map", strings.NewReader(`{"new_product":{"canonical_name":" Bananas "}}`)))
	if len(f.created) != 1 || f.created[0].CanonicalName != "Bananas" || f.mapped[0] != [2]int64{5, 100} ||
		!strings.Contains(rec.Body.String(), `"also_mapped":2`) {
		t.Fatalf("created=%v mapped=%v body=%s", f.created, f.mapped, rec.Body)
	}
}

func TestProductAndQueueEndpoints(t *testing.T) {
	f := &fakeProducts{}
	for _, c := range []struct {
		method, path, body string
		code               int
		want               string
	}{
		{"GET", "/products?q=milk", "", 200, `"canonical_name":"milk"`},
		{"POST", "/products", `{"canonical_name":"Milk","upc":"012345678905"}`, 201, `"id":100`},
		{"POST", "/products", `{"brand":"x"}`, 400, "canonical_name"},
		{"GET", "/review?limit=999", "", 200, `"total":7`},
	} {
		rec := httptest.NewRecorder()
		productRouter(f).ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestUPCRejectedAtAPI(t *testing.T) {
	rec := httptest.NewRecorder()
	productRouter(&fakeProducts{}).ServeHTTP(rec, httptest.NewRequest("POST", "/products", strings.NewReader(`{"canonical_name":"Milk","upc":"123"}`)))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "8, 12, 13 or 14") {
		t.Fatalf("product: %d %s", rec.Code, rec.Body)
	}
	h := NewReceiptHandler(&statusRepo{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec = httptest.NewRecorder()
	h.CreateReceipt(rec, httptest.NewRequest("POST", "/api/v1/receipts", strings.NewReader(
		`{"store_name":"Acme","date":"2026-01-02","items":[{"name":"x","quantity":1,"upc":"12ab5678"}]}`)))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "UPC") {
		t.Fatalf("receipt: %d %s", rec.Code, rec.Body)
	}
}

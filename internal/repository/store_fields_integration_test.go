package repository_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"receipts-db/internal/handlers"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

// PR #9 QA major 1: since 014, store_category lives on the shared merchant and store_phone on
// the store row. A user write that cannot be stored is a 422 and changes nothing; it is never
// a 200 that silently drops the value.
func TestStoreFieldsNeverSilentlyDropped(t *testing.T) {
	db := repository.TestDB(t)
	ctx := context.Background()
	receipts := repository.NewReceiptRepository(db)
	rh := handlers.NewReceiptHandler(receipts, slog.Default())
	r := chi.NewRouter()
	r.Post("/api/v1/receipts", rh.CreateReceipt)
	r.Put("/api/v1/receipts/{id}", rh.UpdateReceipt)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		return rec
	}

	base := &models.Receipt{StoreName: "Elm Market", StoreCategory: "grocery", Date: "2026-03-01"}
	if err := receipts.Create(ctx, base); err != nil {
		t.Fatal(err)
	}
	put := func(fields string) *httptest.ResponseRecorder {
		return call(http.MethodPut, fmt.Sprintf("/api/v1/receipts/%d", base.ID), `{"store_name":"Elm Market","date":"2026-03-01"`+fields+`}`)
	}
	for name, c := range map[string]struct {
		fields string
		want   int
	}{
		"different category":           {`,"store_category":"pharmacy"`, 422},
		"phone without store location": {`,"store_phone":"555-0100"`, 422},
		"same category, other case":    {`,"store_category":" Grocery "`, 200},
		"phone with address":           {`,"store_address":"9 Elm","store_phone":"555-0100"`, 200},
		"category omitted":             {``, 200},
	} {
		if rec := put(c.fields); rec.Code != c.want {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		} else if c.want == 422 && !strings.Contains(rec.Body.String(), "unchanged") {
			t.Fatalf("%s: 422 body %s", name, rec.Body)
		}
	}
	got, err := receipts.GetByID(ctx, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StoreCategory != "grocery" || got.StoreAddress != "" && got.StorePhone == "" {
		t.Fatalf("after edits %+v", got)
	}
	// the last PUT omitted the address, so the receipt has no store; the earlier one stored the phone
	var phone string
	if err := db.QueryRow(`SELECT phone FROM stores WHERE merchant_id = $1 AND address = '9 Elm'`, base.MerchantID).Scan(&phone); err != nil || phone != "555-0100" {
		t.Fatalf("phone not written through: %q %v", phone, err)
	}

	// create: same rules
	if rec := call(http.MethodPost, "/api/v1/receipts", `{"store_name":"Elm Market","date":"2026-03-02","store_category":"pharmacy"}`); rec.Code != 422 {
		t.Fatalf("create different category: %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodPost, "/api/v1/receipts", `{"store_name":"Oak Deli","date":"2026-03-02","store_phone":"555-0111"}`); rec.Code != 422 {
		t.Fatalf("create phone without location: %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodPost, "/api/v1/receipts", `{"date":"2026-03-02","store_category":"deli"}`); rec.Code != 422 && rec.Code != 400 {
		t.Fatalf("create category without merchant: %d %s", rec.Code, rec.Body)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM merchants WHERE name = 'oak_deli'`).Scan(&n)
	if n != 0 {
		t.Fatal("rejected create left a merchant behind")
	}
	// a merchant without a category gets it written through
	if rec := call(http.MethodPost, "/api/v1/receipts", `{"store_name":"Pine Shop","date":"2026-03-03"}`); rec.Code != 201 {
		t.Fatalf("create pine: %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodPost, "/api/v1/receipts", `{"store_name":"Pine Shop","date":"2026-03-04","store_category":"hardware"}`); rec.Code != 201 {
		t.Fatalf("create pine category: %d %s", rec.Code, rec.Body)
	}
	var cat string
	db.QueryRow(`SELECT category FROM merchants WHERE name = 'pine_shop'`).Scan(&cat)
	if cat != "hardware" {
		t.Fatalf("category not written through: %q", cat)
	}
}

// Extraction stays lenient: a phone with no store location is dropped and a differing
// category never overrides the merchant's (no 422 can fail a parse).
func TestStoreFieldsExtractionLenient(t *testing.T) {
	db := repository.TestDB(t)
	ctx := context.Background()
	receipts := repository.NewReceiptRepository(db)
	rc := &models.Receipt{StoreName: "Elm Market", StoreCategory: "grocery", Date: "2026-03-01"}
	if err := receipts.Create(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if err := repository.WriteReceiptLenientForTest(ctx, db, &models.Receipt{ID: rc.ID, StoreName: "Elm Market", StoreCategory: "pharmacy",
		StorePhone: "555-0100", Date: "2026-03-01", Status: models.StatusParsed}); err != nil {
		t.Fatalf("lenient write: %v", err)
	}
	got, _ := receipts.GetByID(ctx, rc.ID)
	if got.StoreCategory != "grocery" || got.StorePhone != "" {
		t.Fatalf("lenient %+v", got)
	}
}

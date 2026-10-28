package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"receipts-db/internal/models"
)

// Merge and review-map run in parallel on the same merchant: consistent lock order plus the
// bounded 40P01/40001 retry means neither ever fails with an unexpected (500-class) error.
func TestMergeAndMapConcurrently(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	products, receipts, merchants := NewProductRepository(db), NewReceiptRepository(db), NewMerchantRepository(db)
	p := &models.Product{CanonicalName: "Thing"}
	if err := products.CreateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	ok := func(err error) bool {
		return err == nil || errors.Is(err, ErrConcurrentUpdate) // 409, never 500
	}
	const rounds = 30
	bad := 0
	for i := 0; i < rounds; i++ {
		mk := func(name string) *models.Receipt {
			r := &models.Receipt{StoreName: name, Date: "2026-06-01", Source: models.SourceManual}
			for j := 0; j < 5; j++ {
				r.Items = append(r.Items, models.ReceiptItem{Name: fmt.Sprintf("THING %d", j%2), ItemNumber: fmt.Sprint(j % 2), Quantity: 1})
			}
			if err := receipts.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
			return r
		}
		from, into := mk(fmt.Sprintf("From %d", i)), mk(fmt.Sprintf("Into %d", i))
		// alternate which merchant has the lower id so both lock orders of Merge are exercised
		if i%2 == 1 {
			from, into = into, from
		}
		var wg sync.WaitGroup
		errs := make([]error, 4)
		wg.Add(4)
		go func() { defer wg.Done(); _, errs[0] = merchants.Merge(ctx, from.MerchantID, into.MerchantID) }()
		go func() { defer wg.Done(); _, errs[1] = products.MapItem(ctx, int64(from.Items[0].ID), p.ID, nil) }()
		go func() { defer wg.Done(); _, errs[2] = products.MapItem(ctx, int64(into.Items[1].ID), p.ID, nil) }()
		go func() {
			defer wg.Done()
			_, errs[3] = products.MapItem(ctx, int64(from.Items[3].ID), 0, &models.Product{CanonicalName: fmt.Sprint("New ", i)})
		}()
		wg.Wait()
		for k, err := range errs {
			if !ok(err) {
				bad++
				t.Errorf("round %d op %d: %v", i, k, err)
			}
		}
	}
	if bad != 0 {
		t.Fatalf("%d/%d operations failed unexpectedly", bad, rounds*4)
	}
}

func TestRetryClassification(t *testing.T) {
	calls := 0
	err := withTxRetry(context.Background(), func() error { calls++; return errRetry })
	if !errors.Is(err, ErrConcurrentUpdate) || calls != txRetries {
		t.Fatalf("exhausted: %v after %d", err, calls)
	}
	calls = 0
	if err := withTxRetry(context.Background(), func() error {
		calls++
		if calls < 2 {
			return errRetry
		}
		return nil
	}); err != nil || calls != 2 {
		t.Fatalf("recover: %v %d", err, calls)
	}
	if err := withTxRetry(context.Background(), func() error { return ErrNotFound }); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

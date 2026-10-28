package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"receipts-db/internal/extraction"
	"receipts-db/internal/models"
	"receipts-db/internal/storage"
)

type fixtureProvider struct {
	file string
	err  error
}

func (f fixtureProvider) Name() string  { return "fixture" }
func (f fixtureProvider) Model() string { return "fixture-1" }
func (f fixtureProvider) Extract(context.Context, []extraction.Image) (*extraction.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	raw, err := os.ReadFile("../extraction/testdata/" + f.file)
	if err != nil {
		return nil, err
	}
	var e extraction.Extracted
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	return &extraction.Result{Receipt: &e, Raw: raw, Model: "fixture-1"}, nil
}

func uploadFixture(t *testing.T, docs SourceDocumentRepository, blobs storage.BlobStore, data []byte) *models.Receipt {
	t.Helper()
	sum := sha256.Sum256(data)
	key := "receipts/test/" + string(rune('a'+data[0]%26)) + ".jpg"
	if err := blobs.Put(context.Background(), key, data); err != nil {
		t.Fatal(err)
	}
	rc := &models.Receipt{Source: models.SourcePhoto, Status: models.StatusUploaded}
	if err := docs.CreateUpload(context.Background(), &models.SourceDocument{ObjectKey: key, SHA256: sum[:], MimeType: "image/jpeg"}, rc); err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestPipelineIntegration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	receipts, docs, blobs := NewReceiptRepository(db), NewSourceDocumentRepository(db), storage.NewMemory()
	run := func(p extraction.Provider, data []byte) (*models.Receipt, *extraction.Outcome) {
		t.Helper()
		rc := uploadFixture(t, docs, blobs, data)
		got, _ := receipts.GetByID(ctx, rc.ID)
		if got.Status != models.StatusUploaded || got.MerchantID != 0 || got.Date != "" {
			t.Fatalf("pending receipt: %+v", got)
		}
		pl := &extraction.Pipeline{Receipts: receipts, Docs: docs, Blobs: blobs, Provider: p}
		out, err := pl.Process(ctx, rc.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, err = receipts.GetByID(ctx, rc.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got, out
	}

	// parsed
	r, out := run(fixtureProvider{file: "receipt_ok.json"}, []byte{1, 2, 3})
	if out.Status != "parsed" || r.Status != "parsed" || r.ParseError != "" || r.ParsedAt == nil || r.ParseStartedAt == nil {
		t.Fatalf("parsed: %+v %+v", out, r)
	}
	var merchant string
	db.QueryRow(`SELECT name FROM merchants WHERE id = $1`, r.MerchantID).Scan(&merchant)
	if merchant != "sprouts_farmers_market" || r.StoreName != "Sprouts Farmers Market" || r.TotalCents != 1963 ||
		len(r.Items) != 3 || r.Items[1].UPC != "0071260002015" || r.Items[0].ItemNumber != "4129" ||
		len(r.Adjustments) != 1 || r.Adjustments[0].AmountCents != -200 || r.StoreID == nil {
		t.Fatalf("mapped: merchant=%q %+v", merchant, r)
	}
	doc, _ := docs.GetByID(ctx, *r.SourceDocumentID)
	if doc.ParseState != "parsed" || doc.Model != "fixture-1" || doc.PromptVersion != extraction.PromptVersion || len(doc.RawExtraction) == 0 || doc.ParsedAt == nil {
		t.Fatalf("doc: %+v", doc)
	}

	// needs_review on totals mismatch
	r, _ = run(fixtureProvider{file: "receipt_mismatch.json"}, []byte{4, 5, 6})
	if r.Status != "needs_review" || r.ParseError == "" || r.ParsedAt == nil {
		t.Fatalf("needs_review: %+v", r)
	}

	// failed on provider error
	r, _ = run(fixtureProvider{err: errors.New("xai returned 503: overloaded")}, []byte{7, 8, 9})
	if r.Status != "failed" || r.ParseError == "" || r.MerchantID != 0 {
		t.Fatalf("failed: %+v", r)
	}
	doc, _ = docs.GetByID(ctx, *r.SourceDocumentID)
	if doc.ParseState != "failed" || doc.Error == "" {
		t.Fatalf("failed doc: %+v", doc)
	}

	// pending list for worker recovery
	ids, err := receipts.ListIDsByStatus(ctx, "uploaded", "parsing")
	if err != nil || len(ids) != 0 {
		t.Fatalf("pending: %v %v", ids, err)
	}
}

func TestUploadDedupeAndPendingCheck(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	docs, blobs := NewSourceDocumentRepository(db), storage.NewMemory()
	rc := uploadFixture(t, docs, blobs, []byte{42})
	sum := sha256.Sum256([]byte{42})
	rid, did, status, found, err := docs.FindReceiptBySHA(ctx, sum[:])
	if err != nil || !found || rid != rc.ID || did != *rc.SourceDocumentID || status != "uploaded" {
		t.Fatalf("dedupe lookup: %d %d %s %v %v", rid, did, status, found, err)
	}
	// identical sha256 cannot be inserted twice
	if err := docs.CreateUpload(ctx, &models.SourceDocument{SHA256: sum[:], MimeType: "image/jpeg"},
		&models.Receipt{Source: models.SourcePhoto, Status: models.StatusUploaded}); err == nil {
		t.Fatal("duplicate sha256 must fail")
	}
	// a parsed receipt must have merchant and date (007 CHECK)
	mustExecErr(t, db, "receipts_complete_when_parsed", `UPDATE receipts SET status = 'parsed' WHERE id = $1`, rc.ID)
	ids, _ := NewReceiptRepository(db).ListIDsByStatus(ctx, "uploaded", "parsing")
	if len(ids) != 1 || ids[0] != rc.ID {
		t.Fatalf("pending ids: %v", ids)
	}
}

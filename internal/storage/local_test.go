package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

var jpeg = []byte{0xff, 0xd8, 0xff, 0xe0}

func TestLocalBlobStore(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if created, err := l.Put(ctx, "receipts/ab/abc.jpg", jpeg); err != nil || !created {
		t.Fatal(created, err)
	}
	got, err := l.Get(ctx, "receipts/ab/abc.jpg")
	if err != nil || string(got) != string(jpeg) {
		t.Fatalf("%v %v", got, err)
	}
	if created, err := l.Put(ctx, "receipts/ab/abc.jpg", []byte("other")); err != nil || created {
		t.Fatalf("existing key: created=%v err=%v", created, err)
	}
	if got, _ := l.Get(ctx, "receipts/ab/abc.jpg"); string(got) != string(jpeg) {
		t.Fatal("existing blob was overwritten")
	}
	if _, err := l.Put(ctx, "../escape", jpeg); err == nil {
		t.Fatal("path traversal must be rejected")
	}
	if err := l.Delete(ctx, "receipts/ab/abc.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(ctx, "receipts/ab/abc.jpg"); err != nil {
		t.Fatal("delete of missing key should succeed:", err)
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "receipts/ab"))
	if len(ents) != 0 {
		t.Fatalf("leftover files: %v", ents)
	}
}

func TestLocalPutConcurrentSameKey(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	data := bytes.Repeat([]byte("receipt-bytes"), 64<<10) // ~832 KiB
	var wg sync.WaitGroup
	var created atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := l.Put(context.Background(), "receipts/cc/same.jpg", data)
			if err != nil {
				t.Error(err)
			}
			if c {
				created.Add(1)
			}
			// a reader never sees a partial file
			if got, err := l.Get(context.Background(), "receipts/cc/same.jpg"); err != nil || !bytes.Equal(got, data) {
				t.Error("partial or missing blob after Put returned")
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("created reported %d times, want 1", created.Load())
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "receipts/cc"))
	if len(ents) != 1 {
		t.Fatalf("want only the final blob, got %d entries (temp files leaked)", len(ents))
	}
}

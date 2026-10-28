package storage

import (
	"context"
	"testing"
)

var jpeg = []byte{0xff, 0xd8, 0xff, 0xe0}

func TestLocalBlobStore(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := l.Put(ctx, "receipts/ab/abc.jpg", jpeg); err != nil {
		t.Fatal(err)
	}
	got, err := l.Get(ctx, "receipts/ab/abc.jpg")
	if err != nil || string(got) != string(jpeg) {
		t.Fatalf("%v %v", got, err)
	}
	if err := l.Put(ctx, "../escape", jpeg); err == nil {
		t.Fatal("path traversal must be rejected")
	}
}

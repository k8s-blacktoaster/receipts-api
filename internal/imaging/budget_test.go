package imaging

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBudgetWeightsAndFIFO(t *testing.T) {
	b := NewBudget(100)
	ctx := context.Background()
	if n, _ := b.Acquire(ctx, 60); n != 60 {
		t.Fatalf("acquire 60 got %d", n)
	}
	got := make(chan int64, 2)
	go func() { n, _ := b.Acquire(ctx, 50); got <- n }() // must wait: 60+50 > 100
	time.Sleep(20 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("50 granted over budget")
	default:
	}
	// FIFO: a small request behind the waiting 50 does not overtake it
	go func() { n, _ := b.Acquire(ctx, 10); got <- n }()
	time.Sleep(20 * time.Millisecond)
	if b.InUse() != 60 {
		t.Fatalf("in use %d, want 60", b.InUse())
	}
	b.Release(60)
	a, c := <-got, <-got
	if a+c != 60 || b.InUse() != 60 {
		t.Fatalf("granted %d+%d, in use %d", a, c, b.InUse())
	}
	// over-cap requests clamp to the cap so they can still run alone
	b.Release(60)
	if n, _ := b.Acquire(ctx, 1000); n != 100 {
		t.Fatalf("clamp got %d", n)
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := b.Acquire(cctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v", err)
	}
	b.Release(100)
	if b.InUse() != 0 || len(b.waiters) != 0 {
		t.Fatalf("leak: in use %d waiters %d", b.InUse(), len(b.waiters))
	}
}

// OPS-07: two maximum-size decodes never overlap, though the count semaphore would allow 2.
func TestNormalizeDecodeBudgetSerializesLargeImages(t *testing.T) {
	var cur, peak int32
	dec := decoderFunc(func() {
		n := atomic.AddInt32(&cur, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&cur, -1)
	})
	// 5000x5000 at 8 B/px (HEIC budget) = 200 MB: one fills the default budget.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = Normalize(context.Background(), ispeHEIC(5000, 5000), dec) }()
	}
	wg.Wait()
	if peak != 1 {
		t.Fatalf("peak large decodes %d, want 1", peak)
	}
	if DecodeBudget().InUse() != 0 {
		t.Fatalf("budget leak %d", DecodeBudget().InUse())
	}
}

func TestDecodeWeight(t *testing.T) {
	// 7000x7000 8-bit JPEG (YCbCr, 4 B/px) is close to the whole default budget.
	if w := decodeWeight(7000, 7000, 4); w < 196_000_000 || w > DefaultDecodeBudgetBytes+workingSetBytes {
		t.Fatalf("weight %d", w)
	}
	if 2*decodeWeight(7000, 7000, 4) <= DefaultDecodeBudgetBytes {
		t.Fatal("two 7000x7000 decodes must not fit the default budget")
	}
}

package imaging

import (
	"context"
	"sync"
)

// OPS-07: a global in-flight decode budget weighted by decoded size. The plain MaxConcurrent
// count still bounds CPU; this bounds memory, so two 7000x7000 bitmaps never decode at once
// while several phone-sized photos still can.

// DefaultDecodeBudgetBytes is the default sum of decoded-bitmap weights allowed in flight.
// It equals MaxDecodeBytes: one maximum-size image at a time.
const DefaultDecodeBudgetBytes = MaxDecodeBytes

// workingSetBytes is added to every weight for the resized RGBA copy and the JPEG encoder
// (long edge MaxEdge, 4 bytes per pixel).
const workingSetBytes = int64(MaxEdge) * MaxEdge * 4

// Budget is a FIFO-fair weighted semaphore over bytes.
type Budget struct {
	mu      sync.Mutex
	cap     int64
	used    int64
	waiters []*budgetWaiter
}

type budgetWaiter struct {
	n     int64
	ready chan struct{}
}

// NewBudget returns a budget of capBytes (<= 0 means DefaultDecodeBudgetBytes).
func NewBudget(capBytes int64) *Budget {
	if capBytes <= 0 {
		capBytes = DefaultDecodeBudgetBytes
	}
	return &Budget{cap: capBytes}
}

// Cap is the budget size in bytes.
func (b *Budget) Cap() int64 { return b.cap }

// InUse is the weight currently held.
func (b *Budget) InUse() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// clamp keeps a single request satisfiable: anything above the cap waits for an empty budget.
func (b *Budget) clamp(n int64) int64 {
	if n < 1 {
		return 1
	}
	if n > b.cap {
		return b.cap
	}
	return n
}

// Acquire blocks until n bytes (clamped to the cap) are free or ctx is done. It returns the
// weight actually held, to pass to Release.
func (b *Budget) Acquire(ctx context.Context, n int64) (int64, error) {
	n = b.clamp(n)
	b.mu.Lock()
	if len(b.waiters) == 0 && b.used+n <= b.cap {
		b.used += n
		b.mu.Unlock()
		return n, nil
	}
	w := &budgetWaiter{n: n, ready: make(chan struct{})}
	b.waiters = append(b.waiters, w)
	b.mu.Unlock()
	select {
	case <-w.ready:
		return n, nil
	case <-ctx.Done():
		b.mu.Lock()
		select {
		case <-w.ready: // granted while cancelling: give it back
			b.used -= n
		default:
			for i, x := range b.waiters {
				if x == w {
					b.waiters = append(b.waiters[:i], b.waiters[i+1:]...)
					break
				}
			}
		}
		b.grantLocked()
		b.mu.Unlock()
		return 0, ctx.Err()
	}
}

// Release returns n bytes and wakes waiters in FIFO order.
func (b *Budget) Release(n int64) {
	b.mu.Lock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
	b.grantLocked()
	b.mu.Unlock()
}

func (b *Budget) grantLocked() {
	for len(b.waiters) > 0 {
		w := b.waiters[0]
		if b.used+w.n > b.cap {
			return
		}
		b.used += w.n
		b.waiters = b.waiters[1:]
		close(w.ready)
	}
}

var decodeBudget = NewBudget(DefaultDecodeBudgetBytes)

// SetDecodeBudget replaces the global decode budget (IMAGE_DECODE_BUDGET_BYTES). Call at startup.
func SetDecodeBudget(capBytes int64) { decodeBudget = NewBudget(capBytes) }

// DecodeBudget returns the global decode budget.
func DecodeBudget() *Budget { return decodeBudget }

// decodeWeight is the budget weight of decoding a w x h image at bpp bytes per pixel.
func decodeWeight(w, h int, bpp int64) int64 {
	return int64(w)*int64(h)*bpp + workingSetBytes
}

package extraction

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockingProc struct {
	started chan int
	release chan struct{}
	calls   atomic.Int32
	done    atomic.Int32
	ctxErr  atomic.Int32
}

func (b *blockingProc) Process(ctx context.Context, id int) (*Outcome, error) {
	b.calls.Add(1)
	b.started <- id
	select {
	case <-b.release:
		b.done.Add(1)
	case <-ctx.Done():
		b.ctxErr.Add(1)
	}
	return &Outcome{}, nil
}

func newBlocking() *blockingProc {
	return &blockingProc{started: make(chan int, 16), release: make(chan struct{})}
}

func TestQueueCoalescesDuplicates(t *testing.T) {
	b := newBlocking()
	q := NewQueue(b, 10, time.Minute, nil)
	q.Start(context.Background(), 4)
	defer q.Shutdown(time.Second)
	q.Enqueue(7)
	<-b.started
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); q.Enqueue(7) }()
	}
	wg.Wait()
	close(b.release)
	time.Sleep(50 * time.Millisecond)
	if n := b.calls.Load(); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
}

func TestQueueShutdownWaitsForInFlight(t *testing.T) {
	b := newBlocking()
	q := NewQueue(b, 10, time.Minute, nil)
	q.Start(context.Background(), 1)
	q.Enqueue(1)
	q.Enqueue(2) // buffered; must not start after shutdown begins
	<-b.started
	res := make(chan bool)
	go func() { res <- q.Shutdown(2 * time.Second) }()
	time.Sleep(50 * time.Millisecond)
	if q.Enqueue(3) {
		t.Fatal("enqueue accepted after shutdown")
	}
	close(b.release)
	if !<-res {
		t.Fatal("shutdown reported timeout")
	}
	if b.calls.Load() != 1 || b.done.Load() != 1 || b.ctxErr.Load() != 0 {
		t.Fatalf("calls=%d done=%d cancelled=%d", b.calls.Load(), b.done.Load(), b.ctxErr.Load())
	}
}

func TestQueueShutdownTimeoutCancelsInFlight(t *testing.T) {
	b := newBlocking()
	q := NewQueue(b, 10, time.Minute, nil)
	q.Start(context.Background(), 1)
	q.Enqueue(1)
	<-b.started
	start := time.Now()
	if q.Shutdown(100 * time.Millisecond) {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > time.Second || b.ctxErr.Load() != 1 {
		t.Fatalf("in-flight job not cancelled: %d", b.ctxErr.Load())
	}
}

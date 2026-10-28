package extraction

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Processor is satisfied by *Pipeline.
type Processor interface {
	Process(ctx context.Context, receiptID int) (*Outcome, error)
}

// Queue is an in-process worker pool. Durable state lives in receipts.status, so on restart
// RequeuePending re-enqueues receipts left in uploaded/parsing (no separate jobs table yet).
type Queue struct {
	ch      chan int
	p       Processor
	timeout time.Duration
	log     *slog.Logger
	wg      sync.WaitGroup
}

func NewQueue(p Processor, size int, timeout time.Duration, log *slog.Logger) *Queue {
	if size <= 0 {
		size = 100
	}
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	return &Queue{ch: make(chan int, size), p: p, timeout: timeout, log: log}
}

// Enqueue never blocks; if the buffer is full the receipt stays "uploaded" and is picked up
// by the next RequeuePending.
func (q *Queue) Enqueue(receiptID int) bool {
	select {
	case q.ch <- receiptID:
		return true
	default:
		q.log.Warn("extraction queue full; receipt left pending", "receipt_id", receiptID)
		return false
	}
}

// Start runs n workers until ctx is cancelled.
func (q *Queue) Start(ctx context.Context, n int) {
	if n <= 0 {
		n = 1
	}
	for i := 0; i < n; i++ {
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-q.ch:
					jctx, cancel := context.WithTimeout(ctx, q.timeout)
					if _, err := q.p.Process(jctx, id); err != nil {
						q.log.Error("extraction pipeline error", "receipt_id", id, "error", err)
					}
					cancel()
				}
			}
		}()
	}
}

// Wait blocks until workers exit (after ctx cancel).
func (q *Queue) Wait() { q.wg.Wait() }

// RequeuePending enqueues receipts still in uploaded/parsing.
func (q *Queue) RequeuePending(ctx context.Context, list func(ctx context.Context, statuses ...string) ([]int, error)) {
	ids, err := list(ctx, "uploaded", "parsing")
	if err != nil {
		q.log.Error("requeue pending failed", "error", err)
		return
	}
	for _, id := range ids {
		q.Enqueue(id)
	}
}

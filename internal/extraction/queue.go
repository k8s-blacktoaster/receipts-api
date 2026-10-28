package extraction

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"receipts-db/internal/redact"
)

// Processor is satisfied by *Pipeline.
type Processor interface {
	Process(ctx context.Context, receiptID int) (*Outcome, error)
}

// Queue is an in-process worker pool. Durable state lives in receipts.status, so on restart
// ResetStaleParsing + RequeuePending pick up unfinished receipts (no separate jobs table yet).
// Concurrency safety does not depend on the queue: the pipeline claims each receipt atomically
// in the DB. The queue additionally coalesces duplicate enqueues of a receipt that is already
// queued or running, so bursts of re-extract requests do not pile up.
type Queue struct {
	ch      chan int
	p       Processor
	timeout time.Duration
	log     *slog.Logger
	wg      sync.WaitGroup

	mu      sync.Mutex
	pending map[int]jobState // queued or running
	closed  bool
	stop    chan struct{}      // closed by Shutdown: workers take no new jobs
	jobCtx  context.Context    // parent of every job context
	abort   context.CancelFunc // cancels in-flight jobs (shutdown timeout)
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
	jctx, abort := context.WithCancel(context.Background())
	return &Queue{ch: make(chan int, size), p: p, timeout: timeout, log: log,
		pending: map[int]jobState{}, stop: make(chan struct{}), jobCtx: jctx, abort: abort}
}

// Enqueue never blocks. It returns false if the queue is shut down or full (the receipt stays
// in the DB and is picked up by the next RequeuePending). A receipt already queued or running
// is coalesced (returns true without adding a second job).
func (q *Queue) Enqueue(receiptID int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		q.log.Warn("extraction queue shut down; receipt left pending", "receipt_id", receiptID)
		return false
	}
	switch q.pending[receiptID] {
	case jobQueued, jobRunningRerun:
		return true // already scheduled
	case jobRunning:
		// If the running job ends up skipping (it lost its claim, e.g. after a sweep reset and
		// this new request), run once more right after it instead of dropping the request.
		q.pending[receiptID] = jobRunningRerun
		return true
	}
	select {
	case q.ch <- receiptID:
		q.pending[receiptID] = jobQueued
		return true
	default:
		q.log.Warn("extraction queue full; receipt left pending", "receipt_id", receiptID)
		return false
	}
}

// Start runs n workers until ctx is cancelled or Shutdown is called. Cancelling ctx also
// cancels in-flight jobs; Shutdown lets them finish (up to its timeout).
func (q *Queue) Start(ctx context.Context, n int) {
	if n <= 0 {
		n = 1
	}
	go func() {
		select {
		case <-ctx.Done():
			q.abort()
		case <-q.jobCtx.Done():
		}
	}()
	for i := 0; i < n; i++ {
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			for {
				// prefer stopping over taking another job
				select {
				case <-q.stop:
					return
				case <-q.jobCtx.Done():
					return
				default:
				}
				select {
				case <-q.stop:
					return
				case <-q.jobCtx.Done():
					return
				case id := <-q.ch:
					q.run(id)
				}
			}
		}()
	}
}

type jobState int

const (
	jobNone jobState = iota
	jobQueued
	jobRunning
	jobRunningRerun
)

func (q *Queue) run(id int) {
	q.mu.Lock()
	q.pending[id] = jobRunning
	q.mu.Unlock()
	var out *Outcome
	defer func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		// Only a job that did no work (skipped: claim lost / not claimable) leaves a request
		// unserved; a completed job already satisfied requests made while it was running.
		rerun := q.pending[id] == jobRunningRerun && !q.closed && out != nil && out.Status == StatusSkipped
		delete(q.pending, id)
		if rerun {
			select {
			case q.ch <- id:
				q.pending[id] = jobQueued
			default:
				q.log.Warn("extraction queue full; rerun left to the sweep", "receipt_id", id)
			}
		}
	}()
	jctx, cancel := context.WithTimeout(q.jobCtx, q.timeout)
	defer cancel()
	var err error
	if out, err = q.p.Process(jctx, id); err != nil {
		q.log.Error("extraction pipeline error", "receipt_id", id, "error", redact.String(err.Error()))
	}
}

// Wait blocks until workers exit.
func (q *Queue) Wait() { q.wg.Wait() }

// Shutdown stops accepting jobs, lets in-flight jobs finish for up to timeout, then cancels
// them. Jobs still buffered are dropped; they remain uploaded/parsing in the DB and are
// recovered by the next startup's ResetStaleParsing + RequeuePending. Returns true if all
// in-flight jobs finished within the timeout.
func (q *Queue) Shutdown(timeout time.Duration) bool {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.stop)
	}
	q.mu.Unlock()
	done := make(chan struct{})
	go func() { q.wg.Wait(); close(done) }()
	select {
	case <-done:
		q.abort()
		return true
	case <-time.After(timeout):
		q.log.Warn("extraction shutdown timeout; cancelling in-flight jobs", "timeout", timeout)
		q.abort()
		<-done
		return false
	}
}

// RequeuePending enqueues receipts still "uploaded" (call ResetStaleParsing first so
// receipts abandoned mid-parse are back in "uploaded").
func (q *Queue) RequeuePending(ctx context.Context, list func(ctx context.Context, statuses ...string) ([]int, error)) {
	ids, err := list(ctx, "uploaded")
	if err != nil {
		q.log.Error("requeue pending failed", "error", err)
		return
	}
	for _, id := range ids {
		q.Enqueue(id)
	}
}

// StaleGrace is added to the job timeout before a "parsing" row counts as abandoned.
const StaleGrace = 30 * time.Second

// StaleAfter is how old a claim must be before the sweep may reset it: longer than any live
// job can run (job timeout + grace; with main.go wiring that is EXTRACTION_TIMEOUT_SECONDS + 60s),
// so a sweep never steals a claim that is still working.
func (q *Queue) StaleAfter() time.Duration { return q.timeout + StaleGrace }

// Recover resets abandoned "parsing" rows (older than StaleAfter) to "uploaded" and requeues
// everything "uploaded". Used at startup and by the periodic sweep, so both share one rule.
func (q *Queue) Recover(ctx context.Context, reset func(ctx context.Context, olderThan time.Duration) ([]int, error),
	list func(ctx context.Context, statuses ...string) ([]int, error)) {
	if ids, err := reset(ctx, q.StaleAfter()); err != nil {
		q.log.Error("reset stale parsing failed", "error", err)
	} else if len(ids) > 0 {
		q.log.Warn("reset stale parsing receipts", "count", len(ids), "receipt_ids", ids)
	}
	q.RequeuePending(ctx, list)
}

// StartSweeper runs Recover every interval until ctx is cancelled or Shutdown is called.
// interval <= 0 disables the periodic sweep (startup recovery still runs).
func (q *Queue) StartSweeper(ctx context.Context, interval time.Duration, reset func(ctx context.Context, olderThan time.Duration) ([]int, error),
	list func(ctx context.Context, statuses ...string) ([]int, error)) {
	if interval <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-q.stop:
				return
			case <-t.C:
				q.Recover(ctx, reset, list)
			}
		}
	}()
}

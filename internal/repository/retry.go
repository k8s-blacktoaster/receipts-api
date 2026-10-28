package repository

import (
	"context"
	"errors"
	"time"

	"github.com/lib/pq"
)

// ErrConcurrentUpdate is returned when a write kept colliding with another one (deadlock or
// serialization failure) after the bounded retries. Handlers map it to 409, never 500.
var ErrConcurrentUpdate = errors.New("concurrent update; try again")

// errRetry asks withTxRetry for another attempt (state changed between reads).
var errRetry = errors.New("retry")

const txRetries = 4

// isRetryable: SQLSTATE 40P01 deadlock_detected, 40001 serialization_failure.
func isRetryable(err error) bool {
	if errors.Is(err, errRetry) {
		return true
	}
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && (pqErr.Code == "40P01" || pqErr.Code == "40001")
}

// withTxRetry runs fn (one whole transaction) up to txRetries times while it fails with a
// retryable error, then returns ErrConcurrentUpdate.
func withTxRetry(ctx context.Context, fn func() error) error {
	for attempt := 0; attempt < txRetries; attempt++ {
		err := fn()
		if !isRetryable(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 15 * time.Millisecond):
		}
	}
	return ErrConcurrentUpdate
}

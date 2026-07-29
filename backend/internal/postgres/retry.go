// Package postgres owns shared PostgreSQL execution behavior.
package postgres

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	transactionMaxAttempts = 3
	transactionMinDelay    = 10 * time.Millisecond
	transactionMaxDelay    = 100 * time.Millisecond
)

// RetryTransaction retries a complete provider-free PostgreSQL transaction.
// The closure must include begin, rollback, and commit so every attempt replays
// the whole transaction and no external effect.
func RetryTransaction(ctx context.Context, transaction func() error) error {
	return retryTransaction(ctx, retryPolicy{
		maxAttempts: transactionMaxAttempts,
		minDelay:    transactionMinDelay,
		maxDelay:    transactionMaxDelay,
		jitter:      equalJitter,
		wait:        waitForRetry,
	}, transaction)
}

type retryPolicy struct {
	maxAttempts int
	minDelay    time.Duration
	maxDelay    time.Duration
	jitter      func(time.Duration) time.Duration
	wait        func(context.Context, time.Duration) error
}

func retryTransaction(
	ctx context.Context,
	policy retryPolicy,
	transaction func() error,
) error {
	delay := policy.minDelay
	for attempt := 1; attempt <= policy.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := transaction()
		if err == nil || !isRetryableTransactionError(err) ||
			attempt == policy.maxAttempts {
			return err
		}
		if err := policy.wait(ctx, policy.jitter(delay)); err != nil {
			return err
		}
		if delay > policy.maxDelay/2 {
			delay = policy.maxDelay
		} else {
			delay *= 2
		}
	}
	return nil
}

func isRetryableTransactionError(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	return postgresError.Code == "40001" || postgresError.Code == "40P01"
}

func equalJitter(delay time.Duration) time.Duration {
	minimum := delay/2 + delay%2
	spread := delay - minimum
	if spread == 0 {
		return delay
	}
	return minimum + time.Duration(rand.Int64N(int64(spread)+1))
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRetryTransactionCommitsAfterOneSerializationFailure(t *testing.T) {
	var attempts, commits int
	var delays []time.Duration
	policy := retryPolicy{
		maxAttempts: 3,
		minDelay:    10 * time.Millisecond,
		maxDelay:    40 * time.Millisecond,
		jitter:      func(delay time.Duration) time.Duration { return delay },
		wait: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	}
	err := retryTransaction(context.Background(), policy, func() error {
		attempts++
		if attempts == 1 {
			return &pgconn.PgError{
				Code:    "40001",
				Message: "injected serialization failure",
			}
		}
		commits++
		return nil
	})
	if err != nil {
		t.Fatalf("retry transaction: %v", err)
	}
	if attempts != 2 || commits != 1 {
		t.Fatalf("attempts=%d commits=%d, want 2 and 1", attempts, commits)
	}
	if len(delays) != 1 || delays[0] != 10*time.Millisecond {
		t.Fatalf("retry delays = %v, want [10ms]", delays)
	}
}

func TestRetryTransactionReturnsFinalDeadlockAfterBoundedAttempts(t *testing.T) {
	var attempts int
	var delays []time.Duration
	policy := retryPolicy{
		maxAttempts: 3,
		minDelay:    10 * time.Millisecond,
		maxDelay:    20 * time.Millisecond,
		jitter:      func(delay time.Duration) time.Duration { return delay },
		wait: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	}
	err := retryTransaction(context.Background(), policy, func() error {
		attempts++
		return &pgconn.PgError{
			Code:    "40P01",
			Message: "injected deadlock",
		}
	})
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "40P01" {
		t.Fatalf("exhausted retry error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	if len(delays) != len(want) ||
		delays[0] != want[0] ||
		delays[1] != want[1] {
		t.Fatalf("retry delays = %v, want %v", delays, want)
	}
}

func TestRetryTransactionStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var attempts int
	err := RetryTransaction(ctx, func() error {
		attempts++
		cancel()
		return &pgconn.PgError{
			Code:    "40001",
			Message: "injected serialization failure",
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retry error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

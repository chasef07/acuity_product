package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(t *testing.T) *pgxpool.Pool {
	return open(t, "")
}

func OpenThrough(t *testing.T, last string) *pgxpool.Pool {
	t.Helper()
	return open(t, last)
}

func open(t *testing.T, last string) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	if !strings.HasSuffix(strings.TrimPrefix(parsed.Path, "/"), "_test") {
		t.Fatalf("refusing to reset non-test database %q", parsed.Path)
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	config.MaxConns = 4
	config.MinConns = 0
	config.MaxConnIdleTime = time.Minute
	config.MaxConnLifetime = 5 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	var lock *pgxpool.Conn
	for {
		candidate, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire test database lock connection: %v", err)
		}
		var acquired bool
		if err := candidate.QueryRow(
			ctx,
			`SELECT pg_try_advisory_lock(4524)`,
		).Scan(&acquired); err != nil {
			candidate.Release()
			t.Fatalf("lock test database: %v", err)
		}
		if acquired {
			lock = candidate
			break
		}
		candidate.Release()
		select {
		case <-ctx.Done():
			t.Fatalf("lock test database: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Cleanup(func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = lock.Exec(unlockContext, `SELECT pg_advisory_unlock(4524)`)
		lock.Release()
	})

	migrationContext, cancelMigration := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelMigration()
	if _, err := lock.Exec(migrationContext, `
		DROP SCHEMA IF EXISTS auth CASCADE;
		DROP SCHEMA public CASCADE;
		CREATE SCHEMA public
	`); err != nil {
		t.Fatalf("reset test database: %v", err)
	}
	var migrationErr error
	if last == "" {
		migrationErr = migrations.Apply(migrationContext, pool)
	} else {
		migrationErr = migrations.ApplyThrough(migrationContext, pool, last)
	}
	if migrationErr != nil {
		t.Fatalf("apply migrations: %v; open transactions: %s", migrationErr, openTransactions(lock))
	}
	return pool
}

func openTransactions(lock *pgxpool.Conn) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var sessions string
	if err := lock.QueryRow(ctx, `
		SELECT COALESCE(string_agg(
			format('pid=%s state=%s since=%s query=%s', pid, state, xact_start, left(query, 120)),
			'; '
		), 'none')
		FROM pg_stat_activity
		WHERE datname = current_database()
			AND pid <> pg_backend_pid()
			AND xact_start IS NOT NULL
	`).Scan(&sessions); err != nil {
		return fmt.Sprintf("unavailable: %v", err)
	}
	return sessions
}

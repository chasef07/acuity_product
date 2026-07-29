package testdb

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open returns a clean, migrated pool to an explicitly named test database.
func Open(t *testing.T) *pgxpool.Pool {
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

	lock, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire test database lock connection: %v", err)
	}
	for {
		var acquired bool
		if err := lock.QueryRow(
			ctx,
			`SELECT pg_try_advisory_lock(4524)`,
		).Scan(&acquired); err != nil {
			lock.Release()
			t.Fatalf("lock test database: %v", err)
		}
		if acquired {
			break
		}
		select {
		case <-ctx.Done():
			lock.Release()
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

	if _, err := lock.Exec(ctx, `
		DROP SCHEMA IF EXISTS auth CASCADE;
		DROP SCHEMA public CASCADE;
		CREATE SCHEMA public
	`); err != nil {
		t.Fatalf("reset test database: %v", err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}

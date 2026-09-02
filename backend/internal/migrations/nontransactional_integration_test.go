package migrations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNonTransactionalMigrationTimeoutPreservesProgressAndResumes(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("TEST_DATABASE_URL must name a disposable _test database")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// A one-connection pool also catches attempts to record the migration by
	// acquiring a second connection while the migration session is still held.
	config.MaxConns = 1
	config.ConnConfig.RuntimeParams["statement_timeout"] = "0"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	schema := fmt.Sprintf("migration_timeout_%d", time.Now().UnixNano())
	name := schema + ".sql"
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
 CREATE TABLE IF NOT EXISTS public.schema_migrations(name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
 CREATE SCHEMA %s;
 CREATE TABLE %s.progress(batch integer PRIMARY KEY);
 CREATE PROCEDURE %s.backfill() LANGUAGE plpgsql AS $$
 BEGIN
  IF NOT EXISTS (SELECT 1 FROM %s.progress WHERE batch=1) THEN
   INSERT INTO %s.progress VALUES(1);
   COMMIT;
   PERFORM pg_sleep(1);
  END IF;
  INSERT INTO %s.progress VALUES(2);
  COMMIT;
 END;
 $$;
`, schema, schema, schema, schema, schema, schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = pool.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
		_, _ = pool.Exec(cleanup, "DELETE FROM public.schema_migrations WHERE name=$1", name)
	}()
	var originalPID int
	if err := pool.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&originalPID); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"SET statement_timeout = '250ms'",
		"CALL " + schema + ".backfill()",
		"RESET statement_timeout",
	}
	err = applyNonTransactional(ctx, pool, name, statements)
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "57014" {
		t.Fatalf("expected real server statement timeout: %v", err)
	}
	var count, nextPID int
	var timeout string
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".progress").Scan(&count); err != nil || count != 1 {
		t.Fatalf("committed batch lost after timeout: count %d, error %v", count, err)
	}
	if err := pool.QueryRow(ctx, "SELECT pg_backend_pid(), current_setting('statement_timeout')").Scan(&nextPID, &timeout); err != nil {
		t.Fatal(err)
	}
	if nextPID == originalPID || timeout != "0" {
		t.Fatalf("failed migration session returned to pool: old PID %d, new PID %d, timeout %s", originalPID, nextPID, timeout)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM public.schema_migrations WHERE name=$1", name).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration was recorded: count %d, error %v", count, err)
	}
	if err := applyNonTransactional(ctx, pool, name, statements); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".progress").Scan(&count); err != nil || count != 2 {
		t.Fatalf("resume did not finish remaining batch: count %d, error %v", count, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM public.schema_migrations WHERE name=$1", name).Scan(&count); err != nil || count != 1 {
		t.Fatalf("successful migration not recorded: count %d, error %v", count, err)
	}
	if err := pool.QueryRow(ctx, "SELECT current_setting('statement_timeout')").Scan(&timeout); err != nil || timeout != "0" {
		t.Fatalf("successful migration leaked its timeout: %s, error %v", timeout, err)
	}
}

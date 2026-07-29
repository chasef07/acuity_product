package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var migrationFiles embed.FS

const (
	nonTransactionalMigrationHeader = "-- acuity:no-transaction"
	migrationStatementSeparator     = "-- acuity:next-statement"
)

// Apply runs every unapplied forward-only migration in filename order.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.schema_migrations (
			name text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFiles, "sql")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var applied bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM public.schema_migrations WHERE name = $1)`,
			entry.Name(),
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", entry.Name(), err)
		}
		if applied {
			continue
		}

		sql, err := migrationFiles.ReadFile("sql/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		statements, nonTransactional, err := migrationStatements(string(sql))
		if err != nil {
			return fmt.Errorf("parse migration %s: %w", entry.Name(), err)
		}
		if nonTransactional {
			if err := applyNonTransactional(
				ctx,
				pool,
				entry.Name(),
				statements,
			); err != nil {
				return err
			}
			continue
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO public.schema_migrations (name) VALUES ($1)`,
			entry.Name(),
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func migrationStatements(
	sql string,
) (statements []string, nonTransactional bool, err error) {
	lines := strings.Split(sql, "\n")
	if len(lines) == 0 ||
		strings.TrimSpace(lines[0]) != nonTransactionalMigrationHeader {
		return []string{sql}, false, nil
	}

	var statement strings.Builder
	appendStatement := func() error {
		value := strings.TrimSpace(statement.String())
		if value == "" {
			return fmt.Errorf("non-transactional migration contains an empty statement")
		}
		statements = append(statements, value)
		statement.Reset()
		return nil
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == migrationStatementSeparator {
			if err := appendStatement(); err != nil {
				return nil, true, err
			}
			continue
		}
		statement.WriteString(line)
		statement.WriteByte('\n')
	}
	if err := appendStatement(); err != nil {
		return nil, true, err
	}
	return statements, true, nil
}

func applyNonTransactional(
	ctx context.Context,
	pool *pgxpool.Pool,
	name string,
	statements []string,
) error {
	for index, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf(
				"apply non-transactional migration %s statement %d: %w",
				name,
				index+1,
				err,
			)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.schema_migrations (name) VALUES ($1)`,
		name,
	); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	return nil
}

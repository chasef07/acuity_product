package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql database-grants.sql
var migrationFiles embed.FS

const (
	nonTransactionalMigrationHeader = "-- acuity:no-transaction"
	migrationCompletionQueryHeader  = "-- acuity:complete-if-true"
	migrationStatementSeparator     = "-- acuity:next-statement"
	retiredMigrationHeader          = "-- acuity:retired"
)

// Apply runs every unapplied forward-only migration in filename order.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	return applyThrough(ctx, pool, "")
}

// ApplyThrough runs migrations through the named file, inclusive. It exists so
// migration tests can prove a real upgrade from the immediately prior schema.
func ApplyThrough(ctx context.Context, pool *pgxpool.Pool, last string) error {
	if last == "" || strings.ContainsAny(last, `/\\`) {
		return fmt.Errorf("valid final migration filename is required")
	}
	return applyThrough(ctx, pool, last)
}

func applyThrough(ctx context.Context, pool *pgxpool.Pool, last string) error {
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
	if last != "" {
		found := false
		for _, entry := range entries {
			if !entry.IsDir() && entry.Name() == last {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("final migration %s does not exist", last)
		}
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if last != "" && entry.Name() > last {
			break
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
		if isRetiredMigration(string(sql)) {
			if err := recordMigration(ctx, pool, entry.Name()); err != nil {
				return err
			}
			continue
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

// ApplyRuntimeGrants reapplies the reviewed least-privilege runtime authority
// after forward migrations add or change relations.
func ApplyRuntimeGrants(ctx context.Context, pool *pgxpool.Pool) error {
	sql, err := migrationFiles.ReadFile("database-grants.sql")
	if err != nil {
		return fmt.Errorf("read database grants: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin database grants: %w", err)
	}
	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("apply database grants: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit database grants: %w", err)
	}
	return nil
}

func isRetiredMigration(sql string) bool {
	firstLine, _, _ := strings.Cut(sql, "\n")
	return strings.TrimSpace(firstLine) == retiredMigrationHeader
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
	firstStatement := 0
	if query, ok := migrationCompletionQuery(statements[0]); ok {
		var complete bool
		if err := pool.QueryRow(ctx, query).Scan(&complete); err != nil {
			return fmt.Errorf(
				"check non-transactional migration %s completion: %w",
				name,
				err,
			)
		}
		if complete {
			return recordMigration(ctx, pool, name)
		}
		firstStatement = 1
	}
	// Session settings must apply to the same connection as the statement they
	// protect. These migrations cannot use a transaction (for example, CALLs
	// that commit batches and CREATE INDEX CONCURRENTLY).
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire non-transactional migration %s connection: %w", name, err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			// A failed statement may skip the migration's RESET commands. Never
			// return that session to the pool with its temporary settings intact.
			closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = connection.Conn().Close(closeContext)
		}
		connection.Release()
	}()
	for index, statement := range statements[firstStatement:] {
		if _, err := connection.Exec(ctx, statement); err != nil {
			return fmt.Errorf(
				"apply non-transactional migration %s statement %d: %w",
				name,
				firstStatement+index+1,
				err,
			)
		}
	}
	if err := recordMigration(ctx, connection, name); err != nil {
		return err
	}
	succeeded = true
	return nil
}

func migrationCompletionQuery(statement string) (string, bool) {
	lines := strings.SplitN(statement, "\n", 2)
	if strings.TrimSpace(lines[0]) != migrationCompletionQueryHeader ||
		len(lines) != 2 ||
		strings.TrimSpace(lines[1]) == "" {
		return "", false
	}
	return strings.TrimSpace(lines[1]), true
}

func recordMigration(
	ctx context.Context,
	database interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	name string,
) error {
	if _, err := database.Exec(ctx,
		`INSERT INTO public.schema_migrations (name) VALUES ($1)`,
		name,
	); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	return nil
}

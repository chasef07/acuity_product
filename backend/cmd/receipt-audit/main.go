package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := run(ctx, os.Stdout, os.Getenv); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	output io.Writer,
	getenv func(string) string,
) error {
	databaseURL := strings.TrimSpace(getenv("AUDIT_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("AUDIT_DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse audit database URL: %w", err)
	}
	config.MaxConns = 1
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("open audit database: %w", err)
	}
	defer pool.Close()

	audit, err := humancalling.New(
		pool,
		nil,
		nil,
		humancalling.Config{},
		time.Now,
	).AuditProviderReceipts(ctx)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(audit); err != nil {
		return fmt.Errorf("encode provider receipt audit: %w", err)
	}
	return nil
}

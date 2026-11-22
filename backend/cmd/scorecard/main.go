package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "", "backfill")
	days := flag.Int("days", 21, "backfill: closed calls started in the last N days")
	batch := flag.Int("batch", 200, "backfill: calls per transaction")
	flag.Parse()
	if *mode != "backfill" {
		return fmt.Errorf("--mode must be backfill")
	}
	if *days < 1 || *days > 90 {
		return fmt.Errorf("--days must be between 1 and 90")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	module := interaction.New(pool, access.New(pool, nil), nil)
	result, err := module.BackfillScorecards(ctx, time.Now().AddDate(0, 0, -*days), *batch)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

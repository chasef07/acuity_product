package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
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
	mode := flag.String("mode", "", "backfill or import-golden-set")
	days := flag.Int("days", 21, "backfill: closed calls started in the last N days")
	batch := flag.Int("batch", 200, "backfill: calls per transaction")
	input := flag.String("input", "", "import-golden-set: JSON lines file")
	practice := flag.String("practice", "", "import-golden-set: Practice UUID")
	subject := flag.String("reviewer-subject", "", "import-golden-set: reviewer's user subject")
	email := flag.String("reviewer-email", "", "import-golden-set: reviewer's email")
	flag.Parse()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	module := interaction.New(pool, access.New(pool, nil), nil)
	var result any
	switch *mode {
	case "backfill":
		if *days < 1 || *days > 90 {
			return fmt.Errorf("--days must be between 1 and 90")
		}
		result, err = module.BackfillScorecards(ctx, time.Now().AddDate(0, 0, -*days), *batch)
	case "import-golden-set":
		file, openErr := os.Open(*input)
		if openErr != nil {
			return openErr
		}
		defer file.Close()
		entries := []interaction.GoldenSetEntry{}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var entry interaction.GoldenSetEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				return fmt.Errorf("read golden set line %d: %w", len(entries)+1, err)
			}
			entries = append(entries, entry)
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		result, err = module.ImportGoldenSet(ctx, *practice, access.Identity{Subject: *subject, Email: strings.ToLower(*email), EmailVerified: true}, entries, time.Now())
	default:
		return fmt.Errorf("--mode must be backfill or import-golden-set")
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

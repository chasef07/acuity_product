// task-maintenance prepares reviewed, version-checked Task classification work.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	mode := flag.String("mode", "plan", "plan, apply, restore-plan, or responsibilities")
	input := flag.String("input", "", "reviewed JSON input")
	output := flag.String("output", "", "new private JSON output file")
	runID := flag.String("run", "", "unique reviewed run identity")
	practice := flag.String("practice", "", "Practice UUID")
	locations := flag.String("locations", "", "comma-separated Location UUIDs")
	subject := flag.String("actor-subject", "", "existing Platform Operator subject")
	email := flag.String("actor-email", "", "existing verified Platform Operator email")
	flag.Parse()
	if *output == "" {
		return fmt.Errorf("--output is required; reports may contain protected Task context")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	a := access.New(pool, nil)
	m := work.New(pool, a, nil)
	identity := access.Identity{Subject: *subject, Email: strings.ToLower(*email), EmailVerified: true}
	read := func(target any) error {
		f, err := os.Open(*input)
		if err != nil {
			return err
		}
		defer f.Close()
		d := json.NewDecoder(f)
		d.DisallowUnknownFields()
		return d.Decode(target)
	}
	// Reserve the report path before performing any mutations. A failed write is
	// recoverable by rerunning the same plan using durable per-Task receipts.
	f, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	var result any
	switch *mode {
	case "plan":
		result, err = m.PlanReclassification(ctx, identity, *runID, *practice, strings.Split(*locations, ","))
	case "restore-plan":
		result, err = m.RestorationPlan(ctx, identity, *runID, *practice, strings.Split(*locations, ","))
	case "apply":
		var plan work.ReclassificationPlan
		if err = read(&plan); err == nil {
			result, err = m.ApplyReclassification(ctx, identity, plan)
		}
	case "responsibilities":
		var roster work.ResponsibilityProvision
		if err = read(&roster); err == nil {
			result, err = m.ProvisionResponsibilities(ctx, roster)
		}
	default:
		err = fmt.Errorf("unknown mode %q", *mode)
	}
	if result != nil {
		encoder := json.NewEncoder(f)
		encoder.SetIndent("", "  ")
		if writeErr := encoder.Encode(result); writeErr != nil {
			return writeErr
		}
		if syncErr := f.Sync(); syncErr != nil {
			return syncErr
		}
	}
	return err
}

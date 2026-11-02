package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/google/uuid"
)

const importReason = "Imported from the middleware's insurance files"

type receipt struct {
	Applied    bool                `json:"applied"`
	Published  bool                `json:"published"`
	PracticeID string              `json:"practiceId"`
	Revision   *insurance.Revision `json:"revision,omitempty"`
	Carriers   int                 `json:"carriers"`
	Regions    int                 `json:"regions"`
	Plans      int                 `json:"plans"`
	Tables     int                 `json:"tables"`
}

func main() {
	practice := flag.String("practice", "", "Practice UUID whose insurance list to seed")
	fromFile := flag.String("from-file", "", "path to the InsuranceCatalog JSON to seed")
	apply := flag.Bool("apply", false, "publish the catalog as the Practice's first revision")
	flag.Parse()
	if err := run(*practice, *fromFile, *apply); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(practice, fromFile string, apply bool) error {
	if uuid.Validate(practice) != nil {
		return errors.New("--practice must be the Practice UUID")
	}
	catalog, err := readCatalog(fromFile)
	if err != nil {
		return err
	}
	middleware, err := insurance.NewMiddlewareClient(os.Getenv("MIDDLEWARE_BASE_URL"), os.Getenv("MIDDLEWARE_API_SECRET"), nil)
	if err != nil {
		return errors.New("MIDDLEWARE_BASE_URL and MIDDLEWARE_API_SECRET are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if !apply {
		result, err := check(ctx, middleware, practice, catalog)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if os.Getenv("INSURANCE_IMPORT_DATABASE_URL") == "" || os.Getenv("INSURANCE_OPERATOR_EMAIL") == "" {
		return errors.New("INSURANCE_IMPORT_DATABASE_URL and INSURANCE_OPERATOR_EMAIL are required")
	}
	pool, err := postgres.OpenOperatorPool(ctx, "INSURANCE_IMPORT_DATABASE_URL", "INSURANCE_DATABASE_HOST")
	if err != nil {
		return err
	}
	defer pool.Close()
	result, err := seed(ctx, pool, middleware, practice, os.Getenv("INSURANCE_OPERATOR_EMAIL"), catalog)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func readCatalog(path string) (insurance.Catalog, error) {
	if path == "" {
		return insurance.Catalog{}, errors.New("--from-file must name the InsuranceCatalog JSON to seed")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return insurance.Catalog{}, fmt.Errorf("read the catalog file: %w", err)
	}
	catalog, err := insurance.ParseCatalog(body)
	if err != nil {
		return insurance.Catalog{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return catalog, nil
}

func check(ctx context.Context, middleware *insurance.MiddlewareClient, practice string, catalog insurance.Catalog) (receipt, error) {
	if err := middleware.Validate(ctx, catalog); err != nil {
		return receipt{}, describe(err)
	}
	return summarize(receipt{PracticeID: practice}, catalog), nil
}

func seed(ctx context.Context, db postgres.Database, middleware *insurance.MiddlewareClient, practice, operatorEmail string, catalog insurance.Catalog) (receipt, error) {
	accessModule := access.New(db, nil)
	subject, err := accessModule.ReadPlatformOperatorSubject(ctx, operatorEmail)
	if err != nil {
		return receipt{}, errors.New("import requires an existing bound Platform Operator")
	}
	revisions, err := insurance.NewRevisions(db, accessModule, middleware)
	if err != nil {
		return receipt{}, err
	}
	revision, published, err := revisions.Import(ctx, insurance.ImportCommand{
		PracticeID: practice, RevisionID: uuid.NewString(), Reason: importReason, ActorSubject: subject, Catalog: catalog,
	})
	if err != nil {
		return receipt{}, describe(err)
	}
	return summarize(receipt{Applied: true, Published: published, PracticeID: practice, Revision: &revision}, catalog), nil
}

func summarize(result receipt, catalog insurance.Catalog) receipt {
	result.Carriers, result.Regions = len(catalog.Carriers), len(catalog.Regions)
	for _, region := range catalog.Regions {
		result.Plans += len(region.Plans)
		result.Tables += len(region.Tables)
	}
	return result
}

func describe(err error) error {
	var rejected *insurance.ValidationError
	if !errors.As(err, &rejected) {
		return fmt.Errorf("insurance import failed: %w", err)
	}
	lines := []string{rejected.Error() + ":"}
	for _, reason := range rejected.Errors {
		lines = append(lines, fmt.Sprintf("  %s/%s/%s %s: %s", reason.Region, reason.Table, reason.PlanID, reason.Rule, reason.Message))
	}
	return errors.New(strings.Join(lines, "\n"))
}

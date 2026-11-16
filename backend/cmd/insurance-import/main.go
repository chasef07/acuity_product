package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/gitsource"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/google/uuid"
)

const importReason = "Imported from the middleware's insurance files"

type receipt struct {
	Applied         bool                `json:"applied"`
	Published       bool                `json:"published"`
	PracticeID      string              `json:"practiceId"`
	Revision        *insurance.Revision `json:"revision,omitempty"`
	SourceGitCommit string              `json:"sourceGitCommit,omitempty"`
	Carriers        int                 `json:"carriers"`
	Regions         int                 `json:"regions"`
	Plans           int                 `json:"plans"`
	Tables          int                 `json:"tables"`
}

type options struct {
	practice         string
	fromFile         string
	apply            bool
	export           bool
	revision         string
	expectedRevision string
	reason           string
	commit           string
}

type updateCommand struct {
	practice         string
	expectedRevision string
	reason           string
	commit           string
	body             []byte
	catalog          insurance.Catalog
}

func main() {
	var o options
	flag.StringVar(&o.practice, "practice", "", "Practice UUID whose insurance list to export, set up, or update")
	flag.StringVar(&o.fromFile, "from-file", "", "path to the InsuranceCatalog JSON to publish")
	flag.BoolVar(&o.apply, "apply", false, "publish the file; without it the middleware only checks it")
	flag.BoolVar(&o.export, "export", false, "write an insurance list revision as JSON to stdout")
	flag.StringVar(&o.revision, "revision", "", "revision UUID for --export; defaults to the active revision")
	flag.StringVar(&o.expectedRevision, "expected-revision", "", "the active revision UUID, from --export, when updating a Practice's list")
	flag.StringVar(&o.reason, "reason", "", "why the insurance list changed; required when updating")
	flag.StringVar(&o.commit, "commit", "", "full Git commit SHA of the reviewed file; required to apply an update")
	flag.Parse()
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(o options) error {
	if uuid.Validate(o.practice) != nil {
		return errors.New("--practice must be the Practice UUID")
	}
	if o.export {
		if o.fromFile != "" || o.apply || o.expectedRevision != "" || o.reason != "" || o.commit != "" {
			return errors.New("--export takes only --practice and --revision")
		}
		if o.revision != "" && uuid.Validate(o.revision) != nil {
			return errors.New("--revision must be a revision UUID")
		}
		return runExport(o.practice, o.revision)
	}
	if o.revision != "" {
		return errors.New("--revision is only for --export; update with --expected-revision")
	}
	updating := o.expectedRevision != ""
	if !updating && (o.reason != "" || o.commit != "") {
		return errors.New("--reason and --commit are for updates and need --expected-revision")
	}
	if updating {
		expected, err := uuid.Parse(o.expectedRevision)
		if err != nil {
			return errors.New("--expected-revision must be the revision UUID printed by --export")
		}
		o.expectedRevision = expected.String()
		if strings.TrimSpace(o.reason) == "" {
			return errors.New("--reason must say why the insurance list changed")
		}
		if o.apply && o.commit == "" {
			return errors.New("--commit must name the Git commit of the reviewed file to apply an update")
		}
	}
	catalog, body, err := readCatalog(o.fromFile)
	if err != nil {
		return err
	}
	if updating && o.apply {
		if err := gitsource.Verify(o.fromFile, o.commit, body); err != nil {
			return err
		}
	}
	middleware, err := insurance.NewMiddlewareClient(os.Getenv("MIDDLEWARE_BASE_URL"), os.Getenv("MIDDLEWARE_API_SECRET"), nil)
	if err != nil {
		return errors.New("MIDDLEWARE_BASE_URL and MIDDLEWARE_API_SECRET are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if !o.apply {
		result, err := check(ctx, middleware, o.practice, catalog)
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
	var result receipt
	if updating {
		result, err = update(ctx, pool, middleware, os.Getenv("INSURANCE_OPERATOR_EMAIL"), updateCommand{
			practice: o.practice, expectedRevision: o.expectedRevision, reason: o.reason, commit: o.commit, body: body, catalog: catalog,
		})
	} else {
		result, err = seed(ctx, pool, middleware, o.practice, os.Getenv("INSURANCE_OPERATOR_EMAIL"), catalog)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func runExport(practice, revision string) error {
	if os.Getenv("INSURANCE_IMPORT_DATABASE_URL") == "" {
		return errors.New("INSURANCE_IMPORT_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.OpenOperatorPool(ctx, "INSURANCE_IMPORT_DATABASE_URL", "INSURANCE_DATABASE_HOST")
	if err != nil {
		return err
	}
	defer pool.Close()
	exported, err := exportCatalog(ctx, pool, practice, revision, os.Stdout)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stderr).Encode(map[string]string{"practiceId": practice, "revisionId": exported})
}

func exportCatalog(ctx context.Context, db postgres.Database, practice, revision string, w io.Writer) (string, error) {
	revisions, err := insurance.NewRevisions(db, access.New(db, nil), nil)
	if err != nil {
		return "", err
	}
	read, err := revisions.OperatorRead(ctx, practice, revision)
	if err != nil {
		return "", fmt.Errorf("could not read the insurance list: %w", err)
	}
	body, err := json.MarshalIndent(read.Catalog, "", "  ")
	if err != nil {
		return "", err
	}
	if _, err := w.Write(append(body, '\n')); err != nil {
		return "", err
	}
	return read.Revision.ID, nil
}

func readCatalog(path string) (insurance.Catalog, []byte, error) {
	if path == "" {
		return insurance.Catalog{}, nil, errors.New("--from-file must name the InsuranceCatalog JSON to publish")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return insurance.Catalog{}, nil, fmt.Errorf("read the catalog file: %w", err)
	}
	catalog, err := insurance.ParseCatalog(body)
	if err != nil {
		return insurance.Catalog{}, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return catalog, body, nil
}

func check(ctx context.Context, middleware *insurance.MiddlewareClient, practice string, catalog insurance.Catalog) (receipt, error) {
	if err := middleware.Validate(ctx, catalog); err != nil {
		return receipt{}, describe(err)
	}
	return summarize(receipt{PracticeID: practice}, catalog), nil
}

func seed(ctx context.Context, db postgres.Database, middleware *insurance.MiddlewareClient, practice, operatorEmail string, catalog insurance.Catalog) (receipt, error) {
	revision, published, err := publish(ctx, db, middleware, operatorEmail, insurance.ImportCommand{
		PracticeID: practice, RevisionID: uuid.NewString(), Reason: importReason, Catalog: catalog,
	})
	if errors.Is(err, insurance.ErrConflict) {
		return receipt{}, fmt.Errorf("%w; export it with --export and publish your changes with --expected-revision", err)
	}
	if err != nil {
		return receipt{}, err
	}
	return summarize(receipt{Applied: true, Published: published, PracticeID: practice, Revision: &revision}, catalog), nil
}

func update(ctx context.Context, db postgres.Database, middleware *insurance.MiddlewareClient, operatorEmail string, cmd updateCommand) (receipt, error) {
	revisionID := uuid.NewSHA1(uuid.NameSpaceURL, append([]byte(cmd.commit+"\n"+cmd.expectedRevision+"\n"+cmd.reason+"\n"), cmd.body...)).String()
	revision, published, err := publish(ctx, db, middleware, operatorEmail, insurance.ImportCommand{
		PracticeID: cmd.practice, RevisionID: revisionID, ExpectedRevisionID: cmd.expectedRevision, Reason: cmd.reason, Catalog: cmd.catalog,
	})
	switch {
	case errors.Is(err, insurance.ErrNoCatalog):
		return receipt{}, fmt.Errorf("%w; set it up without --expected-revision", err)
	case errors.Is(err, insurance.ErrConflict):
		return receipt{}, fmt.Errorf("%w; the active revision is not %s, so export again, reapply your edits, and publish against the new revision", err, cmd.expectedRevision)
	case err != nil:
		return receipt{}, err
	}
	return summarize(receipt{Applied: true, Published: published, PracticeID: cmd.practice, Revision: &revision, SourceGitCommit: cmd.commit}, cmd.catalog), nil
}

func publish(ctx context.Context, db postgres.Database, middleware *insurance.MiddlewareClient, operatorEmail string, cmd insurance.ImportCommand) (insurance.Revision, bool, error) {
	accessModule := access.New(db, nil)
	subject, err := accessModule.ReadPlatformOperatorSubject(ctx, operatorEmail)
	if err != nil {
		return insurance.Revision{}, false, errors.New("import requires an existing bound Platform Operator")
	}
	revisions, err := insurance.NewRevisions(db, accessModule, middleware)
	if err != nil {
		return insurance.Revision{}, false, err
	}
	cmd.ActorSubject = subject
	revision, published, err := revisions.Import(ctx, cmd)
	if err != nil {
		return insurance.Revision{}, false, describe(err)
	}
	return revision, published, nil
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

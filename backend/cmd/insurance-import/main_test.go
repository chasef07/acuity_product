package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

const (
	exportPath    = "../../internal/insurance/testdata/export.json"
	operatorEmail = "operator@acuity.test"
)

type recordedMiddleware struct {
	verdict   atomic.Value
	validated atomic.Int32
}

func (m *recordedMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer synthetic-middleware-secret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/insurance/catalog/validate":
		m.validated.Add(1)
		verdict := m.verdict.Load().(string)
		if strings.Contains(verdict, `"valid":false`) {
			w.WriteHeader(http.StatusBadRequest)
		}
		_, _ = w.Write([]byte(verdict))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

const rejectedVerdict = `{"valid":false,"errors":[{"region":"south_florida","table":"doctors","planId":"aetna-better-health","rule":"note_rule_not_encoded","message":"Synthetic note rule is not encoded."}]}`

type fixture struct {
	db         *testdb.DB
	runtime    postgres.Database
	practice   string
	recorded   *recordedMiddleware
	middleware *insurance.MiddlewareClient
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := testdb.Open(t)
	ctx := context.Background()
	if _, err := access.New(db.Owner, nil).Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "insurance-import-test", PlatformOperators: []string{operatorEmail}, Practices: []access.PracticeProvision{
		{Key: "abita-eye-group", Name: "Synthetic Abita", Locations: []access.LocationProvision{{Key: "spring-hill", Name: "Spring Hill", AbitaOfficeKeys: []string{"spring-hill"}}}},
	}}); err != nil {
		t.Fatal(err)
	}
	runtime := db.As(testdb.Portal)
	if _, err := access.New(runtime, nil).DiscoverActor(ctx, access.Identity{Subject: "operator-subject", Email: operatorEmail, EmailVerified: true}); err != nil {
		t.Fatal(err)
	}
	var practice string
	if err := db.Owner.QueryRow(ctx, `SELECT id::text FROM access_practices WHERE provisioning_key='abita-eye-group'`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	recorded := &recordedMiddleware{}
	recorded.verdict.Store(`{"valid":true}`)
	server := httptest.NewServer(recorded)
	t.Cleanup(server.Close)
	middleware, err := insurance.NewMiddlewareClient(server.URL, "synthetic-middleware-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{db: db, runtime: runtime, practice: practice, recorded: recorded, middleware: middleware}
}

func (f fixture) revisions(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.db.Owner.QueryRow(context.Background(), `SELECT count(*) FROM insurance_revisions WHERE practice_id=$1`, f.practice).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (f fixture) active(t *testing.T) insurance.ActiveCatalog {
	t.Helper()
	module, err := insurance.NewRevisions(f.runtime, access.New(f.runtime, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	active, err := module.Read(context.Background(), access.ServiceIdentity{Subject: "abita-eye-group-middleware", PracticeID: f.practice, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadInsurance}})
	if err != nil {
		t.Fatal(err)
	}
	return active
}

func writeCatalog(t *testing.T, body []byte) (insurance.Catalog, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, read, err := readCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, read
}

func canonical(t *testing.T, body []byte) []byte {
	t.Helper()
	catalog, err := insurance.ParseCatalog(body)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestReadCatalogAcceptsOnlyOneStrictCatalogFile(t *testing.T) {
	catalog, body, err := readCatalog(exportPath)
	if err != nil || len(catalog.Carriers) != 50 || len(catalog.Regions) != 2 {
		t.Fatalf("read the export: %d carriers, %d regions, %v", len(catalog.Carriers), len(catalog.Regions), err)
	}
	if export, _ := os.ReadFile(exportPath); !bytes.Equal(body, export) {
		t.Fatal("readCatalog did not return the file's bytes")
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"unknown-field.json": `{"carriers":[],"regions":[],"revision":"1"}`,
		"two-documents.json": `{"carriers":[],"regions":[]} {}`,
		"not-json.json":      `carriers`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readCatalog(path); !errors.Is(err, insurance.ErrInvalidInput) {
			t.Errorf("%s: error = %v, want invalid input", name, err)
		}
	}
	for _, path := range []string{"", filepath.Join(dir, "missing.json")} {
		if _, _, err := readCatalog(path); err == nil {
			t.Errorf("readCatalog(%q) accepted a missing file", path)
		}
	}
}

func TestRunRejectsIncompleteFlagsBeforeTouchingAnything(t *testing.T) {
	practice := uuid.NewString()
	revision := uuid.NewString()
	commit := strings.Repeat("a", 40)
	for name, test := range map[string]struct {
		options options
		want    string
	}{
		"no practice":                     {options{fromFile: exportPath}, "--practice"},
		"reason without update":           {options{practice: practice, fromFile: exportPath, reason: "Synthetic change"}, "--expected-revision"},
		"commit without update":           {options{practice: practice, fromFile: exportPath, commit: commit}, "--expected-revision"},
		"revision without export":         {options{practice: practice, fromFile: exportPath, revision: revision}, "--export"},
		"expected revision not id":        {options{practice: practice, fromFile: exportPath, expectedRevision: "1", reason: "Synthetic change"}, "--expected-revision"},
		"update without reason":           {options{practice: practice, fromFile: exportPath, expectedRevision: revision, reason: " "}, "--reason"},
		"applied update, no commit":       {options{practice: practice, fromFile: exportPath, expectedRevision: revision, reason: "Synthetic change", apply: true}, "--commit"},
		"applied update, unreviewed file": {options{practice: practice, fromFile: exportPath, expectedRevision: revision, reason: "Synthetic change", commit: strings.Repeat("0", 40), apply: true}, "Git commit"},
		"export with a file":              {options{practice: practice, export: true, fromFile: exportPath}, "--export"},
		"export revision not id":          {options{practice: practice, export: true, revision: "1"}, "--revision"},
	} {
		if err := run(test.options); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: error = %v, want it to mention %q", name, err, test.want)
		}
	}
}

func TestSeedPublishesTheCatalogFileOnceAsRevisionOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	catalog, export, err := readCatalog(exportPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, operator := range []string{"", "unknown@acuity.test"} {
		if _, err := seed(ctx, f.runtime, f.middleware, f.practice, operator, catalog); err == nil {
			t.Fatalf("seed accepted operator %q", operator)
		}
	}

	f.recorded.verdict.Store(rejectedVerdict)
	if _, err := seed(ctx, f.runtime, f.middleware, f.practice, operatorEmail, catalog); err == nil || !strings.Contains(err.Error(), "south_florida/doctors/aetna-better-health note_rule_not_encoded: Synthetic note rule is not encoded.") {
		t.Fatalf("seed of a rejected catalog: %v", err)
	}
	if _, err := check(ctx, f.middleware, f.practice, catalog); err == nil || !strings.Contains(err.Error(), "note_rule_not_encoded") {
		t.Fatalf("dry run of a rejected catalog: %v", err)
	}
	if count := f.revisions(t); count != 0 {
		t.Fatalf("a rejected seed stored %d revisions", count)
	}
	f.recorded.verdict.Store(`{"valid":true}`)

	dry, err := check(ctx, f.middleware, f.practice, catalog)
	if err != nil || dry.Applied || dry.Carriers != 50 || dry.Regions != 2 || dry.Tables != 3 || f.revisions(t) != 0 {
		t.Fatalf("dry run: %+v, %v", dry, err)
	}

	first, err := seed(ctx, f.runtime, f.middleware, f.practice, operatorEmail, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Applied || !first.Published || first.Revision.Provenance != insurance.ProvenanceImport || first.Revision.CreatedBy != operatorEmail ||
		first.Revision.Reason != importReason || first.Revision.PreviousRevisionID != nil || f.revisions(t) != 1 {
		t.Fatalf("first seed: %+v revision %+v", first, first.Revision)
	}
	active := f.active(t)
	if active.RevisionID != first.Revision.ID {
		t.Fatalf("active after seed: %s", active.RevisionID)
	}
	if served, _ := json.Marshal(active.Catalog); !bytes.Equal(served, bytes.TrimSuffix(export, []byte("\n"))) {
		t.Fatal("the stored revision does not reproduce the export byte for byte")
	}

	validations := f.recorded.validated.Load()
	again, err := seed(ctx, f.runtime, f.middleware, f.practice, operatorEmail, catalog)
	if err != nil || again.Published || again.Revision.ID != first.Revision.ID || f.revisions(t) != 1 || f.recorded.validated.Load() != validations {
		t.Fatalf("second seed: %+v, %v (revisions %d)", again, err, f.revisions(t))
	}

	renamed, _ := writeCatalog(t, bytes.Replace(export, []byte(`"name":"AETNA"`), []byte(`"name":"AETNA RENAMED"`), 1))
	if _, err := seed(ctx, f.runtime, f.middleware, f.practice, operatorEmail, renamed); err == nil || !strings.Contains(err.Error(), "--expected-revision") || f.revisions(t) != 1 {
		t.Fatalf("seed over a different active list: %v (revisions %d)", err, f.revisions(t))
	}
}

func TestUpdatePublishesAnEditedExportAsTheNextRevision(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	original, export, err := readCatalog(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	edited, editedBody := writeCatalog(t, bytes.Replace(export, []byte(`"name":"AETNA"`), []byte(`"name":"AETNA RENAMED"`), 1))
	commit := strings.Repeat("c", 40)
	change := func(expected, reason string) updateCommand {
		return updateCommand{practice: f.practice, expectedRevision: expected, reason: reason, commit: commit, body: editedBody, catalog: edited}
	}

	if _, err := update(ctx, f.runtime, f.middleware, operatorEmail, change(uuid.NewString(), "Synthetic change")); err == nil || !strings.Contains(err.Error(), "without --expected-revision") {
		t.Fatalf("update of a Practice with no list: %v", err)
	}
	var none bytes.Buffer
	if _, err := exportCatalog(ctx, f.runtime, f.practice, "", &none); !errors.Is(err, insurance.ErrNoCatalog) || none.Len() != 0 {
		t.Fatalf("export of a Practice with no list: %v (%d bytes)", err, none.Len())
	}

	first, err := seed(ctx, f.runtime, f.middleware, f.practice, operatorEmail, original)
	if err != nil {
		t.Fatal(err)
	}
	var exported bytes.Buffer
	exportedID, err := exportCatalog(ctx, f.runtime, f.practice, "", &exported)
	if err != nil || exportedID != first.Revision.ID || !bytes.Equal(canonical(t, exported.Bytes()), canonical(t, export)) || !bytes.Contains(exported.Bytes(), []byte("\n  \"carriers\": [\n")) {
		t.Fatalf("export of the active list: %s, %v", exportedID, err)
	}

	for _, operator := range []string{"", "unknown@acuity.test"} {
		if _, err := update(ctx, f.runtime, f.middleware, operator, change(first.Revision.ID, "Synthetic change")); err == nil {
			t.Fatalf("update accepted operator %q", operator)
		}
	}
	if _, err := update(ctx, f.runtime, f.middleware, operatorEmail, change(uuid.NewString(), "Synthetic change")); err == nil || !strings.Contains(err.Error(), "export again") || f.revisions(t) != 1 {
		t.Fatalf("update against an unknown revision: %v (revisions %d)", err, f.revisions(t))
	}
	f.recorded.verdict.Store(rejectedVerdict)
	if _, err := update(ctx, f.runtime, f.middleware, operatorEmail, change(first.Revision.ID, "Synthetic change")); err == nil || !strings.Contains(err.Error(), "note_rule_not_encoded") || f.revisions(t) != 1 {
		t.Fatalf("update of a rejected catalog: %v (revisions %d)", err, f.revisions(t))
	}
	f.recorded.verdict.Store(`{"valid":true}`)

	second, err := update(ctx, f.runtime, f.middleware, operatorEmail, change(first.Revision.ID, "Synthetic change"))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Applied || !second.Published || second.SourceGitCommit != commit || second.Revision.Provenance != insurance.ProvenanceImport ||
		second.Revision.CreatedBy != operatorEmail || second.Revision.Reason != "Synthetic change" || second.Revision.PreviousRevisionID == nil ||
		*second.Revision.PreviousRevisionID != first.Revision.ID || f.revisions(t) != 2 {
		t.Fatalf("update: %+v revision %+v", second, second.Revision)
	}
	if active := f.active(t); active.RevisionID != second.Revision.ID {
		t.Fatalf("active after update: %s", active.RevisionID)
	} else if served, _ := json.Marshal(active.Catalog); !bytes.Equal(served, bytes.TrimSuffix(editedBody, []byte("\n"))) {
		t.Fatal("the active list is not the edited file")
	}

	validations := f.recorded.validated.Load()
	again, err := update(ctx, f.runtime, f.middleware, operatorEmail, change(first.Revision.ID, "Synthetic change"))
	if err != nil || again.Published || again.Revision.ID != second.Revision.ID || f.revisions(t) != 2 || f.recorded.validated.Load() != validations {
		t.Fatalf("rerun of the same update: %+v, %v (revisions %d)", again, err, f.revisions(t))
	}
	if _, err := update(ctx, f.runtime, f.middleware, operatorEmail, change(first.Revision.ID, "A different reason")); err == nil || !strings.Contains(err.Error(), "export again") || f.revisions(t) != 2 {
		t.Fatalf("a second update against a stale export: %v (revisions %d)", err, f.revisions(t))
	}

	var earlier bytes.Buffer
	if id, err := exportCatalog(ctx, f.runtime, f.practice, first.Revision.ID, &earlier); err != nil || id != first.Revision.ID || !bytes.Equal(canonical(t, earlier.Bytes()), canonical(t, export)) {
		t.Fatalf("export of an earlier revision: %s, %v", id, err)
	}
	if _, err := exportCatalog(ctx, f.runtime, f.practice, uuid.NewString(), &bytes.Buffer{}); !errors.Is(err, insurance.ErrNotFound) {
		t.Fatalf("export of an unknown revision: %v", err)
	}
}

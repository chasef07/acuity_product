package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

type recordedMiddleware struct {
	export    atomic.Value
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
	case r.Method == http.MethodGet && r.URL.Path == "/api/insurance/catalog":
		_, _ = w.Write(m.export.Load().([]byte))
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

func TestSeedPublishesTheMiddlewareExportOnceAsRevisionOne(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	const email = "operator@acuity.test"
	if _, err := access.New(db.Owner, nil).Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "insurance-import-test", PlatformOperators: []string{email}, Practices: []access.PracticeProvision{
		{Key: "abita-eye-group", Name: "Synthetic Abita", Locations: []access.LocationProvision{{Key: "spring-hill", Name: "Spring Hill", AbitaOfficeKeys: []string{"spring-hill"}}}},
	}}); err != nil {
		t.Fatal(err)
	}
	runtime := db.As(testdb.Portal)
	accessModule := access.New(runtime, nil)
	if _, err := accessModule.DiscoverActor(ctx, access.Identity{Subject: "operator-subject", Email: email, EmailVerified: true}); err != nil {
		t.Fatal(err)
	}
	var practice string
	if err := db.Owner.QueryRow(ctx, `SELECT id::text FROM access_practices WHERE provisioning_key='abita-eye-group'`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	export, err := os.ReadFile("../../internal/insurance/testdata/export.json")
	if err != nil {
		t.Fatal(err)
	}
	recorded := &recordedMiddleware{}
	recorded.export.Store(export)
	recorded.verdict.Store(`{"valid":true}`)
	server := httptest.NewServer(recorded)
	defer server.Close()
	middleware, err := insurance.NewMiddlewareClient(server.URL, "synthetic-middleware-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	revisions := func() int {
		t.Helper()
		var count int
		if err := db.Owner.QueryRow(ctx, `SELECT count(*) FROM insurance_revisions WHERE practice_id=$1`, practice).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	for _, operator := range []string{"", "unknown@acuity.test"} {
		if _, err := seed(ctx, runtime, middleware, practice, operator); err == nil {
			t.Fatalf("seed accepted operator %q", operator)
		}
	}

	recorded.verdict.Store(`{"valid":false,"errors":[{"region":"south_florida","table":"doctors","planId":"aetna-better-health","rule":"note_rule_not_encoded","message":"Synthetic note rule is not encoded."}]}`)
	if _, err := seed(ctx, runtime, middleware, practice, email); err == nil || !strings.Contains(err.Error(), "south_florida/doctors/aetna-better-health note_rule_not_encoded: Synthetic note rule is not encoded.") {
		t.Fatalf("seed of a rejected catalog: %v", err)
	}
	if _, err := check(ctx, middleware, practice); err == nil || !strings.Contains(err.Error(), "note_rule_not_encoded") {
		t.Fatalf("dry run of a rejected catalog: %v", err)
	}
	if count := revisions(); count != 0 {
		t.Fatalf("a rejected seed stored %d revisions", count)
	}
	recorded.verdict.Store(`{"valid":true}`)

	dry, err := check(ctx, middleware, practice)
	if err != nil || dry.Applied || dry.Carriers != 50 || dry.Regions != 2 || dry.Tables != 3 || revisions() != 0 {
		t.Fatalf("dry run: %+v, %v", dry, err)
	}

	first, err := seed(ctx, runtime, middleware, practice, email)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Applied || !first.Published || first.Revision.Provenance != insurance.ProvenanceImport || first.Revision.CreatedBy != email ||
		first.Revision.Reason != importReason || first.Revision.PreviousRevisionID != nil || revisions() != 1 {
		t.Fatalf("first seed: %+v revision %+v", first, first.Revision)
	}
	module, err := insurance.NewRevisions(runtime, accessModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	active, err := module.Read(ctx, access.ServiceIdentity{Subject: "abita-eye-group-middleware", PracticeID: practice, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadInsurance}})
	if err != nil || active.RevisionID != first.Revision.ID {
		t.Fatalf("active after seed: %s, %v", active.RevisionID, err)
	}
	if served, _ := json.Marshal(active.Catalog); !bytes.Equal(served, bytes.TrimSuffix(export, []byte("\n"))) {
		t.Fatal("the stored revision does not reproduce the export byte for byte")
	}

	validations := recorded.validated.Load()
	again, err := seed(ctx, runtime, middleware, practice, email)
	if err != nil || again.Published || again.Revision.ID != first.Revision.ID || revisions() != 1 || recorded.validated.Load() != validations {
		t.Fatalf("second seed: %+v, %v (revisions %d)", again, err, revisions())
	}

	recorded.export.Store(bytes.Replace(export, []byte(`"name":"AETNA"`), []byte(`"name":"AETNA RENAMED"`), 1))
	if _, err := seed(ctx, runtime, middleware, practice, email); err == nil || revisions() != 1 {
		t.Fatalf("seed over a different active list: %v (revisions %d)", err, revisions())
	}
}

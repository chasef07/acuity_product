package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

func TestKnowledgeHTTPAllOfficesPreserveTenantRouteAndRevisionIsolation(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	accessModule := access.New(pool, time.Now)
	canonical := []struct {
		key    string
		tenant int
	}{
		{"spring-hill", 0}, {"crystal-river", 0}, {"hollywood", 0},
		{"sweetwater", 0}, {"north-miami-beach-optical", 0},
		{"rheumatology-demo", 1}, {"ophthalmology-demo", 1}, {"new-tampa-demo", 1},
	}
	practices := []access.PracticeProvision{
		{Key: "synthetic-abita", Name: "Synthetic practice A"},
		{Key: "synthetic-demo", Name: "Synthetic practice B"},
	}
	for _, office := range canonical {
		routes := []string{office.key}
		switch office.key {
		case "rheumatology-demo":
			routes = append(routes, "dev")
		case "new-tampa-demo":
			routes = append(routes, "mental-health-demo")
		}
		practices[office.tenant].Locations = append(practices[office.tenant].Locations,
			access.LocationProvision{Key: office.key, Name: "Synthetic " + office.key, AbitaOfficeKeys: routes})
	}
	// This Product handoff route has its own Location. Agent optical trunks use
	// the canonical sweetwater key, never an inferred corpus alias.
	practices[0].Locations = append(practices[0].Locations,
		access.LocationProvision{Key: "sweetwater-optical", Name: "Synthetic optical", AbitaOfficeKeys: []string{"sweetwater-optical"}})
	for i := range practices {
		// Identical route keys across Practices must still select different facts.
		practices[i].Locations = append(practices[i].Locations,
			access.LocationProvision{Key: "shared-office", Name: "Synthetic shared route", AbitaOfficeKeys: []string{"shared-office"}})
	}
	if _, err := accessModule.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "knowledge-isolation-test", Practices: practices}); err != nil {
		t.Fatal(err)
	}
	var practiceIDs [2]string
	var credentials [2]access.ServiceCredential
	for i, practice := range practices {
		if err := pool.QueryRow(ctx, `SELECT id::text FROM access_practices WHERE provisioning_key=$1`, practice.Key).Scan(&practiceIDs[i]); err != nil {
			t.Fatal(err)
		}
		credentials[i] = access.ServiceCredential{
			Token: fmt.Sprintf("synthetic-service-token-%d", i),
			Identity: access.ServiceIdentity{
				Subject: fmt.Sprintf("synthetic-agent-%d", i), PracticeID: practiceIDs[i],
				LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge},
			},
		}
	}
	authenticator, err := access.NewServiceAuthenticator(credentials[0], credentials[1])
	if err != nil {
		t.Fatal(err)
	}
	// Every passage and query deliberately has the same embedding. Passing this
	// test therefore proves authorization/scope selection, not convenient ranking.
	embeddings := &knowledgeEmbeddingDouble{}
	module, err := knowledge.New(pool, accessModule, embeddings, knowledge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	handler := api.Handler(&Server{role: "portal-api", config: Config{RequestTimeout: time.Second}, knowledge: module, serviceAuth: authenticator})
	search := func(tenant int, office string, status int) knowledge.SearchResult {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/v1/agent/knowledge/search", strings.NewReader(`{"query":"What are the office hours?"}`))
		r.Header.Set("Authorization", "Bearer "+credentials[tenant].Token)
		r.Header.Set("X-Office-Key", office)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("tenant %d office %s status=%d want=%d body=%s", tenant, office, w.Code, status, w.Body)
		}
		var result knowledge.SearchResult
		if status == http.StatusOK || status == http.StatusServiceUnavailable {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		if status == http.StatusServiceUnavailable && (result.Outcome != "temporary_failure" || len(result.Passages) != 0) {
			t.Fatalf("failure returned fallback facts: %+v", result)
		}
		return result
	}
	manifest := func(tenant int, office, text string) knowledge.ImportCommand {
		return knowledge.ImportCommand{ID: uuid.NewString(), PracticeID: practiceIDs[tenant], OfficeKey: office,
			Sections:   []knowledge.Section{{ID: "hours", Title: "Hours", Text: text}},
			Provenance: "approved synthetic fixture", Reason: "isolation test", ActorSubject: "synthetic-operator"}
	}
	check := func(tenant int, office, revision, text string) {
		t.Helper()
		result := search(tenant, office, http.StatusOK)
		if result.Outcome != "found" || result.RevisionID == nil || *result.RevisionID != revision || len(result.Passages) != 1 || result.Passages[0].RevisionID != revision || result.Passages[0].Text != text {
			t.Fatalf("tenant %d office %s returned wrong corpus/revision: %+v", tenant, office, result)
		}
	}
	manifests := make(map[string]knowledge.ImportCommand)
	for _, office := range canonical {
		command := manifest(office.tenant, office.key, "Synthetic hours for "+office.key+" are available only in this corpus.")
		if _, err := module.ReplaceCorpus(ctx, command); err != nil {
			t.Fatal(err)
		}
		manifests[office.key] = command
		check(office.tenant, office.key, command.ID, command.Sections[0].Text)
		before := embeddings.calls
		search(1-office.tenant, office.key, http.StatusForbidden)
		if embeddings.calls != before {
			t.Fatal("cross-Practice request reached the embedding provider")
		}
	}
	search(0, "sweetwater-optical", http.StatusServiceUnavailable)
	search(1, "dev", http.StatusServiceUnavailable)
	search(1, "mental-health-demo", http.StatusServiceUnavailable)
	for tenant := range practices {
		command := manifest(tenant, "shared-office", fmt.Sprintf("Synthetic tenant %d opens at a different hour.", tenant))
		if _, err := module.ReplaceCorpus(ctx, command); err != nil {
			t.Fatal(err)
		}
		manifests[fmt.Sprintf("shared-%d", tenant)] = command
	}
	for tenant := range practices {
		command := manifests[fmt.Sprintf("shared-%d", tenant)]
		check(tenant, command.OfficeKey, command.ID, command.Sections[0].Text)
	}
	// Two populated routes sharing a Location remain independent documents.
	alias := manifest(1, "dev", "Synthetic legacy route hours differ from the canonical route.")
	if _, err := module.ReplaceCorpus(ctx, alias); err != nil {
		t.Fatal(err)
	}
	check(1, "dev", alias.ID, alias.Sections[0].Text)
	rheumatology := manifests["rheumatology-demo"]
	check(1, rheumatology.OfficeKey, rheumatology.ID, rheumatology.Sections[0].Text)

	original := manifests["spring-hill"]
	replacement := manifest(0, original.OfficeKey, "Synthetic corrected hours supersede the previous hours.")
	replacement.ExpectedRevisionID = original.ID
	if _, err := module.ReplaceCorpus(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	check(0, replacement.OfficeKey, replacement.ID, replacement.Sections[0].Text)
	restore := manifest(0, original.OfficeKey, original.Sections[0].Text)
	restore.ExpectedRevisionID = replacement.ID
	embeddings.fail = true
	if _, err := module.ReplaceCorpus(ctx, restore); !errors.Is(err, knowledge.ErrUnavailable) {
		t.Fatalf("failed restore=%v", err)
	}
	search(0, original.OfficeKey, http.StatusServiceUnavailable)
	embeddings.fail = false
	check(0, replacement.OfficeKey, replacement.ID, replacement.Sections[0].Text)
	if _, err := module.ReplaceCorpus(ctx, restore); err != nil {
		t.Fatal(err)
	}
	check(0, restore.OfficeKey, restore.ID, restore.Sections[0].Text)
	if _, err := module.ReplaceCorpus(ctx, original); !errors.Is(err, knowledge.ErrConflict) {
		t.Fatalf("superseded import replay=%v", err)
	}
	check(1, rheumatology.OfficeKey, rheumatology.ID, rheumatology.Sections[0].Text)
}

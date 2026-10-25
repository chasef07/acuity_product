package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
)

type countingEmbedder struct{ calls atomic.Int64 }

func (e *countingEmbedder) Embed(ctx context.Context, texts []string, task knowledge.TaskType) ([][]float32, error) {
	e.calls.Add(1)
	return constantEmbedder{}.Embed(ctx, texts, task)
}

type serviceTokens map[string]access.ServiceIdentity

func (tokens serviceTokens) AuthenticateService(_ context.Context, token string) (access.ServiceIdentity, error) {
	if identity, ok := tokens[token]; ok {
		return identity, nil
	}
	return access.ServiceIdentity{}, access.ErrDenied
}

type officeKnowledgeRead struct {
	RevisionID string              `json:"revisionId"`
	Entries    []knowledge.Section `json:"entries"`
}

func TestAgentReadsTheWholeActiveOfficeKnowledgeRevision(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	runtimePool := db.As(testdb.Portal)
	accessModule := access.New(runtimePool, nil)
	if _, err := access.New(pool, nil).Provision(ctx, access.Provisioning{
		Environment: "test",
		RequestedBy: "knowledge-read-http-test",
		Practices: []access.PracticeProvision{
			{
				Key:  "synthetic-read",
				Name: "Synthetic read practice",
				Locations: []access.LocationProvision{
					{Key: "main", Name: "Main", AbitaOfficeKeys: []string{"main-office"}},
					{Key: "empty", Name: "Empty", AbitaOfficeKeys: []string{"empty-office"}},
				},
				AccessGrants: []access.AccessGrantProvision{
					{Key: "read-admin", Email: "admin@read.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
				},
			},
			{
				Key:       "synthetic-other",
				Name:      "Synthetic other practice",
				Locations: []access.LocationProvision{{Key: "other", Name: "Other", AbitaOfficeKeys: []string{"other-office"}}},
			},
		},
	}); err != nil {
		t.Fatalf("provision knowledge read fixture: %v", err)
	}
	admin := access.Identity{Subject: "read-admin-subject", Email: "admin@read.test", EmailVerified: true}
	practice := testaccess.Activate(t, accessModule, admin).Practice.ID
	var other, mainLocation string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_practices WHERE provisioning_key='synthetic-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1 AND provisioning_key='main'`, practice).Scan(&mainLocation); err != nil {
		t.Fatal(err)
	}

	embedder := &countingEmbedder{}
	knowledgeModule, err := knowledge.New(runtimePool, accessModule, embedder, knowledge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	savedOrderNotIDOrder := []knowledge.Section{
		{ID: "zeta", Title: "Parking", Text: "Synthetic parking is behind the building."},
		{ID: "alpha", Title: "Hours", Text: "The synthetic office closes at five."},
		{ID: "mid", Title: "Fax", Text: "The synthetic fax number is not supplied."},
	}
	initial, err := knowledgeModule.ReplaceCorpus(ctx, knowledge.ImportCommand{ID: uuid.NewString(), PracticeID: practice, OfficeKey: "main-office", Sections: savedOrderNotIDOrder, Provenance: "approved synthetic fixture", Reason: "initial import", ActorSubject: "synthetic-operator"})
	if err != nil {
		t.Fatalf("import main office knowledge: %v", err)
	}
	if _, err := knowledgeModule.ReplaceCorpus(ctx, knowledge.ImportCommand{ID: uuid.NewString(), PracticeID: other, OfficeKey: "other-office", Sections: []knowledge.Section{{ID: "secret", Title: "Other practice", Text: "Other practice private fact."}}, Provenance: "approved synthetic fixture", Reason: "initial import", ActorSubject: "synthetic-operator"}); err != nil {
		t.Fatalf("import other office knowledge: %v", err)
	}

	agent := access.ServiceIdentity{Subject: "read-agent", PracticeID: practice, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge}}
	otherAgent := access.ServiceIdentity{Subject: "other-agent", PracticeID: other, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge}}
	noCapability := access.ServiceIdentity{Subject: "no-capability-agent", PracticeID: practice, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityHumanHandoff}}
	serviceAuthenticator := serviceTokens{"agent-token": agent, "other-agent-token": otherAgent, "no-capability-token": noCapability}
	workModule := work.New(runtimePool, accessModule, nil)
	handler, err := httpapi.NewPortal(httpapi.Config{AcquireTimeout: time.Second, RequestTimeout: 5 * time.Second}, runtimePool, httpapi.PortalDependencies{
		Access:               accessModule,
		Authenticator:        staticAuthenticator{"admin-token": admin},
		Calling:              humancalling.New(runtimePool, accessModule, httpCallingProvider{}, humancalling.Config{}, nil),
		Interactions:         interaction.New(runtimePool, accessModule, nil),
		Messaging:            messaging.New(runtimePool, accessModule, workModule, nil, messaging.Config{}, nil),
		Work:                 workModule,
		Workspace:            workspace.New(runtimePool, accessModule),
		Knowledge:            knowledgeModule,
		ServiceAuthenticator: serviceAuthenticator,
	})
	if err != nil {
		t.Fatalf("create portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	readWithBody := func(t *testing.T, token, office, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/agent/knowledge/read", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Office-Key", office)
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode, readBody(t, response)
	}
	read := func(t *testing.T, token, office string) (int, string) {
		t.Helper()
		return readWithBody(t, token, office, `{}`)
	}
	readOK := func(t *testing.T) officeKnowledgeRead {
		t.Helper()
		status, body := read(t, "agent-token", "main-office")
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		decoder := json.NewDecoder(bytes.NewReader([]byte(body)))
		decoder.DisallowUnknownFields()
		var result officeKnowledgeRead
		if err := decoder.Decode(&result); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return result
	}
	observations := func(t *testing.T) int {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_retrieval_observations`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	t.Run("authorized service reads every active entry in saved order without embedding", func(t *testing.T) {
		embeddingsBefore := embedder.calls.Load()
		result := readOK(t)
		if result.RevisionID != initial.ID || !slices.Equal(result.Entries, savedOrderNotIDOrder) {
			t.Fatalf("read=%+v; want revision %s entries %+v", result, initial.ID, savedOrderNotIDOrder)
		}
		if calls := embedder.calls.Load() - embeddingsBefore; calls != 0 {
			t.Fatalf("read made %d embedding calls", calls)
		}
	})

	t.Run("a successful read records one found observation that history reports as last agent use", func(t *testing.T) {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_retrieval_observations WHERE practice_id=$1 AND office_key='main-office'`, practice).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("observations after one read=%d", count)
		}
		var revision, outcome string
		var sectionIDs []string
		var observedAt time.Time
		if err := pool.QueryRow(ctx, `SELECT revision_id::text, outcome, section_ids, created_at FROM knowledge_retrieval_observations WHERE practice_id=$1 AND office_key='main-office'`, practice).Scan(&revision, &outcome, &sectionIDs, &observedAt); err != nil {
			t.Fatal(err)
		}
		if revision != initial.ID || outcome != "found" || !slices.Equal(sectionIDs, []string{"zeta", "alpha", "mid"}) {
			t.Fatalf("observation revision=%s outcome=%s sections=%v", revision, outcome, sectionIDs)
		}
		payload, err := json.Marshal(map[string]string{"practiceId": practice, "locationId": mainLocation})
		if err != nil {
			t.Fatal(err)
		}
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/knowledge/revisions/query", "admin-token", payload)
		var history struct {
			Revisions []struct {
				ID                string     `json:"id"`
				Active            bool       `json:"active"`
				LastAgentSearchAt *time.Time `json:"lastAgentSearchAt"`
				AgentSearchCount  int        `json:"agentSearchCount"`
			} `json:"revisions"`
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("history status=%d body=%s", response.StatusCode, readBody(t, response))
		}
		decode(t, response, &history)
		if len(history.Revisions) != 1 {
			t.Fatalf("history=%+v", history)
		}
		row := history.Revisions[0]
		if row.ID != initial.ID || !row.Active || row.AgentSearchCount != 1 || row.LastAgentSearchAt == nil || !row.LastAgentSearchAt.Equal(observedAt) {
			t.Fatalf("history row=%+v; want last agent use at %v", row, observedAt)
		}
	})

	t.Run("the next read after a publish returns the new revision", func(t *testing.T) {
		published := []knowledge.Section{
			{ID: "new-first", Title: "Directions", Text: "Synthetic directions: turn left at the light."},
			{ID: "alpha", Title: "Hours", Text: "The synthetic office now closes at half past five."},
		}
		body := publishBody{PracticeID: practice, LocationID: mainLocation, RevisionID: uuid.NewString(), ExpectedRevisionID: initial.ID, Reason: "Correct closing time", Sections: published}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/knowledge/revisions", "admin-token", payload)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("publish status=%d body=%s", response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
		result := readOK(t)
		if result.RevisionID != body.RevisionID || !slices.Equal(result.Entries, published) {
			t.Fatalf("read after publish=%+v; want revision %s entries %+v", result, body.RevisionID, published)
		}
		var sectionIDs []string
		if err := pool.QueryRow(ctx, `SELECT section_ids FROM knowledge_retrieval_observations WHERE revision_id=$1 AND outcome='found'`, body.RevisionID).Scan(&sectionIDs); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(sectionIDs, []string{"new-first", "alpha"}) {
			t.Fatalf("observed sections=%v", sectionIDs)
		}
	})

	for _, tc := range []struct {
		name, token, office string
		status              int
	}{
		{"unauthenticated", "invalid-token", "main-office", http.StatusUnauthorized},
		{"service without the knowledge capability", "no-capability-token", "main-office", http.StatusForbidden},
		{"another practice's office key", "agent-token", "other-office", http.StatusForbidden},
		{"another practice's service", "other-agent-token", "main-office", http.StatusForbidden},
		{"unknown office key", "agent-token", "unknown-office", http.StatusForbidden},
	} {
		t.Run("denies "+tc.name+" without entries", func(t *testing.T) {
			before := observations(t)
			status, body := read(t, tc.token, tc.office)
			if status != tc.status || strings.Contains(body, "entries") || strings.Contains(body, "synthetic") || strings.Contains(body, "private fact") {
				t.Fatalf("status=%d body=%s", status, body)
			}
			if after := observations(t); after != before {
				t.Fatalf("denied read recorded %d observations", after-before)
			}
		})
	}

	t.Run("office with no active revision is unavailable", func(t *testing.T) {
		before := observations(t)
		status, body := read(t, "agent-token", "empty-office")
		if status != http.StatusServiceUnavailable || strings.Contains(body, "entries") {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if after := observations(t); after != before {
			t.Fatalf("unavailable read recorded %d observations", after-before)
		}
	})

	t.Run("request body must be empty JSON", func(t *testing.T) {
		if status, body := readWithBody(t, "agent-token", "main-office", `{"officeKey":"other-office"}`); status != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", status, body)
		}
	})
}

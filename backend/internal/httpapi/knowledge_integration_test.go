package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type knowledgeEmbeddingDouble struct {
	fail  bool
	calls int
}

func (e *knowledgeEmbeddingDouble) Embed(_ context.Context, texts []string, _ knowledge.TaskType) ([][]float32, error) {
	e.calls++
	if e.fail {
		return nil, errors.New("private provider diagnostics must not escape")
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, knowledge.Dimensions)
		out[i][0] = 1
	}
	return out, nil
}

type knowledgeServiceAuth map[string]access.ServiceIdentity

func (a knowledgeServiceAuth) AuthenticateService(_ context.Context, token string) (access.ServiceIdentity, error) {
	if id, ok := a[token]; ok {
		return id, nil
	}
	return access.ServiceIdentity{}, access.ErrDenied
}
func TestKnowledgeHTTPServiceScopeAndRuntimeDatabaseRole(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := access.New(pool, time.Now)
	_, err := a.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "knowledge-test", Practices: []access.PracticeProvision{{Key: "synthetic-knowledge", Name: "Synthetic practice", Locations: []access.LocationProvision{{Key: "one", Name: "Synthetic location", AbitaOfficeKeys: []string{"office-a", "office-b"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	var practice string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_practices WHERE provisioning_key='synthetic-knowledge'`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	embeddings := &knowledgeEmbeddingDouble{}
	module, err := knowledge.New(pool, a, embeddings, knowledge.Config{MinSimilarity: .52})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := module.ReplaceCorpus(ctx, knowledge.ImportCommand{ID: uuid.NewString(), PracticeID: practice, OfficeKey: "office-a", Sections: []knowledge.Section{{ID: "hours", Title: "Hours", Text: "The synthetic office closes at five. Saturday hours are not supplied."}}, Provenance: "approved synthetic source", Reason: "test", ActorSubject: "test-operator"})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"acuity_portal", "acuity_provider", "acuity_realtime", "acuity_worker", "acuity_auth", "acuity_migrate"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			if _, err := pool.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := migrations.ApplyRuntimeGrants(ctx, pool); err != nil {
		t.Fatal(err)
	}
	config := pool.Config().Copy()
	config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, `SET ROLE acuity_portal`)
		return err
	}
	runtimePool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimePool.Close()
	module, err = knowledge.New(runtimePool, access.New(runtimePool, time.Now), embeddings, knowledge.Config{MinSimilarity: .52})
	if err != nil {
		t.Fatal(err)
	}
	// Read-capable runtime cannot import or modify a corpus pointer.
	if _, err := runtimePool.Exec(ctx, `UPDATE knowledge_corpora SET revision_id=NULL WHERE practice_id=$1`, practice); err == nil {
		t.Fatal("runtime may update corpus")
	}
	service := access.ServiceIdentity{Subject: "agent", PracticeID: practice, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge}}
	wrongPractice := service
	wrongPractice.PracticeID = uuid.NewString()
	noCapability := service
	noCapability.Capabilities = nil
	s := &Server{role: "portal-api", config: Config{RequestTimeout: time.Second}, knowledge: module, serviceAuth: knowledgeServiceAuth{"agent": service, "wrong-practice": wrongPractice, "no-capability": noCapability}}
	handler := api.Handler(s)
	for _, tc := range []struct {
		name, token, office, body string
		status                    int
	}{
		{"valid", "agent", "office-a", `{"query":"When does everyone head home?"}`, 200},
		{"unauthenticated", "invalid", "office-a", `{"query":"hours"}`, 401},
		{"cross practice", "wrong-practice", "office-a", `{"query":"hours"}`, 403},
		{"missing capability", "no-capability", "office-a", `{"query":"hours"}`, 403},
		{"same location separate route", "agent", "office-b", `{"query":"hours"}`, 503},
		{"unknown office", "agent", "other", `{"query":"hours"}`, 403},
		{"model cannot select scope", "agent", "office-a", `{"query":"hours","officeKey":"office-b"}`, 400},
		{"empty query", "agent", "office-a", `{"query":" "}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/agent/knowledge/search", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("X-Office-Key", tc.office)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("HTTP %d body=%s", w.Code, w.Body)
			}
			if w.Code == 200 {
				var result knowledge.SearchResult
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Outcome != "found" || result.RevisionID == nil || *result.RevisionID != revision.ID {
					t.Fatalf("response=%+v", result)
				}
			}
		})
	}
	embeddings.fail = true
	r := httptest.NewRequest(http.MethodPost, "/v1/agent/knowledge/search", strings.NewReader(`{"query":"hours"}`))
	r.Header.Set("Authorization", "Bearer agent")
	r.Header.Set("X-Office-Key", "office-a")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "temporary_failure") || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("failure response=%d %s", w.Code, w.Body)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_retrieval_observations WHERE revision_id=$1 AND section_ids=ARRAY['hours']`, revision.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("observed successful retrievals=%d", count)
	}
}

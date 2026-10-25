package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCallLabelsHTTPFiltersAndPublicProjection(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	am := access.New(db.As(testdb.Portal), nil)
	staff := access.Identity{Subject: "labels-http-staff", Email: "staff@labels-http.test", EmailVerified: true}
	operator := access.Identity{Subject: "labels-http-operator", Email: "operator@labels-http.test", EmailVerified: true}
	_, err := access.New(pool, nil).Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", PlatformOperators: []string{operator.Email}, Practices: []access.PracticeProvision{{Key: "labels-http", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "main", Name: "Main"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeAll}}}}})
	if err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, am, staff)
	testaccess.Activate(t, am, operator)
	var practice, location, id string
	if err := pool.QueryRow(ctx, `SELECT p.id::text,l.id::text FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='labels-http'`).Scan(&practice, &location); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage) VALUES('agent',$1,$2,'labels-http','+15555550111','+15555550100',now()-interval '1 minute',now(),'COMPLETED',3) RETURNING id::text`, practice, location).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET closeout_payload='{"evaluation":{"classification":{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"complete","intent":"office_information","outcome":"resolved","answers":{"private-label-evidence":"do not expose"},"reason":"private-error"}}}' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	handler, err := newPortalHandler(t, httpapi.Config{AcquireTimeout: time.Second}, db.As(testdb.Portal), am, staticAuthenticator{"staff": staff, "operator": operator})
	if err != nil {
		t.Fatal(err)
	}
	drainAnalyticsFacts(t, db)
	server := httptest.NewServer(handler)
	defer server.Close()
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	intents, outcomes := spec.Components.Schemas["CallIntent"].Value.Enum, spec.Components.Schemas["CallOutcome"].Value.Enum
	if len(intents) == 0 || len(outcomes) == 0 {
		t.Fatal("generated CallIntent and CallOutcome schemas must list enum values")
	}
	var filters []map[string]any
	for _, value := range intents {
		filters = append(filters, map[string]any{"intent": value})
	}
	for _, value := range outcomes {
		filters = append(filters, map[string]any{"outcome": value})
	}
	query := func(path, token string, labels map[string]any, want int) []byte {
		t.Helper()
		labels["practiceId"], labels["range"] = practice, "7d"
		body, _ := json.Marshal(labels)
		response := request(t, server.Client(), http.MethodPost, server.URL+path, token, body)
		raw := readBody(t, response)
		if response.StatusCode != want {
			t.Fatalf("%s status %d want %d: %s", path, response.StatusCode, want, raw)
		}
		return []byte(raw)
	}
	for _, scope := range []struct{ path, token string }{{"/v1/agent-calls/query", "staff"}, {"/v1/operator/ai-analytics/query", "operator"}} {
		raw := query(scope.path, scope.token, map[string]any{"intent": "office_information", "outcome": "resolved"}, 200)
		var page struct {
			Calls []api.AgentCall `json:"calls"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Calls) != 1 || page.Calls[0].Intent == nil || string(*page.Calls[0].Intent) != "office_information" || page.Calls[0].Outcome == nil || string(*page.Calls[0].Outcome) != "resolved" || page.Calls[0].LabelingStatus == nil || string(*page.Calls[0].LabelingStatus) != "complete" {
			t.Fatalf("public list labels missing: %s", raw)
		}
		if strings.Contains(string(raw), "private-label-evidence") || strings.Contains(string(raw), "private-error") {
			t.Fatalf("private evidence leaked: %s", raw)
		}
		for _, invalid := range []map[string]any{{"intent": "invalid", "outcome": "resolved"}, {"intent": "office_information", "outcome": "invalid"}, {"intent": ""}, {"outcome": ""}} {
			raw := query(scope.path, scope.token, invalid, 400)
			if !strings.Contains(string(raw), `"code":"INVALID_REQUEST"`) {
				t.Fatalf("invalid label filter %v error code: %s", invalid, raw)
			}
		}
		for _, filter := range filters {
			raw = query(scope.path, scope.token, filter, 200)
			want := 0
			if filter["intent"] == "office_information" || filter["outcome"] == "resolved" {
				want = 1
			}
			if err := json.Unmarshal(raw, &page); err != nil || len(page.Calls) != want {
				t.Fatalf("filter %v returned %s %v, want %d calls", filter, raw, err, want)
			}
		}
		raw = query(scope.path, scope.token, map[string]any{"intent": "billing_question", "outcome": "resolved"}, 200)
		if err := json.Unmarshal(raw, &page); err != nil || len(page.Calls) != 0 {
			t.Fatalf("intent filter not applied: %s %v", raw, err)
		}
		raw = query(scope.path, scope.token, map[string]any{"intent": "office_information", "outcome": "abandoned"}, 200)
		if err := json.Unmarshal(raw, &page); err != nil || len(page.Calls) != 0 {
			t.Fatalf("outcome filter not applied: %s %v", raw, err)
		}
	}
	for _, scope := range []struct{ path, token string }{{"/v1/agent-calls/" + id, "staff"}, {"/v1/operator/ai-interactions/" + id + "/analytics", "operator"}} {
		response := request(t, server.Client(), http.MethodGet, server.URL+scope.path, scope.token, nil)
		raw := readBody(t, response)
		if response.StatusCode != 200 {
			t.Fatalf("detail %s: %d %s", scope.path, response.StatusCode, raw)
		}
		if !strings.Contains(raw, `"intent":"office_information"`) || !strings.Contains(raw, `"outcome":"resolved"`) || !strings.Contains(raw, `"labelingStatus":"complete"`) {
			t.Fatalf("detail labels missing: %s", raw)
		}
		if strings.Contains(raw, "private-label-evidence") || strings.Contains(raw, "private-error") {
			t.Fatalf("private evidence leaked: %s", raw)
		}
	}
	for _, needsKnowledgeOnly := range []bool{false, true} {
		body, _ := json.Marshal(map[string]any{"practiceId": practice, "range": "7d", "needsKnowledgeOnly": needsKnowledgeOnly})
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/agent-calls/query", "staff", body)
		raw := readBody(t, response)
		var page struct {
			Calls []api.AgentCall `json:"calls"`
		}
		if response.StatusCode != 200 || json.Unmarshal([]byte(raw), &page) != nil || len(page.Calls) != 1 || page.Calls[0].Id.String() != id {
			t.Fatalf("previous web bundle needsKnowledgeOnly=%v must still list every call: %d %s", needsKnowledgeOnly, response.StatusCode, raw)
		}
	}
}

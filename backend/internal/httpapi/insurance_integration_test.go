package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

type constantEmbedder struct{}

func (constantEmbedder) Embed(_ context.Context, texts []string, _ knowledge.TaskType) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = make([]float32, knowledge.Dimensions)
		vectors[i][0] = 1
	}
	return vectors, nil
}

func TestManageAgentReadsFollowLocationAccessAndAbitaOfficeRoutes(t *testing.T) {
	pool := testdb.Open(t)
	accessModule := access.New(pool, nil)
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "insurance-rules-http-test",
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{
				{Key: "hollywood", Name: "Hollywood", AbitaOfficeKeys: []string{"hollywood"}},
				{Key: "sweetwater-optical", Name: "Sweetwater Optical", AbitaOfficeKeys: []string{"sweetwater-optical"}},
				{Key: "unrouted", Name: "Unrouted Office"},
				{Key: "combined", Name: "Combined Office", AbitaOfficeKeys: []string{"crystal-river", "spring-hill"}},
			},
			AccessGrants: []access.AccessGrantProvision{
				{Key: "admin", Email: "admin@abita.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
				{Key: "hollywood-staff", Email: "staff@abita.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"hollywood"}},
			},
		}},
	}); err != nil {
		t.Fatalf("provision insurance fixture: %v", err)
	}
	admin := access.Identity{Subject: "admin-subject", Email: "admin@abita.test", EmailVerified: true}
	staff := access.Identity{Subject: "staff-subject", Email: "staff@abita.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, admin)
	testaccess.Activate(t, accessModule, staff)
	locations := map[string]string{}
	for _, location := range authorization.Locations {
		locations[location.Name] = location.ID
	}

	var mu sync.Mutex
	offices := []string{}
	middleware := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-middleware-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		offices = append(offices, r.URL.Query().Get("office"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plans":[{"planId":"synthetic-hmo-medical","label":"Synthetic HMO","outcome":"accepted"}]}`))
	}))
	defer middleware.Close()
	rules, err := insurance.NewMiddlewareClient(middleware.URL, "synthetic-middleware-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	insuranceModule, err := insurance.New(accessModule, rules)
	if err != nil {
		t.Fatal(err)
	}
	knowledgeModule, err := knowledge.New(pool, accessModule, constantEmbedder{}, knowledge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := knowledgeModule.ReplaceCorpus(context.Background(), knowledge.ImportCommand{
		ID: "00000000-0000-0000-0000-0000000000aa", PracticeID: authorization.Practice.ID, OfficeKey: "hollywood",
		Sections:   []knowledge.Section{{ID: "hours", Title: "Office hours", Text: "The synthetic office closes at six."}},
		Provenance: "approved synthetic fixture", Reason: "initial import", ActorSubject: "operator@abita.test",
	})
	if err != nil {
		t.Fatalf("import synthetic knowledge: %v", err)
	}
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		access.ServiceCredential{Token: "unused-service-token", Identity: access.ServiceIdentity{Subject: "unused-service", PracticeID: "00000000-0000-0000-0000-000000000001", LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}},
		access.ServiceCredential{Token: "unused-secondary-token", Identity: access.ServiceIdentity{Subject: "unused-secondary", PracticeID: "00000000-0000-0000-0000-000000000002", LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	workModule := work.New(pool, accessModule, nil)
	handler, err := httpapi.NewPortal(httpapi.Config{AcquireTimeout: time.Second}, pool, httpapi.PortalDependencies{
		Access:               accessModule,
		Authenticator:        staticAuthenticator{"admin-token": admin, "staff-token": staff},
		Calling:              humancalling.New(pool, accessModule, httpCallingProvider{}, humancalling.Config{}, nil),
		Interactions:         interaction.New(pool, accessModule, nil),
		Messaging:            messaging.New(pool, accessModule, workModule, nil, messaging.Config{}, nil),
		Work:                 workModule,
		Workspace:            workspace.New(pool, accessModule),
		Insurance:            insuranceModule,
		Knowledge:            knowledgeModule,
		ServiceAuthenticator: serviceAuthenticator,
	})
	if err != nil {
		t.Fatalf("create insurance portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	query := func(token, locationID string) (int, string) {
		return post(t, server, "/v1/insurance/plans/query", token, map[string]string{"practiceId": authorization.Practice.ID, "locationId": locationID, "coverage": "medical"})
	}
	readKnowledge := func(token, locationID string) (int, string) {
		return post(t, server, "/v1/knowledge/query", token, map[string]string{"practiceId": authorization.Practice.ID, "locationId": locationID})
	}
	for _, scenario := range []struct {
		name, token, locationID string
		status                  int
		contains                string
	}{
		{"staff at granted Location", "staff-token", locations["Hollywood"], http.StatusOK, `"revision":{"id":"` + revision.ID + `"`},
		{"sections", "staff-token", locations["Hollywood"], http.StatusOK, `"sections":[{"id":"hours","title":"Office hours","text":"The synthetic office closes at six."}]`},
		{"staff outside scope", "staff-token", locations["Sweetwater Optical"], http.StatusForbidden, "ACCESS_DENIED"},
		{"unauthenticated", "wrong-token", locations["Hollywood"], http.StatusUnauthorized, "UNAUTHENTICATED"},
		{"Location without a corpus", "admin-token", locations["Sweetwater Optical"], http.StatusOK, `"sections":[]`},
		{"Location without a route", "admin-token", locations["Unrouted Office"], http.StatusServiceUnavailable, "Knowledge isn't available for this Location."},
		{"Location with several routes", "admin-token", locations["Combined Office"], http.StatusServiceUnavailable, "Knowledge isn't available for this Location."},
	} {
		status, body := readKnowledge(scenario.token, scenario.locationID)
		if status != scenario.status || !strings.Contains(body, scenario.contains) {
			t.Errorf("knowledge %s: status = %d, body = %s", scenario.name, status, body)
		}
	}
	if status, body := readKnowledge("admin-token", locations["Sweetwater Optical"]); status != http.StatusOK || strings.Contains(body, `"revision"`) {
		t.Errorf("knowledge without a corpus has a revision: %s", body)
	}
	for _, scenario := range []struct {
		name, token, locationID string
		status                  int
		contains                string
	}{
		{"staff at granted Location", "staff-token", locations["Hollywood"], http.StatusOK, `"planId":"synthetic-hmo-medical"`},
		{"staff outside scope", "staff-token", locations["Sweetwater Optical"], http.StatusForbidden, "ACCESS_DENIED"},
		{"unknown Location", "admin-token", "00000000-0000-0000-0000-0000000000ff", http.StatusForbidden, "ACCESS_DENIED"},
		{"unauthenticated", "wrong-token", locations["Hollywood"], http.StatusUnauthorized, "UNAUTHENTICATED"},
		{"admin at optical Location", "admin-token", locations["Sweetwater Optical"], http.StatusOK, `"locationId":"` + locations["Sweetwater Optical"] + `"`},
		{"Location without a route", "admin-token", locations["Unrouted Office"], http.StatusServiceUnavailable, "aren't available for this Location"},
		{"Location with several routes", "admin-token", locations["Combined Office"], http.StatusServiceUnavailable, "aren't available for this Location"},
	} {
		status, body := query(scenario.token, scenario.locationID)
		if status != scenario.status || !strings.Contains(body, scenario.contains) {
			t.Errorf("%s: status = %d, body = %s", scenario.name, status, body)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(offices, ",") != "hollywood,sweetwater" {
		t.Fatalf("middleware offices = %v, want only the authorized single-route Locations", offices)
	}
}

func post(t *testing.T, server *httptest.Server, path, token string, payload map[string]string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(payload)
	response := request(t, server.Client(), http.MethodPost, server.URL+path, token, body)
	defer response.Body.Close()
	return response.StatusCode, readBody(t, response)
}

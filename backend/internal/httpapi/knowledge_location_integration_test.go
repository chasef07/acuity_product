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
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func TestKnowledgeReadsFollowLocationAccessAndAbitaOfficeRoutes(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	accessModule := access.New(db.As(testdb.Portal), nil)
	if _, err := access.New(pool, nil).Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "manage-agent-http-test",
		Practices: []access.PracticeProvision{{
			Key:  "synthetic-manage-agent",
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
		t.Fatalf("provision Manage agent fixture: %v", err)
	}
	admin := access.Identity{Subject: "admin-subject", Email: "admin@abita.test", EmailVerified: true}
	staff := access.Identity{Subject: "staff-subject", Email: "staff@abita.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, admin)
	testaccess.Activate(t, accessModule, staff)
	locations := map[string]string{}
	for _, location := range authorization.Locations {
		locations[location.Name] = location.ID
	}

	knowledgeModule, err := knowledge.New(db.As(testdb.Portal), accessModule)
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
	workModule := work.New(db.As(testdb.Portal), accessModule, nil)
	handler, err := httpapi.NewPortal(httpapi.Config{AcquireTimeout: time.Second}, db.As(testdb.Portal), httpapi.PortalDependencies{
		Access:               accessModule,
		Authenticator:        staticAuthenticator{"admin-token": admin, "staff-token": staff},
		Calling:              humancalling.New(db.As(testdb.Portal), accessModule, httpCallingProvider{}, humancalling.Config{}, nil),
		Interactions:         interaction.New(db.As(testdb.Portal), accessModule, nil),
		Messaging:            messaging.New(db.As(testdb.Portal), accessModule, workModule, nil, messaging.Config{}, nil),
		Work:                 workModule,
		Workspace:            workspace.New(db.As(testdb.Portal), accessModule),
		Knowledge:            knowledgeModule,
		ServiceAuthenticator: serviceAuthenticator,
	})
	if err != nil {
		t.Fatalf("create Manage agent portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

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
}

func post(t *testing.T, server *httptest.Server, path, token string, payload map[string]string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(payload)
	response := request(t, server.Client(), http.MethodPost, server.URL+path, token, body)
	defer response.Body.Close()
	return response.StatusCode, readBody(t, response)
}

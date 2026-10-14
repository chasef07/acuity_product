package httpapi_test

import (
	"context"
	"encoding/json"
	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestAgent444CapturedTasksAndStaffGroupCommands(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(
		context.Background(),
		access.Provisioning{
			Environment: "test",
			RequestedBy: "ai-task-http-test",
			Practices: []access.PracticeProvision{{
				Key:  "calling-practice",
				Name: "Calling Practice",
				Locations: []access.LocationProvision{{
					Key:             "calling-location",
					Name:            "Calling Location",
					AbitaOfficeKeys: []string{"sweetwater"},
				}},
				AccessGrants: []access.AccessGrantProvision{{
					Key:           "calling-staff",
					Email:         "staff@calling.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
				}},
			}},
		},
	)
	if err != nil {
		t.Fatalf("provision staff Task HTTP fixture: %v", err)
	}
	staffIdentity := access.Identity{
		Subject:       "calling-staff-subject",
		Email:         "staff@calling.test",
		EmailVerified: true,
	}
	testaccess.Activate(t, accessModule, staffIdentity)
	var practiceID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM access_practices
		WHERE provisioning_key = 'calling-practice'
	`).Scan(&practiceID); err != nil {
		t.Fatalf("load staff Task HTTP Practice: %v", err)
	}
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		access.ServiceCredential{
			Token: "demo-token",
			Identity: access.ServiceIdentity{
				Subject:       "acuity-demo",
				PracticeID:    practiceID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityCreateTask,
					access.ServiceCapabilityHumanHandoff,
				},
			},
		},
		access.ServiceCredential{
			Token: "production-token",
			Identity: access.ServiceIdentity{
				Subject:       "abita-eye-group",
				PracticeID:    practiceID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityCreateTask,
					access.ServiceCapabilityHumanHandoff,
					access.ServiceCapabilityIngestAIInteraction,
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("new staff Task service authenticator: %v", err)
	}
	handler, err := httpapi.NewPortal(
		httpapi.Config{
			AllowedOrigins: []string{"http://localhost:3000"},
			AcquireTimeout: 500 * time.Millisecond,
		},
		pool,
		httpapi.PortalDependencies{
			Access:        accessModule,
			Authenticator: staticAuthenticator{"staff-token": staffIdentity},
			Calling: humancalling.New(
				pool,
				accessModule,
				httpCallingProvider{},
				humancalling.Config{},
				nil,
			),
			Interactions:         interaction.New(pool, accessModule, func() time.Time { return now }),
			Messaging:            messaging.New(pool, accessModule, work.New(pool, accessModule, nil), nil, messaging.Config{}, nil),
			Work:                 work.New(pool, accessModule, func() time.Time { return now }),
			Workspace:            workspace.New(pool, accessModule),
			ServiceAuthenticator: serviceAuthenticator,
		},
	)
	if err != nil {
		t.Fatalf("new staff Task HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	captured, err := os.ReadFile("testdata/agent-444-staff-tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	var payloads []json.RawMessage
	if err := json.Unmarshal(captured, &payloads); err != nil {
		t.Fatal(err)
	}
	for _, payload := range payloads {
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/tasks", "production-token", payload)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("agent payload rejected: %d %s", response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
	query := func() api.TaskPage {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"practiceId": practiceID, "grouped": true, "responsibility": "mine"})
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/tasks/query", "staff-token", body)
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("query: %d %s", response.StatusCode, readBody(t, response))
		}
		var page api.TaskPage
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := query()
	if page.Counts.Tasks != len(payloads) {
		t.Fatalf("underlying Tasks: %d, want %d", page.Counts.Tasks, len(payloads))
	}
	var optical api.Task
	for _, task := range page.Items {
		if task.Category != nil && *task.Category == "optical" {
			optical = task
		}
	}
	if optical.GroupMembers == nil || len(*optical.GroupMembers) < 2 {
		t.Fatal("shared-number Optical requests were not preserved")
	}
	target := (*optical.GroupMembers)[0]
	move, _ := json.Marshal(map[string]any{"expectedVersion": target.Version, "category": "medication"})
	response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/tasks/"+target.Id.String()+"/category", "staff-token", move)
	if response.StatusCode != 200 {
		t.Fatalf("move: %d %s", response.StatusCode, readBody(t, response))
	}
	var moved api.Task
	if err := json.NewDecoder(response.Body).Decode(&moved); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if moved.Category == nil || *moved.Category != "medication" {
		t.Fatalf("moved Task category: %v", moved.Category)
	}
	refreshed := query()
	if refreshed.Counts.Tasks != len(payloads) {
		t.Fatal("metadata changed open work count")
	}
	for _, payload := range payloads {
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/tasks", "production-token", payload)
		if response.StatusCode != 200 {
			t.Fatalf("replay: %d %s", response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
}

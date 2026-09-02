package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPortalAccessDependencyFailureIsRetryableWithoutRevokingAccess(t *testing.T) {
	owner := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	staff := access.Identity{Subject: "dependency-staff", Email: "staff@dependency.synthetic.test", EmailVerified: true}
	operator := access.Identity{Subject: "dependency-operator", Email: "operator@dependency.synthetic.test", EmailVerified: true}
	unscoped := access.Identity{Subject: "dependency-unscoped", Email: "unscoped@dependency.synthetic.test", EmailVerified: true}
	ownerAccess := access.New(owner, clock)
	if _, err := ownerAccess.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "access-dependency-regression",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key: "dependency", Name: "Dependency Practice",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "staff", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	authorized := testaccess.Activate(t, ownerAccess, staff)
	testaccess.Activate(t, ownerAccess, operator)
	config := owner.Config().Copy()
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	database, err := productpostgres.NewPortalExecutor(pool, productpostgres.ExecutorConfig{
		AcquireTimeout: 150 * time.Millisecond, OperationTimeout: time.Second,
		StatementTimeout: 80 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	accessModule := access.New(database, clock)
	workModule := work.New(database, accessModule, clock)
	serviceAuth, err := access.NewServiceAuthenticator(access.ServiceCredential{
		Token: "synthetic-dependency-service-token",
		Identity: access.ServiceIdentity{Subject: "dependency-service", PracticeID: authorized.Practice.ID,
			LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityIngestAIInteraction}},
	}, access.ServiceCredential{
		Token: "synthetic-dependency-secondary-token",
		Identity: access.ServiceIdentity{Subject: "dependency-secondary", PracticeID: "00000000-0000-0000-0000-000000000002",
			LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityIngestAIInteraction}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewPortal(httpapi.Config{
		AcquireTimeout: 150 * time.Millisecond, RequestTimeout: 2 * time.Second,
	}, pool, httpapi.PortalDependencies{
		Access: accessModule, Authenticator: staticAuthenticator{"staff": staff, "operator": operator, "unscoped": unscoped},
		Calling:      humancalling.New(database, accessModule, httpCallingProvider{}, humancalling.Config{}, clock),
		Interactions: interaction.New(database, accessModule, clock),
		Messaging:    messaging.New(database, accessModule, workModule, nil, messaging.Config{}, clock),
		Work:         workModule, Workspace: workspace.New(database, accessModule), ServiceAuthenticator: serviceAuth,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cases := []struct {
		name, method, path, body, token, deniedToken string
	}{
		{"workspace", http.MethodGet, "/v1/engagements/%2B15555550123/timeline?practiceId=" + authorized.Practice.ID, "", "staff", "unscoped"},
		{"analytics", http.MethodPost, "/v1/operator/ai-analytics/query", fmt.Sprintf(`{"practiceId":%q,"range":"24h","limit":10}`, authorized.Practice.ID), "operator", "staff"},
		{"messages", http.MethodPost, "/v1/message-threads/query", fmt.Sprintf(`{"practiceId":%q,"limit":10}`, authorized.Practice.ID), "staff", "unscoped"},
	}
	check := func(t *testing.T, method, path, body, token string, wantStatus int, retryable bool) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != wantStatus {
			t.Fatalf("HTTP status = %d, want %d", response.StatusCode, wantStatus)
		}
		if wantStatus != http.StatusOK {
			var envelope api.ErrorEnvelope
			if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Retryable != retryable {
				t.Fatalf("retryable = %t, want %t", envelope.Error.Retryable, retryable)
			}
		}
	}
	blocker, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Rollback(ctx) })
	if _, err := blocker.Exec(ctx, `LOCK TABLE access_platform_operators, access_memberships IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.name+" dependency failure", func(t *testing.T) {
			check(t, test.method, test.path, test.body, test.token, http.StatusServiceUnavailable, true)
		})
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.name+" recovered", func(t *testing.T) {
			check(t, test.method, test.path, test.body, test.token, http.StatusOK, false)
		})
		t.Run(test.name+" genuine denial", func(t *testing.T) {
			check(t, test.method, test.path, test.body, test.deniedToken, http.StatusForbidden, false)
		})
	}
}

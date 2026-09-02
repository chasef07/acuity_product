package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCallingLookupFailuresRemainRetryable(t *testing.T) {
	owner := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	identity := access.Identity{Subject: "calling-dependency-staff", Email: "staff@calling-dependency.synthetic.test", EmailVerified: true}
	unscoped := access.Identity{Subject: "calling-dependency-unscoped", Email: "unscoped@calling-dependency.synthetic.test", EmailVerified: true}
	ownerAccess := access.New(owner, clock)
	if _, err := ownerAccess.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "calling-dependency-regression",
		Practices: []access.PracticeProvision{{
			Key: "calling-dependency", Name: "Calling Dependency Practice",
			Locations:    []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: identity.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeAll}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	authorized := testaccess.Activate(t, ownerAccess, identity)
	if _, err := owner.Exec(ctx, `
		INSERT INTO human_calling_softphone_leases (user_subject, session_id, lease_expires_at, readiness_updated_at)
		VALUES ($1, 'calling-browser', $2, $3)
	`, identity.Subject, now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	var callID string
	if err := owner.QueryRow(ctx, `
		INSERT INTO human_calling_calls (practice_id, location_id, direction, entry_point, caller_phone)
		VALUES ($1, $2, 'INBOUND', 'STANDALONE', '+15555550123') RETURNING id::text
	`, authorized.Practice.ID, authorized.Locations[0].ID).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `
		INSERT INTO human_calling_call_legs (call_id, role, sequence, staff_subject, staff_session_id, state, answered_at, bridge_pending_at, bridged_at)
		VALUES ($1, 'STAFF', 1, $2, 'calling-browser', 'BRIDGED', $3, $3, $3)
	`, callID, identity.Subject, now); err != nil {
		t.Fatal(err)
	}
	config := owner.Config().Copy()
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	database, err := productpostgres.NewPortalExecutor(pool, productpostgres.ExecutorConfig{
		AcquireTimeout: 150 * time.Millisecond, OperationTimeout: time.Second,
		StatementTimeout: 60 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	accessModule := access.New(database, clock)
	calling := humancalling.New(database, accessModule, httpCallingProvider{}, humancalling.Config{}, clock)
	handler, err := newPortalHandlerWithCalling(t, httpapi.Config{AcquireTimeout: 150 * time.Millisecond, RequestTimeout: 2 * time.Second}, pool,
		accessModule, staticAuthenticator{"staff": identity, "unscoped": unscoped}, calling)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	type lookup struct{ name, method, path, body string }
	read := lookup{"Call", http.MethodGet, "/v1/calling/calls/" + callID, ""}
	readiness := lookup{"readiness", http.MethodPut, "/v1/calling/readiness", `{"sessionId":"calling-browser","registered":true,"microphoneReady":true,"audioReady":true,"sessionHealthy":true,"available":true}`}
	media := lookup{"media token", http.MethodPost, "/v1/calling/media-token", `{"sessionId":"calling-browser"}`}
	hangup := lookup{"hangup", http.MethodPost, "/v1/calling/calls/" + callID + "/hangup", `{"sessionId":"calling-browser"}`}
	check := func(t *testing.T, item lookup, token string, status int, retryable bool) {
		t.Helper()
		response := request(t, server.Client(), item.method, server.URL+item.path, token, []byte(item.body))
		defer response.Body.Close()
		if response.StatusCode != status {
			t.Fatalf("%s status = %d, want %d", item.name, response.StatusCode, status)
		}
		if status >= 400 {
			var envelope api.ErrorEnvelope
			if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Retryable != retryable {
				t.Fatalf("%s retryable = %t, want %t", item.name, envelope.Error.Retryable, retryable)
			}
		}
	}
	for _, phase := range []struct {
		name, lock string
		lookups    []lookup
	}{
		{"Access authorization", `LOCK TABLE access_platform_operators, access_memberships IN ACCESS EXCLUSIVE MODE`, []lookup{read, readiness, media, hangup}},
		{"lease lookup", `LOCK TABLE human_calling_softphone_leases IN ACCESS EXCLUSIVE MODE`, []lookup{media, hangup}},
	} {
		t.Run(phase.name, func(t *testing.T) {
			blocker, err := owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(ctx) }()
			if _, err := blocker.Exec(ctx, phase.lock); err != nil {
				t.Fatal(err)
			}
			for _, item := range phase.lookups {
				t.Run(item.name, func(t *testing.T) { check(t, item, "staff", http.StatusServiceUnavailable, true) })
			}
		})
	}
	check(t, read, "staff", http.StatusOK, false)
	check(t, readiness, "staff", http.StatusOK, false)
	check(t, read, "unscoped", http.StatusForbidden, false)
	// Missing active credentials and another browser's lease remain real conflicts.
	check(t, media, "staff", http.StatusConflict, false)
	hangup.body = `{"sessionId":"different-browser"}`
	check(t, hangup, "staff", http.StatusConflict, false)
	read.path = "/v1/calling/calls/00000000-0000-0000-0000-000000000001"
	check(t, read, "staff", http.StatusForbidden, false)
}

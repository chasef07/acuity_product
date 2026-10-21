package httpapi_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestConditionalCallingStateReturnsNotModifiedWithoutLoadingFullState(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	identity := access.Identity{
		Subject:       "conditional-calling-state-staff",
		Email:         "staff@conditional-calling-state.test",
		EmailVerified: true,
	}
	_, err := access.New(pool, func() time.Time { return now }).Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "conditional-calling-state-test",
		Practices: []access.PracticeProvision{{
			Key:       "conditional-calling-state",
			Name:      "Conditional Calling State",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "staff",
				Email:         identity.Email,
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision conditional Calling state fixture: %v", err)
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	var completedCallID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point, caller_phone,
			terminal_outcome, ended_at, created_at, updated_at
		)
		VALUES ($1, $2, 'INBOUND', 'STANDALONE', '+19855550100',
			'RESOLVED', $3, $3, $3)
		RETURNING id::text
	`, authorization.Practice.ID, authorization.Locations[0].ID, now).Scan(
		&completedCallID,
	); err != nil {
		t.Fatalf("seed completed Calling state Call: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_softphone_leases (
			user_subject, session_id, lease_expires_at, readiness_updated_at
		)
		VALUES ($1, 'conditional-session', $2, $3)
	`, identity.Subject, now.Add(5*time.Minute), now); err != nil {
		t.Fatalf("seed conditional Calling lease: %v", err)
	}

	calling := humancalling.New(
		db.As(testdb.Portal),
		accessModule,
		httpCallingProvider{},
		humancalling.Config{},
		func() time.Time { return now },
	)
	handler, err := newPortalHandlerWithCalling(
		t,
		httpapi.Config{AcquireTimeout: 100 * time.Millisecond},
		db.As(testdb.Portal),
		accessModule,
		staticAuthenticator{"conditional-token": identity},
		calling,
	)
	if err != nil {
		t.Fatalf("create conditional Calling state handler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	initial := request(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/v1/calling/state",
		"conditional-token",
		nil,
	)
	if initial.StatusCode != http.StatusOK {
		t.Fatalf("initial Calling state status = %d, body = %s", initial.StatusCode, readBody(t, initial))
	}
	etag := initial.Header.Get("ETag")
	_ = initial.Body.Close()
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("Calling state ETag = %q, want a weak validator", etag)
	}

	lock, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire full-state lock connection: %v", err)
	}
	transaction, err := lock.Begin(context.Background())
	if err != nil {
		lock.Release()
		t.Fatalf("begin full-state lock: %v", err)
	}
	t.Cleanup(func() {
		if transaction != nil {
			_ = transaction.Rollback(context.Background())
		}
		if lock != nil {
			lock.Release()
		}
	})
	if _, err := transaction.Exec(
		context.Background(),
		`LOCK TABLE human_calling_handoffs IN ACCESS EXCLUSIVE MODE`,
	); err != nil {
		t.Fatalf("lock full-state table: %v", err)
	}

	assertCallingStateNotModified(
		t,
		server.Client(),
		server.URL+"/v1/calling/state",
		"conditional-token",
		etag,
		"unchanged state",
	)
	if err := transaction.Rollback(context.Background()); err != nil {
		t.Fatalf("release full-state table lock: %v", err)
	}
	transaction = nil
	lock.Release()
	lock = nil

	taskTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin unrelated Task creation: %v", err)
	}
	if _, err := work.New(db.As(testdb.Portal), accessModule, func() time.Time { return now }).EnsureCallFollowUp(
		context.Background(),
		taskTransaction,
		work.EnsureCallFollowUpCommand{
			CallID:     completedCallID,
			PracticeID: authorization.Practice.ID,
			LocationID: authorization.Locations[0].ID,
			Phone:      "+19855550100",
			Reason:     "Unrelated patient work",
			Creator:    authorization.Actor,
		},
	); err != nil {
		_ = taskTransaction.Rollback(context.Background())
		t.Fatalf("create unrelated Task: %v", err)
	}
	if err := taskTransaction.Commit(context.Background()); err != nil {
		t.Fatalf("commit unrelated Task: %v", err)
	}

	assertCallingStateNotModified(
		t,
		server.Client(),
		server.URL+"/v1/calling/state",
		"conditional-token",
		etag,
		"unrelated Task",
	)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point, caller_phone,
			created_at, updated_at
		)
		VALUES ($1, $2, 'INBOUND', 'STANDALONE', '+19855550101', $3, $3)
	`, authorization.Practice.ID, authorization.Locations[0].ID, now); err != nil {
		t.Fatalf("seed unrelated active Call: %v", err)
	}

	assertCallingStateNotModified(
		t,
		server.Client(),
		server.URL+"/v1/calling/state",
		"conditional-token",
		etag,
		"unrelated Call",
	)

	now = now.Add(6 * time.Minute)
	expiredRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/calling/state",
		nil,
	)
	if err != nil {
		t.Fatalf("create expired-lease Calling state request: %v", err)
	}
	expiredRequest.Header.Set("Authorization", "Bearer conditional-token")
	expiredRequest.Header.Set("If-None-Match", etag)
	expired, err := server.Client().Do(expiredRequest)
	if err != nil {
		t.Fatalf("read expired-lease Calling state: %v", err)
	}
	if expired.StatusCode != http.StatusOK {
		t.Fatalf("expired-lease Calling state status = %d, body = %s", expired.StatusCode, readBody(t, expired))
	}
	var expiredState struct {
		Softphone struct {
			Owner     bool `json:"owner"`
			Available bool `json:"available"`
		} `json:"softphone"`
	}
	expiredETag := expired.Header.Get("ETag")
	decode(t, expired, &expiredState)
	if expiredETag == "" || expiredETag == etag {
		t.Fatalf("expired-lease Calling state ETag = %q, want a new validator", expiredETag)
	}
	if expiredState.Softphone.Owner || expiredState.Softphone.Available {
		t.Fatalf("expired-lease softphone = %#v, want no ownership or availability", expiredState.Softphone)
	}

	testaccess.SeedRevokedMembership(t, pool, authorization, now)
	revokedRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/calling/state",
		nil,
	)
	if err != nil {
		t.Fatalf("create revoked-access Calling state request: %v", err)
	}
	revokedRequest.Header.Set("Authorization", "Bearer conditional-token")
	revokedRequest.Header.Set("If-None-Match", expiredETag)
	revoked, err := server.Client().Do(revokedRequest)
	if err != nil {
		t.Fatalf("read revoked-access Calling state: %v", err)
	}
	defer revoked.Body.Close()
	if revoked.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"revoked-access Calling state status = %d, want %d; body = %s",
			revoked.StatusCode,
			http.StatusForbidden,
			readBody(t, revoked),
		)
	}
}

func TestCallingStateLogsTheBrowserCallingChangeReport(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	identity := access.Identity{
		Subject:       "calling-change-report-staff",
		Email:         "staff@calling-change-report.test",
		EmailVerified: true,
	}
	if _, err := access.New(pool, func() time.Time { return now }).Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "calling-change-report-test",
		PlatformOperators: []string{"operator@calling-change-report.test"},
		Practices: []access.PracticeProvision{{
			Key:       "calling-change-report",
			Name:      "Calling Change Report",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "staff",
				Email:         identity.Email,
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision calling change report fixture: %v", err)
	}
	testaccess.Activate(t, accessModule, identity)
	var metrics synchronizedBuffer
	handler, err := newPortalHandlerWithCalling(
		t,
		httpapi.Config{
			AllowedOrigins: []string{"http://localhost:3000"},
			AcquireTimeout: 500 * time.Millisecond,
			Observer: observability.NewLogger(
				observability.RuntimePortalAPI,
				"calling-change-report-test",
				slog.New(slog.NewJSONHandler(&metrics, nil)),
			),
		},
		db.As(testdb.Portal),
		accessModule,
		staticAuthenticator{"change-report-token": identity},
		humancalling.New(
			db.As(testdb.Portal),
			accessModule,
			httpCallingProvider{},
			humancalling.Config{},
			func() time.Time { return now },
		),
	)
	if err != nil {
		t.Fatalf("create calling change report handler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	preflight, err := http.NewRequest(http.MethodOptions, server.URL+"/v1/calling/state", nil)
	if err != nil {
		t.Fatalf("create Calling state preflight: %v", err)
	}
	preflight.Header.Set("Origin", "http://localhost:3000")
	preflight.Header.Set("Access-Control-Request-Headers", "authorization,if-none-match,x-calling-change")
	allowed, err := server.Client().Do(preflight)
	if err != nil {
		t.Fatalf("send Calling state preflight: %v", err)
	}
	_ = allowed.Body.Close()
	if !strings.Contains(allowed.Header.Get("Access-Control-Allow-Headers"), "X-Calling-Change") {
		t.Fatalf("preflight allowed headers = %q", allowed.Header.Get("Access-Control-Allow-Headers"))
	}

	for _, report := range []string{
		"discovery=missed;hints=covered;tab=hidden;offer=ringing",
		"",
	} {
		read, err := http.NewRequest(http.MethodGet, server.URL+"/v1/calling/state", nil)
		if err != nil {
			t.Fatalf("create Calling state read: %v", err)
		}
		read.Header.Set("Authorization", "Bearer change-report-token")
		if report != "" {
			read.Header.Set("X-Calling-Change", report)
		}
		response, err := server.Client().Do(read)
		if err != nil {
			t.Fatalf("read Calling state: %v", err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("Calling state status = %d, body = %s", response.StatusCode, readBody(t, response))
		}
		_ = response.Body.Close()
	}

	logged := metrics.String()
	if count := strings.Count(logged, `"metric":"acuity_call_center_calling_change"`); count != 1 {
		t.Fatalf("calling change metrics = %d, want 1: %s", count, logged)
	}
	for _, field := range []string{
		`"discovery":"missed"`,
		`"hints":"covered"`,
		`"tab":"hidden"`,
		`"offer":"ringing"`,
		`"runtime_role":"portal-api"`,
	} {
		if !strings.Contains(logged, field) {
			t.Fatalf("calling change metric omitted %s: %s", field, logged)
		}
	}
}

func assertCallingStateNotModified(
	t *testing.T,
	client *http.Client,
	target string,
	token string,
	etag string,
	scenario string,
) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("create Calling state request after %s: %v", scenario, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("If-None-Match", etag)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("read Calling state after %s: %v", scenario, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotModified {
		t.Fatalf(
			"Calling state after %s status = %d, want %d; body = %s",
			scenario,
			response.StatusCode,
			http.StatusNotModified,
			readBody(t, response),
		)
	}
	if got := response.Header.Get("ETag"); got != etag {
		t.Fatalf("Calling state ETag after %s = %q, want %q", scenario, got, etag)
	}
}

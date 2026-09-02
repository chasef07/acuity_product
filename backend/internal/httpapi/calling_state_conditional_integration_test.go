package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestConditionalCallingStateReturnsNotModifiedWithoutLoadingFullState(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	identity := access.Identity{
		Subject:       "conditional-calling-state-staff",
		Email:         "staff@conditional-calling-state.test",
		EmailVerified: true,
	}
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "conditional-calling-state-test",
		PlatformOperators: []string{"operator@conditional-calling-state.test"},
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
		pool,
		accessModule,
		httpCallingProvider{},
		humancalling.Config{},
		func() time.Time { return now },
	)
	handler, err := newPortalHandlerWithCalling(
		t,
		httpapi.Config{AcquireTimeout: 100 * time.Millisecond},
		pool,
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
	if etag == "" {
		t.Fatal("initial Calling state omitted ETag")
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
	if _, err := work.New(pool, accessModule, func() time.Time { return now }).EnsureCallFollowUp(
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

	operator := access.Identity{
		Subject:       "conditional-calling-state-operator",
		Email:         "operator@conditional-calling-state.test",
		EmailVerified: true,
	}
	if err := accessModule.RevokeMembership(context.Background(), access.RevokeMembershipCommand{
		Identity:     operator,
		PracticeID:   authorization.Practice.ID,
		MembershipID: authorization.Membership.ID,
	}); err != nil {
		t.Fatalf("revoke conditional Calling Membership: %v", err)
	}
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

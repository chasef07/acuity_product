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
	"github.com/chasef07/acuity_product/backend/internal/admission"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPortalCallControlCommitsWhileBackgroundQueriesAreBlocked(t *testing.T) {
	owner := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	staff := access.Identity{Subject: "capacity-staff", Email: "staff@capacity.synthetic.test", EmailVerified: true}
	operator := access.Identity{Subject: "capacity-operator", Email: "operator@capacity.synthetic.test", EmailVerified: true}
	ownerAccess := access.New(owner, clock)
	if _, err := ownerAccess.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "call-control-capacity",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key: "capacity", Name: "Capacity Practice",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "staff", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	authorization := testaccess.Activate(t, ownerAccess, staff)
	testaccess.Activate(t, ownerAccess, operator)
	const sessionID = "capacity-browser"
	if _, err := owner.Exec(ctx, `
		INSERT INTO human_calling_softphone_leases (
			user_subject, session_id, lease_expires_at, readiness_updated_at
		) VALUES ($1, $2, $3, $4)
	`, staff.Subject, sessionID, now.Add(time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	var callID string
	if err := owner.QueryRow(ctx, `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point, caller_phone, created_at, updated_at
		) VALUES ($1, $2, 'INBOUND', 'STANDALONE', '+15555550123', $3, $3) RETURNING id::text
	`, authorization.Practice.ID, authorization.Locations[0].ID, now).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, staff_subject, staff_session_id, state,
			provider_connection_id, provider_call_control_id, provider_call_leg_id,
			provider_call_session_id, answered_at, bridge_pending_at, bridged_at, created_at, updated_at
		) VALUES
			($1, 'CALLER', 1, NULL, NULL, 'BRIDGED', 'synthetic-connection',
				'synthetic-caller-control', 'synthetic-caller-leg', 'synthetic-call-session', $4, $4, $4, $4, $4),
			($1, 'STAFF', 1, $2, $3, 'BRIDGED', 'synthetic-connection',
				'synthetic-staff-control', 'synthetic-staff-leg', 'synthetic-call-session', $4, $4, $4, $4, $4)
	`, callID, staff.Subject, sessionID, now); err != nil {
		t.Fatal(err)
	}

	config := owner.Config().Copy()
	config.MaxConns = 4
	config.ConnConfig.RuntimeParams["application_name"] = "call-control-capacity"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	database, err := productpostgres.NewPortalExecutor(pool, productpostgres.ExecutorConfig{
		AcquireTimeout: 150 * time.Millisecond, OperationTimeout: 5 * time.Second,
		StatementTimeout: 4 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	accessModule := access.New(database, clock)
	workModule := work.New(database, accessModule, clock)
	serviceAuth, err := access.NewServiceAuthenticator(access.ServiceCredential{
		Token: "unused-capacity-service-token",
		Identity: access.ServiceIdentity{Subject: "capacity-service", PracticeID: authorization.Practice.ID,
			LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityIngestAIInteraction}},
	}, access.ServiceCredential{
		Token: "unused-capacity-secondary-token",
		Identity: access.ServiceIdentity{Subject: "capacity-secondary", PracticeID: "00000000-0000-0000-0000-000000000002",
			LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityIngestAIInteraction}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewPortal(httpapi.Config{
		AcquireTimeout: 150 * time.Millisecond, RequestTimeout: 5 * time.Second,
	}, pool, httpapi.PortalDependencies{
		Access: accessModule, Authenticator: staticAuthenticator{"staff": staff, "operator": operator},
		Calling:      humancalling.New(database, accessModule, httpCallingProvider{}, humancalling.Config{}, clock),
		Interactions: interaction.New(database, accessModule, clock),
		Messaging:    messaging.New(database, accessModule, workModule, nil, messaging.Config{}, clock),
		Work:         workModule, Workspace: workspace.New(database, accessModule), ServiceAuthenticator: serviceAuth,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Match the portal's eight concurrent Cloud Run request slots locally.
	httpSlots := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case httpSlots <- struct{}{}:
			defer func() { <-httpSlots }()
			handler.ServeHTTP(w, r)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	blocker, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Rollback(ctx) })
	if _, err := blocker.Exec(ctx, `LOCK TABLE messaging_messages, ai_interactions IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	type result struct {
		status     int
		retryable  bool
		retryAfter string
		err        error
	}
	startBackground := func(analytics bool) <-chan result {
		t.Helper()
		method, path, token := http.MethodGet,
			"/v1/engagements/%2B15555550123/timeline?practiceId="+authorization.Practice.ID, "staff"
		var body []byte
		if analytics {
			method, path, token = http.MethodPost, "/v1/operator/ai-analytics/query", "operator"
			body = []byte(fmt.Sprintf(`{"practiceId":%q,"range":"24h","limit":10}`, authorization.Practice.ID))
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		done := make(chan result, 1)
		go func() {
			response, err := server.Client().Do(req)
			if err != nil {
				done <- result{err: err}
				return
			}
			var retryable bool
			if response.StatusCode == http.StatusServiceUnavailable {
				var envelope api.ErrorEnvelope
				if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
					_ = response.Body.Close()
					done <- result{err: err}
					return
				}
				retryable = envelope.Error.Retryable && envelope.Error.Code == "UNAVAILABLE"
			}
			_ = response.Body.Close()
			done <- result{status: response.StatusCode, retryable: retryable, retryAfter: response.Header.Get("Retry-After")}
		}()
		return done
	}
	background := []<-chan result{startBackground(false), startBackground(true)}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var blocked int
		if err := owner.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE application_name = 'call-control-capacity' AND wait_event_type = 'Lock'
		`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("blocked background queries = %d, want 2", blocked)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// An eight-read burst must leave room for Calling sync and commands instead
	// of filling either all HTTP slots or all database connections.
	overflow := []<-chan result{startBackground(true)}
	for range 5 {
		overflow = append(overflow, startBackground(false))
	}
	var overflowResult *result
	deadline = time.Now().Add(time.Second)
	for overflowResult == nil && pool.Stat().AcquiredConns() < 4 && time.Now().Before(deadline) {
		select {
		case completed := <-overflow[0]:
			overflowResult = &completed
		case <-time.After(5 * time.Millisecond):
		}
	}
	started := time.Now()
	callingState := request(t, server.Client(), http.MethodGet, server.URL+"/v1/calling/state", "staff", nil)
	if callingState.StatusCode != http.StatusOK {
		t.Fatalf("Calling sync under background pressure = %d, body=%s", callingState.StatusCode, readBody(t, callingState))
	}
	var current api.CallingState
	decode(t, callingState, &current)
	if current.Bridged == nil || current.Bridged.CallId.String() != callID {
		t.Fatalf("Calling sync lost the active Call: %#v", current.Bridged)
	}
	t.Logf("Calling sync preserved the active Call during eight-read burst in %s", time.Since(started))
	// A handler can also encounter the connection-level limit after admission:
	// dependency pressure must remain retryable, never become a false access loss.
	syncTransaction, err := database.BeginTx(admission.WithClass(ctx, admission.CallingSync), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	unavailableState := request(t, server.Client(), http.MethodGet, server.URL+"/v1/calling/state", "staff", nil)
	_ = syncTransaction.Rollback(ctx)
	if unavailableState.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("connection admission rejection became %d instead of retryable 503: %s", unavailableState.StatusCode, readBody(t, unavailableState))
	}
	var unavailableEnvelope api.ErrorEnvelope
	decode(t, unavailableState, &unavailableEnvelope)
	if unavailableEnvelope.Error.Code != "UNAVAILABLE" || !unavailableEnvelope.Error.Retryable {
		t.Fatalf("connection admission response is not retryable: %#v", unavailableEnvelope.Error)
	}
	readinessBody, _ := json.Marshal(api.CallingReadinessRequest{
		SessionId: sessionID, Registered: true, MicrophoneReady: true,
		AudioReady: true, SessionHealthy: true, Available: true,
	})
	started = time.Now()
	readiness := request(t, server.Client(), http.MethodPut, server.URL+"/v1/calling/readiness", "staff", readinessBody)
	if readiness.StatusCode != http.StatusOK {
		t.Fatalf("readiness under background pressure = %d, body=%s", readiness.StatusCode, readBody(t, readiness))
	}
	_ = readiness.Body.Close()
	t.Logf("readiness committed with 2 blocked background queries in %s", time.Since(started))
	var ready bool
	if err := owner.QueryRow(ctx, `
		SELECT registered AND microphone_ready AND audio_ready AND session_healthy
		FROM human_calling_softphone_leases WHERE user_subject = $1 AND version > 1
	`, staff.Subject).Scan(&ready); err != nil || !ready {
		t.Fatalf("readiness was not durably committed: ready=%t err=%v", ready, err)
	}
	hangupBody, _ := json.Marshal(api.CallingControlRequest{SessionId: sessionID})
	unauthorized := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/calling/calls/"+callID+"/hangup", "", hangupBody)
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated control status=%d", unauthorized.StatusCode)
	}
	_ = unauthorized.Body.Close()
	started = time.Now()
	hangup := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/calling/calls/"+callID+"/hangup", "staff", hangupBody)
	if hangup.StatusCode != http.StatusAccepted {
		t.Fatalf("hangup under background pressure = %d, body=%s", hangup.StatusCode, readBody(t, hangup))
	}
	_ = hangup.Body.Close()
	t.Logf("hangup committed with 2 blocked background queries in %s", time.Since(started))
	var commands, endingLegs int
	if err := owner.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM human_calling_provider_commands WHERE call_id = $1 AND action = 'HANGUP_LEG' AND state = 'PENDING'),
			(SELECT count(*) FROM human_calling_call_legs WHERE call_id = $1 AND state = 'ENDING')
	`, callID).Scan(&commands, &endingLegs); err != nil || commands != 2 || endingLegs != 2 {
		t.Fatalf("hangup durable state: commands=%d endingLegs=%d err=%v", commands, endingLegs, err)
	}
	for index, pending := range overflow {
		var completed result
		if index == 0 && overflowResult != nil {
			completed = *overflowResult
		} else {
			select {
			case completed = <-pending:
			case <-time.After(time.Second):
				t.Fatal("background overflow waited instead of rejecting promptly")
			}
		}
		if completed.err != nil || completed.status != http.StatusServiceUnavailable || !completed.retryable || completed.retryAfter != "1" {
			t.Fatalf("background overflow response = %#v, want retryable 503", completed)
		}
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, pending := range background {
		if result := <-pending; result.err != nil || result.status != http.StatusOK {
			t.Fatalf("admitted background request did not recover: %#v", result)
		}
	}
	var busyTransactions []pgx.Tx
	t.Cleanup(func() {
		for _, transaction := range busyTransactions {
			_ = transaction.Rollback(ctx)
		}
	})
	for range 4 {
		transaction, err := database.BeginTx(admission.WithClass(ctx, admission.CallingControl), pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		busyTransactions = append(busyTransactions, transaction)
	}
	leaseBody := []byte(`{"sessionId":"capacity-browser","takeover":false}`)
	busyLease := request(t, server.Client(), http.MethodPost, server.URL+"/v1/calling/softphone/lease", "staff", leaseBody)
	if busyLease.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("actual control pool saturation became %d instead of retryable 503: %s", busyLease.StatusCode, readBody(t, busyLease))
	}
	decode(t, busyLease, &unavailableEnvelope)
	if !unavailableEnvelope.Error.Retryable {
		t.Fatal("busy softphone lease was reported as a permanent access loss")
	}
}

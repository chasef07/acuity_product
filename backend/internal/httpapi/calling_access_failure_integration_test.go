package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCallingRoutesKeepTransientFailuresOutOfAccessDenial(t *testing.T) {
	fixture := newCallingAccessFailureFixture(t, httpCallingProvider{})
	call, expect := fixture.call, fixture.expect
	outbound := func(token, sessionID string) (int, string) {
		return call(http.MethodPost, "/v1/calling/outbound-calls", token, map[string]any{
			"sessionId":      sessionID,
			"idempotencyKey": "calling-access-failure-outbound",
			"practiceId":     fixture.authorization.Practice.ID,
			"locationId":     fixture.authorization.Locations[0].ID,
			"destination":    "+15555550102",
		})
	}
	lease, readiness, mediaToken := fixture.lease, fixture.readiness, fixture.mediaToken

	status, body := readiness("staff-token", "browser-a")
	expect("readiness without a lease", http.StatusConflict, "CALL_CONFLICT", status, body)
	status, body = mediaToken("staff-token", "browser-a")
	expect("media token without a lease", http.StatusConflict, "CALL_CONFLICT", status, body)

	status, body = lease("staff-token", "browser-a")
	if status != http.StatusOK {
		t.Fatalf("acquire lease = %d %s", status, body)
	}
	status, body = readiness("staff-token", "browser-b")
	expect("readiness with another session's lease", http.StatusConflict, "CALL_CONFLICT", status, body)
	status, body = readiness("staff-token", "browser-a")
	if status != http.StatusOK {
		t.Fatalf("readiness with the owned lease = %d %s", status, body)
	}

	fixture.clock.Add(int64(31 * time.Second))
	status, body = readiness("staff-token", "browser-a")
	expect("readiness with an expired lease", http.StatusConflict, "CALL_CONFLICT", status, body)
	status, body = mediaToken("staff-token", "browser-a")
	expect("media token with an expired lease", http.StatusConflict, "CALL_CONFLICT", status, body)

	status, body = lease("staff-token", "browser-a")
	if status != http.StatusOK {
		t.Fatalf("reacquire expired lease = %d %s", status, body)
	}
	fixture.database.failQueriesContaining("FROM access_platform_operators")
	status, body = call(http.MethodGet, "/v1/calling/state", "staff-token", nil)
	expect("state during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = readiness("staff-token", "browser-a")
	expect("readiness during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = lease("staff-token", "browser-a")
	expect("lease during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = mediaToken("staff-token", "browser-a")
	expect("media token during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = outbound("staff-token", "browser-a")
	expect("outbound Call during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	fixture.database.failQueriesContaining("JOIN access_practices p ON p.id = m.practice_id")
	status, body = outbound("staff-token", "browser-a")
	expect("outbound Call during a Location authorization failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	fixture.database.failQueriesContaining("")

	status, body = call(http.MethodGet, "/v1/calling/state", "staff-token", nil)
	if status != http.StatusOK {
		t.Fatalf("state after the access lookup recovers = %d %s", status, body)
	}
	status, body = call(http.MethodGet, "/v1/calling/state", "outsider-token", nil)
	expect("state without calling membership", http.StatusForbidden, "ACCESS_DENIED", status, body)
	status, body = readiness("outsider-token", "outsider-browser")
	expect("readiness without calling membership", http.StatusForbidden, "ACCESS_DENIED", status, body)
	status, body = lease("outsider-token", "outsider-browser")
	expect("lease without calling membership", http.StatusForbidden, "ACCESS_DENIED", status, body)
}

func TestCallRoutesKeepTransientFailuresOutOfAccessDenial(t *testing.T) {
	provider := &rotatingCredentialProvider{}
	fixture := newCallingAccessFailureFixture(t, provider)
	provider.database, provider.now = fixture.pool, fixture.now
	call, expect := fixture.call, fixture.expect
	ctx := context.Background()

	status, body := fixture.lease("staff-token", "browser-a")
	if status != http.StatusOK {
		t.Fatalf("acquire lease = %d %s", status, body)
	}
	inboundCallID, transferID := fixture.seedTransferringInboundCall()
	var outboundCallID string
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point, destination_phone,
			outbound_caller_id, initiating_subject, outbound_idempotency_key,
			outbound_input_fingerprint, version, created_at, updated_at
		)
		VALUES ($1, $2, 'OUTBOUND', 'STANDALONE', '+15555550102',
			'+15555550199', $3, 'seeded-outbound', '\x00'::bytea, 1, $4, $4)
		RETURNING id::text
	`, fixture.authorization.Practice.ID, fixture.authorization.Locations[0].ID,
		fixture.staff.Subject, fixture.now()).Scan(&outboundCallID); err != nil {
		t.Fatalf("seed outbound Call: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO human_calling_credentials (user_subject, provider_credential_id, state)
		VALUES ($1, 'seeded-credential', 'ACTIVE')
	`, fixture.staff.Subject); err != nil {
		t.Fatalf("seed media credential: %v", err)
	}

	readCall := func(token string) (int, string) {
		return call(http.MethodGet, "/v1/calling/calls/"+inboundCallID, token, nil)
	}
	hangup := func(token string) (int, string) {
		return call(http.MethodPost, "/v1/calling/calls/"+inboundCallID+"/hangup", token,
			map[string]any{"sessionId": "browser-a"})
	}
	disposition := func(token string) (int, string) {
		return call(http.MethodPost, "/v1/calling/calls/"+inboundCallID+"/disposition", token,
			map[string]any{"sessionId": "browser-a", "outcome": "NO_FOLLOW_UP"})
	}
	transferCandidates := func(token string) (int, string) {
		return call(http.MethodGet,
			"/v1/calling/calls/"+inboundCallID+"/transfer-candidates?sessionId=browser-a",
			token, nil)
	}
	replayTransfer := func(token string) (int, string) {
		return call(http.MethodPost, "/v1/calling/calls/"+inboundCallID+"/transfers", token,
			map[string]any{
				"sessionId":        "browser-a",
				"recipientSubject": seededTransferRecipient,
				"idempotencyKey":   "seeded-transfer",
				"expectedVersion":  1,
			})
	}
	cancelTransfer := func(token string) (int, string) {
		return call(http.MethodPost, "/v1/calling/transfers/"+transferID+"/cancel", token,
			map[string]any{"sessionId": "browser-a"})
	}
	confirmMedia := func(token string) (int, string) {
		return call(http.MethodPost, "/v1/calling/calls/"+outboundCallID+"/media-ready", token,
			map[string]any{"sessionId": "browser-a", "mediaToken": "seeded-media-token"})
	}

	status, body = readCall("staff-token")
	if status != http.StatusOK {
		t.Fatalf("read Call = %d %s", status, body)
	}

	fixture.database.failQueriesContaining("FROM access_platform_operators")
	status, body = readCall("staff-token")
	expect("Call read during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = hangup("staff-token")
	expect("Hangup during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = disposition("staff-token")
	expect("disposition during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = transferCandidates("staff-token")
	expect("transfer candidates during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = replayTransfer("staff-token")
	expect("transfer replay during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = cancelTransfer("staff-token")
	expect("transfer cancel during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	status, body = confirmMedia("staff-token")
	expect("outbound media confirmation during an access lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	fixture.database.failQueriesContaining("call.terminal_outcome IS NOT NULL")
	status, body = hangup("staff-token")
	expect("Hangup during a Call lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	fixture.database.failQueriesContaining("LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id")
	status, body = disposition("staff-token")
	expect("disposition during a Call lookup failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	fixture.database.failQueriesContaining("")

	status, body = fixture.mediaToken("staff-token", "browser-a")
	expect("media token when the credential rotates mid-issue", http.StatusConflict, "CALL_CONFLICT", status, body)

	status, body = readCall("outsider-token")
	expect("Call read without calling membership", http.StatusForbidden, "ACCESS_DENIED", status, body)
	status, body = hangup("outsider-token")
	expect("Hangup without calling membership", http.StatusForbidden, "ACCESS_DENIED", status, body)
	status, body = disposition("outsider-token")
	expect("disposition without calling membership", http.StatusForbidden, "ACCESS_DENIED", status, body)
	status, body = confirmMedia("outsider-token")
	expect("outbound media confirmation without calling membership", http.StatusForbidden, "ACCESS_DENIED", status, body)

	if _, err := fixture.pool.Exec(ctx, `
		UPDATE access_memberships SET revoked_at = $2 WHERE user_subject = $1
	`, fixture.staff.Subject, fixture.now()); err != nil {
		t.Fatalf("revoke staff membership: %v", err)
	}
	status, body = transferCandidates("staff-token")
	expect("transfer candidates after membership revocation", http.StatusForbidden, "ACCESS_DENIED", status, body)
	status, body = replayTransfer("staff-token")
	expect("transfer replay after membership revocation", http.StatusForbidden, "ACCESS_DENIED", status, body)
	status, body = cancelTransfer("staff-token")
	expect("transfer cancel after membership revocation", http.StatusForbidden, "ACCESS_DENIED", status, body)
}

const seededTransferRecipient = "calling-access-failure-recipient"

type callingAccessFailureFixture struct {
	t             *testing.T
	pool          *pgxpool.Pool
	database      *accessLookupFaultDatabase
	clock         *atomic.Int64
	staff         access.Identity
	authorization access.Authorization
	server        *httptest.Server
}

func newCallingAccessFailureFixture(
	t *testing.T,
	provider humancalling.Provider,
) *callingAccessFailureFixture {
	t.Helper()
	db := testdb.Open(t)
	pool := db.Owner
	fixture := &callingAccessFailureFixture{
		t:     t,
		pool:  pool,
		clock: &atomic.Int64{},
		staff: access.Identity{
			Subject:       "calling-access-failure-staff",
			Email:         "staff@calling-access-failure.test",
			EmailVerified: true,
		},
	}
	fixture.clock.Store(time.Date(2026, time.October, 13, 17, 0, 0, 0, time.UTC).UnixNano())
	outsider := access.Identity{
		Subject:       "calling-access-failure-outsider",
		Email:         "outsider@calling-access-failure.test",
		EmailVerified: true,
	}
	accessModule := access.New(db.As(testdb.Portal), fixture.now)
	if _, err := access.New(pool, fixture.now).Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "calling-access-failure-test",
		PlatformOperators: []string{"operator@calling-access-failure.test"},
		Practices: []access.PracticeProvision{{
			Key:       "calling-access-failure",
			Name:      "Calling Access Failure",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "staff",
				Email:         fixture.staff.Email,
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision calling access failure fixture: %v", err)
	}
	fixture.authorization = testaccess.Activate(t, accessModule, fixture.staff)

	fixture.database = &accessLookupFaultDatabase{Database: pool}
	calling := humancalling.New(
		fixture.database,
		access.New(fixture.database, fixture.now),
		provider,
		humancalling.Config{},
		fixture.now,
	)
	handler, err := newPortalHandlerWithCalling(
		t,
		httpapi.Config{AcquireTimeout: time.Second},
		db.As(testdb.Portal),
		accessModule,
		staticAuthenticator{"staff-token": fixture.staff, "outsider-token": outsider},
		calling,
	)
	if err != nil {
		t.Fatalf("create calling access failure handler: %v", err)
	}
	fixture.server = httptest.NewServer(handler)
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *callingAccessFailureFixture) now() time.Time {
	return time.Unix(0, fixture.clock.Load()).UTC()
}

func (fixture *callingAccessFailureFixture) call(
	method, path, token string,
	body any,
) (int, string) {
	fixture.t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	response := request(fixture.t, fixture.server.Client(), method,
		fixture.server.URL+path, token, payload)
	defer response.Body.Close()
	return response.StatusCode, readBody(fixture.t, response)
}

func (fixture *callingAccessFailureFixture) expect(
	label string,
	want int,
	wantCode string,
	status int,
	body string,
) {
	fixture.t.Helper()
	if status != want || !strings.Contains(body, `"`+wantCode+`"`) {
		fixture.t.Errorf("%s = %d, want %d %s", label, status, want, wantCode)
	}
}

func (fixture *callingAccessFailureFixture) lease(token, sessionID string) (int, string) {
	return fixture.call(http.MethodPost, "/v1/calling/softphone/lease", token, map[string]any{
		"sessionId": sessionID,
		"takeover":  false,
	})
}

func (fixture *callingAccessFailureFixture) readiness(token, sessionID string) (int, string) {
	return fixture.call(http.MethodPut, "/v1/calling/readiness", token, map[string]any{
		"sessionId":       sessionID,
		"registered":      true,
		"microphoneReady": true,
		"audioReady":      true,
		"sessionHealthy":  true,
		"available":       true,
	})
}

func (fixture *callingAccessFailureFixture) mediaToken(token, sessionID string) (int, string) {
	return fixture.call(http.MethodPost, "/v1/calling/media-token", token, map[string]any{
		"sessionId": sessionID,
	})
}

func (fixture *callingAccessFailureFixture) seedTransferringInboundCall() (string, string) {
	fixture.t.Helper()
	ctx := context.Background()
	now := fixture.now()
	var callID string
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point, caller_phone,
			created_at, updated_at
		)
		VALUES ($1, $2, 'INBOUND', 'STANDALONE', '+15555550100', $3, $3)
		RETURNING id::text
	`, fixture.authorization.Practice.ID, fixture.authorization.Locations[0].ID,
		now).Scan(&callID); err != nil {
		fixture.t.Fatalf("seed inbound Call: %v", err)
	}
	insertLeg := func(role string, sequence int, subject, session, state string) string {
		fixture.t.Helper()
		var legID string
		bridged := state == "BRIDGED"
		if err := fixture.pool.QueryRow(ctx, `
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, staff_subject, staff_session_id, state,
				answered_at, bridge_pending_at, bridged_at
			)
			VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6,
				CASE WHEN $7 THEN $8::timestamptz END,
				CASE WHEN $7 THEN $8::timestamptz END,
				CASE WHEN $7 THEN $8::timestamptz END)
			RETURNING id::text
		`, callID, role, sequence, subject, session, state, bridged, now).Scan(&legID); err != nil {
			fixture.t.Fatalf("seed %s CallLeg: %v", role, err)
		}
		return legID
	}
	customerLegID := insertLeg("CALLER", 1, "", "", "BRIDGED")
	sourceLegID := insertLeg("STAFF", 2, fixture.staff.Subject, "browser-a", "BRIDGED")
	targetLegID := insertLeg("STAFF", 3, seededTransferRecipient, "recipient-browser", "RINGING")
	var commandID string
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO human_calling_provider_commands (call_id, action, state)
		VALUES ($1, 'TRANSFER_STAFF', 'RECONCILED')
		RETURNING id::text
	`, callID).Scan(&commandID); err != nil {
		fixture.t.Fatalf("seed transfer provider command: %v", err)
	}
	var transferID string
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO human_calling_staff_transfers (
			call_id, practice_id, location_id, source_staff_leg_id,
			target_staff_leg_id, customer_leg_id, provider_command_id,
			requested_by_subject, requested_by_session_id, recipient_subject,
			recipient_session_id, idempotency_key, handoff_note, state, expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'browser-a', $9,
			'recipient-browser', 'seeded-transfer', '', 'REQUESTED', $10)
		RETURNING id::text
	`, callID, fixture.authorization.Practice.ID, fixture.authorization.Locations[0].ID,
		sourceLegID, targetLegID, customerLegID, commandID, fixture.staff.Subject,
		seededTransferRecipient, now.Add(time.Minute)).Scan(&transferID); err != nil {
		fixture.t.Fatalf("seed staff transfer: %v", err)
	}
	return callID, transferID
}

type rotatingCredentialProvider struct {
	database *pgxpool.Pool
	now      func() time.Time
}

func (provider *rotatingCredentialProvider) Execute(
	ctx context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	if command.Action != humancalling.CommandCreateJWT {
		return humancalling.ProviderResult{}, nil
	}
	if _, err := provider.database.Exec(ctx, `
		UPDATE human_calling_credentials SET provider_credential_id = 'rotated-credential'
		WHERE provider_credential_id = $1
	`, command.TargetID); err != nil {
		return humancalling.ProviderResult{}, err
	}
	return humancalling.ProviderResult{
		JWT:          "test-media-jwt",
		JWTExpiresAt: provider.now().Add(time.Hour),
	}, nil
}

type accessLookupFaultDatabase struct {
	productpostgres.Database
	failingFragment atomic.Value
}

var errAccessLookupConnectionLost = errors.New("simulated connection to client lost")

func (database *accessLookupFaultDatabase) failQueriesContaining(fragment string) {
	database.failingFragment.Store(fragment)
}

func (database *accessLookupFaultDatabase) fails(sql string) bool {
	fragment, _ := database.failingFragment.Load().(string)
	return fragment != "" && strings.Contains(sql, fragment)
}

func (database *accessLookupFaultDatabase) BeginTx(
	ctx context.Context,
	options pgx.TxOptions,
) (pgx.Tx, error) {
	tx, err := database.Database.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return accessLookupFaultTx{Tx: tx, database: database}, nil
}

type accessLookupFaultTx struct {
	pgx.Tx
	database *accessLookupFaultDatabase
}

func (tx accessLookupFaultTx) Exec(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	if tx.database.fails(sql) {
		return pgconn.CommandTag{}, errAccessLookupConnectionLost
	}
	return tx.Tx.Exec(ctx, sql, arguments...)
}

func (tx accessLookupFaultTx) Query(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgx.Rows, error) {
	if tx.database.fails(sql) {
		return nil, errAccessLookupConnectionLost
	}
	return tx.Tx.Query(ctx, sql, arguments...)
}

func (tx accessLookupFaultTx) QueryRow(
	ctx context.Context,
	sql string,
	arguments ...any,
) pgx.Row {
	if tx.database.fails(sql) {
		return failedRow{}
	}
	return tx.Tx.QueryRow(ctx, sql, arguments...)
}

type failedRow struct{}

func (failedRow) Scan(...any) error { return errAccessLookupConnectionLost }

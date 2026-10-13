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
)

func TestCallingRoutesKeepTransientFailuresOutOfAccessDenial(t *testing.T) {
	pool := testdb.Open(t)
	start := time.Date(2026, time.October, 13, 17, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(start.UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	accessModule := access.New(pool, now)
	staff := access.Identity{
		Subject:       "calling-access-failure-staff",
		Email:         "staff@calling-access-failure.test",
		EmailVerified: true,
	}
	outsider := access.Identity{
		Subject:       "calling-access-failure-outsider",
		Email:         "outsider@calling-access-failure.test",
		EmailVerified: true,
	}
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "calling-access-failure-test",
		PlatformOperators: []string{"operator@calling-access-failure.test"},
		Practices: []access.PracticeProvision{{
			Key:       "calling-access-failure",
			Name:      "Calling Access Failure",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "staff",
				Email:         staff.Email,
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision calling access failure fixture: %v", err)
	}
	authorization := testaccess.Activate(t, accessModule, staff)

	database := &accessLookupFaultDatabase{Database: pool}
	calling := humancalling.New(
		database,
		access.New(database, now),
		httpCallingProvider{},
		humancalling.Config{},
		now,
	)
	handler, err := newPortalHandlerWithCalling(
		t,
		httpapi.Config{AcquireTimeout: time.Second},
		pool,
		accessModule,
		staticAuthenticator{"staff-token": staff, "outsider-token": outsider},
		calling,
	)
	if err != nil {
		t.Fatalf("create calling access failure handler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	call := func(method, path, token string, body any) (int, string) {
		t.Helper()
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		response := request(t, server.Client(), method, server.URL+path, token, payload)
		defer response.Body.Close()
		return response.StatusCode, readBody(t, response)
	}
	outbound := func(token, sessionID string) (int, string) {
		return call(http.MethodPost, "/v1/calling/outbound-calls", token, map[string]any{
			"sessionId":      sessionID,
			"idempotencyKey": "calling-access-failure-outbound",
			"practiceId":     authorization.Practice.ID,
			"locationId":     authorization.Locations[0].ID,
			"destination":    "+15555550102",
		})
	}
	lease := func(token, sessionID string) (int, string) {
		return call(http.MethodPost, "/v1/calling/softphone/lease", token, map[string]any{
			"sessionId": sessionID,
			"takeover":  false,
		})
	}
	readiness := func(token, sessionID string) (int, string) {
		return call(http.MethodPut, "/v1/calling/readiness", token, map[string]any{
			"sessionId":       sessionID,
			"registered":      true,
			"microphoneReady": true,
			"audioReady":      true,
			"sessionHealthy":  true,
			"available":       true,
		})
	}
	mediaToken := func(token, sessionID string) (int, string) {
		return call(http.MethodPost, "/v1/calling/media-token", token, map[string]any{
			"sessionId": sessionID,
		})
	}
	expect := func(label string, want int, wantCode string, status int, body string) {
		t.Helper()
		if status != want || !strings.Contains(body, `"`+wantCode+`"`) {
			t.Errorf("%s = %d, want %d %s", label, status, want, wantCode)
		}
	}

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

	clock.Add(int64(31 * time.Second))
	status, body = readiness("staff-token", "browser-a")
	expect("readiness with an expired lease", http.StatusConflict, "CALL_CONFLICT", status, body)
	status, body = mediaToken("staff-token", "browser-a")
	expect("media token with an expired lease", http.StatusConflict, "CALL_CONFLICT", status, body)

	status, body = lease("staff-token", "browser-a")
	if status != http.StatusOK {
		t.Fatalf("reacquire expired lease = %d %s", status, body)
	}
	database.failQueriesContaining("FROM access_platform_operators")
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
	database.failQueriesContaining("JOIN access_practices p ON p.id = m.practice_id")
	status, body = outbound("staff-token", "browser-a")
	expect("outbound Call during a Location authorization failure", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	database.failQueriesContaining("")

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

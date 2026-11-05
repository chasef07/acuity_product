package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestOutboundStartReportsACredentialReadFailureAsUnavailable(t *testing.T) {
	fixture := newCallingAccessFailureFixture(t, httpCallingProvider{})
	if status, body := fixture.lease("staff-token", "browser-a"); status != http.StatusOK {
		t.Fatalf("acquire lease = %d %s", status, body)
	}
	if status, body := fixture.readiness("staff-token", "browser-a"); status != http.StatusOK {
		t.Fatalf("readiness = %d %s", status, body)
	}
	if err := humancalling.New(fixture.pool, access.New(fixture.pool, fixture.now), httpCallingProvider{},
		humancalling.Config{}, fixture.now).ProvisionLocationVoices(context.Background(),
		[]humancalling.LocationVoiceProvision{{
			PracticeKey: "calling-access-failure", LocationKey: "main",
			Number: "+14843336938", Enabled: true,
		}}); err != nil {
		t.Fatalf("provision outbound caller ID: %v", err)
	}
	fixture.database.failQueriesContaining("FROM human_calling_credentials")
	status, body := fixture.call(http.MethodPost, "/v1/calling/outbound-calls", "staff-token", map[string]any{
		"sessionId":      "browser-a",
		"idempotencyKey": "credential-failure-outbound",
		"practiceId":     fixture.authorization.Practice.ID,
		"locationId":     fixture.authorization.Locations[0].ID,
		"destination":    "+15555550102",
	})
	fixture.expect("outbound start credential read", http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
}

func TestCallCommandsReportDatabaseFailuresAsUnavailable(t *testing.T) {
	provider := &rotatingCredentialProvider{}
	fixture := newCallingAccessFailureFixture(t, provider)
	provider.database, provider.now = fixture.pool, fixture.now
	ctx := context.Background()

	if status, body := fixture.lease("staff-token", "browser-a"); status != http.StatusOK {
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

	post := func(path string, body map[string]any) func() (int, string) {
		return func() (int, string) {
			return fixture.call(http.MethodPost, path, "staff-token", body)
		}
	}
	session := map[string]any{"sessionId": "browser-a"}
	hangup := post("/v1/calling/calls/"+inboundCallID+"/hangup", session)
	disposition := post("/v1/calling/calls/"+inboundCallID+"/disposition",
		map[string]any{"sessionId": "browser-a", "outcome": "RESOLVED"})
	start := post("/v1/calling/outbound-calls", map[string]any{
		"sessionId":      "browser-a",
		"idempotencyKey": "database-failure-outbound",
		"practiceId":     fixture.authorization.Practice.ID,
		"locationId":     fixture.authorization.Locations[0].ID,
		"destination":    "+15555550102",
	})
	confirmMedia := post("/v1/calling/calls/"+outboundCallID+"/media-ready",
		map[string]any{"sessionId": "browser-a", "mediaToken": "seeded-media-token"})
	cancel := post("/v1/calling/transfers/"+transferID+"/cancel", session)
	candidates := func() (int, string) {
		return fixture.call(http.MethodGet,
			"/v1/calling/calls/"+inboundCallID+"/transfer-candidates?sessionId=browser-a",
			"staff-token", nil)
	}

	operatorTimeline := func() (int, string) {
		return fixture.call(http.MethodGet, "/v1/operator/calls/"+inboundCallID+"/timeline", "operator-token", nil)
	}
	requeue := func() (int, string) {
		return fixture.call(http.MethodPost, "/v1/operator/practices/"+fixture.authorization.Practice.ID+
			"/provider-receipts/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/requeue", "operator-token", map[string]any{})
	}
	const leases = "FROM human_calling_softphone_leases"
	for _, scenario := range []struct {
		label    string
		fragment string
		request  func() (int, string)
	}{
		{"Hangup lease check", leases, hangup},
		{"Hangup connected-leg check", "AND state IN ('BRIDGE_PENDING', 'BRIDGED')", hangup},
		{"disposition owner check", "FROM human_calling_current_staff_owners", disposition},
		{"disposition lease check", leases, disposition},
		{"outbound start readiness check", leases, start},
		{"outbound media Call check", "WHERE id = $1 AND direction = 'OUTBOUND' FOR UPDATE", confirmMedia},
		{"outbound media Staff leg check", "WHERE call_id = $1 AND role = 'STAFF' AND staff_subject = $2 FOR UPDATE", confirmMedia},
		{"transfer cancel lease check", leases, cancel},
		{"transfer candidates source check", "JOIN human_calling_call_legs source ON source.call_id = call.id", candidates},
		{"transfer candidates lease check", leases, candidates},
		{"operator timeline operator check", "FROM access_platform_operators", operatorTimeline},
		{"operator timeline Call read", "LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id", operatorTimeline},
		{"receipt requeue authorization", "FROM access_platform_operators", requeue},
	} {
		fixture.database.failQueriesContaining(scenario.fragment)
		status, body := scenario.request()
		fixture.database.failQueriesContaining("")
		fixture.expect(scenario.label, http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	}
}

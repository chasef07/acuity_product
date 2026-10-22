package humancalling_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

type callingStateObservation struct {
	name  string
	state humancalling.CallingState
	body  string
}

func observeCallingState(
	t *testing.T,
	calling *humancalling.Module,
	identity access.Identity,
	name string,
	previous *callingStateObservation,
) callingStateObservation {
	t.Helper()
	ctx := context.Background()
	state, notModified, err := calling.ReadCallingStateConditionally(ctx, identity, "")
	if err != nil || notModified {
		t.Fatalf("%s: full Calling state notModified=%t err=%v", name, notModified, err)
	}
	stable := state
	stable.ETag = ""
	stable.Softphone.LeaseExpiresAt = time.Time{}
	body, err := json.Marshal(stable)
	if err != nil {
		t.Fatal(err)
	}
	current := callingStateObservation{name: name, state: state, body: string(body)}
	if previous == nil {
		return current
	}
	_, notModified, err = calling.ReadCallingStateConditionally(ctx, identity, previous.state.ETag)
	if err != nil {
		t.Fatalf("%s: conditional Calling state: %v", name, err)
	}
	unchanged := current.body == previous.body
	if notModified != unchanged || (current.state.ETag == previous.state.ETag) != unchanged {
		t.Errorf("%s: body unchanged=%t, notModified=%t, ETag unchanged=%t",
			name, unchanged, notModified, current.state.ETag == previous.state.ETag)
	}
	return current
}

func assertETagsMatchExactlyWhenBodiesMatch(t *testing.T, observations []callingStateObservation) {
	t.Helper()
	for index, first := range observations {
		for _, second := range observations[index+1:] {
			sameBody := first.body == second.body
			sameETag := first.state.ETag == second.state.ETag
			if sameBody != sameETag {
				t.Errorf("%s vs %s: same body=%t, same ETag=%t",
					first.name, second.name, sameBody, sameETag)
			}
		}
	}
}

func TestCallingStateETagChangesExactlyWhenTheStableBodyChanges(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, staff := provisionConcurrentStaff(t, pool, accessModule, now, "etag-drift", 2)
	requester, recipient := staff[0], staff[1]
	calling := humancalling.New(db.As(testdb.Portal), accessModule, nil,
		humancalling.Config{}, func() time.Time { return now },
	)
	readiness := humancalling.ReadinessCommand{
		Identity: recipient, SessionID: "etag-drift-recipient", Registered: true,
		MicrophoneReady: true, AudioReady: true, SessionHealthy: true, Available: true,
	}
	if _, err := calling.AcquireSoftphone(ctx, recipient, readiness.SessionID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := calling.SetReadiness(ctx, readiness); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		WITH handoff AS (
			INSERT INTO human_calling_handoffs (
				service_subject, practice_id, location_id, source_call_id,
				idempotency_key, input_fingerprint, phone, display_name,
				transfer_reason, expires_at, created_at
			)
			VALUES ('abita-etag-drift', $1, $2, 'etag-drift-source', 'etag-drift-handoff',
				'\x00', '+15555550100', 'Pat Example', 'Billing question',
				$3::timestamptz + interval '1 hour', $3)
			RETURNING id
		), call AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point, source_handoff_id,
				caller_phone, created_at, updated_at
			)
			SELECT $1, $2, 'INBOUND', 'AI_HANDOFF', handoff.id, '+15555550100', $3, $3
			FROM handoff
			RETURNING id
		), legs AS (
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, staff_subject, state,
				answered_at, bridge_pending_at, bridged_at, created_at, updated_at
			)
			SELECT call.id, leg.role, leg.sequence, leg.staff_subject, leg.state,
				leg.connected, leg.connected, leg.connected, $3, $3
			FROM call
			CROSS JOIN (VALUES
				('CALLER', 1, NULL, 'BRIDGED', $3::timestamptz),
				('STAFF', 2, $4, 'BRIDGED', $3::timestamptz),
				('STAFF', 3, $5, 'PENDING', NULL::timestamptz)
			) leg (role, sequence, staff_subject, state, connected)
			RETURNING id, call_id, sequence
		), command AS (
			INSERT INTO human_calling_provider_commands (call_id, call_leg_id, action, state)
			SELECT call_id, id, 'TRANSFER_STAFF', 'PENDING' FROM legs WHERE sequence = 3
			RETURNING id
		)
		INSERT INTO human_calling_staff_transfers (
			call_id, practice_id, location_id, source_staff_leg_id, target_staff_leg_id,
			customer_leg_id, provider_command_id, requested_by_subject,
			requested_by_session_id, recipient_subject, recipient_session_id,
			idempotency_key, handoff_note, state, expires_at, created_at, updated_at
		)
		SELECT call.id, $1, $2, source.id, target.id, customer.id, command.id,
			$4, 'etag-drift-requester', $5, $6, 'etag-drift-transfer',
			'Synthetic handoff note', 'REQUESTED', $3::timestamptz + interval '30 seconds', $3, $3
		FROM call, command
		JOIN legs source ON source.sequence = 2
		JOIN legs target ON target.sequence = 3
		JOIN legs customer ON customer.sequence = 1
	`, authorization.Practice.ID, authorization.Locations[0].ID, now,
		requester.Subject, recipient.Subject, readiness.SessionID); err != nil {
		t.Fatal(err)
	}

	initial := observeCallingState(t, calling, recipient, "initial", nil)
	if len(initial.state.Ringing) != 1 || len(initial.state.Transfers) != 1 {
		t.Fatalf("staff transfer offer not visible: %#v", initial.state)
	}
	offer := initial.state.Ringing[0]
	if offer.DisplayName != "Pat Example" || offer.LocationName != authorization.Locations[0].Name ||
		offer.OriginatorEmail != requester.Email ||
		initial.state.Transfers[0].RequestedByEmail != requester.Email {
		t.Fatalf("unexpected staff transfer offer: %#v", initial.state)
	}

	observations := []callingStateObservation{initial}
	step := func(name string, change func()) {
		t.Helper()
		change()
		previous := observations[len(observations)-1]
		observations = append(observations, observeCallingState(t, calling, recipient, name, &previous))
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	renameHandoff := func(name string) {
		exec(`UPDATE human_calling_handoffs SET display_name = $1 WHERE source_call_id = 'etag-drift-source'`, name)
	}
	renameLocation := func(name string) {
		exec(`UPDATE access_locations SET name = $1 WHERE id = $2`, name, authorization.Locations[0].ID)
	}
	renameRequester := func(email string) {
		exec(`UPDATE access_memberships SET email = $1 WHERE user_subject = $2`, email, requester.Subject)
	}

	step("lease heartbeat", func() {
		now = now.Add(4 * time.Second)
		lease, err := calling.SetReadiness(ctx, readiness)
		if err != nil {
			t.Fatal(err)
		}
		if !lease.LeaseExpiresAt.After(initial.state.Softphone.LeaseExpiresAt) {
			t.Fatal("heartbeat did not extend the lease")
		}
	})
	step("unrelated Call history", func() {
		exec(`
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				terminal_outcome, ended_at, created_at, updated_at
			)
			VALUES ($1, $2, 'INBOUND', 'STANDALONE', 'RESOLVED', $3, $3, $3)
		`, authorization.Practice.ID, authorization.Locations[0].ID, now)
	})
	step("handoff display name", func() { renameHandoff("Pat Renamed") })
	step("location name", func() { renameLocation("Renamed location") })
	step("requester email", func() { renameRequester("renamed-requester@synthetic.test") })
	step("restored names", func() {
		renameHandoff("Pat Example")
		renameLocation(authorization.Locations[0].Name)
		renameRequester(requester.Email)
	})
	step("second lease heartbeat", func() {
		now = now.Add(4 * time.Second)
		if _, err := calling.SetReadiness(ctx, readiness); err != nil {
			t.Fatal(err)
		}
	})

	if observations[len(observations)-1].body != initial.body {
		t.Fatal("restored state body differs from the initial body")
	}
	assertETagsMatchExactlyWhenBodiesMatch(t, observations)
}

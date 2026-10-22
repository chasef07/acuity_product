package workspace_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func TestPhoneTimelineShowsTheCallingProjectionState(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 16, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	if _, err := access.New(pool, func() time.Time { return now }).Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "timeline-call-state-test",
		Practices: []access.PracticeProvision{{
			Key: "timeline-state", Name: "Timeline State Practice",
			Locations: []access.LocationProvision{{Key: "office", Name: "Office"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "viewer", Email: "viewer@timeline-state.test", Role: access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	viewer := access.Identity{Subject: "timeline-state-viewer", Email: "viewer@timeline-state.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, viewer)
	phone := "+12025550142"

	insertCall := func(t *testing.T, direction, terminal string, startedAt time.Time) string {
		t.Helper()
		var callID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point, destination_phone,
				terminal_outcome, ended_at, disposition_deadline, created_at, updated_at
			) VALUES (
				$1, $2, $3, 'STANDALONE', $4, NULLIF($5, ''),
				CASE WHEN $5 <> '' THEN $6::timestamptz END,
				CASE WHEN $5 = 'ENDED' THEN $6::timestamptz + interval '5 minutes' END,
				$6, $6
			) RETURNING id::text
		`, authorization.Practice.ID, authorization.Locations[0].ID, direction, phone,
			terminal, startedAt).Scan(&callID); err != nil {
			t.Fatalf("insert call: %v", err)
		}
		return callID
	}
	insertLeg := func(t *testing.T, callID, role, state string, bridged bool) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, staff_subject, state,
				answered_at, bridge_pending_at, bridged_at, ended_at
			) VALUES (
				$1, $2, 1, CASE WHEN $2 = 'STAFF' THEN 'timeline-state-staff-' || $3 END, $3,
				CASE WHEN $4 OR $3 = 'ANSWERED' THEN $5::timestamptz END,
				CASE WHEN $4 THEN $5::timestamptz END,
				CASE WHEN $4 THEN $5::timestamptz END,
				CASE WHEN $3 = 'ENDED' THEN $5::timestamptz END
			)
		`, callID, role, state, bridged, now); err != nil {
			t.Fatalf("insert %s leg: %v", role, err)
		}
	}
	insertCommand := func(t *testing.T, callID, action string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO human_calling_provider_commands (call_id, action, target_id, state)
			VALUES ($1, $2, 'synthetic-' || $2, 'SENT')
		`, callID, action); err != nil {
			t.Fatalf("insert %s command: %v", action, err)
		}
	}

	preparing := insertCall(t, "OUTBOUND", "", now.Add(-7*time.Minute))
	answered := insertCall(t, "OUTBOUND", "", now.Add(-6*time.Minute))
	insertLeg(t, answered, "STAFF", "ANSWERED", false)
	destinationBridged := insertCall(t, "OUTBOUND", "", now.Add(-5*time.Minute))
	insertLeg(t, destinationBridged, "DESTINATION", "BRIDGED", true)
	greeting := insertCall(t, "INBOUND", "", now.Add(-4*time.Minute))
	insertCommand(t, greeting, "SPEAK_VOICEMAIL")
	recording := insertCall(t, "INBOUND", "", now.Add(-3*time.Minute))
	insertCommand(t, recording, "SPEAK_VOICEMAIL")
	insertCommand(t, recording, "START_VOICEMAIL_RECORDING")
	needsDisposition := insertCall(t, "INBOUND", "ENDED", now.Add(-2*time.Minute))
	insertLeg(t, needsDisposition, "STAFF", "ENDED", true)
	missed := insertCall(t, "INBOUND", "MISSED", now.Add(-time.Minute))

	page, err := workspace.New(db.As(testdb.Portal), accessModule).QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{
		Identity: viewer, PracticeID: authorization.Practice.ID, Phone: phone, Ungrouped: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	timelineStates := map[string]humancalling.CallState{}
	for _, item := range page.Items {
		if item.Type == "CALL" {
			timelineStates[item.Call.ID] = item.Call.Outcome
		}
	}
	calling := humancalling.New(db.As(testdb.Portal), accessModule, nil,
		humancalling.Config{}, func() time.Time { return now },
	)
	for name, callID := range map[string]string{
		"outbound before any leg": preparing,
		"answered staff leg":      answered,
		"bridged destination leg": destinationBridged,
		"voicemail greeting":      greeting,
		"voicemail recording":     recording,
		"ended after a bridge":    needsDisposition,
		"missed":                  missed,
	} {
		call, err := calling.ReadCall(ctx, viewer, callID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, ok := timelineStates[callID]; !ok || got != call.State {
			t.Errorf("%s: timeline state %q, calling projection state %q", name, got, call.State)
		}
	}
}

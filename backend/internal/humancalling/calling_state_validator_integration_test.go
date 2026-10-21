package humancalling_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCallingStateValidatorTracksSoftphoneChangesButNotHeartbeats(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	_, identities := provisionConcurrentStaff(t, db.Owner, accessModule, now, "lease-validator", 1)
	staff := identities[0]
	calling := humancalling.New(db.As(testdb.Portal), accessModule, nil,
		humancalling.Config{}, func() time.Time { return now },
	)
	command := humancalling.ReadinessCommand{
		Identity: staff, SessionID: "lease-validator-session", Registered: true,
		MicrophoneReady: true, AudioReady: true, SessionHealthy: true, Available: true,
	}
	if _, err := calling.AcquireSoftphone(ctx, staff, command.SessionID, false); err != nil {
		t.Fatal(err)
	}
	setReadiness := func(t *testing.T, command humancalling.ReadinessCommand) humancalling.SoftphoneState {
		t.Helper()
		state, err := calling.SetReadiness(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	readState := func(t *testing.T) humancalling.CallingState {
		t.Helper()
		state, err := calling.ReadCallingState(ctx, staff)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	assertModified := func(t *testing.T, before humancalling.CallingState, wantModified bool) humancalling.CallingState {
		t.Helper()
		after, notModified, err := calling.ReadCallingStateConditionally(ctx, staff, before.ETag)
		if err != nil {
			t.Fatal(err)
		}
		if notModified == wantModified || (after.ETag != before.ETag) != wantModified {
			t.Fatalf("validator changed=%t, want %t", !notModified, wantModified)
		}
		return after
	}
	setReadiness(t, command)

	t.Run("unchanged heartbeat", func(t *testing.T) {
		before := readState(t)
		now = now.Add(4 * time.Second)
		lease := setReadiness(t, command)
		if !lease.LeaseExpiresAt.After(before.Softphone.LeaseExpiresAt) {
			t.Fatal("heartbeat did not extend the lease")
		}
		assertModified(t, before, false)
	})
	for _, testCase := range []struct {
		name   string
		change func(*humancalling.ReadinessCommand)
	}{
		{"registered", func(command *humancalling.ReadinessCommand) { command.Registered = false }},
		{"microphone ready", func(command *humancalling.ReadinessCommand) { command.MicrophoneReady = false }},
		{"audio ready", func(command *humancalling.ReadinessCommand) { command.AudioReady = false }},
		{"session healthy", func(command *humancalling.ReadinessCommand) { command.SessionHealthy = false }},
		{"desired available", func(command *humancalling.ReadinessCommand) { command.Available = false }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			before := readState(t)
			changed := command
			testCase.change(&changed)
			setReadiness(t, changed)
			after := assertModified(t, before, true)
			if !before.Softphone.Available || after.Softphone.Available {
				t.Fatal("readiness loss did not change available state")
			}
			setReadiness(t, command)
		})
	}
	t.Run("session ownership", func(t *testing.T) {
		before := readState(t)
		command.SessionID = "replacement-session"
		if _, err := calling.AcquireSoftphone(ctx, staff, command.SessionID, true); err != nil {
			t.Fatal(err)
		}
		setReadiness(t, command)
		after := assertModified(t, before, true)
		if after.Softphone.SessionID != command.SessionID || !after.Softphone.Available {
			t.Fatalf("unexpected replacement softphone: %+v", after.Softphone)
		}
	})
	t.Run("lease expiry", func(t *testing.T) {
		before := readState(t)
		now = before.Softphone.LeaseExpiresAt
		after := assertModified(t, before, true)
		if !before.Softphone.Owner || after.Softphone.Owner || after.Softphone.Available {
			t.Fatalf("expired softphone remains owned or available: %+v", after.Softphone)
		}
	})
}

func TestCallingStateValidatorAvoidsSequentialScansAtVolume(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 17, 0, 0, 0, time.UTC)
	authorization, identities := provisionConcurrentStaff(
		t, db.Owner, access.New(db.As(testdb.Portal), func() time.Time { return now }), now, "calling-state-plan", 2,
	)
	if _, err := pool.Exec(ctx, `
		WITH inserted AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				terminal_outcome, ended_at, created_at, updated_at
			)
			SELECT $1, $2, 'INBOUND', 'STANDALONE',
				CASE WHEN n = 1 THEN NULL WHEN n % 6 = 0 THEN 'VOICEMAIL' ELSE 'RESOLVED' END,
				CASE WHEN n = 1 THEN NULL ELSE $3::timestamptz - interval '1 hour' END,
				$3::timestamptz - n * interval '1 minute',
				$3::timestamptz - n * interval '1 minute'
			FROM generate_series(1, 4000) n RETURNING id, terminal_outcome, created_at
		), commands AS (
			INSERT INTO human_calling_provider_commands (
				call_id, action, target_id, state, created_at, updated_at
			)
			SELECT id, 'HANGUP_LEG', 'synthetic-' || ordinal, 'RECONCILED', $3, $3
			FROM inserted CROSS JOIN generate_series(1, 7) ordinal
		), voicemail_commands AS (
			INSERT INTO human_calling_provider_commands (
				call_id, action, target_id, state, created_at, updated_at
			)
			SELECT id, 'SPEAK_VOICEMAIL', 'synthetic-voicemail', 'RECONCILED', $3, $3
			FROM inserted WHERE terminal_outcome = 'VOICEMAIL'
		)
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, staff_subject, state,
			answered_at, bridge_pending_at, bridged_at,
			ended_at, created_at, updated_at
		)
		SELECT id, CASE WHEN ordinal = 1 THEN 'CALLER' ELSE 'STAFF' END, ordinal,
			CASE WHEN ordinal > 1 THEN $4 END,
			CASE WHEN terminal_outcome IS NULL THEN 'RINGING' ELSE 'ENDED' END,
			CASE WHEN bridged THEN $3::timestamptz END,
			CASE WHEN bridged THEN $3::timestamptz END,
			CASE WHEN bridged THEN $3::timestamptz END,
			CASE WHEN terminal_outcome IS NULL THEN NULL ELSE $3::timestamptz END,
			$3, $3
		FROM inserted
		CROSS JOIN generate_series(1, 2) ordinal
		CROSS JOIN LATERAL (
			SELECT ordinal > 1 AND terminal_outcome = 'RESOLVED'
				AND extract(epoch FROM $3::timestamptz - created_at)::int % 600 < 180 AS bridged
		) leg
	`, authorization.Practice.ID, authorization.Locations[0].ID, now,
		identities[1].Subject); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"human_calling_calls", "human_calling_call_legs", "human_calling_provider_commands",
	} {
		if _, err := pool.Exec(ctx, "ANALYZE "+table); err != nil {
			t.Fatal(err)
		}
	}

	trace := &callingStateQueryTrace{}
	config := pool.Config().Copy()
	config.ConnConfig.Tracer = trace
	tracedPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tracedPool.Close)
	calling := humancalling.New(tracedPool,
		access.New(tracedPool, func() time.Time { return now }), nil,
		humancalling.Config{}, func() time.Time { return now },
	)
	for _, identity := range identities {
		if _, err := calling.ReadCallingState(ctx, identity); err != nil {
			t.Fatal(err)
		}
		if trace.sql == "" {
			t.Fatal("Calling validator query not captured")
		}
		var rawPlan []byte
		if err := pool.QueryRow(ctx,
			"EXPLAIN (FORMAT JSON) "+trace.sql, trace.args...,
		).Scan(&rawPlan); err != nil {
			t.Fatal(err)
		}
		var plans []struct {
			Plan planNode `json:"Plan"`
		}
		if err := json.Unmarshal(rawPlan, &plans); err != nil || len(plans) != 1 {
			t.Fatalf("decode Calling validator plan: %v", err)
		}
		for _, relation := range plans[0].Plan.sequentialScans() {
			switch relation {
			case "human_calling_calls", "human_calling_provider_commands":
				t.Errorf("%s: Calling validator sequentially scans %s; plan: %s",
					identity.Subject, relation, rawPlan)
			}
		}
	}
}

type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	Plans        []planNode `json:"Plans"`
}

func (node planNode) sequentialScans() []string {
	var relations []string
	if node.NodeType == "Seq Scan" {
		relations = append(relations, node.RelationName)
	}
	for _, child := range node.Plans {
		relations = append(relations, child.sequentialScans()...)
	}
	return relations
}

func TestCallingStateValidatorTracksOnlyLiveOrUndisposedOwnCallLegs(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 17, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, identities := provisionConcurrentStaff(
		t, db.Owner, accessModule, now, "calling-state-own-legs", 2,
	)
	staff, other := identities[0], identities[1]
	calling := humancalling.New(db.As(testdb.Portal), accessModule, nil,
		humancalling.Config{}, func() time.Time { return now },
	)

	insertCall := func(terminalOutcome string, disposed bool) string {
		t.Helper()
		var callID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point, terminal_outcome,
				ended_at, disposition_deadline, disposition_at, disposition_outcome
			)
			VALUES ($1, $2, 'INBOUND', 'STANDALONE', NULLIF($3, ''),
				CASE WHEN $3 = '' THEN NULL ELSE $4::timestamptz END,
				CASE WHEN $3 = 'ENDED' AND NOT $5 THEN $4::timestamptz + interval '1 hour' END,
				CASE WHEN $5 THEN $4::timestamptz END,
				CASE WHEN $5 THEN 'RESOLVED' END)
			RETURNING id::text
		`, authorization.Practice.ID, authorization.Locations[0].ID,
			terminalOutcome, now, disposed).Scan(&callID); err != nil {
			t.Fatal(err)
		}
		return callID
	}
	insertStaffLeg := func(callID, subject, state string, answered, bridged bool) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, staff_subject, state,
				answered_at, bridge_pending_at, bridged_at, ending_at, ended_at
			)
			VALUES ($1, 'STAFF', 2, $2, $3,
				CASE WHEN $4 OR $5 THEN $6::timestamptz END,
				CASE WHEN $5 THEN $6::timestamptz END,
				CASE WHEN $5 THEN $6::timestamptz END,
				CASE WHEN $3 IN ('ENDING', 'ENDED') THEN $6::timestamptz END,
				CASE WHEN $3 = 'ENDED' THEN $6::timestamptz END)
		`, callID, subject, state, answered, bridged, now); err != nil {
			t.Fatal(err)
		}
	}

	type ownLegCase struct {
		name    string
		callID  string
		tracked bool
	}
	cases := []ownLegCase{}
	add := func(name string, tracked bool, terminalOutcome string, disposed bool,
		subject, state string, answered, bridged bool,
	) {
		callID := insertCall(terminalOutcome, disposed)
		insertStaffLeg(callID, subject, state, answered, bridged)
		cases = append(cases, ownLegCase{name: name, callID: callID, tracked: tracked})
	}
	add("ringing own leg", true, "", false, staff.Subject, "RINGING", false, false)
	add("answered ending own leg", true, "", false, staff.Subject, "ENDING", true, false)
	add("unanswered ending own leg", false, "", false, staff.Subject, "ENDING", false, false)
	add("bridged own leg awaiting disposition", true, "ENDED", false, staff.Subject, "ENDED", true, true)
	add("bridged own leg after disposition", false, "ENDED", true, staff.Subject, "ENDED", true, true)
	add("unbridged own leg awaiting disposition", false, "ENDED", false, staff.Subject, "ENDED", true, false)
	add("bridged own leg on resolved call", false, "RESOLVED", false, staff.Subject, "ENDED", true, true)
	add("other staff ringing leg", false, "", false, other.Subject, "RINGING", false, false)
	add("other staff leg awaiting disposition", false, "ENDED", false, other.Subject, "ENDED", true, true)

	state, err := calling.ReadCallingState(ctx, staff)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range cases {
		if _, err := pool.Exec(ctx, `
			UPDATE human_calling_calls SET version = version + 1 WHERE id = $1
		`, testCase.callID); err != nil {
			t.Fatal(err)
		}
		next, notModified, err := calling.ReadCallingStateConditionally(ctx, staff, state.ETag)
		if err != nil {
			t.Fatal(err)
		}
		if notModified == testCase.tracked {
			t.Errorf("%s: validator changed=%t, want %t", testCase.name, !notModified, testCase.tracked)
		}
		if !notModified {
			state = next
		}
	}
}

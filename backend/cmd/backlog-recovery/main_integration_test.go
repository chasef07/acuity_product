package main

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCallingVerificationRequiresCommittedTerminalEvidence(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	const callID = "00000000-0000-0000-0000-000000000254"
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ('00000000-0000-0000-0000-000000000251', 'recovery-verify', 'Recovery Verify');
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES ('00000000-0000-0000-0000-000000000252',
			'00000000-0000-0000-0000-000000000251', 'main', 'Main');
		INSERT INTO human_calling_calls (
			id, practice_id, location_id, direction, entry_point, terminal_outcome, ended_at
		) VALUES ('00000000-0000-0000-0000-000000000254',
			'00000000-0000-0000-0000-000000000251', '00000000-0000-0000-0000-000000000252',
			'OUTBOUND', 'STANDALONE', 'UNANSWERED', '2026-09-01T12:00:00Z');
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, staff_subject, state, ended_at
		) VALUES ('00000000-0000-0000-0000-000000000255',
			'00000000-0000-0000-0000-000000000254', 'STAFF', 1, 'synthetic-staff',
			'ENDED', '2026-09-01T12:00:00Z');
		INSERT INTO human_calling_provider_commands (call_id, call_leg_id, action, state)
		VALUES ('00000000-0000-0000-0000-000000000254',
			'00000000-0000-0000-0000-000000000255', 'HANGUP_LEG', 'RECONCILED');
		INSERT INTO human_calling_provider_receipts (
			event_id, call_id, event_type, signature_timestamp, raw_body, state, projected_at
		) VALUES ('recovered-ringtone', '00000000-0000-0000-0000-000000000254',
			'call.playback.ended', 1, '\x7b7d', 'APPLIED', '2026-09-01T12:00:01Z');
		INSERT INTO access_audit_events (actor_type, actor_subject, action, details)
		VALUES ('PLATFORM_OPERATOR', 'synthetic-operator', 'provider_receipt.recovered',
			'{"resourceId":"recovered-ringtone"}');
	`); err != nil {
		t.Fatalf("seed committed calling recovery: %v", err)
	}
	if err := verify(ctx, pool, "calling", callID); err != nil {
		t.Fatalf("completed recovery should verify: %v", err)
	}

	for _, scenario := range []struct {
		name    string
		change  string
		restore string
	}{
		{
			name:    "sent_hangup_still_awaits_confirmation",
			change:  `UPDATE human_calling_provider_commands SET state='SENT' WHERE call_id=$1`,
			restore: `UPDATE human_calling_provider_commands SET state='RECONCILED' WHERE call_id=$1`,
		},
		{
			name:    "terminal_outcome_without_end_time",
			change:  `UPDATE human_calling_calls SET ended_at=NULL WHERE id=$1`,
			restore: `UPDATE human_calling_calls SET ended_at='2026-09-01T12:00:00Z' WHERE id=$1`,
		},
		{
			name:    "end_time_without_terminal_outcome",
			change:  `UPDATE human_calling_calls SET terminal_outcome=NULL WHERE id=$1`,
			restore: `UPDATE human_calling_calls SET terminal_outcome='UNANSWERED' WHERE id=$1`,
		},
		{
			name:    "call_leg_still_ending",
			change:  `UPDATE human_calling_call_legs SET state='ENDING',ended_at=NULL WHERE call_id=$1`,
			restore: `UPDATE human_calling_call_legs SET state='ENDED',ended_at='2026-09-01T12:00:00Z' WHERE call_id=$1`,
		},
		{
			name:    "receipt_not_applied",
			change:  `UPDATE human_calling_provider_receipts SET state='PENDING',projected_at=NULL WHERE call_id=$1`,
			restore: `UPDATE human_calling_provider_receipts SET state='APPLIED',projected_at='2026-09-01T12:00:01Z' WHERE call_id=$1`,
		},
		{
			name:    "audit_for_another_receipt",
			change:  `UPDATE access_audit_events SET details='{"resourceId":"another-receipt"}' WHERE details->>'resourceId'=(SELECT event_id FROM human_calling_provider_receipts WHERE call_id=$1)`,
			restore: `UPDATE access_audit_events SET details=jsonb_build_object('resourceId',(SELECT event_id FROM human_calling_provider_receipts WHERE call_id=$1)) WHERE details->>'resourceId'='another-receipt'`,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, scenario.change, callID); err != nil {
				t.Fatal(err)
			}
			if err := verify(ctx, pool, "calling", callID); err == nil {
				t.Error("incomplete recovery was incorrectly verified")
			} else if err.Error() != "state and audit did not converge" {
				t.Errorf("verification failed for an unexpected reason: %v", err)
			}
			if _, err := pool.Exec(ctx, scenario.restore, callID); err != nil {
				t.Fatal(err)
			}
			if err := verify(ctx, pool, "calling", callID); err != nil {
				t.Fatalf("recovery should verify after evidence converges: %v", err)
			}
		})
	}
}

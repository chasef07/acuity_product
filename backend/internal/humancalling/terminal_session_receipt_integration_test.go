package humancalling_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestTerminalSessionOrphanReceiptStopsRetrying(t *testing.T) {
	for _, tc := range []struct {
		name, eventType, setup, code string
		commands                     int
		duringRead                   bool
		ended                        bool
		providerErr                  error
	}{
		{name: "ended answer", eventType: "call.answered", ended: true, code: "TERMINAL_OR_OBSOLETE_PROVIDER_FACT"},
		{name: "ended hangup", eventType: "call.hangup", ended: true, code: "TERMINAL_OR_OBSOLETE_PROVIDER_FACT"},
		{name: "provider still alive", eventType: "call.hangup", code: "WAITING_FOR_RELATED_FACT"},
		{name: "provider unavailable", eventType: "call.hangup", providerErr: errors.New("synthetic unavailable"), code: "PROJECTION_OBSERVE_ORPHAN_RETRY"},
		{name: "state changes during provider read", eventType: "call.hangup", ended: true, duringRead: true, code: "WAITING_FOR_RELATED_FACT"},
		{name: "pending command", eventType: "call.hangup", ended: true, setup: "INSERT INTO human_calling_provider_commands (call_id,action,state) VALUES ('00000000-0000-0000-0000-000000000904','STOP_RING_WINDOW','PENDING')", commands: 1, code: "WAITING_FOR_RELATED_FACT"},
		{name: "ambiguous session", eventType: "call.hangup", ended: true, setup: "INSERT INTO human_calling_calls (id,practice_id,location_id,caller_phone,terminal_outcome,ended_at) VALUES ('00000000-0000-0000-0000-000000000905','00000000-0000-0000-0000-000000000901','00000000-0000-0000-0000-000000000902','+15555550101','RESOLVED','2026-09-09T08:00:00Z'); INSERT INTO human_calling_call_legs (call_id,role,sequence,state,provider_call_session_id,ended_at) VALUES ('00000000-0000-0000-0000-000000000905','CALLER',1,'ENDED','shared-session','2026-09-09T08:00:00Z')", code: "WAITING_FOR_RELATED_FACT"},
		{name: "owned control", eventType: "call.hangup", ended: true, setup: "UPDATE human_calling_call_legs SET provider_call_control_id='orphan-control'", code: "WAITING_FOR_RELATED_FACT"},
		{name: "owned leg", eventType: "call.hangup", ended: true, setup: "UPDATE human_calling_call_legs SET provider_call_leg_id='orphan-leg'", code: "WAITING_FOR_RELATED_FACT"},
		{name: "active call", eventType: "call.hangup", ended: true, setup: "UPDATE human_calling_calls SET terminal_outcome=NULL, ended_at=NULL", code: "WAITING_FOR_RELATED_FACT"},
		{name: "active leg", eventType: "call.hangup", ended: true, setup: "UPDATE human_calling_call_legs SET state='RINGING', ended_at=NULL", code: "WAITING_FOR_RELATED_FACT"},
		{name: "unknown session", eventType: "call.hangup", ended: true, setup: "UPDATE human_calling_call_legs SET provider_call_session_id='different-session'", code: "WAITING_FOR_RELATED_FACT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eventType := tc.eventType
			pool := testdb.Open(t)
			ctx := context.Background()
			now := time.Date(2026, time.September, 9, 20, 0, 0, 0, time.UTC)
			const callID = "00000000-0000-0000-0000-000000000904"
			_, err := pool.Exec(ctx, `
				INSERT INTO access_practices (id, provisioning_key, name)
				VALUES ('00000000-0000-0000-0000-000000000901', 'orphan-test', 'Synthetic');
				INSERT INTO access_locations (id, practice_id, provisioning_key, name)
				VALUES ('00000000-0000-0000-0000-000000000902', '00000000-0000-0000-0000-000000000901', 'main', 'Main');
				INSERT INTO human_calling_handoffs (id, service_subject, practice_id, location_id, source_call_id, idempotency_key, input_fingerprint, expires_at)
				VALUES ('00000000-0000-0000-0000-000000000903', 'synthetic', '00000000-0000-0000-0000-000000000901', '00000000-0000-0000-0000-000000000902', 'synthetic', 'synthetic', '\x01', '2026-09-09T09:00:00Z');
				INSERT INTO human_calling_calls (id, source_handoff_id, practice_id, location_id, caller_phone, terminal_outcome, ended_at)
				VALUES ('00000000-0000-0000-0000-000000000904', '00000000-0000-0000-0000-000000000903', '00000000-0000-0000-0000-000000000901', '00000000-0000-0000-0000-000000000902', '+15555550100', 'RESOLVED', '2026-09-09T08:00:00Z');
				INSERT INTO human_calling_call_legs (call_id, role, sequence, state, provider_connection_id, provider_call_control_id, provider_call_leg_id, provider_call_session_id, ended_at)
				VALUES ('00000000-0000-0000-0000-000000000904', 'CALLER', 1, 'ENDED', 'expected-connection', 'known-control', 'known-leg', 'shared-session', '2026-09-09T08:00:00Z');
			`)
			if err != nil {
				t.Fatal(err)
			}
			if tc.setup != "" {
				if _, err := pool.Exec(ctx, tc.setup); err != nil {
					t.Fatal(err)
				}
			}
			raw := []byte(fmt.Sprintf(`{"data":{"record_type":"event","event_type":%q,"id":"orphan-event","occurred_at":"2026-09-09T08:01:00Z","payload":{"connection_id":"expected-connection","call_control_id":"orphan-control","call_leg_id":"orphan-leg","call_session_id":"shared-session"}}}`, eventType))
			if _, err := pool.Exec(ctx, `INSERT INTO human_calling_provider_receipts (event_id, event_type, raw_body, signature_timestamp, received_at, next_attempt_at) VALUES ('orphan-event', $1, $2, 1, $3, $3)`, eventType, raw, now); err != nil {
				t.Fatal(err)
			}
			const snapshotSQL = `SELECT jsonb_build_object('calls',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM human_calling_calls c),'legs',(SELECT jsonb_agg(to_jsonb(l) ORDER BY l.id) FROM human_calling_call_legs l),'commands',(SELECT jsonb_agg(to_jsonb(pc) ORDER BY pc.id) FROM human_calling_provider_commands pc))::text`
			var before string
			if err := pool.QueryRow(ctx, snapshotSQL).Scan(&before); err != nil {
				t.Fatal(err)
			}
			provider := &orphanReceiptProvider{recordingProvider: &recordingProvider{}, ended: tc.ended, err: tc.providerErr}
			if tc.duringRead {
				provider.duringRead = func() {
					if _, err := pool.Exec(ctx, "UPDATE human_calling_calls SET terminal_outcome=NULL,ended_at=NULL"); err != nil {
						t.Fatal(err)
					}
				}
			}
			calling := humancalling.New(pool, nil, provider, humancalling.Config{CallControlID: "expected-connection"}, func() time.Time { return now })
			if processed, err := calling.ProcessNextReceipt(ctx); err != nil || !processed {
				t.Fatalf("process orphan: processed=%t err=%v", processed, err)
			}
			var state, code string
			var attached bool
			if err := pool.QueryRow(ctx, `SELECT state, COALESCE(projection_error_code,''), call_id IS NOT NULL FROM human_calling_provider_receipts WHERE event_id='orphan-event'`).Scan(&state, &code, &attached); err != nil {
				t.Fatal(err)
			}
			wantState := "PENDING"
			if tc.code == "TERMINAL_OR_OBSOLETE_PROVIDER_FACT" {
				wantState = "FAILED"
			}
			if state != wantState || code != tc.code || attached {
				t.Fatalf("orphan receipt = %s/%s attached=%t, want %s/%s without attachment", state, code, attached, wantState, tc.code)
			}
			var commandCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM human_calling_provider_commands WHERE call_id=$1`, callID).Scan(&commandCount); err != nil {
				t.Fatal(err)
			}
			if commandCount != tc.commands {
				t.Fatalf("obsolete receipt created %d commands", commandCount)
			}
			if !tc.duringRead {
				var after string
				if err := pool.QueryRow(ctx, snapshotSQL).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if before != after {
					t.Fatal("orphan processing changed Call, leg or command state")
				}
			}
			var saved []byte
			if err := pool.QueryRow(ctx, "SELECT raw_body FROM human_calling_provider_receipts WHERE event_id='orphan-event'").Scan(&saved); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(saved, raw) {
				t.Fatal("original provider evidence changed")
			}
			wantChecks := 1
			if tc.setup != "" {
				wantChecks = 0
			}
			if provider.checks != wantChecks {
				t.Fatalf("provider checks=%d, want %d", provider.checks, wantChecks)
			}
			if wantState == "FAILED" {
				if processed, err := calling.ProcessNextReceipt(ctx); err != nil || processed {
					t.Fatalf("terminal receipt was retried: %t %v", processed, err)
				}
			}
		})
	}
}

type orphanReceiptProvider struct {
	*recordingProvider
	ended      bool
	err        error
	duringRead func()
	checks     int
}

func (p *orphanReceiptProvider) IsCallEnded(_ context.Context, controlID, legID, sessionID string) (bool, error) {
	p.checks++
	if controlID != "orphan-control" || legID != "orphan-leg" || sessionID != "shared-session" {
		return false, errors.New("unexpected provider identity")
	}
	if p.duringRead != nil {
		p.duringRead()
	}
	return p.ended, p.err
}

package humancalling_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCredentialLegReceiptsPreserveCallAndClassifyObservation(t *testing.T) {
	for _, scenario := range []string{"tracked", "arrives_before_staff_session", "unmatched_session", "wrong_connection", "ambiguous_session", "wrong_staff_connection", "non_staff_session"} {
		t.Run(scenario, func(t *testing.T) {
			pool := testdb.Open(t)
			ctx := context.Background()
			now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
			const callID = "00000000-0000-0000-0000-000000000804"
			_, err := pool.Exec(ctx, `
    INSERT INTO access_practices(id,provisioning_key,name) VALUES ('00000000-0000-0000-0000-000000000801','credential-observation','Synthetic');
    INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES ('00000000-0000-0000-0000-000000000802','00000000-0000-0000-0000-000000000801','main','Main');
    INSERT INTO human_calling_handoffs(id,service_subject,practice_id,location_id,source_call_id,idempotency_key,input_fingerprint,expires_at)
    VALUES ('00000000-0000-0000-0000-000000000803','synthetic','00000000-0000-0000-0000-000000000801','00000000-0000-0000-0000-000000000802','source','handoff','\x01','2026-09-20T13:00:00Z');
    INSERT INTO human_calling_calls(id,source_handoff_id,practice_id,location_id,caller_phone)
    VALUES ('00000000-0000-0000-0000-000000000804','00000000-0000-0000-0000-000000000803','00000000-0000-0000-0000-000000000801','00000000-0000-0000-0000-000000000802','+15555550123');
    INSERT INTO human_calling_call_legs(call_id,role,sequence,staff_subject,state,provider_connection_id,provider_call_control_id,provider_call_leg_id,provider_call_session_id)
    VALUES ('00000000-0000-0000-0000-000000000804','STAFF',1,'synthetic-staff','RINGING','call-control','staff-control','staff-leg','shared-session');
   `)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "arrives_before_staff_session" {
				if _, err := pool.Exec(ctx, `UPDATE human_calling_call_legs SET provider_call_session_id=NULL`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "wrong_staff_connection" {
				if _, err := pool.Exec(ctx, `UPDATE human_calling_call_legs SET provider_connection_id='another-connection'`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "non_staff_session" {
				if _, err := pool.Exec(ctx, `UPDATE human_calling_call_legs SET role='CALLER',staff_subject=NULL`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "ambiguous_session" {
				if _, err := pool.Exec(ctx, `
     INSERT INTO human_calling_calls(id,practice_id,location_id,caller_phone)
     VALUES ('00000000-0000-0000-0000-000000000805','00000000-0000-0000-0000-000000000801','00000000-0000-0000-0000-000000000802','+15555550124');
     INSERT INTO human_calling_call_legs(call_id,role,sequence,staff_subject,state,provider_connection_id,provider_call_control_id,provider_call_leg_id,provider_call_session_id)
     VALUES ('00000000-0000-0000-0000-000000000805','STAFF',1,'another-staff','RINGING','call-control','another-control','another-leg','shared-session');
    `); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := func() string {
				var value string
				if err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
     'calls',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM human_calling_calls c),
     'legs',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM human_calling_call_legs l),
     'tasks',(SELECT count(*) FROM work_tasks),
     'commands',(SELECT count(*) FROM human_calling_provider_commands),
     'facts',(SELECT count(*) FROM human_calling_projected_facts),
     'rejections',(SELECT count(*) FROM human_calling_rejected_provider_legs))::text`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			calling := humancalling.New(pool, nil, nil, humancalling.Config{
				CallControlID: "call-control", CredentialConnectionID: "credential", WebhookPublicKeys: [][]byte{public},
				Observer: observability.NewLogger(observability.RuntimeWorker, "synthetic", slog.New(slog.NewJSONHandler(&logs, nil))),
			}, func() time.Time { return now })
			occurredAt := now
			receive := func(kind string) humancalling.WebhookReceipt {
				connection, session := "credential", "shared-session"
				if scenario == "wrong_connection" {
					connection = "untrusted"
				}
				if scenario == "unmatched_session" {
					session = "unmatched"
				}
				raw, err := json.Marshal(map[string]any{"data": map[string]any{"record_type": "event", "id": "credential-" + kind, "event_type": kind, "occurred_at": occurredAt.Format(time.RFC3339Nano), "payload": map[string]any{
					"connection_id": connection, "call_control_id": "credential-control", "call_leg_id": "credential-leg", "call_session_id": session, "direction": "incoming",
				}}})
				if err != nil {
					t.Fatal(err)
				}
				stamp := strconv.FormatInt(time.Now().Unix(), 10)
				signature := base64.StdEncoding.EncodeToString(ed25519.Sign(private, append([]byte(stamp+"|"), raw...)))
				receipt, err := calling.ReceiveWebhook(ctx, raw, stamp, signature)
				if err != nil {
					t.Fatal(err)
				}
				return receipt
			}
			read := func(id string) (string, string, string) {
				var state, code, attached string
				if err := pool.QueryRow(ctx, `SELECT state,COALESCE(projection_error_code,''),COALESCE(call_id::text,'') FROM human_calling_provider_receipts WHERE event_id=$1`, id).Scan(&state, &code, &attached); err != nil {
					t.Fatal(err)
				}
				return state, code, attached
			}
			process := func() {
				t.Helper()
				if ok, err := calling.ProcessNextReceipt(ctx); err != nil || !ok {
					t.Fatalf("process: %t %v", ok, err)
				}
			}
			before := snapshot()
			for _, kind := range []string{"call.initiated", "call.answered", "call.bridged", "call.hangup"} {
				receipt := receive(kind)
				process()
				state, code, attached := read(receipt.EventID)
				if scenario == "wrong_connection" {
					if state != "FAILED" || !strings.Contains(code, "HANDOFF_REJECTED") {
						t.Fatalf("unexpected rejection %s %s", state, code)
					}
					continue
				}
				if scenario == "arrives_before_staff_session" && kind == "call.initiated" {
					if state != "PENDING" || code != "WAITING_FOR_RELATED_FACT" {
						t.Fatalf("early credential event: %s %s", state, code)
					}
					if _, err := pool.Exec(ctx, `UPDATE human_calling_call_legs SET provider_call_session_id='shared-session'`); err != nil {
						t.Fatal(err)
					}
					before = snapshot()
					now = now.Add(2 * time.Second)
					process()
					state, code, attached = read(receipt.EventID)
				}
				if scenario == "unmatched_session" || scenario == "ambiguous_session" || scenario == "wrong_staff_connection" || scenario == "non_staff_session" {
					if state != "PENDING" || code != "WAITING_FOR_RELATED_FACT" || attached != "" {
						t.Fatalf("unverified session: %s %s %s", state, code, attached)
					}
					continue
				}
				if state != "IGNORED" || code != "" || attached != callID {
					t.Fatalf("%s: state=%s code=%s attached=%s; want IGNORED on tracked Call", kind, state, code, attached)
				}
				if duplicate := receive(kind); !duplicate.Duplicate || string(duplicate.State) != "IGNORED" {
					t.Fatalf("duplicate=%+v", duplicate)
				}
			}
			if scenario != "wrong_connection" && snapshot() != before {
				t.Fatal("credential observation changed Call, leg, Task, command, fact, or rejection state")
			}
			if scenario == "tracked" || scenario == "arrives_before_staff_session" {
				if strings.Contains(logs.String(), `"reason":"connection"`) || strings.Contains(logs.String(), `"outcome":"failed"`) || !strings.Contains(logs.String(), `"outcome":"ignored"`) {
					t.Fatalf("misclassified metrics: %s", logs.String())
				}
			}
			if scenario == "unmatched_session" || scenario == "ambiguous_session" || scenario == "wrong_staff_connection" || scenario == "non_staff_session" {
				now = now.Add(25 * time.Hour)
				for range 4 {
					process()
				}
				state, code, _ := read("credential-call.initiated")
				if state != "QUARANTINED" || code != "RELATED_FACT_TIMEOUT" {
					t.Fatalf("unmatched receipt was hidden: %s %s", state, code)
				}
			}
		})
	}
}

package interaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestSourceClockRecoveryPreservesEvidenceAndRequiresOperator(t *testing.T) {
	for _, scenario := range []string{"clock_only", "wrong_phone", "ordinary_user", "historical_summary", "conflicting_outcome"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			pool := testdb.Open(t)
			now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
			started := now.Add(-time.Hour)
			var practiceID, locationID, interactionID, receiptID string
			if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('clock-recovery','Clock Recovery') RETURNING id::text`).Scan(&practiceID); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES('operator@example.test','operator')`); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,status,lifecycle_stage) VALUES('agent',$1,$2,'source-call','+15555550101','+15555550102',$3,'IN_PROGRESS',1) RETURNING id::text`, practiceID, locationID, started).Scan(&interactionID); err != nil {
				t.Fatal(err)
			}
			payload := storedReceiptPayload{
				Kind: MessageCloseout, SourceCallID: "source-call", CallerPhone: "+15555550101", OfficePhone: "+15555550102",
				StartedAt: started.Add(time.Minute), EndedAt: &now, Status: CallCompleted,
				Transcript:      json.RawMessage(`{"items":[{"role":"user","content":"Synthetic request"}]}`),
				CloseoutPayload: json.RawMessage(`{"reason":"session_closed"}`),
			}
			if scenario == "wrong_phone" {
				payload.CallerPhone = "+15555550103"
			}
			if scenario == "historical_summary" {
				payload.Kind = MessageKind("SUMMARY")
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint := sha256.Sum256(raw)
			if err := pool.QueryRow(ctx, `INSERT INTO ai_interaction_receipts(service_subject,practice_id,location_id,source_call_id,kind,payload_fingerprint,payload) VALUES('agent',$1,$2,'source-call',$3,$4,$5) RETURNING id::text`, practiceID, locationID, payload.Kind, fingerprint[:], raw).Scan(&receiptID); err != nil {
				t.Fatal(err)
			}
			module := New(pool, access.New(pool, func() time.Time { return now }), func() time.Time { return now })
			if scenario != "historical_summary" {
				if worked, err := module.ProcessNextReceipt(ctx); err != nil || !worked {
					t.Fatalf("reproduce source conflict: worked=%v err=%v", worked, err)
				}
			} else if _, err := pool.Exec(ctx, `UPDATE ai_interaction_receipts SET state='QUARANTINED',projection_error_code='SOURCE_CONFLICT' WHERE id=$1`, receiptID); err != nil {
				t.Fatal(err)
			}
			var state string
			var before []byte
			if err := pool.QueryRow(ctx, `SELECT state,payload FROM ai_interaction_receipts WHERE id=$1`, receiptID).Scan(&state, &before); err != nil || state != "QUARANTINED" {
				t.Fatalf("original failure: state=%s err=%v", state, err)
			}
			if scenario == "conflicting_outcome" {
				if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET status='FAILED',ended_at=$2,lifecycle_stage=3 WHERE id=$1`, interactionID, now); err != nil {
					t.Fatal(err)
				}
			}
			operator := access.Identity{Subject: "operator", Email: "operator@example.test", EmailVerified: true}
			if scenario == "ordinary_user" {
				operator.Subject, operator.Email = "staff", "staff@example.test"
			}
			result, err := module.RecoverSourceClock(ctx, operator, receiptID)
			if scenario == "clock_only" {
				if err != nil || result.Status != CallCompleted || !result.StartedAt.Equal(started) || result.EndedAt == nil || !result.EndedAt.Equal(now) {
					t.Fatalf("recovered result: status=%s start=%s end=%v err=%v", result.Status, result.StartedAt, result.EndedAt, err)
				}
				if _, err := module.RecoverSourceClock(ctx, operator, receiptID); err != nil {
					t.Fatalf("idempotent recovery: %v", err)
				}
			} else if err == nil {
				t.Fatal("unsafe source recovery succeeded")
			} else if scenario == "ordinary_user" && !errors.Is(err, access.ErrDenied) {
				t.Fatalf("unauthorized error=%v", err)
			}
			var after, afterFingerprint []byte
			var audits int
			if err := pool.QueryRow(ctx, `SELECT state,payload,payload_fingerprint,(SELECT count(*) FROM access_audit_events WHERE action='ai_interaction.source_clock_recovered') FROM ai_interaction_receipts WHERE id=$1`, receiptID).Scan(&state, &after, &afterFingerprint, &audits); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) || !bytes.Equal(fingerprint[:], afterFingerprint) {
				t.Fatal("recovery changed immutable receipt evidence")
			}
			if scenario == "clock_only" && (state != "PROJECTED" || audits != 1) {
				t.Fatalf("recovery state=%s audits=%d", state, audits)
			}
			if scenario != "clock_only" && (state != "QUARANTINED" || audits != 0) {
				t.Fatalf("rejected recovery changed state=%s audits=%d", state, audits)
			}
			if scenario == "historical_summary" {
				if err := module.RetireLegacySummary(ctx, operator, receiptID); !errors.Is(err, ErrConflict) {
					t.Fatalf("retirement without terminal closeout error=%v", err)
				}
				payload.Kind, payload.StartedAt = MessageCloseout, started
				closeout, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				closeoutFingerprint := sha256.Sum256(closeout)
				if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_receipts(service_subject,practice_id,location_id,source_call_id,kind,payload_fingerprint,payload) VALUES('agent',$1,$2,'source-call','CLOSEOUT',$3,$4)`, practiceID, locationID, closeoutFingerprint[:], closeout); err != nil {
					t.Fatal(err)
				}
				if processed, err := module.ProcessNextReceipt(ctx); err != nil || !processed {
					t.Fatalf("establish supported closeout: %v %v", processed, err)
				}
				for attempt := 0; attempt < 2; attempt++ {
					if err := module.RetireLegacySummary(ctx, operator, receiptID); err != nil {
						t.Fatalf("retire legacy receipt: %v", err)
					}
				}
				var errorCode, linkedInteraction string
				if err := pool.QueryRow(ctx, `SELECT state,payload,payload_fingerprint,projection_error_code,interaction_id::text,(SELECT count(*) FROM access_audit_events WHERE action='ai_interaction.legacy_receipt_retired') FROM ai_interaction_receipts WHERE id=$1`, receiptID).Scan(&state, &after, &afterFingerprint, &errorCode, &linkedInteraction, &audits); err != nil {
					t.Fatal(err)
				}
				if state != "RETIRED" || errorCode != "SOURCE_CONFLICT" || linkedInteraction != interactionID || audits != 1 || !bytes.Equal(before, after) || !bytes.Equal(fingerprint[:], afterFingerprint) {
					t.Fatalf("legacy retirement lost evidence or audit: state=%s error=%s audits=%d", state, errorCode, audits)
				}
			}
		})
	}
}

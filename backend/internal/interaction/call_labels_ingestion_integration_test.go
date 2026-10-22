package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCloseoutReceiptProjectsSuppliedLabelsWithoutRejectingUnavailableClassification(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	practice, location := seedRetryLocation(t, pool, "label-receipts")
	module := New(db.As(testdb.Worker), access.New(db.As(testdb.Worker), nil), nil)
	ended := time.Now().UTC().Truncate(time.Second)
	for _, c := range []struct{ source, closeout, intent, outcome, status string }{
		{"complete", `{"evaluation":{"status":"incomplete","classification":{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"complete","intent":"office_information","outcome":"resolved","answers":{"private":true}}}}`, "office_information", "resolved", "complete"},
		{"partial", `{"evaluation":{"classification":{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"incomplete","intent":"speak_to_staff","reason":"outcome_unavailable"}}}`, "speak_to_staff", "", "failed"},
		{"malformed", `{"evaluation":{"classification":"bad"}}`, "", "", "failed"},
		{"legacy", `{}`, "", "", "skipped"},
	} {
		t.Run(c.source, func(t *testing.T) {
			payload := storedReceiptPayload{Kind: MessageCloseout, SourceCallID: c.source, CallerPhone: "+15555550123", OfficePhone: "+15555550100", StartedAt: ended.Add(-time.Minute), EndedAt: &ended, Status: CallCompleted, Summary: "Valid call summary", Transcript: json.RawMessage(`{"items":[]}`), CloseoutPayload: json.RawMessage(c.closeout)}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint := sha256.Sum256(raw)
			if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_receipts(service_subject,practice_id,location_id,source_call_id,kind,payload_fingerprint,payload) VALUES('agent',$1,$2,$3,'CLOSEOUT',$4,$5)`, practice, location, c.source, fingerprint[:], raw); err != nil {
				t.Fatal(err)
			}
			if processed, err := module.ProcessNextReceipt(ctx); err != nil || !processed {
				t.Fatalf("receipt=%v %v", processed, err)
			}
			var state, summary, intent, outcome, status string
			if err := pool.QueryRow(ctx, `SELECT receipt.state,interaction.summary,COALESCE(labels.intent,''),COALESCE(labels.outcome,''),labels.status FROM ai_interaction_receipts receipt JOIN ai_interactions interaction ON interaction.id=receipt.interaction_id JOIN ai_interaction_classifications labels ON labels.interaction_id=interaction.id WHERE receipt.source_call_id=$1`, c.source).Scan(&state, &summary, &intent, &outcome, &status); err != nil {
				t.Fatal(err)
			}
			if state != "PROJECTED" || summary != "Valid call summary" || intent != c.intent || outcome != c.outcome || status != c.status {
				t.Fatalf("receipt result=%s %s %s %s %s", state, summary, intent, outcome, status)
			}
			if processed, err := module.ProcessNextReceipt(ctx); err != nil || processed {
				t.Fatalf("duplicate receipt=%v %v", processed, err)
			}
		})
	}
}

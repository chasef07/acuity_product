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

func TestCloseoutProjectionRecordsScorecard(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('scorecard-projection','Scorecard Projection') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	payload := storedReceiptPayload{
		Kind: MessageCloseout, SourceCallID: "scorecard-call", CallerPhone: "+15555550101", OfficePhone: "+15555550102",
		StartedAt: now.Add(-2 * time.Minute), EndedAt: &now, Status: CallCompleted,
		Transcript:      syntheticTranscript(t, syntheticTool{"resolve_patient", `{"firstName":"Synthetic"}`, "no_results: A complete search found no matching patient."}, syntheticTool{"transfer_call", `{}`, "accepted: Provider accepted the transfer."}),
		CloseoutPayload: json.RawMessage(`{"versions":{"agent":"0.14.0"},"domainOutcomes":[],"evaluation":{"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v6","jurors":["typesafe-ai/jev"],"status":"complete","results":{"booking_requested":{"verdict":false,"probability":0.2,"votes":{"typesafe-ai/jev":0.2},"errors":{}}}}}`),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(raw)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_receipts(service_subject,practice_id,location_id,source_call_id,kind,payload_fingerprint,payload) VALUES('agent',$1,$2,'scorecard-call',$3,$4,$5)`, practiceID, locationID, payload.Kind, fingerprint[:], raw); err != nil {
		t.Fatal(err)
	}
	module := New(pool, access.New(pool, func() time.Time { return now }), func() time.Time { return now })
	if worked, err := module.ProcessNextReceipt(ctx); err != nil || !worked {
		t.Fatalf("project closeout: worked=%v err=%v", worked, err)
	}
	rows, err := pool.Query(ctx, `SELECT question, source, answer, scorecard_version, COALESCE(judge_model,''), agent_version FROM ai_interaction_scorecard_answers ORDER BY question`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var question, source, version, model, agent string
		var answer bool
		if err := rows.Scan(&question, &source, &answer, &version, &model, &agent); err != nil {
			t.Fatal(err)
		}
		if agent != "0.14.0" {
			t.Fatalf("agent version: %q", agent)
		}
		got[question] = source + ":" + map[bool]string{true: "yes", false: "no"}[answer] + ":" + version + ":" + model
	}
	want := map[string]string{
		"scheduling_tool_called": "code:no:" + ScorecardCodeVersion + ":",
		"booking_blocked":        "code:no:" + ScorecardCodeVersion + ":",
		"no_results_retried":     "code:no:" + ScorecardCodeVersion + ":",
		"booking_requested":      "judge:no:" + ScorecardJudgeVersion + ":typesafe-ai/jev",
	}
	if len(got) != len(want) {
		t.Fatalf("scorecard rows: %v", got)
	}
	for question, value := range want {
		if got[question] != value {
			t.Fatalf("%s: got %q want %q", question, got[question], value)
		}
	}
}

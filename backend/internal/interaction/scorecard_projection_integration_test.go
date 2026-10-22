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
	db := testdb.Open(t)
	pool := db.Owner
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
		CloseoutPayload: json.RawMessage(`{"versions":{"agent":"0.14.0"},"domainOutcomes":[],"evaluation":{"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v7","jurors":["typesafe-ai/jev"],"status":"complete","results":{"new_booking_requested":{"verdict":false,"probability":0.2,"votes":{"typesafe-ai/jev":0.2},"errors":{}}}}}`),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(raw)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_receipts(service_subject,practice_id,location_id,source_call_id,kind,payload_fingerprint,payload) VALUES('agent',$1,$2,'scorecard-call',$3,$4,$5)`, practiceID, locationID, payload.Kind, fingerprint[:], raw); err != nil {
		t.Fatal(err)
	}
	module := New(db.As(testdb.Worker), access.New(db.As(testdb.Worker), func() time.Time { return now }), func() time.Time { return now })
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
		"availability_searched":  "code:no:" + ScorecardCodeVersion + ":",
		"scheduling_tool_called": "code:no:" + ScorecardCodeVersion + ":",
		"booking_blocked":        "code:no:" + ScorecardCodeVersion + ":",
		"no_results_retried":     "code:no:" + ScorecardCodeVersion + ":",
		"new_booking_requested":  "judge:no:" + legacyJudgeVersion + ":typesafe-ai/jev",
	}
	if len(got) != len(want) {
		t.Fatalf("scorecard rows: %v", got)
	}
	for question, value := range want {
		if got[question] != value {
			t.Fatalf("%s: got %q want %q", question, got[question], value)
		}
	}
	rows.Close()
	payload.SourceCallID = "scorecard-v8-call"
	payload.CloseoutPayload = json.RawMessage(`{"evaluation":{"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v8","jurors":["a","b","c"],"status":"complete","results":{
 "knowledge_available":{"status":"unknown","reason":"no_office_knowledge_result"},
 "appointment_datetime_correct":{"status":"not_applicable","reason":"no_appointment_action"},
 "office_question_answered":{"verdict":true,"probability":0.9,"votes":{"a":0.9,"b":0.9,"c":0.9}},
 "call_ended_correctly":{"verdict":true,"probability":0.9,"votes":{"a":0.9,"b":0.9,"c":0.9}},
 "transfer_policy_followed":{"verdict":false,"probability":0.2,"votes":{"a":0.2,"b":0.2,"c":0.2}},
 "emergency_policy_followed":{"verdict":true,"probability":0.9,"votes":{"a":0.9,"b":0.9,"c":0.9}}
 }}}`)
	raw, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint = sha256.Sum256(raw)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_receipts(service_subject,practice_id,location_id,source_call_id,kind,payload_fingerprint,payload) VALUES('agent',$1,$2,'scorecard-v8-call',$3,$4,$5)`, practiceID, locationID, payload.Kind, fingerprint[:], raw); err != nil {
		t.Fatal(err)
	}
	if worked, err := module.ProcessNextReceipt(ctx); err != nil || !worked {
		t.Fatalf("project v8: worked=%v err=%v", worked, err)
	}
	var answers map[string]bool
	if err := pool.QueryRow(ctx, `SELECT jsonb_object_agg(a.question,a.answer) FROM ai_interaction_scorecard_answers a JOIN ai_interactions i ON i.id=a.interaction_id WHERE i.source_call_id='scorecard-v8-call' AND a.source='judge' AND a.scorecard_version=$1`, ScorecardJudgeVersion).Scan(&answers); err != nil {
		t.Fatal(err)
	}
	if len(answers) != 4 || answers["transfer_policy_followed"] || !failedReviewQuestion(answers) {
		t.Fatalf("v8 transfer failure must be projected and flagged: %+v", answers)
	}
	if _, found := answers["knowledge_available"]; found {
		t.Fatal("unknown knowledge must not be stored as a yes/no verdict")
	}
	if _, found := answers["appointment_datetime_correct"]; found {
		t.Fatal("inapplicable appointment must not be stored as a yes/no verdict")
	}

}

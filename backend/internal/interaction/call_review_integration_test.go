package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestScorecardReviewQueueBlindReviewAndAccuracy(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	accessModule := access.New(pool, clock)
	first := access.Identity{Subject: "review-first", Email: "first@review.test", EmailVerified: true}
	second := access.Identity{Subject: "review-second", Email: "second@review.test", EmailVerified: true}
	staff := access.Identity{Subject: "review-staff", Email: "staff@review.test", EmailVerified: true}
	if _, err := accessModule.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", PlatformOperators: []string{first.Email, second.Email}, Practices: []access.PracticeProvision{{Key: "review", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "main", Name: "Main"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: staff.Email, Role: access.RoleAdmin, LocationScope: access.LocationScopeAll}}}}}); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []access.Identity{first, second, staff} {
		testaccess.Activate(t, accessModule, identity)
	}
	var practice, location string
	if err := pool.QueryRow(ctx, `SELECT p.id::text, l.id::text FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='review'`).Scan(&practice, &location); err != nil {
		t.Fatal(err)
	}
	evaluation := func(noul map[string]float64) map[string]any {
		results := map[string]any{}
		for name, value := range noul {
			results[name] = map[string]any{"verdict": value > scorecardNoAtOrBelow, "probability": value, "votes": map[string]any{"typesafe-ai/jev": value}, "errors": map[string]any{}}
		}
		return map[string]any{"evaluator": "jury", "evaluatorVersion": ScorecardJudgeVersion, "jurors": []string{"typesafe-ai/jev"}, "status": "complete", "results": results}
	}
	insert := func(index int, seconds int, tools []syntheticTool, judged map[string]float64) string {
		t.Helper()
		closeout, _ := json.Marshal(map[string]any{"versions": map[string]any{"agent": "0.13.1"}, "domainOutcomes": []any{}, "evaluation": evaluation(judged)})
		started := time.Date(2026, 10, 6, 14, 0, index, 0, time.UTC)
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions (service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload) VALUES ('agent',$1,$2,$3,'+15555550111','+15555550100',$4,$4::timestamptz + make_interval(secs => $5),'COMPLETED',3,$6,$7) RETURNING id::text`,
			practice, location, fmt.Sprintf("SCL_synthetic_%02d", index), started, seconds, syntheticTranscript(t, tools...), closeout).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	healthy := map[string]float64{"new_booking_requested": 0.1, "promises_backed": 0.9, "ended_while_caller_active": 0.9, "office_question_answered": 0.9, "office_rules_grounded": 0.9}
	ids := []string{}
	for index := 0; index < 30; index++ {
		judged := healthy
		if index%5 == 0 {
			judged = map[string]float64{"new_booking_requested": 0.1, "promises_backed": 0.9, "ended_while_caller_active": 0.9, "office_question_answered": 0.2, "office_rules_grounded": 0.9}
		}
		ids = append(ids, insert(index, 60, nil, judged))
	}
	insert(40, 10, nil, healthy)
	booked := insert(41, 90, []syntheticTool{
		{"check_insurance", `{"plan":"Synthetic Gold Plan"}`, "blocked: The office needs to verify this plan's coverage before scheduling."},
		{"list_available_appointments", `{}`, "success: Openings found.\nslot-1 – Monday"},
	}, map[string]float64{"new_booking_requested": 0.95, "time_offered": 0.2, "promises_backed": 0.9, "ended_while_caller_active": 0.3, "office_question_answered": 0.9, "office_rules_grounded": 0.9})

	module := New(pool, accessModule, clock)
	backfill, err := module.BackfillScorecards(ctx, time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC), 7)
	if err != nil || backfill.Calls != 32 {
		t.Fatalf("backfill: %+v %v", backfill, err)
	}
	var agentVersion string
	var probability float64
	if err := pool.QueryRow(ctx, `SELECT agent_version, probability FROM ai_interaction_scorecard_answers WHERE interaction_id=$1 AND question='new_booking_requested'`, booked).Scan(&agentVersion, &probability); err != nil || agentVersion != "0.13.1" || probability != 0.95 {
		t.Fatalf("stored judge answer: %q %v %v", agentVersion, probability, err)
	}

	command := ReviewQueueCommand{Identity: first, PracticeID: practice, Date: "2026-10-06", TimeZone: "America/New_York"}
	if _, err := module.OpenReviewQueue(ctx, ReviewQueueCommand{Identity: staff, PracticeID: practice, Date: command.Date, TimeZone: command.TimeZone}); !errors.Is(err, ErrDenied) {
		t.Fatalf("practice admins cannot open the operator review queue: %v", err)
	}
	if _, err := module.OpenReviewQueue(ctx, ReviewQueueCommand{Identity: first, PracticeID: practice, Date: "2026-10-07", TimeZone: command.TimeZone}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("today's queue is not reviewable until the day is over: %v", err)
	}
	queueA, err := module.OpenReviewQueue(ctx, command)
	if err != nil || len(queueA.Calls) != reviewQueueSize || queueA.Calls[0].LocationName != "Main" {
		t.Fatalf("first queue: %d %v", len(queueA.Calls), err)
	}
	var poolFlagged, poolRandom int
	if err := pool.QueryRow(ctx, `SELECT flagged_calls, random_calls FROM ai_call_review_pools WHERE practice_id=$1 AND review_date='2026-10-06'`, practice).Scan(&poolFlagged, &poolRandom); err != nil || poolFlagged != 7 || poolRandom != 24 {
		t.Fatalf("the day's pool is recorded once for weighting: flagged=%d random=%d %v", poolFlagged, poolRandom, err)
	}
	again, err := module.OpenReviewQueue(ctx, command)
	if err != nil || fmt.Sprint(again.Calls) != fmt.Sprint(queueA.Calls) {
		t.Fatalf("reopening must keep the same queue: %v", err)
	}
	flagged, overlap := 0, map[string]bool{}
	for _, call := range queueA.Calls {
		if call.Sample == "flagged" {
			flagged++
		}
		if call.Overlap {
			overlap[call.InteractionID] = true
		}
	}
	if flagged != 7 || len(overlap) != reviewQueueOverlap {
		t.Fatalf("all 7 flagged calls (6 low A3 + 1 low E1) and %d overlap calls: flagged=%d overlap=%d", reviewQueueOverlap, flagged, len(overlap))
	}
	command.Identity = second
	queueB, err := module.OpenReviewQueue(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	shared := 0
	firstCalls := map[string]bool{}
	for _, call := range queueA.Calls {
		firstCalls[call.InteractionID] = true
	}
	for _, call := range queueB.Calls {
		if firstCalls[call.InteractionID] {
			shared++
			if !overlap[call.InteractionID] {
				t.Fatalf("only overlap calls are shared: %s", call.InteractionID)
			}
		}
	}
	if shared != reviewQueueOverlap || len(queueB.Calls) != 31-reviewQueueSize+reviewQueueOverlap {
		t.Fatalf("second reviewer gets overlap plus unclaimed calls: shared=%d total=%d", shared, len(queueB.Calls))
	}

	target := queueA.Calls[0].InteractionID
	for _, call := range queueA.Calls {
		if call.InteractionID == booked {
			target = booked
		}
	}
	if target != booked {
		t.Fatal("the flagged booking call must be in the first queue")
	}
	blind, err := module.ReadCallReview(ctx, first, booked)
	if err != nil || blind.Submitted || len(blind.Judge) != 0 || !blind.Assigned {
		t.Fatalf("judge answers stay hidden before review: %+v %v", blind, err)
	}
	if fmt.Sprint(blind.Questions) != "[new_booking_requested time_offered requested_time_honored office_rules_grounded office_question_answered promises_backed ended_while_caller_active insurance_plan_as_stated]" {
		t.Fatalf("B5 and B6 apply after an availability search, I2 after an insurance check, A2 only after a scheduling write: %v", blind.Questions)
	}
	var insurance *ReviewFact
	for index := range blind.Facts {
		if blind.Facts[index].Question == "insurance_verified" {
			insurance = &blind.Facts[index]
		}
	}
	if insurance == nil || insurance.Answer || insurance.Detail["plan"] != "Synthetic Gold Plan" {
		t.Fatalf("code-check facts at the top: %+v", blind.Facts)
	}
	incomplete := CallReviewSubmission{Answers: []ReviewAnswer{{Question: "new_booking_requested", Answer: true}}}
	if _, err := module.SubmitCallReview(ctx, first, booked, incomplete); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("every applicable question needs an answer: %v", err)
	}
	answers := []ReviewAnswer{{Question: "new_booking_requested", Answer: true}, {Question: "time_offered", Answer: false}, {Question: "requested_time_honored", Answer: true}, {Question: "office_rules_grounded", Answer: true}, {Question: "office_question_answered", Answer: true}, {Question: "promises_backed", Answer: true}, {Question: "ended_while_caller_active", Answer: true, Note: "The caller said goodbye first."}, {Question: "insurance_plan_as_stated", Answer: true}}
	gatedOut := CallReviewSubmission{Answers: append(append([]ReviewAnswer{}, answers...), ReviewAnswer{Question: "appointment_datetime_correct", Answer: true})}
	if _, err := module.SubmitCallReview(ctx, first, booked, gatedOut); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("A2 is not answerable without a scheduling write: %v", err)
	}
	revealed, err := module.SubmitCallReview(ctx, first, booked, CallReviewSubmission{Answers: answers, Note: "Synthetic review", QuestionIdea: "Did the agent mention the insurance card?"})
	if err != nil || !revealed.Submitted || revealed.QuestionIdea != "Did the agent mention the insurance card?" || len(revealed.Answers) != 8 || len(revealed.Judge) != 6 {
		t.Fatalf("submitted review reveals judge answers: %+v %v", revealed, err)
	}
	if _, err := module.SubmitCallReview(ctx, first, booked, CallReviewSubmission{Answers: answers, Note: "Changed after reveal"}); !errors.Is(err, ErrReviewLocked) {
		t.Fatalf("answers are locked once the judge is revealed: %v", err)
	}
	if _, err := module.SubmitCallReview(ctx, second, booked, CallReviewSubmission{Answers: answers}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a reviewer submits only assigned calls: %v", err)
	}
	overlapCall := ""
	for id := range overlap {
		overlapCall = id
	}
	shortAnswers := func(clear bool) []ReviewAnswer {
		return []ReviewAnswer{{Question: "new_booking_requested", Answer: false}, {Question: "office_rules_grounded", Answer: true}, {Question: "office_question_answered", Answer: clear}, {Question: "promises_backed", Answer: true}, {Question: "ended_while_caller_active", Answer: true}}
	}
	if _, err := module.SubmitCallReview(ctx, first, overlapCall, CallReviewSubmission{Answers: shortAnswers(true)}); err != nil {
		t.Fatal(err)
	}
	unseen, err := module.QueryJudgeAccuracy(ctx, second, practice)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range unseen.GoldenSet {
		if call.InteractionID == overlapCall {
			t.Fatal("a shared call stays hidden from a reviewer who has not reviewed it yet")
		}
	}
	if _, err := module.SubmitCallReview(ctx, second, overlapCall, CallReviewSubmission{Answers: shortAnswers(false)}); err != nil {
		t.Fatal(err)
	}

	accuracy, err := module.QueryJudgeAccuracy(ctx, first, practice)
	if err != nil {
		t.Fatal(err)
	}
	falseAlarms := 0
	for _, item := range accuracy.JudgeDisagreements {
		if item.Question == "ended_while_caller_active" && item.Human && !item.Judge {
			falseAlarms++
		}
	}
	if accuracy.ReviewedCalls != 2 || falseAlarms != 1 || accuracy.GoldenAnswers != 18 || accuracy.CurrentJudgeVersion != ScorecardJudgeVersion {
		t.Fatalf("accuracy lists the false alarm: %+v", accuracy)
	}
	if len(accuracy.ReviewerDisagreements) != 1 || accuracy.ReviewerDisagreements[0].Question != "office_question_answered" {
		t.Fatalf("reviewer disagreements: %+v", accuracy.ReviewerDisagreements)
	}

	if accuracy.GoldenAnswers != 18 || len(accuracy.GoldenSet) != 2 || accuracy.GoldenSet[0].Answers+accuracy.GoldenSet[1].Answers != 18 {
		t.Fatalf("golden set summary: answers=%d calls=%+v", accuracy.GoldenAnswers, accuracy.GoldenSet)
	}

	insert(42, 60, nil, nil)
	insert(43, 60, nil, map[string]float64{"new_booking_requested": 0.95, "office_rules_grounded": 0.9})
	otherCaller := insert(44, 60, nil, map[string]float64{"new_booking_requested": 0.95, "office_rules_grounded": 0.9})
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET phone='+15555550122' WHERE id=$1`, otherCaller); err != nil {
		t.Fatal(err)
	}
	previous := insert(45, 60, nil, nil)
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET closeout_payload = jsonb_set(closeout_payload, '{evaluation}', $2::jsonb) WHERE id = $1`, previous,
		`{"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v6","jurors":["typesafe-ai/jev"],"status":"complete","results":{"booking_requested":{"verdict":true,"probability":0.9,"votes":{"typesafe-ai/jev":0.9},"errors":{}}}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers (interaction_id, practice_id, question, source, answer, probability, scorecard_version, judge_model, computed_at) VALUES ($1, $2, 'booking_requested', 'judge', true, 0.9, 'typesafe-scorecard-v6', 'typesafe-ai/jev', $3)`, previous, practice, now); err != nil {
		t.Fatal(err)
	}
	if _, err := module.BackfillScorecards(ctx, time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC), 7); err != nil {
		t.Fatal(err)
	}
	var keptVersion string
	if err := pool.QueryRow(ctx, `SELECT scorecard_version FROM ai_interaction_scorecard_answers WHERE interaction_id = $1 AND source = 'judge'`, previous).Scan(&keptVersion); err != nil || keptVersion != "typesafe-scorecard-v6" {
		t.Fatalf("a backfill keeps the judge rows of a call the previous jury scored: %q %v", keptVersion, err)
	}
	now = time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)
	results, err := module.QueryScorecardResults(ctx, ScorecardResultsCommand{Identity: first, PracticeID: practice, Range: AnalyticsRange7Days})
	if err != nil || results.Calls != 34 || results.ProblemCalls != 9 || results.BookingCallers != 2 || results.Missed != 1 || results.NotSearched != 1 || results.Conversion == nil || *results.Conversion != 0 {
		t.Fatalf("scorecard results: %+v %v", results, err)
	}
	if len(results.Daily) != 8 {
		t.Fatalf("one point per UTC day in range: %d", len(results.Daily))
	}
	for _, day := range results.Daily {
		if (day.Date == "2026-10-06") != (day.Calls == 34) || (day.Calls == 0 && day.ProblemRate != nil) {
			t.Fatalf("daily trend: %+v", results.Daily)
		}
	}
}

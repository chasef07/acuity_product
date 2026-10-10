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
	healthy := map[string]float64{"booking_requested": 0.1, "need_understood": 0.9, "right_help": 0.9, "clear_and_responsive": 0.9, "office_rules_grounded": 0.9}
	ids := []string{}
	for index := 0; index < 30; index++ {
		judged := healthy
		if index%5 == 0 {
			judged = map[string]float64{"booking_requested": 0.1, "need_understood": 0.9, "right_help": 0.9, "clear_and_responsive": 0.2, "office_rules_grounded": 0.9}
		}
		ids = append(ids, insert(index, 60, nil, judged))
	}
	insert(40, 10, nil, healthy)
	booked := insert(41, 90, []syntheticTool{
		{"check_insurance", `{"plan":"Synthetic Gold Plan"}`, "blocked: The office needs to verify this plan's coverage before scheduling."},
	}, map[string]float64{"booking_requested": 0.95, "need_understood": 0.9, "right_help": 0.3, "clear_and_responsive": 0.9, "office_rules_grounded": 0.9})

	module := New(pool, accessModule, clock)
	backfill, err := module.BackfillScorecards(ctx, time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC), 7)
	if err != nil || backfill.Calls != 32 {
		t.Fatalf("backfill: %+v %v", backfill, err)
	}
	var agentVersion string
	var probability float64
	if err := pool.QueryRow(ctx, `SELECT agent_version, probability FROM ai_interaction_scorecard_answers WHERE interaction_id=$1 AND question='booking_requested'`, booked).Scan(&agentVersion, &probability); err != nil || agentVersion != "0.13.1" || probability != 0.95 {
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
		t.Fatalf("all 7 flagged calls (6 low H4 + 1 low H2) and %d overlap calls: flagged=%d overlap=%d", reviewQueueOverlap, flagged, len(overlap))
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
	if fmt.Sprint(blind.Questions) != "[booking_requested time_offered need_understood right_help clear_and_responsive person_request_honored office_rules_grounded]" {
		t.Fatalf("A2 only applies after a scheduling write: %v", blind.Questions)
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
	incomplete := CallReviewSubmission{Answers: []ReviewAnswer{{Question: "booking_requested", Answer: true}}}
	if _, err := module.SubmitCallReview(ctx, first, booked, incomplete); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("every applicable question needs an answer: %v", err)
	}
	gatedOut := CallReviewSubmission{Answers: []ReviewAnswer{{Question: "booking_requested", Answer: false}, {Question: "time_offered", Answer: false}, {Question: "need_understood", Answer: true}, {Question: "right_help", Answer: true}, {Question: "clear_and_responsive", Answer: true}, {Question: "person_request_honored", Answer: true}, {Question: "office_rules_grounded", Answer: true}}}
	if _, err := module.SubmitCallReview(ctx, first, booked, gatedOut); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("B5 is not answerable when B1 is no: %v", err)
	}
	answers := []ReviewAnswer{{Question: "booking_requested", Answer: true}, {Question: "time_offered", Answer: false}, {Question: "need_understood", Answer: true}, {Question: "right_help", Answer: true, Note: "Staff verification was the right next step."}, {Question: "clear_and_responsive", Answer: true}, {Question: "person_request_honored", Answer: true}, {Question: "office_rules_grounded", Answer: true}}
	revealed, err := module.SubmitCallReview(ctx, first, booked, CallReviewSubmission{Answers: answers, Note: "Synthetic review", QuestionIdea: "Did the agent mention the insurance card?"})
	if err != nil || !revealed.Submitted || revealed.QuestionIdea != "Did the agent mention the insurance card?" || len(revealed.Answers) != 7 || len(revealed.Judge) != 5 {
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
		return []ReviewAnswer{{Question: "booking_requested", Answer: false}, {Question: "need_understood", Answer: true}, {Question: "right_help", Answer: true}, {Question: "clear_and_responsive", Answer: clear}, {Question: "person_request_honored", Answer: true}, {Question: "office_rules_grounded", Answer: true}}
	}
	for identity, clear := range map[access.Identity]bool{first: true, second: false} {
		if _, err := module.SubmitCallReview(ctx, identity, overlapCall, CallReviewSubmission{Answers: shortAnswers(clear)}); err != nil {
			t.Fatal(err)
		}
	}

	accuracy, err := module.QueryJudgeAccuracy(ctx, first, practice)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]JudgeAccuracyRow{}
	for _, row := range accuracy.Rows {
		rows[row.Question] = row
	}
	if accuracy.ReviewedCalls != 2 || rows["right_help"].Sample != 3 || rows["right_help"].FalseAlarms != 1 || accuracy.GoldenAnswers != 19 {
		t.Fatalf("accuracy counts false alarms: %+v", accuracy)
	}
	if len(accuracy.ReviewerDisagreements) != 1 || accuracy.ReviewerDisagreements[0].Question != "clear_and_responsive" {
		t.Fatalf("reviewer disagreements: %+v", accuracy.ReviewerDisagreements)
	}

	if accuracy.GoldenAnswers != 19 || len(accuracy.GoldenSet) != 2 || accuracy.GoldenSet[0].Answers+accuracy.GoldenSet[1].Answers != 19 {
		t.Fatalf("golden set summary: answers=%d calls=%+v", accuracy.GoldenAnswers, accuracy.GoldenSet)
	}

	now = time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)
	results, err := module.QueryScorecardResults(ctx, ScorecardResultsCommand{Identity: first, PracticeID: practice, Range: AnalyticsRange7Days})
	if err != nil || results.Calls != 32 || results.ProblemCalls != 7 || results.BookingCalls != 1 || results.Missed != 1 || results.Conversion == nil || *results.Conversion != 0 {
		t.Fatalf("scorecard results: %+v %v", results, err)
	}
	if len(results.Daily) != 8 {
		t.Fatalf("one point per UTC day in range: %d", len(results.Daily))
	}
	for _, day := range results.Daily {
		if (day.Date == "2026-10-06") != (day.Calls == 32) || (day.Calls == 0 && day.ProblemRate != nil) {
			t.Fatalf("daily trend: %+v", results.Daily)
		}
	}
}

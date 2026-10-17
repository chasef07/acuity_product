package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAgentCallsScopePaginationTranscriptAndIssuePersistence(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	accessModule := access.New(pool, func() time.Time { return now })
	staff := access.Identity{Subject: "agent-review-staff", Email: "staff@agent-review.test", EmailVerified: true}
	operator := access.Identity{Subject: "agent-review-operator", Email: "operator@agent-review.test", EmailVerified: true}
	_, err := accessModule.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", PlatformOperators: []string{operator.Email}, Practices: []access.PracticeProvision{{Key: "agent-review", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "allowed", Name: "Allowed"}, {Key: "denied", Name: "Denied"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"allowed"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, accessModule, staff)
	testaccess.Activate(t, accessModule, operator)
	var practice, allowed, denied string
	err = pool.QueryRow(ctx, `SELECT p.id::text, max(l.id::text) FILTER (WHERE l.provisioning_key='allowed'), max(l.id::text) FILTER (WHERE l.provisioning_key='denied') FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='agent-review' GROUP BY p.id`).Scan(&practice, &allowed, &denied)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(location, source string, start time.Time) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `INSERT INTO ai_interactions (service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload) VALUES ('agent',$1,$2,$3,'+15555550111','+15555550100',$4,$4::timestamptz + interval '30 seconds','COMPLETED',3,$5,$6) RETURNING id::text`, practice, location, source, start,
			`{"items":[{"type":"message","role":"system","content":["private system prompt"]},{"type":"message","role":"user","content":["Please book an appointment."]},{"type":"function_call","name":"book_appointment","arguments":"private payload"},{"type":"message","role":"assistant","content":["Your appointment is booked."]}]}`,
			`{"domainOutcomes":[{"outcome":"booked","status":"success"}]}`).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := insert(allowed, "first", now.Add(-time.Minute))
	second := insert(allowed, "second", now.Add(-2*time.Minute))
	forbidden := insert(denied, "forbidden", now.Add(-30*time.Second))
	module := New(pool, accessModule, func() time.Time { return now })
	command := QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: staff, PracticeID: practice, Range: AnalyticsRange7Days, Limit: 1}}
	page, err := module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != first || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if len(page.Calls[0].AppointmentActions) != 0 {
		t.Fatal("list must not present a bare agent claim as appointment success")
	}
	command.Cursor = page.NextCursor
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != second || page.NextCursor != "" {
		t.Fatalf("second page: %+v %v", page, err)
	}
	command.Phone = "9999"
	if _, err = module.QueryAgentCalls(ctx, command); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cursor must bind filters: %v", err)
	}
	command.Cursor = ""
	command.Phone = "(555) 555-0111"
	command.Limit = 50
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 2 {
		t.Fatalf("normalized phone query: %+v %v", page, err)
	}
	command.LocationID = denied
	if _, err = module.QueryAgentCalls(ctx, command); !errors.Is(err, ErrDenied) {
		t.Fatalf("denied location: %v", err)
	}
	if _, err = module.ReadAgentCall(ctx, staff, forbidden); !errors.Is(err, ErrDenied) {
		t.Fatalf("denied detail: %v", err)
	}
	if _, err = module.FlagAgentCallIssue(ctx, staff, forbidden, AgentCallIssueOther, ""); !errors.Is(err, ErrDenied) {
		t.Fatalf("denied flag: %v", err)
	}
	detail, err := module.ReadAgentCall(ctx, staff, first)
	if err != nil || len(detail.Messages) != 2 {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	if len(detail.Call.AppointmentActions) != 0 {
		t.Fatal("detail must not present a bare agent claim as appointment success")
	}
	raw, _ := json.Marshal(detail)
	if strings.Contains(string(raw), "private") {
		t.Fatal("staff projection leaked internal evidence")
	}
	if _, err = module.FlagAgentCallIssue(ctx, staff, first, "Incorrect office hours", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("free-text report: %v", err)
	}
	issue, err := module.FlagAgentCallIssue(ctx, staff, first, AgentCallIssueWrongAppointmentType, " Booked the wrong exam. ")
	if err != nil || issue.Reason != AgentCallIssueWrongAppointmentType || issue.Note != "Booked the wrong exam." {
		t.Fatalf("issue: %+v %v", issue, err)
	}
	retry, err := module.FlagAgentCallIssue(ctx, staff, first, AgentCallIssueWrongAppointmentType, issue.Note)
	if err != nil || retry != issue {
		t.Fatalf("retry must preserve report: %+v %v", retry, err)
	}
	if _, err = module.FlagAgentCallIssue(ctx, staff, first, AgentCallIssueInsurance, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("different reason must conflict: %v", err)
	}
	if _, err = module.FlagAgentCallIssue(ctx, operator, first, issue.Reason, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("different reporter must conflict: %v", err)
	}
	detail, err = module.ReadAgentCall(ctx, operator, first)
	if err != nil || detail.Issue == nil || *detail.Issue != issue || !detail.Call.IssueFlagged {
		t.Fatalf("operator review: %+v %v", detail, err)
	}
	command = QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: operator, PracticeID: practice, Range: AnalyticsRange7Days}, FlaggedOnly: true}
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != first {
		t.Fatalf("review destination: %+v %v", page, err)
	}

	old := insert(allowed, "old", now.Add(-10*24*time.Hour))
	if _, err = module.FlagAgentCallIssue(ctx, staff, old, AgentCallIssueInsurance, ""); err != nil {
		t.Fatal(err)
	}
	analytics := QueryAnalyticsCommand{Identity: operator, PracticeID: practice, Range: AnalyticsRange24Hours}
	diagnostics, err := module.QueryAnalytics(ctx, analytics)
	if err != nil || len(diagnostics.PendingIssues) != 2 || diagnostics.PendingIssues[0].InteractionID != old || diagnostics.PendingIssues[0].ReportedBy != staff.Email {
		t.Fatalf("pending issues: %+v %v", diagnostics.PendingIssues, err)
	}
	if _, err = module.ReviewCallIssue(ctx, staff, first, CallIssueConfirmed); !errors.Is(err, ErrDenied) {
		t.Fatalf("staff review: %v", err)
	}
	if _, err = module.ReviewCallIssue(ctx, operator, second, CallIssueConfirmed); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unflagged review: %v", err)
	}
	if _, err = module.ReviewCallIssue(ctx, operator, first, "MAYBE"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown outcome: %v", err)
	}
	for _, outcome := range []CallIssueOutcome{CallIssueConfirmed, CallIssueNotAnIssue, CallIssueNotAnIssue} {
		reviewed, err := module.ReviewCallIssue(ctx, operator, first, outcome)
		if err != nil || reviewed.Review == nil || reviewed.Review.Outcome != outcome || reviewed.Review.ReviewedBy != operator.Email || !reviewed.Review.ReviewedAt.Equal(now) {
			t.Fatalf("review %s: %+v %v", outcome, reviewed, err)
		}
	}
	var audits []string
	if err := pool.QueryRow(ctx, `SELECT array_agg(action ORDER BY action) FROM access_audit_events WHERE action LIKE 'ai_interaction.issue_%' AND details->>'resourceId'=$1`, first).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if strings.Join(audits, ",") != "ai_interaction.issue_confirmed,ai_interaction.issue_not_an_issue" {
		t.Fatalf("each changed outcome must be audited once: %v", audits)
	}
	evidence, err := module.ReadOperatorAnalytics(ctx, operator, first)
	if err != nil || evidence.Issue == nil || evidence.Issue.Review.Outcome != CallIssueNotAnIssue {
		t.Fatalf("operator evidence issue: %+v %v", evidence.Issue, err)
	}
	diagnostics, err = module.QueryAnalytics(ctx, analytics)
	if err != nil || len(diagnostics.PendingIssues) != 1 || diagnostics.PendingIssues[0].InteractionID != old {
		t.Fatalf("reviewed issue still pending: %+v %v", diagnostics.PendingIssues, err)
	}
	operatorFlagged := insert(allowed, "operator-flagged", now.Add(-3*time.Minute))
	if _, err = module.FlagAgentCallIssue(ctx, operator, operatorFlagged, AgentCallIssueOther, ""); err != nil {
		t.Fatal(err)
	}
	var reported []string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(array_agg(details->>'resourceId' ORDER BY details->>'resourceId'), '{}') FROM access_audit_events WHERE action='ai_interaction.issue_reported'`).Scan(&reported); err != nil {
		t.Fatal(err)
	}
	if strings.Join(reported, ",") != operatorFlagged {
		t.Fatalf("operator report must be audited once and staff reports never: %v", reported)
	}
}

func TestAgentCallsNeedingKnowledgeFollowTheJudge(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	accessModule := access.New(pool, func() time.Time { return now })
	staff := access.Identity{Subject: "knowledge-gap-staff", Email: "staff@knowledge-gap.test", EmailVerified: true}
	_, err := accessModule.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "knowledge-gap", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "office", Name: "Office"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeAll}}}}})
	if err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, accessModule, staff)
	var practice, location string
	if err := pool.QueryRow(ctx, `SELECT p.id::text, l.id::text FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='knowledge-gap'`).Scan(&practice, &location); err != nil {
		t.Fatal(err)
	}
	insert := func(source string, start time.Time) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions (service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload) VALUES ('agent',$1,$2,$3,'+15555550111','+15555550100',$4,$4::timestamptz + interval '30 seconds','COMPLETED',3,'{"items":[]}','{}') RETURNING id::text`, practice, location, source, start).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	score := func(id, question, source string, answer bool, detail any) {
		t.Helper()
		var model any
		if source == "judge" {
			model = "synthetic-judge"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers (interaction_id,practice_id,question,source,answer,scorecard_version,judge_model,detail,computed_at) VALUES ($1,$2,$3,$4,$5,'test',$6,$7,now())`, id, practice, question, source, answer, model, detail); err != nil {
			t.Fatal(err)
		}
	}
	unanswered := insert("unanswered", now.Add(-time.Minute))
	score(unanswered, "office_question_answered", "judge", false, nil)
	score(unanswered, "knowledge_answered", "code", false, map[string]any{"queries": []string{"Saturday hours", "parking"}})
	searchedButHandled := insert("handled", now.Add(-2*time.Minute))
	score(searchedButHandled, "office_question_answered", "judge", true, nil)
	score(searchedButHandled, "knowledge_answered", "code", false, map[string]any{"queries": []string{"fax number"}})
	insert("unjudged", now.Add(-3*time.Minute))

	module := New(pool, accessModule, func() time.Time { return now })
	command := QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: staff, PracticeID: practice, Range: AnalyticsRange7Days, Limit: 50}, NeedsKnowledgeOnly: true}
	page, err := module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != unanswered {
		t.Fatalf("needs knowledge: %+v %v", page, err)
	}
	if got := page.Calls[0].KnowledgeGaps; len(got) != 2 || got[0] != "Saturday hours" || got[1] != "parking" {
		t.Fatalf("knowledge gaps: %+v", got)
	}
	command.NeedsKnowledgeOnly = false
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 3 || len(page.Calls[1].KnowledgeGaps) != 1 || page.Calls[2].KnowledgeGaps == nil || len(page.Calls[2].KnowledgeGaps) != 0 {
		t.Fatalf("all calls keep their gaps: %+v %v", page, err)
	}
	v8Gap := insert("v8-gap", now.Add(-4*time.Minute))
	score(v8Gap, "knowledge_available", "judge", false, nil)
	v8AnswerError := insert("v8-answer-error", now.Add(-5*time.Minute))
	score(v8AnswerError, "knowledge_available", "judge", true, nil)
	score(v8AnswerError, "office_question_answered", "judge", false, nil)
	if _, err := pool.Exec(ctx, `UPDATE ai_interaction_scorecard_answers SET scorecard_version=$1 WHERE interaction_id=ANY($2::uuid[]) AND source='judge'`, ScorecardJudgeVersion, []string{v8Gap, v8AnswerError}); err != nil {
		t.Fatal(err)
	}
	command.NeedsKnowledgeOnly = true
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 2 || page.Calls[0].ID != unanswered || page.Calls[1].ID != v8Gap {
		t.Fatalf("historical knowledge-gap verdicts keep their meaning; v8 answer errors are not knowledge gaps: %+v %v", page, err)
	}

}

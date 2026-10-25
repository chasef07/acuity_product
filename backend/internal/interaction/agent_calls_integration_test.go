package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAgentCallsScopePaginationTranscriptAndIssuePersistence(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	staff := access.Identity{Subject: "agent-review-staff", Email: "staff@agent-review.test", EmailVerified: true}
	operator := access.Identity{Subject: "agent-review-operator", Email: "operator@agent-review.test", EmailVerified: true}
	_, err := access.New(pool, func() time.Time { return now }).Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", PlatformOperators: []string{operator.Email}, Practices: []access.PracticeProvision{{Key: "agent-review", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "allowed", Name: "Allowed"}, {Key: "denied", Name: "Denied"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"allowed"}}}}}})
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
	module := New(db.As(testdb.Portal), accessModule, func() time.Time { return now })
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
	for _, tc := range []struct {
		name, result, errors string
		want                 *float64
	}{
		{"v8 measured zero", `{"expressed_sentiment":{"score":0,"votes":{"a":0,"b":0,"c":0},"errors":{}}}`, `{}`, qualityFloat(0)},
		{"v8 measured mean", `{"expressed_sentiment":{"score":1.5,"votes":{"a":1,"b":2},"errors":{"c":"TimeoutError"}}}`, `{}`, qualityFloat(1.5)},
		{"historical v8 missing sentiment", `{}`, `{}`, nil},
		{"v8 invalid sentiment", `{"expressed_sentiment":{"score":"2"}}`, `{}`, nil},
		{"v8 no quorum", `{"expressed_sentiment":{"score":2}}`, `{"expressed_sentiment":{"cause":"no_quorum","votes":{"a":2},"errors":{"b":"TimeoutError","c":"TimeoutError"}}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evaluation := fmt.Sprintf(`{"evaluatorVersion":"typesafe-scorecard-v8","status":"incomplete","jurors":["a","b","c"],"results":%s,"errors":%s}`, tc.result, tc.errors)
			if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET closeout_payload = jsonb_set(closeout_payload, '{evaluation}', $2::jsonb) WHERE id = $1`, first, evaluation); err != nil {
				t.Fatal(err)
			}
			detail, err := module.ReadAgentCall(ctx, staff, first)
			if err != nil || !reflect.DeepEqual(detail.Sentiment, tc.want) {
				t.Fatalf("call sentiment=%v want=%v err=%v", detail.Sentiment, tc.want, err)
			}
			raw, err := json.Marshal(detail)
			if err != nil || strings.Contains(string(raw), `"sentiment":`) != (tc.want != nil) {
				t.Fatalf("only measured sentiment should appear in the response: %s err=%v", raw, err)
			}
		})
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

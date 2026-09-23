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
	if _, err = module.FlagAgentCallIssue(ctx, staff, forbidden, "Bad answer"); !errors.Is(err, ErrDenied) {
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
	if _, err = module.FlagAgentCallIssue(ctx, staff, first, "  "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank report: %v", err)
	}
	issue, err := module.FlagAgentCallIssue(ctx, staff, first, "Incorrect office hours")
	if err != nil || issue.Note != "Incorrect office hours" {
		t.Fatalf("issue: %+v %v", issue, err)
	}
	retry, err := module.FlagAgentCallIssue(ctx, staff, first, "Do not overwrite")
	if err != nil || retry != issue {
		t.Fatalf("retry must preserve report: %+v %v", retry, err)
	}
	detail, err = module.ReadAgentCall(ctx, operator, first)
	if err != nil || detail.Issue == nil || detail.Issue.Note != issue.Note || !detail.Call.IssueFlagged {
		t.Fatalf("operator review: %+v %v", detail, err)
	}
	command = QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: operator, PracticeID: practice, Range: AnalyticsRange7Days}, FlaggedOnly: true}
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != first {
		t.Fatalf("review destination: %+v %v", page, err)
	}
}

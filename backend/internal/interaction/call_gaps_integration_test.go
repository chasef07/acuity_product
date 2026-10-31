package interaction

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

type gapCase struct {
	name, question, otherQuestion, table, line string
	fixed, otherFixed                          GapResolution
	only                                       func(*QueryAgentCallsCommand, bool)
	gapLine                                    func(AgentCall) *string
	close                                      func(*Module, context.Context, access.Identity, string, GapResolution) (GapReview, error)
}

func TestGapCallsListOpenJudgedGapsAndCloseOnce(t *testing.T) {
	for _, gap := range []gapCase{
		{
			name: "knowledge", question: "knowledge_available", otherQuestion: "insurance_listed", table: "ai_interaction_knowledge_gap_reviews", line: "Are you open on Thanksgiving?", fixed: KnowledgeGapAnswered, otherFixed: InsuranceGapAdded,
			only:    func(command *QueryAgentCallsCommand, on bool) { command.KnowledgeGapOnly = on },
			gapLine: func(call AgentCall) *string { return call.KnowledgeGapLine },
			close:   (*Module).CloseKnowledgeGap,
		},
		{
			name: "insurance", question: "insurance_listed", otherQuestion: "knowledge_available", table: "ai_interaction_insurance_gap_reviews", line: "I have Tricare Prime.", fixed: InsuranceGapAdded, otherFixed: KnowledgeGapAnswered,
			only:    func(command *QueryAgentCallsCommand, on bool) { command.InsuranceGapOnly = on },
			gapLine: func(call AgentCall) *string { return call.InsuranceGapLine },
			close:   (*Module).CloseInsuranceGap,
		},
	} {
		t.Run(gap.name, func(t *testing.T) { testGapCalls(t, gap) })
	}
}

func testGapCalls(t *testing.T, gap gapCase) {
	question, line, fixed, only, gapLine, closeGap := gap.question, gap.line, gap.fixed, gap.only, gap.gapLine, gap.close
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	staff := access.Identity{Subject: "gap-staff", Email: "staff@gap.test", EmailVerified: true}
	other := access.Identity{Subject: "gap-other", Email: "other@gap.test", EmailVerified: true}
	_, err := access.New(pool, func() time.Time { return now }).Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "gap", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "allowed", Name: "Allowed"}, {Key: "denied", Name: "Denied"}}, AccessGrants: []access.AccessGrantProvision{
		{Key: "staff", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"allowed"}},
		{Key: "other", Email: other.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"allowed"}},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, accessModule, staff)
	testaccess.Activate(t, accessModule, other)
	var practice, allowed, denied string
	err = pool.QueryRow(ctx, `SELECT p.id::text, max(l.id::text) FILTER (WHERE l.provisioning_key='allowed'), max(l.id::text) FILTER (WHERE l.provisioning_key='denied') FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='gap' GROUP BY p.id`).Scan(&practice, &allowed, &denied)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(location, source string, start time.Time, listed *bool) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions (service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload) VALUES ('agent',$1,$2,$3,'+15555550111','+15555550100',$4,$4::timestamptz + interval '30 seconds','COMPLETED',3,'{"items":[]}','{}') RETURNING id::text`, practice, location, source, start).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if listed != nil {
			if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers (interaction_id,practice_id,question,source,answer,probability,scorecard_version,judge_model,computed_at) VALUES ($1,$2,$3,'judge',$4,0.2,'typesafe-scorecard-v12','a,b,c',$5)`, id, practice, question, *listed, now); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	missing, answered := false, true
	newest := insert(allowed, "newest-gap", now.Add(-time.Minute), &missing)
	older := insert(allowed, "older-gap", now.Add(-2*time.Minute), &missing)
	knew := insert(allowed, "knew", now.Add(-3*time.Minute), &answered)
	insert(allowed, "unjudged", now.Add(-4*time.Minute), nil)
	yesterday := insert(allowed, "yesterday-gap", now.Add(-25*time.Hour), &missing)
	forbidden := insert(denied, "forbidden-gap", now.Add(-time.Minute), &missing)
	otherGap := insert(allowed, "other-gap", now.Add(-90*time.Second), nil)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers (interaction_id,practice_id,question,source,answer,probability,scorecard_version,judge_model,computed_at) VALUES ($1,$2,$3,'judge',false,0.2,'typesafe-scorecard-v12','a,b,c',$4)`, otherGap, practice, gap.otherQuestion, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET closeout_payload = jsonb_build_object('evaluation', jsonb_build_object('results', jsonb_build_object($2::text, jsonb_build_object('verdict', false, 'line', $3::text)))) WHERE id = $1`, newest, question, line); err != nil {
		t.Fatal(err)
	}
	module := New(db.As(testdb.Portal), accessModule, func() time.Time { return now })

	command := QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: staff, PracticeID: practice, Range: AnalyticsRange24Hours, Limit: 1}}
	only(&command, true)
	page, err := module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != newest || page.Calls[0].LocationID != allowed || page.NextCursor == "" || gapLine(page.Calls[0]) == nil || *gapLine(page.Calls[0]) != line {
		t.Fatalf("first gap page: %+v %v", page, err)
	}
	command.Cursor = page.NextCursor
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != older || page.NextCursor != "" || gapLine(page.Calls[0]) != nil {
		t.Fatalf("second gap page: %+v %v", page, err)
	}
	only(&command, false)
	if _, err = module.QueryAgentCalls(ctx, command); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cursor must bind the gap filter: %v", err)
	}

	if _, err = closeGap(module, ctx, staff, forbidden, fixed); !errors.Is(err, ErrDenied) {
		t.Fatalf("other office: %v", err)
	}
	if _, err = closeGap(module, ctx, staff, knew, fixed); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("call the judge did not flag: %v", err)
	}
	for _, resolution := range []GapResolution{"MAYBE", gap.otherFixed} {
		if _, err = closeGap(module, ctx, staff, newest, resolution); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("resolution %s: %v", resolution, err)
		}
	}
	closed, err := closeGap(module, ctx, staff, newest, fixed)
	if err != nil || closed.Resolution != fixed || !closed.ReviewedAt.Equal(now) {
		t.Fatalf("close: %+v %v", closed, err)
	}
	retry, err := closeGap(module, ctx, other, newest, fixed)
	if err != nil || retry != closed {
		t.Fatalf("retry must return the saved close: %+v %v", retry, err)
	}
	if _, err = closeGap(module, ctx, staff, newest, GapNotNeeded); !errors.Is(err, ErrConflict) {
		t.Fatalf("different resolution must conflict: %v", err)
	}
	if _, err = closeGap(module, ctx, staff, older, GapNotNeeded); err != nil {
		t.Fatal(err)
	}
	if _, err = closeGap(module, ctx, staff, otherGap, fixed); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a call flagged only by the other judge: %v", err)
	}
	command = QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: staff, PracticeID: practice, LocationID: allowed, Range: AnalyticsRange7Days, Limit: 50}}
	only(&command, true)
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != yesterday {
		t.Fatalf("closed gaps must leave the list: %+v %v", page, err)
	}
	var reviewer string
	if err := pool.QueryRow(ctx, `SELECT reviewed_by FROM `+gap.table+` WHERE interaction_id=$1`, newest).Scan(&reviewer); err != nil || reviewer != staff.Subject {
		t.Fatalf("reviewer: %q %v", reviewer, err)
	}
}

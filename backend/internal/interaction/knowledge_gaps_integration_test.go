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

func TestKnowledgeGapCallsListOpenJudgedGapsAndCloseOnce(t *testing.T) {
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
	insert := func(location, source string, start time.Time, knowledgeAvailable *bool) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions (service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload) VALUES ('agent',$1,$2,$3,'+15555550111','+15555550100',$4,$4::timestamptz + interval '30 seconds','COMPLETED',3,'{"items":[]}','{}') RETURNING id::text`, practice, location, source, start).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if knowledgeAvailable != nil {
			if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers (interaction_id,practice_id,question,source,answer,probability,scorecard_version,judge_model,computed_at) VALUES ($1,$2,'knowledge_available','judge',$3,0.2,'typesafe-scorecard-v8','a,b,c',$4)`, id, practice, *knowledgeAvailable, now); err != nil {
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
	module := New(db.As(testdb.Portal), accessModule, func() time.Time { return now })

	command := QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: staff, PracticeID: practice, Range: AnalyticsRange24Hours, Limit: 1}, KnowledgeGapOnly: true}
	page, err := module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != newest || page.Calls[0].LocationID != allowed || page.NextCursor == "" {
		t.Fatalf("first gap page: %+v %v", page, err)
	}
	command.Cursor = page.NextCursor
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != older || page.NextCursor != "" {
		t.Fatalf("second gap page: %+v %v", page, err)
	}
	command.KnowledgeGapOnly = false
	if _, err = module.QueryAgentCalls(ctx, command); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cursor must bind the gap filter: %v", err)
	}

	if _, err = module.CloseKnowledgeGap(ctx, staff, forbidden, KnowledgeGapAnswered); !errors.Is(err, ErrDenied) {
		t.Fatalf("other office: %v", err)
	}
	if _, err = module.CloseKnowledgeGap(ctx, staff, knew, KnowledgeGapAnswered); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("call the judge did not flag: %v", err)
	}
	if _, err = module.CloseKnowledgeGap(ctx, staff, newest, "MAYBE"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown resolution: %v", err)
	}
	closed, err := module.CloseKnowledgeGap(ctx, staff, newest, KnowledgeGapAnswered)
	if err != nil || closed.Resolution != KnowledgeGapAnswered || !closed.ReviewedAt.Equal(now) {
		t.Fatalf("close: %+v %v", closed, err)
	}
	retry, err := module.CloseKnowledgeGap(ctx, other, newest, KnowledgeGapAnswered)
	if err != nil || retry != closed {
		t.Fatalf("retry must return the saved close: %+v %v", retry, err)
	}
	if _, err = module.CloseKnowledgeGap(ctx, staff, newest, KnowledgeGapNotNeeded); !errors.Is(err, ErrConflict) {
		t.Fatalf("different resolution must conflict: %v", err)
	}
	if _, err = module.CloseKnowledgeGap(ctx, staff, older, KnowledgeGapNotNeeded); err != nil {
		t.Fatal(err)
	}
	command = QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: staff, PracticeID: practice, LocationID: allowed, Range: AnalyticsRange7Days, Limit: 50}, KnowledgeGapOnly: true}
	page, err = module.QueryAgentCalls(ctx, command)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != yesterday {
		t.Fatalf("closed gaps must leave the list: %+v %v", page, err)
	}
	var reviewer string
	if err := pool.QueryRow(ctx, `SELECT reviewed_by FROM ai_interaction_knowledge_gap_reviews WHERE interaction_id=$1`, newest).Scan(&reviewer); err != nil || reviewer != staff.Subject {
		t.Fatalf("reviewer: %q %v", reviewer, err)
	}
}

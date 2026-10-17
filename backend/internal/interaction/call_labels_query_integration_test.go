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

func TestCallLabelsQueriesFilterBeforePaginationAndPreserveScope(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	am := access.New(pool, func() time.Time { return now })
	staff := access.Identity{Subject: "labels-staff", Email: "staff@labels.test", EmailVerified: true}
	operator := access.Identity{Subject: "labels-operator", Email: "operator@labels.test", EmailVerified: true}
	_, err := am.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", PlatformOperators: []string{operator.Email}, Practices: []access.PracticeProvision{{Key: "labels", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "allowed", Name: "Allowed"}, {Key: "denied", Name: "Denied"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"allowed"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, am, staff)
	testaccess.Activate(t, am, operator)
	var practice, allowed, denied string
	if err := pool.QueryRow(ctx, `SELECT p.id::text, max(l.id::text) FILTER(WHERE l.provisioning_key='allowed'), max(l.id::text) FILTER(WHERE l.provisioning_key='denied') FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='labels' GROUP BY p.id`).Scan(&practice, &allowed, &denied); err != nil {
		t.Fatal(err)
	}
	insert := func(location, source string, minutes int, intent, outcome string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage) VALUES('agent',$1,$2,$3,'+15555550111','+15555550100',$4,$5,'COMPLETED',3) RETURNING id::text`, practice, location, source, now.Add(-time.Duration(minutes)*time.Minute), now).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if intent != "" {
			if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_classifications(interaction_id,intent,outcome,status) VALUES($1,$2,$3,'complete')`, id, intent, outcome); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	legacy := insert(allowed, "legacy", 1, "", "")
	insert(allowed, "different-intent", 2, "insurance_question", "resolved")
	first := insert(allowed, "first-match", 3, "existing_patient_booking", "resolved")
	insert(allowed, "different-outcome", 4, "existing_patient_booking", "abandoned")
	second := insert(allowed, "second-match", 5, "existing_patient_booking", "resolved")
	forbidden := insert(denied, "forbidden", 1, "existing_patient_booking", "resolved")
	m := New(pool, am, func() time.Time { return now })
	cmd := QueryAnalyticsCommand{Identity: staff, PracticeID: practice, Range: AnalyticsRange7Days, Limit: 1, Intent: "existing_patient_booking", Outcome: "resolved"}
	page, err := m.QueryAgentCalls(ctx, QueryAgentCallsCommand{QueryAnalyticsCommand: cmd})
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != first || page.NextCursor == "" {
		t.Fatalf("filtered staff first page: %+v %v", page, err)
	}
	if page.Calls[0].Intent == nil || *page.Calls[0].Intent != "existing_patient_booking" || page.Calls[0].Outcome == nil || *page.Calls[0].Outcome != "resolved" || page.Calls[0].LabelingStatus == nil || *page.Calls[0].LabelingStatus != "complete" {
		t.Fatalf("public labels missing: %+v", page.Calls[0])
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers(interaction_id, practice_id, question, scorecard_version, source, judge_model, computed_at, answer, detail) VALUES
 ($1, $2, 'knowledge_available', 'typesafe-scorecard-v8', 'judge', 'synthetic', $3, false, '{}'),
 ($1, $2, 'knowledge_answered', 'typesafe-scorecard-v8', 'judge', 'synthetic', $3, false, '{"queries":["Saturday hours"]}')`, first, practice, now); err != nil {
		t.Fatal(err)
	}
	knowledgePage, err := m.QueryAgentCalls(ctx, QueryAgentCallsCommand{QueryAnalyticsCommand: cmd, NeedsKnowledgeOnly: true})
	if err != nil || len(knowledgePage.Calls) != 1 || knowledgePage.Calls[0].ID != first || knowledgePage.NextCursor != "" || len(knowledgePage.Calls[0].KnowledgeGaps) != 1 {
		t.Fatalf("knowledge and label filters/projection: %+v %v", knowledgePage, err)
	}
	if _, err := m.QueryAgentCalls(ctx, QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{
		Identity: staff, PracticeID: practice, Range: AnalyticsRange7Days, Limit: 1, Intent: cmd.Intent, Outcome: cmd.Outcome, Cursor: page.NextCursor,
	}, NeedsKnowledgeOnly: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("label cursor reused across knowledge filter: %v", err)
	}
	cmd.Cursor = page.NextCursor
	page, err = m.QueryAgentCalls(ctx, QueryAgentCallsCommand{QueryAnalyticsCommand: cmd})
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != second || page.NextCursor != "" {
		t.Fatalf("filtered staff continuation: %+v %v", page, err)
	}
	changed := cmd
	changed.Outcome = "abandoned"
	if _, err = m.QueryAgentCalls(ctx, QueryAgentCallsCommand{QueryAnalyticsCommand: changed}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("customer cursor reused across outcomes: %v", err)
	}
	changed = cmd
	changed.Intent = "insurance_question"
	if _, err = m.QueryAgentCalls(ctx, QueryAgentCallsCommand{QueryAnalyticsCommand: changed}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("customer cursor reused across intents: %v", err)
	}
	if _, err = m.ReadAgentCall(ctx, staff, forbidden); !errors.Is(err, ErrDenied) {
		t.Fatalf("staff detail scope: %v", err)
	}
	detail, err := m.ReadAgentCall(ctx, staff, first)
	if err != nil || detail.Call.Intent == nil || *detail.Call.Intent != "existing_patient_booking" {
		t.Fatalf("customer detail labels: %+v %v", detail, err)
	}
	raw, _ := json.Marshal(detail)
	if strings.Contains(string(raw), "probabilities") || strings.Contains(string(raw), "promptVersion") || strings.Contains(string(raw), "lastError") {
		t.Fatalf("private labeling evidence leaked: %s", raw)
	}
	old, err := m.ReadAgentCall(ctx, staff, legacy)
	if err != nil || old.Call.Intent != nil || old.Call.Outcome != nil || old.Call.LabelingStatus != nil {
		t.Fatalf("legacy labels fabricated: %+v %v", old, err)
	}
	cmd.Identity = operator
	cmd.Cursor = ""
	cmd.LocationID = allowed
	analytics, err := m.QueryAnalytics(ctx, cmd)
	if err != nil || len(analytics.Calls) != 1 || analytics.Calls[0].ID != first || analytics.NextCursor == "" {
		t.Fatalf("filtered operator first page: %+v %v", analytics, err)
	}
	if analytics.Summary == nil || analytics.Summary.TotalCalls != 5 {
		t.Fatalf("table filters changed full-scope summary: %+v", analytics.Summary)
	}
	if analytics.Calls[0].Intent == nil || *analytics.Calls[0].Intent != "existing_patient_booking" {
		t.Fatalf("operator table labels: %+v", analytics.Calls[0])
	}
	cmd.Cursor = analytics.NextCursor
	analytics, err = m.QueryAnalytics(ctx, cmd)
	if err != nil || len(analytics.Calls) != 1 || analytics.Calls[0].ID != second || analytics.NextCursor != "" {
		t.Fatalf("filtered operator continuation: %+v %v", analytics, err)
	}
	changed = cmd
	changed.Outcome = "abandoned"
	if _, err = m.QueryAnalytics(ctx, changed); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("operator cursor reused across outcomes: %v", err)
	}
	opDetail, err := m.ReadOperatorAnalytics(ctx, operator, first)
	if err != nil || opDetail.Intent == nil || *opDetail.Intent != "existing_patient_booking" || opDetail.Outcome == nil || *opDetail.Outcome != "resolved" {
		t.Fatalf("operator detail labels: %+v %v", opDetail, err)
	}
	insert(allowed, "matching-labels-without-knowledge-gap", 2, "existing_patient_booking", "resolved")
	for _, id := range []string{legacy, second} {
		if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers (interaction_id,practice_id,question,source,answer,scorecard_version,judge_model,computed_at) VALUES ($1,$2,'office_question_answered','judge',false,'test','synthetic-judge',now())`, id, practice); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers (interaction_id,practice_id,question,source,answer,scorecard_version,detail,computed_at) VALUES ($1,$2,'knowledge_answered','code',false,'test','{"queries":["Saturday hours"]}',now())`, id, practice); err != nil {
			t.Fatal(err)
		}
	}
	knowledge := QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: staff, PracticeID: practice, Range: AnalyticsRange7Days, Limit: 1, Intent: "existing_patient_booking", Outcome: "resolved"}, NeedsKnowledgeOnly: true}
	page, err = m.QueryAgentCalls(ctx, knowledge)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != first || page.NextCursor == "" || len(page.Calls[0].KnowledgeGaps) != 1 || page.Calls[0].KnowledgeGaps[0] != "Saturday hours" || page.Calls[0].Intent == nil {
		t.Fatalf("combined knowledge/labels first page: %+v %v", page, err)
	}
	knowledge.Cursor = page.NextCursor
	page, err = m.QueryAgentCalls(ctx, knowledge)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != second || page.NextCursor != "" {
		t.Fatalf("combined knowledge/labels continuation: %+v %v", page, err)
	}
	knowledge.NeedsKnowledgeOnly = false
	if _, err = m.QueryAgentCalls(ctx, knowledge); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("combined cursor reused across knowledge filter: %v", err)
	}
	knowledge.NeedsKnowledgeOnly = true
	knowledge.Outcome = "abandoned"
	if _, err = m.QueryAgentCalls(ctx, knowledge); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("combined cursor reused across outcome filter: %v", err)
	}
	for _, bad := range []QueryAnalyticsCommand{{Range: AnalyticsRange7Days, Intent: "bad"}, {Range: AnalyticsRange7Days, Outcome: "bad"}} {
		if _, err = m.QueryAnalytics(ctx, bad); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid operator filter: %v", err)
		}
		if _, err = m.QueryAgentCalls(ctx, QueryAgentCallsCommand{QueryAnalyticsCommand: bad}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid customer filter: %v", err)
		}
	}
}

func TestAnalyticsJudgeFailuresFilterBeforePagination(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	am := access.New(pool, func() time.Time { return now })
	operator := access.Identity{Subject: "failure-operator", Email: "operator@failures.test", EmailVerified: true}
	_, err := am.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", PlatformOperators: []string{operator.Email}, Practices: []access.PracticeProvision{{Key: "failures", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "allowed", Name: "Allowed"}, {Key: "other", Name: "Other"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, am, operator)
	var practice, allowed, other string
	if err := pool.QueryRow(ctx, `SELECT p.id::text, max(l.id::text) FILTER(WHERE l.provisioning_key='allowed'), max(l.id::text) FILTER(WHERE l.provisioning_key='other') FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='failures' GROUP BY p.id`).Scan(&practice, &allowed, &other); err != nil {
		t.Fatal(err)
	}
	insert := func(source, location string, minutes int, question, version, answerSource string, answer bool, intent, outcome string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage) VALUES('agent',$1,$2,$3,'+15555550111','+15555550100',$4,$5,'COMPLETED',3) RETURNING id::text`, practice, location, source, now.Add(-time.Duration(minutes)*time.Minute), now).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_classifications(interaction_id,intent,outcome,status) VALUES($1,$2,$3,'complete')`, id, intent, outcome); err != nil {
			t.Fatal(err)
		}
		if question != "" {
			if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers(interaction_id,practice_id,question,source,answer,scorecard_version,judge_model,computed_at) VALUES($1,$2,$3,$4,$5,$6,CASE WHEN $4 = 'judge' THEN 'synthetic' ELSE NULL END,now())`, id, practice, question, answerSource, answer, version); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	insert("unjudged", allowed, 1, "", "", "", false, "office_information", "resolved")
	insert("passing", allowed, 2, "office_question_answered", ScorecardJudgeVersion, "judge", true, "office_information", "resolved")
	insert("context-no", allowed, 3, "new_booking_requested", legacyJudgeVersion, "judge", false, "office_information", "resolved")
	insert("code-no", allowed, 4, "transfer_not_refused", ScorecardCodeVersion, "code", false, "office_information", "resolved")
	insert("removed-judge", allowed, 5, "office_rules_grounded", ScorecardJudgeVersion, "judge", false, "office_information", "resolved")
	insert("unknown-version", allowed, 6, "office_question_answered", "unknown-version", "judge", false, "office_information", "resolved")
	insert("other-intent", allowed, 7, "office_question_answered", ScorecardJudgeVersion, "judge", false, "insurance_question", "resolved")
	insert("other-outcome", allowed, 8, "office_question_answered", ScorecardJudgeVersion, "judge", false, "office_information", "abandoned")
	first := insert("first", allowed, 9, "office_question_answered", ScorecardJudgeVersion, "judge", false, "office_information", "resolved")
	second := insert("second", allowed, 10, "office_rules_grounded", legacyJudgeVersion, "judge", false, "office_information", "resolved")
	third := insert("third", allowed, 11, "office_question_answered", legacyJudgeVersion, "judge", false, "office_information", "resolved")
	insert("other-location", other, 1, "office_question_answered", ScorecardJudgeVersion, "judge", false, "office_information", "resolved")
	m := New(pool, am, func() time.Time { return now })
	cmd := QueryAnalyticsCommand{Identity: operator, PracticeID: practice, LocationID: allowed, Range: AnalyticsRange7Days, Limit: 1, Intent: "office_information", Outcome: "resolved", FailuresOnly: true}
	page, err := m.QueryAnalytics(ctx, cmd)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != first || page.NextCursor == "" {
		t.Fatalf("first failures page: %+v %v", page, err)
	}
	if page.Summary == nil || page.Summary.TotalCalls != 11 {
		t.Fatalf("failure filter changed full-scope summary: %+v", page.Summary)
	}
	cmd.Cursor = page.NextCursor
	page, err = m.QueryAnalytics(ctx, cmd)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != second || page.NextCursor == "" {
		t.Fatalf("second failures page: %+v %v", page, err)
	}
	changed := cmd
	changed.FailuresOnly = false
	if _, err := m.QueryAnalytics(ctx, changed); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cursor reused across failure scope: %v", err)
	}
	changed = cmd
	changed.FailingJudge = "office_question_answered"
	if _, err := m.QueryAnalytics(ctx, changed); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cursor reused across judges: %v", err)
	}
	cmd.Cursor = ""
	cmd.FailuresOnly = false
	cmd.FailingJudge = "office_question_answered"
	page, err = m.QueryAnalytics(ctx, cmd)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != first || page.NextCursor == "" {
		t.Fatalf("specific judge first page: %+v %v", page, err)
	}
	cmd.Cursor = page.NextCursor
	page, err = m.QueryAnalytics(ctx, cmd)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].ID != third || page.NextCursor != "" {
		t.Fatalf("specific judge continuation: %+v %v", page, err)
	}
	cmd.Cursor = ""
	cmd.FailingJudge = "new_booking_requested"
	if _, err := m.QueryAnalytics(ctx, cmd); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("non-failure judge accepted: %v", err)
	}
	cmd.FailingJudge = "invented"
	if _, err := m.QueryAnalytics(ctx, cmd); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown judge accepted: %v", err)
	}
}

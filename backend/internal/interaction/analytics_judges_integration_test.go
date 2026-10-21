package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAnalyticsJudgesCoverFullWindowAndIgnoreLabelFilters(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	var practice, otherPractice, location, otherLocation, foreignLocation string
	for i, target := range []*string{&practice, &otherPractice} {
		if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES($1,'Synthetic') RETURNING id::text`, fmt.Sprint("judges-", i)).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	for i, target := range []*string{&location, &otherLocation, &foreignLocation} {
		owner := practice
		if i == 2 {
			owner = otherPractice
		}
		if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,$2,'Synthetic') RETURNING id::text`, owner, fmt.Sprint(i)).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES('judges@example.test','judges')`); err != nil {
		t.Fatal(err)
	}
	raw := `{"evaluatorVersion":"typesafe-scorecard-v8","status":"complete","jurors":["a","b","c"],"results":{"office_question_answered":{"verdict":true,"probability":0.6333333333333333,"votes":{"a":0.4,"b":0.6,"c":0.9}}}}`
	insert := func(owner, office, source string, at time.Time, evaluation string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,closeout_payload) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',$4,$4,'COMPLETED',3,jsonb_build_object('evaluation',$5::jsonb))`, owner, office, source, at, evaluation); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 60; i++ {
		insert(practice, location, fmt.Sprint("call-", i), now.Add(-time.Hour), raw)
	}
	insert(practice, location, "old", now.Add(-time.Hour), `{"evaluatorVersion":"typesafe-scorecard-v7","status":"complete","jurors":["a"]}`)
	insert(practice, location, "missing", now.Add(-time.Hour), `null`)
	insert(practice, otherLocation, "other-location", now, raw)
	insert(otherPractice, foreignLocation, "other-practice", now, raw)
	insert(practice, location, "past", now.Add(-8*24*time.Hour), raw)
	insert(practice, location, "future", now.Add(time.Second), raw)
	module := New(db.As(testdb.Portal), access.New(db.As(testdb.Portal), func() time.Time { return now }), func() time.Time { return now })
	cmd := QueryAnalyticsCommand{Identity: access.Identity{Subject: "judges", Email: "judges@example.test", EmailVerified: true}, PracticeID: practice, LocationID: location, Range: AnalyticsRange7Days, Limit: 50}
	page, err := module.QueryAnalytics(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if page.Summary.TotalCalls != 62 || len(page.Calls) != 50 || page.NextCursor == "" {
		t.Fatal("incorrect full-window or page scope")
	}
	var summary struct {
		Judges *struct {
			Version                                     string
			TotalCalls, ReportedCalls, UnavailableCalls int
			Pairs                                       []struct {
				Left, Right                 string
				Compared, Agreed, Disagreed int
			}
		}
	}
	rawSummary, _ := json.Marshal(page.Summary)
	if err := json.Unmarshal(rawSummary, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Judges == nil || summary.Judges.Version != ScorecardJudgeVersion || summary.Judges.TotalCalls != 62 || summary.Judges.ReportedCalls != 60 || summary.Judges.UnavailableCalls != 2 {
		t.Fatalf("judge coverage: %+v", summary.Judges)
	}
	if len(summary.Judges.Pairs) != 3 || summary.Judges.Pairs[0].Compared != 60 || summary.Judges.Pairs[0].Disagreed != 60 || summary.Judges.Pairs[2].Agreed != 60 {
		t.Fatalf("pair coverage: %+v", summary.Judges.Pairs)
	}
	filtered := cmd
	filtered.Intent = "office_information"
	filtered.Outcome = "resolved"
	filtered.FailuresOnly = true
	filtered.FailingJudge = "office_question_answered"
	filteredPage, err := module.QueryAnalytics(ctx, filtered)
	if err != nil {
		t.Fatal(err)
	}
	filteredPage.Summary.Distributions = page.Summary.Distributions
	if !reflect.DeepEqual(filteredPage.Summary, page.Summary) {
		t.Fatal("label/failure filter changed global judge summary")
	}
	cmd.Cursor = page.NextCursor
	next, err := module.QueryAnalytics(ctx, cmd)
	if err != nil || next.Summary != nil || len(next.Calls) != 12 {
		t.Fatalf("continuation: %+v %v", next, err)
	}
}

package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAIInteractionVersionsProjectAndBackfill(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0079_agent_call_issue_reasons.sql")
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('version-backfill','Version Backfill') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,status,lifecycle_stage,closeout_payload) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',now(),'COMPLETED',3,$4)`
	historical := `{"agentVersion":"0.9.0","evaluation":{"evaluatorVersion":"typesafe-scorecard-v3","status":"complete"}}`
	if _, err := pool.Exec(ctx, insert, practiceID, locationID, "historical", []byte(historical)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, insert, practiceID, locationID, "skipped", []byte(`{"agentVersion":"0.9.0","evaluation":{"evaluatorVersion":"typesafe-scorecard-v4","status":"skipped"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ApplyThrough(ctx, pool, "0081_backfill_ai_interaction_versions.sql"); err != nil {
		t.Fatal(err)
	}
	read := func(source string) (agent, prompts, evaluator, knowledge *string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT version_agent, version_prompts, version_evaluator, version_knowledge FROM ai_interactions WHERE source_call_id=$1`, source).Scan(&agent, &prompts, &evaluator, &knowledge); err != nil {
			t.Fatal(err)
		}
		return
	}
	agent, prompts, evaluator, _ := read("historical")
	if agent == nil || *agent != "0.9.0" || prompts != nil || evaluator == nil || *evaluator != "typesafe-scorecard-v3" {
		t.Fatalf("historical agent=%v prompts=%v evaluator=%v", agent, prompts, evaluator)
	}
	if _, _, evaluator, _ := read("skipped"); evaluator != nil {
		t.Fatalf("skipped evaluation became an evaluator version: %v", *evaluator)
	}
	current := `{"agentVersion":"0.11.0","versions":{"agent":"0.11.0","prompts":"0.11.0","tools":"0.10.0","judges":"0.9.0","evaluator":"typesafe-scorecard-v4","knowledge":"revision-1"}}`
	if _, err := pool.Exec(ctx, insert, practiceID, locationID, "current", []byte(current)); err != nil {
		t.Fatal(err)
	}
	agent, prompts, _, knowledge := read("current")
	if *agent != "0.11.0" || *prompts != "0.11.0" || *knowledge != "revision-1" {
		t.Fatalf("current agent=%v prompts=%v knowledge=%v", *agent, *prompts, *knowledge)
	}
	// Correcting the source replaces derived versions; direct writes cannot drift.
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET closeout_payload='{}', version_prompts='forged' WHERE source_call_id='current'`); err != nil {
		t.Fatal(err)
	}
	if agent, prompts, _, knowledge := read("current"); agent != nil || prompts != nil || knowledge != nil {
		t.Fatalf("stale versions survived correction: %v %v %v", agent, prompts, knowledge)
	}
}

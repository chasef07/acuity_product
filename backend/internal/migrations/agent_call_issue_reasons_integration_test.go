package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAgentCallIssueReasonsKeepPreviousReleaseWorking(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0078_call_phone_lookup_indexes.sql")
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000201','issue-reasons','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000202','00000000-0000-0000-0000-000000000201','main','Synthetic');
 INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload)
 VALUES('00000000-0000-0000-0000-000000000203','fixture','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000202','issue-reasons','+15555550199','+15555550100',now()-interval '1 day',now(),'COMPLETED',3,'{}','{}'),
  ('00000000-0000-0000-0000-000000000204','fixture','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000202','issue-reasons-old','+15555550198','+15555550100',now()-interval '1 day',now(),'COMPLETED',3,'{}','{}');
 INSERT INTO ai_interaction_issues(interaction_id,reported_by,note) VALUES('00000000-0000-0000-0000-000000000203','synthetic-staff','Synthetic free-text report');
 `); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// The previous release still inserts free-text reports without a reason.
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_issues(interaction_id,reported_by,note) VALUES('00000000-0000-0000-0000-000000000204','synthetic-staff','Previous release report')`); err != nil {
		t.Fatalf("previous release report rejected: %v", err)
	}
	var others int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interaction_issues WHERE reason='OTHER'`).Scan(&others); err != nil {
		t.Fatal(err)
	}
	if others != 2 {
		t.Fatalf("free-text reports classified as OTHER = %d, want 2", others)
	}
}

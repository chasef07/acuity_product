package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAnalyticsEvidenceProjectsUnderRuntimeGrants(t *testing.T) {
	pool := testdb.Open(t)
	createDatabaseRoles(t, pool)
	applyDatabaseGrants(t, pool)
	ctx := context.Background()
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('analytics-grants','Analytics Grants') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"acuity_portal", "acuity_worker"} {
		t.Run(role, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
				t.Fatal(err)
			}
			var id string
			if err := tx.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,status,lifecycle_stage) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',now(),'IN_PROGRESS',1) RETURNING id::text`, practiceID, locationID, role).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `UPDATE ai_interactions SET transcript='{"items":[{"metrics":{"e2eLatencyMs":400}}],"usage":[{"type":"tts_usage","provider":"rime","model":"coda","characters_count":300}]}' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			var value int
			if err := tx.QueryRow(ctx, `SELECT (analytics_evidence #>> '{transcript,items,0,metrics,e2eLatencyMs}')::int FROM ai_interactions WHERE id=$1`, id).Scan(&value); err != nil || value != 400 {
				t.Fatalf("source update did not project readable analytics: value=%d err=%v", value, err)
			}
			if err := tx.QueryRow(ctx, `SELECT (cost_usage_evidence #>> '{0,characters_count}')::int FROM ai_interactions WHERE id=$1`, id).Scan(&value); err != nil || value != 300 {
				t.Fatalf("source update did not project readable cost usage: value=%d err=%v", value, err)
			}
			for _, column := range []string{"analytics_evidence", "cost_usage_evidence"} {
				var canAlter bool
				if err := tx.QueryRow(ctx, `SELECT has_column_privilege(current_user,'ai_interactions',$1,'UPDATE')`, column).Scan(&canAlter); err != nil {
					t.Fatal(err)
				}
				if canAlter {
					t.Fatalf("runtime can mutate derived %s independently of source", column)
				}
			}
		})
	}
}

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCostUsageBackfillResumesCommittedBatches(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0057_call_leg_reconciliation_schedule.sql")
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('cost-backfill','Cost Backfill') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,status,lifecycle_stage,transcript)
 SELECT ('00000000-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'agent',$1,$2,'source-'||n,'+15555550101','+15555550102',now(),'IN_PROGRESS',1,'{"usage":[{"type":"tts_usage","provider":"rime","model":"coda","characters_count":400}]}' FROM generate_series(1,205) AS n`, practiceID, locationID); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ApplyThrough(ctx, pool, "0058_compact_ai_cost_usage.sql"); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx, `SELECT id FROM ai_interactions WHERE id='00000000-0000-0000-0000-000000000101' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = migrations.ApplyThrough(ctx, pool, "0059_backfill_ai_cost_usage.sql")
	if err == nil {
		t.Fatal("backfill unexpectedly passed blocked second batch")
	}
	t.Logf("blocked migration exited in %s with %v", time.Since(start), err)
	var projected int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interactions WHERE cost_usage_evidence IS NOT NULL`).Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if projected != 100 {
		t.Fatalf("committed first batch did not survive interruption: %d", projected)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ApplyThrough(ctx, pool, "0059_backfill_ai_cost_usage.sql"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interactions WHERE cost_usage_evidence #>> '{0,characters_count}' = '400'`).Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if projected != 205 {
		t.Fatalf("resumed backfill lost evidence: %d", projected)
	}
	if err := migrations.ApplyThrough(ctx, pool, "0059_backfill_ai_cost_usage.sql"); err != nil {
		t.Fatalf("completed migration is not idempotent: %v", err)
	}
}

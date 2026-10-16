package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCallLabelsRuntimeGrantsKeepJudgeEvidencePrivate(t *testing.T) {
	pool := testdb.Open(t)
	createDatabaseRoles(t, pool)
	applyDatabaseGrants(t, pool)
	ctx := context.Background()
	var practice, location string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('label-grants','Synthetic Practice') RETURNING id::text`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practice).Scan(&location); err != nil {
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
			if err := tx.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,closeout_payload)
 VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',now(),now(),'COMPLETED',3,'{}') RETURNING id::text`, practice, location, role).Scan(&id); err != nil {
				t.Fatal(err)
			}
			var status string
			if err := tx.QueryRow(ctx, `SELECT status FROM ai_interaction_classifications WHERE interaction_id=$1`, id).Scan(&status); err != nil || status != "pending" {
				t.Fatalf("trigger queue=%s %v", status, err)
			}
			var canReadRaw, canWrite bool
			if err := tx.QueryRow(ctx, `SELECT has_column_privilege(current_user,'ai_interaction_classifications','raw_judgments','SELECT'),has_column_privilege(current_user,'ai_interaction_classifications','outcome','UPDATE')`).Scan(&canReadRaw, &canWrite); err != nil {
				t.Fatal(err)
			}
			if canReadRaw != (role == "acuity_worker") || canWrite != (role == "acuity_worker") {
				t.Fatalf("raw=%v write=%v", canReadRaw, canWrite)
			}
		})
	}
}

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCallLabelsMigrationRetiresQueueAndPreservesHistory(t *testing.T) {
	pool := testdb.OpenThrough(t, "0101_call_classification_queue_index.sql")
	ctx := context.Background()
	var practice, location string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('label-migration','Synthetic') RETURNING id::text`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practice).Scan(&location); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `GRANT SELECT,INSERT,UPDATE ON ai_interaction_classifications TO acuity_worker`); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, status := range []string{"complete", "pending", "processing"} {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,closeout_payload) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',now(),now(),'COMPLETED',3,'{}') RETURNING id::text`, practice, location, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE ai_interaction_classifications SET intent='office_information',outcome='resolved',raw_judgments='{"historical":true}',status=$2,claim_token=CASE WHEN $2='processing' THEN gen_random_uuid() END,lease_expires_at=CASE WHEN $2='processing' THEN now()+interval '1 minute' END WHERE interaction_id=$1`, id, status); err != nil {
			t.Fatal(err)
		}
		ids[status] = id
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for previous, id := range ids {
		var intent, outcome, status string
		var raw string
		var cleared bool
		if err := pool.QueryRow(ctx, `SELECT intent,outcome,status,raw_judgments::text,claim_token IS NULL AND lease_expires_at IS NULL FROM ai_interaction_classifications WHERE interaction_id=$1`, id).Scan(&intent, &outcome, &status, &raw, &cleared); err != nil {
			t.Fatal(err)
		}
		want := "skipped"
		if previous == "complete" {
			want = "complete"
		}
		if intent != "office_information" || outcome != "resolved" || status != want || raw != `{"historical": true}` || !cleared {
			t.Fatalf("history %s: %s %s %s %s %v", previous, intent, outcome, status, raw, cleared)
		}
	}
	var canReadRaw, canWrite bool
	if err := pool.QueryRow(ctx, `SELECT has_column_privilege('acuity_worker','ai_interaction_classifications','raw_judgments','SELECT'),has_column_privilege('acuity_worker','ai_interaction_classifications','outcome','UPDATE')`).Scan(&canReadRaw, &canWrite); err != nil || canReadRaw || canWrite {
		t.Fatalf("historical worker authority raw=%v write=%v %v", canReadRaw, canWrite, err)
	}
	var retired bool
	if err := pool.QueryRow(ctx, `SELECT to_regprocedure('enqueue_ai_call_classification()') IS NULL AND to_regprocedure('refresh_ai_call_classification_from_call()') IS NULL AND NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgname IN ('ai_call_classification_enqueue','ai_call_classification_call_changed','ai_call_classification_leg_changed'))`).Scan(&retired); err != nil || !retired {
		t.Fatalf("retired triggers=%v %v", retired, err)
	}
}

func TestCallLabelsProjectionValidatesSuppliedClassificationAndReplacesStaleLabels(t *testing.T) {
	pool := testdb.Open(t).Owner
	ctx := context.Background()
	var practice, location string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('label-projection','Synthetic') RETURNING id::text`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practice).Scan(&location); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,status,lifecycle_stage) VALUES('agent',$1,$2,'projection','+15555550101','+15555550102',now(),'IN_PROGRESS',1) RETURNING id::text`, practice, location).Scan(&id); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, classification, intent, outcome, status, reason string }{
		{"complete", `{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"complete","intent":"office_information","outcome":"resolved","answers":{"intent":"supplied"}}`, "office_information", "resolved", "complete", ""},
		{"incomplete", `{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"incomplete","intent":"billing_question","reason":"outcome_unavailable"}`, "billing_question", "", "failed", "outcome_unavailable"},
		{"partial outcome", `{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"incomplete","outcome":"transferred"}`, "", "transferred", "failed", "classification_incomplete"},
		{"invalid taxonomy", `{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"complete","intent":"invented","outcome":"abandoned"}`, "", "abandoned", "failed", "invalid_classification"},
		{"invalid outcome", `{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"complete","intent":"other","outcome":"invented"}`, "other", "", "failed", "invalid_classification"},
		{"invalid status", `{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"pending","intent":"other","outcome":"resolved"}`, "", "", "failed", "invalid_classification"},
		{"invalid version", `{"version":"unknown","model":"typesafe/jev-1.14","status":"complete","intent":"other","outcome":"resolved"}`, "", "", "failed", "invalid_classification"},
		{"skipped", `{"version":"call-labels-v1","model":"typesafe/jev-1.14","status":"skipped","reason":"empty_transcript"}`, "", "", "skipped", "empty_transcript"},
		{"scalar", `17`, "", "", "failed", "invalid_classification"},
		{"array", `[]`, "", "", "failed", "invalid_classification"},
		{"null", `null`, "", "", "failed", "invalid_classification"},
		{"missing", ``, "", "", "skipped", "classification_not_supplied"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			closeout := `{"evaluation":{}}`
			if c.classification != "" {
				closeout = `{"evaluation":{"classification":` + c.classification + `}}`
			}
			if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET lifecycle_stage=3,status='COMPLETED',ended_at=now(),closeout_payload=$2 WHERE id=$1`, id, []byte(closeout)); err != nil {
				t.Fatal(err)
			}
			var intent, outcome, status, reason string
			var updated time.Time
			if err := pool.QueryRow(ctx, `SELECT COALESCE(intent,''),COALESCE(outcome,''),status,COALESCE(error_code,''),updated_at FROM ai_interaction_classifications WHERE interaction_id=$1`, id).Scan(&intent, &outcome, &status, &reason, &updated); err != nil {
				t.Fatal(err)
			}
			if intent != c.intent || outcome != c.outcome || status != c.status || reason != c.reason {
				t.Fatalf("projection=%s %s %s %s", intent, outcome, status, reason)
			}
			if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET closeout_payload=$2,transcript='{"items":[]}' WHERE id=$1`, id, []byte(closeout)); err != nil {
				t.Fatal(err)
			}
			var repeated time.Time
			if err := pool.QueryRow(ctx, `SELECT updated_at FROM ai_interaction_classifications WHERE interaction_id=$1`, id).Scan(&repeated); err != nil || !repeated.Equal(updated) {
				t.Fatalf("duplicate changed projection: %s %s %v", updated, repeated, err)
			}
		})
	}
}

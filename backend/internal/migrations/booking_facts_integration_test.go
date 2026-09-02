package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestBookingFactsBackfillResumesAndTracksEvidenceChanges(t *testing.T) {
	pool := testdb.OpenThrough(t, "0048_task_analytics_index.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
  INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000101','booking-migration','Synthetic');
  INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','location','Synthetic');
  INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,appointment_outcome,new_appointment_id,booking_result,transcript,closeout_payload)
  SELECT ('00000000-0000-0000-0001-'||lpad(n::text,12,'0'))::uuid,'migration-fixture','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102',n::text,'+15555550199','+15555550100',now()-interval '1 day',now()-interval '23 hours','COMPLETED',3,'BOOKING','synthetic-'||n,'{"status":"booked"}',
   '{"items":[{"type":"function_call","name":"get_availability"}]}','{"domainOutcomes":[{"outcome":"patient_new","status":"success"}]}'
  FROM generate_series(1,1001)n;
 `); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ApplyThrough(ctx, pool, "0049_booking_analytics_facts.sql"); err != nil {
		t.Fatal(err)
	}
	// An overlapping application revision may already have projected some rows.
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET booking_confirmed=NULL WHERE source_call_id='1'`); err != nil {
		t.Fatal(err)
	}
	// Fail the second batch after the first 500 rows have committed. This tests
	// the actual migration's transaction boundaries, not just repeat application.
	if _, err := pool.Exec(ctx, `
  CREATE FUNCTION interrupt_booking_backfill() RETURNS trigger LANGUAGE plpgsql AS $$
  BEGIN
   IF NEW.source_call_id='1001' THEN RAISE EXCEPTION 'synthetic backfill interruption'; END IF;
   RETURN NEW;
  END;
  $$;
  CREATE TRIGGER interrupt_booking_backfill BEFORE UPDATE ON ai_interactions
   FOR EACH ROW EXECUTE FUNCTION interrupt_booking_backfill();
 `); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err == nil || !strings.Contains(err.Error(), "synthetic backfill interruption") {
		t.Fatalf("expected interrupted second batch: %v", err)
	}
	var complete int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interactions WHERE booking_confirmed IS NOT NULL`).Scan(&complete); err != nil || complete != 501 {
		t.Fatalf("first batch did not survive interruption: count %d, error %v", complete, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER interrupt_booking_backfill ON ai_interactions; DROP FUNCTION interrupt_booking_backfill();`); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interactions WHERE booking_confirmed AND booking_searched AND booking_search_known AND booking_patient_group='new'`).Scan(&complete); err != nil || complete != 1001 {
		t.Fatalf("backfill count %d: %v", complete, err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET appointment_outcome='INDETERMINATE',transcript='{"items":[]}',closeout_payload='{"domainOutcomes":[{"outcome":"patient_verified","status":"success"}]}' WHERE source_call_id='1'`); err != nil {
		t.Fatal(err)
	}
	var booked, searched, known bool
	var group string
	if err := pool.QueryRow(ctx, `SELECT booking_confirmed,booking_searched,booking_search_known,booking_patient_group FROM ai_interactions WHERE source_call_id='1'`).Scan(&booked, &searched, &known, &group); err != nil {
		t.Fatal(err)
	}
	if booked || searched || !known || group != "existing" {
		t.Fatalf("stale projection: %v %v %v %s", booked, searched, known, group)
	}
}

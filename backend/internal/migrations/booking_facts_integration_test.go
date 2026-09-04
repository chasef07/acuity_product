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

func TestInvocationBookingConversionMigrationRestoresEveryInvocation(t *testing.T) {
	pool := testdb.OpenThrough(t, "0053_cover_precise_booking_analytics.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name)
 VALUES('00000000-0000-0000-0000-000000000201','invocation-conversion','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name)
 VALUES('00000000-0000-0000-0000-000000000202','00000000-0000-0000-0000-000000000201','location','Synthetic');
 INSERT INTO ai_interactions(
   id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,
   started_at,ended_at,status,lifecycle_stage,appointment_outcome,transcript,closeout_payload)
 SELECT id,'migration-fixture','00000000-0000-0000-0000-000000000201',
   '00000000-0000-0000-0000-000000000202',source_call_id,'+15555550199',
   '+15555550100',now()-interval '1 day',now()-interval '23 hours','COMPLETED',3,
   appointment_outcome,'{"items":[{"type":"function_call","name":"get_availability"}]}',closeout_payload::jsonb
 FROM (VALUES
   ('00000000-0000-0000-0000-000000000203'::uuid,'booking','INDETERMINATE','{}'),
   ('00000000-0000-0000-0000-000000000204'::uuid,'reschedule','RESCHEDULE','{}'),
   ('00000000-0000-0000-0000-000000000205'::uuid,'cancellation','CANCELLATION','{}'),
   ('00000000-0000-0000-0000-000000000206'::uuid,'versioned-blocked','INDETERMINATE','{"bookingAnalyticsVersion":1,"domainOutcomes":[]}')
 ) AS calls(id,source_call_id,appointment_outcome,closeout_payload);
 INSERT INTO ai_interactions(
   id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,
   started_at,ended_at,status,lifecycle_stage,appointment_outcome,new_appointment_id,
   booking_result,transcript,closeout_payload)
 VALUES(
   '00000000-0000-0000-0000-000000000207','migration-fixture',
   '00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000202',
   'partial','+15555550199','+15555550100',now()-interval '1 day',
   now()-interval '23 hours','COMPLETED',3,'PARTIAL','synthetic-appointment',
   '{"status":"booked","appointmentId":"synthetic-appointment","appointmentTypeName":"New Adult Medical"}',
   '{"items":[{"type":"function_call","name":"get_availability"}]}','{}');
 INSERT INTO ai_interactions(
   id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,
   started_at,ended_at,status,lifecycle_stage,appointment_outcome,
   transcript,closeout_payload)
 VALUES(
   '00000000-0000-0000-0000-000000000208','migration-fixture',
   '00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000202',
   'versioned-success-without-invocation','+15555550199','+15555550100',
   now()-interval '1 day',now()-interval '23 hours','COMPLETED',3,'INDETERMINATE',
   '{"items":[]}',
   '{"bookingAnalyticsVersion":1,"domainOutcomes":[{"outcome":"availability_searched","status":"success","evidence":{"intent":"booking","patientGroup":"existing"}}]}');
 `); err != nil {
		t.Fatal(err)
	}
	var searched int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interactions WHERE booking_searched`).Scan(&searched); err != nil {
		t.Fatal(err)
	}
	if searched != 3 {
		t.Fatalf("precise-era searched calls = %d, want 3", searched)
	}
	var group string
	if err := pool.QueryRow(ctx, `SELECT booking_patient_group FROM ai_interactions WHERE source_call_id='partial'`).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if group != "new" {
		t.Fatalf("precise-era partial group = %q, want new", group)
	}
	var precise bool
	if err := pool.QueryRow(ctx, `SELECT booking_search_precise, booking_patient_group FROM ai_interactions WHERE source_call_id='versioned-success-without-invocation'`).Scan(&precise, &group); err != nil {
		t.Fatal(err)
	}
	if !precise || group != "existing" {
		t.Fatalf("precise-era versioned success = precise %v, group %q; want true, existing", precise, group)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interactions WHERE booking_searched`).Scan(&searched); err != nil {
		t.Fatal(err)
	}
	if searched != 5 {
		t.Fatalf("invocation-based searched calls = %d, want 5", searched)
	}
	var preciseCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interactions WHERE booking_search_precise`).Scan(&preciseCount); err != nil {
		t.Fatal(err)
	}
	if preciseCount != 0 {
		t.Fatalf("retired precise-search facts = %d, want 0", preciseCount)
	}
	if err := pool.QueryRow(ctx, `SELECT booking_patient_group FROM ai_interactions WHERE source_call_id='partial'`).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if group != "unknown" {
		t.Fatalf("restored partial group = %q, want unknown", group)
	}
	var invocation bool
	if err := pool.QueryRow(ctx, `SELECT booking_searched, booking_search_precise, booking_patient_group FROM ai_interactions WHERE source_call_id='versioned-success-without-invocation'`).Scan(&invocation, &precise, &group); err != nil {
		t.Fatal(err)
	}
	if invocation || precise || group != "unknown" {
		t.Fatalf("restored versioned success = searched %v, precise %v, group %q; want false, false, unknown", invocation, precise, group)
	}
}

package migrations_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestBookingPatientBasisUsesOutcomesAndPhoneEvidence(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000101','classification','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','location','Synthetic');
 INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,appointment_outcome,new_appointment_id,booking_result,closeout_payload)
 VALUES('00000000-0000-0000-0000-000000000103','fixture','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','classification','+15555550199','+15555550100',now()-interval '1 day',now()-interval '23 hours','COMPLETED',3,'BOOKING','synthetic-appointment','{"status":"booked"}','{"toolExecutions":[{"toolName":"get_availability","status":"success"}]}');
 `); err != nil {
		t.Fatal(err)
	}

	var insertedBasis string
	if err := pool.QueryRow(ctx, `SELECT booking_patient_basis FROM ai_interactions WHERE source_call_id='classification'`).Scan(&insertedBasis); err != nil {
		t.Fatal(err)
	}
	if insertedBasis != "assumed_new" {
		t.Fatalf("new insert missing telemetry: basis=%s want=assumed_new", insertedBasis)
	}

	for _, tc := range []struct{ name, payload, want string }{
		{"new closeout missing telemetry assumes new", `{"toolExecutions":[{"toolName":"get_availability","status":"success"}]}`, "assumed_new"},
		{"historical explicit no match stays new", `{"toolExecutions":[{"outputClass":"patient_not_found","status":"success"},{"toolName":"get_availability","status":"success"}]}`, "assumed_new"},
		{"native no match overrides historical search assumption", `{"phoneLookup":{"status":"no_match"},"toolExecutions":[{"toolName":"get_availability","status":"success"}]}`, "assumed_new"},
		{"native failed lookup follows current fallback", `{"phoneLookup":{"status":"lookup_failed"},"toolExecutions":[{"toolName":"get_availability","status":"success"}]}`, "assumed_new"},
		{"explicit creation overrides historical search assumption", `{"toolExecutions":[{"outputClass":"patient_created","status":"success"},{"toolName":"get_availability","status":"success"}]}`, "confirmed_new"},
		{"legacy verified output", `{"toolExecutions":[{"outputClass":"patient_verified","status":"success"}]}`, "confirmed_existing"},
		{"legacy transport success is not verification", `{"toolExecutions":[{"toolName":"existing_patient","outputClass":"patient_not_found","status":"success"}]}`, "assumed_new"},
		{"legacy failed verification", `{"toolExecutions":[{"outputClass":"patient_verified","status":"failed"}]}`, "assumed_new"},
		{"legacy switch overrides earlier creation", `{"toolExecutions":[{"outputClass":"patient_created","status":"success"},{"outputClass":"patient_switched","status":"success"}]}`, "confirmed_existing"},
		{"modern evidence is authoritative", `{"domainOutcomes":[],"toolExecutions":[{"outputClass":"patient_verified","status":"success"}]}`, "assumed_new"},
		{"missing assumes new", `{}`, "assumed_new"},
		{"single phone record", `{"phoneLookup":{"status":"verified"}}`, "phone_match"},
		{"multiple phone records", `{"phoneLookup":{"status":"multiple_matches"}}`, "phone_match"},
		{"failed phone lookup", `{"phoneLookup":{"status":"lookup_failed"}}`, "assumed_new"},
		{"no phone record", `{"phoneLookup":{"status":"no_match"}}`, "assumed_new"},
		{"tool failure is not verification", `{"domainOutcomes":[{"outcome":"patient_verified","status":"failed"}]}`, "assumed_new"},
		{"successful verification", `{"domainOutcomes":[{"outcome":"patient_verified","status":"success"}]}`, "confirmed_existing"},
		{"superseded verification", `{"domainOutcomes":[{"outcome":"patient_verified","status":"success","evidence":{"superseded":true}}]}`, "assumed_new"},
		{"creation before verification stays new", `{"domainOutcomes":[{"outcome":"patient_created","status":"success"},{"outcome":"patient_verified","status":"success"}]}`, "confirmed_new"},
		{"identity no-match overrides household", `{"phoneLookup":{"status":"verified"},"domainOutcomes":[{"outcome":"patient_not_found","status":"success"}]}`, "assumed_new"},
		{"successful switch confirms intended existing patient", `{"phoneLookup":{"status":"verified"},"domainOutcomes":[{"outcome":"patient_verified","status":"success"},{"outcome":"patient_switched","status":"success"}]}`, "confirmed_existing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET closeout_payload=$1::jsonb WHERE source_call_id='classification'`, tc.payload); err != nil {
				t.Fatal(err)
			}
			var basis string
			if err := pool.QueryRow(ctx, `SELECT booking_patient_basis FROM ai_interactions WHERE source_call_id='classification'`).Scan(&basis); err != nil {
				t.Fatal(err)
			}
			if basis != tc.want {
				t.Fatalf("basis=%s want=%s", basis, tc.want)
			}
		})
	}
}

func TestBookingPhoneLookupBackfillDryRunAndApply(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	const practice = "00000000-0000-0000-0000-000000000101"
	const call = "00000000-0000-0000-0000-000000000103"
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000101','backfill','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','location','Synthetic');
 INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,appointment_outcome,new_appointment_id,booking_result,closeout_payload)
 VALUES('00000000-0000-0000-0000-000000000103','fixture','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','backfill','+15555550199','+15555550100',now()-interval '1 day',now()-interval '23 hours','COMPLETED',3,'BOOKING','synthetic-appointment','{"status":"booked"}','{}');
 `); err != nil {
		t.Fatal(err)
	}
	before := ""
	if err := pool.QueryRow(ctx, `SELECT (to_jsonb(a)-'booking_phone_lookup_status'-'booking_patient_basis')::text FROM ai_interactions a WHERE id=$1`, call).Scan(&before); err != nil {
		t.Fatal(err)
	}
	run := func(apply string, csv string) error {
		t.Helper()
		evidence := filepath.Join(t.TempDir(), "synthetic-evidence.csv")
		if err := os.WriteFile(evidence, []byte(csv), 0600); err != nil {
			t.Fatal(err)
		}
		input, err := os.Open(evidence)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		cmd := exec.Command("psql", pool.Config().ConnConfig.ConnString(), "-X", "-v", "ON_ERROR_STOP=1", "-v", "practice_id="+practice, "-v", "apply="+apply, "-f", "../../../scripts/backfill-booking-phone-lookups.sql")
		cmd.Stdin = input
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("synthetic backfill result: %s", output)
		}
		return err
	}
	csv := "interaction_id,status\n" + call + ",verified\n"
	if err := run("false", csv); err != nil {
		t.Fatal(err)
	}
	var basis string
	if err := pool.QueryRow(ctx, `SELECT booking_patient_basis FROM ai_interactions WHERE id=$1`, call).Scan(&basis); err != nil {
		t.Fatal(err)
	}
	if basis != "assumed_new" {
		t.Fatalf("dry run persisted: %s", basis)
	}
	for i := 0; i < 2; i++ {
		if err := run("true", csv); err != nil {
			t.Fatal(err)
		}
	}
	var after string
	if err := pool.QueryRow(ctx, `SELECT booking_patient_basis,(to_jsonb(a)-'booking_phone_lookup_status'-'booking_patient_basis')::text FROM ai_interactions a WHERE id=$1`, call).Scan(&basis, &after); err != nil {
		t.Fatal(err)
	}
	if basis != "phone_match" || before != after {
		t.Fatal("backfill must change reporting assumption only")
	}
	if err := run("true", "interaction_id,status\n"+call+",multiple_matches\n"); err == nil {
		t.Fatal("conflicting assumption accepted")
	}
	if err := run("true", csv+"00000000-0000-0000-0000-000000000199,verified\n"); err == nil {
		t.Fatal("unmatched evidence accepted")
	}
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET closeout_payload='{"phoneLookup":{"status":"no_match"}}',booking_phone_lookup_status=NULL WHERE id=$1`, call); err != nil {
		t.Fatal(err)
	}
	if err := run("true", csv); err != nil {
		t.Fatal(err)
	}
	var historical *string
	if err := pool.QueryRow(ctx, `SELECT booking_phone_lookup_status,booking_patient_basis FROM ai_interactions WHERE id=$1`, call).Scan(&historical, &basis); err != nil {
		t.Fatal(err)
	}
	if historical != nil || basis != "assumed_new" {
		t.Fatal("backfill overwrote native lookup")
	}
}

func TestBookingPatientBasisMigrationPreservesExistingFacts(t *testing.T) {
	pool := testdb.OpenThrough(t, "0062_new_tampa_demo_key.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000101','upgrade','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','location','Synthetic');
 INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,appointment_outcome,new_appointment_id,booking_result,closeout_payload)
 VALUES('00000000-0000-0000-0000-000000000103','fixture','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','upgrade','+15555550199','+15555550100',now()-interval '1 day',now()-interval '23 hours','COMPLETED',3,'BOOKING','synthetic-appointment','{"status":"booked"}','{"toolExecutions":[{"toolName":"get_availability","status":"success"},{"outputClass":"patient_verified","status":"success"}]}');
 `); err != nil {
		t.Fatal(err)
	}
	var before, after, basis string
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(a)::text FROM ai_interactions a WHERE source_call_id='upgrade'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT (to_jsonb(a)-'booking_patient_basis'-'booking_phone_lookup_status'-'booking_historical_existing')::text,booking_patient_basis FROM ai_interactions a WHERE source_call_id='upgrade'`).Scan(&after, &basis); err != nil {
		t.Fatal(err)
	}
	if before != after || basis != "confirmed_existing" {
		t.Fatal("upgrade must classify legacy success without changing existing facts")
	}
}

func TestHistoricalPatientBasisUpgradeAndSourceCorrections(t *testing.T) {
	pool := testdb.OpenThrough(t, "0063_booking_patient_basis.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000101','historical','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','location','Synthetic');
 INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,appointment_outcome,new_appointment_id,booking_result,transcript,closeout_payload)
 VALUES('00000000-0000-0000-0000-000000000103','fixture','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','historical','+15555550199','+15555550100',now()-interval '1 day',now()-interval '23 hours','COMPLETED',3,'BOOKING','synthetic-appointment','{"status":"booked"}',
 '{"items":[{"type":"function_call","name":"get_availability","call_id":"search"},{"type":"function_call_output","call_id":"search","is_error":false}]}','{"domainOutcomes":[]}');
 `); err != nil {
		t.Fatal(err)
	}
	var before, after, basis string
	if err := pool.QueryRow(ctx, `SELECT (to_jsonb(a)-'booking_patient_basis'-'booking_historical_existing')::text,booking_patient_basis FROM ai_interactions a WHERE source_call_id='historical'`).Scan(&before, &basis); err != nil {
		t.Fatal(err)
	}
	if basis != "assumed_new" {
		t.Fatalf("pre-fix basis=%s want assumed_new", basis)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT (to_jsonb(a)-'booking_patient_basis'-'booking_historical_existing')::text,booking_patient_basis FROM ai_interactions a WHERE source_call_id='historical'`).Scan(&after, &basis); err != nil {
		t.Fatal(err)
	}
	if before != after || basis != "legacy_existing" {
		t.Fatalf("upgrade must preserve source and other facts; basis=%s", basis)
	}
	for _, tc := range []struct{ name, update, want string }{
		{"corrected transcript removes search", `transcript='{}'`, "assumed_new"},
		{"typed historical receipt establishes existing", `booking_result='{"status":"booked","appointmentId":"synthetic-appointment","appointmentTypeName":"Established Adult Vision"}'`, "legacy_existing"},
		{"appointment correction invalidates receipt", `new_appointment_id='different-appointment'`, "assumed_new"},
		{"matching receipt restored", `new_appointment_id='synthetic-appointment'`, "legacy_existing"},
		{"historical phone evidence replaces fallback", `booking_phone_lookup_status='verified'`, "phone_match"},
		{"native no match overrides historical fallback", `closeout_payload='{"domainOutcomes":[],"phoneLookup":{"status":"no_match"}}'`, "assumed_new"},
		{"explicit new identity overrides historical fallback", `closeout_payload='{"domainOutcomes":[{"outcome":"patient_new","status":"success"}]}'`, "confirmed_new"},
		{"explicit existing identity overrides historical fallback", `closeout_payload='{"domainOutcomes":[{"outcome":"patient_verified","status":"success"}]}'`, "confirmed_existing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, "UPDATE ai_interactions SET "+tc.update+" WHERE source_call_id='historical'"); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT booking_patient_basis FROM ai_interactions WHERE source_call_id='historical'`).Scan(&basis); err != nil {
				t.Fatal(err)
			}
			if basis != tc.want {
				t.Fatalf("basis=%s want=%s", basis, tc.want)
			}
		})
	}
}

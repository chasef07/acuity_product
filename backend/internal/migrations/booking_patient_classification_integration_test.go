package migrations_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBookingPatientClassificationUsesSuccessfulRegistration(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000101','classification','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','location','Synthetic');
 INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,appointment_outcome,new_appointment_id,booking_result,closeout_payload)
 VALUES('00000000-0000-0000-0000-000000000103','fixture','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','classification','+15555550199','+15555550100',now()-interval '1 day',now()-interval '23 hours','COMPLETED',3,'BOOKING','synthetic-appointment','{"status":"booked"}','{"domainOutcomes":[{"outcome":"patient_created","status":"success"}]}');
 `); err != nil {
		t.Fatal(err)
	}
	var group string
	if err := pool.QueryRow(ctx, "SELECT booking_patient_group FROM ai_interactions WHERE source_call_id='classification'").Scan(&group); err != nil {
		t.Fatal(err)
	}
	if group != "new" {
		t.Fatalf("successful new-patient registration classified as %q, want new", group)
	}
	// Receipt labels are semantic evidence; provider-specific numeric IDs alone
	// are not. Exercise every current catalog label through the source trigger.
	for _, tc := range []struct{ label, want string }{
		{"New Adult Medical", "new"},
		{"New Pediatric Medical", "new"},
		{"New Adult Vision", "new"},
		{"New Pediatric Vision", "new"},
		{"Crystal River New Patient", "new"},
		{"Established Adult Medical (Follow Up)", "existing"},
		{"Established Pediatric Medical (Follow Up)", "existing"},
		{"Established Adult Vision", "existing"},
		{"Established Pediatric Vision", "existing"},
		{"Crystal River Established Patient", "existing"},
		{"Post Op", "existing"},
		{"Crystal River Post Op", "existing"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			result, _ := json.Marshal(map[string]any{"status": "booked", "appointmentId": "synthetic-appointment", "appointmentTypeName": tc.label})
			assertBookingPatientGroup(t, pool, string(result), `{}`, tc.want)
		})
	}
	for _, tc := range []struct{ name, result, closeout, want string }{
		{"new receipt overrides call-wide switch", `{"status":"booked","appointmentId":"synthetic-appointment","appointmentTypeName":"New Adult Medical"}`, `{"domainOutcomes":[{"outcome":"patient_switched","status":"success"},{"outcome":"patient_verified","status":"success"}]}`, "new"},
		{"existing receipt overrides another person's creation", `{"status":"booked","appointmentId":"synthetic-appointment","appointmentTypeName":"Established Adult Vision"}`, `{"domainOutcomes":[{"outcome":"patient_created","status":"success"}]}`, "existing"},
		{"normalize explicit label", `{"status":"booked","appointmentId":"synthetic-appointment","appointmentTypeName":"  NEW ADULT VISION  "}`, `{}`, "new"},
		{"wrong appointment receipt", `{"status":"booked","appointmentId":"different-appointment","appointmentTypeName":"New Adult Medical"}`, `{}`, "unknown"},
		{"failed booking is not receipt evidence", `{"status":"error","appointmentId":"synthetic-appointment","appointmentTypeName":"New Adult Medical"}`, `{}`, "unknown"},
		{"provider type ID is not global", `{"status":"booked","appointmentId":"synthetic-appointment","appointmentTypeId":1006}`, `{}`, "unknown"},
		{"post-op remains existing after chart creation", `{"status":"booked","appointmentId":"synthetic-appointment","appointmentTypeName":"Post Op"}`, `{"domainOutcomes":[{"outcome":"patient_created","status":"success"}]}`, "existing"},
		{"unrecognized name is not inferred", `{"status":"booked","appointmentId":"synthetic-appointment","appointmentTypeName":"New York Consultation"}`, `{}`, "unknown"},
		{"successful creation fallback", `{"status":"booked"}`, `{"domainOutcomes":[{"outcome":"patient_created","status":"success"}]}`, "new"},
		{"verification after creation stays new", `{"status":"booked"}`, `{"domainOutcomes":[{"outcome":"patient_created","status":"success"},{"outcome":"patient_verified","status":"success"}]}`, "new"},
		{"superseded creation is ignored", `{"status":"booked"}`, `{"domainOutcomes":[{"outcome":"patient_created","status":"success","evidence":{"superseded":true}}]}`, "unknown"},
		{"failed creation is ignored", `{"status":"booked"}`, `{"domainOutcomes":[{"outcome":"patient_creation_failed","status":"failed"}]}`, "unknown"},
		{"explicit existing fallback", `{"status":"booked"}`, `{"domainOutcomes":[{"outcome":"patient_verified","status":"success"}]}`, "existing"},
		{"unbound identity switch remains ambiguous", `{"status":"booked"}`, `{"domainOutcomes":[{"outcome":"patient_created","status":"success"},{"outcome":"patient_switched","status":"success"}]}`, "unknown"},
		{"superseded switch does not hide identity", `{"status":"booked"}`, `{"domainOutcomes":[{"outcome":"patient_switched","status":"success","evidence":{"superseded":true}},{"outcome":"patient_verified","status":"success"}]}`, "existing"},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBookingPatientGroup(t, pool, tc.result, tc.closeout, tc.want) })
	}

}

func assertBookingPatientGroup(t *testing.T, pool *pgxpool.Pool, result, closeout, want string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET booking_result=$1::jsonb, closeout_payload=$2::jsonb WHERE source_call_id='classification'`, result, closeout); err != nil {
		t.Fatal(err)
	}
	var group string
	if err := pool.QueryRow(ctx, "SELECT booking_patient_group FROM ai_interactions WHERE source_call_id='classification'").Scan(&group); err != nil {
		t.Fatal(err)
	}
	if group != want {
		t.Fatalf("patient group %q, want %q", group, want)
	}
}

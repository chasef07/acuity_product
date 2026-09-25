package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestBookingConversionRecognizesCurrentAvailabilityTool(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0076_backfill_gpt_live_cost_usage.sql")
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000101','tool-rename','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','main','Synthetic');
 INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload)
 VALUES('fixture','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','renamed-search','+15555550199','+15555550100',now()-interval '1 day',now(),'COMPLETED',3,
 '{"items":[{"type":"function_call","name":"add_patient","call_id":"patient"},{"type":"function_call","name":"list_available_appointments","call_id":"search"},{"type":"function_call_output","call_id":"search","is_error":false,"output":"success: Found eligible openings."}]}','{"domainOutcomes":[]}');
 `); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var searched bool
	var group string
	if err := pool.QueryRow(ctx, `SELECT booking_searched, booking_patient_group FROM ai_interactions WHERE source_call_id='renamed-search'`).Scan(&searched, &group); err != nil {
		t.Fatal(err)
	}
	if !searched || group != "new" {
		t.Fatalf("renamed search excluded after migration: searched=%v group=%s", searched, group)
	}
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"success: Found eligible openings.", true},
		{"no_results: No eligible openings in the searched window.", true},
		{"blocked: Availability could not be verified.", false},
		{"needs_input: Verify the patient.", false},
	} {
		if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET transcript=jsonb_set(transcript,'{items,2,output}',to_jsonb($1::text)) WHERE source_call_id='renamed-search'`, tc.output); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT booking_searched FROM ai_interactions WHERE source_call_id='renamed-search'`).Scan(&searched); err != nil {
			t.Fatal(err)
		}
		if searched != tc.want {
			t.Errorf("output %q: searched=%v want %v", tc.output, searched, tc.want)
		}
	}
}

package interaction

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAnalyticsDailyIntentBookingsReadLabelsAndOutcomes(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, time.August, 10, 16, 0, 0, 0, time.UTC)
	var practice, location string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('intent-bookings','Synthetic') RETURNING id::text`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'office','Synthetic') RETURNING id::text`, practice).Scan(&location); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES('intents@example.test','intents')`); err != nil {
		t.Fatal(err)
	}
	for i, call := range []struct{ intent, outcome string }{
		{"new_patient_booking", "BOOKING"},
		{"new_patient_booking", "INDETERMINATE"},
		{"existing_patient_booking", "BOOKING"},
		{"existing_patient_booking", "RESCHEDULE"},
		{"office_information", "BOOKING"},
	} {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,appointment_outcome,lifecycle_stage) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',$4,$4,'COMPLETED',$5,3) RETURNING id::text`, practice, location, fmt.Sprint("intent-", i), now.Add(-time.Hour), call.outcome).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_classifications(interaction_id,intent,outcome,status) VALUES($1,$2,'resolved','complete') ON CONFLICT(interaction_id) DO UPDATE SET intent=EXCLUDED.intent,outcome=EXCLUDED.outcome,status=EXCLUDED.status`, id, call.intent); err != nil {
			t.Fatal(err)
		}
	}
	module := New(db.As(testdb.Portal), access.New(db.As(testdb.Portal), func() time.Time { return now }), func() time.Time { return now })
	page, err := module.QueryAnalytics(ctx, QueryAnalyticsCommand{Identity: access.Identity{Subject: "intents", Email: "intents@example.test", EmailVerified: true}, PracticeID: practice, Range: AnalyticsRange24Hours, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var day AnalyticsDay
	for _, candidate := range page.Summary.Daily {
		if candidate.Date == "2026-08-10" {
			day = candidate
		}
	}
	if day.NewPatientIntentCalls != 2 || day.NewPatientIntentBookings != 1 || day.ExistingPatientIntentCalls != 2 || day.ExistingPatientIntentBookings != 1 || day.BookingCount != 3 {
		t.Fatalf("intent bookings on Aug 10: %+v", page.Summary.Daily)
	}
}

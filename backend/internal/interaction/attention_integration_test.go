package interaction

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

func TestRecentAppointmentAttentionWindowAndBulkReview(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "recent-attention-test", Practices: []access.PracticeProvision{{Key: "recent-attention", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "main", Name: "Main"}, {Key: "other", Name: "Other"}}, AccessGrants: []access.AccessGrantProvision{
		{Key: "staff", Email: "staff@attention.test", Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
		{Key: "other", Email: "other@attention.test", Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
		{Key: "limited", Email: "limited@attention.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"main"}},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	identity := access.Identity{Subject: "staff", Email: "staff@attention.test", EmailVerified: true}
	second := access.Identity{Subject: "other", Email: "other@attention.test", EmailVerified: true}
	limited := access.Identity{Subject: "limited", Email: "limited@attention.test", EmailVerified: true}
	scope := testaccess.Activate(t, accessModule, identity)
	testaccess.Activate(t, accessModule, second)
	testaccess.Activate(t, accessModule, limited)
	var mainID, otherID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1 AND provisioning_key='main'`, scope.Practice.ID).Scan(&mainID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1 AND provisioning_key='other'`, scope.Practice.ID).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	module := New(pool, accessModule, func() time.Time { return now })
	seed := func(location string, at time.Time) string {
		t.Helper()
		id := uuid.NewString()
		_, err := pool.Exec(ctx, `INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,appointment_action,appointment_outcome,appointment_occurred_at,lifecycle_stage,booking_result) VALUES($1,'synthetic',$2,$3,$5,'+15550000001','+15550000002',$4,$4,'COMPLETED','BOOKED','BOOKING',$4,3,'{"appointmentDate":"2027-01-01"}')`, id, scope.Practice.ID, location, at, "synthetic-"+id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_attention(interaction_id,user_subject,outcome_occurred_at,created_at) VALUES($1,'staff',$2,$2),($1,'other',$2,$2),($1,'limited',$2,$2)`, id, at); err != nil {
			t.Fatal(err)
		}
		return id
	}
	expired := seed(mainID, now.Add(-7*24*time.Hour-time.Microsecond))
	boundary := seed(mainID, now.Add(-7*24*time.Hour))
	for i := 0; i < 55; i++ {
		seed(mainID, now.Add(-time.Duration(i+1)*time.Minute))
	}
	seed(otherID, now.Add(-time.Hour))
	query := QueryOutcomesCommand{Identity: identity, PracticeID: scope.Practice.ID, Limit: 10}
	page, err := module.QueryOutcomes(ctx, query)
	if err != nil || page.Counts == nil || page.Counts.Bookings != 57 || len(page.Items) != 10 || page.NextCursor == "" {
		t.Fatalf("initial page=%+v err=%v", page, err)
	}
	if err := module.ReviewRecentOutcomes(ctx, limited, scope.Practice.ID, otherID); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-Location bulk review=%v", err)
	}
	if err := module.ReviewOutcome(ctx, identity, page.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := module.ReviewRecentOutcomes(ctx, identity, scope.Practice.ID, mainID); err != nil {
		t.Fatal(err)
	}
	page, err = module.QueryOutcomes(ctx, query)
	if err != nil || page.Counts.Bookings != 1 {
		t.Fatalf("other Location page=%+v err=%v", page, err)
	}
	if err := module.ReviewRecentOutcomes(ctx, identity, scope.Practice.ID, ""); err != nil {
		t.Fatal(err)
	}
	page, err = module.QueryOutcomes(ctx, query)
	if err != nil || page.Counts.Bookings != 0 || len(page.Items) != 0 {
		t.Fatalf("bulk page=%+v err=%v", page, err)
	}
	query.Identity = second
	page, err = module.QueryOutcomes(ctx, query)
	if err != nil || page.Counts.Bookings != 57 {
		t.Fatalf("other User page=%+v err=%v", page, err)
	}
	var oldUnreviewed, boundaryReviewed bool
	if err := pool.QueryRow(ctx, `SELECT reviewed_at IS NULL FROM ai_interaction_attention WHERE interaction_id=$1 AND user_subject='staff'`, expired).Scan(&oldUnreviewed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT reviewed_at IS NOT NULL FROM ai_interaction_attention WHERE interaction_id=$1 AND user_subject='staff'`, boundary).Scan(&boundaryReviewed); err != nil {
		t.Fatal(err)
	}
	if !oldUnreviewed || !boundaryReviewed {
		t.Fatal("bulk changed history or excluded the seven-day boundary")
	}
	if _, err := module.Read(ctx, identity, expired); err != nil {
		t.Fatalf("old appointment history inaccessible: %v", err)
	}
	now = now.Add(8 * 24 * time.Hour)
	page, err = module.QueryOutcomes(ctx, query)
	if err != nil || page.Counts.Bookings != 0 {
		t.Fatal(fmt.Sprintf("expired page=%+v err=%v", page, err))
	}
}

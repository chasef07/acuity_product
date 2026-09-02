package workspace_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
)

func TestStaffAnalyticsScopeConnectedCallsAndAllAccounts(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := access.New(pool, nil)
	admin := access.Identity{Subject: "staff-report-admin", Email: "admin@staff.test", EmailVerified: true}
	staff := access.Identity{Subject: "staff-report-person", Email: "person@staff.test", EmailVerified: true}
	_, err := a.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "staff-analytics", Practices: []access.PracticeProvision{
		{Key: "staff-report", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "a", Name: "A"}, {Key: "b", Name: "B"}}, AccessGrants: []access.AccessGrantProvision{
			{Key: "admin", Email: admin.Email, Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
			{Key: "person", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
			{Key: "pending", Email: "pending@staff.test", Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
		}},
		{Key: "other", Name: "Other", Locations: []access.LocationProvision{{Key: "other", Name: "Other"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, a, admin)
	testaccess.Activate(t, a, staff)
	var practice, locA, locB, otherPractice, otherLocation string
	if err := pool.QueryRow(ctx, `SELECT p.id::text,a.id::text,b.id::text FROM access_practices p JOIN access_locations a ON a.practice_id=p.id AND a.provisioning_key='a' JOIN access_locations b ON b.practice_id=p.id AND b.provisioning_key='b' WHERE p.provisioning_key='staff-report'`).Scan(&practice, &locA, &locB); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT p.id::text,l.id::text FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='other'`).Scan(&otherPractice, &otherLocation); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day()-3, 12, 0, 0, 0, time.UTC)
	insertCall := func(location, direction string, durations []int, bridged bool) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO human_calling_calls(id,practice_id,location_id,direction,entry_point,terminal_outcome,created_at,ended_at) VALUES($1,$2,$3,$4,'STANDALONE','RESOLVED',$5,$6)`, id, practice, location, direction, day, day.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		for i, seconds := range durations {
			start := day.Add(time.Duration(i) * 10 * time.Minute)
			end := start.Add(time.Duration(seconds) * time.Second)
			var connected *time.Time
			if bridged {
				connected = &start
			}
			if _, err := pool.Exec(ctx, `INSERT INTO human_calling_call_legs(call_id,role,sequence,staff_subject,state,answered_at,bridge_pending_at,bridged_at,ended_at) VALUES($1,'STAFF',$2,$3,'ENDED',$4,$4,$4,$5)`, id, i+1, staff.Subject, connected, end); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	insertCall(locA, "INBOUND", []int{200, 100}, true)
	insertCall(locA, "OUTBOUND", []int{600}, true)
	insertCall(locA, "INBOUND", []int{50}, false)
	insertCall(locB, "INBOUND", []int{999}, true)
	for i, kind := range []string{"HUMAN", "SERVICE", ""} {
		id := uuid.NewString()
		state := "OPEN"
		var completed *time.Time
		var completedKind, subject, email *string
		if kind != "" {
			state = "COMPLETED"
			done := day.Add(24 * time.Hour)
			completed = &done
			completedKind = &kind
			subject = &staff.Subject
			if kind == "HUMAN" {
				email = &staff.Email
			}
		}
		if _, err := pool.Exec(ctx, `INSERT INTO work_tasks (id,practice_id,location_id,phone,title,state,created_by_kind,created_by_subject,created_at,updated_at,origin,source_call_id,source_message,category,ai_idempotency_key,ai_input_fingerprint,completed_by_kind,completed_by_subject,completed_by_email,completed_at) VALUES ($1::uuid,$2,$3,'+15555550199','Synthetic task',$4,'SERVICE','fixture',$5,$5,'ABITA_AI',$1::text,'Synthetic task evidence','other',$1::text,decode(repeat('00',32),'hex'),$6,$7,$8,$9)`, id, practice, locA, state, day.Add(-time.Duration(i)*time.Hour), completedKind, subject, email, completed); err != nil {
			t.Fatal(err)
		}
	}
	module := workspace.New(pool, a)
	command := workspace.QueryStaffAnalyticsCommand{Identity: admin, PracticeID: practice, LocationID: locA, Days: 7, TimeZone: "UTC"}
	report, err := module.QueryStaffAnalytics(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Accounts) != 3 || report.Total.InboundCalls != 1 || report.Total.OutboundCalls != 1 || report.Total.InboundSeconds != 300 || report.Total.OutboundSeconds != 600 || report.Total.TasksCompleted != 1 {
		t.Fatalf("staff aggregates: %+v", report)
	}
	if report.Tasks.Completed != 2 || report.Tasks.Eligible != 3 || report.Tasks.Within48Hours != 2 {
		t.Fatalf("task KPI: %+v", report.Tasks)
	}
	for _, account := range report.Accounts {
		if account.Email == "pending@staff.test" && (account.Status != "PENDING" || account.InboundCalls != 0) {
			t.Fatalf("pending account lost: %+v", account)
		}
	}
	// A completed Call can still be waiting for its staff leg's terminal receipt.
	// Missing inbound timing must not make complete outbound timing unavailable.
	incompleteCall := insertCall(locA, "INBOUND", []int{120}, true)
	if _, err := pool.Exec(ctx, `UPDATE human_calling_call_legs SET state='ENDING', ended_at=NULL WHERE call_id=$1`, incompleteCall); err != nil {
		t.Fatal(err)
	}
	report, err = module.QueryStaffAnalytics(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total.InboundCalls != 2 || report.Total.MissingInboundDurationCalls != 1 || report.Total.MissingOutboundDurationCalls != 0 || report.Total.OutboundSeconds != 600 {
		t.Fatalf("missing inbound timing affected outbound totals: %+v", report.Total)
	}
	for _, account := range report.Accounts {
		if account.Email == staff.Email && (account.MissingInboundDurationCalls != 1 || account.MissingOutboundDurationCalls != 0 || account.OutboundSeconds != 600) {
			t.Fatalf("missing inbound timing affected outbound account metrics: %+v", account)
		}
	}
	command.Identity = staff
	if _, err := module.QueryStaffAnalytics(ctx, command); !errors.Is(err, workspace.ErrDenied) {
		t.Fatalf("staff must be denied: %v", err)
	}
	command.Identity = admin
	command.PracticeID = otherPractice
	command.LocationID = otherLocation
	if _, err := module.QueryStaffAnalytics(ctx, command); !errors.Is(err, workspace.ErrDenied) {
		t.Fatalf("other Practice must be denied: %v", err)
	}
	command.PracticeID = practice
	if _, err := module.QueryStaffAnalytics(ctx, command); !errors.Is(err, workspace.ErrDenied) {
		t.Fatalf("foreign Location must be denied: %v", err)
	}
}

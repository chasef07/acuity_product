package workspace_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPhoneSummaryListsEveryReadableTaskAndNameForOnePhone(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Now().UTC().Add(-time.Hour)
	clock := func() time.Time { return now }
	accessModule := access.New(db.As(testdb.Portal), clock)
	if _, err := access.New(pool, clock).Provision(ctx, access.Provisioning{
		Environment: "test",
		RequestedBy: "phone-summary-test",
		Practices: []access.PracticeProvision{{
			Key:  "phone-summary",
			Name: "Synthetic Phone Summary",
			Locations: []access.LocationProvision{
				{Key: "north", Name: "North Office", AbitaOfficeKeys: []string{"north"}},
				{Key: "south", Name: "South Office", AbitaOfficeKeys: []string{"south"}},
				{Key: "hidden", Name: "Hidden Office", AbitaOfficeKeys: []string{"hidden"}},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key:                  "staff",
				Email:                "staff@phone-summary.test",
				Role:                 access.RoleStaff,
				LocationScope:        access.LocationScopeSelected,
				SelectedLocationKeys: []string{"north", "south"},
			}},
		}},
	}); err != nil {
		t.Fatalf("provision phone summary fixture: %v", err)
	}
	identity := access.Identity{Subject: "phone-summary-staff", Email: "staff@phone-summary.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, identity)
	workModule := work.New(db.As(testdb.Portal), accessModule, clock)
	reads := workspace.New(db.As(testdb.Portal), accessModule)
	const phone = "+17275550199"
	create := func(office, key, callerName string) work.Task {
		t.Helper()
		task, status, err := workModule.CreateAITask(ctx, work.CreateAITaskCommand{
			Service: access.ServiceIdentity{
				Subject:       "phone-summary-service",
				PracticeID:    authorization.Practice.ID,
				LocationScope: access.LocationScopeAll,
				Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
			},
			OfficeKey:      office,
			OfficePhone:    "+17275550100",
			SourceCallID:   "phone-summary-" + key,
			IdempotencyKey: "phone-summary-" + key,
			Phone:          phone,
			CallerName:     callerName,
			Summary:        "Synthetic request " + key,
			Message:        "Synthetic follow-up",
			Category:       work.TaskCategoryOther,
			Urgency:        work.TaskUrgencyNormal,
		})
		if err != nil || status != work.TaskCreated {
			t.Fatalf("create Task %q = %#v, %q, %v", key, task, status, err)
		}
		now = now.Add(time.Minute)
		return task
	}
	complete := func(task work.Task, reason work.CloseReason) work.Task {
		t.Helper()
		completed, err := workModule.CompleteTask(ctx, work.CompleteTaskCommand{
			Identity: identity, TaskID: task.ID, ExpectedVersion: task.Version, Reason: reason,
		})
		if err != nil {
			t.Fatalf("complete Task %s: %v", task.ID, err)
		}
		now = now.Add(time.Minute)
		return completed
	}
	query := func(target string) workspace.PhoneSummary {
		t.Helper()
		summary, err := reads.QueryPhoneSummary(ctx, workspace.QueryPhoneSummaryCommand{
			Identity: identity, PracticeID: authorization.Practice.ID, Phone: target,
		})
		if err != nil {
			t.Fatalf("query phone summary for %s: %v", target, err)
		}
		return summary
	}
	ids := func(tasks []work.Task) []string {
		result := make([]string, 0, len(tasks))
		for _, task := range tasks {
			result = append(result, task.ID)
		}
		return result
	}

	stale := create("north", "stale", "Dolores Reyes")
	northOlder := create("north", "older", "Dolores Reyes")
	southTask := create("south", "south", "Marco Reyes")
	northNewer := create("north", "newer", "")
	closed := create("south", "closed", "")
	create("hidden", "hidden", "Hidden Caller")
	otherPhone, _, err := workModule.CreateAITask(ctx, work.CreateAITaskCommand{
		Service: access.ServiceIdentity{
			Subject:       "phone-summary-service",
			PracticeID:    authorization.Practice.ID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
		},
		OfficeKey: "north", OfficePhone: "+17275550100",
		SourceCallID: "phone-summary-other", IdempotencyKey: "phone-summary-other",
		Phone: "+17275550188", CallerName: "Another Caller",
		Summary: "Synthetic other phone", Message: "Synthetic follow-up",
		Category: work.TaskCategoryOther, Urgency: work.TaskUrgencyNormal,
	})
	if err != nil {
		t.Fatalf("create Task for another phone: %v", err)
	}
	stale = complete(stale, work.CloseReasonDone)
	closed = complete(closed, work.CloseReasonNotNeeded)
	if _, err := pool.Exec(ctx,
		`UPDATE work_tasks SET completed_at = CURRENT_TIMESTAMP - INTERVAL '25 hours' WHERE id = $1`,
		stale.ID,
	); err != nil {
		t.Fatalf("age completed Task: %v", err)
	}

	summary := query(phone)
	if summary.Phone != phone {
		t.Fatalf("summary phone = %q, want %q", summary.Phone, phone)
	}
	if got, want := ids(summary.OpenTasks), []string{northNewer.ID, southTask.ID, northOlder.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("open Tasks = %v, want newest activity first across both readable Locations %v (another phone's Task is %s)", got, want, otherPhone.ID)
	}
	offices := map[string]string{}
	for _, task := range summary.OpenTasks {
		offices[task.ID] = task.LocationName
		if task.State != work.TaskOpen || task.Category != work.TaskCategoryOther || task.Origin != work.TaskOriginAbitaAI {
			t.Fatalf("open Task lost its projection: %#v", task)
		}
	}
	if offices[northNewer.ID] != "North Office" || offices[southTask.ID] != "South Office" {
		t.Fatalf("open Task offices = %v", offices)
	}
	if got, want := ids(summary.ClosedToday), []string{closed.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("closed today = %v, want only the Task closed in the last 24 hours %v", got, want)
	}
	closedRow := summary.ClosedToday[0]
	if closedRow.CloseReason != work.CloseReasonNotNeeded || closedRow.CompletedBy == nil ||
		closedRow.CompletedBy.Email != identity.Email || closedRow.LocationName != "South Office" {
		t.Fatalf("closed Task = %#v, want reason, actor and office", closedRow)
	}
	if got, want := summary.Names, []string{"Marco Reyes", "Dolores Reyes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v with the unreadable Location's caller left out", got, want)
	}
	if summary.DisplayName != "" {
		t.Fatalf("display name = %q, want none while two names share the phone", summary.DisplayName)
	}
	if summary.OpenTaskCount != 3 {
		t.Fatalf("open Task count = %d, want 3", summary.OpenTaskCount)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO messaging_threads(
			practice_id, location_id, office_phone, external_phone,
			display_name, name_source, created_at, updated_at
		)
		SELECT $1, location.id, '+17275550100', $2, seen.name, 'synthetic', seen.created_at, $3
		FROM (VALUES
			('north', 'dolores reyes', $3::timestamptz - INTERVAL '3 days'),
			('south', 'Ana Reyes', $3::timestamptz),
			('hidden', 'Hidden Texter', $3::timestamptz)
		) seen(location_key, name, created_at)
		JOIN access_locations location
			ON location.practice_id = $1 AND location.provisioning_key = seen.location_key
	`, authorization.Practice.ID, phone, now); err != nil {
		t.Fatalf("insert named threads: %v", err)
	}
	withThread := query(phone)
	if got, want := withThread.Names, []string{"Ana Reyes", "Marco Reyes", "Dolores Reyes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names with threads = %v, want %v: one entry per name whatever its case, ordered by when each name was recorded, so a new message on an old thread does not promote its name", got, want)
	}
	if oneName := query("+17275550188"); !reflect.DeepEqual(oneName.Names, []string{"Another Caller"}) ||
		oneName.DisplayName != "Another Caller" {
		t.Fatalf("summary for a phone with one name = %v, display name %q", oneName.Names, oneName.DisplayName)
	}

	empty := query("+17275550177")
	if empty.OpenTasks == nil || empty.ClosedToday == nil || empty.Names == nil ||
		len(empty.OpenTasks)+len(empty.ClosedToday)+len(empty.Names)+empty.OpenTaskCount != 0 ||
		empty.DisplayName != "" {
		t.Fatalf("summary for a phone with no history = %#v, want empty lists", empty)
	}

	for _, invalid := range []string{"", "7275550199", "+1 727 555 0199", "not-a-phone"} {
		if _, err := reads.QueryPhoneSummary(ctx, workspace.QueryPhoneSummaryCommand{
			Identity: identity, PracticeID: authorization.Practice.ID, Phone: invalid,
		}); !errors.Is(err, workspace.ErrInvalidInput) {
			t.Fatalf("summary for invalid phone %q: %v, want ErrInvalidInput", invalid, err)
		}
	}
	stranger := access.Identity{Subject: "phone-summary-stranger", Email: "stranger@phone-summary.test", EmailVerified: true}
	if _, err := reads.QueryPhoneSummary(ctx, workspace.QueryPhoneSummaryCommand{
		Identity: stranger, PracticeID: authorization.Practice.ID, Phone: phone,
	}); !errors.Is(err, workspace.ErrDenied) {
		t.Fatalf("summary for an identity without a grant: %v, want ErrDenied", err)
	}
}

type phoneSummaryFixture struct {
	pool        *pgxpool.Pool
	work        *work.Module
	practiceID  string
	otherID     string
	locationIDs map[string]string
	query       func(phone string) workspace.PhoneSummary
}

func newPhoneSummaryFixture(t *testing.T) phoneSummaryFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	accessModule := access.New(db.As(testdb.Portal), nil)
	if _, err := access.New(db.Owner, nil).Provision(ctx, access.Provisioning{
		Environment: "test",
		RequestedBy: "phone-summary-test",
		Practices: []access.PracticeProvision{
			{
				Key:  "phone-summary",
				Name: "Synthetic Phone Summary",
				Locations: []access.LocationProvision{
					{Key: "spring-hill", Name: "Spring Hill Office", AbitaOfficeKeys: []string{"spring-hill"}},
				},
				AccessGrants: []access.AccessGrantProvision{{
					Key:           "staff",
					Email:         "staff@phone-summary.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
				}},
			},
			{
				Key:       "phone-summary-other",
				Name:      "Synthetic Other Practice",
				Locations: []access.LocationProvision{{Key: "other-office", Name: "Other Office"}},
			},
		},
	}); err != nil {
		t.Fatalf("provision phone summary fixture: %v", err)
	}
	identity := access.Identity{Subject: "phone-summary-staff", Email: "staff@phone-summary.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, identity)
	fixture := phoneSummaryFixture{
		pool:        db.Owner,
		work:        work.New(db.As(testdb.Portal), accessModule, nil),
		practiceID:  authorization.Practice.ID,
		locationIDs: map[string]string{},
	}
	rows, err := db.Owner.Query(ctx, `
		SELECT location.provisioning_key, location.id::text, location.practice_id::text
		FROM access_locations location
	`)
	if err != nil {
		t.Fatalf("read fixture Locations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, id, practiceID string
		if err := rows.Scan(&key, &id, &practiceID); err != nil {
			t.Fatalf("scan fixture Location: %v", err)
		}
		fixture.locationIDs[key] = id
		if practiceID != fixture.practiceID {
			fixture.otherID = practiceID
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read fixture Locations: %v", err)
	}
	reads := workspace.New(db.As(testdb.Portal), accessModule)
	fixture.query = func(phone string) workspace.PhoneSummary {
		t.Helper()
		summary, err := reads.QueryPhoneSummary(ctx, workspace.QueryPhoneSummaryCommand{
			Identity: identity, PracticeID: fixture.practiceID, Phone: phone,
		})
		if err != nil {
			t.Fatalf("query phone summary for %s: %v", phone, err)
		}
		return summary
	}
	return fixture
}

const insertSyntheticPhoneTasks = `
	INSERT INTO work_tasks (
		practice_id, location_id, phone, title, state, origin, category,
		created_by_kind, created_by_subject, created_at, updated_at,
		source_call_id, source_message, ai_idempotency_key, ai_input_fingerprint, caller_name,
		completed_by_kind, completed_by_subject, completed_at, close_reason
	)
	SELECT
		$1, $2, $3, $4 || ' ' || n, $5, 'ABITA_AI', 'other',
		'SERVICE', 'phone-summary-fixture',
		CURRENT_TIMESTAMP - INTERVAL '3 days' + n * INTERVAL '1 minute',
		CURRENT_TIMESTAMP - INTERVAL '3 days' + n * INTERVAL '1 minute',
		$4 || '-' || n, 'Synthetic follow-up', $4 || '-' || n, decode(repeat('00', 32), 'hex'),
		CASE WHEN $6 THEN 'Synthetic Caller ' || n END,
		CASE WHEN $5 = 'COMPLETED' THEN 'SERVICE' END,
		CASE WHEN $5 = 'COMPLETED' THEN 'phone-summary-fixture' END,
		CASE WHEN $5 = 'COMPLETED' THEN CURRENT_TIMESTAMP - $7::interval - n * INTERVAL '1 second' END,
		CASE WHEN $5 = 'COMPLETED' THEN $8 END
	FROM generate_series(1, $9::int) n`

func TestPhoneSummaryCapsOpenTasksClosedTasksAndNames(t *testing.T) {
	ctx := context.Background()
	fixture := newPhoneSummaryFixture(t)
	const phone = "+17275550199"
	office := fixture.locationIDs["spring-hill"]
	for _, batch := range []struct {
		title string
		state string
		named bool
		count int
	}{
		{"open", "OPEN", false, 53},
		{"closed", "COMPLETED", false, 22},
		{"named", "OPEN", true, 7},
	} {
		if _, err := fixture.pool.Exec(ctx, insertSyntheticPhoneTasks,
			fixture.practiceID, office, phone, batch.title, batch.state, batch.named,
			"1 hour", "DONE", batch.count,
		); err != nil {
			t.Fatalf("insert %d %s Tasks: %v", batch.count, batch.title, err)
		}
	}

	summary := fixture.query(phone)
	if len(summary.OpenTasks) != 50 || summary.OpenTaskCount != 60 {
		t.Fatalf("open Tasks = %d of %d, want the 50 with the newest activity of 60", len(summary.OpenTasks), summary.OpenTaskCount)
	}
	if first := summary.OpenTasks[0].Title; first != "open 53" {
		t.Fatalf("first open Task = %q, want the one with the newest activity", first)
	}
	if len(summary.ClosedToday) != 20 || summary.ClosedToday[0].Title != "closed 1" {
		t.Fatalf("closed today = %d Tasks starting at %q, want the 20 closed most recently", len(summary.ClosedToday), summary.ClosedToday[0].Title)
	}
	if want := []string{
		"Synthetic Caller 7", "Synthetic Caller 6", "Synthetic Caller 5", "Synthetic Caller 4", "Synthetic Caller 3",
	}; !reflect.DeepEqual(summary.Names, want) {
		t.Fatalf("names = %v, want the five recorded most recently %v", summary.Names, want)
	}
}

func TestPhoneSummaryKeepsReviewTasksAndLeavesOutExpiredAndOtherPracticeWork(t *testing.T) {
	ctx := context.Background()
	fixture := newPhoneSummaryFixture(t)
	const phone = "+17275550199"
	office := fixture.locationIDs["spring-hill"]
	now := time.Now().UTC()

	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin review fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var threadID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO messaging_threads(practice_id, location_id, office_phone, external_phone, created_at, updated_at)
		VALUES ($1, $2, '+17275550100', $3, $4, $4) RETURNING id::text
	`, fixture.practiceID, office, phone, now).Scan(&threadID); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	messageID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO messaging_messages(
			id, thread_id, practice_id, location_id, direction, body, sender, destination,
			delivery_state, provider_message_id, created_at, updated_at
		) VALUES ($1::uuid, $2, $3, $4, 'INBOUND', 'Synthetic request', $5, '+17275550100', 'DELIVERED', $1::text, $6, $6)
	`, messageID, threadID, fixture.practiceID, office, phone, now); err != nil {
		t.Fatalf("insert inbound text: %v", err)
	}
	if err := fixture.work.EnsureInboundMessageReview(
		ctx, tx, fixture.practiceID, office, phone, threadID, messageID, now,
	); err != nil {
		t.Fatalf("ensure text review: %v", err)
	}
	if err := fixture.work.EnsureAppointmentReview(
		ctx, tx, uuid.NewString(), fixture.practiceID, office, phone,
		"synthetic-appointment", "Review booked appointment", "Check the synthetic appointment.", now,
	); err != nil {
		t.Fatalf("ensure appointment review: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit review fixture: %v", err)
	}
	for _, closed := range []struct {
		title  string
		age    string
		reason string
	}{
		{"closed 23 hours ago", "23 hours", "NOT_NEEDED"},
		{"closed 25 hours ago", "25 hours", "DONE"},
		{"expired an hour ago", "1 hour", "EXPIRED"},
	} {
		if _, err := fixture.pool.Exec(ctx, insertSyntheticPhoneTasks,
			fixture.practiceID, office, phone, closed.title, "COMPLETED", false,
			closed.age, closed.reason, 1,
		); err != nil {
			t.Fatalf("insert Task %q: %v", closed.title, err)
		}
	}
	otherOffice := fixture.locationIDs["other-office"]
	for _, other := range []struct {
		title string
		state string
	}{{"other practice open", "OPEN"}, {"other practice closed", "COMPLETED"}} {
		if _, err := fixture.pool.Exec(ctx, insertSyntheticPhoneTasks,
			fixture.otherID, otherOffice, phone, other.title, other.state, true,
			"1 hour", "DONE", 2,
		); err != nil {
			t.Fatalf("insert Tasks %q in the other Practice: %v", other.title, err)
		}
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO messaging_threads(
			practice_id, location_id, office_phone, external_phone,
			display_name, name_source, created_at, updated_at
		) VALUES ($1, $2, '+17275550100', $3, 'Other Practice Texter', 'synthetic', $4, $4)
	`, fixture.otherID, otherOffice, phone, now); err != nil {
		t.Fatalf("insert a named thread in the other Practice: %v", err)
	}

	summary := fixture.query(phone)
	origins := map[work.TaskOrigin]int{}
	for _, task := range summary.OpenTasks {
		origins[task.Origin]++
	}
	if want := map[work.TaskOrigin]int{
		work.TaskOriginInboundMessageReview: 1, work.TaskOriginAppointmentReview: 1,
	}; !reflect.DeepEqual(origins, want) || summary.OpenTaskCount != 2 {
		t.Fatalf("open Task origins = %v (count %d), want one text review and one appointment review and nothing from the other Practice", origins, summary.OpenTaskCount)
	}
	if len(summary.ClosedToday) != 1 || summary.ClosedToday[0].Title != "closed 23 hours ago 1" ||
		summary.ClosedToday[0].CloseReason != work.CloseReasonNotNeeded ||
		summary.ClosedToday[0].CompletedBy == nil || summary.ClosedToday[0].CompletedBy.Kind != "SERVICE" {
		t.Fatalf("closed today = %#v, want only the system close from 23 hours ago with its reason", summary.ClosedToday)
	}
	if len(summary.Names) != 0 || summary.DisplayName != "" {
		t.Fatalf("names = %v, want none from the other Practice", summary.Names)
	}
}

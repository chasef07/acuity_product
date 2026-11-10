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
	if summary.DisplayName != "Marco Reyes" {
		t.Fatalf("display name = %q, want the most recent name", summary.DisplayName)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO messaging_threads(
			practice_id, location_id, office_phone, external_phone,
			display_name, name_source, created_at, updated_at
		)
		SELECT $1, location.id, '+17275550100', $2, seen.name, 'synthetic', $3, $3
		FROM (VALUES ('north', 'dolores reyes'), ('hidden', 'Hidden Texter')) seen(location_key, name)
		JOIN access_locations location
			ON location.practice_id = $1 AND location.provisioning_key = seen.location_key
	`, authorization.Practice.ID, phone, now); err != nil {
		t.Fatalf("insert named threads: %v", err)
	}
	withThread := query(phone)
	if got, want := withThread.Names, []string{"dolores reyes", "Marco Reyes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names with a thread = %v, want one entry per name whatever its case, most recent first %v", got, want)
	}
	if withThread.DisplayName != "dolores reyes" {
		t.Fatalf("display name with a thread = %q", withThread.DisplayName)
	}

	empty := query("+17275550177")
	if empty.OpenTasks == nil || empty.ClosedToday == nil || empty.Names == nil ||
		len(empty.OpenTasks)+len(empty.ClosedToday)+len(empty.Names) != 0 || empty.DisplayName != "" {
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

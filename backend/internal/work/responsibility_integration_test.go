package work_test

import (
	"context"
	"errors"
	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5/pgxpool"
	"reflect"
	"testing"
	"time"
)

const (
	colleagueCoveringEveryLocation = "colleague@synthetic.test"
	colleagueCoveringOnlyLocation2 = "elsewhere@synthetic.test"
)

func provisionColleagues(t *testing.T, owner *pgxpool.Pool, now time.Time) {
	t.Helper()
	_, err := access.New(owner, func() time.Time { return now }).Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "work-roster-test",
		Practices: []access.PracticeProvision{{
			Key:  "synthetic-practice",
			Name: "Synthetic Practice",
			Locations: []access.LocationProvision{
				{Key: "synthetic-location-1", Name: "Synthetic Location 1", AbitaOfficeKeys: []string{"spring-hill"}},
				{Key: "synthetic-location-2", Name: "Synthetic Location 2"},
			},
			AccessGrants: []access.AccessGrantProvision{
				{Key: "synthetic-staff", Email: "staff@synthetic.test", Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
				{Key: "synthetic-colleague", Email: colleagueCoveringEveryLocation, Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
				{Key: "synthetic-elsewhere", Email: colleagueCoveringOnlyLocation2, Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"synthetic-location-2"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("provision roster colleagues: %v", err)
	}
}

func TestResponsibilityProvisioningReportsUnmatchedAndCoverageGaps(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Now().UTC()
	a := access.New(db.As(testdb.Portal), func() time.Time { return now })
	_, identity := provisionStaff(t, db.Owner, a, now)
	provisionColleagues(t, pool, now)
	report, err := work.New(pool, a, func() time.Time { return now }).ProvisionResponsibilities(context.Background(), work.ResponsibilityProvision{PracticeKey: "synthetic-practice", Locations: []work.ResponsibilityLocation{{LocationKey: "synthetic-location-1", Members: []work.ResponsibilityMember{
		{Email: identity.Email, Category: "medication", Role: "primary"},
		{Email: identity.Email, Category: "medication", Role: "backup"},
		{Email: colleagueCoveringEveryLocation, Category: "optical", Role: "primary"},
		{Email: "pending@synthetic.test", Category: "documentation", Role: "primary"},
		{Email: colleagueCoveringOnlyLocation2, Category: "referrals", Role: "primary"},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := work.ResponsibilityReport{
		Applied:      3,
		Unmatched:    []string{"synthetic-location-1:pending@synthetic.test"},
		CoverageGaps: []string{"synthetic-location-1:" + colleagueCoveringOnlyLocation2},
	}
	if !reflect.DeepEqual(report, want) {
		t.Fatalf("report = %#v, want %#v", report, want)
	}
}

func TestResponsibilityProvisioningConvergesOnTheInput(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Now().UTC()
	a := access.New(db.As(testdb.Portal), func() time.Time { return now })
	provisionStaff(t, db.Owner, a, now)
	provisionColleagues(t, pool, now)
	ctx := context.Background()
	provision := func(input work.ResponsibilityProvision) (work.ResponsibilityReport, error) {
		return work.New(pool, a, func() time.Time { return now }).ProvisionResponsibilities(ctx, input)
	}
	rows := func() string {
		t.Helper()
		var snapshot string
		if err := pool.QueryRow(ctx, `SELECT
			(SELECT coalesce(string_agg(l.provisioning_key, ',' ORDER BY l.provisioning_key), '') FROM work_responsibility_locations c JOIN access_locations l ON l.id=c.location_id)
			|| ' | ' ||
			(SELECT coalesce(string_agg(concat_ws(':', l.provisioning_key, r.account_email, r.category, r.role), ',' ORDER BY l.provisioning_key, r.account_email, r.category, r.role), '') FROM work_responsibilities r JOIN access_locations l ON l.id=r.location_id)`).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	second := work.ResponsibilityLocation{LocationKey: "synthetic-location-2", Members: []work.ResponsibilityMember{{Email: colleagueCoveringOnlyLocation2, Category: "insurance", Role: "primary"}}}
	both := work.ResponsibilityProvision{PracticeKey: "synthetic-practice", Locations: []work.ResponsibilityLocation{
		{LocationKey: "synthetic-location-1", Members: []work.ResponsibilityMember{{Email: colleagueCoveringEveryLocation, Category: "optical", Role: "primary"}}},
		second,
	}}
	first, err := provision(both)
	if err != nil {
		t.Fatal(err)
	}
	loaded := rows()
	wantLoaded := "synthetic-location-1,synthetic-location-2 | synthetic-location-1:" + colleagueCoveringEveryLocation + ":optical:primary,synthetic-location-2:" + colleagueCoveringOnlyLocation2 + ":insurance:primary"
	if loaded != wantLoaded {
		t.Fatalf("roster rows = %q, want %q", loaded, wantLoaded)
	}
	again, err := provision(both)
	if err != nil {
		t.Fatal(err)
	}
	if rows() != loaded || !reflect.DeepEqual(again, first) {
		t.Fatalf("second run changed the roster: rows %q -> %q, report %#v -> %#v", loaded, rows(), first, again)
	}

	if _, err := provision(work.ResponsibilityProvision{PracticeKey: "synthetic-practice", Locations: []work.ResponsibilityLocation{second}}); err != nil {
		t.Fatal(err)
	}
	if got, want := rows(), "synthetic-location-2 | synthetic-location-2:"+colleagueCoveringOnlyLocation2+":insurance:primary"; got != want {
		t.Fatalf("rows after dropping synthetic-location-1 = %q, want %q", got, want)
	}

	_, err = provision(work.ResponsibilityProvision{PracticeKey: "synthetic-practice", Locations: []work.ResponsibilityLocation{{LocationKey: "missing-location"}}})
	if err == nil || rows() != "synthetic-location-2 | synthetic-location-2:"+colleagueCoveringOnlyLocation2+":insurance:primary" {
		t.Fatalf("unknown Location: err=%v rows=%q", err, rows())
	}
}

func TestReclassificationRunAndRestorationNeverOverwriteStaffEdits(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Now().UTC()
	a := access.New(db.As(testdb.Portal), func() time.Time { return now })
	auth, identity := provisionStaff(t, db.Owner, a, now)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES($1,$2)`, identity.Email, identity.Subject); err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, a, identity)
	m := work.New(db.As(testdb.Portal), a, func() time.Time { return now })
	maintenance := work.New(pool, access.New(pool, func() time.Time { return now }), func() time.Time { return now })
	task, _, err := m.CreateAITask(ctx, work.CreateAITaskCommand{Service: access.ServiceIdentity{Subject: "backfill-agent", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}, OfficeKey: "spring-hill", OfficePhone: "+12025550100", SourceCallID: "backfill-call", IdempotencyKey: "backfill-expedite", Phone: "+12025550123", Summary: "Expedite prescription", Message: "Caller requests a glasses prescription copy.", Category: work.TaskCategoryDocumentation, Urgency: work.TaskUrgencyNormal})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := maintenance.PlanReclassification(ctx, identity, "synthetic-run", auth.Practice.ID, []string{task.LocationID})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].NewCategory != work.TaskCategoryOptical {
		t.Fatalf("plan: %#v", plan)
	}
	result, err := maintenance.ApplyReclassification(ctx, identity, plan)
	if err != nil || result[0].Status != "applied" {
		t.Fatalf("apply: %#v %v", result, err)
	}
	result, err = maintenance.ApplyReclassification(ctx, identity, plan)
	if err != nil || result[0].Status != "already_applied" {
		t.Fatalf("repeat: %#v %v", result, err)
	}
	restore, err := maintenance.RestorationPlan(ctx, identity, plan.RunID, plan.PracticeID, plan.LocationIDs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ChangeTaskCategory(ctx, work.ChangeTaskCategoryCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: 2, Category: work.TaskCategoryOther}); err != nil {
		t.Fatal(err)
	}
	result, err = maintenance.ApplyReclassification(ctx, identity, restore)
	if err != nil || result[0].Status != "skipped_changed" {
		t.Fatalf("restore overwrote staff: %#v %v", result, err)
	}
	preserved, err := m.ReadTask(ctx, identity, task.ID)
	if err != nil || preserved.State != work.TaskOpen || !preserved.CreatedAt.Equal(task.CreatedAt) || preserved.SourceMessage != task.SourceMessage {
		t.Fatalf("preservation: %#v %v", preserved, err)
	}
}

func TestReclassificationPlanUsesRequestContextAcrossAuthorizationSubjects(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Now().UTC()
	a := access.New(db.As(testdb.Portal), func() time.Time { return now })
	auth, identity := provisionStaff(t, db.Owner, a, now)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES($1,$2)`, identity.Email, identity.Subject); err != nil {
		t.Fatal(err)
	}
	m := work.New(db.As(testdb.Portal), a, func() time.Time { return now })
	maintenance := work.New(pool, access.New(pool, func() time.Time { return now }), func() time.Time { return now })
	cases := []struct {
		key, message string
		old, want    work.TaskCategory
	}{
		{"glasses", "Please expedite my glasses prescription copy.", "documentation", "optical"},
		{"copay", "Please confirm the copay for my appointment.", "other", "insurance"},
		{"copay-charge", "Why was I charged a co-pay for my glasses?", "optical", "insurance"},
		{"copayment", "Please explain the co payment for my visit.", "appointments", "insurance"},
		{"copago", "Necesito confirmar el copago de mi cita.", "other", "insurance"},
		{"med-copay", "Please confirm my medication copayment amount.", "medication", "insurance"},
		{"mixed-copay", "Please refill my medication and explain my copay.", "medication", ""},
		{"incidental-copay", "Please send my medical records; my copay question was already answered.", "documentation", ""},
		{"note-copay", "Please send a work note; my copay question was already answered.", "documentation", ""},
		{"referral-copay", "Please check my specialist referral; my copay was explained.", "referrals", ""},
		{"imaging-copay", "Please coordinate my imaging order and review my copay.", "referrals", ""},
		{"medication", "Please expedite a medication prescription refill.", "documentation", "medication"},
		{"med-pa", "Prior authorization for eye drops was denied by insurance.", "insurance", "medication"},
		{"service-pa", "Prior authorization for a procedure was denied.", "referrals", "insurance"},
		{"records-pa", "Records release authorization form is needed.", "insurance", "documentation"},
		{"unknown-pa", "Please check the prior authorization status.", "referrals", ""},
		{"preop", "Please explain instructions before surgery.", "medication", "pre_op"},
		{"postop", "Please explain aftercare after surgery.", "medication", "post_op"},
		{"scheduling", "Please reschedule my surgery appointment.", "other", "appointments"},
		{"referral", "Please check specialist referral receipt.", "insurance", "referrals"},
		{"ambiguous", "The caller mentioned surgery and needs help.", "other", ""},
	}
	expected := map[string]work.TaskCategory{}
	for _, c := range cases {
		task, _, err := m.CreateAITask(ctx, work.CreateAITaskCommand{Service: access.ServiceIdentity{Subject: "classification-agent", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}, OfficeKey: "spring-hill", OfficePhone: "+12025550100", SourceCallID: c.key, IdempotencyKey: c.key, Phone: "+12025550123", Summary: c.key, Message: c.message, Category: c.old, Urgency: work.TaskUrgencyNormal})
		if err != nil {
			t.Fatal(err)
		}
		expected[task.ID] = c.want
	}
	plan, err := maintenance.PlanReclassification(ctx, identity, "classification-plan", auth.Practice.ID, []string{auth.Locations[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != len(cases) {
		t.Fatalf("plan lost Tasks: %d", len(plan.Tasks))
	}
	for _, entry := range plan.Tasks {
		if entry.NewCategory != expected[entry.TaskID] {
			t.Errorf("%s: got %s, want %s", entry.Title, entry.NewCategory, expected[entry.TaskID])
		}
	}
}

func TestCategoryChangeDeniesUnrelatedStaffWithoutSideEffects(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Now().UTC()
	a := access.New(db.As(testdb.Portal), func() time.Time { return now })
	auth, identity := provisionStaff(t, db.Owner, a, now)
	ctx := context.Background()
	m := work.New(db.As(testdb.Portal), a, func() time.Time { return now })
	callID := insertCall(t, pool, auth, now)
	task := ensureCallFollowUp(t, db.As(testdb.Portal), m, work.EnsureCallFollowUpCommand{CallID: callID, PracticeID: auth.Practice.ID, LocationID: auth.Locations[0].ID, Phone: "+15555550100", Reason: "Synthetic request", Creator: auth.Actor})
	outsider := access.Identity{Subject: "unrelated-staff", Email: "unrelated@synthetic.test", EmailVerified: true}
	_, err := m.ChangeTaskCategory(ctx, work.ChangeTaskCategoryCommand{Identity: outsider, TaskID: task.ID, ExpectedVersion: task.Version, Category: work.TaskCategoryPreOp})
	if !errors.Is(err, work.ErrDenied) {
		t.Fatalf("move denied: %v", err)
	}
	current, err := m.ReadTask(ctx, identity, task.ID)
	if err != nil || current.Version != task.Version || current.State != work.TaskOpen {
		t.Fatalf("denied command changed Task: %#v %v", current, err)
	}
}

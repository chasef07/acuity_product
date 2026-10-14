package work_test

import (
	"context"
	"errors"
	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"testing"
	"time"
)

func TestResponsibilitiesFilterWithoutGrantingAccess(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	m := work.New(pool, a, func() time.Time { return now })
	report, err := m.ProvisionResponsibilities(context.Background(), work.ResponsibilityProvision{PracticeKey: "synthetic-practice", Locations: []work.ResponsibilityLocation{{LocationKey: "synthetic-location-1", Members: []work.ResponsibilityMember{{Email: identity.Email, Category: "medication", Role: "primary"}, {Email: identity.Email, Category: "medication", Role: "backup"}, {Email: "pending@synthetic.test", Category: "documentation", Role: "primary"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unmatched) != 1 {
		t.Fatalf("unmatched: %#v", report)
	}
	for _, category := range []work.TaskCategory{"medication", "optical", "pre_op"} {
		_, _, err = m.CreateAITask(context.Background(), work.CreateAITaskCommand{Service: access.ServiceIdentity{Subject: "synthetic-agent", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}, OfficeKey: "spring-hill", OfficePhone: "+12025550100", SourceCallID: "roster-call", IdempotencyKey: string(category), Phone: "+12025550123", Summary: string(category), Message: "Synthetic staff follow-up", Category: category, Urgency: work.TaskUrgencyNormal})
		if err != nil {
			t.Fatal(err)
		}
	}
	reads := workspace.New(pool, a)
	page, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Responsibility: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Category != "medication" || page.Counts.Tasks != 1 {
		t.Fatalf("My groups: %#v", page)
	}
	all, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Responsibility: "all"})
	if err != nil || len(all.Items) != 3 {
		t.Fatalf("All tasks: %#v %v", all, err)
	}
}

func TestReviewedGroupPreservesEachRequestAndRejectsNewMembership(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	m := work.New(pool, a, func() time.Time { return now })
	ctx := context.Background()
	create := func(key, name string) work.Task {
		t.Helper()
		task, _, err := m.CreateAITask(ctx, work.CreateAITaskCommand{Service: access.ServiceIdentity{Subject: "group-agent", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}, OfficeKey: "spring-hill", OfficePhone: "+12025550100", SourceCallID: key, IdempotencyKey: key, Phone: "(202) 555-0123", CallerName: name, Summary: "Optical request " + key, Message: "Synthetic glasses prescription follow-up", Category: work.TaskCategoryOptical, Urgency: work.TaskUrgencyNormal})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	first := create("first", "Family Member One")
	second := create("second", "Family Member Two")
	reads := workspace.New(pool, a)
	page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Grouped: true, Search: "Family Member One", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || len(page.Items[0].GroupMembers) != 2 || page.Counts.Tasks != 1 || page.NextCursor != "" {
		t.Fatalf("filtered group hid membership: %#v", page)
	}
	reviewed := []work.ReviewedTask{{ID: first.ID, ExpectedVersion: first.Version}, {ID: second.ID, ExpectedVersion: second.Version}}
	third := create("third", "Family Member Three")
	_, err = m.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{Identity: identity, TaskID: first.ID, Members: reviewed})
	if !errors.Is(err, work.ErrConflict) {
		t.Fatalf("unseen arrival: %v", err)
	}
	reviewed = append(reviewed, work.ReviewedTask{ID: third.ID, ExpectedVersion: third.Version})
	_, err = m.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{Identity: identity, TaskID: first.ID, Members: reviewed})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range reviewed {
		task, err := m.ReadTask(ctx, identity, member.ID)
		if err != nil || task.State != work.TaskCompleted || task.CompletedBy.Subject != identity.Subject {
			t.Fatalf("completion: %#v %v", task, err)
		}
	}
	reopened, err := m.ReopenTask(ctx, work.ReopenTaskCommand{Identity: identity, TaskID: second.ID, ExpectedVersion: 2})
	if err != nil || reopened.State != work.TaskOpen {
		t.Fatalf("reopen: %v", err)
	}
}

func TestRelatedTaskGroupsSpanTaskKindsForOneCaller(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	m := work.New(pool, a, func() time.Time { return now })
	ctx := context.Background()
	ordinary := ensureCallFollowUp(t, pool, m, work.EnsureCallFollowUpCommand{CallID: insertCall(t, pool, auth, now), PracticeID: auth.Practice.ID, LocationID: auth.Locations[0].ID, Phone: "+15555550100", Reason: "Synthetic administrative follow-up", Creator: auth.Actor})
	callID := insertCall(t, pool, auth, now.Add(time.Second))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := m.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{CallID: callID, PracticeID: auth.Practice.ID, LocationID: ordinary.LocationID, Phone: ordinary.Phone, Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: now.Add(time.Second)})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	ai, _, err := m.CreateAITask(ctx, work.CreateAITaskCommand{Service: access.ServiceIdentity{Subject: "group-agent", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}, OfficeKey: "spring-hill", OfficePhone: "+12025550100", SourceCallID: "other-ai-call", IdempotencyKey: "billing", Phone: ordinary.Phone, Summary: "Billing question", Message: "Synthetic billing follow-up", Category: work.TaskCategoryBilling, Urgency: work.TaskUrgencyNormal})
	if err != nil {
		t.Fatal(err)
	}
	other := createTask(t, pool, m, auth, auth.Locations[1].ID, ordinary.Phone, "Same caller at another office", now.Add(2*time.Second))
	members := map[string]work.Task{ordinary.ID: ordinary, recovery.ID: recovery, ai.ID: ai}
	reads := workspace.New(pool, a)
	for _, kind := range []string{"", "follow_up", "calls"} {
		page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Grouped: true, Kind: kind, LocationID: auth.Locations[0].ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || len(page.Items[0].GroupMembers) != len(members) {
			t.Fatalf("kind %q: got %+v, want one group of %d", kind, page.Items, len(members))
		}
		for _, member := range page.Items[0].GroupMembers {
			if _, ok := members[member.ID]; !ok {
				t.Fatalf("kind %q grouped an unrelated Task %s", kind, member.ID)
			}
		}
	}
	all, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Grouped: true})
	if err != nil || len(all.Items) != 2 {
		t.Fatalf("each office keeps its own group: %+v %v", all.Items, err)
	}
	for _, ordering := range []work.TaskOrdering{work.TaskOrderingTime, work.TaskOrderingPriority, work.TaskOrderingRecent} {
		page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Grouped: true, Ordering: ordering, LocationID: auth.Locations[0].ID, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || len(page.Items[0].GroupMembers) != len(members) || page.NextCursor != "" {
			t.Fatalf("%s split one caller across pages: %+v", ordering, page)
		}
		for _, member := range page.Items[0].GroupMembers {
			wantInteractions := 0
			if member.ID == recovery.ID {
				wantInteractions = 1
			}
			if member.RelatedInteractionCount != wantInteractions {
				t.Fatalf("%s lost source count: %+v", ordering, member)
			}
		}
	}
	reviewed := []work.ReviewedTask{{ID: ordinary.ID, ExpectedVersion: ordinary.Version}, {ID: recovery.ID, ExpectedVersion: recovery.Version}}
	_, err = m.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{Identity: identity, TaskID: ordinary.ID, Members: reviewed})
	if !errors.Is(err, work.ErrConflict) {
		t.Fatalf("resolving without the unseen AI Task should conflict: %v", err)
	}
	reviewed = append(reviewed, work.ReviewedTask{ID: ai.ID, ExpectedVersion: ai.Version})
	if _, err = m.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{Identity: identity, TaskID: ordinary.ID, Members: reviewed}); err != nil {
		t.Fatal(err)
	}
	for id := range members {
		task, err := m.ReadTask(ctx, identity, id)
		if err != nil || task.State != work.TaskCompleted {
			t.Fatalf("group member %s not completed: %v", id, err)
		}
	}
	remaining, err := m.ReadTask(ctx, identity, other.ID)
	if err != nil || remaining.State != work.TaskOpen {
		t.Fatalf("another office's Task changed: %v", err)
	}
}

func TestReclassificationRunAndRestorationNeverOverwriteStaffEdits(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES($1,$2)`, identity.Email, identity.Subject); err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, a, identity)
	m := work.New(pool, a, func() time.Time { return now })
	task, _, err := m.CreateAITask(ctx, work.CreateAITaskCommand{Service: access.ServiceIdentity{Subject: "backfill-agent", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}, OfficeKey: "spring-hill", OfficePhone: "+12025550100", SourceCallID: "backfill-call", IdempotencyKey: "backfill-expedite", Phone: "+12025550123", Summary: "Expedite prescription", Message: "Caller requests a glasses prescription copy.", Category: work.TaskCategoryDocumentation, Urgency: work.TaskUrgencyNormal})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.PlanReclassification(ctx, identity, "synthetic-run", auth.Practice.ID, []string{task.LocationID})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].NewCategory != work.TaskCategoryOptical {
		t.Fatalf("plan: %#v", plan)
	}
	result, err := m.ApplyReclassification(ctx, identity, plan)
	if err != nil || result[0].Status != "applied" {
		t.Fatalf("apply: %#v %v", result, err)
	}
	result, err = m.ApplyReclassification(ctx, identity, plan)
	if err != nil || result[0].Status != "already_applied" {
		t.Fatalf("repeat: %#v %v", result, err)
	}
	restore, err := m.RestorationPlan(ctx, identity, plan.RunID, plan.PracticeID, plan.LocationIDs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ChangeTaskCategory(ctx, work.ChangeTaskCategoryCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: 2, Category: work.TaskCategoryOther}); err != nil {
		t.Fatal(err)
	}
	result, err = m.ApplyReclassification(ctx, identity, restore)
	if err != nil || result[0].Status != "skipped_changed" {
		t.Fatalf("restore overwrote staff: %#v %v", result, err)
	}
	preserved, err := m.ReadTask(ctx, identity, task.ID)
	if err != nil || preserved.State != work.TaskOpen || !preserved.CreatedAt.Equal(task.CreatedAt) || preserved.SourceMessage != task.SourceMessage {
		t.Fatalf("preservation: %#v %v", preserved, err)
	}
}

func TestReclassificationPlanUsesRequestContextAcrossAuthorizationSubjects(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES($1,$2)`, identity.Email, identity.Subject); err != nil {
		t.Fatal(err)
	}
	m := work.New(pool, a, func() time.Time { return now })
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
	plan, err := m.PlanReclassification(ctx, identity, "classification-plan", auth.Practice.ID, []string{auth.Locations[0].ID})
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

func TestGroupMetadataCommandsDenyUnrelatedStaffWithoutSideEffects(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	ctx := context.Background()
	m := work.New(pool, a, func() time.Time { return now })
	callID := insertCall(t, pool, auth, now)
	task := ensureCallFollowUp(t, pool, m, work.EnsureCallFollowUpCommand{CallID: callID, PracticeID: auth.Practice.ID, LocationID: auth.Locations[0].ID, Phone: "+15555550100", Reason: "Synthetic request", Creator: auth.Actor})
	outsider := access.Identity{Subject: "unrelated-staff", Email: "unrelated@synthetic.test", EmailVerified: true}
	_, err := m.ChangeTaskCategory(ctx, work.ChangeTaskCategoryCommand{Identity: outsider, TaskID: task.ID, ExpectedVersion: task.Version, Category: work.TaskCategoryPreOp})
	if !errors.Is(err, work.ErrDenied) {
		t.Fatalf("move denied: %v", err)
	}
	_, err = m.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{Identity: outsider, TaskID: task.ID, Members: []work.ReviewedTask{{ID: task.ID, ExpectedVersion: task.Version}}})
	if !errors.Is(err, work.ErrDenied) {
		t.Fatalf("group denied: %v", err)
	}
	current, err := m.ReadTask(ctx, identity, task.ID)
	if err != nil || current.Version != task.Version || current.State != work.TaskOpen {
		t.Fatalf("denied command changed Task: %#v %v", current, err)
	}
}

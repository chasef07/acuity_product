package work_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"os"
	"testing"
	"time"
)

func TestStaffMovesTaskWithoutChangingIngestionEvidence(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	m := work.New(pool, a, func() time.Time { return now })
	command := work.CreateAITaskCommand{Service: access.ServiceIdentity{Subject: "synthetic-agent", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}, OfficeKey: "spring-hill", OfficePhone: "+12025550100", SourceCallID: "synthetic-optical-call", IdempotencyKey: "expedite-glasses", Phone: "+12025550123", Summary: "Expedite prescription", Message: "Please expedite sending my glasses prescription copy.", Category: work.TaskCategoryDocumentation, Urgency: work.TaskUrgencyNormal}
	original, _, err := m.CreateAITask(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := m.ChangeTaskCategory(context.Background(), work.ChangeTaskCategoryCommand{Identity: identity, TaskID: original.ID, ExpectedVersion: original.Version, Category: work.TaskCategoryOptical})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Category != work.TaskCategoryOptical || moved.Version != 2 || moved.ID != original.ID || !moved.CreatedAt.Equal(original.CreatedAt) || moved.SourceMessage != original.SourceMessage {
		t.Fatalf("moved Task lost source: %#v", moved)
	}
	replay, status, err := m.CreateAITask(context.Background(), command)
	if err != nil || status != work.TaskDuplicate || replay.ID != original.ID || replay.Category != work.TaskCategoryOptical {
		t.Fatalf("replay reset classification: %#v %s %v", replay, status, err)
	}
	_, err = m.ChangeTaskCategory(context.Background(), work.ChangeTaskCategoryCommand{Identity: identity, TaskID: original.ID, ExpectedVersion: 1, Category: work.TaskCategoryMedication})
	if !errors.Is(err, work.ErrConflict) {
		t.Fatalf("stale move: %v", err)
	}
	for _, category := range []work.TaskCategory{"insurance", "pre_op", "post_op", "billing"} {
		command.Category = category
		command.IdempotencyKey = string(category)
		command.Summary = string(category)
		task, _, err := m.CreateAITask(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		want := category
		if category == "billing" {
			want = work.TaskCategoryOther
		}
		if task.Category != want {
			t.Fatalf("%s mapped to %s", category, task.Category)
		}
	}
}

func TestKnowledgeFeedbackPersistsAcrossCompletion(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	m := work.New(pool, a, func() time.Time { return now })
	callID := insertCall(t, pool, auth, now)
	task := ensureCallFollowUp(t, pool, m, work.EnsureCallFollowUpCommand{CallID: callID, PracticeID: auth.Practice.ID, LocationID: auth.Locations[0].ID, Phone: "+15555550100", Reason: "Which records are needed?", Creator: auth.Actor})
	flagged, err := m.SetKnowledgeFeedback(context.Background(), work.KnowledgeFeedbackCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: task.Version, Flagged: true})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := m.CompleteTask(context.Background(), work.CompleteTaskCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: flagged.Version})
	if err != nil {
		t.Fatal(err)
	}
	edited, err := m.SetKnowledgeFeedback(context.Background(), work.KnowledgeFeedbackCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: completed.Version, Flagged: true, SuggestedAnswer: "Bring the records requested by the office."})
	if err != nil {
		t.Fatal(err)
	}
	read, err := m.ReadTask(context.Background(), identity, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !read.KnowledgeFlagged || read.SuggestedAnswer != edited.SuggestedAnswer || read.State != work.TaskCompleted || read.Category != task.Category {
		t.Fatalf("feedback altered work: %#v", read)
	}
	_, err = m.SetKnowledgeFeedback(context.Background(), work.KnowledgeFeedbackCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: 1, Flagged: false})
	if !errors.Is(err, work.ErrConflict) {
		t.Fatalf("stale feedback: %v", err)
	}
}

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

func TestRelatedTaskGroupsKeepRecoverySeparate(t *testing.T) {
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
	reads := workspace.New(pool, a)
	for _, folder := range []work.TaskFolder{"", work.TaskFolderWork, work.TaskFolderMissedCalls} {
		page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Grouped: true, Folder: folder})
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if folder == "" {
			want = 2
		}
		if len(page.Items) != want {
			t.Errorf("folder %q: got %d groups, want %d", folder, len(page.Items), want)
		}
		for _, row := range page.Items {
			if len(row.GroupMembers) != 1 || row.GroupMembers[0].ID != row.ID {
				t.Errorf("folder %q mixed ordinary and recovery members", folder)
			}
		}
	}
	// A grouped page carries summaries; selecting a Task still loads every source.
	for _, ordering := range []work.TaskOrdering{work.TaskOrderingTime, work.TaskOrderingPriority, work.TaskOrderingRecent} {
		command := workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Grouped: true, Ordering: ordering, Limit: 1}
		seen := map[string]bool{}
		for {
			page, err := reads.QueryTasks(ctx, command)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 || len(page.Items[0].GroupMembers) != 1 {
				t.Fatalf("%s lost paginated group membership: %+v", ordering, page)
			}
			member := page.Items[0].GroupMembers[0]
			wantInteractions := 0
			if member.ID == recovery.ID {
				wantInteractions = 1
			}
			if seen[member.ID] || member.RelatedInteractionCount != wantInteractions {
				t.Fatalf("%s repeated a group or lost its source count: %+v", ordering, member)
			}
			seen[member.ID] = true
			detail, err := reads.ReadTask(ctx, identity, member.ID)
			if err != nil || len(detail.Interactions) != wantInteractions || detail.RelatedInteractionCount != member.RelatedInteractionCount || detail.CallID != member.CallID {
				t.Fatalf("%s lost source detail: %+v, %v", ordering, detail, err)
			}
			if page.NextCursor == "" {
				break
			}
			command.Cursor = page.NextCursor
		}
		if !seen[ordinary.ID] || !seen[recovery.ID] {
			t.Fatalf("%s skipped a group: %+v", ordering, seen)
		}
	}
	_, err = m.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{Identity: identity, TaskID: ordinary.ID, Members: []work.ReviewedTask{{ID: ordinary.ID, ExpectedVersion: ordinary.Version}, {ID: recovery.ID, ExpectedVersion: recovery.Version}}})
	if !errors.Is(err, work.ErrConflict) {
		t.Fatalf("mixed group resolution should conflict: %v", err)
	}
	_, err = m.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{Identity: identity, TaskID: ordinary.ID, Members: []work.ReviewedTask{{ID: ordinary.ID, ExpectedVersion: ordinary.Version}}})
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := m.ReadTask(ctx, identity, recovery.ID)
	if err != nil || remaining.State != work.TaskOpen || remaining.Version != recovery.Version {
		t.Fatalf("ordinary group resolution changed recovery: %v", err)
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

func TestResponsibilityRosterPreservesElevenOpticalOwnersAndPendingAccounts(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := access.New(pool, nil)
	raw, err := os.ReadFile("../../../config/production-provisioning.json")
	if err != nil {
		t.Fatal(err)
	}
	var provisioning access.Provisioning
	if err := json.Unmarshal(raw, &provisioning); err != nil {
		t.Fatal(err)
	}
	provisioning.Environment = "test"
	if _, err := a.Provision(ctx, provisioning); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../../config/task-responsibilities.json")
	if err != nil {
		t.Fatal(err)
	}
	var roster work.ResponsibilityProvision
	if err := json.Unmarshal(raw, &roster); err != nil {
		t.Fatal(err)
	}
	m := work.New(pool, a, nil)
	report, err := m.ProvisionResponsibilities(ctx, roster)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unmatched) != 8 {
		t.Fatalf("unmatched configured accounts: %#v", report)
	}
	cases := map[string][]string{
		"madelyn@abitaeye.com": {"optical"}, "everth@abitaeye.com": {"appointments", "optical", "other"}, "denise@abitaeye.com": {"optical"}, "sashao@abitaeye.com": {"optical"}, "optical@abitaeye.com": {"optical"}, "doraloptical@abitaeye.com": {"optical"}, "ari@abitaeye.com": {"optical"}, "mobileoptical@abitaeye.com": {"optical"}, "justin@abitaeye.com": {"optical"}, "sweetwateroptical@abitaeye.com": {"optical"}, "abel@abitaeye.com": {"optical"}, "gustavo@abitaeye.com": {"appointments", "other"}, "abita.insurance@abitaeye.com": {"appointments", "other"}, "doralreception@abitaeye.com": {"appointments", "other"}, "aileen@abitaeye.com": {"pre_op", "post_op"}, "jianna@abitaeye.com": {"medication"},
	}
	for email, want := range cases {
		identity := access.Identity{Subject: "roster-" + email, Email: email, EmailVerified: true}
		auth := testaccess.Activate(t, a, identity)
		service := access.ServiceIdentity{Subject: "roster-source", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}
		for _, category := range []work.TaskCategory{"appointments", "documentation", "medication", "optical", "referrals", "other", "insurance", "pre_op", "post_op"} {
			_, _, err := m.CreateAITask(ctx, work.CreateAITaskCommand{Service: service, OfficeKey: "sweetwater", OfficePhone: "+12025550100", SourceCallID: string(category), IdempotencyKey: string(category), Phone: "+12025550123", Summary: string(category), Message: "Synthetic responsibility request", Category: category, Urgency: work.TaskUrgencyNormal})
			if err != nil {
				t.Fatal(err)
			}
		}
		var locationID string
		for _, l := range auth.Locations {
			if l.Name == "Sweetwater" {
				locationID = l.ID
			}
		}
		if locationID == "" {
			t.Fatalf("%s has no expected Sweetwater scope", email)
		}
		page, err := workspace.New(pool, a).QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, LocationID: locationID, Responsibility: "mine"})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, task := range page.Items {
			got[string(task.Category)] = true
		}
		if len(got) != len(want) {
			t.Errorf("%s got %#v, want %#v", email, got, want)
		}
		for _, category := range want {
			if !got[category] {
				t.Errorf("%s missing %s", email, category)
			}
		}
	}
}

func TestKnowledgeFilterFindsOpenAndCompletedRecoveryTasks(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	ctx := context.Background()
	m := work.New(pool, a, func() time.Time { return now })
	callID := insertCall(t, pool, auth, now)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	task, err := m.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{CallID: callID, PracticeID: auth.Practice.ID, LocationID: auth.Locations[0].ID, Phone: "+15555550100", Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: now})
	if err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	task, err = m.SetKnowledgeFeedback(ctx, work.KnowledgeFeedbackCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: task.Version, Flagged: true})
	if err != nil {
		t.Fatal(err)
	}
	reads := workspace.New(pool, a)
	for _, state := range []work.TaskState{work.TaskOpen, work.TaskCompleted} {
		if state == work.TaskCompleted {
			task, err = m.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: task.Version})
			if err != nil {
				t.Fatal(err)
			}
		}
		page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, State: state, KnowledgeFlagged: true, Grouped: true, Responsibility: "mine"})
		if err != nil {
			t.Fatal(err)
		}
		wantRecovery := 0
		if state == work.TaskOpen {
			wantRecovery = 1
		}
		if page.Counts.MissedCalls != wantRecovery {
			t.Errorf("%s separate recovery count=%d, want %d", state, page.Counts.MissedCalls, wantRecovery)
		}
		if len(page.Items) != 1 || page.Items[0].ID != task.ID || page.Counts.Tasks != 1 {
			t.Fatalf("%s flagged recovery missing: %#v", state, page)
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
	_, err = m.SetKnowledgeFeedback(ctx, work.KnowledgeFeedbackCommand{Identity: outsider, TaskID: task.ID, ExpectedVersion: task.Version, Flagged: true})
	if !errors.Is(err, work.ErrDenied) {
		t.Fatalf("feedback denied: %v", err)
	}
	_, err = m.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{Identity: outsider, TaskID: task.ID, Members: []work.ReviewedTask{{ID: task.ID, ExpectedVersion: task.Version}}})
	if !errors.Is(err, work.ErrDenied) {
		t.Fatalf("group denied: %v", err)
	}
	current, err := m.ReadTask(ctx, identity, task.ID)
	if err != nil || current.Version != task.Version || current.State != work.TaskOpen || current.KnowledgeFlagged {
		t.Fatalf("denied command changed Task: %#v %v", current, err)
	}
}

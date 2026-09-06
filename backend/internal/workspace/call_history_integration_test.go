package workspace_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func TestPhoneHistoryKeepsCallAndFollowUpTogetherBeforePagination(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "call-history-test",
		Practices: []access.PracticeProvision{{Key: "history", Name: "History", Locations: []access.LocationProvision{{Key: "office", Name: "Office", AbitaOfficeKeys: []string{"history-office"}}, {Key: "hidden", Name: "Hidden office"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: "staff@history.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"office"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := access.Identity{Subject: "history-staff", Email: "staff@history.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, identity)
	locationID := authorization.Locations[0].ID
	phone := "+12025550123"
	callID := insertRecoveryCall(t, pool, authorization, locationID, phone, now)
	var sourceCallID string
	if err := pool.QueryRow(ctx, `SELECT handoff.source_call_id FROM human_calling_calls call JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id WHERE call.id = $1`, callID).Scan(&sourceCallID); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO ai_interactions (service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, appointment_outcome, lifecycle_stage) VALUES ('history-ai', $1, $2, $3, $4, '+12025550100', $5, $6, 'ESCALATED', 'BOOKING', 3)`, authorization.Practice.ID, locationID, sourceCallID, phone, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	writes := work.New(pool, accessModule, func() time.Time { return now.Add(time.Minute) })
	task, _, err := writes.CreateAITask(ctx, work.CreateAITaskCommand{
		Service:   access.ServiceIdentity{Subject: "history-ai", PracticeID: authorization.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}},
		OfficeKey: "history-office", OfficePhone: "+12025550100", SourceCallID: sourceCallID, IdempotencyKey: "history-task", Phone: phone, Summary: "Confirm instructions", Message: "Caller needs instructions after booking", Category: work.TaskCategoryDocumentation, Urgency: work.TaskUrgencyNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	reads := workspace.New(pool, accessModule)
	page, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Type != "CALL_HISTORY" || page.NextCursor != "" {
		t.Fatalf("one linked call should occupy one complete page item, got types/count/cursor: %#v", page)
	}
	if len(page.Items[0].Entries) != 3 {
		t.Fatalf("call history should retain AI, transfer, and Task evidence, got %d", len(page.Items[0].Entries))
	}
	flat, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone, Ungrouped: true})
	if err != nil || len(flat.Items) != 3 {
		t.Fatalf("legacy flat history lost evidence: %v, %v", flat, err)
	}
	for _, entry := range flat.Items {
		if entry.Type == "CALL_HISTORY" {
			t.Fatal("grouped response leaked into flat contract")
		}
	}

	originalID := page.Items[0].ID
	var booking, transfer, followUp bool
	for _, entry := range page.Items[0].Entries {
		booking = booking || entry.AIInteraction.AppointmentOutcome == "BOOKING"
		transfer = transfer || entry.Call.ID == callID
		followUp = followUp || entry.Task.ID == task.ID
	}
	if !booking || !transfer || !followUp {
		t.Fatal("group omitted a call outcome or linked Task")
	}

	// A separate call from the same number remains a separate history item.
	olderTime := now.Add(-time.Hour)
	olderCallID := insertRecoveryCall(t, pool, authorization, locationID, phone, olderTime)
	first, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != originalID || first.NextCursor == "" {
		t.Fatalf("first whole-call page: %v, %v", first, err)
	}
	older, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone, Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(older.Items) != 1 || older.NextCursor != "" || older.Items[0].Entries[0].Call.ID != olderCallID {
		t.Fatalf("older whole-call page: %v, %v", older, err)
	}
	olderID := older.Items[0].ID

	// A late AI closeout enriches the existing transfer instead of adding a row.
	var olderSource string
	if err := pool.QueryRow(ctx, `SELECT handoff.source_call_id FROM human_calling_calls call JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id WHERE call.id = $1`, olderCallID).Scan(&olderSource); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interactions (service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, lifecycle_stage) VALUES ('history-ai', $1, $2, $3, $4, '+12025550100', $5, $6, 'ESCALATED', 3)`, authorization.Practice.ID, locationID, olderSource, phone, olderTime.Add(-time.Minute), olderTime); err != nil {
		t.Fatal(err)
	}
	older, err = reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone, Limit: 1, Cursor: first.NextCursor})
	if err != nil || older.Items[0].ID != olderID || len(older.Items[0].Entries) != 2 {
		t.Fatalf("late closeout changed identity or lost evidence: %v, %v", older, err)
	}

	// Completing the Task is still a later Activity, with current state also
	// available beside the source call. Reading history does not complete work.
	completed, err := writes.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: task.Version})
	if err != nil {
		t.Fatal(err)
	}
	all, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone})
	if err != nil || len(all.Items) != 3 {
		t.Fatalf("completion history count: %v, %v", all, err)
	}
	if all.Items[2].TaskActivity != "TASK_COMPLETED" || all.Items[2].Task.ID != completed.ID {
		t.Fatal("completion was swallowed into the old call")
	}
	if all.Items[1].ID != originalID {
		t.Fatal("Task completion changed call identity")
	}

	// Other Locations sharing a number cannot contribute evidence to this view.
	var hiddenLocationID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id = $1 AND name = 'Hidden office'`, authorization.Practice.ID).Scan(&hiddenLocationID); err != nil {
		t.Fatal(err)
	}
	insertRecoveryCall(t, pool, authorization, hiddenLocationID, phone, now.Add(time.Hour))
	scoped, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone})
	if err != nil || len(scoped.Items) != 3 {
		t.Fatalf("unauthorized Location changed history: %v, %v", scoped, err)
	}

	// Recovery attachments may link several distinct calls to one Task. The
	// Task stays reachable from each call without adding bookkeeping rows.
	recoveryPhone := "+12025550124"
	recoveryCall := insertRecoveryCall(t, pool, authorization, locationID, recoveryPhone, now)
	voicemailCall := insertRecoveryCall(t, pool, authorization, locationID, recoveryPhone, now.Add(time.Minute))
	var recoveryTaskID string
	for index, id := range []string{recoveryCall, voicemailCall} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		outcome := work.RecoveryOutcomeMissedCall
		if index == 1 {
			outcome = work.RecoveryOutcomeVoicemail
		}
		recovery, err := writes.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{
			CallID: id, PracticeID: authorization.Practice.ID, LocationID: locationID,
			Phone: recoveryPhone, CallerName: "Synthetic caller", Outcome: outcome,
			OccurredAt: now.Add(time.Duration(index) * time.Minute),
		})
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if index == 1 && recovery.ID != recoveryTaskID {
			t.Fatal("fixture did not attach both calls to one recovery Task")
		}
		recoveryTaskID = recovery.ID
	}
	recoveryHistory, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: recoveryPhone})
	if err != nil || len(recoveryHistory.Items) != 2 {
		t.Fatalf("recovery history added bookkeeping rows: %v, %v", recoveryHistory, err)
	}
	for _, history := range recoveryHistory.Items {
		linked := false
		for _, entry := range history.Entries {
			linked = linked || entry.Task.ID == recoveryTaskID
		}
		if !linked {
			t.Fatal("recovery Task link missing from a related call")
		}
	}

}

package workspace_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func TestPhoneHistoryNamesTheStaffMemberWhoPlacedAnUnansweredCallback(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "callback-attribution-test",
		Practices: []access.PracticeProvision{{
			Key: "callback", Name: "Callback Practice",
			Locations: []access.LocationProvision{{Key: "office", Name: "Office"}},
			AccessGrants: []access.AccessGrantProvision{
				{Key: "viewer", Email: "viewer@callback.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"office"}},
				{Key: "caller", Email: "caller@callback.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"office"}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer := access.Identity{Subject: "callback-viewer", Email: "viewer@callback.test", EmailVerified: true}
	caller := access.Identity{Subject: "callback-caller", Email: "caller@callback.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, viewer)
	testaccess.Activate(t, accessModule, caller)
	locationID := authorization.Locations[0].ID
	phone := "+12025550188"

	missedAt := now.Add(-10 * time.Minute)
	missedCallID := insertRecoveryCall(t, pool, authorization, locationID, phone, missedAt)
	writes := work.New(pool, accessModule, func() time.Time { return now })
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	review, err := writes.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{
		CallID: missedCallID, PracticeID: authorization.Practice.ID, LocationID: locationID,
		Phone: phone, Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: missedAt,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	attemptedAt := now.Add(-5 * time.Minute)
	var callbackID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO human_calling_calls (
			practice_id, location_id, provider_termination, terminal_outcome, ended_at,
			direction, entry_point, destination_phone, outbound_caller_id,
			initiating_subject, outbound_idempotency_key, outbound_input_fingerprint,
			created_at, updated_at
		) VALUES (
			$1, $2, 'NO_ANSWER', 'UNANSWERED', $3,
			'OUTBOUND', 'STANDALONE', $4, '+12025550100',
			$5, 'callback-attempt', $6,
			$7, $3
		)
		RETURNING id::text
	`, authorization.Practice.ID, locationID, attemptedAt.Add(30*time.Second), phone,
		caller.Subject, make([]byte, 32), attemptedAt).Scan(&callbackID); err != nil {
		t.Fatalf("create unanswered callback attempt: %v", err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := writes.ResolveRecoveryTasks(ctx, tx, work.ResolveRecoveryTasksCommand{
		PracticeID: authorization.Practice.ID, Phone: phone, OccurredAt: attemptedAt,
		Kind: work.RecoveryResolutionCallbackAttempt, SourceID: callbackID,
	}); !errors.Is(err, work.ErrInvalidInput) {
		t.Fatalf("a callback attempt without its caller must be refused: %v", err)
	}
	if _, err := writes.ResolveRecoveryTasks(ctx, tx, work.ResolveRecoveryTasksCommand{
		PracticeID: authorization.Practice.ID, Phone: phone, OccurredAt: attemptedAt,
		Kind: work.RecoveryResolutionInboundCall, SourceID: callbackID, ActorSubject: caller.Subject,
	}); !errors.Is(err, work.ErrInvalidInput) {
		t.Fatalf("only callback attempts carry a caller: %v", err)
	}
	completed, err := writes.ResolveRecoveryTasks(ctx, tx, work.ResolveRecoveryTasksCommand{
		PracticeID: authorization.Practice.ID, Phone: phone, OccurredAt: attemptedAt,
		Kind: work.RecoveryResolutionCallbackAttempt, SourceID: callbackID, ActorSubject: caller.Subject,
	})
	if err != nil || completed != 1 {
		t.Fatalf("callback attempt completed %d reviews: %v", completed, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := workspace.New(pool, accessModule).QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{
		Identity: viewer, PracticeID: authorization.Practice.ID, Phone: phone,
	})
	if err != nil {
		t.Fatal(err)
	}
	var placedBy, answeredBy, outcome, missedPlacedBy string
	var completion *workspace.TimelineItem
	for index := range page.Items {
		item := page.Items[index]
		if item.TaskActivity == "TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT" {
			completion = &page.Items[index]
		}
		for _, entry := range item.Entries {
			switch entry.Call.ID {
			case callbackID:
				placedBy, answeredBy, outcome = entry.Call.PlacedByEmail, entry.Call.AnsweredByEmail, string(entry.Call.Outcome)
			case missedCallID:
				missedPlacedBy = entry.Call.PlacedByEmail
			}
		}
	}
	if placedBy != caller.Email || answeredBy != "" || outcome != "UNANSWERED" {
		t.Fatalf("unanswered callback attribution: placedBy=%q answeredBy=%q outcome=%q", placedBy, answeredBy, outcome)
	}
	if missedPlacedBy != "" {
		t.Fatalf("an inbound call must not claim a placing staff member: %q", missedPlacedBy)
	}
	if completion == nil || completion.Task.ID != review.ID {
		t.Fatalf("callback-attempt completion is missing from the number's history: %#v", page.Items)
	}
	if completion.Task.CompletedBy == nil || completion.Task.CompletedBy.Kind != access.ActorService {
		t.Fatalf("automatic completion must keep its service actor: %#v", completion.Task.CompletedBy)
	}
	details := completion.TaskActivityDetails
	if details["callerEmail"] != caller.Email || details["callerSubject"] != caller.Subject || details["callId"] != callbackID {
		t.Fatalf("callback-attempt completion must credit its caller: %#v", details)
	}
	if actor := completion.TaskActivityActor; actor == nil || actor.Kind != access.ActorService ||
		actor.Subject != "work-recovery-resolution" || actor.Email != "" {
		t.Fatalf("automatic completion Activity must read as automation: %#v", actor)
	}

	lateAt := now.Add(-7 * time.Minute)
	lateCallID := insertRecoveryCall(t, pool, authorization, locationID, phone, lateAt)
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	late, err := writes.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{
		CallID: lateCallID, PracticeID: authorization.Practice.ID, LocationID: locationID,
		Phone: phone, Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: lateAt,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var lateCaller string
	if err := pool.QueryRow(ctx, `
		SELECT details->>'callerSubject' FROM work_task_activities
		WHERE task_id = $1 AND kind = 'TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT'
	`, late.ID).Scan(&lateCaller); err != nil || late.State != work.TaskCompleted || lateCaller != caller.Subject {
		t.Fatalf("late evidence cleared by the checkpoint must credit the same caller: state=%s caller=%q err=%v", late.State, lateCaller, err)
	}

	reopenedReview, err := writes.ReopenTask(ctx, work.ReopenTaskCommand{
		Identity: caller, TaskID: review.ID, ExpectedVersion: completion.Task.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	afterReopen, err := workspace.New(pool, accessModule).QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{
		Identity: viewer, PracticeID: authorization.Practice.ID, Phone: phone,
	})
	if err != nil {
		t.Fatal(err)
	}
	var reopenActor *work.ActorSnapshot
	for _, item := range afterReopen.Items {
		if item.TaskActivity == "TASK_REOPENED" && item.Task.ID == reopenedReview.ID {
			reopenActor = item.TaskActivityActor
		}
	}
	if reopenActor == nil || reopenActor.Kind != access.ActorHuman || reopenActor.Email != caller.Email || reopenActor.Subject != caller.Subject {
		t.Fatalf("a staff reopen must name who reopened the Task: %#v", reopenActor)
	}
}

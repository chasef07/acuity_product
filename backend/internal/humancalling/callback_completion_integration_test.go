package humancalling_test

import (
	"context"
	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"testing"
	"time"
)

func TestCompletedCallbackAttemptClosesOnlyOlderRecovery(t *testing.T) {
	for _, scenario := range []string{"connected", "voicemail", "no_answer", "busy", "setup_failed", "newer_evidence"} {
		for _, outcome := range []work.RecoveryOutcome{work.RecoveryOutcomeMissedCall, work.RecoveryOutcomeVoicemail} {
			t.Run(scenario+"/"+string(outcome), func(t *testing.T) {
				ctx := context.Background()
				now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
				provider := &recordingProvider{}
				fixture := newOutboundEndFixture(t, "callback-completion", now, provider)
				calling := humancalling.New(fixture.pool, access.New(fixture.pool, func() time.Time { return now }), provider, humancalling.Config{
					StaffSIPDomain: "sip.telnyx.com", RingWindowDuration: 20 * time.Second,
					CallControlID: "staff-call-control-connection", CredentialConnectionID: "staff-credential-connection",
				}, func() time.Time { return now })
				call := fixture.startCall(t, "callback-completion-call")

				workModule := work.New(fixture.pool, access.New(fixture.pool, func() time.Time { return now }), func() time.Time { return now })
				var sourceID string
				if err := fixture.pool.QueryRow(ctx, `INSERT INTO human_calling_calls(practice_id,location_id,direction,entry_point,terminal_outcome,caller_phone,ended_at,created_at,updated_at) VALUES($1,$2,'INBOUND','STANDALONE','VOICEMAIL','+15555550123',$3,$3,$3) RETURNING id::text`, fixture.authorization.Practice.ID, fixture.authorization.Locations[0].ID, now.Add(-time.Minute)).Scan(&sourceID); err != nil {
					t.Fatal(err)
				}
				tx, err := fixture.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				task, err := workModule.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{CallID: sourceID, PracticeID: fixture.authorization.Practice.ID, LocationID: fixture.authorization.Locations[0].ID, Phone: "+15555550123", Outcome: outcome, OccurredAt: now.Add(-time.Minute)})
				if err != nil {
					t.Fatal(err)
				}
				if err := workModule.EnsureAppointmentReview(ctx, tx, sourceID, task.PracticeID, task.LocationID, task.Phone, "synthetic-appointment", "BOOKED", "Verify synthetic appointment.", now); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				assertState := func(want work.TaskState) {
					t.Helper()
					current, err := workModule.ReadTask(ctx, fixture.identity, task.ID)
					if err != nil || current.State != want {
						t.Fatalf("state=%s want=%s err=%v", current.State, want, err)
					}
				}
				assertState(work.TaskOpen)
				processAllCommands(t, calling)
				staffDial := provider.last(humancalling.CommandDialOutboundStaff)
				staff := humancalling.ProviderFact{
					EventID: "staff-initiated", Type: humancalling.FactCallInitiated,
					OccurredAt: now, ConnectionID: "staff-call-control-connection",
					CallControlID: "staff-control", CallLegID: "staff-leg", CallSessionID: "staff-session",
					ClientState: staffDial.Payload["client_state"].(string),
				}
				if err := calling.ApplyProviderFact(ctx, staff); err != nil {
					t.Fatal(err)
				}
				if scenario == "setup_failed" {
					staff.EventID, staff.Type = "staff-failed", humancalling.FactCallHangup
					staff.HangupCause = "no_answer"
					if err := calling.ApplyProviderFact(ctx, staff); err != nil {
						t.Fatal(err)
					}
					assertState(work.TaskOpen)
					return
				}
				staff.EventID, staff.Type = "staff-answered", humancalling.FactCallAnswered
				staff.OccurredAt = now
				if err := calling.ApplyProviderFact(ctx, staff); err != nil {
					t.Fatal(err)
				}
				state, err := calling.ReadCallingState(ctx, fixture.identity)
				if err != nil || len(state.Ringing) != 1 {
					t.Fatalf("read media token: ringing=%d err=%v", len(state.Ringing), err)
				}
				if _, err := calling.ConfirmOutboundMedia(ctx, humancalling.ConfirmOutboundMediaCommand{
					Identity: fixture.identity, SessionID: fixture.sessionID, CallID: call.ID,
					MediaToken: state.Ringing[0].MediaToken,
				}); err != nil {
					t.Fatal(err)
				}
				processAllCommands(t, calling)
				dial := provider.last(humancalling.CommandDialOutboundDestination)
				destination := humancalling.ProviderFact{
					EventID: "destination-initiated", Type: humancalling.FactCallInitiated,
					OccurredAt: now.Add(3 * time.Second), ConnectionID: "staff-call-control-connection",
					CallControlID: "destination-control", CallLegID: "destination-leg", CallSessionID: "destination-session",
					ClientState: dial.Payload["client_state"].(string),
				}
				if err := calling.ApplyProviderFact(ctx, destination); err != nil {
					t.Fatal(err)
				}
				assertState(work.TaskOpen) // dialing never completes work
				if scenario != "no_answer" && scenario != "busy" {
					processAllCommands(t, calling)
					bridge := provider.last(humancalling.CommandBridge)
					staffBridged := staff
					staffBridged.EventID, staffBridged.Type = "staff-bridged", humancalling.FactCallBridged
					staffBridged.ClientState = bridge.Payload["client_state"].(string)
					if err := calling.ApplyProviderFact(ctx, staffBridged); err != nil {
						t.Fatal(err)
					}
					destination.EventID, destination.Type = "destination-answered", humancalling.FactCallAnswered
					destination.OccurredAt = now.Add(4 * time.Second)
					if err := calling.ApplyProviderFact(ctx, destination); err != nil {
						t.Fatal(err)
					}
					destination.EventID, destination.Type = "destination-bridged", humancalling.FactCallBridged
					destination.OccurredAt = now.Add(5 * time.Second)
					if err := calling.ApplyProviderFact(ctx, destination); err != nil {
						t.Fatal(err)
					}
					// Human and voicemail answers have the same media evidence.
					assertState(work.TaskOpen)
				}
				if scenario == "newer_evidence" {
					var newerCallID string
					at := now.Add(6 * time.Second)
					if err := fixture.pool.QueryRow(ctx, `INSERT INTO human_calling_calls(practice_id,location_id,direction,entry_point,terminal_outcome,caller_phone,ended_at,created_at,updated_at) VALUES($1,$2,'INBOUND','STANDALONE','VOICEMAIL','+15555550123',$3,$3,$3) RETURNING id::text`, fixture.authorization.Practice.ID, fixture.authorization.Locations[0].ID, at).Scan(&newerCallID); err != nil {
						t.Fatal(err)
					}
					tx, err := fixture.pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(ctx)
					newer, err := workModule.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{CallID: newerCallID, PracticeID: task.PracticeID, LocationID: task.LocationID, Phone: task.Phone, Outcome: outcome, OccurredAt: at})
					if err != nil {
						t.Fatal(err)
					}
					if newer.ID != task.ID {
						t.Fatal("new evidence must extend the current review")
					}
					if err = tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
				}
				destination.EventID, destination.Type = "destination-ended", humancalling.FactCallHangup
				destination.OccurredAt = now.Add(10 * time.Second)
				destination.HangupCause = "normal_clearing"
				if scenario == "no_answer" || scenario == "busy" {
					destination.HangupCause = scenario
				}
				now = destination.OccurredAt.Add(time.Second)
				if err := calling.ApplyProviderFact(ctx, destination); err != nil {
					t.Fatal(err)
				}
				want := work.TaskCompleted
				if scenario == "newer_evidence" {
					want = work.TaskOpen
				}
				assertState(want)
				if err := calling.ApplyProviderFact(ctx, destination); err != nil {
					t.Fatal(err)
				}
				assertState(want)
				var appointmentOpen int
				if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM work_tasks WHERE origin='APPOINTMENT_REVIEW' AND state='OPEN'`).Scan(&appointmentOpen); err != nil || appointmentOpen != 1 {
					t.Fatalf("callback cleared unrelated appointment: %d %v", appointmentOpen, err)
				}
				if want == work.TaskCompleted {
					completed, err := workModule.ReadTask(ctx, fixture.identity, task.ID)
					if err != nil || completed.CompletedAt == nil || !completed.CompletedAt.Equal(now) {
						t.Fatalf("completion should record completed-at time, not attempt start: %v %v", completed.CompletedAt, err)
					}
					var activities int
					if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM work_task_activities WHERE task_id=$1 AND kind='TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT'`, task.ID).Scan(&activities); err != nil || activities != 1 {
						t.Fatalf("callback attempt activity=%d err=%v", activities, err)
					}
				}

			})
		}
	}
}

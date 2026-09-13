package humancalling_test

import (
	"context"
	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"testing"
	"time"
)

func TestConnectedCallbackCompletesRecoveryOnlyAfterBothLegsBridge(t *testing.T) {
	for _, outcome := range []work.RecoveryOutcome{work.RecoveryOutcomeMissedCall, work.RecoveryOutcomeVoicemail} {
		t.Run(string(outcome), func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
			provider := &recordingProvider{}
			fixture := newOutboundEndFixture(t, "callback-completion", now, provider)
			calling := fixture.calling
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
			processAllCommands(t, calling)
			bridge := provider.last(humancalling.CommandBridge)
			playback := staff
			playback.EventID, playback.Type = "ringtone-started", humancalling.FactPlaybackStarted
			playback.ClientState = bridge.Payload["client_state"].(string)
			if err := calling.ApplyProviderFact(ctx, playback); err != nil {
				t.Fatal(err)
			}
			staffBridged := playback
			staffBridged.EventID, staffBridged.Type = "staff-bridged", humancalling.FactCallBridged
			if err := calling.ApplyProviderFact(ctx, staffBridged); err != nil {
				t.Fatal(err)
			}

			assertState(work.TaskOpen) // staff media and destination ringing do not complete work
			destination.EventID, destination.Type = "destination-answered", humancalling.FactCallAnswered
			destination.OccurredAt = now.Add(4 * time.Second)
			if err := calling.ApplyProviderFact(ctx, destination); err != nil {
				t.Fatal(err)
			}
			assertState(work.TaskOpen)
			destination.EventID, destination.Type = "destination-bridged", humancalling.FactCallBridged
			destination.OccurredAt = now.Add(5 * time.Second)
			if err := calling.ApplyProviderFact(ctx, destination); err != nil {
				t.Fatal(err)
			}
			assertState(work.TaskCompleted)
			if err := calling.ApplyProviderFact(ctx, destination); err != nil {
				t.Fatal(err)
			}
			assertState(work.TaskCompleted)

		})
	}
}

package humancalling_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestVoicemailGreetingSwitchesToAfterHoursGreetingFromSixPMToEightAMEastern(t *testing.T) {
	const day = "Please leave a message."
	const afterHours = "Please leave a message. If this is an emergency, please call 305-741-0830."
	for _, test := range []struct {
		name       string
		now        time.Time
		afterHours string
		want       string
	}{
		{"weekday afternoon", time.Date(2026, time.October, 15, 21, 59, 0, 0, time.UTC), afterHours, day},
		{"six PM", time.Date(2026, time.October, 15, 22, 0, 0, 0, time.UTC), afterHours, afterHours},
		{"before eight AM", time.Date(2026, time.October, 16, 11, 59, 0, 0, time.UTC), afterHours, afterHours},
		{"eight AM", time.Date(2026, time.October, 16, 12, 0, 0, 0, time.UTC), afterHours, day},
		{"winter evening", time.Date(2026, time.December, 15, 23, 0, 0, 0, time.UTC), afterHours, afterHours},
		{"evening without after-hours greeting", time.Date(2026, time.October, 15, 23, 0, 0, 0, time.UTC), "", day},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
				CallControlID: "greeting-staff-control", CallLegID: "greeting-staff-leg",
			}}}
			pool, calling, caller, _ := prepareInboundFanout(
				t, test.now, "after-hours-greeting", provider, 1,
			)
			var practiceKey, locationKey string
			if err := pool.QueryRow(context.Background(), `
				SELECT practice.provisioning_key, location.provisioning_key
				FROM human_calling_calls call
				JOIN access_practices practice ON practice.id = call.practice_id
				JOIN access_locations location ON location.id = call.location_id
			`).Scan(&practiceKey, &locationKey); err != nil {
				t.Fatal(err)
			}
			if err := calling.ProvisionLocationVoices(context.Background(), []humancalling.LocationVoiceProvision{{
				PracticeKey: practiceKey, LocationKey: locationKey,
				Number: "+13055550100", Enabled: true,
				VoicemailGreeting:           day,
				AfterHoursVoicemailGreeting: test.afterHours,
			}}); err != nil {
				t.Fatal(err)
			}
			processAllCommands(t, calling)
			ring := provider.last(humancalling.CommandStartRingWindow)
			ringState, _ := ring.Payload["client_state"].(string)
			if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
				EventID: "after-hours-greeting-ring-completed", Type: humancalling.FactPlaybackEnded,
				OccurredAt: test.now.Add(20 * time.Second), CallControlID: caller.CallControlID,
				CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
				ClientState: ringState, PlaybackStatus: "completed",
			}); err != nil {
				t.Fatal(err)
			}
			processAllCommands(t, calling)
			speak := provider.last(humancalling.CommandSpeakVoicemail)
			if got := speak.Payload["payload"]; got != test.want {
				t.Fatalf("spoken greeting = %q, want %q", got, test.want)
			}
		})
	}
}

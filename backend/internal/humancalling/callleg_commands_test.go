package humancalling

import "testing"

func TestHangupTreatsAlreadyEndedTargetAsSent(t *testing.T) {
	module := &Module{}
	state, errorCode := module.providerCommandResult(
		ProviderCommand{Action: CommandHangupLeg},
		&ProviderError{
			SafeCode:     "TELNYX_CALL_ENDED",
			Definitive:   true,
			TargetAbsent: true,
		},
	)
	if state != "SENT" || errorCode != "" {
		t.Fatalf("ended-target Hangup result = %s %q, want SENT with no error", state, errorCode)
	}
}

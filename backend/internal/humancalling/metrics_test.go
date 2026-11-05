package humancalling

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
)

func TestProviderCommandMetricLabelsEveryAction(t *testing.T) {
	for action, label := range map[CommandAction]string{
		CommandAnswerCaller:            "answer_caller",
		CommandStartRingWindow:         "start_ring_window",
		CommandDialStaff:               "dial_staff",
		CommandBridge:                  "bridge",
		CommandTransferStaff:           "transfer_staff",
		CommandStopRingWindow:          "stop_ring_window",
		CommandHangupLeg:               "hangup_leg",
		CommandSpeakVoicemail:          "speak_voicemail",
		CommandStartVoicemailRecording: "start_voicemail_recording",
		CommandDialOutboundStaff:       "dial_outbound_staff",
		CommandDialOutboundDestination: "dial_outbound_destination",
		CommandCreateCredential:        "create_credential",
		CommandDisableCredential:       "disable_credential",
		CommandCreateJWT:               "create_jwt",
		CommandAction("UNKNOWN"):       "other",
	} {
		var output bytes.Buffer
		m := &Module{observer: observability.NewLogger(observability.RuntimeWorker, "synthetic-revision", slog.New(slog.NewJSONHandler(&output, nil)))}
		m.recordProviderCommand(ProviderCommand{Action: action}, "SENT", time.Time{}, time.Second)
		var entry map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
			t.Fatalf("%s metric: %v", action, err)
		}
		if entry["metric"] != "acuity_call_center_provider_command" || entry["action"] != label {
			t.Fatalf("%s metric = %#v, want action %q", action, entry, label)
		}
	}
}

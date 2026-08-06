package humancalling

import "testing"

func TestOutboundTerminationNormalizesProviderOutcomes(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"normal_clearing": "COMPLETED",
		"no_answer":       "NO_ANSWER",
		"no-answer":       "NO_ANSWER",
		"timeout":         "NO_ANSWER",
		"busy":            "BUSY",
		"user_busy":       "BUSY",
		"declined":        "DECLINED",
		"call_rejected":   "DECLINED",
		"rejected":        "DECLINED",
		"":                "FAILED",
		"carrier_error":   "FAILED",
	}
	for cause, want := range tests {
		t.Run(cause, func(t *testing.T) {
			t.Parallel()
			if got := outboundTermination(cause); got != want {
				t.Fatalf("outboundTermination(%q) = %q, want %q", cause, got, want)
			}
		})
	}
}

func TestSupportedUSDestinationPreservesDialingPolicy(t *testing.T) {
	t.Parallel()
	for _, phone := range []string{
		"+14155550123",
		"+14843336938",
	} {
		if !supportedUSDestination(phone) {
			t.Errorf("supported destination %s was rejected", phone)
		}
	}
	for _, phone := range []string{
		"+19005550123",
		"+14159760123",
		"+12115550123",
		"+14151110123",
		"+441234567890",
	} {
		if supportedUSDestination(phone) {
			t.Errorf("unsupported destination %s was accepted", phone)
		}
	}
}

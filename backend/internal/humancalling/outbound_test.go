package humancalling

import "testing"

func TestOutboundTerminationNormalizesProviderOutcomes(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"no_answer":     "NO_ANSWER",
		"no-answer":     "NO_ANSWER",
		"timeout":       "NO_ANSWER",
		"busy":          "BUSY",
		"user_busy":     "BUSY",
		"declined":      "DECLINED",
		"call_rejected": "DECLINED",
		"rejected":      "DECLINED",
		"":              "FAILED",
		"carrier_error": "FAILED",
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

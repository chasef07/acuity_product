package interaction

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestCostAnalyticsDoesNotPriceUnknownOrInvalidUsageAsGemma(t *testing.T) {
	started := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, raw string
		unpriced  int
	}{
		{"missing report", `null`, 0},
		{"different model", `[{"type":"llm_usage","provider":"baseten","model":"GLM-5.2","input_tokens":1000000}]`, 1},
		{"cached exceeds input", `[{"type":"llm_usage","provider":"livekit","model":"google/gemma-4-31b-it","input_tokens":10,"input_cached_tokens":20}]`, 1},
		{"invalid quantity", `[{"type":"tts_usage","provider":"rime","model":"coda","characters_count":-2}]`, 1},
		{"wrong speech model", `[{"type":"stt_usage","provider":"livekit","model":"assemblyai/other","audio_duration":60}]`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := newCostAnalytics(started.Add(-time.Hour), started.Add(time.Hour), time.UTC)
			report.addCall(started, started.Add(time.Minute), json.RawMessage(tc.raw), time.UTC)
			report.finalize()
			if report.PricedCalls != 0 || report.UnpricedUsage != tc.unpriced || report.CostPerCallUSD != nil || report.CostPerMinuteUSD != nil || report.CacheHitRate != nil {
				t.Fatalf("invented complete cost: %+v", report)
			}
			assertCostClose(t, report.TotalCostUSD, 0.0135)
			if report.Daily[0].PricedCalls != 0 || report.Daily[0].UnpricedUsage != tc.unpriced {
				t.Fatalf("hidden daily coverage: %+v", report.Daily[0])
			}
			for _, item := range report.Items[:5] {
				if item.CostUSD != nil || item.SharePercent != nil {
					t.Fatalf("invented usage cost: %+v", item)
				}
			}
		})
	}
}

func assertCostClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

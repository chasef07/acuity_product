package interaction

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestCostAnalyticsNativeUnitsCacheAndItemShares(t *testing.T) {
	started := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	report := newCostAnalytics(started.Add(-24*time.Hour), started.Add(time.Hour), zone)
	// LiveKit's serialized report uses snake_case and audio seconds. The raw
	// collector uses camelCase and milliseconds. Both describe the same usage.
	for _, raw := range []string{
		`[{"type":"llm_usage","provider":"livekit","model":"google/gemma-4-31b-it","input_tokens":1000000,"input_cached_tokens":250000,"output_tokens":100000},
		  {"type":"stt_usage","provider":"livekit","model":"assemblyai/universal-3-5-pro","audio_duration":600},
		  {"type":"tts_usage","provider":"rime","model":"coda","characters_count":10000}]`,
		`[{"type":"llm_usage","provider":"livekit","model":"google/gemma_4_31b_it","inputTokens":1000000,"inputCachedTokens":250000,"outputTokens":100000},
		  {"type":"stt_usage","provider":"assemblyai","model":"universal-3.5-pro","audioDurationMs":600000},
		  {"type":"tts_usage","provider":"rime","model":"coda","charactersCount":10000}]`,
	} {
		report.addCall(started, started.Add(10*time.Minute), json.RawMessage(raw), zone)
	}
	report.finalize()
	if report.RateEffectiveDate != "2026-09-03" {
		t.Fatalf("rate effective date = %q", report.RateEffectiveDate)
	}
	if report.TotalCalls != 2 || report.PricedCalls != 2 || report.UnpricedUsage != 0 {
		t.Fatalf("coverage: %+v", report)
	}
	assertCostClose(t, report.TotalCostUSD, 2.36)
	assertCostClose(t, *report.CostPerCallUSD, 1.18)
	assertCostClose(t, *report.CostPerMinuteUSD, 0.118)
	assertCostClose(t, *report.CacheHitRate, 25)
	assertCostClose(t, report.CacheSavingsUSD, 0.10)
	var totalShare float64
	for i, want := range []float64{0.60, 0.10, 0.24, 0.15, 1.0, 0.20, 0.07} {
		item := report.Items[i]
		if item.CostUSD == nil || item.SharePercent == nil {
			t.Fatalf("missing item %s", item.ID)
		}
		assertCostClose(t, *item.CostUSD, want)
		assertCostClose(t, *item.SharePercent, want/2.36*100)
		totalShare += *item.SharePercent
	}
	assertCostClose(t, totalShare, 100)
	if report.Daily[1].Day != "2026-09-02" || report.Daily[1].Calls != 2 {
		t.Fatalf("local day: %+v", report.Daily)
	}
	assertCostClose(t, report.Daily[1].CostUSD, 2.36)
	if report.Daily[1].PricedCalls != 2 || report.Daily[1].UnpricedUsage != 0 {
		t.Fatalf("daily coverage: %+v", report.Daily[1])
	}
}

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

func TestCostAnalyticsCacheRateIsTokenWeightedAndOmittedNativeZeroIsKnown(t *testing.T) {
	started := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	report := newCostAnalytics(started.Add(-time.Hour), started.Add(time.Hour), time.UTC)
	for _, raw := range []string{
		`[{"type":"llm_usage","provider":"livekit","model":"google/gemma-4-31b-it","input_tokens":100,"input_cached_tokens":90}]`,
		`[{"type":"llm_usage","provider":"livekit","model":"google/gemma-4-31b-it","input_tokens":900}]`,
	} {
		report.addCall(started, started, json.RawMessage(raw), time.UTC)
	}
	report.finalize()
	assertCostClose(t, *report.CacheHitRate, 9)
	if report.Items[2].CostUSD == nil || *report.Items[2].CostUSD != 0 {
		t.Fatal("native omitted output should be recorded zero")
	}
	empty := newCostAnalytics(started, started.Add(time.Hour), time.UTC)
	empty.finalize()
	if empty.CostPerCallUSD != nil || empty.CostPerMinuteUSD != nil || empty.CacheHitRate != nil {
		t.Fatal("empty report has no denominator")
	}
	for _, item := range empty.Items {
		if item.SharePercent != nil {
			t.Fatal("empty report has a share")
		}
	}
}

func TestCostItemHourlyRateUsesItsStructuredQuantity(t *testing.T) {
	item := CostItem{RateUSD: 0.90, RateQuantity: 2, RateUnit: "hour"}
	assertCostClose(t, item.cost(60), 0.45)
}

func TestCostAnalyticsFallbackAdapterUsage(t *testing.T) {
	// LiveKit's FallbackAdapter forwards the underlying provider metrics and
	// emits its own metrics for the same stream. ModelUsageCollector therefore
	// records both rows even when only the primary model answered.
	provider := `{"type":"llm_usage","provider":"livekit","model":"google/gemma-4-31b-it","input_tokens":1000,"input_cached_tokens":200,"output_tokens":20}`
	wrapper := `{"type":"llm_usage","provider":"unknown","model":"FallbackAdapter","input_tokens":1000,"input_cached_tokens":200,"output_tokens":20}`
	speech := `{"type":"stt_usage","provider":"livekit","model":"assemblyai/universal-3-5-pro","audio_duration":60},{"type":"tts_usage","provider":"rime","model":"coda","characters_count":100}`
	for _, tc := range []struct {
		name, llm        string
		priced, unpriced int
	}{
		{"provider alone", provider, 1, 0},
		{"provider and wrapper", provider + "," + wrapper, 1, 0},
		{"wrapper before provider", wrapper + "," + provider, 1, 0},
		{"raw collector wrapper", provider + `,{"type":"llm_usage","provider":"unknown","model":"FallbackAdapter","inputTokens":1000,"inputCachedTokens":200,"outputTokens":20}`, 1, 0},
		{"wrapper without provider evidence", wrapper, 0, 0},
		{"real fallback remains unpriced", provider + "," + wrapper + `,{"type":"llm_usage","provider":"livekit","model":"xai/grok-4.5","input_tokens":50,"output_tokens":10}`, 0, 1},
		{"unknown model remains unpriced", provider + `,{"type":"llm_usage","provider":"unknown","model":"other","input_tokens":1000}`, 0, 1},
		{"invalid provider usage remains unpriced", wrapper + `,{"type":"llm_usage","provider":"livekit","model":"google/gemma-4-31b-it","input_tokens":10,"input_cached_tokens":20}`, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
			report := newCostAnalytics(started, started.Add(time.Hour), time.UTC)
			report.addCall(started, started.Add(time.Minute), json.RawMessage("["+tc.llm+","+speech+"]"), time.UTC)
			report.finalize()
			if report.PricedCalls != tc.priced || report.UnpricedUsage != tc.unpriced || report.Daily[0].PricedCalls != tc.priced || report.Daily[0].UnpricedUsage != tc.unpriced {
				t.Fatalf("coverage: priced=%d unpriced=%d daily=%+v; want %d, %d", report.PricedCalls, report.UnpricedUsage, report.Daily[0], tc.priced, tc.unpriced)
			}
			if tc.priced == 0 {
				if report.CostPerCallUSD != nil || report.CostPerMinuteUSD != nil {
					t.Fatal("incomplete provider evidence produced an average")
				}
				return
			}
			if report.CostPerCallUSD == nil || report.CostPerMinuteUSD == nil {
				t.Fatal("complete provider usage has no average")
			}
			assertCostClose(t, report.TotalCostUSD, 0.026384)
			assertCostClose(t, *report.CostPerCallUSD, 0.026384)
			assertCostClose(t, *report.CostPerMinuteUSD, 0.026384)
			assertCostClose(t, *report.CacheHitRate, 20)
			assertCostClose(t, report.Items[0].Quantity, 800)
		})
	}
}

func assertCostClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

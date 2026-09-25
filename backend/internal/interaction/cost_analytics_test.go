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

func TestGPTLiveAndLunaRecordedCosts(t *testing.T) {
	started := time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC)
	report := newCostAnalytics(started, started.Add(time.Hour), time.UTC)
	report.addCall(started, started.Add(2*time.Minute), json.RawMessage(`[
 {"type":"llm_usage","provider":"api.openai.com","model":"gpt-live-1","session_duration":91.5},
 {"type":"llm_usage","provider":"api.openai.com","model":"gpt-6-luna","input_tokens":10000,"input_cached_tokens":2000,"input_cache_creation_tokens":1000,"output_tokens":500,"output_reasoning_tokens":200}
 ]`), time.UTC)
	report.finalize()
	if report.PricedCalls != 1 || report.UnpricedUsage != 0 {
		t.Fatalf("missing coverage: %+v", report)
	}
	assertCostClose(t, *costItemForID(report.Items, costVoice).CostUSD, .07625)
	assertCostClose(t, *costItemForID(report.Items, costLunaInput).CostUSD, .0007)
	assertCostClose(t, *costItemForID(report.Items, costLunaCached).CostUSD, .00002)
	assertCostClose(t, *costItemForID(report.Items, costLunaWrite).CostUSD, .000125)
	assertCostClose(t, *costItemForID(report.Items, costLunaOutput).CostUSD, .00025)
	assertCostClose(t, report.TotalCostUSD, .104345)
	assertCostClose(t, *report.CacheHitRate, 20)
	assertCostClose(t, report.CacheSavingsUSD, .00018)
	if costItemForID(report.Items, costLLMInput).CostUSD != nil {
		t.Fatal("repriced historical model")
	}
}

func TestGPTLiveCostsKeepUnknownUsageVisible(t *testing.T) {
	started := time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, usage string
		unpriced    int
	}{
		{"missing voice duration", `[{"type":"llm_usage","provider":"api.openai.com","model":"gpt-live-1"}]`, 1},
		{"invalid voice duration", `[{"type":"llm_usage","provider":"api.openai.com","model":"gpt-live-1","session_duration":-1}]`, 1},
		{"wrong provider", `[{"type":"llm_usage","provider":"other","model":"gpt-live-1","session_duration":60}]`, 1},
		{"unknown context tier", `[{"type":"llm_usage","provider":"api.openai.com","model":"gpt-6-luna","input_tokens":272001}]`, 1},
		{"multiple aggregates exceed tier", `[{"type":"llm_usage","provider":"api.openai.com","model":"gpt-6-luna","input_tokens":200000},{"type":"llm_usage","provider":"api.openai.com","model":"gpt-6-luna","input_tokens":200000}]`, 2},
		{"invalid cache writes", `[{"type":"llm_usage","provider":"api.openai.com","model":"gpt-6-luna","input_tokens":10,"input_cached_tokens":5,"input_cache_creation_tokens":6}]`, 1},
		{"missing delegator", `[{"type":"llm_usage","provider":"api.openai.com","model":"gpt-live-1","session_duration":60}]`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := newCostAnalytics(started, started.Add(time.Hour), time.UTC)
			report.addCall(started, started.Add(time.Minute), json.RawMessage(tc.usage), time.UTC)
			report.finalize()
			if report.PricedCalls != 0 || report.CostPerCallUSD != nil || report.UnpricedUsage != tc.unpriced {
				t.Fatalf("invented coverage: %+v", report)
			}
			if costItemForID(report.Items, costLunaInput).CostUSD != nil {
				t.Fatal("invented delegator cost")
			}
		})
	}
}

func TestCostAnalyticsMixedModelHistory(t *testing.T) {
	started := time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC)
	report := newCostAnalytics(started, started.Add(time.Hour), time.UTC)
	for _, usage := range []string{
		`[{"type":"llm_usage","provider":"livekit","model":"google/gemma-4-31b-it","input_tokens":1000000},
    {"type":"stt_usage","provider":"assemblyai","model":"universal-3.5-pro","audio_duration":60},
    {"type":"tts_usage","provider":"rime","model":"coda","characters_count":1000}]`,
		`[{"type":"llm_usage","provider":"api.openai.com","model":"gpt-live-1","session_duration":60},
    {"type":"llm_usage","provider":"api.openai.com","model":"gpt-6-luna","input_tokens":10000,"output_tokens":1000}]`,
	} {
		report.addCall(started, started.Add(time.Minute), json.RawMessage(usage), time.UTC)
	}
	report.finalize()
	if report.PricedCalls != 2 || report.UnpricedUsage != 0 {
		t.Fatalf("mixed coverage lost: %+v", report)
	}
	assertCostClose(t, report.TotalCostUSD, .536)
	assertCostClose(t, *report.CostPerCallUSD, .268)
	assertCostClose(t, *report.CostPerMinuteUSD, .268)
}

package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

type QueryCostAnalyticsCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Range      AnalyticsRange
}

type CostItem struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Quantity     float64  `json:"quantity"`
	Unit         string   `json:"unit"`
	RateUSD      float64  `json:"rateUsd"`
	RateQuantity float64  `json:"rateQuantity"`
	RateUnit     string   `json:"rateUnit"`
	CostUSD      *float64 `json:"costUsd"`
	SharePercent *float64 `json:"sharePercent"`
	Calls        int      `json:"calls"`
}

type CostDay struct {
	Day           string  `json:"day"`
	CostUSD       float64 `json:"costUsd"`
	Calls         int     `json:"calls"`
	PricedCalls   int     `json:"pricedCalls"`
	UnpricedUsage int     `json:"unpricedUsage"`
}

type CostAnalytics struct {
	From              string     `json:"from"`
	Through           string     `json:"through"`
	TimeZone          string     `json:"timeZone"`
	RateEffectiveDate string     `json:"rateEffectiveDate"`
	TotalCalls        int        `json:"totalCalls"`
	PricedCalls       int        `json:"pricedCalls"`
	UnpricedUsage     int        `json:"unpricedUsage"`
	TotalCostUSD      float64    `json:"totalCostUsd"`
	CostPerCallUSD    *float64   `json:"costPerCallUsd"`
	CostPerMinuteUSD  *float64   `json:"costPerMinuteUsd"`
	CacheHitRate      *float64   `json:"cacheHitRate"`
	CacheSavingsUSD   float64    `json:"cacheSavingsUsd"`
	Items             []CostItem `json:"items"`
	Daily             []CostDay  `json:"daily"`
	pricedCostUSD     float64
	pricedMinutes     float64
}

const (
	costRateEffectiveDate = "2026-11-21"
	costMedia             = "media"
	costTelephony         = "telephony"
	costVoice             = "gpt_live"
	costLunaInput         = "luna_input"
	costLunaCached        = "luna_cached"
	costLunaWrite         = "luna_write"
	costLunaOutput        = "luna_output"
	costTerraInput        = "terra_input"
	costTerraCached       = "terra_cached"
	costTerraWrite        = "terra_write"
	costTerraOutput       = "terra_output"
	shortContextTokens    = 272000
)

type tokenModel struct {
	key, input, cached, write, output string
}

var tokenModels = []tokenModel{
	{key: "gpt_6_luna", input: costLunaInput, cached: costLunaCached, write: costLunaWrite, output: costLunaOutput},
	{key: "gpt_6_1_terra", input: costTerraInput, cached: costTerraCached, write: costTerraWrite, output: costTerraOutput},
}

func tokenModelFor(model string) (tokenModel, bool) {
	for _, candidate := range tokenModels {
		if candidate.key == model {
			return candidate, true
		}
	}
	return tokenModel{}, false
}

func costItems() []CostItem {
	return []CostItem{
		{ID: costVoice, Label: "GPT Live · voice", Unit: "minutes", RateUSD: 0.05, RateQuantity: 1, RateUnit: "minute"},
		{ID: costLunaInput, Label: "GPT-6 Luna · uncached input", Unit: "tokens", RateUSD: 0.10, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costLunaCached, Label: "GPT-6 Luna · cached input", Unit: "tokens", RateUSD: 0.01, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costLunaWrite, Label: "GPT-6 Luna · cache writes", Unit: "tokens", RateUSD: 0.125, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costLunaOutput, Label: "GPT-6 Luna · output", Unit: "tokens", RateUSD: 0.50, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costTerraInput, Label: "GPT-6.1 Terra · uncached input", Unit: "tokens", RateUSD: 1.00, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costTerraCached, Label: "GPT-6.1 Terra · cached input", Unit: "tokens", RateUSD: 0.05, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costTerraWrite, Label: "GPT-6.1 Terra · cache writes", Unit: "tokens", RateUSD: 1.25, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costTerraOutput, Label: "GPT-6.1 Terra · output", Unit: "tokens", RateUSD: 5.00, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costMedia, Label: "LiveKit · media", Unit: "minutes", RateUSD: 0.01, RateQuantity: 1, RateUnit: "minute"},
		{ID: costTelephony, Label: "Telnyx · inbound SIP", Unit: "minutes", RateUSD: 0.0035, RateQuantity: 1, RateUnit: "minute"},
	}
}

func (m *Module) QueryCostAnalytics(ctx context.Context, command QueryCostAnalyticsCommand) (CostAnalytics, error) {
	duration, validRange := analyticsRangeDuration(command.Range)
	if !validRange {
		return CostAnalytics{}, ErrInvalidInput
	}
	to := m.now().UTC()
	from := to.Add(-duration)
	tx, locations, err := m.beginAnalyticsScope(ctx, command.Identity, command.PracticeID, command.LocationID, audienceOperator)
	if err != nil {
		return CostAnalytics{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT started_at, ended_at, cost_usage_evidence
		FROM ai_interactions
		WHERE practice_id = $1::uuid AND location_id = ANY($2::uuid[])
			AND started_at >= $3 AND started_at < $4
			AND ended_at IS NOT NULL AND status <> 'IN_PROGRESS'
		ORDER BY started_at, id
		LIMIT $5
	`, command.PracticeID, locations, from, to, analyticsRowLimit+1)
	if err != nil {
		return CostAnalytics{}, fmt.Errorf("query AI costs: %w", err)
	}
	defer rows.Close()
	report := newCostAnalytics(from, to)
	rowCount := 0
	for rows.Next() {
		rowCount++
		var started, ended time.Time
		var usage json.RawMessage
		if err := rows.Scan(&started, &ended, &usage); err != nil {
			return CostAnalytics{}, fmt.Errorf("scan AI costs: %w", err)
		}
		if len(usage) == 0 {
			return CostAnalytics{}, errors.New("AI cost usage evidence backfill is incomplete")
		}
		report.addCall(started, ended, usage)
	}
	if err := rows.Err(); err != nil {
		return CostAnalytics{}, fmt.Errorf("iterate AI costs: %w", err)
	}
	if rowCount > analyticsRowLimit {
		return CostAnalytics{}, fmt.Errorf("AI costs exceed bounded reporting window")
	}
	if err := tx.Commit(ctx); err != nil {
		return CostAnalytics{}, err
	}
	report.finalize()
	return report, nil
}

func newCostAnalytics(from, to time.Time) CostAnalytics {
	first, last := from.In(ReportingZone), to.In(ReportingZone)
	report := CostAnalytics{
		From: first.Format(time.DateOnly), Through: last.Format(time.DateOnly), TimeZone: ReportingZone.String(), RateEffectiveDate: costRateEffectiveDate,
		Items: costItems(), Daily: []CostDay{},
	}
	for _, day := range analyticsDays(from, to) {
		report.Daily = append(report.Daily, CostDay{Day: day.Date})
	}
	return report
}

func (report *CostAnalytics) addCall(started, ended time.Time, raw json.RawMessage) {
	report.TotalCalls++
	quantities := map[string]float64{}
	known := map[string]bool{}
	minutes := ended.Sub(started).Minutes()
	if minutes >= 0 {
		quantities[costMedia], quantities[costTelephony] = minutes, minutes
		known[costMedia], known[costTelephony] = true, true
	}
	var entries []map[string]any
	_ = json.Unmarshal(raw, &entries)
	unpricedUsage := 0
	inputTotals := map[string]float64{}
	for _, entry := range entries {
		model, _ := entry["model"].(string)
		if usageType(entry) == "llm_usage" {
			input, valid := usageQuantity(entry, "input_tokens", "inputTokens")
			if valid {
				inputTotals[costModelKey(model)] += input
			}
		}
	}
	delegated := false
	for _, entry := range entries {
		provider, _ := entry["provider"].(string)
		model, _ := entry["model"].(string)
		provider, model = costModelKey(provider), costModelKey(model)
		openAI := provider == "openai" || provider == "api_openai_com"
		switch usageType(entry) {
		case "llm_usage":
			if model == "gpt_live_1" || model == "gpt_live_1_1" {
				seconds, valid := usageQuantity(entry, "session_duration")
				_, present := entry["session_duration"]
				if !openAI || !valid || !present {
					unpricedUsage++
					continue
				}
				quantities[costVoice] += seconds / 60
				known[costVoice] = true
				continue
			}
			input, a := usageQuantity(entry, "input_tokens", "inputTokens")
			cached, b := usageQuantity(entry, "input_cached_tokens", "inputCachedTokens")
			output, c := usageQuantity(entry, "output_tokens", "outputTokens")
			if priced, ok := tokenModelFor(model); ok {
				writes, d := usageQuantity(entry, "input_cache_creation_tokens")
				if !openAI || !a || !b || !c || !d || cached+writes > input || inputTotals[model] > shortContextTokens {
					unpricedUsage++
					continue
				}
				quantities[priced.input] += input - cached - writes
				quantities[priced.cached] += cached
				quantities[priced.write] += writes
				quantities[priced.output] += output
				known[priced.input], known[priced.cached], known[priced.write], known[priced.output] = true, true, true, true
				delegated = true
				continue
			}
			unpricedUsage++
		case "stt_usage", "tts_usage":
			unpricedUsage++
		}
	}
	complete := unpricedUsage == 0 && known[costMedia] && known[costTelephony] && known[costVoice] && delegated
	var callCost float64
	for i := range report.Items {
		item := &report.Items[i]
		if !known[item.ID] {
			continue
		}
		quantity := quantities[item.ID]
		item.Calls++
		item.Quantity += quantity
		cost := item.cost(quantity)
		callCost += cost
		if item.CostUSD == nil {
			item.CostUSD = new(float64)
		}
		*item.CostUSD += cost
	}
	if complete {
		report.PricedCalls++
		report.pricedCostUSD += callCost
		report.pricedMinutes += max(minutes, 0)
	}
	report.UnpricedUsage += unpricedUsage
	day := started.In(ReportingZone).Format(time.DateOnly)
	for i := range report.Daily {
		if report.Daily[i].Day == day {
			report.Daily[i].CostUSD += callCost
			report.Daily[i].Calls++
			if complete {
				report.Daily[i].PricedCalls++
			}
			report.Daily[i].UnpricedUsage += unpricedUsage
			break
		}
	}
}

func (report *CostAnalytics) finalize() {
	for _, item := range report.Items {
		if item.CostUSD != nil {
			report.TotalCostUSD += *item.CostUSD
		}
	}
	for i := range report.Items {
		item := &report.Items[i]
		if item.CostUSD != nil && report.TotalCostUSD > 0 {
			share := *item.CostUSD / report.TotalCostUSD * 100
			item.SharePercent = &share
		}
	}
	if report.PricedCalls > 0 {
		perCall := report.pricedCostUSD / float64(report.PricedCalls)
		report.CostPerCallUSD = &perCall
		if report.pricedMinutes > 0 {
			perMinute := report.pricedCostUSD / report.pricedMinutes
			report.CostPerMinuteUSD = &perMinute
		}
	}
	var input, cached float64
	for _, priced := range tokenModels {
		inputItem, cachedItem := costItemForID(report.Items, priced.input), costItemForID(report.Items, priced.cached)
		input += inputItem.Quantity + cachedItem.Quantity + costItemForID(report.Items, priced.write).Quantity
		cached += cachedItem.Quantity
		report.CacheSavingsUSD += inputItem.cost(cachedItem.Quantity) - cachedItem.cost(cachedItem.Quantity)
	}
	if input > 0 {
		rate := cached / input * 100
		report.CacheHitRate = &rate
	}
}

func (item CostItem) cost(quantity float64) float64 {
	return quantity * item.RateUSD / item.RateQuantity
}

func costItemForID(items []CostItem, id string) *CostItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	panic("missing cost item " + id)
}

func costModelKey(value string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(strings.TrimSpace(value)))
}

func usageType(entry map[string]any) string {
	if value, _ := entry["type"].(string); value != "" {
		return value
	}
	return "llm_usage"
}

func usageQuantity(entry map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if raw, exists := entry[key]; exists {
			value, ok := raw.(float64)
			return value, ok && value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
		}
	}
	return 0, true
}

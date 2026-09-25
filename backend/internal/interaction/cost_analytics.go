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
	"github.com/jackc/pgx/v5"
)

type QueryCostAnalyticsCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Range      AnalyticsRange
	TimeZone   string
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
	costRateEffectiveDate = "2026-09-25"
	costMedia             = "media"
	costTelephony         = "telephony"
	costVoice             = "gpt_live"
	costLunaInput         = "luna_input"
	costLunaCached        = "luna_cached"
	costLunaWrite         = "luna_write"
	costLunaOutput        = "luna_output"
)

func costItems() []CostItem {
	return []CostItem{
		{ID: costVoice, Label: "GPT Live 1 · voice", Unit: "minutes", RateUSD: 0.05, RateQuantity: 1, RateUnit: "minute"},
		{ID: costLunaInput, Label: "GPT-6 Luna · uncached input", Unit: "tokens", RateUSD: 0.10, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costLunaCached, Label: "GPT-6 Luna · cached input", Unit: "tokens", RateUSD: 0.01, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costLunaWrite, Label: "GPT-6 Luna · cache writes", Unit: "tokens", RateUSD: 0.125, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costLunaOutput, Label: "GPT-6 Luna · output", Unit: "tokens", RateUSD: 0.50, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costMedia, Label: "LiveKit · media", Unit: "minutes", RateUSD: 0.01, RateQuantity: 1, RateUnit: "minute"},
		{ID: costTelephony, Label: "Telnyx · inbound SIP", Unit: "minutes", RateUSD: 0.0035, RateQuantity: 1, RateUnit: "minute"},
	}
}

func (m *Module) QueryCostAnalytics(ctx context.Context, command QueryCostAnalyticsCommand) (CostAnalytics, error) {
	duration, validRange := analyticsRangeDuration(command.Range)
	zone, zoneErr := time.LoadLocation(command.TimeZone)
	if m.database == nil || m.access == nil || !validRange || !validUUID(command.PracticeID) ||
		(command.LocationID != "" && !validUUID(command.LocationID)) ||
		command.TimeZone == "" || command.TimeZone == "Local" || zoneErr != nil {
		return CostAnalytics{}, ErrInvalidInput
	}
	to := m.now().UTC()
	from := to.Add(-duration)
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CostAnalytics{}, fmt.Errorf("begin AI costs: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '1500ms'; SET LOCAL lock_timeout = '100ms'; SET LOCAL max_parallel_workers_per_gather = 0; SET LOCAL work_mem = '4MB'`); err != nil {
		return CostAnalytics{}, err
	}
	authorization, err := m.access.LockReadAuthorization(ctx, tx, command.Identity, command.PracticeID, command.LocationID)
	if errors.Is(err, access.ErrDenied) {
		return CostAnalytics{}, ErrDenied
	}
	if err != nil {
		return CostAnalytics{}, err
	}
	if !authorization.PlatformOperator {
		return CostAnalytics{}, ErrDenied
	}
	locations := authorizedLocationIDs(authorization, command.LocationID)
	if len(locations) == 0 {
		return CostAnalytics{}, ErrDenied
	}
	// Source-driven compact usage avoids detoasting full session reports for
	// every call in the reporting window. Pricing remains owned here.
	rows, err := tx.Query(ctx, `
		SELECT started_at, ended_at, cost_usage_evidence
		FROM ai_interactions
		WHERE practice_id = $1::uuid AND location_id = ANY($2::uuid[])
			AND started_at >= $3 AND started_at < $4
			AND ended_at IS NOT NULL AND status <> 'IN_PROGRESS'
		ORDER BY started_at, id
		LIMIT 50001
	`, command.PracticeID, locations, from, to)
	if err != nil {
		return CostAnalytics{}, fmt.Errorf("query AI costs: %w", err)
	}
	defer rows.Close()
	report := newCostAnalytics(from, to, zone)
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
		report.addCall(started, ended, usage, zone)
	}
	if err := rows.Err(); err != nil {
		return CostAnalytics{}, fmt.Errorf("iterate AI costs: %w", err)
	}
	if rowCount > 50000 {
		return CostAnalytics{}, fmt.Errorf("AI costs exceed bounded reporting window")
	}
	if err := tx.Commit(ctx); err != nil {
		return CostAnalytics{}, err
	}
	report.finalize()
	return report, nil
}

func newCostAnalytics(from, to time.Time, zone *time.Location) CostAnalytics {
	first, last := from.In(zone), to.In(zone)
	report := CostAnalytics{
		From: first.Format(time.DateOnly), Through: last.Format(time.DateOnly), TimeZone: zone.String(), RateEffectiveDate: costRateEffectiveDate,
		Items: costItems(), Daily: []CostDay{},
	}
	for day := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, zone); !day.After(last); day = day.AddDate(0, 0, 1) {
		report.Daily = append(report.Daily, CostDay{Day: day.Format(time.DateOnly)})
	}
	return report
}

func (report *CostAnalytics) addCall(started, ended time.Time, raw json.RawMessage, zone *time.Location) {
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
	// Session reports aggregate requests. Only totals <=272K prove every
	// request used short-context pricing; larger totals cannot establish a tier.
	// Rates: https://developers.openai.com/api/docs/models/gpt-6-luna
	var lunaInputTotal float64
	for _, entry := range entries {
		model, _ := entry["model"].(string)
		if entry["type"] == "llm_usage" && costModelKey(model) == "gpt_6_luna" {
			input, valid := usageQuantity(entry, "input_tokens", "inputTokens")
			if valid {
				lunaInputTotal += input
			}
		}
	}
	for _, entry := range entries {
		provider, _ := entry["provider"].(string)
		model, _ := entry["model"].(string)
		provider, model = costModelKey(provider), costModelKey(model)
		openAI := provider == "openai" || provider == "api_openai_com"
		switch entry["type"] {
		case "llm_usage":
			if model == "gpt_live_1" {
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
			if model == "gpt_6_luna" {
				writes, d := usageQuantity(entry, "input_cache_creation_tokens")
				if !openAI || !a || !b || !c || !d || cached+writes > input || lunaInputTotal > 272000 {
					unpricedUsage++
					continue
				}
				quantities[costLunaInput] += input - cached - writes
				quantities[costLunaCached] += cached
				quantities[costLunaWrite] += writes
				quantities[costLunaOutput] += output
				known[costLunaInput], known[costLunaCached], known[costLunaWrite], known[costLunaOutput] = true, true, true, true
				continue
			}
			unpricedUsage++
		case "stt_usage", "tts_usage":
			unpricedUsage++
		}
	}
	complete := unpricedUsage == 0 && known[costMedia] && known[costTelephony] && known[costVoice] && known[costLunaInput]
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
	day := started.In(zone).Format(time.DateOnly)
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
	inputItem, cachedItem := costItemForID(report.Items, costLunaInput), costItemForID(report.Items, costLunaCached)
	input := inputItem.Quantity + cachedItem.Quantity + costItemForID(report.Items, costLunaWrite).Quantity
	if input > 0 {
		rate := cachedItem.Quantity / input * 100
		report.CacheHitRate = &rate
	}
	report.CacheSavingsUSD = inputItem.cost(cachedItem.Quantity) - cachedItem.cost(cachedItem.Quantity)
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

// Native LiveKit reports omit zero-valued fields. A missing field inside a
// recorded usage entry is zero; a missing usage entry is unknown.
func usageQuantity(entry map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if raw, exists := entry[key]; exists {
			value, ok := raw.(float64)
			return value, ok && value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
		}
	}
	return 0, true
}

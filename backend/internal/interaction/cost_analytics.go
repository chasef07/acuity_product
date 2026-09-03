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
	costRateEffectiveDate = "2026-09-03"
	costLLMInput          = "llm_input"
	costLLMCached         = "llm_cached"
	costLLMOutput         = "llm_output"
	costSTT               = "stt"
	costTTS               = "tts"
	costMedia             = "media"
	costTelephony         = "telephony"
)

func costItems() []CostItem {
	return []CostItem{
		{ID: costLLMInput, Label: "Gemma 4 31B IT · uncached input", Unit: "tokens", RateUSD: 0.40, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costLLMCached, Label: "Gemma 4 31B IT · cached input", Unit: "tokens", RateUSD: 0.20, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costLLMOutput, Label: "Gemma 4 31B IT · output", Unit: "tokens", RateUSD: 1.20, RateQuantity: 1e6, RateUnit: "tokens"},
		{ID: costSTT, Label: "AssemblyAI · Universal 3.5 Pro", Unit: "minutes", RateUSD: 0.45, RateQuantity: 1, RateUnit: "hour"},
		{ID: costTTS, Label: "Rime · Coda", Unit: "characters", RateUSD: 0.05, RateQuantity: 1000, RateUnit: "characters"},
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
	// The native session report is stored as transcript. Read its bounded usage
	// records only; never transfer conversation content to the cost query.
	rows, err := tx.Query(ctx, `
		SELECT started_at, ended_at, transcript->'usage'
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
	unpriced := false
	unpricedUsage := 0
	for _, entry := range entries {
		provider, _ := entry["provider"].(string)
		model, _ := entry["model"].(string)
		provider, model = costModelKey(provider), costModelKey(model)
		switch entry["type"] {
		case "llm_usage":
			input, a := usageQuantity(entry, "input_tokens", "inputTokens")
			cached, b := usageQuantity(entry, "input_cached_tokens", "inputCachedTokens")
			output, c := usageQuantity(entry, "output_tokens", "outputTokens")
			if (provider != "livekit" && provider != "google") || model != "google/gemma_4_31b_it" || !a || !b || !c || cached > input {
				unpriced = true
				unpricedUsage++
				continue
			}
			quantities[costLLMInput] += input - cached
			quantities[costLLMCached] += cached
			quantities[costLLMOutput] += output
			known[costLLMInput], known[costLLMCached], known[costLLMOutput] = true, true, true
		case "stt_usage":
			audio, valid := usageQuantity(entry, "audio_duration", "audio_duration_ms", "audioDurationMs")
			if _, seconds := entry["audio_duration"]; seconds {
				audio *= 1000
			}
			matchedModel := (provider == "livekit" && model == "assemblyai/universal_3_5_pro") ||
				(provider == "assemblyai" && model == "universal_3_5_pro")
			if !matchedModel || !valid {
				unpriced = true
				unpricedUsage++
				continue
			}
			quantities[costSTT] += audio / 60000
			known[costSTT] = true
		case "tts_usage":
			characters, valid := usageQuantity(entry, "characters_count", "charactersCount")
			matchedModel := (provider == "rime" && model == "coda") || (provider == "livekit" && model == "rime/coda")
			if !matchedModel || !valid {
				unpriced = true
				unpricedUsage++
				continue
			}
			quantities[costTTS] += characters
			known[costTTS] = true
		}
	}
	complete := !unpriced
	var callCost float64
	for i := range report.Items {
		item := &report.Items[i]
		if !known[item.ID] {
			complete = false
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
	inputItem := costItemForID(report.Items, costLLMInput)
	cachedItem := costItemForID(report.Items, costLLMCached)
	input := inputItem.Quantity + cachedItem.Quantity
	if input > 0 {
		rate := cachedItem.Quantity / input * 100
		report.CacheHitRate = &rate
	}
	report.CacheSavingsUSD = cachedItem.Quantity * (inputItem.unitRate() - cachedItem.unitRate())
}

func (item CostItem) unitRate() float64 {
	if item.RateUnit == "hour" {
		return item.RateUSD / (item.RateQuantity * 60)
	}
	return item.RateUSD / item.RateQuantity
}

func (item CostItem) cost(quantity float64) float64 {
	return quantity * item.unitRate()
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

package interaction

import (
	"encoding/json"
	"math"
	"sort"
	"time"
)

type AnalyticsQuality struct {
	Daily            []QualityDay
	EvaluatedCalls   int
	UnevaluatedCalls int
	FlaggedCalls     int
	CheckFlags       []CheckFlagCount
	StaffFlags       StaffFlagCounts
	SentimentCalls   int
	MeanSentiment    *float64
	TokenCalls       int
	P50InputTokens   *int
	P50CachedTokens  *int
	P50OutputTokens  *int
	FootprintCalls   int
	LatestFootprint  *ContextFootprint
}

type QualityDay struct {
	Date             string
	EvaluatedCalls   int
	UnevaluatedCalls int
	FlaggedCalls     int
	CheckFlags       []CheckFlagCount
	StaffFlags       StaffFlagCounts
	SentimentCalls   int
	SentimentCounts  []int
	MeanSentiment    *float64
	TokenCalls       int
	P50InputTokens   *int
	P90InputTokens   *int
	P50CachedTokens  *int
	P50OutputTokens  *int
	FootprintCalls   int
	Footprint        *ContextFootprint
}

type CheckFlagCount struct {
	Check       string
	Calls       int
	ScoredCalls int
}

type StaffFlagCounts struct {
	Pending    int
	Confirmed  int
	NotAnIssue int
}

type ContextFootprint struct {
	SpeakerPromptTokens int
	ThinkerPromptTokens int
	ToolSchemaTokens    int
	ToolCount           int
}

type qualitySample struct {
	startedAt  time.Time
	evaluation evaluationReading
	staffFlag  string
	usage      *tokenUsage
	footprint  *ContextFootprint
}

type tokenUsage struct{ input, cached, output float64 }

type qualityDayAccumulator struct {
	day                   QualityDay
	checks                map[string]*CheckFlagCount
	sentiment             []float64
	input, cached, output []float64
	footprints            []ContextFootprint
}

type qualityAccumulator struct {
	days                  []*qualityDayAccumulator
	byDate                map[string]*qualityDayAccumulator
	checks                map[string]*CheckFlagCount
	sentiment             []float64
	input, cached, output []float64
	latest                *qualitySample
	result                AnalyticsQuality
}

func newQualityAccumulator(days []AnalyticsDay) *qualityAccumulator {
	q := &qualityAccumulator{byDate: map[string]*qualityDayAccumulator{}, checks: map[string]*CheckFlagCount{}}
	for _, day := range days {
		a := &qualityDayAccumulator{
			day:    QualityDay{Date: day.Date, SentimentCounts: make([]int, 5)},
			checks: map[string]*CheckFlagCount{},
		}
		q.days = append(q.days, a)
		q.byDate[day.Date] = a
	}
	return q
}

func (q *qualityAccumulator) add(sample qualitySample) {
	day := q.byDate[sample.startedAt.UTC().Format(time.DateOnly)]
	if day == nil {
		return
	}
	if !sample.evaluation.Evaluated {
		day.day.UnevaluatedCalls++
		q.result.UnevaluatedCalls++
	} else {
		day.day.EvaluatedCalls++
		q.result.EvaluatedCalls++
		if len(sample.evaluation.Flags) > 0 {
			day.day.FlaggedCalls++
			q.result.FlaggedCalls++
		}
		for _, check := range sample.evaluation.Scored {
			checkCount(day.checks, check).ScoredCalls++
			checkCount(q.checks, check).ScoredCalls++
		}
		for _, flag := range sample.evaluation.Flags {
			checkCount(day.checks, flag.Check).Calls++
			checkCount(q.checks, flag.Check).Calls++
		}
	}
	if score := sample.evaluation.Sentiment; score != nil {
		day.day.SentimentCalls++
		q.result.SentimentCalls++
		day.day.SentimentCounts[int(math.Round(*score))]++
		day.sentiment = append(day.sentiment, *score)
		q.sentiment = append(q.sentiment, *score)
	}
	countStaffFlag(&day.day.StaffFlags, sample.staffFlag)
	countStaffFlag(&q.result.StaffFlags, sample.staffFlag)
	if usage := sample.usage; usage != nil {
		day.day.TokenCalls++
		q.result.TokenCalls++
		day.input, day.cached, day.output = append(day.input, usage.input), append(day.cached, usage.cached), append(day.output, usage.output)
		q.input, q.cached, q.output = append(q.input, usage.input), append(q.cached, usage.cached), append(q.output, usage.output)
	}
	if sample.footprint != nil {
		day.day.FootprintCalls++
		q.result.FootprintCalls++
		day.footprints = append(day.footprints, *sample.footprint)
		if q.latest == nil || sample.startedAt.After(q.latest.startedAt) {
			q.latest = &sample
		}
	}
}

func checkCount(checks map[string]*CheckFlagCount, check string) *CheckFlagCount {
	if checks[check] == nil {
		checks[check] = &CheckFlagCount{Check: check}
	}
	return checks[check]
}

func countStaffFlag(counts *StaffFlagCounts, state string) {
	switch state {
	case "PENDING":
		counts.Pending++
	case "CONFIRMED":
		counts.Confirmed++
	case "NOT_AN_ISSUE":
		counts.NotAnIssue++
	}
}

func (q *qualityAccumulator) finish() AnalyticsQuality {
	result := q.result
	result.Daily = make([]QualityDay, 0, len(q.days))
	for _, a := range q.days {
		day := a.day
		day.CheckFlags = checkFlagCounts(a.checks)
		day.MeanSentiment = mean(a.sentiment)
		day.P50InputTokens, day.P90InputTokens = medianAndP90(a.input)
		day.P50CachedTokens, _ = medianAndP90(a.cached)
		day.P50OutputTokens, _ = medianAndP90(a.output)
		day.Footprint = medianFootprint(a.footprints)
		result.Daily = append(result.Daily, day)
	}
	result.CheckFlags = checkFlagCounts(q.checks)
	result.MeanSentiment = mean(q.sentiment)
	result.P50InputTokens, _ = medianAndP90(q.input)
	result.P50CachedTokens, _ = medianAndP90(q.cached)
	result.P50OutputTokens, _ = medianAndP90(q.output)
	if q.latest != nil {
		result.LatestFootprint = q.latest.footprint
	}
	return result
}

func checkFlagCounts(checks map[string]*CheckFlagCount) []CheckFlagCount {
	result := make([]CheckFlagCount, 0, len(checks))
	for _, count := range checks {
		result = append(result, *count)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Calls != result[j].Calls {
			return result[i].Calls > result[j].Calls
		}
		return result[i].Check < result[j].Check
	})
	return result
}

func mean(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	var total float64
	for _, value := range values {
		total += value
	}
	result := total / float64(len(values))
	return &result
}

func medianAndP90(values []float64) (*int, *int) {
	sort.Float64s(values)
	return sortedMedian(values), sortedPercentile(values, 90)
}

func medianFootprint(values []ContextFootprint) *ContextFootprint {
	if len(values) == 0 {
		return nil
	}
	component := func(value func(ContextFootprint) int) int {
		samples := make([]float64, 0, len(values))
		for _, footprint := range values {
			samples = append(samples, float64(value(footprint)))
		}
		sort.Float64s(samples)
		return *sortedMedian(samples)
	}
	return &ContextFootprint{
		SpeakerPromptTokens: component(func(f ContextFootprint) int { return f.SpeakerPromptTokens }),
		ThinkerPromptTokens: component(func(f ContextFootprint) int { return f.ThinkerPromptTokens }),
		ToolSchemaTokens:    component(func(f ContextFootprint) int { return f.ToolSchemaTokens }),
		ToolCount:           component(func(f ContextFootprint) int { return f.ToolCount }),
	}
}

func callTokenUsage(raw json.RawMessage) *tokenUsage {
	var entries []map[string]any
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var usage tokenUsage
	reported := false
	for _, entry := range entries {
		_, snake := entry["input_tokens"]
		_, camel := entry["inputTokens"]
		if usageType(entry) != "llm_usage" || (!snake && !camel) {
			continue
		}
		input, a := usageQuantity(entry, "input_tokens", "inputTokens")
		cached, b := usageQuantity(entry, "input_cached_tokens", "inputCachedTokens")
		output, c := usageQuantity(entry, "output_tokens", "outputTokens")
		if !a || !b || !c {
			continue
		}
		reported = true
		usage.input += input
		usage.cached += cached
		usage.output += output
	}
	if !reported {
		return nil
	}
	return &usage
}

func contextFootprint(raw json.RawMessage) *ContextFootprint {
	var value struct {
		SpeakerPromptTokens *int `json:"speakerPromptTokens"`
		ThinkerPromptTokens *int `json:"thinkerPromptTokens"`
		ToolSchemaTokens    *int `json:"toolSchemaTokens"`
		ToolCount           *int `json:"toolCount"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	for _, field := range []*int{value.SpeakerPromptTokens, value.ThinkerPromptTokens, value.ToolSchemaTokens, value.ToolCount} {
		if field == nil || *field < 0 {
			return nil
		}
	}
	return &ContextFootprint{
		SpeakerPromptTokens: *value.SpeakerPromptTokens,
		ThinkerPromptTokens: *value.ThinkerPromptTokens,
		ToolSchemaTokens:    *value.ToolSchemaTokens,
		ToolCount:           *value.ToolCount,
	}
}

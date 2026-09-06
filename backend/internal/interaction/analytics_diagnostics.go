package interaction

import (
	"math"
	"sort"
	"time"
)

// Diagnostics use the same observed samples as the range summary. Examples are
// bounded, content-free links back to authoritative call evidence.
type AnalyticsDiagnostics struct {
	Stages []LatencyDistribution
	Tools  []ToolDiagnostics
}
type DiagnosticExample struct {
	InteractionID string
	ItemID        string
	CallID        string
	StartedAt     time.Time
	DurationMs    *int
	Status        string
}
type LatencyBucket struct {
	FromMs   int
	ToMs     *int
	Count    int
	Examples []DiagnosticExample
}
type LatencyTrend struct {
	Date        string
	SampleCount int
	P50Ms       *int
	P95Ms       *int
}
type LatencyDistribution struct {
	Stage         string
	SampleCount   int
	MeasuredCalls int
	P50Ms         *int
	P95Ms         *int
	P99Ms         *int
	Buckets       []LatencyBucket
	Trend         []LatencyTrend
}
type ToolDiagnostics struct {
	Name            string
	ExecutionCount  int
	ErrorCount      int
	IncompleteCount int
	SampleCount     int
	P50Ms           *int
	P95Ms           *int
	Examples        []DiagnosticExample
	Errors          []DiagnosticExample
}
type latencyObservation struct {
	stage  string
	value  float64
	itemID string
}

var diagnosticStages = []string{"e2e", "stt", "llm", "tts"}
var latencyBucketEdges = []int{0, 250, 500, 1000, 1500, 2000, 3000, 5000, 10000}

func latencyStageValues(values latencyValueSet, stage string) []float64 {
	switch stage {
	case "stt":
		return values.stt
	case "llm":
		return values.ttft
	case "tts":
		return values.ttsTtfb
	default:
		return values.total
	}
}
func appendLatencyObservation(values *latencyValueSet, metrics map[string]any, itemID string) {
	var sample latencyValueSet
	appendLatencyValues(&sample, metrics)
	appendLatencyValues(values, metrics)
	for _, stage := range diagnosticStages {
		for _, value := range latencyStageValues(sample, stage) {
			values.observations = append(values.observations, latencyObservation{stage, value, itemID})
		}
	}
}

// Transcript event timestamps are provider evidence. Never use synthetic sort
// timestamps, missing outputs, negative durations, or E2E timing as execution time.
func toolDuration(call, output map[string]any) *int {
	if output == nil {
		return nil
	}
	start := timestampValue(firstRecordValue(call, "created_at", "createdAt"), time.Time{})
	end := timestampValue(firstRecordValue(output, "created_at", "createdAt"), time.Time{})
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	value := int(math.Round(end.Sub(start).Seconds() * 1000))
	return &value
}

type stageAccumulator struct {
	result LatencyDistribution
	values []float64
	days   map[string][]float64
}
type toolAccumulator struct {
	result ToolDiagnostics
	values []float64
}
type diagnosticsAccumulator struct {
	from, through time.Time
	stages        map[string]*stageAccumulator
	tools         map[string]*toolAccumulator
}

func newDiagnosticsAccumulator() *diagnosticsAccumulator {
	d := &diagnosticsAccumulator{stages: map[string]*stageAccumulator{}, tools: map[string]*toolAccumulator{}}
	for _, stage := range diagnosticStages {
		a := &stageAccumulator{result: LatencyDistribution{Stage: stage, Buckets: []LatencyBucket{}, Trend: []LatencyTrend{}}, days: map[string][]float64{}}
		for i, edge := range latencyBucketEdges {
			b := LatencyBucket{FromMs: edge, Examples: []DiagnosticExample{}}
			if i+1 < len(latencyBucketEdges) {
				upper := latencyBucketEdges[i+1]
				b.ToMs = &upper
			}
			a.result.Buckets = append(a.result.Buckets, b)
		}
		d.stages[stage] = a
	}
	return d
}
func (d *diagnosticsAccumulator) add(p analyticsProjection) {
	for _, stage := range diagnosticStages {
		a := d.stages[stage]
		values := latencyStageValues(p.latencySamples, stage)
		if len(values) > 0 {
			a.result.MeasuredCalls++
		}
		a.values = append(a.values, values...)
		day := p.call.StartedAt.UTC().Format("2006-01-02")
		if len(values) > 0 {
			a.days[day] = append(a.days[day], values...)
		}
	}
	for _, sample := range p.latencySamples.observations {
		a := d.stages[sample.stage]
		value := int(math.Round(sample.value))
		// Bucket the original observation, not its rounded display value.
		index := sort.Search(len(latencyBucketEdges), func(i int) bool { return float64(latencyBucketEdges[i]) > sample.value }) - 1
		b := &a.result.Buckets[max(index, 0)]
		b.Count++
		b.Examples = keepDiagnosticExamples(b.Examples, DiagnosticExample{InteractionID: p.call.ID, ItemID: sample.itemID, StartedAt: p.call.StartedAt, DurationMs: &value})
	}
	for _, execution := range p.executions {
		a := d.tools[execution.Name]
		if a == nil {
			a = &toolAccumulator{result: ToolDiagnostics{Name: execution.Name, Examples: []DiagnosticExample{}, Errors: []DiagnosticExample{}}}
			d.tools[execution.Name] = a
		}
		a.result.ExecutionCount++
		example := DiagnosticExample{InteractionID: p.call.ID, CallID: execution.CallID, StartedAt: p.call.StartedAt, DurationMs: execution.DurationMs, Status: execution.Status}
		if execution.Status == "ERROR" {
			a.result.ErrorCount++
			a.result.Errors = keepDiagnosticExamples(a.result.Errors, example)
		}
		if execution.Status == "INCOMPLETE" {
			a.result.IncompleteCount++
		}
		if execution.DurationMs != nil {
			a.values = append(a.values, float64(*execution.DurationMs))
			a.result.Examples = keepDiagnosticExamples(a.result.Examples, example)
		}
	}
}
func keepDiagnosticExamples(examples []DiagnosticExample, value DiagnosticExample) []DiagnosticExample {
	examples = append(examples, value)
	sort.SliceStable(examples, func(i, j int) bool {
		a, b := examples[i], examples[j]
		if a.DurationMs != nil && b.DurationMs == nil {
			return true
		}
		if a.DurationMs == nil && b.DurationMs != nil {
			return false
		}
		if a.DurationMs != nil && b.DurationMs != nil && *a.DurationMs != *b.DurationMs {
			return *a.DurationMs > *b.DurationMs
		}
		if !a.StartedAt.Equal(b.StartedAt) {
			return a.StartedAt.After(b.StartedAt)
		}
		if a.InteractionID != b.InteractionID {
			return a.InteractionID < b.InteractionID
		}
		return a.ItemID+a.CallID < b.ItemID+b.CallID
	})
	return examples[:min(len(examples), 5)]
}
func diagnosticPercentiles(values []float64) (*int, *int, *int) {
	sort.Float64s(values)
	return sortedMedianMilliseconds(values), sortedPercentileMilliseconds(values, 95), sortedPercentileMilliseconds(values, 99)
}
func (d *diagnosticsAccumulator) finish() AnalyticsDiagnostics {
	result := AnalyticsDiagnostics{Stages: []LatencyDistribution{}, Tools: []ToolDiagnostics{}}
	for _, stage := range diagnosticStages {
		a := d.stages[stage]
		// Empty UTC dates are gaps in the chart, never zero-latency samples.
		if !d.from.IsZero() && !d.through.IsZero() {
			for day := d.from.UTC().Truncate(24 * time.Hour); !day.After(d.through); day = day.AddDate(0, 0, 1) {
				key := day.Format("2006-01-02")
				if _, exists := a.days[key]; !exists {
					a.days[key] = nil
				}
			}
		}
		a.result.SampleCount = len(a.values)
		a.result.P50Ms, a.result.P95Ms, a.result.P99Ms = diagnosticPercentiles(a.values)
		for day, values := range a.days {
			p50, p95, _ := diagnosticPercentiles(values)
			a.result.Trend = append(a.result.Trend, LatencyTrend{day, len(values), p50, p95})
		}
		sort.Slice(a.result.Trend, func(i, j int) bool { return a.result.Trend[i].Date < a.result.Trend[j].Date })
		result.Stages = append(result.Stages, a.result)
	}
	for _, a := range d.tools {
		a.result.SampleCount = len(a.values)
		a.result.P50Ms, a.result.P95Ms, _ = diagnosticPercentiles(a.values)
		result.Tools = append(result.Tools, a.result)
	}
	sort.Slice(result.Tools, func(i, j int) bool {
		a, b := result.Tools[i], result.Tools[j]
		if a.P95Ms != nil && b.P95Ms == nil {
			return true
		}
		if a.P95Ms == nil && b.P95Ms != nil {
			return false
		}
		if a.P95Ms != nil && b.P95Ms != nil && *a.P95Ms != *b.P95Ms {
			return *a.P95Ms > *b.P95Ms
		}
		return a.Name < b.Name
	})
	return result
}

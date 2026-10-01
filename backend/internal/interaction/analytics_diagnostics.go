package interaction

import (
	"math"
	"sort"
	"time"
)

// Diagnostics use the same observed samples as the range summary. Examples are
// bounded, content-free links back to authoritative call evidence.
type AnalyticsDiagnostics struct {
	Tools []ToolDiagnostics
}
type DiagnosticExample struct {
	InteractionID string
	ItemID        string
	CallID        string
	StartedAt     time.Time
	DurationMs    *int
	Status        string
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

type toolAccumulator struct {
	result ToolDiagnostics
	values []float64
}
type diagnosticsAccumulator struct {
	tools map[string]*toolAccumulator
}

func newDiagnosticsAccumulator() *diagnosticsAccumulator {
	return &diagnosticsAccumulator{tools: map[string]*toolAccumulator{}}
}
func (d *diagnosticsAccumulator) add(p analyticsProjection) {
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
func (d *diagnosticsAccumulator) finish() AnalyticsDiagnostics {
	result := AnalyticsDiagnostics{Tools: []ToolDiagnostics{}}
	for _, a := range d.tools {
		a.result.SampleCount = len(a.values)
		sort.Float64s(a.values)
		a.result.P50Ms, a.result.P95Ms = sortedMedian(a.values), sortedPercentile(a.values, 95)
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

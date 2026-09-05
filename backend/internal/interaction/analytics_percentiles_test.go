package interaction

import (
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"sort"
	"testing"
)

func TestLatencyPercentilesPreserveExactStatistics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []float64
		want   [3]int
	}{
		{"empty", nil, [3]int{}},
		{"single", []float64{10.5}, [3]int{11, 11, 11}},
		{"even median averages", []float64{100, 10}, [3]int{55, 100, 100}},
		{"odd median", []float64{100, 9, 20}, [3]int{20, 100, 100}},
		{"tails use nearest rank", []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 100}, [3]int{6, 9, 100}},
		{"duplicates and rounding", []float64{1.1, 3.9, 1.1, 3.9}, [3]int{3, 4, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.values)
			median := medianMilliseconds(tc.values)
			if !reflect.DeepEqual(before, tc.values) {
				t.Fatal("per-call median mutated shared samples")
			}
			p50, p90, p99 := latencyPercentiles(tc.values)
			if len(tc.values) == 0 {
				if p50 != nil || p90 != nil || p99 != nil {
					t.Fatal("missing samples became zero")
				}
				return
			}
			if [3]int{*p50, *p90, *p99} != tc.want || *median != *p50 {
				t.Fatalf("got %d/%d/%d want %v", *p50, *p90, *p99, tc.want)
			}
		})
	}
}

// Keep the previous algorithm here to quantify the savings against identical
// exact statistics, without adding a second production implementation.
func BenchmarkLatencyPercentiles(b *testing.B) {
	random := rand.New(rand.NewPCG(1, 2))
	values := make([]float64, 100000)
	for i := range values {
		values[i] = random.Float64() * 10000
	}
	b.Run("three-sorts", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = medianMilliseconds(values)
			for _, percentile := range []float64{90, 99} {
				ordered := slices.Clone(values)
				sort.Float64s(ordered)
				index := int(math.Ceil(percentile/100*float64(len(ordered)))) - 1
				_ = int(math.Round(ordered[index]))
			}
		}
	})
	b.Run("one-sort", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			latencyPercentiles(slices.Clone(values))
		}
	})
}

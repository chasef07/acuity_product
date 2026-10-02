package interaction

import (
	"reflect"
	"slices"
	"sort"
	"testing"
)

func TestPercentilesPreserveExactStatistics(t *testing.T) {
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
			ordered := slices.Clone(tc.values)
			sort.Float64s(ordered)
			p50, p90, p99 := sortedMedian(ordered), sortedPercentile(ordered, 90), sortedPercentile(ordered, 99)
			if len(tc.values) == 0 {
				if p50 != nil || p90 != nil || p99 != nil || median != nil {
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

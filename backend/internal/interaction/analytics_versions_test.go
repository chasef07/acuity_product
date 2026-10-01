package interaction

import (
	"testing"
	"time"
)

func TestVersionChangesMarkFirstServedVersionOnly(t *testing.T) {
	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	v := newVersionAccumulator()
	v.seed("agent", "", "0.10.0")
	v.seed("knowledge", "office-a", "revision-1")
	calls := []callVersions{
		{"office-a", map[string]string{"agent": "0.10.0", "knowledge": "revision-1"}},
		{"office-b", map[string]string{"agent": "0.11.0", "knowledge": "revision-9", "prompts": "0.4.0"}},
		{"office-a", map[string]string{"agent": "0.10.0", "prompts": "0.5.0"}},
		{"office-a", map[string]string{"agent": "0.11.0", "knowledge": "revision-2"}},
		{"office-b", map[string]string{"evaluator": "", "judges": ""}},
	}
	for i, call := range calls {
		v.add(start.Add(time.Duration(i)*time.Hour), call)
	}
	got := v.finish().Changes
	want := []struct{ dimension, version, previous, location string }{
		{"agent", "0.11.0", "0.10.0", ""},
		{"prompts", "0.5.0", "0.4.0", ""},
		{"knowledge", "revision-2", "revision-1", "office-a"},
	}
	if len(got) != len(want) {
		t.Fatalf("changes=%+v", got)
	}
	for i, change := range got {
		w := want[i]
		if change.Dimension != w.dimension || change.Version != w.version || change.PreviousVersion != w.previous || change.LocationID != w.location {
			t.Fatalf("change %d=%+v want %+v", i, change, w)
		}
	}
	if got[0].Date != "2026-09-28" || !got[0].FirstSeenAt.Equal(start.Add(time.Hour)) {
		t.Fatalf("first served=%+v", got[0])
	}
}

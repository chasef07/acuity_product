package interaction

import (
	"testing"
	"time"
)

type versionCall struct {
	day, hour int
	call      callVersions
}

type wantChange struct {
	dimension, version, previous, location string
	firstSeenAt                            time.Time
}

func versionChanges(seed func(*versionAccumulator), calls []versionCall) []VersionChange {
	v := newVersionAccumulator()
	seed(v)
	for _, call := range calls {
		v.add(versionTestTime(call.day, call.hour), call.call)
	}
	return v.finish().Changes
}

func versionTestTime(day, hour int) time.Time {
	return time.Date(2026, 9, 28+day, hour, 0, 0, 0, time.UTC)
}

func assertVersionChanges(t *testing.T, got []VersionChange, want []wantChange) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("changes=%+v want %+v", got, want)
	}
	for i, change := range got {
		w := want[i]
		if change.Dimension != w.dimension || change.Version != w.version || change.PreviousVersion != w.previous ||
			change.LocationID != w.location || !change.FirstSeenAt.Equal(w.firstSeenAt) ||
			change.Date != w.firstSeenAt.UTC().Format(time.DateOnly) {
			t.Fatalf("change %d=%+v want %+v", i, change, w)
		}
	}
}

func versionAgentCall(day, hour int, version string) versionCall {
	return versionCall{day, hour, callVersions{"office-a", map[string]string{"agent": version}}}
}

func TestVersionChangesFollowDailyMajority(t *testing.T) {
	seedAgent := func(v *versionAccumulator) { v.seed("agent", "", "0.10.0") }
	t.Run("interleaved rollout within a day does not flicker", func(t *testing.T) {
		got := versionChanges(seedAgent, []versionCall{
			versionAgentCall(0, 9, "0.10.0"), versionAgentCall(0, 10, "0.11.0"), versionAgentCall(0, 11, "0.10.0"), versionAgentCall(0, 12, "0.11.0"), versionAgentCall(0, 13, "0.10.0"),
			versionAgentCall(1, 9, "0.10.0"), versionAgentCall(1, 10, "0.11.0"), versionAgentCall(1, 11, "0.11.0"), versionAgentCall(1, 12, "0.10.0"), versionAgentCall(1, 13, "0.11.0"),
		})
		assertVersionChanges(t, got, []wantChange{{"agent", "0.11.0", "0.10.0", "", versionTestTime(1, 10)}})
	})
	t.Run("rollback produces two markers", func(t *testing.T) {
		got := versionChanges(seedAgent, []versionCall{
			versionAgentCall(0, 9, "0.10.0"), versionAgentCall(0, 10, "0.10.0"), versionAgentCall(0, 11, "0.11.0"),
			versionAgentCall(1, 9, "0.10.0"), versionAgentCall(1, 10, "0.11.0"), versionAgentCall(1, 11, "0.11.0"),
			versionAgentCall(3, 9, "0.11.0"), versionAgentCall(3, 10, "0.10.0"), versionAgentCall(3, 11, "0.10.0"),
		})
		assertVersionChanges(t, got, []wantChange{
			{"agent", "0.11.0", "0.10.0", "", versionTestTime(1, 10)},
			{"agent", "0.10.0", "0.11.0", "", versionTestTime(3, 10)},
		})
	})
	t.Run("ties keep the version in effect or pick the earliest", func(t *testing.T) {
		got := versionChanges(seedAgent, []versionCall{
			versionAgentCall(0, 9, "0.11.0"), versionAgentCall(0, 10, "0.10.0"),
			versionAgentCall(1, 9, "0.11.0"), versionAgentCall(1, 10, "0.12.0"), versionAgentCall(1, 11, "0.12.0"), versionAgentCall(1, 12, "0.11.0"),
		})
		assertVersionChanges(t, got, []wantChange{{"agent", "0.11.0", "0.10.0", "", versionTestTime(1, 9)}})
	})
	t.Run("unseeded stream starts silently from its first majority", func(t *testing.T) {
		got := versionChanges(func(*versionAccumulator) {}, []versionCall{
			versionAgentCall(0, 9, "0.11.0"), versionAgentCall(0, 10, "0.10.0"),
			versionAgentCall(1, 9, "0.10.0"), versionAgentCall(1, 10, "0.10.0"),
		})
		assertVersionChanges(t, got, []wantChange{{"agent", "0.10.0", "0.11.0", "", versionTestTime(1, 9)}})
	})
	t.Run("knowledge stays per office and dimensions stay independent", func(t *testing.T) {
		got := versionChanges(func(v *versionAccumulator) {
			v.seed("knowledge", "office-a", "revision-1")
			v.seed("prompts", "", "0.4.0")
		}, []versionCall{
			{0, 9, callVersions{"office-a", map[string]string{"knowledge": "revision-1", "prompts": "0.4.0"}}},
			{0, 10, callVersions{"office-b", map[string]string{"knowledge": "revision-9", "prompts": "0.5.0", "judges": ""}}},
			{0, 11, callVersions{"office-b", map[string]string{"knowledge": "revision-9", "prompts": "0.5.0"}}},
			{1, 9, callVersions{"office-a", map[string]string{"knowledge": "revision-2"}}},
			{1, 10, callVersions{"office-b", map[string]string{"knowledge": "revision-8"}}},
		})
		assertVersionChanges(t, got, []wantChange{
			{"prompts", "0.5.0", "0.4.0", "", versionTestTime(0, 10)},
			{"knowledge", "revision-2", "revision-1", "office-a", versionTestTime(1, 9)},
			{"knowledge", "revision-8", "revision-9", "office-b", versionTestTime(1, 10)},
		})
	})
}

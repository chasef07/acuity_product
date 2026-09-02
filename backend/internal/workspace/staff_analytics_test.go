package workspace

import (
	"testing"
	"time"
)

func TestStaffTaskCompletionClockAndMature48HourGoal(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	now := to.Add(12 * time.Hour)
	onTime, late, young := from.Add(24*time.Hour), from.Add(96*time.Hour), from.Add(156*time.Hour)
	facts := []staffTaskFact{
		{Created: from, Completed: &onTime, State: "COMPLETED"},
		{Created: from.Add(24 * time.Hour), Completed: &late, State: "COMPLETED"},
		{Created: from.Add(144 * time.Hour), Completed: &young, State: "COMPLETED"},
		{Created: from.Add(48 * time.Hour), State: "OPEN"},
		{Created: from.Add(-24 * time.Hour), State: "OPEN"},
	}
	result, days := summarizeStaffTasks(facts, from, to, now)
	if result.Completed != 3 || result.Eligible != 3 || result.Within48Hours != 1 {
		t.Fatalf("wrong task cohort: %+v", result)
	}
	if result.P50Seconds == nil || *result.P50Seconds != 24*3600 {
		t.Fatalf("completion clock: %+v", result)
	}
	if len(days) != 7 || days[1].Completed != 1 || days[2].P50Seconds != nil {
		t.Fatalf("daily completion grouping: %+v", days)
	}
	// Exactly 48 elapsed hours qualifies, and viewing/reopening never grants another clock.
	completed := from.Add(48 * time.Hour)
	exact, _ := summarizeStaffTasks([]staffTaskFact{{Created: from, Completed: &completed, State: "COMPLETED"}, {Created: from, State: "OPEN"}}, from, to, now)
	if exact.Eligible != 2 || exact.Within48Hours != 1 {
		t.Fatalf("48h boundary: %+v", exact)
	}
	empty, _ := summarizeStaffTasks(nil, from, to, now)
	if empty.P50Seconds != nil || empty.Within48HoursPercent != nil {
		t.Fatal("empty samples must not look like zero latency or 0% compliance")
	}
}

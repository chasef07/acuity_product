package interaction

import (
	"fmt"
	"reflect"
	"testing"
)

func TestDistributionPathsKeepEachCallsFullRoute(t *testing.T) {
	text := func(value string) *string { return &value }
	intent := func(value CallIntent) *CallIntent { return &value }
	outcome := func(value CallOutcome) *CallOutcome { return &value }
	a := newDistributionAccumulator()
	a.routes = map[string]*AnalyticsPathCount{}
	a.add(text("new_patient_booking"), text("abandoned"), true, nil)
	a.add(text("new_patient_booking"), text("abandoned"), true, nil)
	a.add(text("new_patient_booking"), text("abandoned"), true, []string{"knowledge_available"})
	a.add(text("speak_to_staff"), text("transferred"), true, []string{"transfer_policy_followed", "knowledge_available"})
	a.add(text("speak_to_staff"), nil, false, nil)
	a.add(nil, nil, false, nil)

	got := a.paths()
	want := []AnalyticsPathCount{
		{Intent: intent("new_patient_booking"), Outcome: outcome("abandoned"), Evaluated: true, Failures: []string{}, Calls: 2},
		{Intent: intent("new_patient_booking"), Outcome: outcome("abandoned"), Evaluated: true, Failures: []string{"knowledge_available"}, Calls: 1},
		{Intent: intent("speak_to_staff"), Outcome: outcome("transferred"), Evaluated: true, Failures: []string{"knowledge_available", "transfer_policy_followed"}, Calls: 1},
		{Intent: intent("speak_to_staff"), Failures: []string{}, Calls: 1},
		{Failures: []string{}, Calls: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths:\n got %s\nwant %s", describePaths(got), describePaths(want))
	}
	total := 0
	for _, path := range got {
		total += path.Calls
	}
	if calls := a.finish().TotalCalls; total != calls {
		t.Fatalf("paths cover %d calls, want every call (%d)", total, calls)
	}
}

func describePaths(paths []AnalyticsPathCount) string {
	out := ""
	for _, path := range paths {
		out += fmt.Sprintf("%s=%d ", pathKey(path), path.Calls)
	}
	return out
}

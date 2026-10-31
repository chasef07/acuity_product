package interaction

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestAnalyticsFactsVersionTracksScorecardCatalogs(t *testing.T) {
	catalog := fmt.Sprint(ScorecardJudgeVersion, juryScorecardVersions, scorecardNoAtOrBelow)
	for _, version := range juryScorecardVersions {
		catalog += fmt.Sprint(version, juryQuestions(version))
		for _, question := range ScorecardQuestionsForVersion(version) {
			catalog += fmt.Sprint(question.Key, question.Source, question.Failure, question.Label)
		}
	}
	const pinned = "1b0aeee2fb671fdaef063dda81850c583dfc61440baa4a96d87c996101861f06"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(catalog))); analyticsFactsVersion != 5 || got != pinned {
		t.Fatalf("scorecard catalogs changed (fingerprint %s): bump analyticsFactsVersion (now %d) and re-pin", got, analyticsFactsVersion)
	}
}

func TestStoredAnalyticsFactsRejectMalformedRows(t *testing.T) {
	version := analyticsFactsVersion
	stale := analyticsFactsVersion - 1
	questions := len(newJudgeAccumulator().summary.Questions)
	results := func(value string) []string {
		values := make([]string, questions)
		for i := range values {
			values[i] = value
		}
		return values
	}
	valid := func() analyticsFactsRow {
		return analyticsFactsRow{
			version: &version, toolNames: []string{"lookup"}, toolCallIDs: []string{"a"}, toolStatuses: []string{"SUCCESS"}, toolDurations: []*int{nil},
			footprint: []int{1, 2, 3, 4}, juryModels: []string{"a", "b"}, juryResults: results(juryQuestionQuorumMet), juryVotes: results("yn"),
		}
	}
	if _, err := valid().facts(questions); err != nil {
		t.Fatalf("valid row rejected: %v", err)
	}
	for name, change := range map[string]func(*analyticsFactsRow){
		"missing version":        func(row *analyticsFactsRow) { row.version = nil },
		"older version":          func(row *analyticsFactsRow) { row.version = &stale },
		"tool arrays disagree":   func(row *analyticsFactsRow) { row.toolStatuses = nil },
		"footprint length":       func(row *analyticsFactsRow) { row.footprint = []int{1, 2, 3} },
		"results without models": func(row *analyticsFactsRow) { row.juryModels = nil },
		"question count":         func(row *analyticsFactsRow) { row.juryResults = row.juryResults[1:] },
		"unknown result":         func(row *analyticsFactsRow) { row.juryResults[0] = "maybe" },
		"vote count":             func(row *analyticsFactsRow) { row.juryVotes[0] = "y" },
		"vote value":             func(row *analyticsFactsRow) { row.juryVotes[0] = "yx" },
	} {
		row := valid()
		change(&row)
		if _, err := row.facts(questions); err == nil {
			t.Errorf("%s: malformed facts were accepted", name)
		}
	}
}

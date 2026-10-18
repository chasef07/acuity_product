package interaction

import "sort"

type AnalyticsIntentCount struct {
	Intent CallIntent
	Calls  int
}

type AnalyticsOutcomeCount struct {
	Outcome CallOutcome
	Calls   int
}

type AnalyticsFailureCount struct {
	Key   string
	Label string
	Calls int
}

type AnalyticsDistributions struct {
	AnalyticsDistributionCounts
	Daily []AnalyticsDistributionDay
}

type AnalyticsDistributionDay struct {
	Date string
	AnalyticsDistributionCounts
}

type AnalyticsDistributionCounts struct {
	Intents               []AnalyticsIntentCount
	Outcomes              []AnalyticsOutcomeCount
	Failures              []AnalyticsFailureCount
	UnlabeledIntentCalls  int
	UnlabeledOutcomeCalls int
	EvaluatedCalls        int
	FailedCalls           int
	UnevaluatedCalls      int
}

type distributionAccumulator struct {
	summary  AnalyticsDistributionCounts
	intents  map[CallIntent]int
	outcomes map[CallOutcome]int
	failures map[string]int
}

func newDistributionAccumulator() *distributionAccumulator {
	return &distributionAccumulator{intents: map[CallIntent]int{}, outcomes: map[CallOutcome]int{}, failures: map[string]int{}}
}

func (a *distributionAccumulator) add(intent, outcome *string, evaluated bool, failures []string) {
	if intent == nil {
		a.summary.UnlabeledIntentCalls++
	} else {
		a.intents[CallIntent(*intent)]++
	}
	if outcome == nil {
		a.summary.UnlabeledOutcomeCalls++
	} else {
		a.outcomes[CallOutcome(*outcome)]++
	}
	if evaluated {
		a.summary.EvaluatedCalls++
	} else {
		a.summary.UnevaluatedCalls++
	}
	if len(failures) > 0 {
		a.summary.FailedCalls++
	}
	for _, key := range failures {
		a.failures[key]++
	}
}

func (a *distributionAccumulator) finish() AnalyticsDistributionCounts {
	result := a.summary
	result.Intents = []AnalyticsIntentCount{}
	result.Outcomes = []AnalyticsOutcomeCount{}
	result.Failures = []AnalyticsFailureCount{}
	for intent, calls := range a.intents {
		result.Intents = append(result.Intents, AnalyticsIntentCount{Intent: intent, Calls: calls})
	}
	for outcome, calls := range a.outcomes {
		result.Outcomes = append(result.Outcomes, AnalyticsOutcomeCount{Outcome: outcome, Calls: calls})
	}
	for _, version := range []string{ScorecardJudgeVersion, legacyJudgeVersion, previousJudgeVersion} {
		for _, question := range juryQuestions(version) {
			if calls, ok := a.failures[question.key]; ok {
				result.Failures = append(result.Failures, AnalyticsFailureCount{Key: question.key, Label: question.failureLabel, Calls: calls})
				delete(a.failures, question.key)
			}
		}
	}
	sort.Slice(result.Intents, func(i, j int) bool {
		if result.Intents[i].Calls != result.Intents[j].Calls {
			return result.Intents[i].Calls > result.Intents[j].Calls
		}
		return result.Intents[i].Intent < result.Intents[j].Intent
	})
	sort.Slice(result.Outcomes, func(i, j int) bool {
		if result.Outcomes[i].Calls != result.Outcomes[j].Calls {
			return result.Outcomes[i].Calls > result.Outcomes[j].Calls
		}
		return result.Outcomes[i].Outcome < result.Outcomes[j].Outcome
	})
	sort.Slice(result.Failures, func(i, j int) bool {
		if result.Failures[i].Calls != result.Failures[j].Calls {
			return result.Failures[i].Calls > result.Failures[j].Calls
		}
		return result.Failures[i].Key < result.Failures[j].Key
	})
	return result
}

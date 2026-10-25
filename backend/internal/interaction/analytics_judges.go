package interaction

import (
	"encoding/json"
	"sort"
	"strings"
)

type AnalyticsJudgePair struct {
	Left      string
	Right     string
	Compared  int
	Agreed    int
	Disagreed int
}

type AnalyticsJudgeModel struct {
	Model        string
	Votes        int
	MissingVotes int
}

type AnalyticsJudgeLineup struct {
	Models        []string
	RequiredVotes int
	Calls         int
}

type AnalyticsJudgeQuestion struct {
	Key           string
	Label         string
	Prompt        string
	Question      string
	Yes           string
	No            string
	Attempted     int
	QuorumMet     int
	NoQuorum      int
	Unknown       int
	NotApplicable int
	Unavailable   int
	Pairs         []AnalyticsJudgePair
}

type AnalyticsJudges struct {
	Version          string
	TotalCalls       int
	ReportedCalls    int
	UnavailableCalls int
	Models           []AnalyticsJudgeModel
	Lineups          []AnalyticsJudgeLineup
	Questions        []AnalyticsJudgeQuestion
	Pairs            []AnalyticsJudgePair
	YesAbove         float64
}

type judgePairKey struct{ left, right string }

type judgeAccumulator struct {
	summary       AnalyticsJudges
	models        map[string]*AnalyticsJudgeModel
	lineups       map[string]*AnalyticsJudgeLineup
	pairs         map[judgePairKey]AnalyticsJudgePair
	questionPairs []map[judgePairKey]AnalyticsJudgePair
}

func newJudgeAccumulator() *judgeAccumulator {
	a := &judgeAccumulator{
		summary: AnalyticsJudges{Version: ScorecardJudgeVersion, YesAbove: scorecardNoAtOrBelow, Models: []AnalyticsJudgeModel{}, Lineups: []AnalyticsJudgeLineup{}, Questions: []AnalyticsJudgeQuestion{}, Pairs: []AnalyticsJudgePair{}},
		models:  map[string]*AnalyticsJudgeModel{}, lineups: map[string]*AnalyticsJudgeLineup{}, pairs: map[judgePairKey]AnalyticsJudgePair{},
	}
	for _, q := range ScorecardQuestionsForVersion(ScorecardJudgeVersion) {
		if q.Source == "judge" {
			a.summary.Questions = append(a.summary.Questions, AnalyticsJudgeQuestion{Key: q.Key, Label: q.Label, Prompt: q.Prompt, Question: q.Question, Yes: q.Yes, No: q.No, Pairs: []AnalyticsJudgePair{}})
			a.questionPairs = append(a.questionPairs, map[judgePairKey]AnalyticsJudgePair{})
		}
	}
	return a
}

const (
	juryQuestionUnavailable          = "unavailable"
	juryQuestionUnknown              = "unknown"
	juryQuestionNotApplicable        = "not_applicable"
	juryQuestionNoQuorum             = "no_quorum"
	juryQuestionQuorumMet            = "quorum_met"
	juryQuestionAttemptedUnavailable = "attempted_unavailable"
)

const (
	juryVoteYes     = 'y'
	juryVoteNo      = 'n'
	juryVoteMissing = '-'
)

type juryFacts struct {
	Models  []string
	Results []string
	Votes   []string
}

func readJuryFacts(raw json.RawMessage) juryFacts {
	jury, valid := readJury(raw)
	roster := map[string]bool{}
	for _, model := range jury.Jurors {
		if roster[model] || strings.TrimSpace(model) != model {
			valid = false
		}
		roster[model] = true
	}
	if !valid || jury.Version != ScorecardJudgeVersion {
		return juryFacts{}
	}
	models := append([]string{}, jury.Jurors...)
	sort.Strings(models)
	required := len(models)/2 + 1
	facts := juryFacts{Models: models, Results: []string{}, Votes: []string{}}
	for _, q := range ScorecardQuestionsForVersion(ScorecardJudgeVersion) {
		if q.Source != "judge" {
			continue
		}
		outcome, votes := readJuryQuestion(jury, q.Key, models, required)
		facts.Results = append(facts.Results, outcome)
		facts.Votes = append(facts.Votes, votes)
	}
	return facts
}

func readJuryQuestion(jury juryEvaluation, key string, models []string, required int) (string, string) {
	var result struct {
		Status      string                     `json:"status"`
		Cause       string                     `json:"cause"`
		Verdict     json.RawMessage            `json:"verdict"`
		Probability json.RawMessage            `json:"probability"`
		Votes       map[string]json.RawMessage `json:"votes"`
	}
	source, failed := jury.Errors[key]
	if !failed {
		source = jury.Results[key]
	}
	if json.Unmarshal(source, &result) != nil {
		return juryQuestionUnavailable, ""
	}
	if !failed && result.Status == "unknown" {
		return juryQuestionUnknown, ""
	}
	if !failed && result.Status == "not_applicable" {
		return juryQuestionNotApplicable, ""
	}
	if result.Votes == nil || failed && result.Cause != "no_quorum" || !failed && result.Status != "" {
		return juryQuestionUnavailable, ""
	}
	votes := make([]byte, len(models))
	cast, yes := 0, 0
	sum := 0.0
	for i, model := range models {
		var probability *float64
		if json.Unmarshal(result.Votes[model], &probability) != nil || !validProbability(probability) {
			votes[i] = juryVoteMissing
			continue
		}
		cast++
		sum += *probability
		votes[i] = juryVoteNo
		if *probability > scorecardNoAtOrBelow {
			votes[i] = juryVoteYes
			yes++
		}
	}
	var verdict *bool
	var probability *float64
	validConsensus := json.Unmarshal(result.Verdict, &verdict) == nil && verdict != nil &&
		json.Unmarshal(result.Probability, &probability) == nil && validProbability(probability)
	outcome := juryQuestionAttemptedUnavailable
	if failed {
		if cast < required {
			outcome = juryQuestionNoQuorum
		}
	} else if cast >= required && validConsensus && *verdict == (yes > cast-yes || yes == cast-yes && sum/float64(cast) > scorecardNoAtOrBelow) {
		outcome = juryQuestionQuorumMet
	}
	return outcome, string(votes)
}

func (a *judgeAccumulator) add(facts juryFacts) {
	a.summary.TotalCalls++
	if facts.Models == nil {
		a.summary.UnavailableCalls++
		return
	}
	a.summary.ReportedCalls++
	models := facts.Models
	key, _ := json.Marshal(models)
	if a.lineups[string(key)] == nil {
		a.lineups[string(key)] = &AnalyticsJudgeLineup{Models: models, RequiredVotes: len(models)/2 + 1}
	}
	a.lineups[string(key)].Calls++
	for _, model := range models {
		if a.models[model] == nil {
			a.models[model] = &AnalyticsJudgeModel{Model: model}
		}
	}
	for i := range a.summary.Questions {
		q := &a.summary.Questions[i]
		switch facts.Results[i] {
		case juryQuestionUnavailable:
			q.Unavailable++
			continue
		case juryQuestionUnknown:
			q.Unknown++
			continue
		case juryQuestionNotApplicable:
			q.NotApplicable++
			continue
		case juryQuestionNoQuorum:
			q.NoQuorum++
		case juryQuestionQuorumMet:
			q.QuorumMet++
		case juryQuestionAttemptedUnavailable:
			q.Unavailable++
		}
		q.Attempted++
		votes := map[string]bool{}
		for j, model := range models {
			if facts.Votes[i][j] == juryVoteMissing {
				a.models[model].MissingVotes++
				continue
			}
			a.models[model].Votes++
			votes[model] = facts.Votes[i][j] == juryVoteYes
		}
		for leftIndex, left := range models {
			leftVote, present := votes[left]
			if !present {
				continue
			}
			for _, right := range models[leftIndex+1:] {
				rightVote, present := votes[right]
				if !present {
					continue
				}
				pair := judgePairKey{left: left, right: right}
				for _, counts := range []map[judgePairKey]AnalyticsJudgePair{a.pairs, a.questionPairs[i]} {
					count := counts[pair]
					count.Left, count.Right = left, right
					count.Compared++
					if leftVote == rightVote {
						count.Agreed++
					} else {
						count.Disagreed++
					}
					counts[pair] = count
				}
			}
		}
	}
}

func sortedJudgePairs(counts map[judgePairKey]AnalyticsJudgePair) []AnalyticsJudgePair {
	pairs := make([]AnalyticsJudgePair, 0, len(counts))
	for _, pair := range counts {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Left != pairs[j].Left {
			return pairs[i].Left < pairs[j].Left
		}
		return pairs[i].Right < pairs[j].Right
	})
	return pairs
}

func (a *judgeAccumulator) finish() AnalyticsJudges {
	result := a.summary
	for _, model := range a.models {
		result.Models = append(result.Models, *model)
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].Model < result.Models[j].Model })
	for _, lineup := range a.lineups {
		result.Lineups = append(result.Lineups, *lineup)
	}
	sort.Slice(result.Lineups, func(i, j int) bool {
		left, _ := json.Marshal(result.Lineups[i].Models)
		right, _ := json.Marshal(result.Lineups[j].Models)
		return string(left) < string(right)
	})
	for i := range result.Questions {
		result.Questions[i].Pairs = sortedJudgePairs(a.questionPairs[i])
	}
	result.Pairs = sortedJudgePairs(a.pairs)
	return result
}

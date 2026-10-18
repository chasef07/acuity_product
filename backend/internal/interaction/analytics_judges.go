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

func (a *judgeAccumulator) add(raw json.RawMessage) {
	a.summary.TotalCalls++
	jury, valid := readJury(raw)
	roster := map[string]bool{}
	for _, model := range jury.Jurors {
		if roster[model] || strings.TrimSpace(model) != model {
			valid = false
		}
		roster[model] = true
	}
	if !valid || jury.Version != ScorecardJudgeVersion {
		a.summary.UnavailableCalls++
		return
	}
	a.summary.ReportedCalls++
	models := append([]string{}, jury.Jurors...)
	sort.Strings(models)
	required := len(models)/2 + 1
	key, _ := json.Marshal(models)
	if a.lineups[string(key)] == nil {
		a.lineups[string(key)] = &AnalyticsJudgeLineup{Models: models, RequiredVotes: required}
	}
	a.lineups[string(key)].Calls++
	for _, model := range models {
		if a.models[model] == nil {
			a.models[model] = &AnalyticsJudgeModel{Model: model}
		}
	}
	for i := range a.summary.Questions {
		q := &a.summary.Questions[i]
		var result struct {
			Status      string                     `json:"status"`
			Cause       string                     `json:"cause"`
			Verdict     json.RawMessage            `json:"verdict"`
			Probability json.RawMessage            `json:"probability"`
			Votes       map[string]json.RawMessage `json:"votes"`
		}
		source, failed := jury.Errors[q.Key]
		if !failed {
			source = jury.Results[q.Key]
		}
		if json.Unmarshal(source, &result) != nil {
			q.Unavailable++
			continue
		}
		if !failed && result.Status == "unknown" {
			q.Unknown++
			continue
		}
		if !failed && result.Status == "not_applicable" {
			q.NotApplicable++
			continue
		}
		if result.Votes == nil || failed && result.Cause != "no_quorum" || !failed && result.Status != "" {
			q.Unavailable++
			continue
		}
		q.Attempted++
		votes := map[string]bool{}
		yes := 0
		sum := 0.0
		for _, model := range models {
			var probability *float64
			if json.Unmarshal(result.Votes[model], &probability) != nil || !validProbability(probability) {
				a.models[model].MissingVotes++
				continue
			}
			a.models[model].Votes++
			votes[model] = *probability > scorecardNoAtOrBelow
			sum += *probability
			if votes[model] {
				yes++
			}
		}
		var verdict *bool
		var probability *float64
		validConsensus := json.Unmarshal(result.Verdict, &verdict) == nil && verdict != nil &&
			json.Unmarshal(result.Probability, &probability) == nil && validProbability(probability)

		if failed {
			if len(votes) < required {
				q.NoQuorum++
			} else {
				q.Unavailable++
			}
		} else if len(votes) >= required && validConsensus && *verdict == (yes > len(votes)-yes || yes == len(votes)-yes && sum/float64(len(votes)) > scorecardNoAtOrBelow) {
			q.QuorumMet++
		} else {
			q.Unavailable++
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

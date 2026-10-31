package interaction

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAnalyticsJudgesValidateVotesAndQuorum(t *testing.T) {
	for _, test := range []struct {
		name, models, result, errors                                                                                  string
		attempted, quorum, noQuorum, unknown, notApplicable, unavailable, compared, agreed, disagreed, votes, missing int
	}{
		{name: "strict boundary and split", models: `["c","a","b"]`, result: `{"verdict":true,"probability":0.6,"votes":{"a":0.4,"b":0.4001,"c":1}}`, attempted: 1, quorum: 1, compared: 3, agreed: 1, disagreed: 2, votes: 3},
		{name: "majority beats mean", models: `["a","b","c"]`, result: `{"verdict":false,"probability":0.4666666667,"votes":{"a":0,"b":0.4,"c":1}}`, attempted: 1, quorum: 1, compared: 3, agreed: 1, disagreed: 2, votes: 3},
		{name: "tie mean at boundary is no", models: `["a","b"]`, result: `{"verdict":false,"probability":0.4,"votes":{"a":0.2,"b":0.6}}`, attempted: 1, quorum: 1, compared: 1, disagreed: 1, votes: 2},
		{name: "tie mean above boundary is yes", models: `["a","b"]`, result: `{"verdict":true,"probability":0.6,"votes":{"a":0.4,"b":0.8}}`, attempted: 1, quorum: 1, compared: 1, disagreed: 1, votes: 2},
		{name: "invalid and extraneous votes", models: `["a","b","c"]`, result: `{"verdict":true,"probability":0.9,"votes":{"a":0.9,"b":null,"c":"0.2","outsider":0.2}}`, attempted: 1, unavailable: 1, votes: 1, missing: 2},
		{name: "out of range and nonfinite votes", models: `["a","b","c"]`, result: `{"verdict":true,"probability":0.9,"votes":{"a":-0.1,"b":1.1,"c":1e999}}`, attempted: 1, unavailable: 1, missing: 3},
		{name: "no quorum keeps shared votes", models: `["a","b","c","d","e"]`, errors: `{"cause":"no_quorum","votes":{"a":0.2,"b":0.8}}`, attempted: 1, noQuorum: 1, compared: 1, disagreed: 1, votes: 2, missing: 3},
		{name: "empty no quorum votes", models: `["a","b","c"]`, errors: `{"cause":"no_quorum","votes":{}}`, attempted: 1, noQuorum: 1, missing: 3},
		{name: "no quorum contradicts sufficient votes", models: `["a","b","c"]`, errors: `{"cause":"no_quorum","votes":{"a":0.2,"b":0.8}}`, attempted: 1, unavailable: 1, compared: 1, disagreed: 1, votes: 2, missing: 1},
		{name: "unknown makes no vote opportunities", models: `["a","b","c"]`, result: `{"status":"unknown","votes":{"a":0.2}}`, unknown: 1},
		{name: "not applicable makes no vote opportunities", models: `["a","b","c"]`, result: `{"status":"not_applicable"}`, notApplicable: 1},
		{name: "unrecognized error", models: `["a","b","c"]`, errors: `{"cause":"timeout","votes":{"a":0.2}}`, unavailable: 1},
		{name: "missing result", models: `["a","b","c"]`, unavailable: 1},
		{name: "null votes", models: `["a","b","c"]`, result: `{"verdict":true,"probability":0.9,"votes":null}`, unavailable: 1},
		{name: "missing verdict keeps observed comparisons", models: `["a","b","c"]`, result: `{"probability":0.9,"votes":{"a":0.8,"b":1}}`, attempted: 1, unavailable: 1, compared: 1, agreed: 1, votes: 2, missing: 1},
		{name: "contradictory verdict is unavailable", models: `["a","b","c"]`, result: `{"verdict":false,"probability":0.9,"votes":{"a":0.8,"b":1}}`, attempted: 1, unavailable: 1, compared: 1, agreed: 1, votes: 2, missing: 1},
		{name: "string verdict keeps observed comparisons", models: `["a","b","c"]`, result: `{"verdict":"true","probability":0.9,"votes":{"a":0.8,"b":1}}`, attempted: 1, unavailable: 1, compared: 1, agreed: 1, votes: 2, missing: 1},
		{name: "string probability keeps observed comparisons", models: `["a","b","c"]`, result: `{"verdict":true,"probability":"0.9","votes":{"a":0.8,"b":1}}`, attempted: 1, unavailable: 1, compared: 1, agreed: 1, votes: 2, missing: 1},
		{name: "invalid consensus probability", models: `["a","b","c"]`, result: `{"verdict":true,"probability":2,"votes":{"a":0.8,"b":1}}`, attempted: 1, unavailable: 1, compared: 1, agreed: 1, votes: 2, missing: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, errors := `{}`, `{}`
			if test.result != "" {
				result = `{"office_question_answered":` + test.result + `}`
			}
			if test.errors != "" {
				errors = `{"office_question_answered":` + test.errors + `}`
			}
			raw := json.RawMessage(`{"evaluatorVersion":"typesafe-scorecard-v12","status":"incomplete","jurors":` + test.models + `,"results":` + result + `,"errors":` + errors + `}`)
			a := newJudgeAccumulator()
			a.add(readJuryFacts(raw))
			summary := a.finish()
			if summary.TotalCalls != 1 || summary.ReportedCalls != 1 || summary.UnavailableCalls != 0 || summary.YesAbove != 0.4 {
				t.Fatalf("coverage: %+v", summary)
			}
			var q AnalyticsJudgeQuestion
			for _, row := range summary.Questions {
				if row.Key == "office_question_answered" {
					q = row
				}
			}
			if q.Attempted != test.attempted || q.QuorumMet != test.quorum || q.NoQuorum != test.noQuorum || q.Unknown != test.unknown || q.NotApplicable != test.notApplicable || q.Unavailable != test.unavailable {
				t.Fatalf("question: %+v", q)
			}
			compared, agreed, disagreed := 0, 0, 0
			for _, pair := range summary.Pairs {
				if pair.Left >= pair.Right || pair.Compared != pair.Agreed+pair.Disagreed {
					t.Fatalf("pair symmetry or reconciliation: %+v", pair)
				}
				compared += pair.Compared
				agreed += pair.Agreed
				disagreed += pair.Disagreed
			}
			if compared != test.compared || agreed != test.agreed || disagreed != test.disagreed || !reflect.DeepEqual(summary.Pairs, q.Pairs) {
				t.Fatalf("pairs: %+v", summary.Pairs)
			}
			votes, missing := 0, 0
			for _, model := range summary.Models {
				if model.Model == "outsider" {
					t.Fatal("extraneous model leaked")
				}
				votes += model.Votes
				missing += model.MissingVotes
			}
			if votes != test.votes || missing != test.missing {
				t.Fatalf("models: %+v", summary.Models)
			}
			for _, question := range summary.Questions {
				if question.QuorumMet+question.NoQuorum+question.Unknown+question.NotApplicable+question.Unavailable != summary.ReportedCalls {
					t.Fatalf("question status coverage: %+v", question)
				}
			}
		})
	}
}

func TestAnalyticsJudgesExcludeUnsupportedAndMalformedReports(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"evaluatorVersion":"typesafe-scorecard-v8","status":"complete","jurors":["a"]}`, `{"evaluatorVersion":"typesafe-scorecard-v12","status":"complete","jurors":["a","a"]}`, `{"evaluatorVersion":"typesafe-scorecard-v12","status":"complete","jurors":[" "]}`, `{"evaluatorVersion":"typesafe-scorecard-v12","status":"skipped","jurors":["a"]}`} {
		a := newJudgeAccumulator()
		a.add(readJuryFacts(json.RawMessage(raw)))
		summary := a.finish()
		if summary.TotalCalls != 1 || summary.ReportedCalls != 0 || summary.UnavailableCalls != 1 || len(summary.Models)+len(summary.Lineups)+len(summary.Pairs) != 0 {
			t.Fatalf("excluded report leaked: %+v", summary)
		}
	}
}

func TestAnalyticsJudgesAggregateCanonicalLineupsAndQuestionPairs(t *testing.T) {
	a := newJudgeAccumulator()
	for _, models := range []string{`["b","a"]`, `["a","b"]`} {
		a.add(readJuryFacts(json.RawMessage(`{"evaluatorVersion":"typesafe-scorecard-v12","status":"complete","jurors":` + models + `,"results":{"office_question_answered":{"verdict":true,"probability":0.6,"votes":{"a":0.4,"b":0.8}},"call_ended_correctly":{"verdict":true,"probability":0.8,"votes":{"a":0.8,"b":0.8}},"expressed_sentiment":{"score":4,"votes":{"a":4,"b":4}}}}`)))
	}
	summary := a.finish()
	if len(summary.Lineups) != 1 || summary.Lineups[0].Calls != 2 || summary.Lineups[0].RequiredVotes != 2 || !reflect.DeepEqual(summary.Lineups[0].Models, []string{"a", "b"}) {
		t.Fatalf("lineups: %+v", summary.Lineups)
	}
	if !reflect.DeepEqual(summary.Pairs, []AnalyticsJudgePair{{Left: "a", Right: "b", Compared: 4, Agreed: 2, Disagreed: 2}}) {
		t.Fatalf("aggregate pairs: %+v", summary.Pairs)
	}
	if len(summary.Questions) != 7 || len(summary.Models) != 2 || summary.Models[0].Votes != 4 || summary.Models[1].Votes != 4 {
		t.Fatalf("boolean observations: %+v", summary)
	}
	for _, q := range summary.Questions {
		if q.Key == "expressed_sentiment" || q.Prompt == "" || q.Question == "" || q.Yes == "" || q.No == "" {
			t.Fatalf("criteria: %+v", q)
		}
	}
}

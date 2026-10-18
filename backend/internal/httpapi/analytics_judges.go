package httpapi

import (
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
)

func analyticsJudgesResponse(value interaction.AnalyticsJudges) *api.OperatorAIAnalyticsJudges {
	result := &api.OperatorAIAnalyticsJudges{
		Version: value.Version, TotalCalls: value.TotalCalls, ReportedCalls: value.ReportedCalls, UnavailableCalls: value.UnavailableCalls, YesAbove: value.YesAbove,
		Models: make([]api.OperatorAIJudgeModel, 0, len(value.Models)), Lineups: make([]api.OperatorAIJudgeLineup, 0, len(value.Lineups)), Questions: make([]api.OperatorAIJudgeQuestion, 0, len(value.Questions)), Pairs: analyticsJudgePairsResponse(value.Pairs),
	}
	for _, model := range value.Models {
		result.Models = append(result.Models, api.OperatorAIJudgeModel{Model: model.Model, Votes: model.Votes, MissingVotes: model.MissingVotes})
	}
	for _, lineup := range value.Lineups {
		result.Lineups = append(result.Lineups, api.OperatorAIJudgeLineup{Models: lineup.Models, RequiredVotes: lineup.RequiredVotes, Calls: lineup.Calls})
	}
	for _, q := range value.Questions {
		result.Questions = append(result.Questions, api.OperatorAIJudgeQuestion{Key: q.Key, Label: q.Label, Prompt: q.Prompt, Question: q.Question, Yes: q.Yes, No: q.No, Attempted: q.Attempted, QuorumMet: q.QuorumMet, NoQuorum: q.NoQuorum, Unknown: q.Unknown, NotApplicable: q.NotApplicable, Unavailable: q.Unavailable, Pairs: analyticsJudgePairsResponse(q.Pairs)})
	}
	return result
}

func analyticsJudgePairsResponse(values []interaction.AnalyticsJudgePair) []api.OperatorAIJudgePair {
	result := make([]api.OperatorAIJudgePair, 0, len(values))
	for _, pair := range values {
		result = append(result, api.OperatorAIJudgePair{Left: pair.Left, Right: pair.Right, Compared: pair.Compared, Agreed: pair.Agreed, Disagreed: pair.Disagreed})
	}
	return result
}

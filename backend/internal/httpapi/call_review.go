package httpapi

import (
	"net/http"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (server *Server) operatorIdentity(w http.ResponseWriter, r *http.Request) (access.Identity, bool) {
	if !server.portalOnly(w, r) {
		return access.Identity{}, false
	}
	return server.authenticate(w, r)
}

func (server *Server) GetOperatorScorecardQuestions(w http.ResponseWriter, r *http.Request) {
	if _, ok := server.operatorIdentity(w, r); !ok {
		return
	}
	server.writeJSON(w, http.StatusOK, map[string]any{
		"judgeVersion": interaction.ScorecardJudgeVersion,
		"codeVersion":  interaction.ScorecardCodeVersion,
		"questions":    interaction.ScorecardQuestions,
	})
}

func (server *Server) QueryOperatorScorecard(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.operatorIdentity(w, r)
	if !ok {
		return
	}
	var body api.OperatorScorecardQueryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, finish, ok := server.beginAnalytics(w, r)
	if !ok {
		return
	}
	defer finish()
	report, err := server.interactions.QueryScorecardReport(ctx, interaction.ScorecardReportCommand{
		Identity: identity, PracticeID: body.PracticeId.String(), LocationID: uuidString(body.LocationId),
		Weeks: body.Weeks, TimeZone: body.TimeZone,
	})
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, report)
}

func (server *Server) OpenOperatorCallReviewQueue(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.operatorIdentity(w, r)
	if !ok {
		return
	}
	var body api.OperatorCallReviewQueueRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	queue, err := server.interactions.OpenReviewQueue(ctx, interaction.ReviewQueueCommand{
		Identity: identity, PracticeID: body.PracticeId.String(), Date: body.Date.String(), TimeZone: body.TimeZone,
	})
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, queue)
}

func (server *Server) QueryOperatorJudgeAccuracy(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.operatorIdentity(w, r)
	if !ok {
		return
	}
	var body api.OperatorJudgeAccuracyRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, finish, ok := server.beginAnalytics(w, r)
	if !ok {
		return
	}
	defer finish()
	accuracy, err := server.interactions.QueryJudgeAccuracy(ctx, identity, body.PracticeId.String())
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, accuracy)
}

func (server *Server) GetOperatorCallReview(w http.ResponseWriter, r *http.Request, interactionID openapi_types.UUID) {
	identity, ok := server.operatorIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	review, err := server.interactions.ReadCallReview(ctx, identity, interactionID.String())
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, review)
}

func (server *Server) SubmitOperatorCallReview(w http.ResponseWriter, r *http.Request, interactionID openapi_types.UUID) {
	identity, ok := server.operatorIdentity(w, r)
	if !ok {
		return
	}
	var body api.OperatorCallReviewSubmission
	if !server.decodeJSON(w, r, &body) {
		return
	}
	submission := interaction.CallReviewSubmission{Note: body.Note}
	for _, answer := range body.Answers {
		submission.Answers = append(submission.Answers, interaction.ReviewAnswer{Question: answer.Question, Answer: answer.Answer, Note: answer.Note})
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	review, err := server.interactions.SubmitCallReview(ctx, identity, interactionID.String(), submission)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, review)
}

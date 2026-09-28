package httpapi

import (
	"net/http"

	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (server *Server) ReviewOperatorAICallIssue(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.ReviewOperatorAICallIssueJSONRequestBody
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	issue, err := server.interactions.ReviewCallIssue(ctx, identity, id.String(), interaction.CallIssueOutcome(body.Outcome))
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	response, err := operatorAICallIssueResponse(issue)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func operatorAICallIssuesResponse(issues []interaction.OperatorCallIssue) (*[]api.OperatorAICallIssue, error) {
	response := make([]api.OperatorAICallIssue, 0, len(issues))
	for _, issue := range issues {
		value, err := operatorAICallIssueResponse(issue)
		if err != nil {
			return nil, err
		}
		response = append(response, value)
	}
	return &response, nil
}

func operatorAICallIssueResponse(issue interaction.OperatorCallIssue) (api.OperatorAICallIssue, error) {
	id, err := uuid.Parse(issue.InteractionID)
	response := api.OperatorAICallIssue{
		InteractionId: id,
		Phone:         issue.Phone,
		StartedAt:     issue.StartedAt,
		Reason:        api.AgentCallIssueReason(issue.Reason),
		ReportedBy:    issue.ReportedBy,
		ReportedAt:    issue.ReportedAt,
	}
	if issue.Review != nil {
		response.Review = &api.OperatorAICallIssueReview{
			Outcome:    api.OperatorAICallIssueOutcome(issue.Review.Outcome),
			ReviewedBy: issue.Review.ReviewedBy,
			ReviewedAt: issue.Review.ReviewedAt,
		}
	}
	return response, err
}

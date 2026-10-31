package httpapi

import (
	"context"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"net/http"
)

func (server *Server) QueryAgentCalls(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.AgentCallsQuery
	if !server.decodeJSON(w, r, &body) {
		return
	}
	if !validCallLabelFilters(body.Intent, body.Outcome) {
		server.writeInteractionError(w, r, interaction.ErrInvalidInput)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	page, err := server.interactions.QueryAgentCalls(ctx, interaction.QueryAgentCallsCommand{
		QueryAnalyticsCommand: interaction.QueryAnalyticsCommand{
			Identity:   identity,
			PracticeID: body.PracticeId.String(),
			LocationID: uuidString(body.LocationId),
			Range:      interaction.AnalyticsRange(body.Range),
			Cursor:     stringValue(body.Cursor),
			Limit:      intValue(body.Limit),
			Intent:     stringValue(body.Intent),
			Outcome:    stringValue(body.Outcome),
		},
		Phone: stringValue(body.Phone), FlaggedOnly: body.FlaggedOnly != nil && *body.FlaggedOnly,
		KnowledgeGapOnly: body.KnowledgeGapOnly != nil && *body.KnowledgeGapOnly,
		InsuranceGapOnly: body.InsuranceGapOnly != nil && *body.InsuranceGapOnly,
	})
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, page)
}
func (server *Server) GetAgentCall(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	detail, err := server.interactions.ReadAgentCall(ctx, identity, id.String())
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, detail)
}
func (server *Server) FlagAgentCallIssue(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.AgentCallIssueInput
	if !server.decodeJSON(w, r, &body) {
		return
	}
	note := ""
	if body.Note != nil {
		note = *body.Note
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	issue, err := server.interactions.FlagAgentCallIssue(ctx, identity, id.String(), interaction.AgentCallIssueReason(body.Reason), note)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, issue)
}
func (server *Server) CloseAgentCallKnowledgeGap(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	var body api.KnowledgeGapCloseInput
	server.closeCallGap(w, r, &body, func(ctx context.Context, identity access.Identity) (interaction.GapReview, error) {
		return server.interactions.CloseKnowledgeGap(ctx, identity, id.String(), interaction.GapResolution(body.Resolution))
	})
}
func (server *Server) CloseAgentCallInsuranceGap(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	var body api.InsuranceGapCloseInput
	server.closeCallGap(w, r, &body, func(ctx context.Context, identity access.Identity) (interaction.GapReview, error) {
		return server.interactions.CloseInsuranceGap(ctx, identity, id.String(), interaction.GapResolution(body.Resolution))
	})
}
func (server *Server) closeCallGap(w http.ResponseWriter, r *http.Request, body any, close func(context.Context, access.Identity) (interaction.GapReview, error)) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	if !server.decodeJSON(w, r, body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	review, err := close(ctx, identity)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, review)
}
func validCallLabelFilters(intent *api.CallIntent, outcome *api.CallOutcome) bool {
	return (intent == nil || intent.Valid()) && (outcome == nil || outcome.Valid())
}

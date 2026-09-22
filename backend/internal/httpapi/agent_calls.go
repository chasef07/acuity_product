package httpapi

import (
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
	ctx, cancel := server.requestContext(r)
	defer cancel()
	page, err := server.interactions.QueryAgentCalls(ctx, interaction.QueryAgentCallsCommand{
		QueryAnalyticsCommand: interaction.QueryAnalyticsCommand{Identity: identity, PracticeID: body.PracticeId.String(), LocationID: uuidString(body.LocationId), Range: interaction.AnalyticsRange(body.Range), Cursor: stringValue(body.Cursor), Limit: intValue(body.Limit)},
		Phone:                 stringValue(body.Phone), FlaggedOnly: body.FlaggedOnly != nil && *body.FlaggedOnly,
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
	ctx, cancel := server.requestContext(r)
	defer cancel()
	issue, err := server.interactions.FlagAgentCallIssue(ctx, identity, id.String(), body.Note)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, issue)
}

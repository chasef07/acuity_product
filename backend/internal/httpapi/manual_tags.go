package httpapi

import (
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"net/http"
)

func (server *Server) GetOperatorAICallTags(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	server.operatorCallTags(w, r, id, false)
}
func (server *Server) SetOperatorAICallTag(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	server.operatorCallTags(w, r, id, true)
}
func (server *Server) operatorCallTags(w http.ResponseWriter, r *http.Request, id openapi_types.UUID, write bool) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var change *interaction.ManualTagChange
	if write {
		var body api.SetOperatorAICallTagJSONRequestBody
		if !server.decodeJSON(w, r, &body) {
			return
		}
		change = &interaction.ManualTagChange{Name: body.Name, Applied: body.Applied}
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	tags, err := server.interactions.OperatorManualTags(ctx, identity, id.String(), change)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, api.OperatorAICallTags{Available: tags.Available, Selected: tags.Selected})
}

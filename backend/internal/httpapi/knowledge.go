package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
)

func (server *Server) SearchOfficeKnowledge(w http.ResponseWriter, r *http.Request, params api.SearchOfficeKnowledgeParams) {
	started := time.Now()
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticateService(w, r)
	if !ok {
		return
	}
	if !identity.Allows(access.ServiceCapabilityReadKnowledge) {
		server.writeError(w, r, http.StatusForbidden, "DENIED", "This service cannot search office knowledge.", false)
		return
	}
	var request api.SearchOfficeKnowledgeJSONRequestBody
	if !server.decodeJSONLimit(w, r, &request, 4096) {
		return
	}
	if server.knowledge == nil {
		slog.Warn("office_knowledge_search_failed", "cause", "provider_not_configured", "elapsed_ms", time.Since(started).Milliseconds())
		server.writeKnowledgeUnavailable(w)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	result, err := server.knowledge.Search(ctx, identity, params.XOfficeKey, request.Query)
	switch {
	case errors.Is(err, access.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "DENIED", "This office is outside the service scope.", false)
	case errors.Is(err, knowledge.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Use a short non-patient question of at most 500 characters.", false)
	case err != nil:
		slog.Warn("office_knowledge_search_failed", "cause", knowledge.FailureCode(err), "elapsed_ms", time.Since(started).Milliseconds())
		server.writeKnowledgeUnavailable(w)
	default:
		server.writeJSON(w, http.StatusOK, result)
	}
}
func (server *Server) writeKnowledgeUnavailable(w http.ResponseWriter) {
	server.writeJSON(w, http.StatusServiceUnavailable, knowledge.SearchResult{Outcome: "temporary_failure", Passages: []knowledge.Passage{}})
}

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
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "This service cannot search office knowledge.", false)
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
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "This office is outside the service scope.", false)
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

func (server *Server) QueryLocationKnowledge(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.LocationKnowledgeQuery
	if !server.decodeJSON(w, r, &body) {
		return
	}
	if server.knowledge == nil {
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Knowledge isn't available right now.", false)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	result, err := server.knowledge.ReadLocation(ctx, identity, body.PracticeId.String(), body.LocationId.String())
	switch {
	case err == nil:
		server.writeJSON(w, http.StatusOK, result)
	case errors.Is(err, knowledge.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, access.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, access.ErrNoOfficeRoute):
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Knowledge isn't available for this Location.", false)
	default:
		if r.Context().Err() == nil {
			slog.Warn("location_knowledge_read_failed", "error", err.Error(), "elapsed_ms", time.Since(started).Milliseconds())
		}
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Knowledge isn't available right now.", true)
	}
}

package httpapi

import (
	"context"
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
func (server *Server) ReadOfficeKnowledge(w http.ResponseWriter, r *http.Request, params api.ReadOfficeKnowledgeParams) {
	started := time.Now()
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticateService(w, r)
	if !ok {
		return
	}
	if !identity.Allows(access.ServiceCapabilityReadKnowledge) {
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "This service cannot read office knowledge.", false)
		return
	}
	var empty struct{}
	if !server.decodeJSONLimit(w, r, &empty, 4096) {
		return
	}
	if server.knowledge == nil {
		slog.Warn("office_knowledge_read_failed", "cause", "knowledge_not_configured", "elapsed_ms", time.Since(started).Milliseconds())
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Office knowledge isn't available right now.", true)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	result, err := server.knowledge.Read(ctx, identity, params.XOfficeKey)
	switch {
	case err == nil:
		server.writeJSON(w, http.StatusOK, result)
	case errors.Is(err, access.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "This office is outside the service scope.", false)
	default:
		slog.Warn("office_knowledge_read_failed", "cause", knowledge.FailureCode(err), "elapsed_ms", time.Since(started).Milliseconds())
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Office knowledge isn't available right now.", true)
	}
}

func (server *Server) writeKnowledgeUnavailable(w http.ResponseWriter) {
	server.writeJSON(w, http.StatusServiceUnavailable, knowledge.SearchResult{Outcome: "temporary_failure", Passages: []knowledge.Passage{}})
}

func (server *Server) PublishLocationKnowledge(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.KnowledgePublication
	if !server.decodeJSONLimit(w, r, &body, 4<<20) {
		return
	}
	if server.knowledge == nil {
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Not published. Nothing changed. Try again.", true)
		return
	}
	sections := make([]knowledge.Section, len(body.Sections))
	for i, section := range body.Sections {
		sections[i] = knowledge.Section{ID: section.Id, Title: section.Title, Text: section.Text}
	}
	expected := ""
	if body.ExpectedRevisionId != nil {
		expected = body.ExpectedRevisionId.String()
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	revision, err := server.knowledge.PublishLocation(ctx, knowledge.PublishCommand{
		Identity: identity, PracticeID: body.PracticeId.String(), LocationID: body.LocationId.String(),
		RevisionID: body.RevisionId.String(), ExpectedRevisionID: expected, Reason: body.Reason, Sections: sections,
	})
	var entry *knowledge.EntryError
	var size *knowledge.SizeError
	switch {
	case err == nil:
		server.writeJSON(w, http.StatusOK, revision)
	case errors.As(err, &entry):
		envelope := errorEnvelope(r, "INVALID_KNOWLEDGE_ENTRY", entry.Error(), false)
		envelope.Error.EntryId = &entry.EntryID
		server.writeJSON(w, http.StatusBadRequest, envelope)
	case errors.As(err, &size):
		server.writeError(w, r, http.StatusBadRequest, "KNOWLEDGE_TOO_LARGE", size.Error(), false)
	case errors.Is(err, knowledge.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, access.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, knowledge.ErrConflict):
		server.writeError(w, r, http.StatusConflict, "KNOWLEDGE_CHANGED", "Someone else changed this office's knowledge. Reload to see their changes.", false)
	case errors.Is(err, access.ErrNoOfficeRoute):
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Knowledge isn't available for this Location.", false)
	default:
		if r.Context().Err() == nil {
			slog.Warn("location_knowledge_publish_failed", "cause", knowledge.FailureCode(err), "error", err.Error(), "elapsed_ms", time.Since(started).Milliseconds())
		}
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Not published. Nothing changed. Try again.", true)
	}
}

func (server *Server) QueryLocationKnowledge(w http.ResponseWriter, r *http.Request) {
	var body api.LocationKnowledgeQuery
	server.readLocationKnowledge(w, r, &body, func(ctx context.Context, identity access.Identity) (any, error) {
		return server.knowledge.ReadLocation(ctx, identity, body.PracticeId.String(), body.LocationId.String())
	})
}

func (server *Server) QueryLocationKnowledgeHistory(w http.ResponseWriter, r *http.Request) {
	var body api.LocationKnowledgeQuery
	server.readLocationKnowledge(w, r, &body, func(ctx context.Context, identity access.Identity) (any, error) {
		return server.knowledge.ReadLocationHistory(ctx, identity, body.PracticeId.String(), body.LocationId.String())
	})
}

func (server *Server) ReadLocationKnowledgeRevision(w http.ResponseWriter, r *http.Request) {
	var body api.LocationKnowledgeRevisionQuery
	server.readLocationKnowledge(w, r, &body, func(ctx context.Context, identity access.Identity) (any, error) {
		return server.knowledge.ReadLocationRevision(ctx, identity, body.PracticeId.String(), body.LocationId.String(), body.RevisionId.String())
	})
}

func (server *Server) readLocationKnowledge(w http.ResponseWriter, r *http.Request, body any, read func(context.Context, access.Identity) (any, error)) {
	started := time.Now()
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
	if server.knowledge == nil {
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Knowledge isn't available right now.", false)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	result, err := read(ctx, identity)
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
			slog.Warn("location_knowledge_read_failed", "path", r.URL.Path, "error", err.Error(), "elapsed_ms", time.Since(started).Milliseconds())
		}
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Knowledge isn't available right now.", true)
	}
}

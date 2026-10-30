package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
)

func (server *Server) QueryInsuranceCatalog(w http.ResponseWriter, r *http.Request) {
	var body api.InsuranceCatalogQuery
	server.readInsurance(w, r, &body, func(ctx context.Context, identity access.Identity) (any, error) {
		return server.insuranceRevisions.Query(ctx, identity, body.PracticeId.String(), body.LocationId.String())
	})
}

func (server *Server) QueryInsuranceHistory(w http.ResponseWriter, r *http.Request) {
	var body api.InsuranceCatalogQuery
	server.readInsurance(w, r, &body, func(ctx context.Context, identity access.Identity) (any, error) {
		return server.insuranceRevisions.History(ctx, identity, body.PracticeId.String(), body.LocationId.String())
	})
}

func (server *Server) ReadInsuranceRevision(w http.ResponseWriter, r *http.Request) {
	var body api.InsuranceRevisionQuery
	server.readInsurance(w, r, &body, func(ctx context.Context, identity access.Identity) (any, error) {
		return server.insuranceRevisions.ReadRevision(ctx, identity, body.PracticeId.String(), body.LocationId.String(), body.RevisionId.String())
	})
}

func (server *Server) readInsurance(w http.ResponseWriter, r *http.Request, body any, read func(context.Context, access.Identity) (any, error)) {
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
	if server.insuranceRevisions == nil {
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "The insurance list isn't available right now.", false)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	result, err := read(ctx, identity)
	switch {
	case err == nil:
		server.writeJSON(w, http.StatusOK, result)
	case errors.Is(err, insurance.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, access.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, insurance.ErrNotFound):
		server.writeError(w, r, http.StatusNotFound, "NOT_FOUND", "This version of the insurance list doesn't exist.", false)
	case errors.Is(err, access.ErrNoOfficeRoute):
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "The insurance list isn't available for this Location.", false)
	case errors.Is(err, insurance.ErrNoCatalog):
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "No insurance list has been published for this practice yet.", false)
	default:
		if r.Context().Err() == nil {
			slog.Warn("insurance_catalog_read_failed", "path", r.URL.Path, "error", err.Error(), "elapsed_ms", time.Since(started).Milliseconds())
		}
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "The insurance list isn't available right now.", true)
	}
}

func (server *Server) PublishInsuranceCatalog(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.InsurancePublication
	if !server.decodeJSONLimit(w, r, &body, 4<<20) {
		return
	}
	if server.insuranceRevisions == nil {
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Not published. Nothing changed. Try again.", true)
		return
	}
	var provenance insurance.Provenance
	if body.Provenance != nil {
		provenance = insurance.Provenance(*body.Provenance)
	}
	encoded, _ := json.Marshal(body.Catalog)
	catalog, err := insurance.ParseCatalog(encoded)
	if err != nil {
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The JSON request body is invalid.", false)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	revision, err := server.insuranceRevisions.Publish(ctx, insurance.PublishCommand{
		Identity: identity, PracticeID: body.PracticeId.String(), LocationID: body.LocationId.String(),
		RevisionID: body.RevisionId.String(), ExpectedRevisionID: body.ExpectedRevisionId.String(), Reason: body.Reason,
		Provenance: provenance, Catalog: catalog,
	})
	var rejected *insurance.ValidationError
	switch {
	case err == nil:
		server.writeJSON(w, http.StatusOK, revision)
	case errors.As(err, &rejected):
		envelope := errorEnvelope(r, "INSURANCE_INVALID", "The insurance list has problems to fix before it can be published. Nothing changed.", false)
		reasons := make([]api.InsuranceValidationError, len(rejected.Errors))
		for i, reason := range rejected.Errors {
			reasons[i] = api.InsuranceValidationError{Region: reason.Region, Table: reason.Table, PlanId: reason.PlanID, Rule: reason.Rule, Message: reason.Message}
		}
		envelope.Error.Errors = &reasons
		server.writeJSON(w, http.StatusBadRequest, envelope)
	case errors.Is(err, insurance.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, access.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "You don't have access to every office this change affects. Nothing changed.", false)
	case errors.Is(err, insurance.ErrConflict):
		server.writeError(w, r, http.StatusConflict, "INSURANCE_CHANGED", "Someone else changed the insurance list. Reload to see their changes.", false)
	default:
		if r.Context().Err() == nil {
			slog.Warn("insurance_catalog_publish_failed", "error", err.Error(), "elapsed_ms", time.Since(started).Milliseconds())
		}
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Not published. Nothing changed. Try again.", true)
	}
}

func (server *Server) ReadAgentInsuranceCatalog(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticateService(w, r)
	if !ok {
		return
	}
	var empty struct{}
	if !server.decodeJSONLimit(w, r, &empty, 4096) {
		return
	}
	if server.insuranceRevisions == nil {
		slog.Warn("agent_insurance_read_failed", "cause", "insurance_not_configured", "elapsed_ms", time.Since(started).Milliseconds())
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "The insurance list isn't available right now.", true)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	result, err := server.insuranceRevisions.Read(ctx, identity)
	switch {
	case err == nil:
		server.writeJSON(w, http.StatusOK, result)
	case errors.Is(err, access.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "This service cannot read the insurance list.", false)
	case errors.Is(err, insurance.ErrNoCatalog):
		slog.Warn("agent_insurance_read_failed", "cause", "no_catalog", "elapsed_ms", time.Since(started).Milliseconds())
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "No insurance list has been published for this practice yet.", false)
	default:
		if r.Context().Err() == nil {
			slog.Warn("agent_insurance_read_failed", "cause", "database_failure", "error", err.Error(), "elapsed_ms", time.Since(started).Milliseconds())
		}
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "The insurance list isn't available right now.", true)
	}
}

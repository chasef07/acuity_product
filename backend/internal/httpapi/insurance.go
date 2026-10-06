package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
)

func (server *Server) QueryInsurancePlans(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.InsurancePlansQuery
	if !server.decodeJSON(w, r, &body) {
		return
	}
	if server.insurance == nil {
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Insurance rules aren't available right now.", false)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	page, err := server.insurance.Plans(ctx, insurance.Query{Identity: identity, PracticeID: body.PracticeId.String(), LocationID: body.LocationId.String(), Coverage: insurance.Coverage(body.Coverage)})
	switch {
	case err == nil:
		server.writeJSON(w, http.StatusOK, page)
	case errors.Is(err, insurance.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, access.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, access.ErrNoOfficeRoute), errors.Is(err, insurance.ErrUnknownOffice):
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Insurance rules aren't available for this Location.", false)
	default:
		if r.Context().Err() == nil {
			slog.Warn("insurance_plans_failed", "error", err.Error(), "elapsed_ms", time.Since(started).Milliseconds())
		}
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Insurance rules aren't available right now.", true)
	}
}

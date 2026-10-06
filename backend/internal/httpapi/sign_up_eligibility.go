package httpapi

import (
	"net/http"

	"github.com/chasef07/acuity_product/backend/internal/api"
)

func (server *Server) InspectSignUpEligibility(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	var body api.SignUpEligibilityRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	if err := server.access.InspectSignUpEligibility(ctx, string(body.Email)); err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

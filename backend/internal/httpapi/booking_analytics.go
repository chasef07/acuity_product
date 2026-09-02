package httpapi

import (
	"net/http"

	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
)

func (server *Server) QueryBookingAnalytics(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.PracticeAnalyticsQueryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, finish, ok := server.beginAnalytics(w, r)
	if !ok {
		return
	}
	defer finish()
	page, err := server.interactions.QueryBookingAnalytics(ctx, interaction.QueryBookingAnalyticsCommand{
		Identity: identity, PracticeID: body.PracticeId.String(), LocationID: uuidString(body.LocationId),
		Days: int(body.Days), TimeZone: body.TimeZone,
	})
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, page)
}

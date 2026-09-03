package httpapi

import (
	"net/http"

	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
)

func (server *Server) QueryOperatorAICosts(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.OperatorAICostQueryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, finish, ok := server.beginAnalytics(w, r)
	if !ok {
		return
	}
	defer finish()
	report, err := server.interactions.QueryCostAnalytics(ctx, interaction.QueryCostAnalyticsCommand{
		Identity: identity, PracticeID: body.PracticeId.String(), LocationID: uuidString(body.LocationId),
		Range: interaction.AnalyticsRange(body.Range), TimeZone: body.TimeZone,
	})
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, report)
}

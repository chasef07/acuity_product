package httpapi

import (
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"net/http"
)

func (server *Server) QueryStaffAnalytics(w http.ResponseWriter, r *http.Request) {
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
	report, err := server.workspace.QueryStaffAnalytics(ctx, workspace.QueryStaffAnalyticsCommand{
		Identity: identity, PracticeID: body.PracticeId.String(), LocationID: uuidString(body.LocationId), Days: int(body.Days), TimeZone: body.TimeZone,
	})
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, report)
}

package httpapi

import (
	"net/http"

	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
)

func (server *Server) GetOperatorScorecardQuestions(w http.ResponseWriter, r *http.Request, params api.GetOperatorScorecardQuestionsParams) {
	if _, ok := server.portalIdentity(w, r); !ok {
		return
	}
	version := ""
	if params.Version != nil {
		version = string(*params.Version)
	}
	questions := interaction.ScorecardQuestionsForVersion(version)
	if questions == nil {
		server.writeDomainError(w, r, interaction.ErrInvalidInput)
		return
	}
	server.writeJSON(w, http.StatusOK, map[string]any{"questions": questions})
}

package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func TestDomainErrorResponses(t *testing.T) {
	const (
		invalid     = "The request is invalid."
		denied      = "The requested access is not available."
		unavailable = "A required dependency is unavailable."
	)
	server := &Server{}
	for _, scenario := range []struct {
		name    string
		write   func(http.ResponseWriter, *http.Request, error)
		err     error
		status  int
		code    string
		message string
	}{
		{"access invalid", server.writeAccessError, access.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"access denied", server.writeAccessError, access.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"access location conflict", server.writeAccessError, access.ErrLocationConflict, 409, "LOCATION_CONFLICT", "This Location key already belongs to a Location with a different name."},
		{"access no office route", server.writeAccessError, access.ErrNoOfficeRoute, 503, "UNAVAILABLE", unavailable},
		{"calling invalid", server.writeCallingError, humancalling.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"calling invalid webhook", server.writeCallingError, humancalling.ErrInvalidWebhook, 400, "INVALID_REQUEST", invalid},
		{"calling occupied", server.writeCallingError, humancalling.ErrOccupied, 409, "CALL_OCCUPIED", "Another Call is pending or active."},
		{"calling denied", server.writeCallingError, humancalling.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"calling invalid handoff", server.writeCallingError, humancalling.ErrInvalidHandoff, 403, "ACCESS_DENIED", denied},
		{"calling ineligible", server.writeCallingError, humancalling.ErrIneligible, 403, "ACCESS_DENIED", denied},
		{"calling conflict", server.writeCallingError, humancalling.ErrConflict, 409, "CALL_CONFLICT", "The Call state changed. Refresh and try again."},
		{"calling expired", server.writeCallingError, humancalling.ErrExpired, 409, "CALL_CONFLICT", "The Call state changed. Refresh and try again."},
		{"calling ambiguous effect", server.writeCallingError, humancalling.ErrAmbiguousEffect, 503, "UNAVAILABLE", unavailable},
		{"work invalid", server.writeWorkError, work.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"work denied", server.writeWorkError, work.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"work conflict", server.writeWorkError, work.ErrConflict, 409, "TASK_CONFLICT", "The Task state changed. Refresh and try again."},
		{"workspace invalid", server.writeWorkspaceError, workspace.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"workspace denied", server.writeWorkspaceError, workspace.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"interaction invalid", server.writeInteractionError, interaction.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"interaction denied", server.writeInteractionError, interaction.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"interaction conflict", server.writeInteractionError, interaction.ErrConflict, 409, "INTERACTION_CONFLICT", "The source call conflicts with the stored Interaction."},
		{"messaging invalid", server.writeMessagingError, messaging.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"messaging denied", server.writeMessagingError, messaging.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"messaging blocked", server.writeMessagingError, messaging.ErrBlocked, 409, "MESSAGE_BLOCKED", "This conversation has opted out of outbound Messages."},
		{"messaging conflict", server.writeMessagingError, messaging.ErrConflict, 409, "MESSAGE_CONFLICT", "The Message state changed. Refresh and try again."},
		{"messaging ambiguous", server.writeMessagingError, messaging.ErrAmbiguous, 503, "UNAVAILABLE", unavailable},
		{"messaging rejected", server.writeMessagingError, messaging.ErrRejected, 503, "UNAVAILABLE", unavailable},
		{"messaging temporary", server.writeMessagingError, messaging.ErrTemporary, 503, "UNAVAILABLE", unavailable},
		{"unknown error", server.writeWorkError, errors.New("synthetic outage"), 503, "UNAVAILABLE", unavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, err := range []error{scenario.err, fmt.Errorf("wrapped: %w", scenario.err)} {
				response := httptest.NewRecorder()
				scenario.write(response, httptest.NewRequest(http.MethodPost, "/", nil), err)
				var envelope api.ErrorEnvelope
				if decodeErr := json.Unmarshal(response.Body.Bytes(), &envelope); decodeErr != nil {
					t.Fatalf("decode response: %v", decodeErr)
				}
				if response.Code != scenario.status || envelope.Error.Code != scenario.code ||
					envelope.Error.Message != scenario.message ||
					envelope.Error.Retryable != (scenario.status == http.StatusServiceUnavailable) {
					t.Fatalf("%v answered %d %+v", err, response.Code, envelope.Error)
				}
			}
		})
	}
}

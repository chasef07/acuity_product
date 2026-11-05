package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
		err     error
		status  int
		code    string
		message string
	}{
		{"access invalid", access.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"access denied", access.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"access location conflict", access.ErrLocationConflict, 409, "LOCATION_CONFLICT", "This Location key already belongs to a Location with a different name."},
		{"access no office route", access.ErrNoOfficeRoute, 503, "UNAVAILABLE", unavailable},
		{"calling invalid", humancalling.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"calling invalid webhook", humancalling.ErrInvalidWebhook, 400, "INVALID_REQUEST", invalid},
		{"calling occupied", humancalling.ErrOccupied, 409, "CALL_OCCUPIED", "Another Call is pending or active."},
		{"calling denied", humancalling.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"calling invalid handoff", humancalling.ErrInvalidHandoff, 403, "ACCESS_DENIED", denied},
		{"calling ineligible", humancalling.ErrIneligible, 403, "ACCESS_DENIED", denied},
		{"calling conflict", humancalling.ErrConflict, 409, "CALL_CONFLICT", "The Call state changed. Refresh and try again."},
		{"calling expired", humancalling.ErrExpired, 409, "CALL_CONFLICT", "The Call state changed. Refresh and try again."},
		{"calling ambiguous effect", humancalling.ErrAmbiguousEffect, 503, "UNAVAILABLE", unavailable},
		{"work invalid", work.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"work denied", work.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"work conflict", work.ErrConflict, 409, "TASK_CONFLICT", "The Task state changed. Refresh and try again."},
		{"workspace invalid", workspace.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"workspace denied", workspace.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"interaction invalid", interaction.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"interaction denied", interaction.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"interaction conflict", interaction.ErrConflict, 409, "INTERACTION_CONFLICT", "The source call conflicts with the stored Interaction."},
		{"messaging invalid", messaging.ErrInvalidInput, 400, "INVALID_REQUEST", invalid},
		{"messaging denied", messaging.ErrDenied, 403, "ACCESS_DENIED", denied},
		{"messaging blocked", messaging.ErrBlocked, 409, "MESSAGE_BLOCKED", "This conversation has opted out of outbound Messages."},
		{"messaging conflict", messaging.ErrConflict, 409, "MESSAGE_CONFLICT", "The Message state changed. Refresh and try again."},
		{"messaging ambiguous", messaging.ErrAmbiguous, 503, "UNAVAILABLE", unavailable},
		{"messaging rejected", messaging.ErrRejected, 503, "UNAVAILABLE", unavailable},
		{"messaging temporary", messaging.ErrTemporary, 503, "UNAVAILABLE", unavailable},
		{"unknown error", errors.New("synthetic outage"), 503, "UNAVAILABLE", unavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, err := range []error{scenario.err, fmt.Errorf("wrapped: %w", scenario.err)} {
				response := httptest.NewRecorder()
				server.writeDomainError(response, httptest.NewRequest(http.MethodPost, "/", nil), err)
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

func TestUnavailableRequestsLogTheirCause(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/v1/calling/outbound-calls", nil)
	request.Pattern = "POST /v1/calling/outbound-calls"

	rejected := httptest.NewRecorder()
	server.writeDomainError(rejected, request, interaction.ErrInvalidInput)
	if rejected.Code != http.StatusBadRequest || logs.Len() != 0 {
		t.Fatalf("client error status=%d logs=%s", rejected.Code, logs.String())
	}

	failed := httptest.NewRecorder()
	server.writeDomainError(failed, request, fmt.Errorf("read calling state: %w", context.DeadlineExceeded))
	if failed.Code != http.StatusServiceUnavailable ||
		!strings.Contains(logs.String(), "request_failed") ||
		!strings.Contains(logs.String(), `route="POST /v1/calling/outbound-calls"`) ||
		!strings.Contains(logs.String(), "read calling state: context deadline exceeded") {
		t.Fatalf("dependency failure status=%d logs=%s", failed.Code, logs.String())
	}

	logs.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server.writeDomainError(httptest.NewRecorder(), request.WithContext(ctx), context.Canceled)
	if logs.Len() != 0 {
		t.Fatalf("caller cancellation logged as a failure: %s", logs.String())
	}
}

package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/interaction"
)

func TestUnavailableInteractionRequestsLogTheirCause(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/v1/operator/ai-analytics/query", nil)
	request.Pattern = "POST /v1/operator/ai-analytics/query"

	rejected := httptest.NewRecorder()
	server.writeInteractionError(rejected, request, interaction.ErrInvalidInput)
	if rejected.Code != http.StatusBadRequest || logs.Len() != 0 {
		t.Fatalf("client error status=%d logs=%s", rejected.Code, logs.String())
	}

	failed := httptest.NewRecorder()
	server.writeInteractionError(failed, request, fmt.Errorf("query operator AI analytics summary: %w", context.DeadlineExceeded))
	if failed.Code != http.StatusServiceUnavailable ||
		!strings.Contains(logs.String(), "interaction_request_failed") ||
		!strings.Contains(logs.String(), `route="POST /v1/operator/ai-analytics/query"`) ||
		!strings.Contains(logs.String(), "query operator AI analytics summary: context deadline exceeded") {
		t.Fatalf("dependency failure status=%d logs=%s", failed.Code, logs.String())
	}

	logs.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server.writeInteractionError(httptest.NewRecorder(), request.WithContext(ctx), context.Canceled)
	if logs.Len() != 0 {
		t.Fatalf("caller cancellation logged as a failure: %s", logs.String())
	}
}

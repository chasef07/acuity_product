package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/google/uuid"
)

func TestAIInteractionDetailResponseIncludesAppointmentAction(t *testing.T) {
	now := time.Date(2026, time.August, 16, 10, 0, 0, 0, time.UTC)
	response, err := aiInteractionDetailResponse(interaction.Interaction{
		ID:                 uuid.NewString(),
		PracticeID:         uuid.NewString(),
		LocationID:         uuid.NewString(),
		SourceCallID:       "source-call",
		Phone:              "+15555550100",
		OfficePhone:        "+15555550101",
		StartedAt:          now,
		Status:             interaction.CallCompleted,
		AppointmentAction:  interaction.AppointmentRescheduled,
		AppointmentOutcome: interaction.OutcomeReschedule,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("map AI Interaction detail: %v", err)
	}
	if response.AppointmentAction == nil ||
		*response.AppointmentAction != "RESCHEDULED" {
		t.Fatalf("appointment action = %#v, want RESCHEDULED", response.AppointmentAction)
	}
}

func TestRequestMetadataAllowsEachConfiguredBrowserOrigin(t *testing.T) {
	server := &Server{config: Config{AllowedOrigins: []string{
		"https://acuityhealth.io",
		"https://acuity-web.example.run.app",
	}}}
	handler := server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, test := range []struct {
		origin     string
		want       string
		wantAllow  string
		wantExpose string
	}{
		{origin: "https://acuityhealth.io", want: "https://acuityhealth.io", wantAllow: "Authorization, Content-Type, If-None-Match, X-Correlation-ID", wantExpose: "ETag"},
		{origin: "https://acuity-web.example.run.app", want: "https://acuity-web.example.run.app", wantAllow: "Authorization, Content-Type, If-None-Match, X-Correlation-ID", wantExpose: "ETag"},
		{origin: "https://untrusted.example", want: "", wantAllow: "", wantExpose: ""},
	} {
		request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		request.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		if got := response.Header().Get("Access-Control-Allow-Origin"); got != test.want {
			t.Errorf("origin %q allowed as %q, want %q", test.origin, got, test.want)
		}
		if got := response.Header().Get("Access-Control-Allow-Headers"); got != test.wantAllow {
			t.Errorf("origin %q allows headers %q, want %q", test.origin, got, test.wantAllow)
		}
		if got := response.Header().Get("Access-Control-Expose-Headers"); got != test.wantExpose {
			t.Errorf("origin %q exposes %q, want %q", test.origin, got, test.wantExpose)
		}
	}
}

func TestRequestMetadataRecordsOnlyFixedCustomerJourneyRoutes(t *testing.T) {
	var output bytes.Buffer
	server := &Server{
		role:   "portal-api",
		config: Config{},
		observer: observability.NewLogger(
			observability.RuntimePortalAPI,
			"portal-api-test",
			slog.New(slog.NewJSONHandler(&output, nil)),
		),
	}
	handler := server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/access" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/health/ready", "/v1/access"} {
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, path, nil),
		)
	}

	scanner := bufio.NewScanner(&output)
	var entries []map[string]any
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("decode availability metric: %v", err)
		}
		entries = append(entries, entry)
	}
	if len(entries) != 1 || entries[0]["route"] != "/v1/access" ||
		entries[0]["outcome"] != "unavailable" ||
		entries[0]["failure_stage"] != "dependency" {
		t.Fatalf("availability entries = %#v", entries)
	}
}

func TestRequestMetadataDoesNotRecordPortalAvailabilityForOtherRuntimeRoles(t *testing.T) {
	var output bytes.Buffer
	server := &Server{
		role: "realtime",
		observer: observability.NewLogger(
			observability.RuntimeRealtime,
			"realtime-test",
			slog.New(slog.NewJSONHandler(&output, nil)),
		),
	}
	handler := server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/v1/access", nil),
	)

	if output.Len() != 0 {
		t.Fatalf("realtime runtime emitted Portal availability: %s", output.Bytes())
	}
}

func TestRequestMetadataRecordsCriticalRoutePanicsAsUnavailable(t *testing.T) {
	var output bytes.Buffer
	server := &Server{
		role: "portal-api",
		observer: observability.NewLogger(
			observability.RuntimePortalAPI,
			"portal-api-test",
			slog.New(slog.NewJSONHandler(&output, nil)),
		),
	}
	handler := server.withRequestMetadata(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("synthetic handler panic")
	}))

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/v1/access", nil),
		)
	}()
	if recovered == nil {
		t.Fatal("critical route panic did not propagate")
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatalf("decode panic availability metric: %v", err)
	}
	if entry["route"] != "/v1/access" ||
		entry["outcome"] != "unavailable" ||
		entry["failure_stage"] != "handler" {
		t.Fatalf("panic availability entry = %#v", entry)
	}
}

func TestAvailabilityResultTreatsCallingNotModifiedAsAvailable(t *testing.T) {
	outcome, stage := availabilityResult(
		observability.AvailabilityCallingState,
		http.StatusNotModified,
	)
	if outcome != observability.AvailabilityAvailable || stage != observability.FailureNone {
		t.Fatalf("Calling State 304 = %q/%q", outcome, stage)
	}
}

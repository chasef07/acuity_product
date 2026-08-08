package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestAIInteractionIngestionIsAuthenticatedAndIdempotent(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 8, 9, 30, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "ai-interaction-http-test",
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{
				{
					Key:             "spring-hill",
					Name:            "Spring Hill",
					AbitaOfficeKeys: []string{"spring-hill"},
				},
				{Key: "hidden-office", Name: "Hidden Office"},
			},
			Invitations: []access.InvitationProvision{
				{
					Key:           "admin",
					Email:         "admin@abita.test",
					Role:          access.RoleAdmin,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(time.Hour),
				},
				{
					Key:                  "hidden-staff",
					Email:                "hidden@abita.test",
					Role:                 access.RoleStaff,
					LocationScope:        access.LocationScopeSelected,
					SelectedLocationKeys: []string{"hidden-office"},
					ExpiresAt:            now.Add(time.Hour),
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("provision AI Interaction fixture: %v", err)
	}
	admin := access.Identity{
		Subject:       "admin-subject",
		Email:         "admin@abita.test",
		EmailVerified: true,
	}
	if _, err := accessModule.AcceptInvitation(
		context.Background(), admin, provisioned.Invitations[0].Token,
	); err != nil {
		t.Fatalf("accept AI Interaction admin invitation: %v", err)
	}
	hiddenStaff := access.Identity{
		Subject:       "hidden-subject",
		Email:         "hidden@abita.test",
		EmailVerified: true,
	}
	if _, err := accessModule.AcceptInvitation(
		context.Background(), hiddenStaff, provisioned.Invitations[1].Token,
	); err != nil {
		t.Fatalf("accept hidden AI Interaction invitation: %v", err)
	}
	var practiceID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text FROM access_practices WHERE provisioning_key = 'abita-eye-group'
	`).Scan(&practiceID); err != nil {
		t.Fatalf("read AI Interaction Practice: %v", err)
	}
	serviceAuth, err := access.NewServiceAuthenticator(
		"abita-interaction-token",
		access.ServiceIdentity{
			Subject:       "abita-agent",
			PracticeID:    practiceID,
			LocationScope: access.LocationScopeAll,
			Capabilities: []access.ServiceCapability{
				access.ServiceCapability("INGEST_AI_INTERACTION"),
				access.ServiceCapabilityCreateTask,
			},
		},
	)
	if err != nil {
		t.Fatalf("create AI Interaction service authenticator: %v", err)
	}
	workModule := work.New(pool, accessModule, func() time.Time {
		return now.Add(10 * time.Minute)
	})
	callingModule := humancalling.New(pool, accessModule, httpCallingProvider{}, humancalling.Config{}, nil)
	if err := callingModule.ProvisionLocationVoices(context.Background(),
		[]humancalling.LocationVoiceProvision{{
			PracticeKey: "abita-eye-group",
			LocationKey: "spring-hill",
			Number:      "+17275919997",
			Enabled:     true,
		}}); err != nil {
		t.Fatalf("provision AI Interaction voice route: %v", err)
	}
	handler, err := httpapi.NewPortal(
		httpapi.Config{AcquireTimeout: time.Second},
		pool,
		httpapi.PortalDependencies{
			Access: accessModule,
			Authenticator: staticAuthenticator{
				"admin-token":  admin,
				"hidden-token": hiddenStaff,
			},
			Calling:              callingModule,
			Interactions:         interaction.New(pool, accessModule, func() time.Time { return now }),
			Messaging:            messaging.New(pool, accessModule, workModule, nil, messaging.Config{}, nil),
			Work:                 workModule,
			ServiceAuthenticator: serviceAuth,
		},
	)
	if err != nil {
		t.Fatalf("create AI Interaction portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	body, _ := json.Marshal(map[string]any{
		"kind":         "START",
		"sourceCallId": "abita-call-63",
		"callerPhone":  "+17275550199",
		"officePhone":  "+17275919997",
		"startedAt":    now.Format(time.RFC3339),
		"status":       "IN_PROGRESS",
	})
	unauthenticated := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "wrong-token", body,
	)
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated AI Interaction status = %d, body = %s",
			unauthenticated.StatusCode, readBody(t, unauthenticated))
	}
	_ = unauthenticated.Body.Close()

	created := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "abita-interaction-token", body,
	)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create AI Interaction status = %d, body = %s",
			created.StatusCode, readBody(t, created))
	}
	var first struct {
		InteractionID string `json:"interactionId"`
		Status        string `json:"status"`
	}
	decode(t, created, &first)
	if first.InteractionID == "" || first.Status != "created" {
		t.Fatalf("create AI Interaction receipt = %#v", first)
	}

	duplicate := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "abita-interaction-token", body,
	)
	if duplicate.StatusCode != http.StatusOK {
		t.Fatalf("duplicate AI Interaction status = %d, body = %s",
			duplicate.StatusCode, readBody(t, duplicate))
	}
	var replay struct {
		InteractionID string `json:"interactionId"`
		Status        string `json:"status"`
	}
	decode(t, duplicate, &replay)
	if replay.InteractionID != first.InteractionID || replay.Status != "updated" {
		t.Fatalf("duplicate AI Interaction receipt = %#v, first = %#v", replay, first)
	}

	checkpointBody, _ := json.Marshal(map[string]any{
		"kind":         "OUTCOME_CHECKPOINT",
		"officeKey":    "spring-hill",
		"sourceCallId": "abita-call-63",
		"callerPhone":  "+17275550199",
		"officePhone":  "+17275919997",
		"startedAt":    now.Format(time.RFC3339),
		"status":       "IN_PROGRESS",
		"appointmentOutcome": map[string]any{
			"action":             "RESCHEDULED",
			"occurredAt":         now.Add(2 * time.Minute).Format(time.RFC3339),
			"externalPatientId":  "patient-63",
			"oldAppointmentId":   "appointment-old",
			"newAppointmentId":   "appointment-new",
			"bookingResult":      map[string]any{"status": "booked", "appointmentId": 6302},
			"cancellationResult": map[string]any{"status": "error", "reason": "middleware_error"},
		},
	})
	checkpoint := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "abita-interaction-token", checkpointBody,
	)
	if checkpoint.StatusCode != http.StatusOK {
		t.Fatalf("checkpoint AI Interaction status = %d, body = %s",
			checkpoint.StatusCode, readBody(t, checkpoint))
	}
	_ = checkpoint.Body.Close()
	checkpointReplay := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "abita-interaction-token", checkpointBody,
	)
	if checkpointReplay.StatusCode != http.StatusOK {
		t.Fatalf("checkpoint replay AI Interaction status = %d, body = %s",
			checkpointReplay.StatusCode, readBody(t, checkpointReplay))
	}
	_ = checkpointReplay.Body.Close()

	endedAt := now.Add(5 * time.Minute)
	summaryBody, _ := json.Marshal(map[string]any{
		"kind":         "SUMMARY",
		"officeKey":    "spring-hill",
		"sourceCallId": "abita-call-63",
		"callerPhone":  "+17275550199",
		"officePhone":  "+17275919997",
		"startedAt":    now.Format(time.RFC3339),
		"endedAt":      endedAt.Format(time.RFC3339),
		"status":       "COMPLETED",
		"summary":      "Caller rescheduled an appointment.",
		"summaryPayload": map[string]any{
			"callId": "abita-call-63",
			"phase":  "summary",
		},
	})
	for attempt := 1; attempt <= 2; attempt++ {
		summary := request(
			t, server.Client(), http.MethodPost,
			server.URL+"/v1/ai/interactions", "abita-interaction-token", summaryBody,
		)
		if summary.StatusCode != http.StatusOK {
			t.Fatalf("summary attempt %d AI Interaction status = %d, body = %s",
				attempt, summary.StatusCode, readBody(t, summary))
		}
		_ = summary.Body.Close()
	}
	minimalCloseoutBody, _ := json.Marshal(map[string]any{
		"kind":         "CLOSEOUT",
		"officeKey":    "spring-hill",
		"sourceCallId": "abita-call-63",
		"callerPhone":  "+17275550199",
		"officePhone":  "+17275919997",
		"startedAt":    now.Format(time.RFC3339),
		"endedAt":      endedAt.Format(time.RFC3339),
		"status":       "COMPLETED",
		"transcript":   map[string]any{"items": []any{}},
		"closeoutPayload": map[string]any{
			"callId": "abita-call-63",
		},
	})
	minimalCloseout := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "abita-interaction-token", minimalCloseoutBody,
	)
	if minimalCloseout.StatusCode != http.StatusOK {
		t.Fatalf("minimal closeout AI Interaction status = %d, body = %s",
			minimalCloseout.StatusCode, readBody(t, minimalCloseout))
	}
	_ = minimalCloseout.Body.Close()
	closeoutBody, _ := json.Marshal(map[string]any{
		"kind":         "CLOSEOUT",
		"officeKey":    "spring-hill",
		"sourceCallId": "abita-call-63",
		"callerPhone":  "+17275550199",
		"officePhone":  "+17275919997",
		"startedAt":    now.Format(time.RFC3339),
		"endedAt":      endedAt.Format(time.RFC3339),
		"status":       "COMPLETED",
		"summary":      "Caller rescheduled an appointment.",
		"transcript": map[string]any{
			"items": []map[string]any{{"role": "user", "text": "Please move my appointment."}},
		},
		"appointmentOutcome": map[string]any{
			"action":             "RESCHEDULED",
			"occurredAt":         now.Add(3 * time.Minute).Format(time.RFC3339),
			"externalPatientId":  "patient-63",
			"oldAppointmentId":   "appointment-old",
			"newAppointmentId":   "appointment-new",
			"bookingResult":      map[string]any{"status": "booked", "appointmentId": 6302},
			"cancellationResult": map[string]any{"status": "cancelled"},
		},
		"closeoutPayload": map[string]any{
			"callId": "abita-call-63",
			"appointmentActions": []map[string]any{{
				"action": "rescheduled",
				"status": "success",
			}},
		},
	})
	closeout := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "abita-interaction-token", closeoutBody,
	)
	if closeout.StatusCode != http.StatusOK {
		t.Fatalf("closeout AI Interaction status = %d, body = %s",
			closeout.StatusCode, readBody(t, closeout))
	}
	_ = closeout.Body.Close()
	closeoutReplay := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "abita-interaction-token", closeoutBody,
	)
	if closeoutReplay.StatusCode != http.StatusOK {
		t.Fatalf("closeout replay AI Interaction status = %d, body = %s",
			closeoutReplay.StatusCode, readBody(t, closeoutReplay))
	}
	_ = closeoutReplay.Body.Close()

	lateStart := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "abita-interaction-token", body,
	)
	if lateStart.StatusCode != http.StatusOK {
		t.Fatalf("late start AI Interaction status = %d, body = %s",
			lateStart.StatusCode, readBody(t, lateStart))
	}
	_ = lateStart.Body.Close()
	poorerCloseoutBody, _ := json.Marshal(map[string]any{
		"kind":         "CLOSEOUT",
		"officeKey":    "spring-hill",
		"sourceCallId": "abita-call-63",
		"callerPhone":  "+17275550199",
		"officePhone":  "+17275919997",
		"startedAt":    now.Format(time.RFC3339),
		"endedAt":      endedAt.Add(time.Minute).Format(time.RFC3339),
		"status":       "FAILED",
		"transcript": map[string]any{
			"items": []map[string]any{{
				"role":      "assistant",
				"synthetic": "must-not-merge",
			}},
		},
		"closeoutPayload": map[string]any{
			"padding": strings.Repeat("x", 1_000),
		},
	})
	poorerCloseout := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "abita-interaction-token", poorerCloseoutBody,
	)
	if poorerCloseout.StatusCode != http.StatusOK {
		t.Fatalf("poorer replay AI Interaction status = %d, body = %s",
			poorerCloseout.StatusCode, readBody(t, poorerCloseout))
	}
	_ = poorerCloseout.Body.Close()

	detail := request(
		t, server.Client(), http.MethodGet,
		server.URL+"/v1/ai/interactions/"+first.InteractionID,
		"admin-token", nil,
	)
	if detail.StatusCode != http.StatusOK {
		t.Fatalf("read AI Interaction detail status = %d, body = %s",
			detail.StatusCode, readBody(t, detail))
	}
	var stored struct {
		Status             string         `json:"status"`
		Summary            string         `json:"summary"`
		AppointmentOutcome string         `json:"appointmentOutcome"`
		OldAppointmentID   string         `json:"oldAppointmentId"`
		NewAppointmentID   string         `json:"newAppointmentId"`
		Transcript         map[string]any `json:"transcript"`
		CloseoutPayload    map[string]any `json:"closeoutPayload"`
	}
	decode(t, detail, &stored)
	storedTranscript, _ := json.Marshal(stored.Transcript)
	if stored.Status != "COMPLETED" ||
		stored.Summary != "Caller rescheduled an appointment." ||
		stored.AppointmentOutcome != "RESCHEDULE" ||
		stored.OldAppointmentID != "appointment-old" ||
		stored.NewAppointmentID != "appointment-new" ||
		string(storedTranscript) != `{"items":[{"role":"user","text":"Please move my appointment."}]}` ||
		stored.CloseoutPayload["appointmentActions"] == nil {
		t.Fatalf("monotonic AI Interaction detail = %#v", stored)
	}
	deniedDetail := request(
		t, server.Client(), http.MethodGet,
		server.URL+"/v1/ai/interactions/"+first.InteractionID,
		"hidden-token", nil,
	)
	if deniedDetail.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-Location AI Interaction detail status = %d, body = %s",
			deniedDetail.StatusCode, readBody(t, deniedDetail))
	}
	_ = deniedDetail.Body.Close()
	postCloseout := func(
		sourceCallID string,
		callerPhone string,
		appointmentOutcome map[string]any,
	) {
		t.Helper()
		payload := map[string]any{
			"kind":            "CLOSEOUT",
			"officeKey":       "spring-hill",
			"sourceCallId":    sourceCallID,
			"callerPhone":     callerPhone,
			"officePhone":     "+17275919997",
			"startedAt":       now.Add(time.Minute).Format(time.RFC3339),
			"endedAt":         endedAt.Format(time.RFC3339),
			"status":          "COMPLETED",
			"closeoutPayload": map[string]any{"callId": sourceCallID},
		}
		if appointmentOutcome != nil {
			payload["appointmentOutcome"] = appointmentOutcome
		}
		encoded, _ := json.Marshal(payload)
		response := request(
			t, server.Client(), http.MethodPost,
			server.URL+"/v1/ai/interactions", "abita-interaction-token", encoded,
		)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create %s AI Interaction status = %d, body = %s",
				sourceCallID, response.StatusCode, readBody(t, response))
		}
		_ = response.Body.Close()
	}
	outcomeAt := now.Add(3 * time.Minute).Format(time.RFC3339)
	postCloseout("abita-booking-63", "+17275550201", map[string]any{
		"action":           "BOOKED",
		"occurredAt":       outcomeAt,
		"newAppointmentId": "appointment-booked",
		"bookingResult":    map[string]any{"status": "booked", "appointmentId": 6303},
	})
	postCloseout("abita-cancellation-63", "+17275550202", map[string]any{
		"action":             "CANCELLED",
		"occurredAt":         outcomeAt,
		"oldAppointmentId":   "appointment-cancelled",
		"cancellationResult": map[string]any{"status": "cancelled"},
	})
	postCloseout("abita-partial-63", "+17275550203", map[string]any{
		"action":             "RESCHEDULED",
		"occurredAt":         outcomeAt,
		"oldAppointmentId":   "appointment-partial-old",
		"newAppointmentId":   "appointment-partial-new",
		"bookingResult":      map[string]any{"status": "booked", "appointmentId": 6304},
		"cancellationResult": map[string]any{"status": "error", "reason": "middleware_error"},
	})
	postCloseout("abita-indeterminate-63", "+17275550204", nil)

	outcomeQueryBody, _ := json.Marshal(map[string]any{
		"practiceId": practiceID,
		"date":       "2026-08-08",
	})
	outcomes := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions/outcomes/query",
		"admin-token", outcomeQueryBody,
	)
	if outcomes.StatusCode != http.StatusOK {
		t.Fatalf("query AI Interaction outcomes status = %d, body = %s",
			outcomes.StatusCode, readBody(t, outcomes))
	}
	var daily struct {
		Counts struct {
			Bookings      int `json:"bookings"`
			Cancellations int `json:"cancellations"`
			Reschedules   int `json:"reschedules"`
			Partial       int `json:"partial"`
			Indeterminate int `json:"indeterminate"`
		} `json:"counts"`
		Items []struct {
			ID                 string `json:"id"`
			AppointmentOutcome string `json:"appointmentOutcome"`
		} `json:"items"`
	}
	decode(t, outcomes, &daily)
	if daily.Counts.Reschedules != 1 ||
		daily.Counts.Bookings != 1 ||
		daily.Counts.Cancellations != 1 ||
		daily.Counts.Partial != 1 ||
		daily.Counts.Indeterminate != 1 ||
		len(daily.Items) != 5 ||
		daily.Items[0].ID != first.InteractionID ||
		daily.Items[0].AppointmentOutcome != "RESCHEDULE" {
		t.Fatalf("daily AI Interaction outcomes = %#v", daily)
	}
	taskBody, _ := json.Marshal(map[string]any{
		"callId":         "abita-call-63",
		"callerPhone":    "+17275550199",
		"category":       "appointments",
		"idempotencyKey": "issue-63-history-task",
		"message":        "Caller also asked office staff to verify paperwork.",
		"officeKey":      "spring-hill",
		"officePhone":    "+17275919997",
		"source":         "agent",
		"summary":        "Verify paperwork.",
		"urgency":        "normal",
	})
	taskReceipt := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/tasks", "abita-interaction-token", taskBody,
	)
	if taskReceipt.StatusCode != http.StatusCreated {
		t.Fatalf("create unified history Task status = %d, body = %s",
			taskReceipt.StatusCode, readBody(t, taskReceipt))
	}
	_ = taskReceipt.Body.Close()

	timeline := request(
		t, server.Client(), http.MethodGet,
		server.URL+"/v1/engagements/+17275550199/timeline?practiceId="+
			url.QueryEscape(practiceID),
		"admin-token", nil,
	)
	if timeline.StatusCode != http.StatusOK {
		t.Fatalf("AI Interaction Engagement History status = %d, body = %s",
			timeline.StatusCode, readBody(t, timeline))
	}
	var history struct {
		Items []struct {
			Type          string `json:"type"`
			AIInteraction *struct {
				ID                 string `json:"id"`
				AppointmentOutcome string `json:"appointmentOutcome"`
			} `json:"aiInteraction"`
		} `json:"items"`
	}
	decode(t, timeline, &history)
	if len(history.Items) != 2 ||
		history.Items[0].Type != "AI_INTERACTION" ||
		history.Items[0].AIInteraction == nil ||
		history.Items[0].AIInteraction.ID != first.InteractionID ||
		history.Items[0].AIInteraction.AppointmentOutcome != "RESCHEDULE" ||
		history.Items[1].Type != "TASK" {
		t.Fatalf("AI Interaction Engagement History = %#v", history)
	}
}

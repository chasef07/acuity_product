package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func TestAIInteractionIngestionIsAuthenticatedAndIdempotent(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 8, 9, 30, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
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
			AccessGrants: []access.AccessGrantProvision{
				{
					Key:           "admin",
					Email:         "admin@abita.test",
					Role:          access.RoleAdmin,
					LocationScope: access.LocationScopeAll,
				},
				{
					Key:                  "spring-hill-staff",
					Email:                "staff@abita.test",
					Role:                 access.RoleStaff,
					LocationScope:        access.LocationScopeSelected,
					SelectedLocationKeys: []string{"spring-hill"},
				},
				{
					Key:                  "hidden-staff",
					Email:                "hidden@abita.test",
					Role:                 access.RoleStaff,
					LocationScope:        access.LocationScopeSelected,
					SelectedLocationKeys: []string{"hidden-office"},
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
	testaccess.Activate(t, accessModule, admin)
	staff := access.Identity{
		Subject:       "staff-subject",
		Email:         "staff@abita.test",
		EmailVerified: true,
	}
	testaccess.Activate(t, accessModule, staff)
	hiddenStaff := access.Identity{
		Subject:       "hidden-subject",
		Email:         "hidden@abita.test",
		EmailVerified: true,
	}
	testaccess.Activate(t, accessModule, hiddenStaff)
	var practiceID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text FROM access_practices WHERE provisioning_key = 'abita-eye-group'
	`).Scan(&practiceID); err != nil {
		t.Fatalf("read AI Interaction Practice: %v", err)
	}
	serviceAuth, err := access.NewServiceAuthenticator(
		access.ServiceCredential{
			Token: "demo-interaction-token",
			Identity: access.ServiceIdentity{
				Subject:       "abita-demo",
				PracticeID:    "00000000-0000-0000-0000-000000000001",
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityCreateTask,
					access.ServiceCapabilityIngestAIInteraction,
				},
			},
		},
		access.ServiceCredential{
			Token: "production-interaction-token",
			Identity: access.ServiceIdentity{
				Subject:       "abita-agent",
				PracticeID:    practiceID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityHumanHandoff,
					access.ServiceCapabilityIngestAIInteraction,
				},
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
		[]humancalling.LocationVoiceProvision{
			{
				PracticeKey: "abita-eye-group",
				LocationKey: "spring-hill",
				Number:      "+17275919997",
				Enabled:     true,
			},
			{
				PracticeKey: "abita-eye-group",
				LocationKey: "hidden-office",
				Number:      "+17275919996",
				Enabled:     true,
			},
		}); err != nil {
		t.Fatalf("provision AI Interaction voice route: %v", err)
	}
	interactionClock := now
	interactionModule := interaction.New(pool, accessModule, func() time.Time { return interactionClock })
	handler, err := httpapi.NewPortal(
		httpapi.Config{AcquireTimeout: time.Second},
		pool,
		httpapi.PortalDependencies{
			Access: accessModule,
			Authenticator: staticAuthenticator{
				"admin-token":  admin,
				"staff-token":  staff,
				"hidden-token": hiddenStaff,
			},
			Calling:              callingModule,
			Interactions:         interactionModule,
			Messaging:            messaging.New(pool, accessModule, workModule, nil, messaging.Config{}, nil),
			Work:                 workModule,
			Workspace:            workspace.New(pool, accessModule),
			ServiceAuthenticator: serviceAuth,
		},
	)
	if err != nil {
		t.Fatalf("create AI Interaction portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	startedAt := now.Add(123_456_789 * time.Nanosecond)

	body, _ := json.Marshal(map[string]any{
		"kind":         "START",
		"sourceCallId": "abita-call-63",
		"callerPhone":  "+17275550199",
		"officePhone":  "+17275919997",
		"startedAt":    startedAt.Format(time.RFC3339Nano),
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
	oversizedBody := append([]byte{}, body...)
	oversizedBody = append(oversizedBody, bytes.Repeat([]byte(" "), 32*1024*1024)...)
	oversizedBody = append(oversizedBody, 'x')
	oversized := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "production-interaction-token", oversizedBody,
	)
	if oversized.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized AI Interaction status = %d, body = %s",
			oversized.StatusCode, readBody(t, oversized))
	}
	_ = oversized.Body.Close()
	wrongTenant := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "demo-interaction-token", body,
	)
	if wrongTenant.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong-tenant AI Interaction status = %d, body = %s",
			wrongTenant.StatusCode, readBody(t, wrongTenant))
	}
	_ = wrongTenant.Body.Close()

	created := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "production-interaction-token", body,
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
		server.URL+"/v1/ai/interactions", "production-interaction-token", body,
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
	poisonedStartBody, _ := json.Marshal(map[string]any{
		"kind":            "START",
		"sourceCallId":    "abita-poisoned-start-63",
		"callerPhone":     "+17275550198",
		"officePhone":     "+17275919997",
		"startedAt":       startedAt.Format(time.RFC3339Nano),
		"status":          "IN_PROGRESS",
		"closeoutPayload": map[string]any{"phase": "not-a-closeout"},
	})
	poisonedStart := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "production-interaction-token", poisonedStartBody,
	)
	if poisonedStart.StatusCode != http.StatusBadRequest {
		t.Fatalf("poisoned start AI Interaction status = %d, body = %s",
			poisonedStart.StatusCode, readBody(t, poisonedStart))
	}
	_ = poisonedStart.Body.Close()
	mismatchedLocationBody, _ := json.Marshal(map[string]any{
		"kind":         "START",
		"officeKey":    "spring-hill",
		"sourceCallId": "abita-mismatched-location-63",
		"callerPhone":  "+17275550197",
		"officePhone":  "+17275919996",
		"startedAt":    startedAt.Format(time.RFC3339Nano),
		"status":       "IN_PROGRESS",
	})
	mismatchedLocation := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "production-interaction-token", mismatchedLocationBody,
	)
	if mismatchedLocation.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatched Location AI Interaction status = %d, body = %s",
			mismatchedLocation.StatusCode, readBody(t, mismatchedLocation))
	}
	_ = mismatchedLocation.Body.Close()

	concurrentBody, _ := json.Marshal(map[string]any{
		"kind":         "START",
		"sourceCallId": "abita-concurrent-63",
		"callerPhone":  "+17275550999",
		"officePhone":  "+17275919997",
		"startedAt":    startedAt.Format(time.RFC3339Nano),
		"status":       "IN_PROGRESS",
	})
	type concurrentResult struct {
		statusCode int
		body       []byte
		err        error
	}
	const concurrentRequests = 2
	startRequests := make(chan struct{})
	results := make(chan concurrentResult, concurrentRequests)
	var requests sync.WaitGroup
	for requestIndex := 0; requestIndex < concurrentRequests; requestIndex++ {
		requests.Add(1)
		go func() {
			defer requests.Done()
			<-startRequests
			req, err := http.NewRequest(
				http.MethodPost,
				server.URL+"/v1/ai/interactions",
				bytes.NewReader(concurrentBody),
			)
			if err != nil {
				results <- concurrentResult{err: fmt.Errorf("create request: %w", err)}
				return
			}
			req.Header.Set("Authorization", "Bearer production-interaction-token")
			req.Header.Set("Content-Type", "application/json")
			response, err := server.Client().Do(req)
			if err != nil {
				results <- concurrentResult{err: fmt.Errorf("send request: %w", err)}
				return
			}
			responseBody, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr != nil {
				results <- concurrentResult{err: fmt.Errorf("read response: %w", readErr)}
				return
			}
			if closeErr != nil {
				results <- concurrentResult{err: fmt.Errorf("close response: %w", closeErr)}
				return
			}
			results <- concurrentResult{
				statusCode: response.StatusCode,
				body:       responseBody,
			}
		}()
	}
	close(startRequests)
	requests.Wait()
	close(results)
	createdCount := 0
	concurrentInteractionID := ""
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent AI Interaction request: %v", result.err)
		}
		if result.statusCode == http.StatusCreated {
			createdCount++
		} else if result.statusCode != http.StatusOK {
			t.Fatalf("concurrent AI Interaction status = %d, body = %s",
				result.statusCode, result.body)
		}
		var receipt struct {
			InteractionID string `json:"interactionId"`
			Status        string `json:"status"`
		}
		if err := json.Unmarshal(result.body, &receipt); err != nil {
			t.Fatalf("decode concurrent AI Interaction receipt: %v", err)
		}
		expectedStatus := "updated"
		if result.statusCode == http.StatusCreated {
			expectedStatus = "created"
		}
		if receipt.InteractionID == "" || receipt.Status != expectedStatus {
			t.Fatalf("concurrent AI Interaction receipt = %#v", receipt)
		}
		if concurrentInteractionID == "" {
			concurrentInteractionID = receipt.InteractionID
		} else if receipt.InteractionID != concurrentInteractionID {
			t.Fatalf("concurrent AI Interaction IDs = %q and %q",
				concurrentInteractionID, receipt.InteractionID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent AI Interaction created count = %d", createdCount)
	}

	endedAt := now.Add(5 * time.Minute)
	t.Run("rejects SUMMARY before durable receipt", func(t *testing.T) {
		legacySummaryBody, _ := json.Marshal(map[string]any{
			"kind":           "SUMMARY",
			"officeKey":      "spring-hill",
			"sourceCallId":   "abita-call-63",
			"callerPhone":    "+17275550199",
			"officePhone":    "+17275919997",
			"startedAt":      startedAt.Format(time.RFC3339Nano),
			"endedAt":        endedAt.Format(time.RFC3339),
			"status":         "COMPLETED",
			"summary":        "Caller rescheduled an appointment.",
			"summaryPayload": map[string]any{"phase": "legacy-summary"},
		})
		closeoutShapedSummaryBody, _ := json.Marshal(map[string]any{
			"kind":         "SUMMARY",
			"officeKey":    "spring-hill",
			"sourceCallId": "abita-call-63",
			"callerPhone":  "+17275550199",
			"officePhone":  "+17275919997",
			"startedAt":    startedAt.Format(time.RFC3339Nano),
			"endedAt":      endedAt.Format(time.RFC3339),
			"status":       "COMPLETED",
			"summary":      "Caller rescheduled an appointment.",
			"transcript":   map[string]any{"items": []map[string]any{{"role": "user"}}},
			"closeoutPayload": map[string]any{
				"callId": "abita-call-63",
			},
		})
		for name, requestBody := range map[string][]byte{
			"legacy payload":          legacySummaryBody,
			"closeout-shaped payload": closeoutShapedSummaryBody,
		} {
			response := request(
				t, server.Client(), http.MethodPost,
				server.URL+"/v1/ai/interactions", "production-interaction-token", requestBody,
			)
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s SUMMARY AI Interaction status = %d, body = %s",
					name, response.StatusCode, readBody(t, response))
			}
			_ = response.Body.Close()
		}
		var summaryReceiptCount int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*)
			FROM ai_interaction_receipts
			WHERE practice_id = $1
				AND source_call_id = 'abita-call-63'
				AND kind = 'SUMMARY'
		`, practiceID).Scan(&summaryReceiptCount); err != nil {
			t.Fatalf("read rejected SUMMARY receipts: %v", err)
		}
		if summaryReceiptCount != 0 {
			t.Fatalf("rejected SUMMARY receipt count = %d, want 0", summaryReceiptCount)
		}
	})
	sessionReport := map[string]any{
		"chat_history": map[string]any{"items": []map[string]any{
			{
				"id":         "turn-1",
				"type":       "message",
				"role":       "user",
				"content":    []string{"Please move my appointment."},
				"created_at": now.Add(time.Minute).UnixMilli(),
			},
			{
				"id":         "tool-1",
				"type":       "function_call",
				"name":       "reschedule_appointment",
				"call_id":    "reschedule-63",
				"arguments":  `{"appointmentId":"appointment-old"}`,
				"created_at": now.Add(2 * time.Minute).UnixMilli(),
			},
			{
				"id":         "result-1",
				"type":       "function_call_output",
				"name":       "reschedule_appointment",
				"call_id":    "reschedule-63",
				"output":     `"Rescheduled."`,
				"is_error":   false,
				"created_at": now.Add(3 * time.Minute).UnixMilli(),
			},
			{
				"id":         "turn-2",
				"type":       "message",
				"role":       "assistant",
				"content":    []string{"Your appointment is rescheduled."},
				"created_at": now.Add(4 * time.Minute).UnixMilli(),
			},
		}},
	}
	jevEvaluation := map[string]any{
		"evaluator": "jev", "evaluatorVersion": "typesafe-trace-v1",
		"model": "typesafe-ai/jev", "status": "complete",
		"evaluatedAt": endedAt.Format(time.RFC3339),
		"results": map[string]any{
			"outcome": map[string]any{"answers": map[string]any{
				"claims_supported": map[string]any{"type": "boolean", "probability": 0.37},
			}},
			"reaction": map[string]any{"status": "unavailable", "reason": "No post-call feedback supplied."},
		},
	}
	closeoutBody, _ := json.Marshal(map[string]any{
		"kind":         "CLOSEOUT",
		"officeKey":    "spring-hill",
		"sourceCallId": "abita-call-63",
		"callerPhone":  "+17275550199",
		"officePhone":  "+17275919997",
		"startedAt":    startedAt.Format(time.RFC3339Nano),
		"endedAt":      endedAt.Format(time.RFC3339),
		"status":       "COMPLETED",
		"summary":      "Caller rescheduled an appointment.",
		"transcript":   sessionReport,
		"appointmentOutcome": map[string]any{
			"action":            "RESCHEDULED",
			"occurredAt":        now.Add(3 * time.Minute).Format(time.RFC3339),
			"externalPatientId": "patient-63",
			"oldAppointmentId":  "appointment-old",
			"newAppointmentId":  "appointment-new",
			"bookingResult": map[string]any{
				"status":          "booked",
				"appointmentId":   6302,
				"receiptSequence": json.Number("9007199254740993"),
			},
			"cancellationResult": map[string]any{"status": "cancelled"},
		},
		"closeoutPayload": map[string]any{
			"callId":     "abita-call-63",
			"evaluation": jevEvaluation,
			"eligibilityChecks": []any{map[string]any{
				"status": "complete", "request": map[string]any{"firstName": "Jane", "lastName": "Example", "plan": "Example Health", "memberId": "private-4821"},
				"result": map[string]any{"status": "active", "checkedAt": "2026-09-23T12:00:00Z", "identity": map[string]any{"status": "exact_name_dob", "reviewRequired": false},
					"providerResponse": map[string]any{"subscriber": map[string]any{"memberId": "private-4821"}, "x12": "private-eligibility-x12",
						"benefitsInformation": []any{map[string]any{"code": "B", "serviceTypeCodes": []string{"98"}, "benefitAmount": "35", "additionalInformation": []any{map[string]any{"description": "Specific provider tier"}}}},
					},
				},
			}},
			"callerPhone": "+17275550199",
			"officeKey":   "spring-hill",
			"officePhone": "+17275919997",
			"startedAt":   startedAt.Format(time.RFC3339Nano),
			"endedAt":     endedAt.Format(time.RFC3339),
			"status":      "COMPLETED",
			"durationSec": 300,
			"language":    map[string]any{"currentLanguage": "en"},
			"voiceLanguage": map[string]any{
				"activeLanguage": "en",
			},
			"appointmentOutcome": map[string]any{
				"action":            "rescheduled",
				"occurredAt":        now.Add(3 * time.Minute).Format(time.RFC3339),
				"externalPatientId": "patient-63",
				"oldAppointmentId":  "appointment-old",
				"newAppointmentId":  "appointment-new",
				"bookingResult": map[string]any{
					"status":          "booked",
					"appointmentId":   6302,
					"receiptSequence": json.Number("9007199254740993"),
				},
				"cancellationResult": map[string]any{"status": "cancelled"},
			},
			"domainOutcomes": []map[string]any{{
				"callId":     "reschedule-63",
				"toolName":   "reschedule_appointment",
				"outcome":    "rescheduled",
				"status":     "success",
				"occurredAt": now.Add(3 * time.Minute).Format(time.RFC3339),
				"evidence": map[string]any{
					"action": "rescheduled",
					"appointment": map[string]any{
						"patientName":         "Jane Doe",
						"appointmentDate":     "2026-08-20",
						"appointmentTime":     "2:30 PM",
						"providerName":        "Dr. Bach",
						"locationName":        "Spring Hill",
						"appointmentTypeName": "Medical follow-up",
						"careLane":            "medical_md",
					},
					"cancelledAppointment": map[string]any{
						"patientName":     "Jane Doe",
						"appointmentDate": "2026-08-12",
						"appointmentTime": "9:00 AM",
						"providerName":    "Dr. Bach",
						"locationName":    "Spring Hill",
					},
				},
			}},
			"sttProfiles":   []map[string]any{},
			"sessionReport": sessionReport,
		},
	})
	closeout := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "production-interaction-token", closeoutBody,
	)
	if closeout.StatusCode != http.StatusOK {
		t.Fatalf("closeout AI Interaction status = %d, body = %s",
			closeout.StatusCode, readBody(t, closeout))
	}
	_ = closeout.Body.Close()
	closeoutReplay := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "production-interaction-token", closeoutBody,
	)
	if closeoutReplay.StatusCode != http.StatusOK {
		t.Fatalf("closeout replay AI Interaction status = %d, body = %s",
			closeoutReplay.StatusCode, readBody(t, closeoutReplay))
	}
	_ = closeoutReplay.Body.Close()

	lateStart := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "production-interaction-token", body,
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
		"startedAt":    startedAt.Format(time.RFC3339Nano),
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
		server.URL+"/v1/ai/interactions", "production-interaction-token", poorerCloseoutBody,
	)
	if poorerCloseout.StatusCode != http.StatusConflict {
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
		Status              string          `json:"status"`
		Summary             string          `json:"summary"`
		AppointmentAction   string          `json:"appointmentAction"`
		AppointmentOutcome  string          `json:"appointmentOutcome"`
		Appointment         map[string]any  `json:"appointment"`
		PreviousAppointment map[string]any  `json:"previousAppointment"`
		OldAppointmentID    string          `json:"oldAppointmentId"`
		NewAppointmentID    string          `json:"newAppointmentId"`
		BookingResult       json.RawMessage `json:"bookingResult"`
	}
	decode(t, detail, &stored)
	if stored.Status != "COMPLETED" ||
		stored.Summary != "Caller rescheduled an appointment." ||
		stored.AppointmentAction != "RESCHEDULED" ||
		stored.AppointmentOutcome != "RESCHEDULE" ||
		stored.Appointment["patientName"] != "Jane Doe" ||
		stored.Appointment["appointmentDate"] != "2026-08-20" ||
		stored.Appointment["appointmentId"] != "appointment-new" ||
		stored.PreviousAppointment["appointmentDate"] != "2026-08-12" ||
		stored.PreviousAppointment["appointmentId"] != "appointment-old" ||
		stored.OldAppointmentID != "appointment-old" ||
		stored.NewAppointmentID != "appointment-new" ||
		!bytes.Contains(stored.BookingResult, []byte(`"receiptSequence":9007199254740993`)) {
		t.Fatalf("monotonic AI Interaction detail = %#v", stored)
	}
	staffDetail := request(
		t, server.Client(), http.MethodGet,
		server.URL+"/v1/ai/interactions/"+first.InteractionID,
		"staff-token", nil,
	)
	if staffDetail.StatusCode != http.StatusOK {
		t.Fatalf("staff AI Interaction detail status = %d, body = %s",
			staffDetail.StatusCode, readBody(t, staffDetail))
	}
	staffDetailBody := readBody(t, staffDetail)
	for _, value := range []string{`"eligibilityChecks"`, `"benefitAmount":"35"`, `Specific provider tier`, `"memberIdLast4":"4821"`} {
		if !strings.Contains(staffDetailBody, value) {
			t.Fatalf("staff eligibility missing %s", value)
		}
	}
	for _, value := range []string{"private-4821", "private-eligibility-x12", `"providerResponse"`} {
		if strings.Contains(staffDetailBody, value) {
			t.Fatalf("staff summary leaked raw eligibility: %s", value)
		}
	}

	if bytes.Contains([]byte(staffDetailBody), []byte(`"transcript"`)) ||
		bytes.Contains([]byte(staffDetailBody), []byte(`"closeoutPayload"`)) ||
		bytes.Contains([]byte(staffDetailBody), []byte("Please move my appointment.")) {
		t.Fatalf("routine AI Interaction detail leaked evidence: %s", staffDetailBody)
	}
	evidence := request(
		t, server.Client(), http.MethodGet,
		server.URL+"/v1/ai/interactions/"+first.InteractionID+"/evidence",
		"admin-token", nil,
	)
	if evidence.StatusCode != http.StatusOK {
		t.Fatalf("read AI Interaction evidence status = %d, body = %s",
			evidence.StatusCode, readBody(t, evidence))
	}
	var storedEvidence struct {
		Transcript      map[string]any `json:"transcript"`
		CloseoutPayload map[string]any `json:"closeoutPayload"`
	}
	decode(t, evidence, &storedEvidence)
	chatHistory, _ := storedEvidence.Transcript["chat_history"].(map[string]any)
	chatItems, _ := chatHistory["items"].([]any)
	if len(chatItems) != 4 || storedEvidence.CloseoutPayload["domainOutcomes"] == nil {
		t.Fatalf("admin AI Interaction evidence = %#v", storedEvidence)
	}
	wantEvaluation, _ := json.Marshal(jevEvaluation)
	gotEvaluation, _ := json.Marshal(storedEvidence.CloseoutPayload["evaluation"])
	if !bytes.Equal(gotEvaluation, wantEvaluation) {
		t.Fatalf("Jev evaluation did not survive ingestion and evidence retrieval: %s", gotEvaluation)
	}
	var storedEvaluation json.RawMessage
	if err := pool.QueryRow(context.Background(),
		`SELECT closeout_payload->'evaluation' FROM ai_interactions WHERE id=$1`,
		first.InteractionID).Scan(&storedEvaluation); err != nil {
		t.Fatalf("read persisted Jev evaluation: %v", err)
	}
	var evaluationValue any
	if err := json.Unmarshal(storedEvaluation, &evaluationValue); err != nil {
		t.Fatalf("decode persisted Jev evaluation: %v", err)
	}
	storedEvaluation, _ = json.Marshal(evaluationValue)
	if !bytes.Equal(storedEvaluation, wantEvaluation) {
		t.Fatalf("Jev evaluation not persisted on call record: %s", storedEvaluation)
	}
	var interactionCount, transcriptEvidenceCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(*),
			count(*) FILTER (
				WHERE transcript IS NOT NULL AND closeout_payload IS NOT NULL
			)
		FROM ai_interactions
		WHERE practice_id = $1 AND source_call_id = 'abita-call-63'
	`, practiceID).Scan(&interactionCount, &transcriptEvidenceCount); err != nil {
		t.Fatalf("read START to CLOSEOUT persistence: %v", err)
	}
	if interactionCount != 1 || transcriptEvidenceCount != 1 {
		t.Fatalf("START to CLOSEOUT persistence = (%d, %d), want (1, 1)",
			interactionCount, transcriptEvidenceCount)
	}
	staffEvidence := request(
		t, server.Client(), http.MethodGet,
		server.URL+"/v1/ai/interactions/"+first.InteractionID+"/evidence",
		"staff-token", nil,
	)
	if staffEvidence.StatusCode != http.StatusForbidden {
		t.Fatalf("staff AI Interaction evidence status = %d, body = %s",
			staffEvidence.StatusCode, readBody(t, staffEvidence))
	}
	_ = staffEvidence.Body.Close()
	var receiptCount, duplicateCount, quarantinedCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(*),
			COALESCE(sum(receipt.duplicate_count), 0),
			count(*) FILTER (WHERE receipt.state = 'QUARANTINED')
		FROM ai_interaction_receipts receipt
		WHERE receipt.practice_id = $1
			AND receipt.source_call_id = 'abita-call-63'
	`, practiceID).Scan(&receiptCount, &duplicateCount, &quarantinedCount); err != nil {
		t.Fatalf("read AI Interaction receipts: %v", err)
	}
	if receiptCount != 3 || duplicateCount != 3 || quarantinedCount != 1 {
		t.Fatalf("AI Interaction receipt counts = (%d, %d, %d), want (3, 3, 1)",
			receiptCount, duplicateCount, quarantinedCount)
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
	t.Run("multiple full eligibility responses exceed former envelope limit", func(t *testing.T) {
		providerText := strings.Repeat("synthetic-evidence-", 300000)
		checks := []any{}
		for _, name := range []string{"Jane", "John"} {
			checks = append(checks, map[string]any{
				"status": "complete", "request": map[string]any{"firstName": name, "lastName": "Example", "plan": "Example Health"},
				"result": map[string]any{"status": "review", "providerResponse": map[string]any{"x12": providerText}},
			})
		}
		payload, _ := json.Marshal(map[string]any{
			"kind": "CLOSEOUT", "officeKey": "spring-hill", "officePhone": "+17275919997", "sourceCallId": "large-eligibility-evidence",
			"callerPhone": "+15555550125", "startedAt": startedAt.Format(time.RFC3339Nano), "endedAt": endedAt.Format(time.RFC3339), "status": "COMPLETED",
			"transcript":      map[string]any{"items": []any{map[string]any{"role": "user", "text": "Synthetic transcript retained"}}},
			"closeoutPayload": map[string]any{"eligibilityChecks": checks},
		})
		if len(payload) <= 8*1024*1024 || len(payload) >= 32*1024*1024 {
			t.Fatal("fixture must cross former limit within supported envelope")
		}
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/ai/interactions", "production-interaction-token", payload)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("large evidence rejected: %d", response.StatusCode)
		}
		response.Body.Close()
		var firstLength, secondLength int
		var transcriptText string
		err := pool.QueryRow(context.Background(), `SELECT length(closeout_payload->'eligibilityChecks'->0->'result'->'providerResponse'->>'x12'),length(closeout_payload->'eligibilityChecks'->1->'result'->'providerResponse'->>'x12'),transcript->'items'->0->>'text' FROM ai_interactions WHERE source_call_id='large-eligibility-evidence'`).Scan(&firstLength, &secondLength, &transcriptText)
		if err != nil || firstLength != len(providerText) || secondLength != len(providerText) || transcriptText != "Synthetic transcript retained" {
			t.Fatalf("large evidence lost: %d %d %q %v", firstLength, secondLength, transcriptText, err)
		}
	})
	postCloseout := func(
		sourceCallID string,
		callerPhone string,
		callStartedAt time.Time,
		appointmentOutcome map[string]any,
	) {
		t.Helper()
		payload := map[string]any{
			"kind":            "CLOSEOUT",
			"officeKey":       "spring-hill",
			"sourceCallId":    sourceCallID,
			"callerPhone":     callerPhone,
			"officePhone":     "+17275919997",
			"startedAt":       callStartedAt.Format(time.RFC3339),
			"endedAt":         callStartedAt.Add(5 * time.Minute).Format(time.RFC3339),
			"status":          "COMPLETED",
			"closeoutPayload": map[string]any{"callId": sourceCallID},
		}
		if appointmentOutcome != nil {
			payload["appointmentOutcome"] = appointmentOutcome
		}
		encoded, _ := json.Marshal(payload)
		response := request(
			t, server.Client(), http.MethodPost,
			server.URL+"/v1/ai/interactions", "production-interaction-token", encoded,
		)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create %s AI Interaction status = %d, body = %s",
				sourceCallID, response.StatusCode, readBody(t, response))
		}
		_ = response.Body.Close()
	}
	outcomeAt := now.Add(3 * time.Minute).Format(time.RFC3339)
	afterHoursStart := now.Add(-72 * time.Hour)
	postCloseout("abita-booking-63", "+17275550201", afterHoursStart, map[string]any{
		"action":           "BOOKED",
		"occurredAt":       afterHoursStart.Add(3 * time.Minute).Format(time.RFC3339),
		"newAppointmentId": "appointment-booked",
		"bookingResult":    map[string]any{"status": "booked", "appointmentId": 6303},
	})
	postCloseout("abita-cancellation-63", "+17275550202", now.Add(time.Minute), map[string]any{
		"action":             "CANCELLED",
		"occurredAt":         outcomeAt,
		"oldAppointmentId":   "appointment-cancelled",
		"cancellationResult": map[string]any{"status": "cancelled"},
	})
	postCloseout("abita-partial-63", "+17275550203", now.Add(time.Minute), map[string]any{
		"action":             "RESCHEDULED",
		"occurredAt":         outcomeAt,
		"oldAppointmentId":   "appointment-partial-old",
		"newAppointmentId":   "appointment-partial-new",
		"bookingResult":      map[string]any{"status": "booked", "appointmentId": 6304},
		"cancellationResult": map[string]any{"status": "error", "reason": "middleware_error"},
	})
	postCloseout("abita-indeterminate-63", "+17275550204", now.Add(time.Minute), nil)
	interactionClock = now.Add(30 * 24 * time.Hour)
	queryReviews := func(token, state string) map[string]api.Task {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"practiceId": practiceID, "state": state, "responsibility": "all"})
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/tasks/query", token, body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("query shared review Tasks: %d %s", response.StatusCode, readBody(t, response))
		}
		var page api.TaskPage
		decode(t, response, &page)
		items := map[string]api.Task{}
		for _, item := range page.Items {
			if item.Origin == api.APPOINTMENTREVIEW && item.SourceCallId != nil {
				items[*item.SourceCallId] = item
			}
		}
		return items
	}
	adminReviews, staffReviews := queryReviews("admin-token", "OPEN"), queryReviews("staff-token", "OPEN")
	if len(adminReviews) != 4 || len(staffReviews) != 4 || len(queryReviews("hidden-token", "OPEN")) != 0 {
		t.Fatalf("shared review scope/count: admin=%d staff=%d", len(adminReviews), len(staffReviews))
	}
	for _, source := range []string{"abita-booking-63", "abita-cancellation-63", "abita-call-63", "abita-partial-63"} {
		item, found := adminReviews[source]
		if !found || item.Id != staffReviews[source].Id || item.SourceMessage == nil || *item.SourceMessage == "" {
			t.Fatalf("shared source missing for %s", source)
		}
	}
	booking := adminReviews["abita-booking-63"]
	var bookingInteractionID string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM ai_interactions WHERE practice_id=$1 AND source_call_id='abita-booking-63'`, practiceID).Scan(&bookingInteractionID); err != nil {
		t.Fatal(err)
	}
	legacyRead := request(t, server.Client(), http.MethodPost, server.URL+"/v1/ai/interactions/"+bookingInteractionID+"/review", "admin-token", nil)
	if legacyRead.StatusCode != http.StatusNoContent {
		t.Fatalf("legacy read: %d %s", legacyRead.StatusCode, readBody(t, legacyRead))
	}
	_ = legacyRead.Body.Close()
	if len(queryReviews("admin-token", "OPEN")) != 4 || len(queryReviews("staff-token", "OPEN")) != 4 {
		t.Fatal("personal read completed shared work")
	}
	completeBody, _ := json.Marshal(map[string]any{"expectedVersion": booking.Version})
	denied := request(t, server.Client(), http.MethodPost, server.URL+"/v1/tasks/"+booking.Id.String()+"/complete", "hidden-token", completeBody)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-Location completion: %d %s", denied.StatusCode, readBody(t, denied))
	}
	_ = denied.Body.Close()
	completedResponse := request(t, server.Client(), http.MethodPost, server.URL+"/v1/tasks/"+booking.Id.String()+"/complete", "staff-token", completeBody)
	if completedResponse.StatusCode != http.StatusOK {
		t.Fatalf("shared completion: %d %s", completedResponse.StatusCode, readBody(t, completedResponse))
	}
	_ = completedResponse.Body.Close()
	for _, token := range []string{"admin-token", "staff-token"} {
		if len(queryReviews(token, "OPEN")) != 3 {
			t.Fatal("completion was not shared")
		}
		completed := queryReviews(token, "COMPLETED")["abita-booking-63"]
		if completed.Id != booking.Id || completed.CompletedBy == nil || completed.CompletedAt == nil || completed.SourceCallId == nil || *completed.SourceCallId != "abita-booking-63" {
			t.Fatalf("completion lost source/actor: %#v", completed)
		}
	}
	_, _, err = workModule.CreateAITask(context.Background(), work.CreateAITaskCommand{
		Service: access.ServiceIdentity{
			Subject:       "history-fixture",
			PracticeID:    practiceID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
		},
		OfficeKey:      "spring-hill",
		OfficePhone:    "+17275919997",
		SourceCallID:   "abita-call-63",
		IdempotencyKey: "issue-63-history-task",
		Phone:          "+17275550199",
		Summary:        "Verify paperwork.",
		Message:        "Caller also asked office staff to verify paperwork.",
		Category:       work.TaskCategoryAppointments,
		Urgency:        work.TaskUrgencyNormal,
	})
	if err != nil {
		t.Fatalf("create unified history Task fixture: %v", err)
	}

	timeline := request(
		t, server.Client(), http.MethodGet,
		server.URL+"/v1/engagements/+17275550199/timeline?groupCalls=true&practiceId="+
			url.QueryEscape(practiceID),
		"admin-token", nil,
	)
	if timeline.StatusCode != http.StatusOK {
		t.Fatalf("AI Interaction Engagement History status = %d, body = %s",
			timeline.StatusCode, readBody(t, timeline))
	}
	var history api.ConversationTimelinePage
	decode(t, timeline, &history)
	if len(history.Items) != 1 || history.Items[0].Type != "CALL_HISTORY" || history.Items[0].Entries == nil {
		t.Fatalf("AI Interaction Engagement History = %#v", history)
	}
	entries := *history.Items[0].Entries
	if len(entries) != 3 || entries[0].AiInteraction == nil ||
		entries[0].AiInteraction.Id.String() != first.InteractionID ||
		entries[0].AiInteraction.AppointmentOutcome != "RESCHEDULE" || entries[1].Task == nil {
		t.Fatalf("AI outcome and follow-up were not preserved together: %#v", entries)
	}

	sequenceStartBody, _ := json.Marshal(map[string]any{
		"kind":         "START",
		"sourceCallId": "abita-sequence-63",
		"callerPhone":  "+17275550210",
		"officePhone":  "+17275919997",
		"startedAt":    startedAt.Format(time.RFC3339Nano),
		"status":       "IN_PROGRESS",
	})
	sequenceStart := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "production-interaction-token", sequenceStartBody,
	)
	if sequenceStart.StatusCode != http.StatusCreated {
		t.Fatalf("sequence start status = %d, body = %s",
			sequenceStart.StatusCode, readBody(t, sequenceStart))
	}
	var sequenceReceipt struct {
		InteractionID string `json:"interactionId"`
	}
	decode(t, sequenceStart, &sequenceReceipt)
	bookingOccurredAt := now.Add(6 * time.Minute)
	postSequenceCheckpoint := func(outcome map[string]any) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"kind":               "OUTCOME_CHECKPOINT",
			"officeKey":          "spring-hill",
			"sourceCallId":       "abita-sequence-63",
			"callerPhone":        "+17275550210",
			"officePhone":        "+17275919997",
			"startedAt":          startedAt.Format(time.RFC3339Nano),
			"status":             "IN_PROGRESS",
			"appointmentOutcome": outcome,
		})
		response := request(
			t, server.Client(), http.MethodPost,
			server.URL+"/v1/ai/interactions", "production-interaction-token", payload,
		)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("sequence checkpoint status = %d, body = %s",
				response.StatusCode, readBody(t, response))
		}
		_ = response.Body.Close()
	}
	postSequenceCheckpoint(map[string]any{
		"action":           "BOOKED",
		"occurredAt":       bookingOccurredAt.Format(time.RFC3339Nano),
		"newAppointmentId": "appointment-sequence-new",
		"bookingResult":    map[string]any{"status": "booked"},
	})
	postSequenceCheckpoint(map[string]any{
		"action":            "BOOKED",
		"occurredAt":        bookingOccurredAt.Format(time.RFC3339Nano),
		"externalPatientId": "patient-sequence",
		"bookingResult": map[string]any{
			"status":        "booked",
			"appointmentId": "appointment-sequence-new",
		},
	})
	postSequenceCheckpoint(map[string]any{
		"action":             "CANCELLED",
		"occurredAt":         bookingOccurredAt.Add(time.Minute).Format(time.RFC3339Nano),
		"externalPatientId":  "patient-sequence",
		"oldAppointmentId":   "appointment-sequence-new",
		"cancellationResult": map[string]any{"status": "cancelled"},
	})
	sequenceDetail := request(
		t, server.Client(), http.MethodGet,
		server.URL+"/v1/ai/interactions/"+sequenceReceipt.InteractionID,
		"admin-token", nil,
	)
	if sequenceDetail.StatusCode != http.StatusOK {
		t.Fatalf("sequence detail status = %d, body = %s",
			sequenceDetail.StatusCode, readBody(t, sequenceDetail))
	}
	var sequenceStored struct {
		AppointmentOutcome string          `json:"appointmentOutcome"`
		ExternalPatientID  string          `json:"externalPatientId"`
		OldAppointmentID   string          `json:"oldAppointmentId"`
		BookingResult      json.RawMessage `json:"bookingResult"`
		CancellationResult json.RawMessage `json:"cancellationResult"`
	}
	decode(t, sequenceDetail, &sequenceStored)
	if sequenceStored.AppointmentOutcome != "CANCELLATION" ||
		sequenceStored.ExternalPatientID != "patient-sequence" ||
		sequenceStored.OldAppointmentID != "appointment-sequence-new" ||
		!bytes.Contains(sequenceStored.BookingResult, []byte(`"appointmentId":"appointment-sequence-new"`)) ||
		!bytes.Contains(sequenceStored.CancellationResult, []byte(`"status":"cancelled"`)) {
		t.Fatalf("latest appointment projection = %#v", sequenceStored)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM ai_interaction_receipts
		WHERE interaction_id = $1
	`, sequenceReceipt.InteractionID).Scan(&receiptCount); err != nil {
		t.Fatalf("read sequence AI Interaction receipts: %v", err)
	}
	if receiptCount != 4 {
		t.Fatalf("sequence AI Interaction receipt count = %d, want 4", receiptCount)
	}

	if _, err := pool.Exec(context.Background(), `
		CREATE FUNCTION reject_ai_interaction_projection() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic projection failure';
		END
		$$;
		CREATE TRIGGER reject_ai_interaction_projection
		BEFORE INSERT ON ai_interactions
		FOR EACH ROW EXECUTE FUNCTION reject_ai_interaction_projection();
	`); err != nil {
		t.Fatalf("install AI Interaction projection failure: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS reject_ai_interaction_projection ON ai_interactions;
			DROP FUNCTION IF EXISTS reject_ai_interaction_projection();
		`)
	})
	recoverableBody, _ := json.Marshal(map[string]any{
		"kind":         "START",
		"officeKey":    "spring-hill",
		"sourceCallId": "abita-recoverable-receipt-63",
		"callerPhone":  "+17275550213",
		"officePhone":  "+17275919997",
		"startedAt":    startedAt.Format(time.RFC3339Nano),
		"status":       "IN_PROGRESS",
	})
	recoverable := request(
		t, server.Client(), http.MethodPost,
		server.URL+"/v1/ai/interactions", "production-interaction-token", recoverableBody,
	)
	if recoverable.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("recoverable projection failure status = %d, body = %s",
			recoverable.StatusCode, readBody(t, recoverable))
	}
	_ = recoverable.Body.Close()
	if _, err := pool.Exec(context.Background(), `
		DROP TRIGGER reject_ai_interaction_projection ON ai_interactions;
		DROP FUNCTION reject_ai_interaction_projection();
	`); err != nil {
		t.Fatalf("remove AI Interaction projection failure: %v", err)
	}
	processed, err := interactionModule.ProcessNextReceipt(context.Background())
	if err != nil || !processed {
		t.Fatalf("recover pending AI Interaction receipt = %t, %v", processed, err)
	}
	var recoveredState string
	if err := pool.QueryRow(context.Background(), `
		SELECT state
		FROM ai_interaction_receipts
		WHERE practice_id = $1 AND source_call_id = 'abita-recoverable-receipt-63'
	`, practiceID).Scan(&recoveredState); err != nil {
		t.Fatalf("read recovered AI Interaction receipt: %v", err)
	}
	if recoveredState != "PROJECTED" {
		t.Fatalf("recovered AI Interaction receipt state = %q", recoveredState)
	}
}

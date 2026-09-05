package httpapi_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOperatorAIAnalyticsIsScopedPaginatedAndNormalized(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject:       "operator-ai-analytics-subject",
		Email:         "operator-ai-analytics@acuity.test",
		EmailVerified: true,
	}
	admin := access.Identity{
		Subject:       "admin-ai-analytics-subject",
		Email:         "admin-ai-analytics@acuity.test",
		EmailVerified: true,
	}
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "operator-ai-analytics-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key:  "operator-ai-practice",
			Name: "Operator AI Practice",
			Locations: []access.LocationProvision{
				{Key: "north", Name: "North Office"},
				{Key: "south", Name: "South Office"},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "admin",
				Email:         admin.Email,
				Role:          access.RoleAdmin,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision operator AI analytics fixture: %v", err)
	}
	testaccess.Activate(t, accessModule, operator)
	testaccess.Activate(t, accessModule, admin)

	var practiceID, northID, southID string
	if err := pool.QueryRow(context.Background(), `
		SELECT
			practice.id::text,
			max(location.id::text) FILTER (WHERE location.provisioning_key = 'north'),
			max(location.id::text) FILTER (WHERE location.provisioning_key = 'south')
		FROM access_practices practice
		JOIN access_locations location ON location.practice_id = practice.id
		WHERE practice.provisioning_key = 'operator-ai-practice'
		GROUP BY practice.id
	`).Scan(&practiceID, &northID, &southID); err != nil {
		t.Fatalf("read operator AI analytics scope: %v", err)
	}

	richID := "10000000-0000-0000-0000-000000000001"
	escalatedID := "10000000-0000-0000-0000-000000000002"
	insertOperatorAIInteraction(t, pool, operatorAIInteractionFixture{
		ID:         richID,
		PracticeID: practiceID,
		LocationID: northID,
		SourceCall: "operator-rich-call",
		Phone:      "+17275550101",
		StartedAt:  now.Add(-2 * time.Hour),
		EndedAt:    now.Add(-90 * time.Minute),
		Status:     "COMPLETED",
		Summary:    "Caller tried to reschedule.",
		Transcript: map[string]any{"chat_history": map[string]any{"items": []map[string]any{
			{"id": "system", "type": "message", "role": "system", "content": []string{"never expose this system prompt"}, "created_at": now.Add(-119 * time.Minute).UnixMilli()},
			{"id": "caller", "type": "message", "role": "user", "content": []string{"Please move my appointment."}, "created_at": now.Add(-118 * time.Minute).UnixMilli(), "metrics": map[string]any{"transcription_delay": 0.2}},
			{"id": "transfer-tool", "type": "function_call", "name": "transfer_call", "call_id": "transfer-attempt", "arguments": `{}`, "created_at": now.Add(-117 * time.Minute).UnixMilli()},
			{"id": "transfer-result", "type": "function_call_output", "name": "transfer_call", "call_id": "transfer-attempt", "output": `"Transfer initiated."`, "is_error": false, "created_at": now.Add(-116 * time.Minute).UnixMilli()},
			{"id": "tool", "type": "function_call", "name": "reschedule_appointment", "call_id": "tool-call-1", "arguments": `{"appointmentId":"appointment-old"}`, "created_at": now.Add(-115 * time.Minute).UnixMilli()},
			{"id": "result", "type": "function_call_output", "name": "reschedule_appointment", "call_id": "tool-call-1", "output": `"The appointment could not be moved."`, "is_error": true, "created_at": now.Add(-114 * time.Minute).UnixMilli()},
			{"id": "agent", "type": "message", "role": "assistant", "content": []string{"The appointment could not be moved."}, "created_at": now.Add(-113 * time.Minute).UnixMilli(), "metrics": map[string]any{"llm_node_ttft": 0.4, "tts_node_ttfb": 0.1}},
		}}},
		Closeout: map[string]any{
			"turnMetrics": []map[string]any{
				{"itemId": "caller", "metrics": map[string]any{"e2eLatency": 0.8}},
				{"itemId": "agent", "metrics": map[string]any{"e2eLatency": 1.2}},
			},
			"domainOutcomes": []map[string]any{
				{"callId": "tool-call-1", "toolName": "reschedule_appointment", "outcome": "rescheduled", "status": "failed", "occurredAt": now.Add(-114 * time.Minute)},
				{"callId": "transfer-attempt", "toolName": "transfer_call", "outcome": "transfer_started", "status": "success", "occurredAt": now.Add(-116 * time.Minute)},
			},
		},
		AppointmentOutcome: "PARTIAL",
		BookingResult:      map[string]any{"status": "booked", "receiptId": "booking-receipt-1"},
		CancellationResult: map[string]any{"status": "error", "receiptId": "cancellation-receipt-1"},
	})
	insertOperatorAIInteraction(t, pool, operatorAIInteractionFixture{
		ID:         escalatedID,
		PracticeID: practiceID,
		LocationID: northID,
		SourceCall: "operator-escalated-call",
		Phone:      "+17275550102",
		StartedAt:  now.Add(-time.Hour),
		EndedAt:    now.Add(-55 * time.Minute),
		Status:     "ESCALATED",
		Closeout: map[string]any{
			"turnMetrics":    []map[string]any{{"itemId": "agent", "metrics": map[string]any{"e2eLatency": 2.0}}},
			"toolExecutions": []map[string]any{{"callId": "lookup-1", "createdAt": now.Add(-58 * time.Minute), "outputClass": "patient_verified", "status": "success", "toolName": "resolve_patient"}},
		},
		AppointmentOutcome: "BOOKING",
	})
	insertOperatorAIInteraction(t, pool, operatorAIInteractionFixture{
		ID:                 "10000000-0000-0000-0000-000000000003",
		PracticeID:         practiceID,
		LocationID:         southID,
		SourceCall:         "operator-south-call",
		Phone:              "+17275550103",
		StartedAt:          now.Add(-30 * time.Minute),
		EndedAt:            now.Add(-20 * time.Minute),
		Status:             "COMPLETED",
		AppointmentOutcome: "INDETERMINATE",
	})
	insertOperatorAIInteraction(t, pool, operatorAIInteractionFixture{
		ID:                 "10000000-0000-0000-0000-000000000004",
		PracticeID:         practiceID,
		LocationID:         northID,
		SourceCall:         "operator-old-call",
		Phone:              "+17275550104",
		StartedAt:          now.Add(-8 * 24 * time.Hour),
		EndedAt:            now.Add(-8*24*time.Hour + time.Minute),
		Status:             "COMPLETED",
		AppointmentOutcome: "INDETERMINATE",
	})

	serviceAuth, err := access.NewServiceAuthenticator(access.ServiceCredential{
		Token: "unused-operator-ai-service-token",
		Identity: access.ServiceIdentity{
			Subject:       "unused-operator-ai-service",
			PracticeID:    practiceID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityIngestAIInteraction},
		},
	}, access.ServiceCredential{
		Token: "unused-demo-operator-ai-service-token",
		Identity: access.ServiceIdentity{
			Subject:       "unused-demo-operator-ai-service",
			PracticeID:    "00000000-0000-0000-0000-000000000001",
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityIngestAIInteraction},
		},
	})
	if err != nil {
		t.Fatalf("create operator AI service authenticator: %v", err)
	}
	workModule := work.New(pool, accessModule, func() time.Time { return now })
	callingModule := humancalling.New(pool, accessModule, httpCallingProvider{}, humancalling.Config{}, nil)
	handler, err := httpapi.NewPortal(
		httpapi.Config{AcquireTimeout: time.Second},
		pool,
		httpapi.PortalDependencies{
			Access: accessModule,
			Authenticator: staticAuthenticator{
				"operator-token": operator,
				"admin-token":    admin,
			},
			Calling:              callingModule,
			Interactions:         interaction.New(pool, accessModule, func() time.Time { return now }),
			Messaging:            messaging.New(pool, accessModule, workModule, nil, messaging.Config{}, nil),
			Work:                 workModule,
			Workspace:            workspace.New(pool, accessModule),
			ServiceAuthenticator: serviceAuth,
		},
	)
	if err != nil {
		t.Fatalf("create operator AI analytics portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	usage := `[{"type":"llm_usage","provider":"livekit","model":"google/gemma-4-31b-it","input_tokens":1000000,"input_cached_tokens":250000,"output_tokens":100000},{"type":"stt_usage","provider":"livekit","model":"assemblyai/universal-3-5-pro","audio_duration":120},{"type":"tts_usage","provider":"rime","model":"coda","characters_count":10000}]`
	// Native LiveKit reports also contain duplicate stream usage from the
	// fallback wrapper. It must neither add cost nor suppress the averages.
	usage = strings.TrimSuffix(usage, "]") + `,{"type":"llm_usage","provider":"unknown","model":"FallbackAdapter","input_tokens":1000000,"input_cached_tokens":250000,"output_tokens":100000}]`
	if _, err := pool.Exec(context.Background(), `UPDATE ai_interactions SET transcript = jsonb_set(transcript, '{usage}', $2::jsonb) WHERE id=$1::uuid`, richID, usage); err != nil {
		t.Fatal(err)
	}
	costBody, _ := json.Marshal(map[string]any{"practiceId": practiceID, "locationId": northID, "range": "7d", "timeZone": "America/Los_Angeles"})
	for _, token := range []string{"", "admin-token", "operator-token"} {
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/operator/ai-costs/query", token, costBody)
		if token != "operator-token" {
			want := http.StatusForbidden
			if token == "" {
				want = http.StatusUnauthorized
			}
			if response.StatusCode != want {
				t.Fatalf("cost authorization = %d, want %d", response.StatusCode, want)
			}
			_ = response.Body.Close()
			continue
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("cost query = %d: %s", response.StatusCode, readBody(t, response))
		}
		var costs interaction.CostAnalytics
		decode(t, response, &costs)
		if costs.TotalCalls != 2 || costs.PricedCalls != 1 || costs.UnpricedUsage != 0 || costs.CostPerCallUSD == nil ||
			math.Abs(*costs.CostPerCallUSD-1.39) > 1e-9 || costs.CostPerMinuteUSD == nil ||
			math.Abs(*costs.CostPerMinuteUSD-(1.39/30)) > 1e-9 || math.Abs(costs.TotalCostUSD-1.4575) > 1e-9 {
			t.Fatalf("scoped partial cost estimate: %+v", costs)
		}
		if costs.Items[0].Quantity != 750000 || costs.Items[1].Quantity != 250000 || costs.Items[3].Quantity != 2 {
			t.Fatalf("native usage quantities: %+v", costs.Items)
		}
		var shares, daily float64
		for _, item := range costs.Items {
			if item.SharePercent != nil {
				shares += *item.SharePercent
			}
		}
		for _, day := range costs.Daily {
			daily += day.CostUSD
		}
		if math.Abs(shares-100) > 1e-9 || math.Abs(daily-costs.TotalCostUSD) > 1e-9 {
			t.Fatal("cost breakdown does not reconcile")
		}
	}
	invalidCostBody, _ := json.Marshal(map[string]any{"practiceId": practiceID, "range": "7d", "timeZone": "Not/AZone"})
	invalidCost := request(t, server.Client(), http.MethodPost, server.URL+"/v1/operator/ai-costs/query", "operator-token", invalidCostBody)
	if invalidCost.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid cost timezone = %d", invalidCost.StatusCode)
	}
	_ = invalidCost.Body.Close()

	queryBody, _ := json.Marshal(map[string]any{
		"practiceId": practiceID,
		"locationId": northID,
		"range":      "7d",
		"limit":      1,
	})
	denied := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/operator/ai-analytics/query", "admin-token", queryBody)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("non-operator analytics query status = %d, body = %s", denied.StatusCode, readBody(t, denied))
	}
	_ = denied.Body.Close()

	firstPageResponse := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/operator/ai-analytics/query", "operator-token", queryBody)
	if firstPageResponse.StatusCode != http.StatusOK {
		t.Fatalf("operator analytics query status = %d, body = %s", firstPageResponse.StatusCode, readBody(t, firstPageResponse))
	}
	var firstPage operatorAIAnalyticsTestPage
	decode(t, firstPageResponse, &firstPage)
	if firstPage.Summary == nil || firstPage.Summary.TotalCalls != 2 || firstPage.Summary.TransferCount != 1 ||
		math.Abs(firstPage.Summary.TransferRate-0.5) > 0.0001 ||
		firstPage.Summary.BookingCount != 1 || firstPage.Summary.CancellationCount != 0 ||
		firstPage.Summary.RescheduleCount != 0 ||
		firstPage.Summary.ToolCallCount != 3 || firstPage.Summary.ToolErrorCount != 1 ||
		math.Abs(firstPage.Summary.ToolFailureRate-(1.0/3.0)) > 0.0001 ||
		firstPage.Summary.P50SttMs != 200 || firstPage.Summary.P50TtftMs != 400 ||
		firstPage.Summary.P50TtsTtfbMs != 100 ||
		firstPage.Summary.P50TotalLatencyMs != 1200 ||
		firstPage.Summary.P90TotalLatencyMs != 2000 ||
		firstPage.Summary.P99TotalLatencyMs != 2000 {
		t.Fatalf("operator analytics summary = %#v", firstPage.Summary)
	}
	if len(firstPage.Calls) != 1 || firstPage.Calls[0].ID != escalatedID ||
		!firstPage.Calls[0].Transferred || firstPage.Calls[0].TranscriptAvailable ||
		firstPage.Calls[0].ToolCallCount != 1 || firstPage.Calls[0].ToolErrorCount != 0 ||
		!slices.Equal(firstPage.Calls[0].ToolActions, []string{"resolve_patient"}) ||
		firstPage.NextCursor == "" {
		t.Fatalf("operator analytics first page = %#v", firstPage)
	}

	secondBody, _ := json.Marshal(map[string]any{
		"practiceId": practiceID,
		"locationId": northID,
		"range":      "7d",
		"limit":      1,
		"cursor":     firstPage.NextCursor,
	})
	// The continuation must not slide the reporting window as time advances.
	initialNow := now
	now = now.Add(8 * 24 * time.Hour)
	secondPageResponse := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/operator/ai-analytics/query", "operator-token", secondBody)
	if secondPageResponse.StatusCode != http.StatusOK {
		t.Fatalf("operator analytics second page status = %d, body = %s", secondPageResponse.StatusCode, readBody(t, secondPageResponse))
	}
	var secondPage operatorAIAnalyticsTestPage
	decode(t, secondPageResponse, &secondPage)
	now = initialNow
	if secondPage.Summary != nil {
		t.Fatal("continuation recalculated the full-range summary")
	}
	if len(secondPage.Calls) != 1 || secondPage.Calls[0].ID != richID ||
		secondPage.Calls[0].Transferred || !secondPage.Calls[0].TranscriptAvailable ||
		secondPage.Calls[0].P50SttMs != 200 || secondPage.Calls[0].P50TtftMs != 400 ||
		secondPage.Calls[0].P50TtsTtfbMs != 100 ||
		secondPage.Calls[0].ToolCallCount != 2 || secondPage.Calls[0].ToolErrorCount != 1 ||
		!slices.Equal(secondPage.Calls[0].ToolActions, []string{"transfer_call", "reschedule_appointment"}) ||
		secondPage.Calls[0].P50TotalLatencyMs != 1000 || secondPage.NextCursor != "" {
		t.Fatalf("operator analytics second page = %#v", secondPage)
	}

	deniedPage := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/operator/ai-analytics/query", "admin-token", secondBody)
	if deniedPage.StatusCode != http.StatusForbidden {
		t.Fatalf("continuation bypassed operator authorization: %d", deniedPage.StatusCode)
	}
	_ = deniedPage.Body.Close()

	allLocationsBody, _ := json.Marshal(map[string]any{
		"practiceId": practiceID,
		"range":      "7d",
	})
	allLocationsResponse := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/operator/ai-analytics/query", "operator-token", allLocationsBody)
	if allLocationsResponse.StatusCode != http.StatusOK {
		t.Fatalf("all-Location analytics status = %d, body = %s", allLocationsResponse.StatusCode, readBody(t, allLocationsResponse))
	}
	var allLocations operatorAIAnalyticsTestPage
	decode(t, allLocationsResponse, &allLocations)
	if allLocations.Summary.TotalCalls != 3 || len(allLocations.Calls) != 3 {
		t.Fatalf("all-Location analytics page = %#v", allLocations)
	}

	detailResponse := request(t, server.Client(), http.MethodGet,
		server.URL+"/v1/operator/ai-interactions/"+richID+"/analytics", "operator-token", nil)
	if detailResponse.StatusCode != http.StatusOK {
		t.Fatalf("operator analytics detail status = %d, body = %s", detailResponse.StatusCode, readBody(t, detailResponse))
	}
	var detail struct {
		ID                string         `json:"id"`
		P50SttMs          int            `json:"p50SttMs"`
		P50TtftMs         int            `json:"p50TtftMs"`
		P50TtsTtfbMs      int            `json:"p50TtsTtfbMs"`
		P50TotalLatencyMs int            `json:"p50TotalLatencyMs"`
		BookingResult     map[string]any `json:"bookingResult"`
		Timeline          []struct {
			Kind    string         `json:"kind"`
			Text    string         `json:"text"`
			Name    string         `json:"name"`
			CallID  string         `json:"callId"`
			Payload map[string]any `json:"payload"`
			Error   string         `json:"error"`
		} `json:"timeline"`
		ToolExecutions []struct {
			Name          string `json:"name"`
			Status        string `json:"status"`
			OutputClass   string `json:"outputClass"`
			DomainOutcome string `json:"domainOutcome"`
			DomainStatus  string `json:"domainStatus"`
			TaskID        string `json:"taskId"`
		} `json:"toolExecutions"`
	}
	decode(t, detailResponse, &detail)
	if detail.ID != richID || detail.P50SttMs != 200 || detail.P50TtftMs != 400 ||
		detail.P50TtsTtfbMs != 100 || detail.P50TotalLatencyMs != 1000 ||
		detail.BookingResult["receiptId"] != "booking-receipt-1" ||
		len(detail.Timeline) != 6 || len(detail.ToolExecutions) != 2 {
		t.Fatalf("operator analytics detail = %#v", detail)
	}
	for _, item := range detail.Timeline {
		if item.Text == "never expose this system prompt" || item.Kind == "SYSTEM_MESSAGE" {
			t.Fatalf("operator analytics detail exposed system prompt: %#v", detail.Timeline)
		}
	}
	if detail.Timeline[3].Kind != "TOOL_CALL" || detail.Timeline[3].CallID != "tool-call-1" ||
		detail.Timeline[3].Payload["appointmentId"] != "appointment-old" ||
		detail.Timeline[4].Kind != "TOOL_RESULT" || detail.Timeline[4].Error == "" ||
		detail.ToolExecutions[1].Status != "ERROR" ||
		detail.ToolExecutions[1].DomainOutcome != "rescheduled" ||
		detail.ToolExecutions[1].DomainStatus != "failed" {
		t.Fatalf("normalized operator tool evidence = %#v / %#v", detail.Timeline, detail.ToolExecutions)
	}

	deniedDetail := request(t, server.Client(), http.MethodGet,
		server.URL+"/v1/operator/ai-interactions/"+richID+"/analytics", "admin-token", nil)
	if deniedDetail.StatusCode != http.StatusForbidden {
		t.Fatalf("non-operator analytics detail status = %d, body = %s", deniedDetail.StatusCode, readBody(t, deniedDetail))
	}
	_ = deniedDetail.Body.Close()

	// Exercise the real cost endpoint under its SQL budget with large session
	// reports. Conversation size must not determine the range-read payload.
	largeReport, _ := json.Marshal(map[string]any{
		"items": []any{map[string]any{"type": "message", "content": strings.Repeat("Synthetic conversation content. ", 5000)}},
		"usage": json.RawMessage(usage),
	})
	const largeCalls = 2000
	if _, err := pool.Exec(context.Background(), `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript)
	 SELECT 'cost-scale',$1,$2,'cost-scale-'||n,'+15555550101','+15555550102',$3::timestamptz-interval '10 minutes',$3,'COMPLETED',3,$4 FROM generate_series(1,$5) n`, practiceID, southID, now.Add(-time.Hour), largeReport, largeCalls); err != nil {
		t.Fatal(err)
	}
	largeCostBody, _ := json.Marshal(map[string]any{"practiceId": practiceID, "locationId": southID, "range": "30d", "timeZone": "UTC"})
	started := time.Now()
	largeCostResponse := request(t, server.Client(), http.MethodPost, server.URL+"/v1/operator/ai-costs/query", "operator-token", largeCostBody)
	if largeCostResponse.StatusCode != http.StatusOK {
		t.Fatalf("large cost query = %d: %s", largeCostResponse.StatusCode, readBody(t, largeCostResponse))
	}
	var largeCosts interaction.CostAnalytics
	decode(t, largeCostResponse, &largeCosts)
	t.Logf("cost HTTP read: %d calls with %d-byte reports in %s", largeCalls, len(largeReport), time.Since(started))
	if largeCosts.TotalCalls != largeCalls+1 || largeCosts.PricedCalls != largeCalls || largeCosts.Items[0].Quantity != largeCalls*750000 {
		t.Fatalf("large cost query lost usage: %+v", largeCosts)
	}
	// Correcting source usage must replace the derived estimate immediately.
	if _, err := pool.Exec(context.Background(), `UPDATE ai_interactions SET transcript=jsonb_set(transcript,'{usage}','[]') WHERE source_call_id='cost-scale-1'`); err != nil {
		t.Fatal(err)
	}
	correctedResponse := request(t, server.Client(), http.MethodPost, server.URL+"/v1/operator/ai-costs/query", "operator-token", largeCostBody)
	if correctedResponse.StatusCode != http.StatusOK {
		t.Fatalf("corrected cost query = %d: %s", correctedResponse.StatusCode, readBody(t, correctedResponse))
	}
	var corrected interaction.CostAnalytics
	decode(t, correctedResponse, &corrected)
	if corrected.PricedCalls != largeCalls-1 || corrected.Items[0].Quantity != (largeCalls-1)*750000 {
		t.Fatalf("cost query ignored corrected usage: %+v", corrected)
	}
}

type operatorAIInteractionFixture struct {
	ID                 string
	PracticeID         string
	LocationID         string
	SourceCall         string
	Phone              string
	StartedAt          time.Time
	EndedAt            time.Time
	Status             string
	Summary            string
	Transcript         map[string]any
	Closeout           map[string]any
	AppointmentOutcome string
	BookingResult      map[string]any
	CancellationResult map[string]any
}

type operatorAIAnalyticsTestPage struct {
	Summary *struct {
		TotalCalls        int     `json:"totalCalls"`
		BookingCount      int     `json:"bookingCount"`
		CancellationCount int     `json:"cancellationCount"`
		RescheduleCount   int     `json:"rescheduleCount"`
		P50SttMs          int     `json:"p50SttMs"`
		P90SttMs          int     `json:"p90SttMs"`
		P99SttMs          int     `json:"p99SttMs"`
		P50TtftMs         int     `json:"p50TtftMs"`
		P90TtftMs         int     `json:"p90TtftMs"`
		P99TtftMs         int     `json:"p99TtftMs"`
		P50TtsTtfbMs      int     `json:"p50TtsTtfbMs"`
		P90TtsTtfbMs      int     `json:"p90TtsTtfbMs"`
		P99TtsTtfbMs      int     `json:"p99TtsTtfbMs"`
		P50TotalLatencyMs int     `json:"p50TotalLatencyMs"`
		P90TotalLatencyMs int     `json:"p90TotalLatencyMs"`
		P99TotalLatencyMs int     `json:"p99TotalLatencyMs"`
		TransferCount     int     `json:"transferCount"`
		TransferRate      float64 `json:"transferRate"`
		ToolCallCount     int     `json:"toolCallCount"`
		ToolErrorCount    int     `json:"toolErrorCount"`
		ToolFailureRate   float64 `json:"toolFailureRate"`
	} `json:"summary"`
	Calls []struct {
		ID                  string   `json:"id"`
		P50SttMs            int      `json:"p50SttMs"`
		P50TtftMs           int      `json:"p50TtftMs"`
		P50TtsTtfbMs        int      `json:"p50TtsTtfbMs"`
		P50TotalLatencyMs   int      `json:"p50TotalLatencyMs"`
		ToolCallCount       int      `json:"toolCallCount"`
		ToolErrorCount      int      `json:"toolErrorCount"`
		ToolActions         []string `json:"toolActions"`
		Transferred         bool     `json:"transferred"`
		TranscriptAvailable bool     `json:"transcriptAvailable"`
	} `json:"calls"`
	NextCursor string `json:"nextCursor"`
}

func insertOperatorAIInteraction(
	t *testing.T,
	pool *pgxpool.Pool,
	fixture operatorAIInteractionFixture,
) {
	t.Helper()
	transcript, _ := json.Marshal(fixture.Transcript)
	closeout, _ := json.Marshal(fixture.Closeout)
	booking, _ := json.Marshal(fixture.BookingResult)
	cancellation, _ := json.Marshal(fixture.CancellationResult)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO ai_interactions (
			id, service_subject, practice_id, location_id, source_call_id,
			phone, office_phone, started_at, ended_at, status, summary,
			transcript, appointment_outcome, booking_result, cancellation_result,
			closeout_payload, lifecycle_stage, created_at, updated_at
		) VALUES (
			$1, 'operator-ai-test', $2, $3, $4,
			$5, '+17275919997', $6, $7, $8, NULLIF($9, ''),
			$10, $11, $12, $13, $14, 3, $6, $7
		)
	`, fixture.ID, fixture.PracticeID, fixture.LocationID, fixture.SourceCall,
		fixture.Phone, fixture.StartedAt, fixture.EndedAt, fixture.Status, fixture.Summary,
		nullJSON(transcript, fixture.Transcript), fixture.AppointmentOutcome,
		nullJSON(booking, fixture.BookingResult), nullJSON(cancellation, fixture.CancellationResult),
		nullJSON(closeout, fixture.Closeout))
	if err != nil {
		t.Fatalf("insert operator AI Interaction %q: %v", fixture.SourceCall, err)
	}
}

func nullJSON(encoded []byte, value map[string]any) any {
	if value == nil {
		return nil
	}
	return json.RawMessage(encoded)
}

package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func TestMissedHandoffOverHTTPJoinsTheAITaskFiledForTheSameCall(t *testing.T) {
	outcome := missHandoffAfterAITaskOverHTTP(t, "same-ai-call", "same-ai-call")
	if len(outcome.completed) != 0 || len(outcome.open) != 1 {
		t.Fatalf("staff see %d open and %d completed Tasks; want only the AI Task open",
			len(outcome.open), len(outcome.completed))
	}
	task := outcome.open[0]
	if task.Id != outcome.aiTaskID || task.Origin != api.ABITAAI ||
		len(task.Interactions) != 1 ||
		task.Interactions[0].CallId.String() != outcome.missedCallID {
		t.Fatalf("open Task = %s origin %s interactions %#v; want AI Task %s holding missed call %s",
			task.Id, task.Origin, task.Interactions, outcome.aiTaskID, outcome.missedCallID)
	}
}

func TestMissedHandoffOverHTTPAfterAnEarlierCallsAITaskGetsARecoveryTask(t *testing.T) {
	outcome := missHandoffAfterAITaskOverHTTP(t, "earlier-ai-call", "later-ai-call")
	if len(outcome.completed) != 0 || len(outcome.open) != 2 {
		t.Fatalf("staff see %d open and %d completed Tasks; want the AI Task and one recovery Task open",
			len(outcome.open), len(outcome.completed))
	}
	for _, task := range outcome.open {
		if task.Id == outcome.aiTaskID {
			if task.Origin != api.ABITAAI || len(task.Interactions) != 0 ||
				task.Version != outcome.aiTaskVersionBeforeMiss {
				t.Fatalf("AI Task from the earlier call = version %d interactions %#v; want it untouched",
					task.Version, task.Interactions)
			}
			continue
		}
		if task.Origin != api.MISSEDCALLRECOVERY || len(task.Interactions) != 1 ||
			task.Interactions[0].CallId.String() != outcome.missedCallID {
			t.Fatalf("second open Task = origin %s interactions %#v; want a recovery Task holding missed call %s",
				task.Origin, task.Interactions, outcome.missedCallID)
		}
	}
}

type missedHandoffHTTPOutcome struct {
	aiTaskID                openapi_types.UUID
	aiTaskVersionBeforeMiss int64
	missedCallID            string
	open                    []api.Task
	completed               []api.Task
}

type clientStateProvider struct {
	mu           sync.Mutex
	clientStates map[humancalling.CommandAction]string
}

func (provider *clientStateProvider) Execute(
	_ context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if state, ok := command.Payload["client_state"].(string); ok {
		provider.clientStates[command.Action] = state
	}
	if command.Action == humancalling.CommandCreateCredential {
		return humancalling.ProviderResult{
			CredentialID: "missed-handoff-credential",
			SIPUsername:  "missed-handoff-sip-user",
		}, nil
	}
	return humancalling.ProviderResult{}, nil
}

func (provider *clientStateProvider) clientState(
	t *testing.T,
	action humancalling.CommandAction,
) string {
	t.Helper()
	provider.mu.Lock()
	defer provider.mu.Unlock()
	state := provider.clientStates[action]
	if state == "" {
		t.Fatalf("provider never received %s", action)
	}
	return state
}

func missHandoffAfterAITaskOverHTTP(
	t *testing.T,
	taskCallID string,
	handoffCallID string,
) missedHandoffHTTPOutcome {
	t.Helper()
	db := testdb.Open(t)
	now := time.Date(2026, time.October, 14, 17, 15, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	accessModule := access.New(db.As(testdb.Portal), clock)
	if _, err := access.New(db.Owner, clock).Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "missed-handoff-http-test",
		Practices: []access.PracticeProvision{{
			Key:  "calling-practice",
			Name: "Calling Practice",
			Locations: []access.LocationProvision{{
				Key:             "calling-location",
				Name:            "Calling Location",
				AbitaOfficeKeys: []string{"calling-office"},
			}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "calling-staff",
				Email:         "staff@calling.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision missed handoff HTTP fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "calling-staff-subject",
		Email:         "staff@calling.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	provider := &clientStateProvider{clientStates: map[humancalling.CommandAction]string{}}
	callingConfig := humancalling.Config{
		HandoffSIPDomain:   "synthetic.sip.telnyx.com",
		RingWindowDuration: 20 * time.Second,
		HandoffTokenKey:    []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:      "http-call-control-connection",
	}
	worker := humancalling.New(db.As(testdb.Worker), accessModule, provider, callingConfig, clock)
	processCommands := func() {
		t.Helper()
		for {
			processed, err := worker.ProcessNextCommand(context.Background())
			if err != nil {
				t.Fatalf("process provider command: %v", err)
			}
			if !processed {
				return
			}
		}
	}
	if err := worker.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("reconcile calling credentials: %v", err)
	}
	processCommands()
	serviceAuthenticator, err := access.NewServiceAuthenticator(access.ServiceCredential{
		Token: "voice-agent-token",
		Identity: access.ServiceIdentity{
			Subject:       "abita-synthetic",
			PracticeID:    authorization.Practice.ID,
			LocationScope: access.LocationScopeAll,
			Capabilities: []access.ServiceCapability{
				access.ServiceCapabilityCreateTask,
				access.ServiceCapabilityHumanHandoff,
			},
		},
	})
	if err != nil {
		t.Fatalf("new service authenticator: %v", err)
	}
	portal := db.As(testdb.Portal)
	workModule := work.New(portal, accessModule, clock)
	handler, err := httpapi.NewPortal(
		httpapi.Config{
			AllowedOrigins: []string{"http://localhost:3000"},
			AcquireTimeout: 500 * time.Millisecond,
		},
		portal,
		httpapi.PortalDependencies{
			Access:               accessModule,
			Authenticator:        staticAuthenticator{"staff-token": identity},
			Calling:              humancalling.New(portal, accessModule, provider, callingConfig, clock),
			Interactions:         interaction.New(portal, accessModule, clock),
			Messaging:            messaging.New(portal, accessModule, workModule, nil, messaging.Config{}, nil),
			Work:                 workModule,
			Workspace:            workspace.New(portal, accessModule),
			ServiceAuthenticator: serviceAuthenticator,
		},
	)
	if err != nil {
		t.Fatalf("new missed handoff HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	send := func(method, path, token string, payload map[string]any, wantStatus int, target any) {
		t.Helper()
		var body []byte
		if payload != nil {
			body, _ = json.Marshal(payload)
		}
		response := request(t, server.Client(), method, server.URL+path, token, body)
		if response.StatusCode != wantStatus {
			t.Fatalf("%s %s = %d, body = %s; want %d",
				method, path, response.StatusCode, readBody(t, response), wantStatus)
		}
		if target == nil {
			_ = response.Body.Close()
			return
		}
		decode(t, response, target)
	}
	applyFact := func(fact humancalling.ProviderFact) {
		t.Helper()
		fact.CallControlID = "http-caller-control"
		fact.CallLegID = "http-caller-leg"
		fact.CallSessionID = "http-call-session"
		if err := worker.ApplyProviderFact(context.Background(), fact); err != nil {
			t.Fatalf("apply %s: %v", fact.EventID, err)
		}
		processCommands()
	}

	callerPhone := "+15555550100"
	var receipt api.StaffTaskReceipt
	send(http.MethodPost, "/v1/tasks", "voice-agent-token", map[string]any{
		"callId":         taskCallID,
		"callerPhone":    callerPhone,
		"category":       "medication",
		"idempotencyKey": "task-" + taskCallID,
		"message":        "Caller asked the office to refill eye drops.",
		"officeKey":      "calling-office",
		"officePhone":    "+17275919997",
		"source":         "agent",
		"summary":        "Caller needs a refill.",
		"urgency":        "normal",
	}, http.StatusCreated, &receipt)
	var aiTaskBeforeMiss api.Task
	send(http.MethodGet, "/v1/tasks/"+receipt.TaskId.String(), "staff-token", nil,
		http.StatusOK, &aiTaskBeforeMiss)
	send(http.MethodPost, "/v1/handoffs", "voice-agent-token", map[string]any{
		"practiceId":     authorization.Practice.ID,
		"officeKey":      "calling-office",
		"sourceCallId":   handoffCallID,
		"idempotencyKey": "handoff-" + handoffCallID,
		"contact":        map[string]any{"phone": callerPhone},
	}, http.StatusCreated, nil)

	applyFact(humancalling.ProviderFact{
		EventID: "caller-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: callingConfig.CallControlID,
		From: callerPhone, To: "+14843989071",
	})
	send(http.MethodPost, "/v1/calling/softphone/lease", "staff-token", map[string]any{
		"sessionId": "http-browser",
		"takeover":  false,
	}, http.StatusOK, nil)
	send(http.MethodPut, "/v1/calling/readiness", "staff-token", map[string]any{
		"sessionId":       "http-browser",
		"registered":      true,
		"microphoneReady": true,
		"audioReady":      true,
		"sessionHealthy":  true,
		"available":       true,
	}, http.StatusOK, nil)
	processCommands()
	applyFact(humancalling.ProviderFact{
		EventID: "caller-answered", Type: humancalling.FactCallAnswered,
		OccurredAt: now.Add(time.Second),
	})
	var calling struct {
		Ringing []struct {
			CallID string `json:"callId"`
		} `json:"ringing"`
	}
	send(http.MethodGet, "/v1/calling/state", "staff-token", nil, http.StatusOK, &calling)
	if len(calling.Ringing) != 1 {
		t.Fatalf("staff see %d ringing calls; want the handoff ringing", len(calling.Ringing))
	}
	applyFact(humancalling.ProviderFact{
		EventID: "ring-window-ended", Type: humancalling.FactPlaybackEnded,
		OccurredAt:     now.Add(20 * time.Second),
		ClientState:    provider.clientState(t, humancalling.CommandStartRingWindow),
		PlaybackStatus: "completed",
	})
	applyFact(humancalling.ProviderFact{
		EventID: "greeting-ended-by-hangup", Type: humancalling.FactSpeakEnded,
		OccurredAt:     now.Add(21 * time.Second),
		ClientState:    provider.clientState(t, humancalling.CommandSpeakVoicemail),
		PlaybackStatus: "call_hangup",
	})

	tasksIn := func(state api.TaskQueryRequestState) []api.Task {
		t.Helper()
		var page api.TaskPage
		send(http.MethodPost, "/v1/tasks/query", "staff-token", map[string]any{
			"practiceId": authorization.Practice.ID,
			"state":      state,
		}, http.StatusOK, &page)
		tasks := make([]api.Task, len(page.Items))
		for index, item := range page.Items {
			send(http.MethodGet, "/v1/tasks/"+item.Id.String(), "staff-token", nil,
				http.StatusOK, &tasks[index])
		}
		return tasks
	}
	return missedHandoffHTTPOutcome{
		aiTaskID:                receipt.TaskId,
		aiTaskVersionBeforeMiss: aiTaskBeforeMiss.Version,
		missedCallID:            calling.Ringing[0].CallID,
		open:                    tasksIn(api.TaskQueryRequestStateOPEN),
		completed:               tasksIn(api.TaskQueryRequestStateCOMPLETED),
	}
}

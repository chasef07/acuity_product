package realtime_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/authn"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/realtime"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestRealtimeStreamsDisposablePostgresHintsForAuthorizedScope(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject:       "founder-subject",
		Email:         "founder@acuity.test",
		EmailVerified: true,
	}
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "slice-1-realtime-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key:       "abita-eye-group",
			Name:      "Abita Eye Group",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture 1"}},
			Invitations: []access.InvitationProvision{{
				Key:           "fixture-member",
				Email:         "member@abita.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
				ExpiresAt:     now.Add(time.Hour),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision realtime fixture: %v", err)
	}
	discovery, err := accessModule.DiscoverActor(context.Background(), operator)
	if err != nil {
		t.Fatalf("discover realtime scope: %v", err)
	}
	practice := discovery.Practices[0]
	location := practice.Locations[0]
	member := access.Identity{
		Subject:       "member-subject",
		Email:         "member@abita.test",
		EmailVerified: true,
	}
	memberAuthorization, err := accessModule.AcceptInvitation(
		context.Background(),
		member,
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("accept realtime member invitation: %v", err)
	}
	support, err := accessModule.EnterSupportMode(context.Background(), access.EnterSupportModeCommand{
		Identity:   operator,
		PracticeID: practice.ID,
		Reason:     "Exercise the realtime version hint",
		Duration:   time.Hour,
	})
	if err != nil {
		t.Fatalf("enter Support Mode: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	streams, err := realtime.New(realtime.Config{
		DatabaseURL:        testDatabaseURL(t),
		AccessTimeout:      500 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		StreamLifetime:     2 * time.Second,
		RevalidateInterval: 100 * time.Millisecond,
		ReconnectMin:       10 * time.Millisecond,
		ReconnectMax:       50 * time.Millisecond,
	}, accessModule)
	if err != nil {
		t.Fatalf("new realtime adapter: %v", err)
	}
	go streams.Run(ctx)

	handler, err := httpapi.NewRealtime(httpapi.Config{
		AllowedOrigin:  "http://localhost:3000",
		AcquireTimeout: 500 * time.Millisecond,
	}, pool, httpapi.RealtimeDependencies{
		Access: accessModule,
		Authenticator: staticAuthenticator{
			"operator-token": operator,
			"member-token":   member,
		},
		Events: streams,
	})
	if err != nil {
		t.Fatalf("new realtime HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	waitForReady(t, server.Client(), server.URL+"/health/ready")

	streamContext, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	streamURL := server.URL + "/v1/events?" + url.Values{
		"practiceId": {practice.ID},
		"locationId": {location.ID},
	}.Encode()
	request, err := http.NewRequestWithContext(streamContext, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer operator-token")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("stream status = %d, body = %s", response.StatusCode, body)
	}
	reader := bufio.NewReader(response.Body)
	ready := readSSEEvent(t, reader)
	if ready.Event != "ready" || ready.Data.PracticeID != practice.ID {
		t.Fatalf("ready event = %#v", ready)
	}

	mutation, err := accessModule.AddLocation(context.Background(), access.AddLocationCommand{
		Identity:         operator,
		PracticeID:       practice.ID,
		SupportSessionID: support.ID,
		Key:              "fixture-2",
		Name:             "Fixture 2",
	})
	if err != nil {
		t.Fatalf("publish Access mutation: %v", err)
	}
	hint := readSSEEvent(t, reader)
	if hint.Event != "hint" ||
		hint.Data.PracticeID != practice.ID ||
		hint.Data.Version != mutation.PracticeVersion {
		t.Fatalf("hint event = %#v", hint)
	}

	memberRequest, err := http.NewRequest(http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	memberRequest.Header.Set("Authorization", "Bearer member-token")
	memberResponse, err := server.Client().Do(memberRequest)
	if err != nil {
		t.Fatalf("open member SSE stream: %v", err)
	}
	defer memberResponse.Body.Close()
	memberReader := bufio.NewReader(memberResponse.Body)
	if event := readSSEEvent(t, memberReader); event.Event != "ready" {
		t.Fatalf("member ready event = %#v", event)
	}
	calling := humancalling.New(
		pool,
		accessModule,
		realtimeCallingProvider{},
		humancalling.Config{
			SIPDomain:       "synthetic.sip.telnyx.com",
			HandoffTokenKey: []byte("0123456789abcdef0123456789abcdef"),
		},
		func() time.Time { return now },
	)
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-realtime-test",
			PracticeID: practice.ID,
		},
		LocationID:     location.ID,
		SourceCallID:   "realtime-call-source",
		IdempotencyKey: "realtime-call-idempotency",
	})
	if err != nil {
		t.Fatalf("create realtime Call handoff: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "realtime-call-initiated",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "realtime-caller-control",
		CallLegID:     "realtime-caller-leg",
		CallSessionID: "realtime-call-session",
		To:            handoff.SIPDestination,
	}); err != nil {
		t.Fatalf("publish HumanCalling mutation: %v", err)
	}
	callHint := readSSEEvent(t, memberReader)
	if callHint.Event != "hint" ||
		callHint.Data.PracticeID != practice.ID ||
		callHint.Data.Version <= mutation.PracticeVersion {
		t.Fatalf("HumanCalling hint event = %#v", callHint)
	}
	if err := accessModule.RevokeMembership(
		context.Background(),
		access.RevokeMembershipCommand{
			Identity:         operator,
			PracticeID:       practice.ID,
			SupportSessionID: support.ID,
			MembershipID:     memberAuthorization.Membership.ID,
		},
	); err != nil {
		t.Fatalf("revoke streamed Membership: %v", err)
	}
	closed := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, memberReader)
		closed <- err
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("read revoked member stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("revoked Membership stream exceeded the revalidation bound")
	}

	deniedURL := server.URL + "/v1/events?" + url.Values{
		"practiceId": {practice.ID},
		"locationId": {"00000000-0000-0000-0000-000000000001"},
	}.Encode()
	deniedRequest, _ := http.NewRequest(http.MethodGet, deniedURL, nil)
	deniedRequest.Header.Set("Authorization", "Bearer operator-token")
	denied, err := server.Client().Do(deniedRequest)
	if err != nil {
		t.Fatalf("open unauthorized SSE stream: %v", err)
	}
	defer denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(denied.Body)
		t.Fatalf("unauthorized stream status = %d, body = %s", denied.StatusCode, body)
	}
}

func waitForReady(t *testing.T, client *http.Client, target string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(target)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("realtime readiness did not become observable")
}

type staticAuthenticator map[string]access.Identity

type realtimeCallingProvider struct{}

func (realtimeCallingProvider) Execute(
	context.Context,
	humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	return humancalling.ProviderResult{}, nil
}

func (adapter staticAuthenticator) Authenticate(_ context.Context, token string) (access.Identity, error) {
	identity, ok := adapter[token]
	if !ok {
		return access.Identity{}, authn.ErrInvalidCredential
	}
	return identity, nil
}

type sseEvent struct {
	Event string
	Data  struct {
		PracticeID string `json:"practiceId"`
		Version    int64  `json:"version"`
	}
}

func readSSEEvent(t *testing.T, reader *bufio.Reader) sseEvent {
	t.Helper()
	event := sseEvent{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "event: "):
			event.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event.Data); err != nil {
				t.Fatalf("decode SSE data: %v", err)
			}
		case line == "" && event.Event != "":
			return event
		}
	}
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	value := "postgres://127.0.0.1:55424/acuity_test?sslmode=disable"
	if configured := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")); configured != "" {
		value = configured
	}
	return value
}

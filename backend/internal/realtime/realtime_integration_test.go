package realtime_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/authn"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/realtime"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
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
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		},
		func() time.Time { return now },
	)
	_, err = calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-realtime-test",
			PracticeID: practice.ID,
		},
		LocationID:     location.ID,
		SourceCallID:   "realtime-call-source",
		IdempotencyKey: "realtime-call-idempotency",
		Contact: humancalling.ContactContext{
			Phone: "+15555550100",
		},
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
		From:          "+15555550100",
		To:            "+14843336938",
	}); err != nil {
		t.Fatalf("publish HumanCalling mutation: %v", err)
	}
	callHint := readSSEEvent(t, memberReader)
	if callHint.Event != "hint" ||
		callHint.Data.PracticeID != practice.ID ||
		callHint.Data.Version <= mutation.PracticeVersion {
		t.Fatalf("HumanCalling hint event = %#v", callHint)
	}
	var callID string
	if err := pool.QueryRow(context.Background(), `
		UPDATE human_calling_calls
		SET state = 'FOLLOW_UP_REQUIRED'
		WHERE call_session_id = 'realtime-call-session'
		RETURNING id::text
	`).Scan(&callID); err != nil {
		t.Fatalf("prepare realtime Task Call: %v", err)
	}
	workModule := work.New(pool, accessModule, func() time.Time { return now })
	taskTx, err := pool.BeginTx(context.Background(), pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin realtime Task creation: %v", err)
	}
	task, err := workModule.EnsureCallFollowUp(
		context.Background(),
		taskTx,
		work.EnsureCallFollowUpCommand{
			CallID:     callID,
			PracticeID: practice.ID,
			LocationID: location.ID,
			Phone:      "+15555550100",
			Reason:     "Realtime follow-up",
			Creator:    memberAuthorization.Actor,
		},
	)
	if err != nil {
		_ = taskTx.Rollback(context.Background())
		t.Fatalf("create realtime Task: %v", err)
	}
	if err := taskTx.Commit(context.Background()); err != nil {
		t.Fatalf("commit realtime Task: %v", err)
	}
	taskHint := readSSEEvent(t, memberReader)
	if taskHint.Event != "hint" ||
		taskHint.Data.PracticeID != practice.ID ||
		taskHint.Data.Version <= callHint.Data.Version {
		t.Fatalf("Task creation hint = %#v", taskHint)
	}
	renamed, err := workModule.RenameTask(
		context.Background(),
		work.RenameTaskCommand{
			Identity:        member,
			TaskID:          task.ID,
			ExpectedVersion: task.Version,
			Title:           "Renamed realtime follow-up",
		},
	)
	if err != nil {
		t.Fatalf("rename realtime Task: %v", err)
	}
	renameHint := readSSEEvent(t, memberReader)
	if renameHint.Event != "hint" ||
		renameHint.Data.PracticeID != practice.ID ||
		renameHint.Data.Version <= taskHint.Data.Version ||
		renamed.Version != task.Version+1 {
		t.Fatalf("Task rename hint = %#v, Task = %#v", renameHint, renamed)
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
	if _, err := workModule.ReadTask(
		context.Background(),
		member,
		task.ID,
	); !errors.Is(err, work.ErrDenied) {
		t.Fatalf("revoked member Task read error = %v, want denied", err)
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

func TestRealtimeBurstPreservesNewestWorkspaceVersion(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeRealtime,
		"realtime-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "realtime-burst-test",
		Practices: []access.PracticeProvision{{
			Key:       "realtime-burst-practice",
			Name:      "Realtime Burst Practice",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture 1"}},
			Invitations: []access.InvitationProvision{{
				Key:           "realtime-burst-member",
				Email:         "member@realtime-burst.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
				ExpiresAt:     now.Add(time.Hour),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision realtime burst fixture: %v", err)
	}
	member := access.Identity{
		Subject:       "realtime-burst-member",
		Email:         "member@realtime-burst.test",
		EmailVerified: true,
	}
	authorization, err := accessModule.AcceptInvitation(
		context.Background(),
		member,
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("accept realtime burst invitation: %v", err)
	}

	hubContext, stopHub := context.WithCancel(context.Background())
	t.Cleanup(stopHub)
	hub, err := realtime.New(realtime.Config{
		DatabaseURL:        testDatabaseURL(t),
		AccessTimeout:      500 * time.Millisecond,
		HeartbeatInterval:  time.Second,
		StreamLifetime:     3 * time.Second,
		RevalidateInterval: time.Second,
		ReconnectMin:       10 * time.Millisecond,
		ReconnectMax:       50 * time.Millisecond,
		Observer:           observer,
	}, accessModule)
	if err != nil {
		t.Fatalf("new realtime burst adapter: %v", err)
	}
	go hub.Run(hubContext)
	deadline := time.Now().Add(time.Second)
	for !hub.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("realtime burst adapter did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	writer := newGatedSSEWriter()
	streamContext, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	request := httptest.NewRequest(http.MethodGet, "/v1/events", nil).
		WithContext(streamContext)
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- hub.Stream(
			writer,
			request,
			member,
			authorization.Practice.ID,
			authorization.Locations[0].ID,
		)
	}()
	select {
	case <-writer.ready:
	case <-time.After(time.Second):
		t.Fatal("realtime burst stream did not become ready")
	}

	publishChange := func() int64 {
		t.Helper()
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin realtime burst change: %v", err)
		}
		version, err := accessModule.RecordWorkspaceChange(
			context.Background(),
			tx,
			authorization.Practice.ID,
		)
		if err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatalf("record realtime burst change: %v", err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("commit realtime burst change: %v", err)
		}
		return version
	}
	publishChange()
	select {
	case <-writer.blocked:
	case <-time.After(time.Second):
		t.Fatal("realtime burst stream did not block on its first hint")
	}
	var newestVersion int64
	for range 16 {
		newestVersion = publishChange()
	}
	time.Sleep(250 * time.Millisecond)
	close(writer.release)

	deadline = time.Now().Add(time.Second)
	for writer.maxHintVersion() < newestVersion && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stopStream()
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatalf("realtime burst stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("realtime burst stream did not stop")
	}
	if got := writer.maxHintVersion(); got != newestVersion {
		t.Fatalf("newest realtime burst version = %d, want %d", got, newestVersion)
	}
	for _, fragment := range []string{
		`"metric":"acuity_call_center_sse_stream"`,
		`"active":0`,
		`"state":"closed"`,
		`"reason":"client"`,
		`"metric":"acuity_call_center_sse_listener"`,
	} {
		if !strings.Contains(metrics.String(), fragment) {
			t.Fatalf("realtime metrics omitted %s: %s", fragment, metrics.String())
		}
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

type gatedSSEWriter struct {
	header  http.Header
	ready   chan struct{}
	blocked chan struct{}
	release chan struct{}

	mu          sync.Mutex
	readyOnce   sync.Once
	blockedOnce sync.Once
	writes      []string
}

func newGatedSSEWriter() *gatedSSEWriter {
	return &gatedSSEWriter{
		header:  http.Header{},
		ready:   make(chan struct{}),
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (writer *gatedSSEWriter) Header() http.Header {
	return writer.header
}

func (writer *gatedSSEWriter) WriteHeader(int) {}

func (writer *gatedSSEWriter) Write(body []byte) (int, error) {
	event := string(body)
	if strings.Contains(event, "event: ready") {
		writer.readyOnce.Do(func() { close(writer.ready) })
	}
	if strings.Contains(event, "event: hint") {
		block := false
		writer.blockedOnce.Do(func() {
			close(writer.blocked)
			block = true
		})
		if block {
			<-writer.release
		}
	}
	writer.mu.Lock()
	writer.writes = append(writer.writes, event)
	writer.mu.Unlock()
	return len(body), nil
}

func (writer *gatedSSEWriter) Flush() {}

func (writer *gatedSSEWriter) maxHintVersion() int64 {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	var maximum int64
	for _, event := range writer.writes {
		if !strings.Contains(event, "event: hint") {
			continue
		}
		parts := strings.SplitN(event, "data: ", 2)
		if len(parts) != 2 {
			continue
		}
		var hint realtime.Hint
		data := strings.TrimSpace(parts[1])
		if err := json.Unmarshal([]byte(data), &hint); err == nil && hint.Version > maximum {
			maximum = hint.Version
		}
	}
	return maximum
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

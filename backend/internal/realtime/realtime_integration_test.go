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
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/realtime"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "slice-1-realtime-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key:       "abita-eye-group",
			Name:      "Abita Eye Group",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture 1"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "fixture-member",
				Email:         "member@abita.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
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
	memberAuthorization := testaccess.Activate(t, accessModule, member)
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
		AllowedOrigins: []string{"http://localhost:3000"},
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
		Identity:   operator,
		PracticeID: practice.ID,
		Key:        "fixture-2",
		Name:       "Fixture 2",
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
			CallControlID:    "realtime-call-control-connection",
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
		ConnectionID:  "realtime-call-control-connection",
		CallControlID: "realtime-caller-control",
		CallLegID:     "realtime-caller-leg",
		CallSessionID: "realtime-call-session",
		From:          "+15555550100",
		To:            "+14843989071",
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
		SET terminal_outcome = 'FOLLOW_UP_REQUIRED',
			disposition_outcome = 'FOLLOW_UP_REQUIRED',
			ended_at = $1, updated_at = $1
		WHERE id = (
			SELECT call_id FROM human_calling_call_legs
			WHERE provider_call_session_id = 'realtime-call-session'
			LIMIT 1
		)
		RETURNING id::text
	`, now).Scan(&callID); err != nil {
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
			Identity:     operator,
			PracticeID:   practice.ID,
			MembershipID: memberAuthorization.Membership.ID,
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

func TestRealtimeReconnectAndRevalidationBurstStaysAvailableDuringUnrelatedOperatorBinding(t *testing.T) {
	ownerPool := testdb.Open(t)
	now := time.Date(2026, time.August, 18, 15, 38, 57, 0, time.UTC)
	_, identity, authorization := provisionRealtimeMember(
		t,
		ownerPool,
		now,
		"realtime-reconnect-contention",
	)

	var metrics synchronizedBuffer
	observer := observability.NewLogger(
		observability.RuntimeRealtime,
		"realtime-contention-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	poolConfig, err := pgxpool.ParseConfig(testDatabaseURL(t))
	if err != nil {
		t.Fatalf("parse realtime production pool: %v", err)
	}
	poolConfig.MaxConns = 1
	poolConfig.MinConns = 0
	runtimePool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatalf("open realtime production pool: %v", err)
	}
	t.Cleanup(runtimePool.Close)
	database, err := productpostgres.NewExecutor(
		runtimePool,
		productpostgres.ExecutorConfig{
			AcquireTimeout:   150 * time.Millisecond,
			OperationTimeout: time.Second,
			StatementTimeout: 500 * time.Millisecond,
		},
		observer,
	)
	if err != nil {
		t.Fatalf("create realtime production executor: %v", err)
	}
	runtimeAccess := access.New(database, func() time.Time { return now })

	hubContext, stopHub := context.WithCancel(context.Background())
	t.Cleanup(stopHub)
	hub, err := realtime.New(realtime.Config{
		DatabaseURL:        testDatabaseURL(t),
		AccessTimeout:      time.Second,
		HeartbeatInterval:  time.Second,
		StreamLifetime:     5 * time.Second,
		RevalidateInterval: 100 * time.Millisecond,
		ReconnectMin:       10 * time.Millisecond,
		ReconnectMax:       50 * time.Millisecond,
		Observer:           observer,
	}, runtimeAccess)
	if err != nil {
		t.Fatalf("new realtime contention adapter: %v", err)
	}
	go hub.Run(hubContext)

	handler, err := httpapi.NewRealtime(httpapi.Config{
		AllowedOrigins: []string{"http://localhost:3000"},
		AcquireTimeout: 150 * time.Millisecond,
		RequestTimeout: time.Second,
		Observer:       observer,
	}, runtimePool, httpapi.RealtimeDependencies{
		Access:        runtimeAccess,
		Authenticator: staticAuthenticator{"member-token": identity},
		Events:        hub,
	})
	if err != nil {
		t.Fatalf("new realtime contention HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	waitForReady(t, server.Client(), server.URL+"/health/ready")
	streamURL := server.URL + "/v1/events?" + url.Values{
		"practiceId": {authorization.Practice.ID},
		"locationId": {authorization.Locations[0].ID},
	}.Encode()

	const establishedStreams = 8
	establishedCancels := make([]context.CancelFunc, 0, establishedStreams)
	establishedBodies := make([]io.ReadCloser, 0, establishedStreams)
	establishedClosed := make(chan struct{}, establishedStreams)
	for range establishedStreams {
		streamContext, cancel := context.WithCancel(context.Background())
		request, requestErr := http.NewRequestWithContext(
			streamContext,
			http.MethodGet,
			streamURL,
			nil,
		)
		if requestErr != nil {
			cancel()
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer member-token")
		response, requestErr := server.Client().Do(request)
		if requestErr != nil {
			cancel()
			t.Fatalf("open established realtime stream: %v", requestErr)
		}
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			cancel()
			t.Fatalf("established realtime stream status = %d, body = %s", response.StatusCode, body)
		}
		if event := readSSEEvent(t, bufio.NewReader(response.Body)); event.Event != "ready" {
			_ = response.Body.Close()
			cancel()
			t.Fatalf("established realtime event = %#v", event)
		}
		establishedCancels = append(establishedCancels, cancel)
		establishedBodies = append(establishedBodies, response.Body)
		go func(body io.Reader) {
			_, _ = io.Copy(io.Discard, body)
			establishedClosed <- struct{}{}
		}(response.Body)
	}
	defer func() {
		for _, cancel := range establishedCancels {
			cancel()
		}
		for _, body := range establishedBodies {
			_ = body.Close()
		}
	}()
	poolIdleDeadline := time.Now().Add(500 * time.Millisecond)
	for runtimePool.Stat().AcquiredConns() != 0 && time.Now().Before(poolIdleDeadline) {
		time.Sleep(time.Millisecond)
	}
	if acquired := runtimePool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("established SSE streams retain %d general-pool connections", acquired)
	}

	operatorBinding, err := ownerPool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin unrelated operator binding: %v", err)
	}
	defer func() { _ = operatorBinding.Rollback(context.Background()) }()
	if _, err := operatorBinding.Exec(context.Background(), `
		SELECT pg_advisory_xact_lock(1094927189, hashtext($1))
	`, identity.Subject); err != nil {
		t.Fatalf("hold unrelated operator subject binding: %v", err)
	}
	if _, err := operatorBinding.Exec(context.Background(), `
		SELECT pg_advisory_xact_lock(1094927188, hashtext($1))
	`, identity.Email); err != nil {
		t.Fatalf("hold unrelated operator email binding: %v", err)
	}

	const reconnects = 42
	statuses := make([]int, reconnects)
	requestErrors := make([]error, reconnects)
	start := make(chan struct{})
	var reconnectGroup sync.WaitGroup
	for index := range reconnects {
		reconnectGroup.Add(1)
		go func(index int) {
			defer reconnectGroup.Done()
			<-start
			requestContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			request, requestErr := http.NewRequestWithContext(
				requestContext,
				http.MethodGet,
				streamURL,
				nil,
			)
			if requestErr != nil {
				requestErrors[index] = requestErr
				return
			}
			request.Header.Set("Authorization", "Bearer member-token")
			response, requestErr := server.Client().Do(request)
			if requestErr != nil {
				requestErrors[index] = requestErr
				return
			}
			statuses[index] = response.StatusCode
			_ = response.Body.Close()
		}(index)
	}
	close(start)

	var blockedQuery string
	blockedDeadline := time.Now().Add(time.Second)
	for blockedQuery == "" && time.Now().Before(blockedDeadline) {
		_ = ownerPool.QueryRow(context.Background(), `
			SELECT query
			FROM pg_stat_activity
			WHERE datname = current_database()
				AND state = 'active'
				AND wait_event_type = 'Lock'
				AND wait_event = 'advisory'
				AND query LIKE '%pg_advisory_xact_lock(1094927189%'
			LIMIT 1
		`).Scan(&blockedQuery)
		if blockedQuery == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	time.Sleep(650 * time.Millisecond)
	if err := operatorBinding.Rollback(context.Background()); err != nil {
		t.Fatalf("release unrelated operator binding: %v", err)
	}
	reconnectGroup.Wait()

	prematureClosures := len(establishedClosed)
	acquireTimeouts := strings.Count(metrics.String(), `"cause":"acquire_timeout"`)
	statementTimeouts := strings.Count(metrics.String(), `"cause":"statement_timeout"`)
	failedStatuses := 0
	for _, status := range statuses {
		if status != http.StatusOK {
			failedStatuses++
		}
	}
	for _, requestErr := range requestErrors {
		if requestErr != nil {
			failedStatuses++
		}
	}
	if failedStatuses != 0 || prematureClosures != 0 || acquireTimeouts != 0 || statementTimeouts != 0 {
		t.Fatalf(
			"realtime reconnect/revalidation burst: failed=%d/%d premature_closures=%d acquire_timeouts=%d statement_timeouts=%d blocked_query=%q statuses=%v errors=%v",
			failedStatuses,
			reconnects,
			prematureClosures,
			acquireTimeouts,
			statementTimeouts,
			strings.Join(strings.Fields(blockedQuery), " "),
			statuses,
			requestErrors,
		)
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
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "realtime-burst-test",
		Practices: []access.PracticeProvision{{
			Key:       "realtime-burst-practice",
			Name:      "Realtime Burst Practice",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture 1"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "realtime-burst-member",
				Email:         "member@realtime-burst.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
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
	authorization := testaccess.Activate(t, accessModule, member)

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

func TestRealtimeListenerDeathClosesStreamsAndRecoveryAcceptsFreshStreams(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "realtime-listener-recovery-test",
		Practices: []access.PracticeProvision{{
			Key:       "realtime-listener-recovery",
			Name:      "Realtime Listener Recovery",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture 1"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "realtime-listener-member",
				Email:         "member@realtime-listener.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision realtime listener fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "realtime-listener-member",
		Email:         "member@realtime-listener.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)

	hubContext, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	hub, err := realtime.New(realtime.Config{
		DatabaseURL:        testDatabaseURL(t),
		AccessTimeout:      500 * time.Millisecond,
		HeartbeatInterval:  time.Second,
		StreamLifetime:     5 * time.Second,
		RevalidateInterval: time.Second,
		ReconnectMin:       10 * time.Millisecond,
		ReconnectMax:       50 * time.Millisecond,
	}, accessModule)
	if err != nil {
		t.Fatalf("new realtime listener adapter: %v", err)
	}
	go hub.Run(hubContext)
	waitForHubReady(t, hub)

	firstWriter := newGatedSSEWriter()
	firstRequest := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- hub.Stream(
			firstWriter,
			firstRequest,
			identity,
			authorization.Practice.ID,
			authorization.Locations[0].ID,
		)
	}()
	select {
	case <-firstWriter.ready:
	case <-time.After(time.Second):
		t.Fatal("first realtime listener stream did not become ready")
	}

	var terminated bool
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(bool_or(pg_terminate_backend(pid)), false)
		FROM pg_stat_activity
		WHERE datname = current_database()
			AND pid <> pg_backend_pid()
			AND query = 'LISTEN acuity_workspace_hints'
	`).Scan(&terminated); err != nil {
		t.Fatalf("terminate realtime listener: %v", err)
	}
	if !terminated {
		t.Fatal("realtime LISTEN backend was not found")
	}
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("listener-loss stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener loss did not force the existing stream to reconnect")
	}

	waitForHubReady(t, hub)
	secondContext, stopSecond := context.WithCancel(context.Background())
	secondWriter := newGatedSSEWriter()
	secondRequest := httptest.NewRequest(http.MethodGet, "/v1/events", nil).
		WithContext(secondContext)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- hub.Stream(
			secondWriter,
			secondRequest,
			identity,
			authorization.Practice.ID,
			authorization.Locations[0].ID,
		)
	}()
	select {
	case <-secondWriter.ready:
	case <-time.After(time.Second):
		t.Fatal("recovered realtime listener did not accept a fresh stream")
	}
	stopSecond()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("stop recovered realtime stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("recovered realtime stream did not stop")
	}
}

func TestRealtimePlannedRotationIsJitteredAcrossConcurrentClients(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 31, 13, 0, 0, 0, time.UTC)
	accessModule, identity, authorization := provisionRealtimeMember(
		t,
		pool,
		now,
		"realtime-rotation",
	)
	hubContext, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	hub, err := realtime.New(realtime.Config{
		DatabaseURL:          testDatabaseURL(t),
		AccessTimeout:        500 * time.Millisecond,
		HeartbeatInterval:    20 * time.Millisecond,
		StreamLifetime:       200 * time.Millisecond,
		StreamLifetimeJitter: 100 * time.Millisecond,
		RevalidateInterval:   time.Second,
		ReconnectMin:         10 * time.Millisecond,
		ReconnectMax:         50 * time.Millisecond,
	}, accessModule)
	if err != nil {
		t.Fatalf("new realtime rotation adapter: %v", err)
	}
	go hub.Run(hubContext)
	waitForHubReady(t, hub)

	const clients = 32
	writers := make([]*gatedSSEWriter, clients)
	type rotationResult struct {
		err     error
		endedAt time.Time
	}
	done := make([]chan rotationResult, clients)
	for index := range clients {
		writers[index] = newGatedSSEWriter()
		done[index] = make(chan rotationResult, 1)
		request := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
		go func(index int) {
			done[index] <- rotationResult{
				err: hub.Stream(
					writers[index],
					request,
					identity,
					authorization.Practice.ID,
					authorization.Locations[0].ID,
				),
				endedAt: time.Now(),
			}
		}(index)
	}
	for index, writer := range writers {
		select {
		case <-writer.ready:
		case <-time.After(time.Second):
			t.Fatalf("concurrent realtime stream %d did not become ready", index)
		}
	}

	buckets := map[int64]struct{}{}
	for index, result := range done {
		select {
		case rotation := <-result:
			if rotation.err != nil {
				t.Fatalf("planned realtime rotation %d: %v", index, rotation.err)
			}
			elapsed := rotation.endedAt.Sub(writers[index].readyAt)
			if elapsed < 70*time.Millisecond || elapsed > 500*time.Millisecond {
				t.Fatalf("planned realtime rotation %d elapsed %s", index, elapsed)
			}
			buckets[elapsed.Milliseconds()/10] = struct{}{}
		case <-time.After(time.Second):
			t.Fatalf("planned realtime rotation %d did not close", index)
		}
	}
	if len(buckets) < 3 {
		t.Fatalf("planned realtime rotations were not observably jittered: %v", buckets)
	}
}

func provisionRealtimeMember(
	t *testing.T,
	pool *pgxpool.Pool,
	now time.Time,
	key string,
) (*access.Module, access.Identity, access.Authorization) {
	t.Helper()
	accessModule := access.New(pool, func() time.Time { return now })
	email := key + "@realtime.test"
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: key,
		Practices: []access.PracticeProvision{{
			Key:       key,
			Name:      "Realtime Test Practice",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture 1"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           key + "-member",
				Email:         email,
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision %s fixture: %v", key, err)
	}
	identity := access.Identity{
		Subject:       key + "-member",
		Email:         email,
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	return accessModule, identity, authorization
}

func waitForHubReady(t *testing.T, hub *realtime.Hub) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !hub.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("realtime Hub did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
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

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(body []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(body)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

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
	readyAt time.Time
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
		writer.readyOnce.Do(func() {
			writer.readyAt = time.Now()
			close(writer.ready)
		})
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

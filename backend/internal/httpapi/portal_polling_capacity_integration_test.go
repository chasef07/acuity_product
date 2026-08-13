package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMessageThreadBurstUsesFourConnectionsWithoutTimeouts(t *testing.T) {
	ownerPool := testdb.Open(t)
	now := time.Date(2026, time.August, 13, 20, 0, 0, 0, time.UTC)
	ownerAccess := access.New(ownerPool, func() time.Time { return now })
	grants := make([]access.AccessGrantProvision, 20)
	identities := make([]access.Identity, 20)
	authenticator := staticAuthenticator{}
	for index := range grants {
		email := fmt.Sprintf("staff-%02d@message-burst.test", index)
		grants[index] = access.AccessGrantProvision{
			Key:           fmt.Sprintf("staff-%02d", index),
			Email:         email,
			Role:          access.RoleStaff,
			LocationScope: access.LocationScopeAll,
		}
		identities[index] = access.Identity{
			Subject:       fmt.Sprintf("message-burst-staff-%02d", index),
			Email:         email,
			EmailVerified: true,
		}
		authenticator[fmt.Sprintf("message-burst-token-%02d", index)] = identities[index]
	}
	_, err := ownerAccess.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "message-thread-burst-test",
		Practices: []access.PracticeProvision{{
			Key:          "message-thread-burst",
			Name:         "Message Thread Burst",
			Locations:    []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: grants,
		}},
	})
	if err != nil {
		t.Fatalf("provision Message Thread burst fixture: %v", err)
	}
	var authorization access.Authorization
	for _, identity := range identities {
		authorization = testaccess.Activate(t, ownerAccess, identity)
	}
	practiceID := authorization.Practice.ID
	locationID := authorization.Locations[0].ID
	if _, err := ownerPool.Exec(context.Background(), `
		INSERT INTO messaging_threads (
			practice_id, location_id, office_phone, external_phone,
			created_at, updated_at
		)
		SELECT
			$1, $2, '+17275550100',
			'+1' || (2000000000 + thread_number)::text,
			$3::timestamptz + thread_number * interval '1 second',
			$3::timestamptz + thread_number * interval '1 second'
		FROM generate_series(1, 5000) thread_number
	`, practiceID, locationID, now); err != nil {
		t.Fatalf("seed 5,000 Message Threads: %v", err)
	}
	if _, err := ownerPool.Exec(context.Background(), `
		INSERT INTO messaging_messages (
			thread_id, practice_id, location_id, direction, body,
			sender, destination, delivery_state, created_by_subject,
			created_at, updated_at
		)
		SELECT
			thread.id, thread.practice_id, thread.location_id, 'OUTBOUND',
			'Message ' || message_number,
			thread.office_phone, thread.external_phone, 'SENT', 'burst-seed',
			thread.created_at + message_number * interval '1 millisecond',
			thread.created_at + message_number * interval '1 millisecond'
		FROM messaging_threads thread
		CROSS JOIN generate_series(1, 10) message_number
		WHERE thread.practice_id = $1
			AND thread.location_id = $2
	`, practiceID, locationID); err != nil {
		t.Fatalf("seed 50,000 Messages: %v", err)
	}

	holdTracer := newPoolHoldTracer()
	poolConfig, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse Message Thread burst pool: %v", err)
	}
	poolConfig.MaxConns = 4
	poolConfig.MinConns = 0
	poolConfig.ConnConfig.Tracer = holdTracer
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatalf("open Message Thread burst pool: %v", err)
	}
	t.Cleanup(pool.Close)
	database, err := productpostgres.NewExecutor(
		pool,
		productpostgres.ExecutorConfig{
			AcquireTimeout:   1500 * time.Millisecond,
			OperationTimeout: 10 * time.Second,
			StatementTimeout: 5 * time.Second,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("create Message Thread burst executor: %v", err)
	}
	accessModule := access.New(database, func() time.Time { return now })
	workModule := work.New(database, accessModule, func() time.Time { return now })
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		access.ServiceCredential{
			Token: "message-burst-service-token",
			Identity: access.ServiceIdentity{
				Subject:       "message-burst-service",
				PracticeID:    practiceID,
				LocationScope: access.LocationScopeAll,
				Capabilities:  []access.ServiceCapability{access.ServiceCapabilityHumanHandoff},
			},
		},
		access.ServiceCredential{
			Token: "message-burst-interaction-token",
			Identity: access.ServiceIdentity{
				Subject:       "message-burst-interaction",
				PracticeID:    practiceID,
				LocationScope: access.LocationScopeAll,
				Capabilities:  []access.ServiceCapability{access.ServiceCapabilityIngestAIInteraction},
			},
		},
	)
	if err != nil {
		t.Fatalf("create Message Thread burst service authenticator: %v", err)
	}
	handler, err := httpapi.NewPortal(
		httpapi.Config{
			AcquireTimeout:   1500 * time.Millisecond,
			OperationTimeout: 10 * time.Second,
		},
		pool,
		httpapi.PortalDependencies{
			Access:               accessModule,
			Authenticator:        authenticator,
			Calling:              humancalling.New(database, accessModule, httpCallingProvider{}, humancalling.Config{}, nil),
			Interactions:         interaction.New(database, accessModule, nil),
			Messaging:            messaging.New(database, accessModule, workModule, nil, messaging.Config{}, nil),
			Work:                 workModule,
			ServiceAuthenticator: serviceAuthenticator,
		},
	)
	if err != nil {
		t.Fatalf("create Message Thread burst portal: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	limit := 50
	body, err := json.Marshal(api.MessageThreadQueryRequest{
		PracticeId: parsedUUID(t, practiceID),
		LocationId: parsedUUIDPointer(t, locationID),
		Limit:      &limit,
	})
	if err != nil {
		t.Fatalf("encode Message Thread burst query: %v", err)
	}

	type result struct {
		status   int
		duration time.Duration
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, len(identities))
	for index := range identities {
		go func(index int) {
			<-start
			request, err := http.NewRequest(
				http.MethodPost,
				server.URL+"/v1/message-threads/query",
				bytes.NewReader(body),
			)
			if err != nil {
				results <- result{err: err}
				return
			}
			request.Header.Set("Authorization", fmt.Sprintf("Bearer message-burst-token-%02d", index))
			request.Header.Set("Content-Type", "application/json")
			started := time.Now()
			response, err := server.Client().Do(request)
			if err != nil {
				results <- result{duration: time.Since(started), err: err}
				return
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if readErr != nil {
				err = readErr
			} else if closeErr != nil {
				err = closeErr
			}
			results <- result{
				status:   response.StatusCode,
				duration: time.Since(started),
				err:      err,
			}
		}(index)
	}
	close(start)
	var maximumRequest time.Duration
	for range identities {
		result := <-results
		if result.err != nil {
			t.Errorf("Message Thread burst request failed: %v", result.err)
			continue
		}
		if result.status != http.StatusOK {
			t.Errorf("Message Thread burst status = %d, want 200", result.status)
		}
		maximumRequest = max(maximumRequest, result.duration)
	}
	maximumHold := holdTracer.MaximumHold()
	if maximumHold >= 1500*time.Millisecond {
		t.Errorf("maximum Message Thread connection hold = %s, want below acquisition timeout", maximumHold)
	}
	t.Logf(
		"20 authenticated requests over 5,000 Threads/50,000 Messages: max request %s, max connection hold %s",
		maximumRequest,
		maximumHold,
	)
}

type poolHoldTracer struct {
	mu       sync.Mutex
	acquired map[*pgx.Conn]time.Time
	maximum  time.Duration
}

func newPoolHoldTracer() *poolHoldTracer {
	return &poolHoldTracer{acquired: map[*pgx.Conn]time.Time{}}
}

func (*poolHoldTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	_ pgx.TraceQueryStartData,
) context.Context {
	return ctx
}

func (*poolHoldTracer) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

func (*poolHoldTracer) TraceAcquireStart(
	ctx context.Context,
	_ *pgxpool.Pool,
	_ pgxpool.TraceAcquireStartData,
) context.Context {
	return ctx
}

func (tracer *poolHoldTracer) TraceAcquireEnd(
	_ context.Context,
	_ *pgxpool.Pool,
	data pgxpool.TraceAcquireEndData,
) {
	if data.Err != nil || data.Conn == nil {
		return
	}
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	tracer.acquired[data.Conn] = time.Now()
}

func (tracer *poolHoldTracer) TraceRelease(
	_ *pgxpool.Pool,
	data pgxpool.TraceReleaseData,
) {
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	started, ok := tracer.acquired[data.Conn]
	if !ok {
		return
	}
	delete(tracer.acquired, data.Conn)
	tracer.maximum = max(tracer.maximum, time.Since(started))
}

func (tracer *poolHoldTracer) MaximumHold() time.Duration {
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	return tracer.maximum
}

func TestCallingPollsKeepServingDuringParallelPortalRefresh(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	var twoConnections []int
	t.Run("two connections", func(t *testing.T) {
		twoConnections = runCallingPollBurst(t, 2)
	})
	if twoConnections[0] == http.StatusOK || twoConnections[1] == http.StatusOK {
		t.Fatalf("two-connection portal unexpectedly served busy burst: %v", twoConnections)
	}

	var fourConnections []int
	t.Run("four connections", func(t *testing.T) {
		fourConnections = runCallingPollBurst(t, 4)
	})
	if fourConnections[0] != http.StatusOK || fourConnections[1] != http.StatusOK {
		t.Fatalf("four-connection portal burst statuses = %v, want two 200s", fourConnections)
	}
}

func runCallingPollBurst(t *testing.T, poolMaximum int32) []int {
	t.Helper()
	ownerPool := testdb.Open(t)
	now := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	ownerAccess := access.New(ownerPool, func() time.Time { return now })
	_, err := ownerAccess.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "portal-polling-capacity-test",
		Practices: []access.PracticeProvision{{
			Key:       "portal-polling",
			Name:      "Portal Polling",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "staff",
				Email:         "staff@portal-polling.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision portal polling fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "portal-polling-staff",
		Email:         "staff@portal-polling.test",
		EmailVerified: true,
	}
	testaccess.Activate(t, ownerAccess, identity)
	calling := humancalling.New(
		ownerPool,
		ownerAccess,
		httpCallingProvider{},
		humancalling.Config{CallControlID: "portal-polling-control"},
		func() time.Time { return now },
	)
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("reconcile portal polling credential: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("create portal polling credential: processed=%t err=%v", processed, err)
	}

	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse limited portal pool: %v", err)
	}
	config.MaxConns = poolMaximum
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open limited portal pool: %v", err)
	}
	t.Cleanup(pool.Close)
	accessModule := access.New(pool, func() time.Time { return now })
	handler, err := newPortalHandler(
		t,
		httpapi.Config{AcquireTimeout: 150 * time.Millisecond},
		pool,
		accessModule,
		staticAuthenticator{"polling-token": identity},
	)
	if err != nil {
		t.Fatalf("create limited portal: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	held := make([]*pgxpool.Conn, 0, 2)
	for range 2 {
		connection, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatalf("hold portal connection: %v", err)
		}
		held = append(held, connection)
	}
	requests := []*http.Request{
		pollingRequest(t, http.MethodGet, server.URL+"/v1/calling/state", nil),
		pollingRequest(t, http.MethodPost, server.URL+"/v1/calling/softphone/lease",
			[]byte(`{"sessionId":"polling-session","takeover":false}`)),
	}
	statuses := make(chan int, len(requests))
	for _, request := range requests {
		go func(request *http.Request) {
			response, err := server.Client().Do(request)
			if err != nil {
				statuses <- 0
				return
			}
			_ = response.Body.Close()
			statuses <- response.StatusCode
		}(request)
	}
	result := []int{<-statuses, <-statuses}
	for _, connection := range held {
		connection.Release()
	}
	sort.Ints(result)
	return result
}

func pollingRequest(t *testing.T, method string, target string, body []byte) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create polling request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer polling-token")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

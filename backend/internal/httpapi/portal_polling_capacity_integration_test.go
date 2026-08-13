package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
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
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMessageThreadQueryAggregatesActivityBeforeRanking(t *testing.T) {
	ownerPool := testdb.Open(t)
	now := time.Date(2026, time.August, 13, 20, 0, 0, 0, time.UTC)
	ownerAccess := access.New(ownerPool, func() time.Time { return now })
	identity := access.Identity{
		Subject:       "message-query-staff",
		Email:         "staff@message-query.test",
		EmailVerified: true,
	}
	_, err := ownerAccess.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "message-thread-query-test",
		Practices: []access.PracticeProvision{{
			Key:       "message-thread-query",
			Name:      "Message Thread Query",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "staff",
				Email:         identity.Email,
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Message Thread query fixture: %v", err)
	}
	authorization := testaccess.Activate(t, ownerAccess, identity)
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
		FROM generate_series(1, 2000) thread_number
	`, practiceID, locationID, now); err != nil {
		t.Fatalf("seed 2,000 Message Threads: %v", err)
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
		t.Fatalf("seed 20,000 Messages: %v", err)
	}
	if _, err := ownerPool.Exec(context.Background(), `
		INSERT INTO human_calling_handoffs (
			service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, phone, phone_source,
			expires_at, created_at
		)
		SELECT
			'message-query', thread.practice_id, thread.location_id,
			'message-query-' || thread.id::text,
			'message-query-' || thread.id::text,
			decode(repeat('01', 32), 'hex'), thread.external_phone, 'fixture',
			$3::timestamptz + interval '1 hour',
			thread.created_at + interval '20 milliseconds'
		FROM messaging_threads thread
		WHERE thread.practice_id = $1 AND thread.location_id = $2
	`, practiceID, locationID, now); err != nil {
		t.Fatalf("seed 2,000 handoffs: %v", err)
	}
	if _, err := ownerPool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			source_handoff_id, practice_id, location_id, caller_phone,
			terminal_outcome, ended_at, created_at, updated_at
		)
		SELECT
			handoff.id, handoff.practice_id, handoff.location_id, handoff.phone,
			'RESOLVED', handoff.created_at + interval '10 seconds',
			handoff.created_at, handoff.created_at
		FROM human_calling_handoffs handoff
		WHERE handoff.service_subject = 'message-query'
			AND handoff.practice_id = $1 AND handoff.location_id = $2
	`, practiceID, locationID); err != nil {
		t.Fatalf("seed 2,000 Calls: %v", err)
	}
	if _, err := ownerPool.Exec(context.Background(), `
		WITH candidate AS (
			SELECT thread.*, row_number() OVER (ORDER BY thread.id) AS position
			FROM messaging_threads thread
			WHERE thread.practice_id = $1 AND thread.location_id = $2
		), latest AS (
			SELECT candidate.id AS thread_id, message.id AS message_id
			FROM candidate
			JOIN LATERAL (
				SELECT message.id
				FROM messaging_messages message
				WHERE message.thread_id = candidate.id
				ORDER BY message.created_at DESC, message.id DESC
				LIMIT 1
			) message ON true
			WHERE candidate.position % 2 = 0
		)
		INSERT INTO work_tasks (
			practice_id, location_id, phone, title, state,
			created_by_subject, created_by_email, created_at, updated_at,
			origin, source_message_id, message_thread_id
		)
		SELECT
			thread.practice_id, thread.location_id, thread.external_phone,
			'Follow up on Message', 'OPEN', 'message-query-staff',
			'staff@message-query.test', thread.created_at + interval '30 milliseconds',
			thread.created_at + interval '30 milliseconds',
			'STAFF_MESSAGE_FOLLOW_UP', latest.message_id, thread.id
		FROM latest
		JOIN messaging_threads thread ON thread.id = latest.thread_id
	`, practiceID, locationID); err != nil {
		t.Fatalf("seed 1,000 Message-linked Tasks: %v", err)
	}
	if _, err := ownerPool.Exec(context.Background(), `
		WITH candidate AS (
			SELECT
				call.id, call.practice_id, call.location_id, handoff.phone,
				call.created_at,
				row_number() OVER (ORDER BY call.id) AS position
			FROM human_calling_calls call
			JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
			WHERE handoff.service_subject = 'message-query'
				AND call.practice_id = $1 AND call.location_id = $2
		)
		INSERT INTO work_tasks (
			practice_id, location_id, call_id, phone, title, state,
			created_by_subject, created_by_email, created_at, updated_at
		)
		SELECT
			practice_id, location_id, id, phone, 'Follow up on Call', 'OPEN',
			'message-query-staff', 'staff@message-query.test',
			created_at + interval '40 milliseconds',
			created_at + interval '40 milliseconds'
		FROM candidate
		WHERE position % 2 = 1
	`, practiceID, locationID); err != nil {
		t.Fatalf("seed 1,000 phone-matched Tasks: %v", err)
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
			AcquireTimeout: 1500 * time.Millisecond,
			RequestTimeout: 10 * time.Second,
		},
		pool,
		httpapi.PortalDependencies{
			Access:               accessModule,
			Authenticator:        staticAuthenticator{"message-query-token": identity},
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

	request, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/v1/message-threads/query",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("create Message Thread query: %v", err)
	}
	request.Header.Set("Authorization", "Bearer message-query-token")
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("execute Message Thread query: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Message Thread query status = %d, want 200", response.StatusCode)
	}
	requestDuration := time.Since(started)
	query, arguments, ok := holdTracer.MessageThreadQuery()
	if !ok {
		t.Fatal("Message Thread SQL was not captured from the real handler")
	}
	var rawPlan []byte
	if err := ownerPool.QueryRow(
		context.Background(),
		"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query,
		arguments...,
	).Scan(&rawPlan); err != nil {
		t.Fatalf("explain Message Thread query: %v", err)
	}
	var explained []struct {
		Plan          explainPlan `json:"Plan"`
		ExecutionTime float64     `json:"Execution Time"`
		JIT           struct {
			Timing struct {
				Total float64 `json:"Total"`
			} `json:"Timing"`
		} `json:"JIT"`
	}
	if err := json.Unmarshal(rawPlan, &explained); err != nil || len(explained) != 1 {
		t.Fatalf("decode Message Thread plan: count=%d err=%v", len(explained), err)
	}
	callScans := explained[0].Plan.relationLoops("human_calling_calls")
	taskScans := explained[0].Plan.relationLoops("work_tasks")
	if callScans != 1 || taskScans != 2 {
		t.Fatalf(
			"activity relation loops = Calls %.0f, Tasks %.0f; want one Call branch and two Task branches before ranking",
			callScans,
			taskScans,
		)
	}
	if explained[0].Plan.indexLoops("messaging_threads_phone_activity_idx") == 0 {
		t.Fatal("Message Thread phone-activity index was not used")
	}
	t.Logf(
		"authenticated query over 2,000 Threads, 20,000 Messages, 2,000 Calls, and 2,000 Tasks: request %s, connection hold %s, plan %.1f ms/%d shared hits/cost %.0f/JIT %.1f ms",
		requestDuration,
		holdTracer.MaximumHold(),
		explained[0].ExecutionTime,
		explained[0].Plan.SharedHitBlocks,
		explained[0].Plan.TotalCost,
		explained[0].JIT.Timing.Total,
	)
	t.Logf("plan relations: %s", strings.Join(explained[0].Plan.relationSummary(), "; "))
	t.Logf("slow plan nodes: %s", strings.Join(explained[0].Plan.slowNodeSummary(), "; "))
}

type explainPlan struct {
	NodeType        string        `json:"Node Type"`
	RelationName    string        `json:"Relation Name"`
	IndexName       string        `json:"Index Name"`
	ActualLoops     float64       `json:"Actual Loops"`
	ActualRows      float64       `json:"Actual Rows"`
	ActualTotalTime float64       `json:"Actual Total Time"`
	TotalCost       float64       `json:"Total Cost"`
	SharedHitBlocks int64         `json:"Shared Hit Blocks"`
	Plans           []explainPlan `json:"Plans"`
}

func (plan explainPlan) relationLoops(relation string) float64 {
	loops := float64(0)
	if plan.RelationName == relation {
		loops += plan.ActualLoops
	}
	for _, child := range plan.Plans {
		loops += child.relationLoops(relation)
	}
	return loops
}

func (plan explainPlan) indexLoops(index string) float64 {
	loops := float64(0)
	if plan.IndexName == index {
		loops += plan.ActualLoops
	}
	for _, child := range plan.Plans {
		loops += child.indexLoops(index)
	}
	return loops
}

func (plan explainPlan) relationSummary() []string {
	var summary []string
	if plan.RelationName != "" {
		summary = append(summary, fmt.Sprintf(
			"%s %s index=%s loops=%.0f rows=%.0f time=%.1fms hits=%d",
			plan.NodeType,
			plan.RelationName,
			plan.IndexName,
			plan.ActualLoops,
			plan.ActualRows,
			plan.ActualTotalTime,
			plan.SharedHitBlocks,
		))
	}
	for _, child := range plan.Plans {
		summary = append(summary, child.relationSummary()...)
	}
	return summary
}

func (plan explainPlan) slowNodeSummary() []string {
	var summary []string
	if plan.ActualTotalTime >= 1 {
		summary = append(summary, fmt.Sprintf(
			"%s loops=%.0f rows=%.0f time=%.1fms cost=%.0f",
			plan.NodeType,
			plan.ActualLoops,
			plan.ActualRows,
			plan.ActualTotalTime,
			plan.TotalCost,
		))
	}
	for _, child := range plan.Plans {
		summary = append(summary, child.slowNodeSummary()...)
	}
	return summary
}

type poolHoldTracer struct {
	mu       sync.Mutex
	acquired map[*pgx.Conn]time.Time
	maximum  time.Duration
	query    string
	args     []any
}

func newPoolHoldTracer() *poolHoldTracer {
	return &poolHoldTracer{acquired: map[*pgx.Conn]time.Time{}}
}

func (tracer *poolHoldTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	if strings.Contains(data.SQL, "FROM messaging_threads thread") &&
		strings.Contains(data.SQL, "ORDER BY") {
		tracer.mu.Lock()
		if tracer.query == "" {
			tracer.query = data.SQL
			tracer.args = append([]any(nil), data.Args...)
		}
		tracer.mu.Unlock()
	}
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

func (tracer *poolHoldTracer) MessageThreadQuery() (string, []any, bool) {
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	return tracer.query, append([]any(nil), tracer.args...), tracer.query != ""
}

func TestFourConnectionPortalPoolServesTwoCallingRequestsWithTwoConnectionsBusy(t *testing.T) {
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

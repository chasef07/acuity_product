package humancalling_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Keep the inbound answer projection and ringback command intact. Ten ready
// Staff provider requests remain in flight to prove overlap. Timing is logged
// for comparison; deterministic scheduler tests own the no-batch-sleep rule.
func TestInboundStaffDialFanoutProgressesWithTwoDatabaseConnections(t *testing.T) {
	const staffCount = 10
	pool, _, _, _ := prepareInboundFanout(t, time.Now().Add(-2*time.Second), "full-inbound-latency", &recordingProvider{}, staffCount)
	config := pool.Config().Copy()
	config.MaxConns = 2
	runtimePool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtimePool.Close)
	database, err := postgres.NewExecutor(runtimePool, postgres.ExecutorConfig{
		AcquireTimeout: 1500 * time.Millisecond, OperationTimeout: 10 * time.Second, StatementTimeout: 5 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider := &inboundLatencyProvider{blockingDialProvider: blockingDialProvider{
		started: make(chan struct{}, staffCount), release: make(chan struct{}),
	}}
	var releaseDials sync.Once
	calling := humancalling.New(database, access.New(database, time.Now), provider, humancalling.Config{}, time.Now)
	runner, err := worker.New(worker.Config{
		WorkInterval: 250 * time.Millisecond, WorkTimeout: 10 * time.Second,
		CredentialInterval: time.Hour, CredentialTimeout: time.Second,
		HealthInterval: time.Hour, HealthTimeout: time.Second,
		MetricInterval: time.Hour, MetricTimeout: time.Second,
		ReceiptBatchSize: 8, RecoveryAndMessagingBatchSize: 1,
		ProviderCommandBatchSize: 8, CommandWorkers: staffCount,
	}, calling, idleMessagingWork{}, idleInteractionWork{}, runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- runner.Run(ctx) }()
	t.Cleanup(func() {
		releaseDials.Do(func() { close(provider.release) })
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("Runner did not stop")
		}
	})
	var first, last time.Time
	for range staffCount {
		select {
		case <-provider.started:
			last = time.Now()
			if first.IsZero() {
				first = last
			}
		case <-time.After(5 * time.Second):
			t.Fatal("all Staff Dials did not start concurrently")
		}
	}
	t.Logf("full inbound Runner-start-to-all-dispatched=%s, first-to-last-dial=%s", last.Sub(started), last.Sub(first))

	// Durable ingress and recovery must keep moving while all ten provider
	// executors are occupied and the production pool still has only two slots.
	receiptBody := []byte(`{"data":{"record_type":"event","event_type":"call.synthetic_unknown","id":"full-inbound-latency-receipt","occurred_at":"2026-09-04T18:30:00Z","payload":{}}}`)
	if _, err := database.Exec(context.Background(), `
 INSERT INTO human_calling_provider_receipts (
 event_id, event_type, occurred_at, received_at, signature_timestamp, raw_body, next_attempt_at
 ) VALUES ('full-inbound-latency-receipt', 'call.synthetic_unknown', $1, $1, $2, $3, $1)
 `, time.Now(), time.Now().Unix(), receiptBody); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(context.Background(), `
 INSERT INTO work_recovery_reconciliation_queue (practice_id, phone, enqueued_at)
 SELECT practice_id, '+15555550999', $1 FROM human_calling_calls LIMIT 1
 `, time.Now()); err != nil {
		t.Fatal(err)
	}
	mixedDeadline := time.Now().Add(5 * time.Second)
	for {
		var projected bool
		if err := database.QueryRow(context.Background(), `SELECT
 EXISTS (SELECT 1 FROM human_calling_provider_receipts WHERE event_id = 'full-inbound-latency-receipt' AND state = 'UNKNOWN')
 AND NOT EXISTS (SELECT 1 FROM work_recovery_reconciliation_queue WHERE phone = '+15555550999')`).Scan(&projected); err != nil {
			t.Fatal(err)
		}
		if projected {
			break
		}
		if time.Now().After(mixedDeadline) {
			t.Fatal("receipt/recovery work did not advance while Dials were in flight")
		}
		time.Sleep(5 * time.Millisecond)
	}
	releaseDials.Do(func() { close(provider.release) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		var sent, ringback int
		if err := database.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM human_calling_provider_commands WHERE action = 'DIAL_STAFF' AND state = 'SENT'),
 (SELECT count(*) FROM human_calling_provider_commands WHERE action = 'START_RING_WINDOW' AND state = 'SENT')`).Scan(&sent, &ringback); err != nil {
			t.Fatal(err)
		}
		if sent == staffCount && ringback == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable sent: dials=%d ringback=%d", sent, ringback)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if provider.count() != staffCount {
		t.Fatalf("provider Dial effects=%d want %d", provider.count(), staffCount)
	}
}

type inboundLatencyProvider struct {
	blockingDialProvider
}

func (provider *inboundLatencyProvider) Execute(ctx context.Context, command humancalling.ProviderCommand) (humancalling.ProviderResult, error) {
	if command.Action == humancalling.CommandStartRingWindow {
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return humancalling.ProviderResult{}, ctx.Err()
		}
	}
	return provider.blockingDialProvider.Execute(ctx, command)
}

// This is a bounded mixed-workload acceptance scenario, not a production
// capacity claim. Both Calls follow the real inbound domain path. The synthetic
// receipt burst starts at the durable ingress seam; HTTP signing is tested by
// ingress tests. Analytics uses its real authorized module and compact evidence.
func TestMixedInboundCallsReceiptsAndAnalyticsProgressWithBoundedDatabaseCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := testdb.Open(t)
	now := time.Now()
	seedAccess := access.New(pool, func() time.Time { return now })
	seedCalling := humancalling.New(pool, seedAccess, &recordingProvider{}, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com", StaffSIPDomain: "sip.telnyx.com",
		RingWindowDuration: 20 * time.Second, HandoffTokenKey: []byte("0123456789abcdef0123456789abcdef"),
		CallControlID: "staff-call-control-connection", CredentialConnectionID: "staff-credential-connection",
		FromNumber: "+15555550102", RingbackURL: "https://media.synthetic.test/ringback.wav",
	}, func() time.Time { return now })
	var practices []access.Authorization
	var staffGroups [][]access.Identity
	for index := range 2 {
		authorization, staff := provisionConcurrentStaff(t, seedAccess, now, fmt.Sprintf("mixed-%d", index), 5)
		practices = append(practices, authorization)
		staffGroups = append(staffGroups, staff)
	}
	prepareCredentials(t, seedCalling)
	var callers []humancalling.ProviderFact
	for index, authorization := range practices {
		prefix := fmt.Sprintf("mixed-%d", index)
		readyConcurrentStaff(t, seedCalling, staffGroups[index], prefix+"-browser")
		if _, err := seedCalling.CreateHandoff(ctx, humancalling.CreateHandoffCommand{
			Service:    humancalling.ServiceIdentity{Subject: "agent-" + prefix, PracticeID: authorization.Practice.ID},
			LocationID: authorization.Locations[0].ID, SourceCallID: prefix + "-source", IdempotencyKey: prefix + "-handoff",
			Contact: humancalling.ContactContext{Phone: "+15555550100"},
		}); err != nil {
			t.Fatal(err)
		}
		fact := humancalling.ProviderFact{
			EventID: prefix + "-initiated", Type: humancalling.FactCallInitiated, OccurredAt: now,
			ConnectionID: "staff-call-control-connection", CallControlID: prefix + "-control",
			CallLegID: prefix + "-leg", CallSessionID: prefix + "-session", From: "+15555550100", To: "+15555550103",
		}
		if err := seedCalling.ApplyProviderFact(ctx, fact); err != nil {
			t.Fatal(err)
		}
		callers = append(callers, fact)
	}
	processAllCommands(t, seedCalling)
	for index, fact := range callers {
		fact.EventID = fmt.Sprintf("mixed-%d-answered", index)
		fact.Type = humancalling.FactCallAnswered
		if err := seedCalling.ApplyProviderFact(ctx, fact); err != nil {
			t.Fatal(err)
		}
	}
	const receiptCount = 16
	for index := range receiptCount {
		eventID := fmt.Sprintf("mixed-receipt-%d", index)
		raw, _ := json.Marshal(map[string]any{"data": map[string]any{
			"record_type": "event", "event_type": "call.synthetic_unknown", "id": eventID,
			"occurred_at": now.Format(time.RFC3339Nano), "payload": map[string]any{},
		}})
		if _, err := pool.Exec(ctx, `
 INSERT INTO human_calling_provider_receipts (
 event_id,event_type,occurred_at,received_at,signature_timestamp,raw_body,next_attempt_at
 ) VALUES ($1,'call.synthetic_unknown',$2,$2,$3,$4,$2)
 `, eventID, now, now.Unix(), raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES ('mixed-operator@synthetic.test','mixed-operator')`); err != nil {
		t.Fatal(err)
	}
	const analyticsCalls = 300
	transcript, _ := json.Marshal(map[string]any{"items": []any{map[string]any{
		"type": "message", "content": strings.Repeat("Synthetic content. ", 100), "metrics": map[string]any{"e2e_latency": 0.4},
	}}})
	if _, err := pool.Exec(ctx, `
 INSERT INTO ai_interactions (
 service_subject,practice_id,location_id,source_call_id,phone,office_phone,
 started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload
 ) SELECT 'agent',$1,$2,'mixed-ai-'||n,'+15555550101','+15555550102',
 $3::timestamptz-n*interval '1 second',$3,'COMPLETED',3,$4,'{"domainOutcomes":[]}'
 FROM generate_series(1,$5) AS n
 `, practices[0].Practice.ID, practices[0].Locations[0].ID, now, transcript, analyticsCalls); err != nil {
		t.Fatal(err)
	}
	var metrics mixedLatencyLog
	observer := observability.NewLogger(observability.RuntimeWorker, "worker-mixed-test", slog.New(slog.NewJSONHandler(&metrics, nil)))
	newRuntime := func(role observability.RuntimeRole) (*pgxpool.Pool, *postgres.Executor) {
		runtimeObserver := observability.NewLogger(role, "mixed-test", slog.New(slog.NewJSONHandler(&metrics, nil)))
		config := pool.Config().Copy()
		config.MaxConns = 2
		runtimePool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(runtimePool.Close)
		database, err := postgres.NewExecutor(runtimePool, postgres.ExecutorConfig{
			AcquireTimeout: 1500 * time.Millisecond, OperationTimeout: 10 * time.Second, StatementTimeout: 5 * time.Second,
		}, runtimeObserver)
		if err != nil {
			t.Fatal(err)
		}
		return runtimePool, database
	}
	workerPool, workerDatabase := newRuntime(observability.RuntimeWorker)
	_, portalDatabase := newRuntime(observability.RuntimePortalAPI)
	provider := &mixedLatencyProvider{started: make(chan struct{}, 10), release: make(chan struct{}), effects: make(map[string]int)}
	var releaseEffects sync.Once
	calling := humancalling.New(workerDatabase, access.New(workerDatabase, time.Now), provider, humancalling.Config{Observer: observer}, time.Now)
	runner, err := worker.New(worker.Config{
		WorkInterval: 250 * time.Millisecond, WorkTimeout: 10 * time.Second,
		CredentialInterval: time.Hour, CredentialTimeout: time.Second, HealthInterval: time.Hour, HealthTimeout: time.Second,
		MetricInterval: time.Hour, MetricTimeout: time.Second, ReceiptBatchSize: 8, RecoveryAndMessagingBatchSize: 1,
		ProviderCommandBatchSize: 8, CommandWorkers: 10,
	}, calling, idleMessagingWork{}, idleInteractionWork{}, workerPool)
	if err != nil {
		t.Fatal(err)
	}
	heldConnection, err := workerPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var releaseConnection sync.Once
	timer := time.AfterFunc(200*time.Millisecond, func() { releaseConnection.Do(heldConnection.Release) })
	t.Cleanup(func() { timer.Stop(); releaseConnection.Do(heldConnection.Release) })
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- runner.Run(ctx) }()
	stopped := false
	t.Cleanup(func() {
		releaseEffects.Do(func() { close(provider.release) })
		cancel()
		if !stopped {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("mixed Runner did not stop")
			}
		}
	})
	analytics := interaction.New(portalDatabase, access.New(portalDatabase, time.Now), time.Now)
	analyticsResult := make(chan error, 1)
	go func() {
		for range 3 {
			page, err := analytics.QueryAnalytics(ctx, interaction.QueryAnalyticsCommand{
				Identity:   access.Identity{Subject: "mixed-operator", Email: "mixed-operator@synthetic.test", EmailVerified: true},
				PracticeID: practices[0].Practice.ID, Range: interaction.AnalyticsRange24Hours, Limit: 25,
			})
			if err != nil {
				analyticsResult <- err
				return
			}
			if page.Summary.TotalCalls != analyticsCalls || len(page.Calls) != 25 || page.Summary.P50TotalLatencyMs == nil || *page.Summary.P50TotalLatencyMs != 400 {
				analyticsResult <- fmt.Errorf("unexpected analytics results: calls=%d page=%d median=%v", page.Summary.TotalCalls, len(page.Calls), page.Summary.P50TotalLatencyMs)
				return
			}
		}
		analyticsResult <- nil
	}()
	for range 10 {
		select {
		case <-provider.started:
		case <-ctx.Done():
			t.Fatal("both Calls did not dispatch all Staff while provider work overlapped")
		}
	}
	dispatched := time.Since(started)
	select {
	case err := <-analyticsResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("analytics did not progress")
	}
	for {
		var projected int
		if err := portalDatabase.QueryRow(ctx, `SELECT count(*) FROM human_calling_provider_receipts WHERE event_id LIKE 'mixed-receipt-%' AND state='UNKNOWN'`).Scan(&projected); err != nil {
			t.Fatal(err)
		}
		if projected == receiptCount {
			break
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("receipt burst projected=%d want%d", projected, receiptCount)
		}
	}
	receiptsAndAnalytics := time.Since(started)
	releaseEffects.Do(func() { close(provider.release) })
	for {
		var sent, failed, ringbacks int
		if err := portalDatabase.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM human_calling_provider_commands WHERE action='DIAL_STAFF' AND state='SENT'),
 (SELECT count(*) FROM human_calling_provider_commands WHERE action='DIAL_STAFF' AND state='FAILED'),
 (SELECT count(*) FROM human_calling_provider_commands WHERE action='START_RING_WINDOW' AND state='SENT')`).Scan(&sent, &failed, &ringbacks); err != nil {
			t.Fatal(err)
		}
		if sent == 9 && failed == 1 && ringbacks == 2 {
			break
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("durable results sent=%d failed=%d ringbacks=%d", sent, failed, ringbacks)
		}
	}
	var callsWithDials int
	if err := portalDatabase.QueryRow(ctx, `
 SELECT count(DISTINCT call_id) FROM human_calling_provider_commands
 WHERE action = 'DIAL_STAFF' AND state IN ('SENT', 'FAILED')
 `).Scan(&callsWithDials); err != nil {
		t.Fatal(err)
	}
	if callsWithDials != 2 {
		t.Fatalf("Calls with durable Dial outcomes=%d want2", callsWithDials)
	}
	// Snapshot the completed workload before shutdown; cancellation is outside
	// this window so it cannot look like a workload acquisition failure.
	complete := time.Since(started)
	logs := metrics.String()
	if strings.Contains(logs, `"metric":"acuity_backend_database_execution"`) || strings.Contains(logs, `"metric":"acuity_call_center_database_pool_acquire"`) {
		t.Fatalf("mixed workload emitted database failure telemetry: %s", logs)
	}
	provider.mu.Lock()
	for _, count := range provider.effects {
		if count != 1 {
			t.Errorf("provider command executed%d times", count)
		}
	}
	if len(provider.effects) != 12 {
		t.Errorf("distinct effects=%d want12", len(provider.effects))
	}
	provider.mu.Unlock()
	cancel()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mixed Runner did not stop")
	}
	t.Logf("2 Calls/10 Dials (1 definitive failure),16 receipts,3 analytics reads/300 rows,worker DB slots=2 with1 held200ms: all-dispatched=%s receipts+analytics=%s durable-complete=%s", dispatched, receiptsAndAnalytics, complete)
}

type mixedLatencyProvider struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	effects map[string]int
	dials   int
}

func (provider *mixedLatencyProvider) Execute(ctx context.Context, command humancalling.ProviderCommand) (humancalling.ProviderResult, error) {
	provider.mu.Lock()
	provider.effects[command.ID]++
	dial := command.Action == humancalling.CommandDialStaff
	ordinal := 0
	if dial {
		provider.dials++
		ordinal = provider.dials
	}
	provider.mu.Unlock()
	if !dial {
		return humancalling.ProviderResult{}, nil
	}
	provider.started <- struct{}{}
	select {
	case <-provider.release:
	case <-ctx.Done():
		return humancalling.ProviderResult{}, ctx.Err()
	}
	if ordinal == 1 {
		return humancalling.ProviderResult{}, humancalling.ErrDefinitiveProviderFailure
	}
	return humancalling.ProviderResult{CallControlID: fmt.Sprintf("mixed-dial-control-%d", ordinal), CallLegID: fmt.Sprintf("mixed-dial-leg-%d", ordinal)}, nil
}

type mixedLatencyLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (log *mixedLatencyLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.buffer.Write(data)
}
func (log *mixedLatencyLog) String() string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.buffer.String()
}

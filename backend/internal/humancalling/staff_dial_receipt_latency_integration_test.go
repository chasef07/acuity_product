package humancalling_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/worker"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The measured window starts before signed ingress, with the real Runner already
// polling. Provider invocation is the endpoint; a local stub cannot prove ringing
// in a Staff browser. Submission follows an empty receipt scan, deliberately
// exercising the idle-poll phase; this is not a production percentile sample.
// Timings are observations, not flaky wall-clock assertions.
func TestStaffDialLatencyFromSignedAnswerReceipt(t *testing.T) {
	for _, scenario := range []struct {
		staff                    int
		ringback, holdConnection time.Duration
	}{
		{staff: 1}, {staff: 5}, {staff: 10},
		{staff: 1, ringback: 200 * time.Millisecond},
		{staff: 5, ringback: 200 * time.Millisecond},
		{staff: 10, ringback: 200 * time.Millisecond},
		{staff: 10, holdConnection: 600 * time.Millisecond},
	} {
		name := fmt.Sprintf("staff_%d/ringback_%s/connection_hold_%s", scenario.staff, scenario.ringback, scenario.holdConnection)
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			pool := testdb.Open(t)
			publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			config := humancalling.Config{
				HandoffSIPDomain: "synthetic.sip.telnyx.com", StaffSIPDomain: "sip.telnyx.com",
				RingWindowDuration: 20 * time.Second, HandoffTokenKey: []byte("0123456789abcdef0123456789abcdef"),
				CallControlID: "synthetic-connection", CredentialConnectionID: "synthetic-credential-connection",
				FromNumber: "+15555550102", RingbackURL: "https://media.synthetic.test/ringback.wav",
				WebhookPublicKeys: [][]byte{publicKey},
			}
			seedAccess := access.New(pool, time.Now)
			authorization, staff := provisionConcurrentStaff(t, seedAccess, time.Now(), "receipt-latency", scenario.staff)
			seedCalling := humancalling.New(pool, seedAccess, &recordingProvider{}, config, time.Now)
			prepareCredentials(t, seedCalling)
			readyConcurrentStaff(t, seedCalling, staff, "receipt-latency-browser")
			if _, err := seedCalling.CreateHandoff(ctx, humancalling.CreateHandoffCommand{
				Service:    humancalling.ServiceIdentity{Subject: "synthetic-agent", PracticeID: authorization.Practice.ID},
				LocationID: authorization.Locations[0].ID, SourceCallID: "receipt-latency-source",
				IdempotencyKey: "receipt-latency-handoff", Contact: humancalling.ContactContext{Phone: "+15555550100"},
			}); err != nil {
				t.Fatal(err)
			}
			if err := seedCalling.ApplyProviderFact(ctx, humancalling.ProviderFact{
				EventID: "receipt-latency-initiated", Type: humancalling.FactCallInitiated, OccurredAt: time.Now(),
				ConnectionID: config.CallControlID, CallControlID: "receipt-latency-caller-control",
				CallLegID: "receipt-latency-caller-leg", CallSessionID: "receipt-latency-caller-session",
				From: "+15555550100", To: "+15555550103",
			}); err != nil {
				t.Fatal(err)
			}
			processAllCommands(t, seedCalling)
			newExecutor := func() (*pgxpool.Pool, *postgres.Executor) {
				poolConfig := pool.Config().Copy()
				poolConfig.MaxConns = 2
				runtimePool, err := pgxpool.NewWithConfig(ctx, poolConfig)
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
				return runtimePool, database
			}
			workerPool, workerExecutor := newExecutor()
			_, ingressExecutor := newExecutor()
			measuredDatabase := &fanoutCommitDatabase{Database: workerExecutor, committed: make(chan fanoutCommitObservation, 1)}
			provider := &receiptLatencyProvider{ringbackDelay: scenario.ringback, dispatches: make(chan time.Time, scenario.staff), effects: make(map[string]int)}
			calling := humancalling.New(measuredDatabase, access.New(measuredDatabase, time.Now), provider, config, time.Now)
			work := &startedReceiptWork{Module: calling, polled: make(chan struct{})}
			runner, err := worker.New(worker.Config{
				WorkInterval: 250 * time.Millisecond, WorkTimeout: 10 * time.Second,
				CredentialInterval: time.Hour, CredentialTimeout: time.Second, HealthInterval: time.Hour, HealthTimeout: time.Second,
				MetricInterval: time.Hour, MetricTimeout: time.Second, ReceiptBatchSize: 8, RecoveryAndMessagingBatchSize: 1,
				ProviderCommandBatchSize: 8, CommandWorkers: 10,
			}, work, idleMessagingWork{}, idleInteractionWork{}, workerPool)
			if err != nil {
				t.Fatal(err)
			}
			runnerDone := make(chan error, 1)
			go func() { runnerDone <- runner.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-runnerDone:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("receipt Runner did not stop")
				}
			})
			select {
			case <-work.polled:
			case <-ctx.Done():
				t.Fatal("Runner did not reach its initial receipt scan")
			}
			var connectionReservedAt time.Time
			var connectionReleased <-chan time.Time
			if scenario.holdConnection > 0 {
				connection, err := workerPool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				connectionReservedAt = time.Now()
				released := make(chan time.Time, 1)
				connectionReleased = released
				var release sync.Once
				releaseReserved := func() { release.Do(func() { connection.Release(); released <- time.Now() }) }
				timer := time.AfterFunc(scenario.holdConnection, releaseReserved)
				t.Cleanup(func() { timer.Stop(); releaseReserved() })
			}
			ingress := humancalling.New(ingressExecutor, access.New(ingressExecutor, time.Now), nil, config, time.Now)
			raw, err := json.Marshal(map[string]any{"data": map[string]any{
				"record_type": "event", "event_type": "call.answered", "id": "receipt-latency-answered",
				"occurred_at": time.Now().Format(time.RFC3339Nano), "payload": map[string]any{
					"connection_id": config.CallControlID, "call_control_id": "receipt-latency-caller-control",
					"call_leg_id": "receipt-latency-caller-leg", "call_session_id": "receipt-latency-caller-session",
					"from": "+15555550100", "to": "+15555550103",
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, append([]byte(timestamp+"|"), raw...)))
			submittedAt := time.Now()
			receipt, err := ingress.ReceiveWebhook(ctx, raw, timestamp, signature)
			acceptedAt := time.Now()
			if err != nil || receipt.Duplicate {
				t.Fatalf("signed ingress receipt=%+v error=%v", receipt, err)
			}
			var fanout fanoutCommitObservation
			select {
			case fanout = <-measuredDatabase.committed:
			case <-ctx.Done():
				t.Fatal("answer receipt did not commit Staff fanout")
			}
			first, last := time.Time{}, time.Time{}
			for range scenario.staff {
				select {
				case dispatched := <-provider.dispatches:
					if first.IsZero() || dispatched.Before(first) {
						first = dispatched
					}
					if dispatched.After(last) {
						last = dispatched
					}
				case <-ctx.Done():
					t.Fatal("committed fanout did not dispatch every Staff Dial")
				}
			}
			// APPLIED is a later receipt bookkeeping write, distinct from the atomic
			// fanout commit observed above. Require both it and durable provider results.
			for {
				var applied bool
				var sentDials, sentRingback int
				if err := pool.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM human_calling_provider_receipts WHERE event_id='receipt-latency-answered' AND state='APPLIED'),
 (SELECT count(*) FROM human_calling_provider_commands WHERE action='DIAL_STAFF' AND state='SENT'),
 (SELECT count(*) FROM human_calling_provider_commands WHERE action='START_RING_WINDOW' AND state='SENT')`).Scan(&applied, &sentDials, &sentRingback); err != nil {
					t.Fatal(err)
				}
				if applied && sentDials == scenario.staff && sentRingback == 1 {
					break
				}
				select {
				case <-time.After(5 * time.Millisecond):
				case <-ctx.Done():
					t.Fatalf("durable receipt/effects incomplete: applied=%t dials=%d ringback=%d", applied, sentDials, sentRingback)
				}
			}
			provider.mu.Lock()
			for _, count := range provider.effects {
				if count != 1 {
					t.Errorf("provider effect attempts=%d want1", count)
				}
			}
			if len(provider.effects) != scenario.staff+1 {
				t.Errorf("unique provider effects=%d want%d", len(provider.effects), scenario.staff+1)
			}
			ringbackStarted, ringbackCompleted := provider.ringbackStarted, provider.ringbackCompleted
			provider.mu.Unlock()
			gap := "not_before_first_dial"
			if !ringbackCompleted.IsZero() && ringbackCompleted.Before(first) {
				gap = first.Sub(ringbackCompleted).String()
			}
			if connectionReleased != nil {
				select {
				case releasedAt := <-connectionReleased:
					t.Logf("one worker connection reserved=%s fanout-commit-during-reservation=%t (reduced capacity; not a forced query block)", releasedAt.Sub(connectionReservedAt), connectionReservedAt.Before(fanout.started) && fanout.completed.Before(releasedAt))
				case <-ctx.Done():
					t.Fatal("reserved worker connection did not release")
				}
			}
			t.Logf("signed-ingress=%s accepted-to-fanout-commit=%s fanout-begin-through-commit=%s accepted-to-first-dial=%s accepted-to-last-dial=%s fanout-to-first-dial=%s dial-spread=%s ringback-start-before-first=%t ringback-complete-to-first=%s",
				acceptedAt.Sub(submittedAt), fanout.completed.Sub(acceptedAt), fanout.completed.Sub(fanout.started), first.Sub(acceptedAt), last.Sub(acceptedAt), first.Sub(fanout.completed), last.Sub(first), ringbackStarted.Before(first), gap)
		})
	}
}

// This wrapper only observes the actual production transaction's commit return.
// It neither changes SQL nor performs additional database reads in that path.
type fanoutCommitDatabase struct {
	postgres.Database
	committed chan fanoutCommitObservation
}
type fanoutCommitObservation struct{ started, completed time.Time }

func (database *fanoutCommitDatabase) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	started := time.Now()
	tx, err := database.Database.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &fanoutCommitTransaction{Tx: tx, started: started, committed: database.committed}, nil
}

type fanoutCommitTransaction struct {
	pgx.Tx
	started   time.Time
	fanout    bool
	committed chan fanoutCommitObservation
}

func (tx *fanoutCommitTransaction) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	tag, err := tx.Tx.Exec(ctx, query, args...)
	if err == nil && strings.Contains(query, "INSERT INTO human_calling_call_legs") && strings.Contains(query, "'STAFF'") {
		tx.fanout = true
	}
	return tag, err
}
func (tx *fanoutCommitTransaction) Commit(ctx context.Context) error {
	err := tx.Tx.Commit(ctx)
	if err == nil && tx.fanout {
		tx.committed <- fanoutCommitObservation{started: tx.started, completed: time.Now()}
	}
	return err
}

type startedReceiptWork struct {
	*humancalling.Module
	polled chan struct{}
	once   sync.Once
}

func (work *startedReceiptWork) ProcessNextReceipt(ctx context.Context) (bool, error) {
	processed, err := work.Module.ProcessNextReceipt(ctx)
	work.once.Do(func() { close(work.polled) })
	return processed, err
}

type receiptLatencyProvider struct {
	mu                                 sync.Mutex
	ringbackDelay                      time.Duration
	ringbackStarted, ringbackCompleted time.Time
	dispatches                         chan time.Time
	effects                            map[string]int
}

func (provider *receiptLatencyProvider) Execute(ctx context.Context, command humancalling.ProviderCommand) (humancalling.ProviderResult, error) {
	started := time.Now()
	provider.mu.Lock()
	provider.effects[command.ID]++
	provider.mu.Unlock()
	if command.Action == humancalling.CommandStartRingWindow {
		provider.mu.Lock()
		provider.ringbackStarted = started
		provider.mu.Unlock()
		select {
		case <-time.After(provider.ringbackDelay):
		case <-ctx.Done():
			return humancalling.ProviderResult{}, ctx.Err()
		}
		provider.mu.Lock()
		provider.ringbackCompleted = time.Now()
		provider.mu.Unlock()
	}
	if command.Action == humancalling.CommandDialStaff {
		provider.dispatches <- started
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			return humancalling.ProviderResult{}, ctx.Err()
		}
		return humancalling.ProviderResult{CallControlID: "synthetic-control-" + command.ID, CallLegID: "synthetic-leg-" + command.ID}, nil
	}
	return humancalling.ProviderResult{}, nil
}

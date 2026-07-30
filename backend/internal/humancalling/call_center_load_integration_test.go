package humancalling_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMixedRoleBurstKeepsTenStaffCommandsAndWorkerLanesMoving(t *testing.T) {
	testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ingressPool := openLoadPool(t, 1)
	portalPool := openLoadPool(t, 1)
	workerPool := openLoadPool(t, 1)
	now := time.Date(2026, time.July, 29, 18, 30, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate mixed-role webhook key: %v", err)
	}
	provider := newBlockingRecordingProvider()
	portalAccess := access.New(portalPool, func() time.Time { return now })
	config := humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		StaffSIPDomain:   "sip.telnyx.com",
		OfferDuration:    20 * time.Second,
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		WebhookPublicKey: publicKey,
		WebhookTolerance: 5 * time.Minute,
	}
	clock := func() time.Time { return now }
	ingress := humancalling.New(ingressPool, nil, nil, config, clock)
	portal := humancalling.New(portalPool, portalAccess, provider, config, clock)
	durableWorker := humancalling.New(workerPool, nil, provider, config, clock)

	authorization, identities := provisionConcurrentStaff(
		t, portalAccess, now, "load", 10,
	)
	prepareCredentials(t, portal)
	readyConcurrentStaff(t, portal, identities, "load-browser")

	const backgroundCalls = 8
	target := createLoadHandoff(t, portal, authorization, "target")
	initiated := [][]byte{loadWebhook(
		now, "call.initiated", "load-00-initiated", "target", target.SIPDestination,
	)}
	answered := make([][]byte, 0, backgroundCalls*3)
	for index := range backgroundCalls {
		key := fmt.Sprintf("background-%02d", index+1)
		handoff := createLoadHandoff(t, portal, authorization, key)
		occurredAt := now.Add(time.Duration(index) * time.Millisecond)
		first := loadWebhook(
			occurredAt,
			"call.initiated",
			fmt.Sprintf("load-%02d-01-initiated", index+1),
			key,
			handoff.SIPDestination,
		)
		second := loadWebhook(
			occurredAt.Add(time.Microsecond),
			"call.answered",
			fmt.Sprintf("load-%02d-02-answered", index+1),
			key,
			"",
		)
		initiated = append(initiated, first, first)
		answered = append(answered, first, second, second)
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	ackDurations := collectLoadBurst(
		t,
		startLoadBurst(ctx, ingress, privateKey, timestamp, initiated).results,
		len(initiated),
	)
	if processed, err := durableWorker.ProcessNextReceipt(ctx); err != nil || !processed {
		t.Fatalf("project target receipt: processed=%t err=%v", processed, err)
	}
	for {
		processed, err := durableWorker.ProcessNextCommand(ctx)
		if err != nil {
			t.Fatalf("drain target setup command: %v", err)
		}
		if !processed {
			break
		}
	}
	provider.reset()

	var targetCallID string
	if err := portalPool.QueryRow(ctx, `
		SELECT id::text FROM human_calling_calls
		WHERE call_session_id = 'load-target-session'
	`).Scan(&targetCallID); err != nil {
		t.Fatalf("read target Call: %v", err)
	}

	held := make([]*pgxpool.Conn, 0, 1)
	defer func() {
		for _, connection := range held {
			connection.Release()
		}
	}()
	for range 1 {
		connection, err := ingressPool.Acquire(ctx)
		if err != nil {
			t.Fatalf("hold provider-ingress connection: %v", err)
		}
		held = append(held, connection)
	}
	retryBurst := startLoadBurst(ctx, ingress, privateKey, timestamp, answered)
	<-retryBurst.started

	type commandResult struct {
		status   humancalling.AcceptStatus
		duration time.Duration
		err      error
	}
	startReads, startAccepts := make(chan struct{}), make(chan struct{})
	commands := make(chan commandResult, len(identities))
	portalWaitBeforeCommands := portalPool.Stat().AcquireDuration()
	var reads sync.WaitGroup
	reads.Add(len(identities))
	for index, identity := range identities {
		go func() {
			<-startReads
			startedAt := time.Now()
			offers, err := portal.ListOffers(ctx, identity)
			if err == nil && (len(offers) != 1 || offers[0].ID != targetCallID) {
				err = fmt.Errorf("offers=%#v", offers)
			}
			reads.Done()
			<-startAccepts
			result := humancalling.AcceptResult{}
			if err == nil {
				result, err = portal.AcceptOffer(
					ctx,
					identity,
					fmt.Sprintf("load-browser-%d", index+1),
					targetCallID,
				)
			}
			commands <- commandResult{
				status: result.Status, duration: time.Since(startedAt), err: err,
			}
		}()
	}
	close(startReads)
	reads.Wait()
	close(startAccepts)
	statuses := map[humancalling.AcceptStatus]int{}
	commandDurations := make([]time.Duration, 0, len(identities))
	for range identities {
		outcome := <-commands
		if outcome.err != nil {
			t.Fatalf("staff command during ingress saturation: %v", outcome.err)
		}
		statuses[outcome.status]++
		commandDurations = append(commandDurations, outcome.duration)
	}
	if statuses[humancalling.Accepted] != 1 ||
		statuses[humancalling.AlreadyClaimed] != len(identities)-1 {
		t.Fatalf("mixed-role accept outcomes = %#v", statuses)
	}
	if len(retryBurst.results) != 0 {
		t.Fatal("provider burst completed while every ingress connection was held")
	}
	for _, connection := range held {
		connection.Release()
	}
	held = nil
	ackDurations = append(
		ackDurations,
		collectLoadBurst(t, retryBurst.results, len(answered))...,
	)

	provider.blockDial()
	commandDone := make(chan error, 1)
	go func() {
		processed, err := durableWorker.ProcessNextCommand(ctx)
		if err == nil && !processed {
			err = fmt.Errorf("target Dial command was not claimed")
		}
		commandDone <- err
	}()
	awaitSignal(t, provider.dialStarted, "target Dial command")
	for range backgroundCalls * 2 {
		if processed, err := durableWorker.ProcessNextReceipt(ctx); err != nil || !processed {
			t.Fatalf("project receipt beside blocked command: processed=%t err=%v", processed, err)
		}
	}
	close(provider.dialRelease)
	if err := <-commandDone; err != nil {
		t.Fatalf("finish target Dial command: %v", err)
	}
	for {
		processed, err := durableWorker.ProcessNextCommand(ctx)
		if err != nil {
			t.Fatalf("drain background provider command: %v", err)
		}
		if !processed {
			break
		}
	}

	var receipts, duplicates, applied, calls int
	if err := workerPool.QueryRow(ctx, `
		SELECT
			count(*),
			COALESCE(sum(duplicate_count), 0),
			count(*) FILTER (WHERE state = 'APPLIED'),
			(SELECT count(*) FROM human_calling_calls
			 WHERE call_session_id LIKE 'load-background-%-session')
		FROM human_calling_provider_receipts
		WHERE event_id LIKE 'load-__-__-%'
	`).Scan(&receipts, &duplicates, &applied, &calls); err != nil {
		t.Fatalf("read mixed-role convergence: %v", err)
	}
	if receipts != backgroundCalls*2 || applied != receipts ||
		duplicates != backgroundCalls*3 || calls != backgroundCalls {
		t.Fatalf(
			"mixed-role convergence: receipts=%d applied=%d duplicates=%d calls=%d",
			receipts, applied, duplicates, calls,
		)
	}
	if provider.count(humancalling.CommandDialStaff) != 1 ||
		provider.count(humancalling.CommandAnswerCaller) != backgroundCalls ||
		provider.count(humancalling.CommandStartRingback) != backgroundCalls {
		t.Fatalf("mixed-role provider commands = %#v", provider.commands)
	}
	if duplicateID := provider.duplicateCommandID(); duplicateID != "" {
		t.Fatalf("provider command %s executed more than once", duplicateID)
	}
	for _, role := range []struct {
		name string
		pool *pgxpool.Pool
		max  int32
	}{
		{"provider-ingress", ingressPool, 1},
		{"portal-api", portalPool, 1},
		{"worker", workerPool, 1},
	} {
		if role.pool.Config().MaxConns != role.max ||
			role.pool.Stat().TotalConns() > role.max {
			t.Fatalf("%s pool exceeded %d connections", role.name, role.max)
		}
	}

	ackP95, ackP99 := loadPercentile(ackDurations, 95), loadPercentile(ackDurations, 99)
	commandP95 := loadPercentile(commandDurations, 95)
	commandP99 := loadPercentile(commandDurations, 99)
	portalCommandPoolWait :=
		portalPool.Stat().AcquireDuration() - portalWaitBeforeCommands
	const localCeiling = time.Second
	if ackP99 > localCeiling || commandP99 > localCeiling {
		t.Fatalf(
			"deterministic local ceiling exceeded: ack_p99=%s command_p99=%s",
			ackP99, commandP99,
		)
	}
	t.Logf(
		"deterministic local proof only (not a production SLA): webhook_ack_p95=%s webhook_ack_p99=%s staff_command_p95=%s staff_command_p99=%s portal_pool_wait_during_staff_commands=%s",
		ackP95,
		ackP99,
		commandP95,
		commandP99,
		portalCommandPoolWait,
	)
}

type loadBurst struct {
	started <-chan struct{}
	results <-chan loadDelivery
}
type loadDelivery struct {
	duration time.Duration
	err      error
}

func startLoadBurst(
	ctx context.Context,
	ingress *humancalling.Module,
	privateKey ed25519.PrivateKey,
	timestamp string,
	deliveries [][]byte,
) loadBurst {
	started := make(chan struct{})
	results := make(chan loadDelivery, len(deliveries))
	var arrivals sync.WaitGroup
	arrivals.Add(len(deliveries))
	for _, raw := range deliveries {
		go func() {
			arrivals.Done()
			signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
				privateKey,
				append([]byte(timestamp+"|"), raw...),
			))
			startedAt := time.Now()
			_, err := ingress.ReceiveWebhook(ctx, raw, timestamp, signature)
			results <- loadDelivery{duration: time.Since(startedAt), err: err}
		}()
	}
	go func() {
		arrivals.Wait()
		close(started)
	}()
	return loadBurst{started: started, results: results}
}

func collectLoadBurst(
	t *testing.T, results <-chan loadDelivery, count int,
) []time.Duration {
	t.Helper()
	durations := make([]time.Duration, 0, count)
	for range count {
		result := <-results
		if result.err != nil {
			t.Fatalf("receive signed mixed-role webhook: %v", result.err)
		}
		durations = append(durations, result.duration)
	}
	return durations
}

func openLoadPool(t *testing.T, maximum int32) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse load pool: %v", err)
	}
	config.MinConns, config.MaxConns = 0, maximum
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open load pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func createLoadHandoff(
	t *testing.T, calling *humancalling.Module,
	authorization access.Authorization, key string,
) humancalling.Handoff {
	t.Helper()
	handoff, err := calling.CreateHandoff(
		context.Background(),
		humancalling.CreateHandoffCommand{
			Service: humancalling.ServiceIdentity{
				Subject: "abita-load", PracticeID: authorization.Practice.ID,
			},
			LocationID:   authorization.Locations[0].ID,
			SourceCallID: "load-" + key, IdempotencyKey: "load-" + key,
			Contact: humancalling.ContactContext{
				Phone: loadCallerPhone(key), DisplayName: "Load Caller",
				TransferReason: "Mixed-role load proof",
			},
		},
	)
	if err != nil {
		t.Fatalf("create load handoff %s: %v", key, err)
	}
	return handoff
}

func loadWebhook(
	occurredAt time.Time, eventType, eventID, key, destination string,
) []byte {
	referIdentity := ""
	if destination != "" {
		referIdentity = fmt.Sprintf(
			`,"from":"%s","to":"+14843336938"`,
			loadCallerPhone(key),
		)
	}
	return []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"%s","id":"%s","occurred_at":"%s","payload":{"call_control_id":"load-%s-control","call_leg_id":"load-%s-leg","call_session_id":"load-%s-session"%s}}}`,
		eventType, eventID, occurredAt.Format(time.RFC3339Nano), key, key, key, referIdentity,
	))
}

func loadCallerPhone(key string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	return fmt.Sprintf("+1555%07d", hash.Sum32()%10_000_000)
}

func loadPercentile(values []time.Duration, percentile int) time.Duration {
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	index := (len(ordered)*percentile + 99) / 100
	return ordered[max(index-1, 0)]
}

type blockingRecordingProvider struct {
	recordingProvider
	blockNextDial   bool
	dialStarted     chan struct{}
	dialRelease     chan struct{}
	dialStartedOnce sync.Once
}

func newBlockingRecordingProvider() *blockingRecordingProvider {
	return &blockingRecordingProvider{
		dialStarted: make(chan struct{}), dialRelease: make(chan struct{}),
	}
}

func (provider *blockingRecordingProvider) Execute(
	ctx context.Context, command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	provider.mu.Lock()
	block := provider.blockNextDial && command.Action == humancalling.CommandDialStaff
	provider.mu.Unlock()
	if block {
		provider.dialStartedOnce.Do(func() { close(provider.dialStarted) })
		select {
		case <-provider.dialRelease:
		case <-ctx.Done():
			return humancalling.ProviderResult{}, ctx.Err()
		}
	}
	return provider.recordingProvider.Execute(ctx, command)
}

func (provider *blockingRecordingProvider) reset() {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.commands = nil
}

func (provider *blockingRecordingProvider) blockDial() {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.blockNextDial = true
}

func (provider *blockingRecordingProvider) duplicateCommandID() string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	seen := make(map[string]bool)
	for _, command := range provider.commands {
		if seen[command.ID] {
			return command.ID
		}
		seen[command.ID] = true
	}
	return ""
}

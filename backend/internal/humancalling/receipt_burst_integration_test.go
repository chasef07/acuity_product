package humancalling_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProviderReceiptBurstAppliesWithinOneSecond(t *testing.T) {
	const calls = 20
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := testdb.Open(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	config := humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com", StaffSIPDomain: "sip.telnyx.com",
		RingWindowDuration: 20 * time.Second, HandoffTokenKey: []byte("0123456789abcdef0123456789abcdef"),
		CallControlID: "burst-connection", CredentialConnectionID: "burst-credential-connection",
		FromNumber: "+15555550102", RingbackURL: "https://media.synthetic.test/ringback.wav",
		WebhookPublicKeys: [][]byte{publicKey},
	}
	provider := &recordingProvider{}
	seedAccess := access.New(pool, time.Now)
	seedCalling := humancalling.New(pool, seedAccess, provider, config, time.Now)
	burst := make([][]map[string]any, calls)
	for index := range calls {
		burst[index] = stageBurstCall(t, ctx, pool, seedAccess, seedCalling, provider, config, index)
	}

	newExecutor := func(maxConns int32) (*pgxpool.Pool, *postgres.Executor) {
		poolConfig := pool.Config().Copy()
		poolConfig.MaxConns = maxConns
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
	workerPool, workerDatabase := newExecutor(2)
	_, ingressDatabase := newExecutor(1)
	calling := humancalling.New(workerDatabase, access.New(workerDatabase, time.Now), provider, config, time.Now)
	runner, err := worker.New(worker.Config{
		WorkInterval: 250 * time.Millisecond, WorkTimeout: 10 * time.Second,
		CredentialInterval: time.Hour, CredentialTimeout: time.Second, HealthInterval: time.Hour, HealthTimeout: time.Second,
		MetricInterval: time.Hour, MetricTimeout: time.Second, ReceiptBatchSize: 8, ReceiptWorkers: 4,
		RecoveryAndMessagingBatchSize: 1, ProviderCommandBatchSize: 8, CommandWorkers: 10, IdleBackoffMax: 2 * time.Second,
		ErrorBackoffMin: 250 * time.Millisecond, ErrorBackoffMax: 10 * time.Second,
	}, calling, idleMessagingWork{}, idleInteractionWork{}, workerPool)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stopRunner := context.WithCancel(ctx)
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.Run(runCtx) }()
	defer func() {
		stopRunner()
		if err := <-runnerDone; err != nil {
			t.Error(err)
		}
	}()

	ingress := humancalling.New(ingressDatabase, access.New(ingressDatabase, time.Now), nil, config, time.Now)
	eventIDs := []string{}
	for step := range len(burst[0]) {
		for index := range calls {
			event := burst[index][step]
			raw, err := json.Marshal(map[string]any{"data": event})
			if err != nil {
				t.Fatal(err)
			}
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, append([]byte(timestamp+"|"), raw...)))
			if receipt, err := ingress.ReceiveWebhook(ctx, raw, timestamp, signature); err != nil || receipt.Duplicate {
				t.Fatalf("signed burst receipt=%+v error=%v", receipt, err)
			}
			eventIDs = append(eventIDs, event["id"].(string))
		}
	}

	for {
		var unfinished int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM human_calling_provider_receipts
			WHERE event_id = ANY($1) AND projected_at IS NULL
		`, eventIDs).Scan(&unfinished); err != nil {
			t.Fatal(err)
		}
		if unfinished == 0 {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("%d burst receipts were not projected", unfinished)
		}
	}
	rows, err := pool.Query(ctx, `
		SELECT state, projection_attempts,
			extract(epoch FROM projected_at - received_at)::float8,
			extract(epoch FROM projected_at - last_attempt_at)::float8
		FROM human_calling_provider_receipts
		WHERE event_id = ANY($1)
	`, eventIDs)
	if err != nil {
		t.Fatal(err)
	}
	lags := []float64{}
	processing := []float64{}
	states := map[string]int{}
	retried := 0
	for rows.Next() {
		var state string
		var attempts int
		var lag, apply float64
		if err := rows.Scan(&state, &attempts, &lag, &apply); err != nil {
			t.Fatal(err)
		}
		states[state]++
		if attempts > 1 {
			retried++
		}
		lags = append(lags, lag)
		processing = append(processing, apply)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Float64s(lags)
	sort.Float64s(processing)
	seconds := func(value float64) time.Duration {
		return time.Duration(value * float64(time.Second)).Round(time.Millisecond)
	}
	maximum := seconds(lags[len(lags)-1])
	t.Logf("burst receipts=%d calls=%d apply_lag_p50=%s apply_lag_max=%s processing_p50=%s processing_max=%s states=%v retried=%d",
		len(lags), calls, seconds(lags[len(lags)/2]), maximum,
		seconds(processing[len(processing)/2]), seconds(processing[len(processing)-1]), states, retried)
	if len(lags) != calls*len(burst[0]) || states[string(humancalling.ReceiptApplied)] != len(lags) || retried != 0 {
		t.Fatalf("burst receipts must each apply on their first attempt: states=%v retried=%d", states, retried)
	}
	if maximum >= time.Second {
		t.Fatalf("burst apply lag max=%s, want under 1s", maximum)
	}
}

func stageBurstCall(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	seedAccess *access.Module,
	seedCalling *humancalling.Module,
	provider *recordingProvider,
	config humancalling.Config,
	index int,
) []map[string]any {
	t.Helper()
	prefix := fmt.Sprintf("receipt-burst-%02d", index)
	authorization, staff := provisionConcurrentStaff(t, seedAccess, time.Now(), prefix, 1)
	prepareCredentials(t, seedCalling)
	readyConcurrentStaff(t, seedCalling, staff, prefix+"-browser")
	phone := fmt.Sprintf("+1555556%04d", index)
	if _, err := seedCalling.CreateHandoff(ctx, humancalling.CreateHandoffCommand{
		Service:    humancalling.ServiceIdentity{Subject: "synthetic-agent", PracticeID: authorization.Practice.ID},
		LocationID: authorization.Locations[0].ID, SourceCallID: prefix + "-source",
		IdempotencyKey: prefix + "-handoff", Contact: humancalling.ContactContext{Phone: phone},
	}); err != nil {
		t.Fatal(err)
	}
	callerControl, callerLeg, session := prefix+"-caller-control", prefix+"-caller-leg", prefix+"-session"
	caller := humancalling.ProviderFact{
		EventID: prefix + "-caller-initiated", Type: humancalling.FactCallInitiated, OccurredAt: time.Now(),
		ConnectionID: config.CallControlID, CallControlID: callerControl, CallLegID: callerLeg,
		CallSessionID: session, From: phone, To: "+15555550103",
	}
	if err := seedCalling.ApplyProviderFact(ctx, caller); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, seedCalling)
	caller.EventID, caller.Type, caller.OccurredAt = prefix+"-caller-answered", humancalling.FactCallAnswered, time.Now()
	staffControl, staffLeg := prefix+"-staff-control", prefix+"-staff-leg"
	provider.mu.Lock()
	provider.dialResults = []humancalling.ProviderResult{{CallControlID: staffControl, CallLegID: staffLeg}}
	provider.mu.Unlock()
	if err := seedCalling.ApplyProviderFact(ctx, caller); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, seedCalling)
	var ringState, dialState string
	if err := pool.QueryRow(ctx, `
		SELECT
			max(command.payload->>'client_state') FILTER (WHERE command.action = 'START_RING_WINDOW'),
			max(command.payload->>'client_state') FILTER (WHERE command.action = 'DIAL_STAFF')
		FROM human_calling_provider_commands command
		JOIN human_calling_call_legs leg ON leg.call_id = command.call_id
		WHERE leg.provider_call_control_id = $1
	`, callerControl).Scan(&ringState, &dialState); err != nil {
		t.Fatalf("read staged client states: %v", err)
	}
	payload := func(control, leg, clientState string, extra map[string]any) map[string]any {
		values := map[string]any{
			"connection_id": config.CallControlID, "call_control_id": control,
			"call_leg_id": leg, "call_session_id": session,
			"from": phone, "to": "+15555550103",
		}
		if clientState != "" {
			values["client_state"] = clientState
		}
		for key, value := range extra {
			values[key] = value
		}
		return values
	}
	event := func(name, eventType string, values map[string]any) map[string]any {
		return map[string]any{
			"record_type": "event", "event_type": eventType, "id": prefix + "-" + name,
			"occurred_at": time.Now().Format(time.RFC3339Nano), "payload": values,
		}
	}
	return []map[string]any{
		event("ring-started", "call.playback.started", payload(callerControl, callerLeg, ringState, nil)),
		event("staff-initiated", "call.initiated", payload(staffControl, staffLeg, dialState, nil)),
		event("staff-answered", "call.answered", payload(staffControl, staffLeg, dialState, nil)),
		event("ring-ended", "call.playback.ended", payload(callerControl, callerLeg, ringState, map[string]any{"status": "completed"})),
		event("caller-hangup", "call.hangup", payload(callerControl, callerLeg, "", map[string]any{
			"hangup_cause": "normal_clearing", "hangup_source": "caller",
		})),
	}
}

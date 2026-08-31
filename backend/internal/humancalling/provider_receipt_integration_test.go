package humancalling_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

func TestOutboundStaffAnswerAndProvisioningConvergeWithoutPracticeLockUpgrade(
	t *testing.T,
) {
	pool := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Date(2026, time.August, 18, 17, 56, 0, 0, time.UTC)
	const prefix = "outbound-answer-provisioning"
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, staff := provisionConcurrentStaff(t, accessModule, now, prefix, 1)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control",
		CallLegID:     prefix + "-staff-leg",
	}}}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
		WebhookPublicKeys:      [][]byte{publicKey},
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, prefix+"-browser")
	if err := calling.ProvisionLocationVoices(ctx, []humancalling.LocationVoiceProvision{{
		PracticeKey: prefix + "-practice",
		LocationKey: prefix + "-location",
		Number:      "+15555550123",
		Enabled:     true,
	}}); err != nil {
		t.Fatalf("provision outbound caller ID: %v", err)
	}
	call, err := calling.StartOutboundCall(ctx, humancalling.StartOutboundCallCommand{
		Identity:       staff[0],
		SessionID:      prefix + "-browser-1",
		IdempotencyKey: prefix,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "+15555550124",
	})
	if err != nil {
		t.Fatalf("start outbound Call: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(ctx); err != nil || !processed {
		t.Fatalf("execute outbound Staff Dial: processed=%t err=%v", processed, err)
	}
	dial := provider.last(humancalling.CommandDialOutboundStaff)
	clientState, _ := dial.Payload["client_state"].(string)
	body, err := json.Marshal(map[string]any{"data": map[string]any{
		"record_type": "event",
		"event_type":  "call.answered",
		"id":          prefix + "-answered",
		"occurred_at": now.Add(time.Second).Format(time.RFC3339Nano),
		"payload": map[string]any{
			"connection_id":   "staff-call-control-connection",
			"call_control_id": prefix + "-staff-control",
			"call_leg_id":     prefix + "-staff-leg",
			"call_session_id": prefix + "-staff-session",
			"client_state":    clientState,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), body...),
	))
	if _, err := calling.ReceiveWebhook(ctx, body, timestamp, signature); err != nil {
		t.Fatalf("receive outbound Staff answer: %v", err)
	}

	const barrierKey int64 = 818175616
	barrier := holdPostgresAdvisoryLock(t, pool, barrierKey)
	defer barrier.close()
	const triggerName = "test_block_outbound_answer_workspace_change"
	const functionName = "test_wait_for_outbound_answer_workspace_change"
	installPostgresTestTrigger(t, pool, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			PERFORM pg_advisory_xact_lock(TG_ARGV[0]::bigint);
			RETURN NEW;
		END
		$function$;
		CREATE TRIGGER %s
		AFTER INSERT ON human_calling_timeline
		FOR EACH ROW WHEN (
			NEW.call_id = '%s'::uuid AND NEW.kind = 'provider.staff.answered'
		)
		EXECUTE FUNCTION %s('%d')
	`, functionName, triggerName, call.ID, functionName, barrierKey), fmt.Sprintf(`
		DROP TRIGGER IF EXISTS %s ON human_calling_timeline;
		DROP FUNCTION IF EXISTS %s()
	`, triggerName, functionName))

	receiptResult := make(chan error, 1)
	go func() {
		processed, err := calling.ProcessNextReceipt(ctx)
		if !processed && err == nil {
			err = errors.New("no outbound Staff answer receipt processed")
		}
		receiptResult <- err
	}()
	receiptPID := waitForPostgresLockWaiter(
		t, barrier.connection, "advisory", barrier.pid,
	)

	provisioningResult := make(chan error, 1)
	go func() {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			provisioningResult <- err
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = accessModule.ProvisionInTx(ctx, tx, access.Provisioning{
			Environment: "test",
			RequestedBy: prefix + "-release",
			Practices: []access.PracticeProvision{{
				Key:  prefix + "-practice",
				Name: prefix + " practice",
				Locations: []access.LocationProvision{{
					Key:  prefix + "-location",
					Name: prefix + " location",
				}},
			}},
		})
		if err != nil {
			err = fmt.Errorf("access provisioning: %w", err)
		} else if err = calling.ProvisionOutboundVoiceFallbacksInTx(
			ctx,
			tx,
			[]humancalling.OutboundVoiceFallbackProvision{{
				PracticeKey: prefix + "-practice",
				LocationKey: prefix + "-location",
			}},
		); err != nil {
			err = fmt.Errorf("outbound fallback provisioning: %w", err)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		provisioningResult <- err
	}()

	var provisioningErr error
	provisioningComplete := false
	var provisioningPID int32
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pid, waiting, err := findPostgresLockWaiter(
			ctx, barrier.connection, "transactionid", receiptPID,
		)
		if err != nil {
			t.Fatalf("inspect provisioning blocker chain: %v", err)
		}
		if waiting {
			provisioningPID = pid
			break
		}
		select {
		case provisioningErr = <-provisioningResult:
			provisioningComplete = true
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if provisioningComplete {
			break
		}
	}
	if !provisioningComplete && provisioningPID == 0 {
		t.Fatal("provisioning neither completed nor exposed the receipt as its exact blocker")
	}
	barrier.release()
	if provisioningPID != 0 {
		waitForPostgresLockWaiter(
			t, barrier.connection, "transactionid", provisioningPID,
		)
	}
	if !provisioningComplete {
		select {
		case provisioningErr = <-provisioningResult:
		case <-time.After(5 * time.Second):
			t.Fatal("release provisioning did not finish")
		}
	}
	var receiptErr error
	select {
	case receiptErr = <-receiptResult:
	case <-time.After(5 * time.Second):
		t.Fatal("outbound Staff answer receipt did not finish")
	}
	if provisioningErr != nil || receiptErr != nil {
		t.Fatalf("concurrent release provisioning and receipt projection: provisioning=%v receipt=%v",
			provisioningErr, receiptErr)
	}

	var receiptState, projectionCode, staffState string
	var attempts int
	if err := pool.QueryRow(ctx, `
		SELECT receipt.state, receipt.projection_attempts,
			COALESCE(receipt.projection_error_code, ''), staff.state
		FROM human_calling_provider_receipts receipt
		JOIN human_calling_call_legs staff
			ON staff.call_id = receipt.call_id AND staff.role = 'STAFF'
		WHERE receipt.event_id = $1
	`, prefix+"-answered").Scan(
		&receiptState, &attempts, &projectionCode, &staffState,
	); err != nil {
		t.Fatal(err)
	}
	if receiptState != string(humancalling.ReceiptApplied) || attempts != 1 ||
		projectionCode != "" || staffState != "BRIDGE_PENDING" ||
		provider.count(humancalling.CommandDialOutboundStaff) != 1 {
		t.Fatalf(
			"release/receipt convergence = receipt:%s attempts:%d code:%s staff:%s provider effects:%d",
			receiptState, attempts, projectionCode, staffState,
			provider.count(humancalling.CommandDialOutboundStaff),
		)
	}
}

func TestRejectedHandoffTerminalizesExactProviderLegLifecycleReceipts(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(
		pool,
		nil,
		nil,
		humancalling.Config{
			CallControlID:     "expected-connection",
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return now },
	)
	receive := func(raw []byte) humancalling.WebhookReceipt {
		t.Helper()
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(timestamp+"|"), raw...),
		))
		receipt, err := calling.ReceiveWebhook(
			context.Background(), raw, timestamp, signature,
		)
		if err != nil {
			t.Fatalf("receive rejected-leg webhook: %v", err)
		}
		return receipt
	}
	process := func(eventType string) {
		t.Helper()
		processed, err := calling.ProcessNextReceipt(context.Background())
		if err != nil || !processed {
			t.Fatalf("process %s: processed=%t err=%v", eventType, processed, err)
		}
	}
	raw := func(eventID, eventType, sessionID string) []byte {
		t.Helper()
		return []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"%s","id":"%s","occurred_at":"%s","payload":{"connection_id":"rejected-connection","call_control_id":"rejected-control","call_leg_id":"rejected-leg","call_session_id":"%s"}}}`,
			eventType,
			eventID,
			now.Format(time.RFC3339Nano),
			sessionID,
		))
	}

	initiated := raw("rejected-initiated", "call.initiated", "rejected-session")
	receive(initiated)
	process("call.initiated")
	if receipt := receive(initiated); receipt.State != humancalling.ReceiptFailed {
		t.Fatalf("rejected initiation receipt state = %s, want FAILED", receipt.State)
	}

	for _, eventType := range []string{"call.answered", "call.bridged", "call.hangup"} {
		event := raw(
			"rejected-"+strings.TrimPrefix(eventType, "call."),
			eventType,
			"rejected-session",
		)
		receive(event)
		process(eventType)
		if receipt := receive(event); receipt.State != humancalling.ReceiptFailed {
			t.Fatalf("%s receipt state = %s, want FAILED", eventType, receipt.State)
		}
	}

	unrelated := raw("unrelated-answered", "call.answered", "other-session")
	receive(unrelated)
	process("unrelated call.answered")
	if receipt := receive(unrelated); receipt.State != humancalling.ReceiptPending {
		t.Fatalf("unrelated receipt state = %s, want PENDING", receipt.State)
	}
}

func TestRejectedHandoffFinalizedDuringRolloutTerminalizesLifecycleReceipt(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 11, 12, 30, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(
		pool,
		nil,
		nil,
		humancalling.Config{
			CallControlID:     "expected-connection",
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return now },
	)
	receive := func(eventID, eventType string) humancalling.WebhookReceipt {
		t.Helper()
		raw := []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"%s","id":"%s","occurred_at":"%s","payload":{"connection_id":"rollout-connection","call_control_id":"rollout-control","call_leg_id":"rollout-leg","call_session_id":"rollout-session"}}}`,
			eventType,
			eventID,
			now.Format(time.RFC3339Nano),
		))
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(timestamp+"|"), raw...),
		))
		receipt, err := calling.ReceiveWebhook(
			context.Background(), raw, timestamp, signature,
		)
		if err != nil {
			t.Fatalf("receive %s: %v", eventType, err)
		}
		return receipt
	}

	receive("rollout-initiated", "call.initiated")
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_receipts
		SET state = 'FAILED', projection_attempts = 1,
			projection_error_code = 'HANDOFF_REJECTED',
			last_attempt_at = $2, projected_at = $2
		WHERE event_id = $1
	`, "rollout-initiated", now); err != nil {
		t.Fatalf("simulate old worker rejection after migration: %v", err)
	}

	receive("rollout-answered", "call.answered")
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process rollout-overlap answer: processed=%t err=%v", processed, err)
	}
	if receipt := receive("rollout-answered", "call.answered"); receipt.State != humancalling.ReceiptFailed {
		t.Fatalf("rollout-overlap answer state = %s, want FAILED", receipt.State)
	}
}

func TestUnrelatedProviderReceiptStopsRetryingAfterOneDay(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(
		pool,
		nil,
		nil,
		humancalling.Config{
			CallControlID:     "expected-connection",
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return now },
	)
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.answered","id":"expired-related-fact","occurred_at":"%s","payload":{"connection_id":"expected-connection","call_control_id":"orphan-control","call_leg_id":"orphan-leg","call_session_id":"orphan-session"}}}`,
		now.Add(-25*time.Hour).Format(time.RFC3339Nano),
	))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	if _, err := calling.ReceiveWebhook(
		context.Background(), raw, timestamp, signature,
	); err != nil {
		t.Fatalf("receive unrelated provider receipt: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_receipts
		SET received_at = $2, projection_attempts = 10,
			last_attempt_at = $2, next_attempt_at = $3
		WHERE event_id = $1
	`, "expired-related-fact", now.Add(-25*time.Hour), now); err != nil {
		t.Fatalf("age unrelated provider receipt: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process unrelated provider receipt: processed=%t err=%v", processed, err)
	}

	var state, errorCode string
	var quarantinedAt *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT state, COALESCE(projection_error_code, ''), quarantined_at
		FROM human_calling_provider_receipts
		WHERE event_id = $1
	`, "expired-related-fact").Scan(&state, &errorCode, &quarantinedAt); err != nil {
		t.Fatal(err)
	}
	if state != string(humancalling.ReceiptQuarantined) ||
		errorCode != "RELATED_FACT_TIMEOUT" || quarantinedAt == nil {
		t.Fatalf("expired unrelated receipt = state:%s error:%s quarantine:%v",
			state, errorCode, quarantinedAt)
	}
}

func TestUnrelatedHangupWaitsForRelatedFact(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(
		pool,
		nil,
		nil,
		humancalling.Config{
			CallControlID:     "expected-connection",
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return now },
	)
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.hangup","id":"orphan-hangup","occurred_at":"%s","payload":{"connection_id":"expected-connection","call_control_id":"orphan-control","call_leg_id":"orphan-leg","call_session_id":"orphan-session","hangup_cause":"normal_clearing","hangup_source":"callee"}}}`,
		now.Format(time.RFC3339Nano),
	))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	if _, err := calling.ReceiveWebhook(
		context.Background(), raw, timestamp, signature,
	); err != nil {
		t.Fatalf("receive unrelated Hangup: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process unrelated Hangup: processed=%t err=%v", processed, err)
	}

	var state, errorCode string
	var attempts int
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, COALESCE(projection_error_code, '')
		FROM human_calling_provider_receipts
		WHERE event_id = $1
	`, "orphan-hangup").Scan(&state, &attempts, &errorCode); err != nil {
		t.Fatal(err)
	}
	if state != string(humancalling.ReceiptPending) || attempts != 1 ||
		errorCode != "WAITING_FOR_RELATED_FACT" {
		t.Fatalf("unrelated Hangup = state:%s attempts:%d error:%s",
			state, attempts, errorCode)
	}
}

func TestChildReceiptWakesWhenParentAttachesRelatedCall(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 14, 13, 0, 0, 0, time.UTC)
	prefix := "child-before-parent"
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(t, accessModule, now, prefix, 1)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var metrics bytes.Buffer
	calling := humancalling.New(
		pool,
		accessModule,
		nil,
		humancalling.Config{
			HandoffSIPDomain:  "synthetic.sip.telnyx.com",
			CallControlID:     "expected-connection",
			WebhookPublicKeys: [][]byte{publicKey},
			Observer: observability.NewLogger(
				observability.RuntimeWorker,
				"worker-related-receipt-test",
				slog.New(slog.NewJSONHandler(&metrics, nil)),
			),
		},
		func() time.Time { return now },
	)
	callerPhone := "+15555550100"
	if _, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject: prefix + "-service", PracticeID: authorization.Practice.ID,
		},
		LocationID: authorization.Locations[0].ID, SourceCallID: prefix + "-source",
		IdempotencyKey: prefix, Contact: humancalling.ContactContext{Phone: callerPhone},
	}); err != nil {
		t.Fatalf("create valid handoff: %v", err)
	}
	raw := func(eventID, eventType string) []byte {
		t.Helper()
		return []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"%s","id":"%s","occurred_at":"%s","payload":{"connection_id":"expected-connection","call_control_id":"%s-control","call_leg_id":"%s-leg","call_session_id":"%s-session","from":"%s","to":"+14843989071"}}}`,
			eventType, eventID, now.Format(time.RFC3339Nano), prefix, prefix, prefix,
			callerPhone,
		))
	}
	receive := func(body []byte) {
		t.Helper()
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(timestamp+"|"), body...),
		))
		if _, err := calling.ReceiveWebhook(
			context.Background(), body, timestamp, signature,
		); err != nil {
			t.Fatalf("receive provider receipt: %v", err)
		}
	}
	process := func(eventType string) bool {
		t.Helper()
		processed, err := calling.ProcessNextReceipt(context.Background())
		if err != nil {
			t.Fatalf("process %s: %v", eventType, err)
		}
		return processed
	}

	receive(raw(prefix+"-answered", "call.answered"))
	if !process("child call.answered") {
		t.Fatal("child call.answered was not claimed")
	}
	var childState, childError string
	var childAttempts int
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, COALESCE(projection_error_code, '')
		FROM human_calling_provider_receipts WHERE event_id = $1
	`, prefix+"-answered").Scan(&childState, &childAttempts, &childError); err != nil {
		t.Fatal(err)
	}
	if childState != string(humancalling.ReceiptPending) || childAttempts != 1 ||
		childError != "WAITING_FOR_RELATED_FACT" {
		t.Fatalf("child before parent = state:%s attempts:%d error:%s",
			childState, childAttempts, childError)
	}
	if !strings.Contains(metrics.String(), `"outcome":"related_fact"`) {
		t.Fatalf("related-fact classification metric = %s", metrics.String())
	}

	receive(raw(prefix+"-initiated", "call.initiated"))
	if !process("parent call.initiated") {
		t.Fatal("parent call.initiated was not claimed")
	}
	if !process("woken child call.answered") {
		t.Fatal("related child was not woken when parent attached the Call")
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, COALESCE(projection_error_code, '')
		FROM human_calling_provider_receipts WHERE event_id = $1
	`, prefix+"-answered").Scan(&childState, &childAttempts, &childError); err != nil {
		t.Fatal(err)
	}
	if childState != string(humancalling.ReceiptApplied) || childAttempts != 2 || childError != "" {
		t.Fatalf("child after parent = state:%s attempts:%d error:%s",
			childState, childAttempts, childError)
	}
}

func TestProviderProjectionConflictRemainsRetryable(t *testing.T) {
	now := time.Date(2026, time.August, 14, 13, 30, 0, 0, time.UTC)
	currentTime := now
	pool := testdb.Open(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(
		pool,
		nil,
		nil,
		humancalling.Config{
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return currentTime },
	)
	clientState := base64.StdEncoding.EncodeToString([]byte(
		`{"v":2,"call":"00000000-0000-0000-0000-000000000931","call_leg":"00000000-0000-0000-0000-000000000932","role":"CALLER","kind":"not_ring_window"}`,
	))
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.playback.ended","id":"projection-conflict","occurred_at":"%s","payload":{"call_control_id":"conflict-control","call_leg_id":"conflict-leg","call_session_id":"conflict-session","client_state":"%s","status":"completed"}}}`,
		now.Format(time.RFC3339Nano), clientState,
	))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	if _, err := calling.ReceiveWebhook(
		context.Background(), raw, timestamp, signature,
	); err != nil {
		t.Fatalf("receive projection conflict: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process projection conflict: processed=%t err=%v", processed, err)
	}
	var state, errorCode string
	var attempts int
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, COALESCE(projection_error_code, '')
		FROM human_calling_provider_receipts
		WHERE event_id = 'projection-conflict'
	`).Scan(&state, &attempts, &errorCode); err != nil {
		t.Fatal(err)
	}
	if state != string(humancalling.ReceiptPending) || attempts != 1 ||
		errorCode != "PROJECTION_APPLY_FACT_CONFLICT" {
		t.Fatalf("projection conflict receipt = state:%s attempts:%d error:%s",
			state, attempts, errorCode)
	}
	for attempts = 2; attempts <= 10; attempts++ {
		currentTime = currentTime.Add(time.Hour)
		if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
			t.Fatalf("retry projection conflict attempt %d: processed=%t err=%v",
				attempts, processed, err)
		}
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, COALESCE(projection_error_code, '')
		FROM human_calling_provider_receipts
		WHERE event_id = 'projection-conflict'
	`).Scan(&state, &attempts, &errorCode); err != nil {
		t.Fatal(err)
	}
	if state != string(humancalling.ReceiptQuarantined) || attempts != 10 ||
		errorCode != "PROJECTION_APPLY_FACT_CONFLICT" {
		t.Fatalf("bounded projection conflict receipt = state:%s attempts:%d error:%s",
			state, attempts, errorCode)
	}
}

func TestLateAnswerForTerminalCleanupFailedLegIsObsolete(t *testing.T) {
	now := time.Date(2026, time.August, 14, 13, 45, 0, 0, time.UTC)
	pool := testdb.Open(t)
	ctx := context.Background()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, staff := provisionConcurrentStaff(
		t, accessModule, now, "terminal-late-answer", 1,
	)
	provider := &recordingProvider{}
	calling := humancalling.New(
		pool,
		accessModule,
		provider,
		humancalling.Config{
			StaffSIPDomain:         "sip.telnyx.com",
			CallControlID:          "staff-call-control-connection",
			CredentialConnectionID: "staff-credential-connection",
			WebhookPublicKeys:      [][]byte{publicKey},
		},
		func() time.Time { return now },
	)
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, "terminal-late-answer-browser")
	if err := calling.ProvisionLocationVoices(ctx, []humancalling.LocationVoiceProvision{{
		PracticeKey: "terminal-late-answer-practice",
		LocationKey: "terminal-late-answer-location",
		Number:      "+14843336938", Enabled: true,
	}}); err != nil {
		t.Fatalf("provision outbound caller ID: %v", err)
	}
	provider.actionErrors = map[humancalling.CommandAction][]error{
		humancalling.CommandDialOutboundStaff: {
			fmt.Errorf("%w: synthetic rejected Dial", humancalling.ErrDefinitiveProviderFailure),
		},
	}
	call, err := calling.StartOutboundCall(ctx, humancalling.StartOutboundCallCommand{
		Identity: staff[0], SessionID: "terminal-late-answer-browser-1",
		IdempotencyKey: "terminal-late-answer",
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "+15555550123",
	})
	if err != nil {
		t.Fatalf("start outbound Call: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(ctx); !processed || err != nil {
		t.Fatalf("reject outbound Dial: processed=%t err=%v", processed, err)
	}
	var callLegID, legState, legError string
	if err := pool.QueryRow(ctx, `
		SELECT id::text, state, COALESCE(error_code, '')
		FROM human_calling_call_legs
		WHERE call_id = $1 AND role = 'CALLER'
	`, call.ID).Scan(&callLegID, &legState, &legError); err != nil {
		t.Fatalf("read terminal caller CallLeg: %v", err)
	}
	if legState != "FAILED" || legError != "CALL_TERMINATED_BEFORE_PROVIDER_START" {
		t.Fatalf("terminal caller cleanup = state:%s error:%s", legState, legError)
	}
	clientStateJSON := []byte(fmt.Sprintf(
		`{"v":2,"call":"%s","call_leg":"%s","role":"CALLER","kind":"answer"}`,
		call.ID, callLegID,
	))
	clientState := base64.StdEncoding.EncodeToString(clientStateJSON)
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.answered","id":"terminal-late-answer","occurred_at":"%s","payload":{"connection_id":"late-connection","call_control_id":"late-control","call_leg_id":"late-leg","call_session_id":"late-session","client_state":"%s"}}}`,
		now.Format(time.RFC3339Nano), clientState,
	))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	if _, err := calling.ReceiveWebhook(ctx, raw, timestamp, signature); err != nil {
		t.Fatalf("receive terminal late answer: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(ctx); err != nil || !processed {
		t.Fatalf("process terminal late answer: processed=%t err=%v", processed, err)
	}
	var receiptState, errorCode string
	var attempts int
	if err := pool.QueryRow(ctx, `
		SELECT state, projection_attempts, COALESCE(projection_error_code, '')
		FROM human_calling_provider_receipts
		WHERE event_id = 'terminal-late-answer'
	`).Scan(&receiptState, &attempts, &errorCode); err != nil {
		t.Fatal(err)
	}
	var providerIdentityCount int
	if err := pool.QueryRow(ctx, `
		SELECT state, COALESCE(error_code, ''),
			(CASE WHEN provider_connection_id IS NOT NULL THEN 1 ELSE 0 END) +
			(CASE WHEN provider_call_control_id IS NOT NULL THEN 1 ELSE 0 END) +
			(CASE WHEN provider_call_leg_id IS NOT NULL THEN 1 ELSE 0 END) +
			(CASE WHEN provider_call_session_id IS NOT NULL THEN 1 ELSE 0 END)
		FROM human_calling_call_legs WHERE id = $1
	`, callLegID).Scan(&legState, &legError, &providerIdentityCount); err != nil {
		t.Fatal(err)
	}
	if receiptState != string(humancalling.ReceiptFailed) || attempts != 1 ||
		errorCode != "TERMINAL_OR_OBSOLETE_PROVIDER_FACT" {
		t.Fatalf("terminal late answer receipt = state:%s attempts:%d error:%s",
			receiptState, attempts, errorCode)
	}
	if legState != "FAILED" || legError != "CALL_TERMINATED_BEFORE_PROVIDER_START" ||
		providerIdentityCount != 0 {
		t.Fatalf("terminal late answer revived leg = state:%s error:%s provider identities:%d",
			legState, legError, providerIdentityCount)
	}
}

func TestTransientProjectionFailureRetriesThenStopsAtAttemptBound(t *testing.T) {
	baseTime := time.Date(2026, time.August, 14, 14, 0, 0, 0, time.UTC)
	prefix := "transient-receipt-projection"
	provider := &recordingProvider{}
	pool, _, caller, _ := prepareInboundFanout(t, baseTime, prefix, provider, 1)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	currentTime := baseTime.Add(2 * time.Second)
	calling := humancalling.New(
		pool,
		access.New(pool, func() time.Time { return currentTime }),
		provider,
		humancalling.Config{
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return currentTime },
	)
	if _, err := pool.Exec(context.Background(), `
		CREATE FUNCTION fail_provider_fact_projection() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic serialization failure' USING ERRCODE = '40001';
		END
		$$;
		CREATE TRIGGER fail_provider_fact_projection
		BEFORE INSERT ON human_calling_projected_facts
		FOR EACH ROW EXECUTE FUNCTION fail_provider_fact_projection()
	`); err != nil {
		t.Fatalf("install transient projection failure: %v", err)
	}
	dropFailure := func() {
		t.Helper()
		if _, err := pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS fail_provider_fact_projection
				ON human_calling_projected_facts;
			DROP FUNCTION IF EXISTS fail_provider_fact_projection()
		`); err != nil {
			t.Fatalf("remove transient projection failure: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS fail_provider_fact_projection
				ON human_calling_projected_facts;
			DROP FUNCTION IF EXISTS fail_provider_fact_projection()
		`)
	})
	receiveHangup := func(eventID string) []byte {
		t.Helper()
		body := []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"call.hangup","id":"%s","occurred_at":"%s","payload":{"call_control_id":"%s","call_leg_id":"%s","call_session_id":"%s","hangup_cause":"normal_clearing"}}}`,
			eventID, currentTime.Format(time.RFC3339Nano), caller.CallControlID,
			caller.CallLegID, caller.CallSessionID,
		))
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(timestamp+"|"), body...),
		))
		if _, err := calling.ReceiveWebhook(
			context.Background(), body, timestamp, signature,
		); err != nil {
			t.Fatalf("receive transient provider receipt: %v", err)
		}
		return body
	}
	process := func() {
		t.Helper()
		processed, err := calling.ProcessNextReceipt(context.Background())
		if err != nil || !processed {
			t.Fatalf("process transient provider receipt: processed=%t err=%v", processed, err)
		}
	}
	readReceipt := func(eventID string) (string, int, string) {
		t.Helper()
		var state, errorCode string
		var attempts int
		if err := pool.QueryRow(context.Background(), `
			SELECT state, projection_attempts, COALESCE(projection_error_code, '')
			FROM human_calling_provider_receipts WHERE event_id = $1
		`, eventID).Scan(&state, &attempts, &errorCode); err != nil {
			t.Fatal(err)
		}
		return state, attempts, errorCode
	}

	receiveHangup(prefix + "-recovers")
	process()
	if state, attempts, code := readReceipt(prefix + "-recovers"); state != string(humancalling.ReceiptPending) || attempts != 1 ||
		code != "PROJECTION_APPLY_FACT_RETRY" {
		t.Fatalf("transient receipt first attempt = state:%s attempts:%d error:%s",
			state, attempts, code)
	}
	dropFailure()
	currentTime = currentTime.Add(time.Second)
	process()
	if state, attempts, code := readReceipt(prefix + "-recovers"); state != string(humancalling.ReceiptApplied) || attempts != 2 || code != "" {
		t.Fatalf("recovered transient receipt = state:%s attempts:%d error:%s",
			state, attempts, code)
	}

	if _, err := pool.Exec(context.Background(), `
		CREATE FUNCTION fail_provider_fact_projection() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic serialization failure' USING ERRCODE = '40001';
		END
		$$;
		CREATE TRIGGER fail_provider_fact_projection
		BEFORE INSERT ON human_calling_projected_facts
		FOR EACH ROW EXECUTE FUNCTION fail_provider_fact_projection()
	`); err != nil {
		t.Fatalf("restore transient projection failure: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_receipts (
			event_id, event_type, occurred_at, received_at,
			signature_timestamp, raw_body, state, projection_attempts,
			projection_error_code, last_attempt_at, next_attempt_at,
			projected_at, quarantined_at
		) VALUES ($1, 'call.speak.ended', $2, $2, 1, $3,
			'QUARANTINED', 10, 'PROJECTION_RETRY_EXHAUSTED', $2, $2, $2, $2)
	`, prefix+"-historical", currentTime, []byte("historical-raw-evidence")); err != nil {
		t.Fatalf("seed historical quarantine: %v", err)
	}
	boundedRaw := receiveHangup(prefix + "-bounded")
	for attempt := 1; attempt <= 10; attempt++ {
		process()
		if attempt < 10 {
			currentTime = currentTime.Add(2 * time.Minute)
		}
	}
	if state, attempts, code := readReceipt(prefix + "-bounded"); state != string(humancalling.ReceiptQuarantined) || attempts != 10 ||
		code != "PROJECTION_APPLY_FACT_RETRY" {
		t.Fatalf("bounded transient receipt = state:%s attempts:%d error:%s",
			state, attempts, code)
	}
	var historicalState, historicalCode string
	var historicalAttempts int
	var historicalRaw, boundedStoredRaw []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, projection_error_code, raw_body
		FROM human_calling_provider_receipts WHERE event_id = $1
	`, prefix+"-historical").Scan(
		&historicalState, &historicalAttempts, &historicalCode, &historicalRaw,
	); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT raw_body FROM human_calling_provider_receipts WHERE event_id = $1
	`, prefix+"-bounded").Scan(&boundedStoredRaw); err != nil {
		t.Fatal(err)
	}
	if historicalState != string(humancalling.ReceiptQuarantined) ||
		historicalAttempts != 10 || historicalCode != "PROJECTION_RETRY_EXHAUSTED" ||
		string(historicalRaw) != "historical-raw-evidence" {
		t.Fatalf("historical quarantine changed = state:%s attempts:%d error:%s raw:%q",
			historicalState, historicalAttempts, historicalCode, historicalRaw)
	}
	if string(boundedStoredRaw) != string(boundedRaw) {
		t.Fatal("bounded transient quarantine did not preserve raw receipt evidence")
	}
}

func TestValidHandoffQuickHangupReceiptsRemainApplied(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 11, 13, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(
		t, accessModule, now, "receipt-quick-hangup", 1,
	)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(
		pool,
		accessModule,
		nil,
		humancalling.Config{
			HandoffSIPDomain:  "synthetic.sip.telnyx.com",
			CallControlID:     "expected-connection",
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return now },
	)
	callerPhone := "+" + "15555550100"
	if _, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject: "receipt-quick-hangup-service", PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "receipt-quick-hangup-source",
		IdempotencyKey: "receipt-quick-hangup",
		Contact:        humancalling.ContactContext{Phone: callerPhone},
	}); err != nil {
		t.Fatalf("create valid handoff: %v", err)
	}
	receiveAndProcess := func(eventID, eventType string) humancalling.WebhookReceipt {
		t.Helper()
		raw := []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"%s","id":"%s","occurred_at":"%s","payload":{"connection_id":"expected-connection","call_control_id":"quick-control","call_leg_id":"quick-leg","call_session_id":"quick-session","from":"%s","to":"%s","hangup_cause":"normal_clearing"}}}`,
			eventType,
			eventID,
			now.Format(time.RFC3339Nano),
			callerPhone,
			"+"+"14843989071",
		))
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(timestamp+"|"), raw...),
		))
		if _, err := calling.ReceiveWebhook(
			context.Background(), raw, timestamp, signature,
		); err != nil {
			t.Fatalf("receive %s: %v", eventType, err)
		}
		if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
			t.Fatalf("process %s: processed=%t err=%v", eventType, processed, err)
		}
		receipt, err := calling.ReceiveWebhook(
			context.Background(), raw, timestamp, signature,
		)
		if err != nil {
			t.Fatalf("read %s receipt state: %v", eventType, err)
		}
		return receipt
	}

	if receipt := receiveAndProcess("quick-initiated", "call.initiated"); receipt.State != humancalling.ReceiptApplied {
		t.Fatalf("valid initiation receipt state = %s, want APPLIED", receipt.State)
	}
	now = now.Add(time.Second)
	if receipt := receiveAndProcess("quick-hangup", "call.hangup"); receipt.State != humancalling.ReceiptApplied {
		t.Fatalf("valid quick hangup receipt state = %s, want APPLIED", receipt.State)
	}
}

func TestTerminalCallSpeakEndedReceiptStopsWithoutSlowRetry(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	prefix := "terminal-before-speak-ended"
	provider := &recordingProvider{}
	pool, setupCalling, caller, _ := prepareInboundFanout(
		t, now, prefix, provider, 1,
	)
	processAllCommands(t, setupCalling)
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := setupCalling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-ring-ended", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(20 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatalf("complete ring window: %v", err)
	}
	processAllCommands(t, setupCalling)
	speak := provider.last(humancalling.CommandSpeakVoicemail)
	speakState, _ := speak.Payload["client_state"].(string)
	if speakState == "" {
		t.Fatal("voicemail Speak command omitted client state")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	currentTime := now.Add(21 * time.Second)
	var metrics bytes.Buffer
	calling := humancalling.New(
		pool,
		access.New(pool, func() time.Time { return currentTime }),
		provider,
		humancalling.Config{
			CallControlID:     "staff-call-control-connection",
			WebhookPublicKeys: [][]byte{publicKey},
			Observer: observability.NewLogger(
				observability.RuntimeWorker,
				"worker-terminal-receipt-test",
				slog.New(slog.NewJSONHandler(&metrics, nil)),
			),
		},
		func() time.Time { return currentTime },
	)
	receive := func(raw []byte) humancalling.WebhookReceipt {
		t.Helper()
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(timestamp+"|"), raw...),
		))
		receipt, err := calling.ReceiveWebhook(
			context.Background(), raw, timestamp, signature,
		)
		if err != nil {
			t.Fatalf("receive provider receipt: %v", err)
		}
		return receipt
	}
	process := func(eventType string) {
		t.Helper()
		processed, err := calling.ProcessNextReceipt(context.Background())
		if err != nil || !processed {
			t.Fatalf("process %s: processed=%t err=%v", eventType, processed, err)
		}
	}

	hangup := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.hangup","id":"%s","occurred_at":"%s","payload":{"connection_id":"staff-call-control-connection","call_control_id":"%s","call_leg_id":"%s","call_session_id":"%s","hangup_cause":"normal_clearing"}}}`,
		prefix+"-hangup", currentTime.Format(time.RFC3339Nano),
		caller.CallControlID, caller.CallLegID, caller.CallSessionID,
	))
	receive(hangup)
	process("call.hangup")

	currentTime = currentTime.Add(time.Second)
	speakEnded := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.speak.ended","id":"%s","occurred_at":"%s","payload":{"connection_id":"staff-call-control-connection","call_control_id":"%s","call_leg_id":"%s","call_session_id":"%s","client_state":"%s","status":"completed"}}}`,
		prefix+"-speak-ended", currentTime.Format(time.RFC3339Nano),
		caller.CallControlID, caller.CallLegID, caller.CallSessionID, speakState,
	))
	receive(speakEnded)
	process("call.speak.ended")

	var state, errorCode string
	var attempts int
	var rawBody []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, COALESCE(projection_error_code, ''), raw_body
		FROM human_calling_provider_receipts
		WHERE event_id = $1
	`, prefix+"-speak-ended").Scan(&state, &attempts, &errorCode, &rawBody); err != nil {
		t.Fatal(err)
	}
	if state != string(humancalling.ReceiptFailed) || attempts != 1 ||
		errorCode != "TERMINAL_OR_OBSOLETE_PROVIDER_FACT" {
		t.Fatalf("terminal Speak receipt = state:%s attempts:%d error:%s",
			state, attempts, errorCode)
	}
	if string(rawBody) != string(speakEnded) {
		t.Fatal("terminal Speak classification did not preserve raw receipt evidence")
	}
	if !strings.Contains(metrics.String(), `"outcome":"obsolete"`) {
		t.Fatalf("terminal Speak classification metric = %s", metrics.String())
	}
	if duplicate := receive(speakEnded); duplicate.State != humancalling.ReceiptFailed ||
		!duplicate.Duplicate || duplicate.DuplicateCount != 1 {
		t.Fatalf("terminal Speak duplicate = %#v", duplicate)
	}
}

func TestTerminalCallRecordingReceiptsDistinguishConflictFromLateEvidence(t *testing.T) {
	now := time.Date(2026, time.August, 14, 15, 0, 0, 0, time.UTC)
	prefix := "terminal-before-recording"
	provider := &recordingProvider{}
	pool, setupCalling, caller, _ := prepareInboundFanout(t, now, prefix, provider, 1)
	processAllCommands(t, setupCalling)
	ringState, _ := provider.last(humancalling.CommandStartRingWindow).
		Payload["client_state"].(string)
	if err := setupCalling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-ring-ended", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(20 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatalf("complete ring window: %v", err)
	}
	processAllCommands(t, setupCalling)
	speakState, _ := provider.last(humancalling.CommandSpeakVoicemail).
		Payload["client_state"].(string)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	currentTime := now.Add(21 * time.Second)
	calling := humancalling.New(
		pool,
		access.New(pool, func() time.Time { return currentTime }),
		provider,
		humancalling.Config{
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return currentTime },
	)
	receiveAndProcess := func(eventID, eventType string, payload map[string]any) {
		t.Helper()
		envelope := map[string]any{"data": map[string]any{
			"record_type": "event", "event_type": eventType, "id": eventID,
			"occurred_at": currentTime.Format(time.RFC3339Nano), "payload": payload,
		}}
		body, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(timestamp+"|"), body...),
		))
		if _, err := calling.ReceiveWebhook(
			context.Background(), body, timestamp, signature,
		); err != nil {
			t.Fatalf("receive %s: %v", eventType, err)
		}
		processed, err := calling.ProcessNextReceipt(context.Background())
		if err != nil || !processed {
			t.Fatalf("process %s: processed=%t err=%v", eventType, processed, err)
		}
	}
	identity := func(clientState, sessionID string) map[string]any {
		return map[string]any{
			"call_control_id": caller.CallControlID,
			"call_leg_id":     caller.CallLegID,
			"call_session_id": sessionID,
			"client_state":    clientState,
		}
	}
	read := func(eventID string) (string, int, string) {
		t.Helper()
		var state, code string
		var attempts int
		if err := pool.QueryRow(context.Background(), `
			SELECT state, projection_attempts, COALESCE(projection_error_code, '')
			FROM human_calling_provider_receipts WHERE event_id = $1
		`, eventID).Scan(&state, &attempts, &code); err != nil {
			t.Fatal(err)
		}
		return state, attempts, code
	}

	speakPayload := identity(speakState, caller.CallSessionID)
	speakPayload["status"] = "completed"
	receiveAndProcess(prefix+"-speak-ended", "call.speak.ended", speakPayload)
	processAllCommands(t, calling)
	recordState, _ := provider.last(humancalling.CommandStartVoicemailRecording).
		Payload["client_state"].(string)
	currentTime = currentTime.Add(time.Second)
	hangupPayload := identity("", caller.CallSessionID)
	hangupPayload["hangup_cause"] = "normal_clearing"
	receiveAndProcess(prefix+"-hangup", "call.hangup", hangupPayload)

	currentTime = currentTime.Add(time.Second)
	receiveAndProcess(
		prefix+"-recording-wrong-session",
		"call.recording.error",
		identity(recordState, "wrong-session"),
	)
	if state, attempts, code := read(prefix + "-recording-wrong-session"); state != string(humancalling.ReceiptPending) || attempts != 1 ||
		code != "PROJECTION_APPLY_FACT_CONFLICT" {
		t.Fatalf("conflicting recording receipt = state:%s attempts:%d error:%s",
			state, attempts, code)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_receipts
		SET next_attempt_at = $2
		WHERE event_id = $1
	`, prefix+"-recording-wrong-session", currentTime.Add(time.Hour)); err != nil {
		t.Fatalf("defer conflicting recording receipt retry: %v", err)
	}

	currentTime = currentTime.Add(time.Second)
	recordingPayload := identity(recordState, caller.CallSessionID)
	recordingPayload["recording_id"] = prefix + "-recording"
	recordingPayload["recording_started_at"] = now.Add(21 * time.Second).Format(time.RFC3339Nano)
	recordingPayload["recording_ended_at"] = currentTime.Format(time.RFC3339Nano)
	receiveAndProcess(prefix+"-recording-saved", "call.recording.saved", recordingPayload)
	if state, attempts, code := read(prefix + "-recording-saved"); state != string(humancalling.ReceiptApplied) || attempts != 1 || code != "" {
		t.Fatalf("late recording evidence = state:%s attempts:%d error:%s",
			state, attempts, code)
	}
	var terminalOutcome, audioState string
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, voicemail.audio_state
		FROM human_calling_calls call
		JOIN human_calling_voicemails voicemail ON voicemail.call_id = call.id
		WHERE call.id = (
			SELECT call_id FROM human_calling_provider_receipts WHERE event_id = $1
		)
	`, prefix+"-recording-saved").Scan(&terminalOutcome, &audioState); err != nil {
		t.Fatal(err)
	}
	if terminalOutcome != "VOICEMAIL" || audioState != "READY" {
		t.Fatalf("late recording evidence outcome = terminal:%s audio:%s",
			terminalOutcome, audioState)
	}
}

func TestVoicemailRecordingSavedAfterRoutingFailureWithCompletedTaskAppliesImmediately(t *testing.T) {
	now := time.Date(2026, time.August, 19, 17, 28, 0, 0, time.UTC)
	prefix := "routing-failure-voicemail-recording"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control",
		CallLegID:     prefix + "-staff-leg",
	}}}
	pool, setupCalling, caller, staff := prepareInboundFanout(t, now, prefix, provider, 1)
	processAllCommands(t, setupCalling)

	withKind := func(encoded, kind string) string {
		t.Helper()
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode Caller client state: %v", err)
		}
		var state map[string]any
		if err := json.Unmarshal(decoded, &state); err != nil {
			t.Fatalf("parse Caller client state: %v", err)
		}
		state["kind"] = kind
		updated, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("encode Caller client state: %v", err)
		}
		return base64.StdEncoding.EncodeToString(updated)
	}

	answerState, _ := provider.last(humancalling.CommandAnswerCaller).
		Payload["client_state"].(string)
	unownedLaterState := withKind(answerState, "routing_failure")
	if err := setupCalling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:            prefix + "-unowned-recording",
		Type:               humancalling.FactRecordingSaved,
		OccurredAt:         now.Add(2 * time.Second),
		ConnectionID:       caller.ConnectionID,
		CallControlID:      caller.CallControlID,
		CallLegID:          caller.CallLegID,
		CallSessionID:      caller.CallSessionID,
		ClientState:        unownedLaterState,
		RecordingID:        prefix + "-unowned-recording",
		RecordingStartedAt: now,
		RecordingEndedAt:   now.Add(2 * time.Second),
	}); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("unowned Caller recording error = %v", err)
	}

	ringState, _ := provider.last(humancalling.CommandStartRingWindow).
		Payload["client_state"].(string)
	if err := setupCalling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-ring-ended", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(20 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatalf("complete ring window: %v", err)
	}
	processAllCommands(t, setupCalling)
	speakState, _ := provider.last(humancalling.CommandSpeakVoicemail).
		Payload["client_state"].(string)
	if err := setupCalling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-speak-ended", Type: humancalling.FactSpeakEnded,
		OccurredAt: now.Add(21 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: speakState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatalf("complete voicemail greeting: %v", err)
	}
	processAllCommands(t, setupCalling)
	record := provider.last(humancalling.CommandStartVoicemailRecording)
	if record.ID == "" {
		t.Fatal("voicemail recording command was not sent")
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET created_at = $2, updated_at = $2
		WHERE id = $1
	`, record.ID, now); err != nil {
		t.Fatalf("pin voicemail recording command timing: %v", err)
	}
	provider.mu.Lock()
	provider.observations = append(provider.observations, humancalling.ProviderCallObservation{
		Active:        true,
		CallControlID: caller.CallControlID,
		CallLegID:     caller.CallLegID,
		CallSessionID: caller.CallSessionID,
	})
	provider.mu.Unlock()
	reconciliationTime := now.Add(80 * time.Second)
	reconciliationCalling := humancalling.New(
		pool,
		access.New(pool, func() time.Time { return reconciliationTime }),
		provider,
		humancalling.Config{},
		func() time.Time { return reconciliationTime },
	)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $1 WHERE role <> 'CALLER'
	`, reconciliationTime); err != nil {
		t.Fatalf("keep unrelated CallLegs out of reconciliation: %v", err)
	}
	if reconciled, err := reconciliationCalling.MaintainOutgoingCallLegs(context.Background()); err != nil || !reconciled {
		t.Fatalf("reconcile absent voicemail recording event = %t, %v", reconciled, err)
	}
	processAllCommands(t, reconciliationCalling)

	var routingFailureState string
	for _, command := range provider.all(humancalling.CommandHangupLeg) {
		if command.TargetID == caller.CallControlID {
			routingFailureState, _ = command.Payload["client_state"].(string)
		}
	}
	if routingFailureState == "" {
		t.Fatal("routing failure Hangup omitted Caller client state")
	}
	var terminalOutcome, voicemailOutcome, audioState, recordingCommandState, recordingCommandError string
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, voicemail.outcome, voicemail.audio_state,
			command.state, COALESCE(command.last_error_code, '')
		FROM human_calling_calls call
		JOIN human_calling_voicemails voicemail ON voicemail.call_id = call.id
		JOIN human_calling_provider_commands command
			ON command.id = $1 AND command.call_id = call.id
	`, record.ID).Scan(
		&terminalOutcome, &voicemailOutcome, &audioState,
		&recordingCommandState, &recordingCommandError,
	); err != nil {
		t.Fatalf("read routing failure evidence: %v", err)
	}
	if terminalOutcome != "ROUTING_FAILED" || voicemailOutcome != "MISSED_CALL" ||
		audioState != string(humancalling.VoicemailUnavailable) ||
		recordingCommandState != "FAILED" ||
		recordingCommandError != "START_VOICEMAIL_RECORDING_EVENT_ABSENT" {
		t.Fatalf(
			"routing failure = terminal:%s voicemail:%s audio:%s command:%s/%s",
			terminalOutcome, voicemailOutcome, audioState,
			recordingCommandState, recordingCommandError,
		)
	}
	if err := setupCalling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:            prefix + "-recording-before-voicemail-command",
		Type:               humancalling.FactRecordingSaved,
		OccurredAt:         now.Add(10 * time.Second),
		ConnectionID:       caller.ConnectionID,
		CallControlID:      caller.CallControlID,
		CallLegID:          caller.CallLegID,
		CallSessionID:      caller.CallSessionID,
		ClientState:        routingFailureState,
		RecordingID:        prefix + "-earlier-recording",
		RecordingStartedAt: now.Add(-10 * time.Second),
		RecordingEndedAt:   now.Add(10 * time.Second),
	}); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("recording predating voicemail command error = %v", err)
	}
	if err := setupCalling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:            prefix + "-wrong-connection",
		Type:               humancalling.FactRecordingSaved,
		OccurredAt:         now.Add(100 * time.Second),
		ConnectionID:       "wrong-connection",
		CallControlID:      caller.CallControlID,
		CallLegID:          caller.CallLegID,
		CallSessionID:      caller.CallSessionID,
		ClientState:        routingFailureState,
		RecordingID:        prefix + "-recording",
		RecordingStartedAt: now.Add(22 * time.Second),
		RecordingEndedAt:   now.Add(100 * time.Second),
	}); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("mismatched later-state recording connection error = %v", err)
	}
	if err := setupCalling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:            prefix + "-wrong-session",
		Type:               humancalling.FactRecordingSaved,
		OccurredAt:         now.Add(100 * time.Second),
		ConnectionID:       caller.ConnectionID,
		CallControlID:      caller.CallControlID,
		CallLegID:          caller.CallLegID,
		CallSessionID:      "wrong-session",
		ClientState:        routingFailureState,
		RecordingID:        prefix + "-recording",
		RecordingStartedAt: now.Add(22 * time.Second),
		RecordingEndedAt:   now.Add(100 * time.Second),
	}); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("mismatched later-state recording error = %v", err)
	}

	currentTime := now.Add(100*time.Second + 70*time.Millisecond)
	var recoveryTaskID string
	var recoveryTaskVersion int64
	if err := pool.QueryRow(context.Background(), `
		SELECT task.id::text, task.version
		FROM human_calling_voicemails voicemail
		JOIN work_tasks task ON task.id = voicemail.task_id
		JOIN human_calling_provider_commands command ON command.call_id = voicemail.call_id
		WHERE command.id = $1
	`, record.ID).Scan(&recoveryTaskID, &recoveryTaskVersion); err != nil {
		t.Fatalf("read routing failure recovery Task: %v", err)
	}
	workModule := work.New(
		pool,
		access.New(pool, func() time.Time { return currentTime }),
		func() time.Time { return currentTime },
	)
	completedTask, err := workModule.CompleteTask(context.Background(), work.CompleteTaskCommand{
		Identity:        staff[0],
		TaskID:          recoveryTaskID,
		ExpectedVersion: recoveryTaskVersion,
	})
	if err != nil {
		t.Fatalf("complete routing failure recovery Task before replay: %v", err)
	}
	if completedTask.State != work.TaskCompleted {
		t.Fatalf("completed routing failure recovery Task = %#v", completedTask)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(
		pool,
		access.New(pool, func() time.Time { return currentTime }),
		provider,
		humancalling.Config{
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return currentTime },
	)
	providerCommandRowsBefore := 0
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_provider_commands
		WHERE call_id = (
			SELECT call_id FROM human_calling_provider_commands WHERE id = $1
		)
	`, record.ID).Scan(&providerCommandRowsBefore); err != nil {
		t.Fatalf("count provider commands before recording callback: %v", err)
	}
	provider.mu.Lock()
	providerEffectsBefore := len(provider.commands)
	provider.mu.Unlock()

	recordingID := prefix + "-recording"
	envelope := map[string]any{"data": map[string]any{
		"record_type": "event", "event_type": "call.recording.saved",
		"id":          prefix + "-recording-saved",
		"occurred_at": now.Add(100 * time.Second).Format(time.RFC3339Nano),
		"payload": map[string]any{
			"connection_id":        caller.ConnectionID,
			"call_control_id":      caller.CallControlID,
			"call_leg_id":          caller.CallLegID,
			"call_session_id":      caller.CallSessionID,
			"client_state":         routingFailureState,
			"recording_id":         recordingID,
			"recording_started_at": now.Add(22 * time.Second).Format(time.RFC3339Nano),
			"recording_ended_at":   now.Add(100 * time.Second).Format(time.RFC3339Nano),
		},
	}}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), body...),
	))
	if _, err := calling.ReceiveWebhook(context.Background(), body, timestamp, signature); err != nil {
		t.Fatalf("receive later-state voicemail recording: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process later-state voicemail recording: processed=%t err=%v", processed, err)
	}

	var receiptState, projectionCode, providerRecordingID string
	var attempts, voicemailTimelineEntries int
	if err := pool.QueryRow(context.Background(), `
		SELECT receipt.state, receipt.projection_attempts,
			COALESCE(receipt.projection_error_code, ''), call.terminal_outcome,
			voicemail.outcome, voicemail.audio_state,
			COALESCE(voicemail.provider_recording_id, ''),
			(
				SELECT count(*) FROM human_calling_timeline timeline
				WHERE timeline.call_id = receipt.call_id
					AND timeline.kind = 'call.recovery_task_created'
					AND timeline.error_code = 'VOICEMAIL'
			)
		FROM human_calling_provider_receipts receipt
		JOIN human_calling_calls call ON call.id = receipt.call_id
		JOIN human_calling_voicemails voicemail ON voicemail.call_id = call.id
		WHERE receipt.event_id = $1
	`, prefix+"-recording-saved").Scan(
		&receiptState, &attempts, &projectionCode, &terminalOutcome,
		&voicemailOutcome, &audioState, &providerRecordingID,
		&voicemailTimelineEntries,
	); err != nil {
		t.Fatalf("read later-state voicemail receipt evidence: %v", err)
	}
	if receiptState != string(humancalling.ReceiptApplied) || attempts != 1 ||
		projectionCode != "" || terminalOutcome != "VOICEMAIL" ||
		voicemailOutcome != "VOICEMAIL" ||
		audioState != string(humancalling.VoicemailReady) ||
		providerRecordingID != recordingID || voicemailTimelineEntries != 1 {
		t.Fatalf(
			"later-state voicemail receipt = state:%s attempts:%d code:%s terminal:%s voicemail:%s audio:%s recording:%s timeline:%d",
			receiptState, attempts, projectionCode, terminalOutcome,
			voicemailOutcome, audioState, providerRecordingID,
			voicemailTimelineEntries,
		)
	}
	duplicate, err := calling.ReceiveWebhook(context.Background(), body, timestamp, signature)
	if err != nil {
		t.Fatalf("receive duplicate later-state voicemail recording: %v", err)
	}
	if duplicate.State != humancalling.ReceiptApplied || !duplicate.Duplicate ||
		duplicate.DuplicateCount != 1 {
		t.Fatalf("duplicate later-state voicemail receipt = %#v", duplicate)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM human_calling_timeline timeline
		JOIN human_calling_provider_receipts receipt ON receipt.call_id = timeline.call_id
		WHERE receipt.event_id = $1
			AND timeline.kind = 'call.recovery_task_created'
			AND timeline.error_code = 'VOICEMAIL'
	`, prefix+"-recording-saved").Scan(&voicemailTimelineEntries); err != nil {
		t.Fatalf("count voicemail timeline after duplicate callback: %v", err)
	}
	if voicemailTimelineEntries != 1 {
		t.Fatalf("voicemail timeline after duplicate callback = %d, want 1", voicemailTimelineEntries)
	}
	providerCommandRowsAfter := 0
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_provider_commands
		WHERE call_id = (
			SELECT call_id FROM human_calling_provider_commands WHERE id = $1
		)
	`, record.ID).Scan(&providerCommandRowsAfter); err != nil {
		t.Fatalf("count provider commands after recording callback: %v", err)
	}
	provider.mu.Lock()
	providerEffectsAfter := len(provider.commands)
	provider.mu.Unlock()
	if providerCommandRowsAfter != providerCommandRowsBefore ||
		providerEffectsAfter != providerEffectsBefore {
		t.Fatalf(
			"voicemail recording callback created provider work = commands:%d->%d effects:%d->%d",
			providerCommandRowsBefore, providerCommandRowsAfter,
			providerEffectsBefore, providerEffectsAfter,
		)
	}
	var taskState, taskTitle, taskOrigin, taskOutcome string
	var taskVersionAfter int64
	if err := pool.QueryRow(context.Background(), `
		SELECT state, title, origin, recovery_outcome, version
		FROM work_tasks
		WHERE id = $1
	`, recoveryTaskID).Scan(
		&taskState,
		&taskTitle,
		&taskOrigin,
		&taskOutcome,
		&taskVersionAfter,
	); err != nil {
		t.Fatalf("read completed recovery Task after recording callback: %v", err)
	}
	if taskState != string(work.TaskCompleted) ||
		taskTitle != completedTask.Title ||
		taskOrigin != string(completedTask.Origin) ||
		taskOutcome != string(completedTask.RecoveryOutcome) ||
		taskVersionAfter != completedTask.Version {
		t.Fatalf(
			"completed recovery Task changed during replay = state:%s title:%s origin:%s outcome:%s version:%d; want %#v",
			taskState,
			taskTitle,
			taskOrigin,
			taskOutcome,
			taskVersionAfter,
			completedTask,
		)
	}
}

func TestOutboundRecordingSavedAfterStaffHangupAppliesImmediately(t *testing.T) {
	testOutboundRecordingSavedAfterLaterClientStateAppliesImmediately(t, "")
}

func TestOutboundRecordingSavedAfterCleanupAppliesImmediately(t *testing.T) {
	testOutboundRecordingSavedAfterLaterClientStateAppliesImmediately(t, "cleanup")
}

func testOutboundRecordingSavedAfterLaterClientStateAppliesImmediately(
	t *testing.T,
	recordingClientStateKind string,
) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	currentTime := now
	accessModule := access.New(pool, func() time.Time { return currentTime })
	authorization, staff := provisionConcurrentStaff(
		t, accessModule, now, "outbound-recording-after-hangup", 1,
	)
	if _, err := pool.Exec(context.Background(), `
		UPDATE access_practices SET
			connected_call_recording_retention_days = 30,
			connected_call_recording_enabled = true
		WHERE id = $1
	`, authorization.Practice.ID); err != nil {
		t.Fatalf("enable outbound connected recording: %v", err)
	}
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{
		{CallControlID: "outbound-recording-staff-control", CallLegID: "outbound-recording-staff-leg"},
		{CallControlID: "outbound-recording-destination-control", CallLegID: "outbound-recording-destination-leg"},
	}}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
		WebhookPublicKeys:      [][]byte{publicKey},
	}, func() time.Time { return currentTime })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, "outbound-recording-browser")
	if err := calling.ProvisionLocationVoices(context.Background(),
		[]humancalling.LocationVoiceProvision{{
			PracticeKey: "outbound-recording-after-hangup-practice",
			LocationKey: "outbound-recording-after-hangup-location",
			Number:      "+14843336938", Enabled: true,
		}}); err != nil {
		t.Fatalf("provision outbound caller ID: %v", err)
	}

	call, err := calling.StartOutboundCall(context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity: staff[0], SessionID: "outbound-recording-browser-1",
			IdempotencyKey: "outbound-recording-call",
			PracticeID:     authorization.Practice.ID,
			LocationID:     authorization.Locations[0].ID,
			Destination:    "+15555550123",
		})
	if err != nil {
		t.Fatalf("start outbound Call: %v", err)
	}
	processAllCommands(t, calling)
	staffDial := provider.last(humancalling.CommandDialOutboundStaff)
	staffClientState, _ := staffDial.Payload["client_state"].(string)
	staffFact := humancalling.ProviderFact{
		EventID: "outbound-recording-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: "outbound-recording-staff-control", CallLegID: "outbound-recording-staff-leg",
		CallSessionID: "outbound-recording-staff-session", ClientState: staffClientState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatalf("project outbound Staff initiation: %v", err)
	}
	staffFact.EventID = "outbound-recording-staff-answered"
	staffFact.Type = humancalling.FactCallAnswered
	staffFact.OccurredAt = now.Add(2 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatalf("project outbound Staff answer: %v", err)
	}
	callingState, err := calling.ReadCallingState(context.Background(), staff[0])
	if err != nil || len(callingState.Ringing) != 1 {
		t.Fatalf("read outbound media state: %#v, err = %v", callingState, err)
	}
	if _, err := calling.ConfirmOutboundMedia(context.Background(),
		humancalling.ConfirmOutboundMediaCommand{
			Identity: staff[0], SessionID: "outbound-recording-browser-1", CallID: call.ID,
			MediaToken: callingState.Ringing[0].MediaToken,
		}); err != nil {
		t.Fatalf("confirm outbound Staff media: %v", err)
	}
	processAllCommands(t, calling)
	destinationDial := provider.last(humancalling.CommandDialOutboundDestination)
	destinationClientState, _ := destinationDial.Payload["client_state"].(string)
	destinationFact := humancalling.ProviderFact{
		EventID: "outbound-recording-destination-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(3 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: "outbound-recording-destination-control", CallLegID: "outbound-recording-destination-leg",
		CallSessionID: "outbound-recording-destination-session", ClientState: destinationClientState,
	}
	if err := calling.ApplyProviderFact(context.Background(), destinationFact); err != nil {
		t.Fatalf("project outbound destination initiation: %v", err)
	}
	destinationFact.EventID = "outbound-recording-destination-answered"
	destinationFact.Type = humancalling.FactCallAnswered
	destinationFact.OccurredAt = now.Add(4 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), destinationFact); err != nil {
		t.Fatalf("project outbound destination answer: %v", err)
	}
	processAllCommands(t, calling)
	for _, fact := range []humancalling.ProviderFact{
		{
			EventID: "outbound-recording-destination-bridged", Type: humancalling.FactCallBridged,
			OccurredAt: now.Add(5 * time.Second), CallControlID: destinationFact.CallControlID,
			CallLegID: destinationFact.CallLegID, CallSessionID: destinationFact.CallSessionID,
		},
		{
			EventID: "outbound-recording-staff-bridged", Type: humancalling.FactCallBridged,
			OccurredAt: now.Add(5 * time.Second), CallControlID: staffFact.CallControlID,
			CallLegID: staffFact.CallLegID, CallSessionID: staffFact.CallSessionID,
		},
	} {
		if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
			t.Fatalf("project outbound Bridge: %v", err)
		}
	}
	currentTime = now.Add(6 * time.Second)
	if _, err := calling.RequestHangup(
		context.Background(), staff[0], "outbound-recording-browser-1", call.ID,
	); err != nil {
		t.Fatalf("request outbound Hangup: %v", err)
	}
	processAllCommands(t, calling)
	var recordingClientState string
	for _, command := range provider.all(humancalling.CommandHangupLeg) {
		if command.TargetID == destinationFact.CallControlID {
			recordingClientState, _ = command.Payload["client_state"].(string)
		}
	}
	if recordingClientState == "" {
		t.Fatal("destination Hangup omitted client_state")
	}
	if recordingClientStateKind != "" {
		decoded, err := base64.StdEncoding.DecodeString(recordingClientState)
		if err != nil {
			t.Fatalf("decode destination Hangup client_state: %v", err)
		}
		var state map[string]any
		if err := json.Unmarshal(decoded, &state); err != nil {
			t.Fatalf("parse destination Hangup client_state: %v", err)
		}
		state["kind"] = recordingClientStateKind
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("encode destination Hangup client_state: %v", err)
		}
		recordingClientState = base64.StdEncoding.EncodeToString(encoded)
	}
	var providerCommandRowsBefore int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_provider_commands WHERE call_id = $1
	`, call.ID).Scan(&providerCommandRowsBefore); err != nil {
		t.Fatalf("count provider commands before recording callback: %v", err)
	}
	provider.mu.Lock()
	providerEffectsBefore := len(provider.commands)
	provider.mu.Unlock()

	recordingStartedAt := now.Add(5 * time.Second)
	recordingEndedAt := now.Add(17 * time.Second)
	envelope := map[string]any{"data": map[string]any{
		"record_type": "event", "event_type": "call.recording.saved",
		"id":          "outbound-recording-saved-after-hangup",
		"occurred_at": recordingEndedAt.Format(time.RFC3339Nano),
		"payload": map[string]any{
			"connection_id":        "staff-call-control-connection",
			"call_control_id":      destinationFact.CallControlID,
			"call_leg_id":          destinationFact.CallLegID,
			"call_session_id":      destinationFact.CallSessionID,
			"client_state":         recordingClientState,
			"recording_id":         "outbound-recording-id",
			"recording_started_at": recordingStartedAt.Format(time.RFC3339Nano),
			"recording_ended_at":   recordingEndedAt.Format(time.RFC3339Nano),
		},
	}}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	currentTime = recordingEndedAt
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), body...),
	))
	if _, err := calling.ReceiveWebhook(
		context.Background(), body, timestamp, signature,
	); err != nil {
		t.Fatalf("receive outbound recording webhook: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process outbound recording webhook: processed=%t err=%v", processed, err)
	}
	duplicateReceipt, err := calling.ReceiveWebhook(
		context.Background(), body, timestamp, signature,
	)
	if err != nil {
		t.Fatalf("read outbound recording receipt: %v", err)
	}
	if duplicateReceipt.State != humancalling.ReceiptApplied ||
		!duplicateReceipt.Duplicate || duplicateReceipt.DuplicateCount != 1 {
		t.Fatalf("outbound recording duplicate receipt = %#v", duplicateReceipt)
	}
	var receiptState, projectionCode, audioState, providerRecordingID string
	var attempts, readyTimelineEntries int
	if err := pool.QueryRow(context.Background(), `
		SELECT receipt.state, receipt.projection_attempts,
			COALESCE(receipt.projection_error_code, ''), recording.audio_state,
			COALESCE(recording.provider_recording_id, ''),
			(
				SELECT count(*) FROM human_calling_timeline timeline
				WHERE timeline.call_id = receipt.call_id
					AND timeline.kind = 'call.recording.ready'
			)
		FROM human_calling_provider_receipts receipt
		JOIN human_calling_call_recordings recording
			ON recording.call_id = receipt.call_id
		WHERE receipt.event_id = 'outbound-recording-saved-after-hangup'
	`).Scan(&receiptState, &attempts, &projectionCode, &audioState,
		&providerRecordingID, &readyTimelineEntries); err != nil {
		t.Fatalf("read outbound recording receipt evidence: %v", err)
	}
	if receiptState != string(humancalling.ReceiptApplied) || attempts != 1 ||
		projectionCode != "" || audioState != string(humancalling.RecordingReady) ||
		providerRecordingID != "outbound-recording-id" || readyTimelineEntries != 1 {
		t.Fatalf(
			"outbound recording receipt = state:%s attempts:%d code:%s audio:%s recording:%s timeline:%d",
			receiptState, attempts, projectionCode, audioState, providerRecordingID,
			readyTimelineEntries,
		)
	}
	projected, err := calling.ReadCall(context.Background(), staff[0], call.ID)
	if err != nil {
		t.Fatalf("read outbound Call: %v", err)
	}
	if projected.Recording.AudioState != humancalling.RecordingReady ||
		projected.Recording.DurationSeconds != 12 {
		t.Fatalf("outbound recording = %#v, want READY for 12 seconds", projected.Recording)
	}

	readyEnvelope := envelope["data"].(map[string]any)
	readyEnvelope["id"] = "outbound-recording-saved-ready-replay"
	readyBody, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	readySignature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), readyBody...),
	))
	if _, err := calling.ReceiveWebhook(
		context.Background(), readyBody, timestamp, readySignature,
	); err != nil {
		t.Fatalf("receive READY recording replay: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process READY recording replay: processed=%t err=%v", processed, err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT receipt.state, receipt.projection_attempts,
			COALESCE(receipt.projection_error_code, ''),
			(
				SELECT count(*) FROM human_calling_timeline timeline
				WHERE timeline.call_id = receipt.call_id
					AND timeline.kind = 'call.recording.ready'
			)
		FROM human_calling_provider_receipts receipt
		WHERE receipt.event_id = 'outbound-recording-saved-ready-replay'
	`).Scan(&receiptState, &attempts, &projectionCode, &readyTimelineEntries); err != nil {
		t.Fatalf("read READY recording replay: %v", err)
	}
	if receiptState != string(humancalling.ReceiptApplied) || attempts != 1 ||
		projectionCode != "" || readyTimelineEntries != 1 {
		t.Fatalf("READY recording replay = state:%s attempts:%d code:%s timeline:%d",
			receiptState, attempts, projectionCode, readyTimelineEntries)
	}

	readyEnvelope["id"] = "outbound-recording-saved-mismatched-recording"
	readyPayload := readyEnvelope["payload"].(map[string]any)
	readyPayload["recording_id"] = "different-recording-id"
	mismatchBody, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	mismatchSignature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), mismatchBody...),
	))
	if _, err := calling.ReceiveWebhook(
		context.Background(), mismatchBody, timestamp, mismatchSignature,
	); err != nil {
		t.Fatalf("receive mismatched recording callback: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process mismatched recording callback: processed=%t err=%v", processed, err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, COALESCE(projection_error_code, '')
		FROM human_calling_provider_receipts
		WHERE event_id = 'outbound-recording-saved-mismatched-recording'
	`).Scan(&receiptState, &attempts, &projectionCode); err != nil {
		t.Fatalf("read mismatched recording receipt: %v", err)
	}
	if receiptState != string(humancalling.ReceiptPending) || attempts != 1 ||
		projectionCode != "PROJECTION_APPLY_FACT_CONFLICT" {
		t.Fatalf("mismatched recording receipt = state:%s attempts:%d code:%s",
			receiptState, attempts, projectionCode)
	}
	var providerCommandRowsAfter int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_provider_commands WHERE call_id = $1
	`, call.ID).Scan(&providerCommandRowsAfter); err != nil {
		t.Fatalf("count provider commands after recording callbacks: %v", err)
	}
	provider.mu.Lock()
	providerEffectsAfter := len(provider.commands)
	provider.mu.Unlock()
	if providerCommandRowsAfter != providerCommandRowsBefore ||
		providerEffectsAfter != providerEffectsBefore {
		t.Fatalf(
			"recording callbacks created provider work = commands:%d->%d effects:%d->%d",
			providerCommandRowsBefore, providerCommandRowsAfter,
			providerEffectsBefore, providerEffectsAfter,
		)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(provider_recording_id, ''),
			(
				SELECT count(*) FROM human_calling_timeline timeline
				WHERE timeline.call_id = recording.call_id
					AND timeline.kind = 'call.recording.ready'
			)
		FROM human_calling_call_recordings recording WHERE call_id = $1
	`, call.ID).Scan(&providerRecordingID, &readyTimelineEntries); err != nil {
		t.Fatalf("read recording after mismatch: %v", err)
	}
	if providerRecordingID != "outbound-recording-id" || readyTimelineEntries != 1 {
		t.Fatalf("mismatch changed recording evidence = recording:%s timeline:%d",
			providerRecordingID, readyTimelineEntries)
	}
}

func TestDelayedProviderHangupAfterLocalEndingConvergesWithoutRetry(t *testing.T) {
	now := time.Date(2026, time.August, 11, 14, 0, 0, 0, time.UTC)
	prefix := "delayed-hangup-after-local-ending"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control",
		CallLegID:     prefix + "-staff-leg",
	}}}
	pool, setupCalling, caller, staff := prepareInboundFanout(
		t, now, prefix, provider, 1,
	)
	processAllCommands(t, setupCalling)
	dial := provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	staffFact := humancalling.ProviderFact{
		EventID:       prefix + "-staff-initiated",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now.Add(2 * time.Second),
		ConnectionID:  "staff-call-control-connection",
		CallControlID: prefix + "-staff-control",
		CallLegID:     prefix + "-staff-leg",
		CallSessionID: prefix + "-staff-session",
		ClientState:   staffState,
	}
	if err := setupCalling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatalf("project Staff initiation: %v", err)
	}
	staffFact.EventID = prefix + "-staff-answered"
	staffFact.Type = humancalling.FactCallAnswered
	staffFact.OccurredAt = now.Add(3 * time.Second)
	if err := setupCalling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatalf("project Staff answer: %v", err)
	}
	processAllCommands(t, setupCalling)
	bridge := provider.last(humancalling.CommandBridge)
	bridgeState, _ := bridge.Payload["client_state"].(string)
	if err := setupCalling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       prefix + "-staff-bridged",
		Type:          humancalling.FactCallBridged,
		OccurredAt:    now.Add(4 * time.Second),
		CallControlID: staffFact.CallControlID,
		CallLegID:     staffFact.CallLegID,
		CallSessionID: staffFact.CallSessionID,
		ClientState:   bridgeState,
	}); err != nil {
		t.Fatalf("project Staff Bridge: %v", err)
	}
	caller.EventID = prefix + "-caller-bridged"
	caller.Type = humancalling.FactCallBridged
	caller.OccurredAt = now.Add(4 * time.Second)
	if err := setupCalling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("project caller Bridge: %v", err)
	}

	var callID string
	if err := pool.QueryRow(context.Background(), `
		SELECT call_id::text FROM human_calling_call_legs
		WHERE provider_call_leg_id = $1
	`, staffFact.CallLegID).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	currentTime := now.Add(6 * time.Second)
	accessModule := access.New(pool, func() time.Time { return currentTime })
	calling := humancalling.New(
		pool,
		accessModule,
		provider,
		humancalling.Config{
			WebhookPublicKeys: [][]byte{publicKey},
		},
		func() time.Time { return currentTime },
	)
	sessionID := prefix + "-browser-1"
	if _, err := calling.RequestHangup(
		context.Background(), staff[0], sessionID, callID,
	); err != nil {
		t.Fatalf("begin local ending: %v", err)
	}
	processAllCommands(t, calling)
	localEndingAt := currentTime
	receiveAndProcessHangup := func(eventID, callSessionID string, occurredAt time.Time) {
		t.Helper()
		raw := []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"call.hangup","id":"%s","occurred_at":"%s","payload":{"connection_id":"staff-call-control-connection","call_control_id":"%s","call_leg_id":"%s","call_session_id":"%s","client_state":"%s","hangup_cause":"normal_clearing","hangup_source":"staff"}}}`,
			eventID,
			occurredAt.Format(time.RFC3339Nano),
			staffFact.CallControlID,
			staffFact.CallLegID,
			callSessionID,
			staffState,
		))
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(timestamp+"|"), raw...),
		))
		if _, err := calling.ReceiveWebhook(
			context.Background(), raw, timestamp, signature,
		); err != nil {
			t.Fatalf("receive provider Hangup %s: %v", eventID, err)
		}
		if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
			t.Fatalf("process provider Hangup %s: processed=%t err=%v", eventID, processed, err)
		}
	}
	providerOccurredAt := now.Add(7 * time.Second)
	currentTime = now.Add(8 * time.Second)
	receiveAndProcessHangup(
		prefix+"-delayed-staff-hangup", caller.CallSessionID, providerOccurredAt,
	)

	var receiptState, projectionError, legState, terminalOutcome, hangupCommandState string
	var projectionAttempts int
	var receiptOccurredAt, answeredAt, endingAt time.Time
	var endedAt, quarantinedAt, timelineOccurredAt *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, COALESCE(projection_error_code, ''),
			occurred_at, quarantined_at
		FROM human_calling_provider_receipts WHERE event_id = $1
	`, prefix+"-delayed-staff-hangup").Scan(
		&receiptState,
		&projectionAttempts,
		&projectionError,
		&receiptOccurredAt,
		&quarantinedAt,
	); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT leg.state, leg.answered_at, leg.ending_at, leg.ended_at,
			COALESCE(call.terminal_outcome, '')
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		WHERE leg.provider_call_leg_id = $1
	`, staffFact.CallLegID).Scan(
		&legState,
		&answeredAt,
		&endingAt,
		&endedAt,
		&terminalOutcome,
	); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT max(occurred_at) FROM human_calling_timeline
		WHERE provider_event_id = $1 AND kind = 'call_leg.ended'
	`, prefix+"-delayed-staff-hangup").Scan(&timelineOccurredAt); err != nil {
		t.Fatalf("read delayed Hangup timeline evidence: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT command.state
		FROM human_calling_provider_commands command
		JOIN human_calling_call_legs leg ON leg.id = command.call_leg_id
		WHERE leg.provider_call_leg_id = $1 AND command.action = 'HANGUP_LEG'
		ORDER BY command.created_at DESC, command.id DESC
		LIMIT 1
	`, staffFact.CallLegID).Scan(&hangupCommandState); err != nil {
		t.Fatalf("read delayed Hangup command state: %v", err)
	}
	if receiptState != string(humancalling.ReceiptApplied) ||
		projectionAttempts != 1 || projectionError != "" || quarantinedAt != nil {
		t.Errorf("delayed Hangup receipt = state:%s attempts:%d error:%s quarantine:%v",
			receiptState, projectionAttempts, projectionError, quarantinedAt)
	}
	if legState != "ENDED" || terminalOutcome != "ENDED" {
		t.Errorf("delayed Hangup outcome = leg:%s Call:%s", legState, terminalOutcome)
	}
	if hangupCommandState != "RECONCILED" {
		t.Errorf("delayed Hangup command = %s, want RECONCILED", hangupCommandState)
	}
	if endedAt == nil || answeredAt.After(endingAt) ||
		!localEndingAt.Equal(endingAt) ||
		(endedAt != nil && providerOccurredAt.After(*endedAt)) ||
		(endedAt != nil && endingAt.After(*endedAt)) {
		t.Errorf("CallLeg termination times = answered:%s provider:%s local:%s ending:%s ended:%s",
			answeredAt, providerOccurredAt, localEndingAt, endingAt, endedAt)
	}
	if !receiptOccurredAt.Equal(providerOccurredAt) || timelineOccurredAt == nil ||
		(timelineOccurredAt != nil && !timelineOccurredAt.Equal(providerOccurredAt)) {
		t.Errorf("provider time was not preserved = receipt:%s timeline:%s want:%s",
			receiptOccurredAt, timelineOccurredAt, providerOccurredAt)
	}
	if _, err := calling.SetReadiness(context.Background(), humancalling.ReadinessCommand{
		Identity: staff[0], SessionID: sessionID, Registered: true,
		MicrophoneReady: true, AudioReady: true, SessionHealthy: true,
		Available: true,
	}); err != nil {
		t.Fatalf("enable Staff availability after delayed Hangup: %v", err)
	}
	state, err := calling.ReadCallingState(context.Background(), staff[0])
	if err != nil || !state.Softphone.Available || state.Softphone.ActiveCallID != "" {
		t.Errorf("Staff availability after delayed Hangup = %#v err=%v",
			state.Softphone, err)
	}

	currentTime = now.Add(10 * time.Second)
	receiveAndProcessHangup(
		prefix+"-foreign-session-hangup", "foreign-session", now.Add(9*time.Second),
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_attempts, COALESCE(projection_error_code, '')
		FROM human_calling_provider_receipts WHERE event_id = $1
	`, prefix+"-foreign-session-hangup").Scan(
		&receiptState, &projectionAttempts, &projectionError,
	); err != nil {
		t.Fatal(err)
	}
	if receiptState != string(humancalling.ReceiptPending) ||
		projectionAttempts != 1 || projectionError != "PROJECTION_APPLY_FACT_CONFLICT" {
		t.Errorf("foreign-session Hangup = state:%s attempts:%d error:%s",
			receiptState, projectionAttempts, projectionError)
	}
}

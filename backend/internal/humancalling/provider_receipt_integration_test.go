package humancalling_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

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
			WebhookPublicKeys: []ed25519.PublicKey{publicKey},
			WebhookTolerance:  5 * time.Minute,
		},
		func() time.Time { return now },
	)
	receive := func(raw []byte) humancalling.WebhookReceipt {
		t.Helper()
		timestamp := strconv.FormatInt(now.Unix(), 10)
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
			WebhookPublicKeys: []ed25519.PublicKey{publicKey},
			WebhookTolerance:  5 * time.Minute,
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
		timestamp := strconv.FormatInt(now.Unix(), 10)
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
			WebhookPublicKeys: []ed25519.PublicKey{publicKey},
			WebhookTolerance:  5 * time.Minute,
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
		timestamp := strconv.FormatInt(now.Unix(), 10)
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
			WebhookPublicKeys: []ed25519.PublicKey{publicKey},
			WebhookTolerance:  5 * time.Minute,
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
	providerOccurredAt := now.Add(5 * time.Second)
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.hangup","id":"%s","occurred_at":"%s","payload":{"connection_id":"staff-call-control-connection","call_control_id":"%s","call_leg_id":"%s","call_session_id":"%s","hangup_cause":"normal_clearing","hangup_source":"staff"}}}`,
		prefix+"-delayed-staff-hangup",
		providerOccurredAt.Format(time.RFC3339Nano),
		staffFact.CallControlID,
		staffFact.CallLegID,
		staffFact.CallSessionID,
	))
	timestamp := strconv.FormatInt(currentTime.Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	if _, err := calling.ReceiveWebhook(
		context.Background(), raw, timestamp, signature,
	); err != nil {
		t.Fatalf("receive delayed provider Hangup: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process delayed provider Hangup: processed=%t err=%v", processed, err)
	}

	var receiptState, projectionError, legState, terminalOutcome string
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
	if receiptState != string(humancalling.ReceiptApplied) ||
		projectionAttempts != 1 || projectionError != "" || quarantinedAt != nil {
		t.Errorf("delayed Hangup receipt = state:%s attempts:%d error:%s quarantine:%v",
			receiptState, projectionAttempts, projectionError, quarantinedAt)
	}
	if legState != "ENDED" || terminalOutcome != "ENDED" {
		t.Errorf("delayed Hangup outcome = leg:%s Call:%s", legState, terminalOutcome)
	}
	if endedAt == nil || answeredAt.After(endingAt) ||
		providerOccurredAt.After(endingAt) || localEndingAt.After(endingAt) ||
		(endedAt != nil && endingAt.After(*endedAt)) {
		t.Errorf("non-monotonic CallLeg times = answered:%s provider:%s local:%s ending:%s ended:%s",
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
}

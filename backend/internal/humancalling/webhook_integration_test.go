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

func TestSignedWebhookCommitsExactReceiptBeforeIdempotentProjection(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(pool, accessModule, &recordingProvider{}, humancalling.Config{
		SIPDomain:        "synthetic.sip.telnyx.com",
		OfferDuration:    20 * time.Second,
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		RecordingBucket:  "synthetic-recordings",
		WebhookPublicKey: publicKey,
		WebhookTolerance: 5 * time.Minute,
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-webhook",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "webhook-source",
		IdempotencyKey: "webhook-idempotency",
		Contact: humancalling.ContactContext{
			DisplayName:    "Webhook Caller",
			NameSource:     "Abita",
			TransferReason: "Webhook proof",
			ReasonSource:   "Abita AI",
		},
	})
	if err != nil {
		t.Fatalf("create webhook handoff: %v", err)
	}
	handoffToken := strings.SplitN(
		strings.TrimPrefix(handoff.SIPDestination, "sip:"),
		"@",
		2,
	)[0]

	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.initiated","id":"webhook-event-1","occurred_at":"%s","payload":{"call_control_id":"webhook-caller-control","call_leg_id":"webhook-caller-leg","call_session_id":"webhook-session","client_state":"","to":"+14843336938","custom_headers":[{"name":"X-Acuity-Handoff-Token","value":"%s"}]}}}`,
		now.Format(time.RFC3339Nano),
		handoffToken,
	))
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	receipt, err := calling.ReceiveWebhook(
		context.Background(),
		raw,
		timestamp,
		signature,
	)
	if err != nil {
		t.Fatalf("receive signed webhook: %v", err)
	}
	if receipt.EventID != "webhook-event-1" || receipt.Duplicate {
		t.Fatalf("first receipt = %#v", receipt)
	}
	replayed, err := calling.ReceiveWebhook(
		context.Background(),
		raw,
		timestamp,
		signature,
	)
	if err != nil || !replayed.Duplicate {
		t.Fatalf("duplicate receipt = %#v, err = %v", replayed, err)
	}
	if _, err := calling.ReceiveWebhook(
		context.Background(),
		raw,
		timestamp,
		base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
	); err != humancalling.ErrInvalidWebhook {
		t.Fatalf("invalid signature error = %v", err)
	}

	processed, err := calling.ProcessNextReceipt(context.Background())
	if err != nil || !processed {
		t.Fatalf("process signed receipt: processed=%t err=%v", processed, err)
	}
	var projectedState humancalling.ReceiptState
	var duplicateCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT state, duplicate_count
		FROM human_calling_provider_receipts
		WHERE event_id = 'webhook-event-1'
	`).Scan(&projectedState, &duplicateCount); err != nil {
		t.Fatalf("read projected receipt: %v", err)
	}
	if projectedState != humancalling.ReceiptApplied || duplicateCount != 1 {
		t.Fatalf("projected receipt state = %q, duplicates = %d", projectedState, duplicateCount)
	}

	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"webhook-browser",
		false,
	); err != nil {
		t.Fatalf("acquire webhook softphone: %v", err)
	}
	if _, err := calling.SetReadiness(context.Background(), humancalling.ReadinessCommand{
		Identity:        identity,
		SessionID:       "webhook-browser",
		Registered:      true,
		MicrophoneReady: true,
		AudioReady:      true,
		SessionHealthy:  true,
		Available:       true,
	}); err != nil {
		t.Fatalf("ready webhook softphone: %v", err)
	}
	offers, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(offers) != 1 || offers[0].DisplayName != "Webhook Caller" {
		t.Fatalf("projected webhook offers = %#v, err = %v", offers, err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "webhook-event-1",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "webhook-caller-control",
		CallLegID:     "webhook-caller-leg",
		CallSessionID: "webhook-session",
		To:            handoff.SIPDestination,
	}); err != nil {
		t.Fatalf("replay projected fact: %v", err)
	}
	offers, err = calling.ListOffers(context.Background(), identity)
	if err != nil || len(offers) != 1 {
		t.Fatalf("idempotent projected offers = %#v, err = %v", offers, err)
	}

	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"webhook-browser",
		offers[0].ID,
	); err != nil {
		t.Fatalf("accept webhook offer: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process webhook Call commands: %v", err)
		}
		if !processed {
			break
		}
	}
	var attemptID string
	if err := pool.QueryRow(context.Background(), `
		SELECT current_attempt_id::text
		FROM human_calling_calls
		WHERE id = $1
	`, offers[0].ID).Scan(&attemptID); err != nil {
		t.Fatalf("load webhook connection attempt: %v", err)
	}
	clientState := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"v":1,"call":"%s","leg":"staff","attempt":"%s"}`,
		offers[0].ID,
		attemptID,
	)))
	receive := func(eventID, eventType, occurredAt, hangupCause string) {
		t.Helper()
		eventRaw := []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"%s","id":"%s","occurred_at":"%s","payload":{"call_control_id":"staff-control-1","call_leg_id":"staff-leg-1","call_session_id":"webhook-session","client_state":"%s","hangup_cause":"%s"}}}`,
			eventType,
			eventID,
			occurredAt,
			clientState,
			hangupCause,
		))
		eventTimestamp := strconv.FormatInt(now.Unix(), 10)
		eventSignature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(eventTimestamp+"|"), eventRaw...),
		))
		if _, err := calling.ReceiveWebhook(
			context.Background(),
			eventRaw,
			eventTimestamp,
			eventSignature,
		); err != nil {
			t.Fatalf("receive %s: %v", eventID, err)
		}
	}
	bridgeAt := now.Add(time.Second)
	hangupAt := now.Add(2 * time.Second)
	now = now.Add(5 * time.Second)
	receive(
		"a-webhook-hangup",
		"call.hangup",
		hangupAt.Format(time.RFC3339Nano),
		"normal_clearing",
	)
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("defer reordered hangup: processed=%t err=%v", processed, err)
	}
	now = now.Add(3 * time.Second)
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("project reordered hangup first: processed=%t err=%v", processed, err)
	}
	reopened, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(reopened) != 1 || reopened[0].ID != offers[0].ID {
		t.Fatalf("hangup-reopened offer = %#v, err = %v", reopened, err)
	}
	now = now.Add(5 * time.Second)
	receive(
		"b-webhook-bridge",
		"call.bridged",
		bridgeAt.Format(time.RFC3339Nano),
		"",
	)
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("project bridge delayed beyond receipt hold: processed=%t err=%v", processed, err)
	}
	ended, err := calling.ReadCall(context.Background(), identity, offers[0].ID)
	if err != nil || ended.State != humancalling.CallNeedsDisposition {
		t.Fatalf("reordered bridge/hangup Call = %#v, err = %v", ended, err)
	}
}

func TestIrreparableKnownWebhookReceiptFailsPermanently(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(
		pool,
		nil,
		&recordingProvider{},
		humancalling.Config{
			WebhookPublicKey: publicKey,
			WebhookTolerance: 5 * time.Minute,
		},
		func() time.Time { return now },
	)
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.hangup","id":"malformed-known-event","occurred_at":"%s","payload":{}}}`,
		now.Format(time.RFC3339Nano),
	))
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	if _, err := calling.ReceiveWebhook(
		context.Background(),
		raw,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive malformed known event: %v", err)
	}

	now = now.Add(3 * time.Second)
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process malformed known event: processed=%t err=%v", processed, err)
	}
	var state humancalling.ReceiptState
	var errorCode string
	if err := pool.QueryRow(context.Background(), `
		SELECT state, projection_error_code
		FROM human_calling_provider_receipts
		WHERE event_id = 'malformed-known-event'
	`).Scan(&state, &errorCode); err != nil {
		t.Fatalf("read malformed receipt diagnostics: %v", err)
	}
	if state != humancalling.ReceiptFailed {
		t.Fatalf("malformed receipt state = %q", state)
	}
	if errorCode != "INVALID_PROVIDER_EVENT" {
		t.Fatalf("projection error = %q, want INVALID_PROVIDER_EVENT", errorCode)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || processed {
		t.Fatalf("malformed receipt retried: processed=%t err=%v", processed, err)
	}
}

func TestWaitingProviderReceiptIsAttachedToOperatorTimeline(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionStaff(t, accessModule, now)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(
		pool,
		accessModule,
		&recordingProvider{},
		humancalling.Config{
			SIPDomain:        "synthetic.sip.telnyx.com",
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
			WebhookPublicKey: publicKey,
			WebhookTolerance: 5 * time.Minute,
		},
		func() time.Time { return now },
	)
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-waiting-receipt",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "waiting-receipt-source",
		IdempotencyKey: "waiting-receipt-idempotency",
	})
	if err != nil {
		t.Fatalf("create waiting receipt handoff: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "waiting-receipt-caller",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "waiting-receipt-caller-control",
		CallLegID:     "waiting-receipt-caller-leg",
		CallSessionID: "waiting-receipt-session",
		To:            handoff.SIPDestination,
	}); err != nil {
		t.Fatalf("admit waiting receipt Call: %v", err)
	}
	var callID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM human_calling_calls
		WHERE handoff_id = $1
	`, handoff.ID).Scan(&callID); err != nil {
		t.Fatalf("load waiting receipt Call: %v", err)
	}
	clientState := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"v":1,"call":"%s","leg":"staff","attempt":"00000000-0000-0000-0000-000000000999"}`,
		callID,
	)))
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.bridged","id":"waiting-related-fact","occurred_at":"%s","payload":{"call_control_id":"unknown-staff-control","call_leg_id":"unknown-staff-leg","call_session_id":"waiting-receipt-session","client_state":"%s"}}}`,
		now.Format(time.RFC3339Nano),
		clientState,
	))
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	if _, err := calling.ReceiveWebhook(
		context.Background(),
		raw,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive waiting receipt: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process waiting receipt: processed=%t err=%v", processed, err)
	}
	var attachedCallID, receiptState, errorCode string
	if err := pool.QueryRow(context.Background(), `
		SELECT call_id::text, state, projection_error_code
		FROM human_calling_provider_receipts
		WHERE event_id = 'waiting-related-fact'
	`).Scan(&attachedCallID, &receiptState, &errorCode); err != nil {
		t.Fatalf("read waiting receipt: %v", err)
	}
	if attachedCallID != callID ||
		receiptState != string(humancalling.ReceiptPending) ||
		errorCode != "WAITING_FOR_RELATED_FACT" {
		t.Fatalf(
			"waiting receipt: call=%s state=%s error=%s",
			attachedCallID,
			receiptState,
			errorCode,
		)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO access_platform_operators (email)
		VALUES ('waiting-operator@synthetic.test')
	`); err != nil {
		t.Fatalf("provision waiting receipt operator: %v", err)
	}
	timeline, err := calling.ReadOperatorTimeline(
		context.Background(),
		access.Identity{
			Subject:       "waiting-operator-subject",
			Email:         "waiting-operator@synthetic.test",
			EmailVerified: true,
		},
		callID,
	)
	if err != nil {
		t.Fatalf("read waiting receipt timeline: %v", err)
	}
	found := false
	for _, entry := range timeline.Entries {
		if entry.Kind == "provider.receipt.pending" &&
			entry.ReceiptState == "PENDING" &&
			entry.ErrorCode == "WAITING_FOR_RELATED_FACT" {
			found = true
		}
	}
	if !found {
		t.Fatalf("waiting receipt absent from timeline: %#v", timeline.Entries)
	}
}

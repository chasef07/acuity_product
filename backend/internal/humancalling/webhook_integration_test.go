package humancalling_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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
	"github.com/jackc/pgx/v5/pgxpool"
)

var expectedFastReceiptRetryDelays = [...]time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	32 * time.Second,
	time.Minute,
	time.Minute,
	time.Minute,
}

func TestSignedWebhookCommitsExactReceiptBeforeIdempotentProjection(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeWorker,
		"webhook-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	calling := humancalling.New(pool, accessModule, &recordingProvider{}, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		OfferDuration:    20 * time.Second,
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		WebhookPublicKey: publicKey,
		WebhookTolerance: 5 * time.Minute,
		Observer:         observer,
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
			Phone:          "+15555550100",
			DisplayName:    "Webhook Caller",
			NameSource:     "Abita",
			TransferReason: "Webhook proof",
			ReasonSource:   "Abita AI",
		},
	})
	if err != nil {
		t.Fatalf("create webhook handoff: %v", err)
	}
	if handoff.SIPDestination != "sip:acuity-handoff@synthetic.sip.telnyx.com" {
		t.Fatalf("handoff SIP destination = %q", handoff.SIPDestination)
	}
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.initiated","id":"webhook-event-1","occurred_at":"%s","payload":{"call_control_id":"webhook-caller-control","call_leg_id":"webhook-caller-leg","call_session_id":"webhook-session","client_state":"","from":"+15555550100","to":"+14843336938"}}}`,
		now.Format(time.RFC3339Nano),
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
	if err := calling.ReportReceiptQueue(context.Background()); err != nil {
		t.Fatalf("report receipt queue: %v", err)
	}
	if !strings.Contains(
		metrics.String(),
		`"metric":"acuity_call_center_receipt_processing"`,
	) || !strings.Contains(metrics.String(), `"outcome":"applied"`) ||
		!strings.Contains(
			metrics.String(),
			`"metric":"acuity_call_center_receipt_queue"`,
		) {
		t.Fatalf("receipt metrics = %s", metrics.String())
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
		From:          "+15555550100",
		To:            "+14843336938",
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

func TestSignedReferRejectsAmbiguousReservations(t *testing.T) {
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
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
			WebhookPublicKey: publicKey,
			WebhookTolerance: 5 * time.Minute,
		},
		func() time.Time { return now },
	)
	for index := range 2 {
		key := fmt.Sprintf("ambiguous-refer-%d", index+1)
		if _, err := calling.CreateHandoff(
			context.Background(),
			humancalling.CreateHandoffCommand{
				Service: humancalling.ServiceIdentity{
					Subject:    "abita-ambiguous-refer",
					PracticeID: authorization.Practice.ID,
				},
				LocationID:     authorization.Locations[0].ID,
				SourceCallID:   key,
				IdempotencyKey: key,
				Contact: humancalling.ContactContext{
					Phone: "+15555550100",
				},
			},
		); err != nil {
			t.Fatalf("create reservation %d: %v", index+1, err)
		}
	}
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.initiated","id":"ambiguous-refer","occurred_at":"%s","payload":{"call_control_id":"ambiguous-control","call_leg_id":"ambiguous-leg","call_session_id":"ambiguous-session","from":"+15555550100","to":"+14843336938"}}}`,
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
		t.Fatalf("receive ambiguous REFER: %v", err)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process ambiguous REFER: processed=%t err=%v", processed, err)
	}
	var state humancalling.ReceiptState
	var errorCode string
	var calls, consumed int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			receipt.state,
			receipt.projection_error_code,
			(SELECT count(*) FROM human_calling_calls),
			(SELECT count(*) FROM human_calling_handoffs WHERE consumed_at IS NOT NULL)
		FROM human_calling_provider_receipts receipt
		WHERE receipt.event_id = 'ambiguous-refer'
	`).Scan(&state, &errorCode, &calls, &consumed); err != nil {
		t.Fatalf("read ambiguous REFER outcome: %v", err)
	}
	if state != humancalling.ReceiptFailed ||
		errorCode != "HANDOFF_REJECTED" ||
		calls != 0 ||
		consumed != 0 {
		t.Fatalf(
			"ambiguous REFER state=%s error=%s calls=%d consumed=%d",
			state,
			errorCode,
			calls,
			consumed,
		)
	}
}

func TestRejectedHandoffTerminalizesRelatedLifecycleReceipts(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
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
	receive := func(raw []byte) humancalling.WebhookReceipt {
		t.Helper()
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
			t.Fatalf("receive rejected-leg webhook: %v", err)
		}
		return receipt
	}
	process := func(label string) {
		t.Helper()
		if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
			t.Fatalf("process %s: processed=%t err=%v", label, processed, err)
		}
	}
	raw := func(eventID, eventType string) []byte {
		t.Helper()
		extra := ""
		if eventType == "call.initiated" {
			extra = `,"from":"+15555550100","to":"+14843336938"`
		}
		if eventType == "call.hangup" {
			extra = `,"hangup_cause":"normal_clearing"`
		}
		return []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"%s","id":"%s","occurred_at":"%s","payload":{"call_control_id":"rejected-control","call_leg_id":"rejected-leg","call_session_id":"rejected-session"%s}}}`,
			eventType,
			eventID,
			now.Format(time.RFC3339Nano),
			extra,
		))
	}

	initiated := raw("rejected-initiated", "call.initiated")
	receive(initiated)
	process("rejected initiation")
	if receipt := receive(initiated); receipt.State != humancalling.ReceiptFailed {
		t.Fatalf("rejected initiation receipt = %#v", receipt)
	}

	for _, eventType := range []string{"call.answered", "call.bridged", "call.hangup"} {
		event := raw("rejected-"+strings.TrimPrefix(eventType, "call."), eventType)
		receive(event)
		process(eventType)
		if eventType == "call.hangup" {
			now = now.Add(3 * time.Second)
			process(eventType + " after reorder hold")
		}
		if receipt := receive(event); receipt.State != humancalling.ReceiptFailed {
			t.Fatalf("%s receipt = %#v", eventType, receipt)
		}
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || processed {
		t.Fatalf("rejected leg retained retrying receipts: processed=%t err=%v", processed, err)
	}
}

func TestRetryingReceiptDoesNotStarveNewHandoff(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(pool, accessModule, &recordingProvider{}, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		OfferDuration:    20 * time.Second,
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		WebhookPublicKey: publicKey,
		WebhookTolerance: 5 * time.Minute,
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"non-starved-browser",
		false,
	); err != nil {
		t.Fatalf("acquire softphone: %v", err)
	}
	if _, err := calling.SetReadiness(context.Background(), humancalling.ReadinessCommand{
		Identity:        identity,
		SessionID:       "non-starved-browser",
		Registered:      true,
		MicrophoneReady: true,
		AudioReady:      true,
		SessionHealthy:  true,
		Available:       true,
	}); err != nil {
		t.Fatalf("set readiness: %v", err)
	}
	_, err = calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-non-starved-handoff",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "non-starved-source",
		IdempotencyKey: "non-starved-idempotency",
		Contact: humancalling.ContactContext{
			Phone:       "+15555550100",
			DisplayName: "Non-starved Caller",
		},
	})
	if err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	receive := func(raw []byte) {
		t.Helper()
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
			t.Fatalf("receive webhook: %v", err)
		}
	}
	receive([]byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.answered","id":"waiting-before-handoff","occurred_at":"%s","payload":{"call_control_id":"waiting-control","call_leg_id":"waiting-leg","call_session_id":"waiting-session"}}}`,
		now.Format(time.RFC3339Nano),
	)))
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process waiting receipt: processed=%t err=%v", processed, err)
	}

	now = now.Add(500 * time.Millisecond)
	receive([]byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.initiated","id":"new-handoff-behind-retry","occurred_at":"%s","payload":{"call_control_id":"new-handoff-control","call_leg_id":"new-handoff-leg","call_session_id":"new-handoff-session","from":"+15555550100","to":"+14843336938"}}}`,
		now.Format(time.RFC3339Nano),
	)))
	now = now.Add(600 * time.Millisecond)
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process new handoff receipt: processed=%t err=%v", processed, err)
	}
	offers, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(offers) != 1 || offers[0].DisplayName != "Non-starved Caller" {
		t.Fatalf("new handoff offer = %#v, err = %v", offers, err)
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

func TestSlowRelatedFactReceiptIsVisibleInOperatorTimeline(t *testing.T) {
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
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
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
		Contact: humancalling.ContactContext{
			Phone: "+15555550100",
		},
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
		From:          "+15555550100",
		To:            "+14843336938",
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
	for retry, delay := range expectedFastReceiptRetryDelays {
		now = now.Add(delay)
		if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
			t.Fatalf(
				"process timeline related-fact retry %d: processed=%t err=%v",
				retry+2,
				processed,
				err,
			)
		}
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
			entry.ErrorCode == "WAITING_FOR_RELATED_FACT_SLOW_RETRY" {
			found = true
		}
	}
	if !found {
		t.Fatalf("waiting receipt absent from timeline: %#v", timeline.Entries)
	}
}

func TestRelatedFactReceiptFallsBackToSlowCadenceAndConverges(t *testing.T) {
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
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
			HandoffLifetime:  time.Hour,
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
			WebhookPublicKey: publicKey,
			WebhookTolerance: 5 * time.Minute,
		},
		func() time.Time { return now },
	)
	_, err = calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-late-related-fact",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "late-related-fact-source",
		IdempotencyKey: "late-related-fact-idempotency",
		Contact: humancalling.ContactContext{
			Phone: "+15555550100",
		},
	})
	if err != nil {
		t.Fatalf("create late-related-fact handoff: %v", err)
	}
	receive := func(raw []byte) humancalling.WebhookReceipt {
		t.Helper()
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
			t.Fatalf("receive late-related-fact webhook: %v", err)
		}
		return receipt
	}
	answeredRaw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.answered","id":"answered-before-call","occurred_at":"%s","payload":{"call_control_id":"late-related-control","call_leg_id":"late-related-leg","call_session_id":"late-related-session"}}}`,
		now.Format(time.RFC3339Nano),
	))
	if receipt := receive(answeredRaw); receipt.State != humancalling.ReceiptPending {
		t.Fatalf("initial answer receipt = %#v", receipt)
	}
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process answer before Call: processed=%t err=%v", processed, err)
	}
	for retry, delay := range expectedFastReceiptRetryDelays {
		now = now.Add(delay - time.Millisecond)
		if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || processed {
			t.Fatalf(
				"fast related-fact retry %d ran early: processed=%t err=%v",
				retry+2,
				processed,
				err,
			)
		}
		now = now.Add(time.Millisecond)
		if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
			t.Fatalf(
				"process fast related-fact retry %d: processed=%t err=%v",
				retry+2,
				processed,
				err,
			)
		}
	}
	if receipt := receive(answeredRaw); !receipt.Duplicate ||
		receipt.State != humancalling.ReceiptPending {
		t.Fatalf("slow-wait answer receipt = %#v", receipt)
	}
	now = now.Add(15*time.Minute - time.Second)
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || processed {
		t.Fatalf("slow retry ran early: processed=%t err=%v", processed, err)
	}

	initiatedRaw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.initiated","id":"call-after-answer","occurred_at":"%s","payload":{"call_control_id":"late-related-control","call_leg_id":"late-related-leg","call_session_id":"late-related-session","from":"+15555550100","to":"+14843336938"}}}`,
		now.Format(time.RFC3339Nano),
	))
	receive(initiatedRaw)
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process Call after answer: processed=%t err=%v", processed, err)
	}

	now = now.Add(time.Second)
	if processed, err := calling.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("replay answer after Call: processed=%t err=%v", processed, err)
	}
	if receipt := receive(answeredRaw); !receipt.Duplicate ||
		receipt.State != humancalling.ReceiptApplied {
		t.Fatalf("converged answer receipt = %#v", receipt)
	}
}

func TestReceiptQueueReportsDurableQuarantineDepth(t *testing.T) {
	fixture := newQuarantinedReceiptFixture(t)
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeWorker,
		"receipt-quarantine-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	calling := humancalling.New(
		fixture.pool,
		nil,
		nil,
		humancalling.Config{Observer: observer},
		func() time.Time { return fixture.now },
	)

	if err := calling.ReportReceiptQueue(context.Background()); err != nil {
		t.Fatalf("report receipt queue: %v", err)
	}
	if !strings.Contains(metrics.String(), `"quarantined_depth":1`) {
		t.Fatalf("receipt queue metric = %s", metrics.String())
	}
}

func TestProviderReceiptRequeueRequiresScopedOperatorSupportMode(t *testing.T) {
	fixture := newQuarantinedReceiptFixture(t)
	support := fixture.enterSupportMode(
		t,
		fixture.practiceID,
		"Repair failed provider receipt projection",
	)

	_, err := fixture.calling.RequeueQuarantinedReceipt(
		context.Background(),
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:         fixture.staffIdentity,
			PracticeID:       fixture.practiceID,
			SupportSessionID: support.ID,
			EventID:          fixture.eventID,
		},
	)
	if !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("Staff receipt requeue error = %v, want denied", err)
	}
	_, err = fixture.calling.RequeueQuarantinedReceipt(
		context.Background(),
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:   fixture.operator,
			PracticeID: fixture.practiceID,
			EventID:    fixture.eventID,
		},
	)
	if !errors.Is(err, access.ErrSupportRequired) {
		t.Fatalf("operator requeue without Support Mode error = %v", err)
	}
	if _, err := fixture.access.EnterSupportMode(
		context.Background(),
		access.EnterSupportModeCommand{
			Identity:   fixture.operator,
			PracticeID: fixture.practiceID,
			Reason:     " ",
			Duration:   time.Hour,
		},
	); !errors.Is(err, access.ErrInvalidInput) {
		t.Fatalf("Support Mode without reason error = %v", err)
	}
	otherSupport := fixture.enterSupportMode(
		t,
		fixture.otherPracticeID,
		"Investigate another Practice",
	)
	_, err = fixture.calling.RequeueQuarantinedReceipt(
		context.Background(),
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:         fixture.operator,
			PracticeID:       fixture.practiceID,
			SupportSessionID: otherSupport.ID,
			EventID:          fixture.eventID,
		},
	)
	if !errors.Is(err, access.ErrSupportPracticeMismatch) {
		t.Fatalf("cross-Practice Support Mode error = %v", err)
	}
	_, err = fixture.calling.RequeueQuarantinedReceipt(
		context.Background(),
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:         fixture.operator,
			PracticeID:       fixture.otherPracticeID,
			SupportSessionID: otherSupport.ID,
			EventID:          fixture.eventID,
		},
	)
	if !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("wrong-Practice receipt requeue error = %v, want conflict", err)
	}
	_, err = fixture.calling.RequeueQuarantinedReceipt(
		context.Background(),
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:         fixture.operator,
			PracticeID:       fixture.practiceID,
			SupportSessionID: support.ID,
			EventID:          "missing-provider-receipt",
		},
	)
	if !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("missing receipt requeue error = %v, want conflict", err)
	}
	if receipt := fixture.receive(t); receipt.State != humancalling.ReceiptQuarantined {
		t.Fatalf("rejected requeue changed receipt = %#v", receipt)
	}
}

func TestSupportedProviderReceiptRequeueIsAuditedAndReplaysImmutableEvidence(t *testing.T) {
	fixture := newQuarantinedReceiptFixture(t)
	reason := "Retry verified receipt after projection repair"
	support := fixture.enterSupportMode(t, fixture.practiceID, reason)
	var callID string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT call_id::text
		FROM human_calling_provider_receipts
		WHERE event_id = $1
	`, fixture.eventID).Scan(&callID); err != nil {
		t.Fatalf("read quarantined receipt Call: %v", err)
	}
	timeline, err := fixture.calling.ReadOperatorTimeline(
		context.Background(),
		fixture.operator,
		callID,
	)
	if err != nil {
		t.Fatalf("read quarantined receipt timeline: %v", err)
	}
	recoveryReference := ""
	for _, entry := range timeline.Entries {
		if entry.ReceiptState == string(humancalling.ReceiptQuarantined) {
			recoveryReference = entry.RecoveryReference
		}
	}
	if recoveryReference == "" {
		t.Fatalf("quarantined receipt recovery reference absent: %#v", timeline)
	}

	requeued, err := fixture.calling.RequeueQuarantinedReceipt(
		context.Background(),
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:         fixture.operator,
			PracticeID:       fixture.practiceID,
			SupportSessionID: support.ID,
			ReceiptReference: recoveryReference,
		},
	)
	if err != nil {
		t.Fatalf("supported receipt requeue: %v", err)
	}
	if requeued.EventID != fixture.eventID ||
		requeued.EventType != string(humancalling.FactCallAnswered) ||
		requeued.State != humancalling.ReceiptPending ||
		requeued.Duplicate {
		t.Fatalf("supported requeue = %#v", requeued)
	}
	auditEvents, err := fixture.access.AuditTrail(
		context.Background(),
		fixture.operator,
		fixture.practiceID,
	)
	if err != nil {
		t.Fatalf("read receipt requeue audit: %v", err)
	}
	foundAudit := false
	for _, event := range auditEvents {
		if event.Action != "provider_receipt.requeued" {
			continue
		}
		if event.ActorSubject != fixture.operator.Subject ||
			event.PracticeID != fixture.practiceID ||
			event.SupportSessionID != support.ID ||
			event.Reason != reason {
			t.Fatalf("receipt requeue audit = %#v", event)
		}
		foundAudit = true
	}
	if !foundAudit {
		t.Fatalf("receipt requeue audit absent: %#v", auditEvents)
	}
	fixture.provider.mu.Lock()
	providerCommands := len(fixture.provider.commands)
	fixture.provider.mu.Unlock()
	if providerCommands != 0 {
		t.Fatalf("receipt requeue issued %d provider commands", providerCommands)
	}

	if processed, err := fixture.calling.ProcessNextReceipt(
		context.Background(),
	); err != nil || !processed {
		t.Fatalf("replay supported receipt: processed=%t err=%v", processed, err)
	}
	if receipt := fixture.receive(t); !receipt.Duplicate ||
		receipt.State != humancalling.ReceiptApplied {
		t.Fatalf("replayed immutable receipt = %#v", receipt)
	}
	_, err = fixture.calling.RequeueQuarantinedReceipt(
		context.Background(),
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:         fixture.operator,
			PracticeID:       fixture.practiceID,
			SupportSessionID: support.ID,
			EventID:          fixture.eventID,
		},
	)
	if !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("requeue applied receipt error = %v, want conflict", err)
	}
}

func TestProviderReceiptRequeueRollsBackWhenAuditFails(t *testing.T) {
	fixture := newQuarantinedReceiptFixture(t)
	support := fixture.enterSupportMode(
		t,
		fixture.practiceID,
		"Verify atomic provider receipt requeue",
	)
	if _, err := fixture.pool.Exec(context.Background(), `
		CREATE FUNCTION reject_provider_receipt_requeue_audit()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic provider receipt audit failure';
		END
		$$;

		CREATE TRIGGER reject_provider_receipt_requeue_audit
		BEFORE INSERT ON access_audit_events
		FOR EACH ROW
		WHEN (NEW.action = 'provider_receipt.requeued')
		EXECUTE FUNCTION reject_provider_receipt_requeue_audit()
	`); err != nil {
		t.Fatalf("install receipt audit failure: %v", err)
	}
	command := humancalling.RequeueQuarantinedReceiptCommand{
		Identity:         fixture.operator,
		PracticeID:       fixture.practiceID,
		SupportSessionID: support.ID,
		EventID:          fixture.eventID,
	}
	if _, err := fixture.calling.RequeueQuarantinedReceipt(
		context.Background(),
		command,
	); err == nil {
		t.Fatal("receipt requeue succeeded without its audit")
	}
	if receipt := fixture.receive(t); receipt.State != humancalling.ReceiptQuarantined {
		t.Fatalf("receipt requeue survived audit failure = %#v", receipt)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		DROP TRIGGER reject_provider_receipt_requeue_audit
			ON access_audit_events;
		DROP FUNCTION reject_provider_receipt_requeue_audit()
	`); err != nil {
		t.Fatalf("remove receipt audit failure: %v", err)
	}
	if _, err := fixture.calling.RequeueQuarantinedReceipt(
		context.Background(),
		command,
	); err != nil {
		t.Fatalf("requeue after audit repair: %v", err)
	}
	auditEvents, err := fixture.access.AuditTrail(
		context.Background(),
		fixture.operator,
		fixture.practiceID,
	)
	if err != nil {
		t.Fatalf("read repaired receipt audit: %v", err)
	}
	requeueAudits := 0
	for _, event := range auditEvents {
		if event.Action == "provider_receipt.requeued" {
			requeueAudits++
		}
	}
	if requeueAudits != 1 {
		t.Fatalf("receipt requeue audits = %d, want 1", requeueAudits)
	}
}

type quarantinedReceiptFixture struct {
	pool            *pgxpool.Pool
	now             time.Time
	access          *access.Module
	calling         *humancalling.Module
	provider        *recordingProvider
	staffIdentity   access.Identity
	operator        access.Identity
	practiceID      string
	locationID      string
	otherPracticeID string
	eventID         string
	raw             []byte
	privateKey      ed25519.PrivateKey
}

func newQuarantinedReceiptFixture(t *testing.T) *quarantinedReceiptFixture {
	t.Helper()
	fixture := &quarantinedReceiptFixture{
		pool:     testdb.Open(t),
		now:      time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC),
		provider: &recordingProvider{},
		operator: access.Identity{
			Subject:       "receipt-operator-subject",
			Email:         "receipt-operator@acuity.test",
			EmailVerified: true,
		},
		eventID: "quarantined-projection",
	}
	fixture.access = access.New(fixture.pool, func() time.Time {
		return fixture.now
	})
	staff, staffIdentity := provisionStaff(t, fixture.access, fixture.now)
	fixture.staffIdentity = staffIdentity
	fixture.practiceID = staff.Practice.ID
	fixture.locationID = staff.Locations[0].ID
	if _, err := fixture.access.Provision(
		context.Background(),
		access.Provisioning{
			Environment:       "test",
			RequestedBy:       "receipt-requeue-test",
			PlatformOperators: []string{fixture.operator.Email},
			Practices: []access.PracticeProvision{{
				Key:       "other-receipt-practice",
				Name:      "Other Receipt Practice",
				Locations: []access.LocationProvision{{Key: "office", Name: "Office"}},
			}},
		},
	); err != nil {
		t.Fatalf("provision receipt operator fixture: %v", err)
	}
	discovery, err := fixture.access.DiscoverActor(
		context.Background(),
		fixture.operator,
	)
	if err != nil {
		t.Fatalf("discover receipt operator Practices: %v", err)
	}
	for _, practice := range discovery.Practices {
		if practice.ID != fixture.practiceID {
			fixture.otherPracticeID = practice.ID
		}
	}
	if fixture.otherPracticeID == "" {
		t.Fatal("other receipt Practice was not provisioned")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture.privateKey = privateKey
	fixture.calling = humancalling.New(
		fixture.pool,
		fixture.access,
		fixture.provider,
		humancalling.Config{
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
			WebhookPublicKey: publicKey,
			WebhookTolerance: 5 * time.Minute,
		},
		func() time.Time { return fixture.now },
	)
	_, err = fixture.calling.CreateHandoff(
		context.Background(),
		humancalling.CreateHandoffCommand{
			Service: humancalling.ServiceIdentity{
				Subject:    "receipt-requeue-service",
				PracticeID: fixture.practiceID,
			},
			LocationID:     fixture.locationID,
			SourceCallID:   "receipt-requeue-source",
			IdempotencyKey: "receipt-requeue-idempotency",
			Contact: humancalling.ContactContext{
				Phone: "+15555550100",
			},
		},
	)
	if err != nil {
		t.Fatalf("create receipt-requeue handoff: %v", err)
	}
	if err := fixture.calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       "receipt-requeue-call",
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    fixture.now,
			CallControlID: "receipt-requeue-control",
			CallLegID:     "receipt-requeue-leg",
			CallSessionID: "receipt-requeue-session",
			From:          "+15555550100",
			To:            "+14843336938",
		},
	); err != nil {
		t.Fatalf("admit receipt-requeue Call: %v", err)
	}
	fixture.raw = []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.answered","id":"%s","occurred_at":"%s","payload":{"call_control_id":"receipt-requeue-control","call_leg_id":"receipt-requeue-leg","call_session_id":"receipt-requeue-session"}}}`,
		fixture.eventID,
		fixture.now.Format(time.RFC3339Nano),
	))
	if _, err := fixture.pool.Exec(context.Background(), `
		CREATE FUNCTION reject_receipt_projection_workspace_change()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic receipt projection failure';
		END
		$$;

		CREATE TRIGGER reject_receipt_projection_workspace_change
		BEFORE UPDATE OF workspace_version ON access_practices
		FOR EACH ROW
		EXECUTE FUNCTION reject_receipt_projection_workspace_change()
	`); err != nil {
		t.Fatalf("install receipt projection failure: %v", err)
	}
	if receipt := fixture.receive(t); receipt.State != humancalling.ReceiptPending {
		t.Fatalf("initial failing receipt = %#v", receipt)
	}
	if processed, err := fixture.calling.ProcessNextReceipt(
		context.Background(),
	); err != nil || !processed {
		t.Fatalf("process initial failing receipt: processed=%t err=%v", processed, err)
	}
	for retry, delay := range expectedFastReceiptRetryDelays {
		fixture.now = fixture.now.Add(delay)
		if processed, err := fixture.calling.ProcessNextReceipt(
			context.Background(),
		); err != nil || !processed {
			t.Fatalf(
				"process failing receipt retry %d: processed=%t err=%v",
				retry+2,
				processed,
				err,
			)
		}
	}
	if receipt := fixture.receive(t); receipt.State != humancalling.ReceiptQuarantined {
		t.Fatalf("projection failure receipt = %#v", receipt)
	}
	fixture.allowProjection(t)
	return fixture
}

func (fixture *quarantinedReceiptFixture) receive(
	t *testing.T,
) humancalling.WebhookReceipt {
	t.Helper()
	timestamp := strconv.FormatInt(fixture.now.Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		fixture.privateKey,
		append([]byte(timestamp+"|"), fixture.raw...),
	))
	receipt, err := fixture.calling.ReceiveWebhook(
		context.Background(),
		fixture.raw,
		timestamp,
		signature,
	)
	if err != nil {
		t.Fatalf("receive quarantined receipt: %v", err)
	}
	return receipt
}

func (fixture *quarantinedReceiptFixture) enterSupportMode(
	t *testing.T,
	practiceID string,
	reason string,
) access.SupportMode {
	t.Helper()
	support, err := fixture.access.EnterSupportMode(
		context.Background(),
		access.EnterSupportModeCommand{
			Identity:   fixture.operator,
			PracticeID: practiceID,
			Reason:     reason,
			Duration:   time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("enter receipt Support Mode: %v", err)
	}
	return support
}

func (fixture *quarantinedReceiptFixture) allowProjection(t *testing.T) {
	t.Helper()
	if _, err := fixture.pool.Exec(context.Background(), `
		DROP TRIGGER reject_receipt_projection_workspace_change
			ON access_practices;
		DROP FUNCTION reject_receipt_projection_workspace_change()
	`); err != nil {
		t.Fatalf("remove receipt projection failure: %v", err)
	}
}

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

package messaging_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/messaging"
)

func sendAutomaticAcknowledgement(
	t *testing.T,
	fixture automaticAcknowledgementTestFixture,
) messaging.ProviderCommand {
	t.Helper()
	if queued, err := fixture.module.QueueNextTaskAcknowledgement(context.Background()); err != nil || !queued {
		t.Fatalf("queue automatic acknowledgement = %t, %v", queued, err)
	}
	if processed, err := fixture.module.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("send automatic acknowledgement = %t, %v", processed, err)
	}
	return fixture.provider.commands[len(fixture.provider.commands)-1]
}

func projectDeliveryEvent(
	t *testing.T,
	fixture automaticAcknowledgementTestFixture,
	callbackToken string,
	eventID string,
	eventType string,
	status string,
) {
	t.Helper()
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"%s","id":"%s","occurred_at":"%s","payload":{"id":"provider-message-1","from":{"phone_number":"+17275550100"},"to":[{"phone_number":"%s","status":"%s"}]}}}`,
		eventType,
		eventID,
		fixture.clock.Format(time.RFC3339),
		fixture.task.Phone,
		status,
	))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		fixture.privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	if _, err := fixture.module.ReceiveWebhook(
		context.Background(), callbackToken, raw, timestamp, signature,
	); err != nil {
		t.Fatalf("receive %s %s: %v", eventType, status, err)
	}
	if processed, err := fixture.module.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("project %s %s = %t, %v", eventType, status, processed, err)
	}
}

type acknowledgementEvidence struct {
	delivery       string
	commandState   string
	commandError   string
	receiptState   string
	receiptFailure string
}

func readAcknowledgementEvidence(
	t *testing.T,
	fixture automaticAcknowledgementTestFixture,
	eventID string,
) acknowledgementEvidence {
	t.Helper()
	var evidence acknowledgementEvidence
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT
			message.delivery_state,
			command.state,
			COALESCE(command.last_error_code, ''),
			receipt.state,
			COALESCE(receipt.projection_error_code, '')
		FROM messaging_messages message
		JOIN messaging_provider_commands command ON command.message_id = message.id
		JOIN messaging_provider_receipts receipt ON receipt.event_id = $2
		WHERE message.task_id = $1
	`, fixture.task.ID, eventID).Scan(
		&evidence.delivery,
		&evidence.commandState,
		&evidence.commandError,
		&evidence.receiptState,
		&evidence.receiptFailure,
	); err != nil {
		t.Fatalf("read acknowledgement evidence: %v", err)
	}
	return evidence
}

func TestFinalizedDeliveryStatusesProjectAndIgnoreLateSent(t *testing.T) {
	for status, delivery := range map[string]string{
		"expired":              "FAILED",
		"delivery_unconfirmed": "SENT",
		"delivery_failed":      "FAILED",
		"delivered":            "DELIVERED",
	} {
		t.Run(status, func(t *testing.T) {
			fixture := newAutomaticAcknowledgementTestFixture(t, true)
			command := sendAutomaticAcknowledgement(t, fixture)
			projectDeliveryEvent(t, fixture, command.CallbackToken, "final-"+status, "message.finalized", status)
			projectDeliveryEvent(t, fixture, command.CallbackToken, "late-sent-"+status, "message.sent", "sent")
			for _, eventID := range []string{"final-" + status, "late-sent-" + status} {
				evidence := readAcknowledgementEvidence(t, fixture, eventID)
				if evidence.delivery != delivery ||
					evidence.commandState != "SENT" ||
					evidence.commandError != "" ||
					evidence.receiptState != "APPLIED" ||
					evidence.receiptFailure != "" {
					t.Fatalf("%s evidence = %#v, want delivery %s", eventID, evidence, delivery)
				}
			}
		})
	}
}

func TestReconciliationDoesNotTreatLateSentAsContradictory(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	fixture.provider.sendError = errors.New("provider connection ended without a response")
	fixture.provider.sendResult = messaging.ProviderResult{MessageID: "provider-message-1"}
	command := sendAutomaticAcknowledgement(t, fixture)
	projectDeliveryEvent(t, fixture, command.CallbackToken, "reconcile-final", "message.finalized", "expired")
	if _, err := fixture.pool.Exec(context.Background(), `
		UPDATE messaging_provider_commands
		SET state = 'UNKNOWN', next_attempt_at = $2,
			reconcile_until = $2::timestamptz + interval '1 hour'
		WHERE message_id = (SELECT id FROM messaging_messages WHERE task_id = $1)
	`, fixture.task.ID, *fixture.clock); err != nil {
		t.Fatalf("prepare late reconciliation: %v", err)
	}
	fixture.provider.reconcileResult = messaging.ProviderResult{
		MessageID: "provider-message-1",
		State:     messaging.DeliverySent,
	}
	if reconciled, err := fixture.module.ReconcileNextCommand(context.Background()); err != nil || !reconciled {
		t.Fatalf("reconcile late sent evidence = %t, %v", reconciled, err)
	}
	evidence := readAcknowledgementEvidence(t, fixture, "reconcile-final")
	if evidence.delivery != "FAILED" ||
		evidence.commandState != "FAILED" ||
		evidence.commandError != "" {
		t.Fatalf("late reconciliation evidence = %#v", evidence)
	}
	if reconciled, err := fixture.module.ReconcileNextCommand(context.Background()); err != nil || reconciled {
		t.Fatalf("late sent evidence kept reconciliation polling = %t, %v", reconciled, err)
	}
}

func TestTemporaryProviderUnavailabilityDefersSendWithinWindow(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	fixture.provider.sendError = messaging.ErrTemporary
	if queued, err := fixture.module.QueueNextTaskAcknowledgement(context.Background()); err != nil || !queued {
		t.Fatalf("queue automatic acknowledgement = %t, %v", queued, err)
	}
	if processed, err := fixture.module.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("process rate-limited acknowledgement = %t, %v", processed, err)
	}
	var delivery, commandState, commandError string
	var nextAttemptAt time.Time
	readState := func() {
		t.Helper()
		if err := fixture.pool.QueryRow(context.Background(), `
			SELECT message.delivery_state, command.state,
				COALESCE(command.last_error_code, ''), command.next_attempt_at
			FROM messaging_messages message
			JOIN messaging_provider_commands command ON command.message_id = message.id
			WHERE message.task_id = $1
		`, fixture.task.ID).Scan(&delivery, &commandState, &commandError, &nextAttemptAt); err != nil {
			t.Fatalf("read deferred command: %v", err)
		}
	}
	readState()
	if delivery != "SENDING" ||
		commandState != "PENDING" ||
		commandError != "PROVIDER_TEMPORARILY_UNAVAILABLE" ||
		!nextAttemptAt.Equal(fixture.clock.Add(15*time.Second)) {
		t.Fatalf("deferred command = %s/%s/%s at %s", delivery, commandState, commandError, nextAttemptAt)
	}
	if processed, err := fixture.module.ProcessNextCommand(context.Background()); err != nil || processed {
		t.Fatalf("deferred command retried before its delay = %t, %v", processed, err)
	}
	*fixture.clock = fixture.clock.Add(15 * time.Second)
	if processed, err := fixture.module.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("retry deferred command = %t, %v", processed, err)
	}
	readState()
	if delivery != "SENT" || commandState != "SENT" || len(fixture.provider.commands) != 2 {
		t.Fatalf("retried command = %s/%s with %d provider requests", delivery, commandState, len(fixture.provider.commands))
	}
}

func TestTemporaryProviderUnavailabilityFailsAfterWindow(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	if queued, err := fixture.module.QueueNextTaskAcknowledgement(context.Background()); err != nil || !queued {
		t.Fatalf("queue automatic acknowledgement = %t, %v", queued, err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		UPDATE messaging_provider_commands
		SET created_at = $2::timestamptz - interval '5 minutes'
		WHERE message_id = (SELECT id FROM messaging_messages WHERE task_id = $1)
	`, fixture.task.ID, *fixture.clock); err != nil {
		t.Fatalf("age provider command: %v", err)
	}
	fixture.provider.sendError = messaging.ErrTemporary
	if processed, err := fixture.module.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("process exhausted rate-limited acknowledgement = %t, %v", processed, err)
	}
	var delivery, failure, commandState string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT message.delivery_state, COALESCE(message.safe_failure_code, ''), command.state
		FROM messaging_messages message
		JOIN messaging_provider_commands command ON command.message_id = message.id
		WHERE message.task_id = $1
	`, fixture.task.ID).Scan(&delivery, &failure, &commandState); err != nil {
		t.Fatalf("read exhausted command: %v", err)
	}
	if delivery != "FAILED" ||
		failure != "PROVIDER_TEMPORARILY_UNAVAILABLE" ||
		commandState != "FAILED" {
		t.Fatalf("exhausted temporary failure = %s/%s/%s", delivery, failure, commandState)
	}
}

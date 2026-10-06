package humancalling_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestProviderWebhookRetryWithNewDeliveryMetadataIsDuplicate(t *testing.T) {
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
	data := fmt.Sprintf(
		`{"record_type":"event","event_type":"call.answered","id":"webhook-retry-event","occurred_at":"%s","payload":{"connection_id":"expected-connection","call_control_id":"retry-control","call_leg_id":"retry-leg","call_session_id":"retry-session"}}`,
		now.Format(time.RFC3339Nano),
	)
	receive := func(raw string) (humancalling.WebhookReceipt, error) {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			privateKey,
			append([]byte(timestamp+"|"), raw...),
		))
		return calling.ReceiveWebhook(context.Background(), []byte(raw), timestamp, signature)
	}
	firstDelivery := `{"data":` + data + `,"meta":{"attempt":1,"delivered_to":"https://ingress.example/calls"}}`
	first, err := receive(firstDelivery)
	if err != nil || first.Duplicate {
		t.Fatalf("receive first delivery = %#v, %v", first, err)
	}
	retry, err := receive(`{"data":` + data + `,"meta":{"attempt":2,"delivered_to":"https://ingress.example/calls"}}`)
	if err != nil || !retry.Duplicate || retry.DuplicateCount != 1 {
		t.Fatalf("receive provider retry = %#v, %v", retry, err)
	}
	changed := fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.answered","id":"webhook-retry-event","occurred_at":"%s","payload":{"connection_id":"expected-connection","call_control_id":"other-control","call_leg_id":"retry-leg","call_session_id":"retry-session"}},"meta":{"attempt":3}}`,
		now.Format(time.RFC3339Nano),
	)
	if _, err := receive(changed); !errors.Is(err, humancalling.ErrInvalidWebhook) {
		t.Fatalf("receive changed event under reused id error = %v, want invalid webhook", err)
	}
	var stored []byte
	var duplicateCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT raw_body, duplicate_count
		FROM human_calling_provider_receipts
		WHERE event_id = 'webhook-retry-event'
	`).Scan(&stored, &duplicateCount); err != nil {
		t.Fatal(err)
	}
	if duplicateCount != 1 || string(stored) != firstDelivery {
		t.Fatalf("stored receipt = %s with %d duplicates", stored, duplicateCount)
	}
}

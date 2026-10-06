package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestReceiptRetryDelayBacksOffToCeiling(t *testing.T) {
	for attempt, want := range map[int]time.Duration{
		1:                            5 * time.Second,
		2:                            10 * time.Second,
		5:                            80 * time.Second,
		10:                           maxReceiptRetryDelay,
		maxReceiptProjectionAttempts: maxReceiptRetryDelay,
	} {
		if got := receiptRetryDelay(attempt); got != want {
			t.Errorf("attempt %d delay = %s, want %s", attempt, got, want)
		}
	}
	var budget time.Duration
	for attempt := 1; attempt < maxReceiptProjectionAttempts; attempt++ {
		budget += receiptRetryDelay(attempt)
	}
	if budget < time.Hour {
		t.Fatalf("retry budget %s quarantines receipts before a sustained failure is visible", budget)
	}
}

func TestReceiptFailureCodeKeepsOnlyClassification(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("save: %w", &pgconn.PgError{Code: "23514", Message: "synthetic +15555550100"}), "DATABASE_23514"},
		{fmt.Errorf("save: %w", context.DeadlineExceeded), "TIMEOUT"},
		{errors.New("synthetic +15555550100 failure"), "PROJECTION_FAILED"},
	} {
		if got := receiptFailureCode(c.err); got != c.want {
			t.Errorf("receiptFailureCode(%v) = %s, want %s", c.err, got, c.want)
		}
	}
}

func TestReceiptPayloadRoundTripsIngestCommand(t *testing.T) {
	ended := time.Date(2026, 9, 1, 12, 5, 0, 0, time.UTC)
	command := IngestCommand{
		Kind:            MessageCloseout,
		OfficeKey:       "synthetic-office",
		SourceCallID:    "synthetic-call",
		CallerPhone:     "+15555550123",
		OfficePhone:     "+15555550100",
		StartedAt:       ended.Add(-5 * time.Minute),
		EndedAt:         &ended,
		Status:          CallCompleted,
		Summary:         "Synthetic summary",
		Transcript:      json.RawMessage(`{"items":[]}`),
		CloseoutPayload: json.RawMessage(`{"reason":"session_closed"}`),
		Appointment:     &AppointmentEvidence{Action: AppointmentBooked, OccurredAt: ended, NewAppointmentID: "synthetic-appointment", BookingResult: json.RawMessage(`{"status":"booked"}`)},
	}
	command.Service.Subject = "synthetic-agent"
	raw, _, err := receiptPayload(command)
	if err != nil {
		t.Fatal(err)
	}
	receipt := acceptedReceipt{ServiceSubject: "synthetic-agent", SourceCallID: "synthetic-call"}
	decoded, stage, valid := receiptCommand(receipt, raw)
	if !valid || stage != LifecycleClosed || !reflect.DeepEqual(decoded, command) {
		t.Fatalf("decoded = %+v stage=%d valid=%t", decoded, stage, valid)
	}
	receipt.SourceCallID = "different-call"
	if _, _, valid := receiptCommand(receipt, raw); valid {
		t.Fatal("receipt accepted a payload for another source call")
	}
	if _, _, valid := receiptCommand(receipt, []byte(`{`)); valid {
		t.Fatal("receipt accepted corrupt payload")
	}
}

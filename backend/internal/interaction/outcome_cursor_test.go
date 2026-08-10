package interaction

import (
	"testing"
	"time"
)

func TestOutcomeCursorRoundTrip(t *testing.T) {
	occurredAt := time.Date(2026, time.August, 10, 9, 30, 0, 123000, time.UTC)
	encoded, err := encodeOutcomeCursor(OutcomeItem{
		ID:                    "00000000-0000-0000-0000-000000000101",
		AppointmentOccurredAt: &occurredAt,
	})
	if err != nil {
		t.Fatalf("encode AI outcome cursor: %v", err)
	}
	cursor, err := decodeOutcomeCursor(encoded)
	if err != nil {
		t.Fatalf("decode AI outcome cursor: %v", err)
	}
	if !cursor.Present || cursor.ID != "00000000-0000-0000-0000-000000000101" ||
		!cursor.OccurredAt.Equal(occurredAt) {
		t.Fatalf("AI outcome cursor = %#v", cursor)
	}
}

func TestOutcomeCursorRejectsMalformedInput(t *testing.T) {
	for _, encoded := range []string{
		"not-base64",
		"e30",
		"eyJvY2N1cnJlZEF0IjoiMjAyNi0wOC0xMFQwOTozMDowMFoiLCJpZCI6Im5vdC1hLXV1aWQifQ",
	} {
		if _, err := decodeOutcomeCursor(encoded); err == nil {
			t.Fatalf("decode malformed AI outcome cursor %q succeeded", encoded)
		}
	}
}

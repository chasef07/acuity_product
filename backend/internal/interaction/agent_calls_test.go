package interaction

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestAgentCallRequiresAppointmentOutcomeEvidence(t *testing.T) {
	cases := []struct {
		name     string
		stored   Interaction
		receipts string
		want     []AppointmentAction
	}{
		{"attempt alone", Interaction{AppointmentAction: AppointmentBooked}, `[]`, []AppointmentAction{}},
		{"claim without evidence", Interaction{}, `[{"outcome":"booked","status":"success"}]`, []AppointmentAction{}},
		{"empty evidence", Interaction{}, `[{"outcome":"booked","status":"success","evidence":{}}]`, []AppointmentAction{}},
		{"claim does not override receipts", Interaction{AppointmentOutcome: OutcomeCancellation}, `[{"outcome":"booked","status":"success"}]`, []AppointmentAction{}},
		{"blocked receipt", Interaction{AppointmentOutcome: OutcomeBooking}, `[{"outcome":"booked","status":"blocked"}]`, []AppointmentAction{}},
		{"confirmed booking", Interaction{AppointmentOutcome: OutcomeBooking}, ``, []AppointmentAction{AppointmentBooked}},
		{"confirmed reschedule", Interaction{AppointmentOutcome: OutcomeReschedule}, ``, []AppointmentAction{AppointmentRescheduled}},
		{"partial reschedule", Interaction{AppointmentOutcome: OutcomePartial, BookingResult: json.RawMessage(`{"status":"booked"}`)}, ``, []AppointmentAction{AppointmentBooked}},
		{"partial receipt with confirmed replacement", Interaction{AppointmentOutcome: OutcomePartial}, `[{"outcome":"rescheduled","status":"partial","evidence":{"bookingResult":{"status":"booked","appointmentId":"synthetic-new"},"cancellationResult":{"status":"unconfirmed"}}}]`, []AppointmentAction{AppointmentBooked}},
		{"partial receipt with confirmed cancellation", Interaction{}, `[{"outcome":"rescheduled","status":"partial","evidence":{"bookingResult":{"status":"error"},"cancellationResult":{"status":"cancelled"}}}]`, []AppointmentAction{AppointmentCancelled}},
		{"partial receipt without confirmed leg", Interaction{AppointmentOutcome: OutcomeBooking}, `[{"outcome":"rescheduled","status":"partial","evidence":{"bookingResult":{"status":"partial"},"cancellationResult":{"status":"unconfirmed"}}}]`, []AppointmentAction{}},
		{"partial receipt replay excluded", Interaction{AppointmentOutcome: OutcomeBooking}, `[{"outcome":"rescheduled","status":"partial","evidence":{"replayed":true,"bookingResult":{"status":"booked"}}}]`, []AppointmentAction{}},
		{"non-appointment partial receipt excluded", Interaction{}, `[{"outcome":"staff_task_created","status":"partial","evidence":{"bookingResult":{"status":"booked"}}}]`, []AppointmentAction{}},
		{"multiple actions deduplicated", Interaction{}, `[{"outcome":"booked","status":"success","evidence":{"appointmentId":"synthetic-new"}},{"outcome":"booked","status":"success","evidence":{"appointmentId":"synthetic-new"}},{"outcome":"cancelled","status":"success","evidence":{"cancelledAppointmentId":"synthetic-old"}}]`, []AppointmentAction{AppointmentBooked, AppointmentCancelled}},
		{"replay excluded", Interaction{AppointmentOutcome: OutcomeBooking}, `[{"outcome":"booked","status":"success","evidence":{"replayed":true}}]`, []AppointmentAction{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agentCall(c.stored, json.RawMessage(c.receipts), false)
			if !reflect.DeepEqual(got.AppointmentActions, c.want) {
				t.Fatalf("actions = %v, want %v", got.AppointmentActions, c.want)
			}
			if got.DurationSeconds != nil {
				t.Fatal("missing end time must not produce a fabricated duration")
			}
		})
	}
}

func TestCursorsBindTheirQuery(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	command := QueryAnalyticsCommand{PracticeID: "00000000-0000-0000-0000-000000000001", Range: AnalyticsRange7Days, Limit: 10}
	position := analyticsCursor{}
	encoded, err := encodeCursor(agentCallsCursor{analyticsCursor: newAnalyticsCursor(command, now.Add(-time.Hour), "00000000-0000-0000-0000-000000000002", now), Phone: "5555", FlaggedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	command.Cursor = encoded
	var cursor agentCallsCursor
	if !decodeCursor(command, &cursor, &cursor.analyticsCursor, now) || cursor.Phone != "5555" || !cursor.FlaggedOnly || !cursor.StartedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("agent call cursor = %+v", cursor)
	}
	if !decodeCursor(command, &position, &position, now) || position.ID != cursor.ID {
		t.Fatalf("analytics cursor = %+v", position)
	}
	for name, change := range map[string]func(*QueryAnalyticsCommand){
		"location":     func(c *QueryAnalyticsCommand) { c.LocationID = "00000000-0000-0000-0000-000000000003" },
		"range":        func(c *QueryAnalyticsCommand) { c.Range = AnalyticsRange30Days },
		"corrupt":      func(c *QueryAnalyticsCommand) { c.Cursor = "%%%" },
		"not a cursor": func(c *QueryAnalyticsCommand) { c.Cursor = "e30" },
	} {
		changed := command
		change(&changed)
		var decoded analyticsCursor
		if decodeCursor(changed, &decoded, &decoded, now) {
			t.Errorf("%s: cursor accepted for a different query", name)
		}
	}
	var future analyticsCursor
	if decodeCursor(command, &future, &future, now.Add(-time.Minute)) {
		t.Error("cursor from the future accepted")
	}
	stale, err := encodeCursor(newAnalyticsCursor(command, now.Add(-8*24*time.Hour), cursor.ID, now))
	if err != nil {
		t.Fatal(err)
	}
	command.Cursor = stale
	var outside analyticsCursor
	if decodeCursor(command, &outside, &outside, now) {
		t.Error("cursor outside its range accepted")
	}
}

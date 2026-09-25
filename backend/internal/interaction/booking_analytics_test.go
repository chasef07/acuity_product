package interaction

import (
	"testing"
	"time"
)

func TestBookingAnalyticsCalendarDaysAndPooledPercentiles(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 3, 7, 0, 0, 0, 0, zone)
	to := from.AddDate(0, 0, 3)
	first := from.Add(23 * time.Hour)
	second := from.AddDate(0, 0, 1).Add(23 * time.Hour) // next local midnight after the DST change
	end1, end2 := first.Add(100*time.Second), second.Add(300*time.Second)
	report := summarizeBookingFacts([]bookingFact{
		{appointmentID: "first", started: first, ended: &end1, booked: true, searched: true, patientGroup: "new"},
		{appointmentID: "second", started: second, ended: &end2, booked: true, searched: true, patientGroup: "existing"},
		{started: first, booked: false, searched: true, patientGroup: "unknown"},
	}, from, to)
	if len(report.Daily) != 3 || report.Daily[1].Day != "2026-03-08" || report.Daily[2].Existing.Bookings != 1 {
		t.Fatalf("DST daily buckets: %+v", report.Daily)
	}
	if report.Total.P50 == nil || *report.Total.P50 != 200 || report.Total.P90 == nil || *report.Total.P90 != 280 {
		t.Fatalf("pooled percentiles: %+v", report.Total)
	}
	if report.Daily[1].New.P50 != nil || report.Daily[1].New.Conversion != nil {
		t.Fatal("missing samples rendered as zero")
	}
}

func TestBookingWithoutSearchDoesNotConvertAnotherCall(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	report := summarizeBookingFacts([]bookingFact{
		{started: from, searched: true, patientGroup: "new"},
		{started: from.Add(time.Hour), booked: true, appointmentID: "synthetic-booking", patientGroup: "new"},
	}, from, from.AddDate(0, 0, 7))
	if report.Total.Searched != 1 || report.Total.Converted != 0 || report.Total.Bookings != 1 {
		t.Fatalf("a booking cannot convert another call: %+v", report.Total)
	}
}

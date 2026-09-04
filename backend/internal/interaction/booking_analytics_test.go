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

func TestBookingAnalyticsCountsAnAppointmentOnceButConversionPerCall(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	report := summarizeBookingFacts([]bookingFact{
		{appointmentID: "same-appointment", started: from, booked: true, searched: true, patientGroup: "new"},
		{appointmentID: "same-appointment", started: from.AddDate(0, 0, 1), booked: true, searched: true, patientGroup: "existing"},
	}, from, from.AddDate(0, 0, 2))
	if report.Total.Bookings != 1 || report.Total.Converted != 2 || report.Groups.New.Bookings != 1 || report.Groups.Existing.Bookings != 0 || report.Daily[1].Existing.Bookings != 0 {
		t.Fatalf("duplicate booking reporting: %+v", report)
	}
}

func TestBookingDailyTotalIncludesUnclassifiedCallsAndPoolsDurations(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var facts []bookingFact
	for i, group := range []string{"new", "existing", "unknown", "unknown"} {
		end := from.Add(time.Duration(100+i*100) * time.Second)
		facts = append(facts, bookingFact{appointmentID: group, started: from, ended: &end, booked: true, searched: true, patientGroup: group})
	}
	facts = append(facts, bookingFact{started: from, patientGroup: "unknown"})
	report := summarizeBookingFacts(facts, from, from.AddDate(0, 0, 1))
	daily := report.Daily[0].Total
	if daily.Bookings != 3 || daily.Calls != 5 || daily.SearchEvidenceCalls != 4 || daily.PreciseSearchCalls != 0 || daily.Searched != 4 || daily.Converted != 4 || daily.DurationSamples != 4 || daily.P50 == nil || *daily.P50 != 250 || daily.P90 == nil || *daily.P90 != 370 {
		t.Fatalf("daily total must aggregate all source observations: %+v", daily)
	}
}

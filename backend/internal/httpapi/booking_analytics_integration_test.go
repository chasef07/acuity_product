package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestBookingAnalyticsAdminScopeAndDurableEvidence(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	accessModule := access.New(pool, nil)
	admin := access.Identity{Subject: "booking-admin", Email: "booking-admin@acuity.test", EmailVerified: true}
	staff := access.Identity{Subject: "booking-staff", Email: "booking-staff@acuity.test", EmailVerified: true}
	operator := access.Identity{Subject: "booking-operator", Email: "booking-operator@acuity.test", EmailVerified: true}
	_, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "booking-analytics-test", PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{
			{Key: "booking", Name: "Booking Practice", Locations: []access.LocationProvision{{Key: "north", Name: "North"}, {Key: "south", Name: "South"}}, AccessGrants: []access.AccessGrantProvision{
				{Key: "admin", Email: admin.Email, Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
				{Key: "staff", Email: staff.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
			}},
			{Key: "other", Name: "Other Practice", Locations: []access.LocationProvision{{Key: "other", Name: "Other"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []access.Identity{admin, staff} {
		testaccess.Activate(t, accessModule, identity)
	}
	if _, err := accessModule.DiscoverActor(ctx, operator); err != nil {
		t.Fatal(err)
	}
	var practice, north, south, otherPractice, otherLocation string
	if err := pool.QueryRow(ctx, `SELECT p.id::text, n.id::text, s.id::text FROM access_practices p JOIN access_locations n ON n.practice_id=p.id AND n.provisioning_key='north' JOIN access_locations s ON s.practice_id=p.id AND s.provisioning_key='south' WHERE p.provisioning_key='booking'`).Scan(&practice, &north, &south); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT p.id::text, l.id::text FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='other'`).Scan(&otherPractice, &otherLocation); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	yesterday := time.Date(now.Year(), now.Month(), now.Day()-1, 12, 0, 0, 0, time.UTC)
	for index, fixture := range []struct {
		location, group            string
		booked, searched, evidence bool
		start                      time.Time
	}{
		{north, "patient_new", true, true, true, yesterday},
		{north, "patient_verified", false, true, true, yesterday},
		{north, "", true, false, false, yesterday.Add(-24 * time.Hour)},
		{south, "patient_verified", true, true, true, yesterday},
		{north, "patient_verified", true, true, true, now}, // incomplete reporting day excluded
		{north, "patient_verified", true, true, true, yesterday.Add(-10 * 24 * time.Hour)},
	} {
		id := "10000000-0000-0000-0000-00000000000" + string(rune('1'+index))
		var transcript map[string]any
		if fixture.evidence {
			items := []map[string]any{}
			if fixture.searched {
				if fixture.group == "patient_new" {
					items = append(items, map[string]any{"type": "function_call", "name": "add_patient", "call_id": "patient-1"})
				}
				items = append(items,
					map[string]any{"type": "function_call", "name": "get_availability", "call_id": "search-1"},
					map[string]any{"type": "function_call_output", "call_id": "search-1", "is_error": false},
					map[string]any{"type": "function_call", "name": "get_availability", "call_id": "search-2"},
					map[string]any{"type": "function_call_output", "call_id": "search-2", "is_error": true})
			}
			transcript = map[string]any{"chat_history": map[string]any{"items": items}}
		}
		outcome := "INDETERMINATE"
		if fixture.booked {
			outcome = "BOOKING"
		}
		insertOperatorAIInteraction(t, pool, operatorAIInteractionFixture{
			ID: id, PracticeID: practice, LocationID: fixture.location, SourceCall: id, Phone: fmt.Sprintf("+172755501%02d", index), StartedAt: fixture.start, EndedAt: fixture.start.Add(300 * time.Second),
			Status: "COMPLETED", Summary: "PRIVATE-CALL-CONTENT", Transcript: transcript, AppointmentOutcome: outcome, BookingResult: map[string]any{"status": "booked"},
			Closeout: map[string]any{"domainOutcomes": []map[string]any{{"outcome": fixture.group, "status": "success"}}},
		})
		if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET new_appointment_id=$2, appointment_occurred_at=started_at+interval '200 seconds' WHERE id=$1::uuid`, id, "appointment-"+id); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := newPortalHandler(t, httpapi.Config{AcquireTimeout: time.Second}, pool, accessModule, staticAuthenticator{"admin": admin, "staff": staff, "operator": operator})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	query := func(token, practiceID, locationID, timeZone string, status int) []byte {
		t.Helper()
		body := map[string]any{"practiceId": practiceID, "days": 7, "timeZone": timeZone}
		if locationID != "" {
			body["locationId"] = locationID
		}
		encoded, _ := json.Marshal(body)
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/analytics/bookings/query", token, encoded)
		raw := readBody(t, response)
		if response.StatusCode != status {
			t.Fatalf("query status=%d want=%d body=%s", response.StatusCode, status, raw)
		}
		return []byte(raw)
	}
	query("", practice, north, "UTC", 401)
	query("staff", practice, north, "UTC", 403)
	query("admin", otherPractice, "", "UTC", 403)
	query("admin", practice, otherLocation, "UTC", 403)
	query("admin", practice, north, "invalid", 400)
	raw := query("admin", practice, north, "UTC", 200)
	var report interaction.BookingAnalytics
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Total.Calls != 3 || report.Total.Bookings != 2 || report.Total.Searched != 2 || report.Total.Converted != 1 || report.Total.SearchEvidenceCalls != 2 || report.Total.PreciseSearchCalls != 0 || report.Total.Conversion == nil || *report.Total.Conversion != 50 {
		t.Fatalf("unexpected totals: %+v", report.Total)
	}
	if len(report.Daily) != 7 || report.Groups.New.Bookings != 2 || report.Groups.Unknown.Bookings != 0 || report.Total.P50 == nil || *report.Total.P50 != 300 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if strings.Contains(string(raw), "PRIVATE-CALL-CONTENT") || strings.Contains(string(raw), "1727555") || strings.Contains(string(raw), "appointment-") {
		t.Fatal("analytics leaked source content")
	}
	if report.Total.P90 == nil || math.Abs(*report.Total.P90-300) > .001 {
		t.Fatalf("full booked-call duration: %+v", report.Total)
	}
	if err := json.Unmarshal(query("admin", practice, "", "UTC", 200), &report); err != nil {
		t.Fatal(err)
	}
	if report.Total.Bookings != 3 || report.Total.Searched != 3 {
		t.Fatalf("all-office totals: %+v", report.Total)
	}
	if err := json.Unmarshal(query("operator", otherPractice, "", "UTC", 200), &report); err != nil {
		t.Fatal(err)
	}
	if report.Total.Conversion != nil || report.Total.P50 != nil || report.Total.Bookings != 0 {
		t.Fatalf("empty report: %+v", report.Total)
	}
	// The review must partition the exact same denominator, without source content.
	reviewBody, _ := json.Marshal(map[string]any{"practiceId": practice, "locationId": north, "days": 7, "timeZone": "UTC"})
	for _, check := range []struct {
		token  string
		status int
	}{{"", 401}, {"staff", 403}, {"admin", 403}, {"operator", 200}} {
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/analytics/bookings/non-conversions/query", check.token, reviewBody)
		raw := readBody(t, response)
		if response.StatusCode != check.status {
			t.Fatalf("review status=%d want=%d body=%s", response.StatusCode, check.status, raw)
		}
		if check.status != 200 {
			continue
		}
		var review interaction.BookingNonConversions
		if err := json.Unmarshal([]byte(raw), &review); err != nil {
			t.Fatal(err)
		}
		if len(review.Calls) != 1 || review.Calls[0].ID != "10000000-0000-0000-0000-000000000002" || review.Calls[0].LocationID != north || review.Calls[0].PatientGroup != "existing" {
			t.Fatalf("wrong review cohort: %+v", review.Calls)
		}
		if len(review.Calls) != review.Report.Total.Searched-review.Report.Total.Converted || review.Report.Total.Calls != 3 || review.Report.Total.Bookings != 2 {
			t.Fatalf("review denominator drift: %+v", review.Report.Total)
		}
		if strings.Contains(raw, "PRIVATE-CALL-CONTENT") || strings.Contains(raw, "1727555") {
			t.Fatal("review list leaked transcript or contact context")
		}
	}
	for _, location := range []string{south, otherLocation} {
		body, _ := json.Marshal(map[string]any{"practiceId": practice, "locationId": location, "days": 7, "timeZone": "UTC"})
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/analytics/bookings/non-conversions/query", "operator", body)
		raw := readBody(t, response)
		if location == otherLocation {
			if response.StatusCode != 403 {
				t.Fatalf("cross-practice location allowed: %d", response.StatusCode)
			}
		} else {
			var review interaction.BookingNonConversions
			if response.StatusCode != 200 {
				t.Fatalf("empty review status: %d", response.StatusCode)
			}
			if err := json.Unmarshal([]byte(raw), &review); err != nil {
				t.Fatal(err)
			}
			if len(review.Calls) != 0 || review.Calls == nil {
				t.Fatal("empty review must be an empty array")
			}
		}
	}

	// Customer access never unlocks the existing operator evidence endpoint.
	response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/operator/ai-analytics/query", "admin", []byte(`{"practiceId":"`+practice+`","range":"7d","limit":1}`))
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("operator evidence status=%d", response.StatusCode)
	}
	// Calls from the same number remain independent, including after one books.
	repeatID := "10000000-0000-0000-0000-000000000099"
	insertOperatorAIInteraction(t, pool, operatorAIInteractionFixture{
		ID: repeatID, PracticeID: practice, LocationID: north, SourceCall: repeatID, Phone: "+17275550101", StartedAt: yesterday.Add(time.Hour), EndedAt: yesterday.Add(time.Hour + 120*time.Second),
		Status: "COMPLETED", AppointmentOutcome: "INDETERMINATE", Transcript: map[string]any{"items": []map[string]any{{"type": "function_call", "name": "get_availability", "call_id": "repeat"}, {"type": "function_call_output", "call_id": "repeat", "is_error": false}}},
		Closeout: map[string]any{"domainOutcomes": []map[string]any{{"outcome": "patient_verified", "status": "success"}}},
	})
	readReview := func() interaction.BookingNonConversions {
		t.Helper()
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/analytics/bookings/non-conversions/query", "operator", reviewBody)
		raw := readBody(t, response)
		if response.StatusCode != 200 {
			t.Fatalf("review status %d: %s", response.StatusCode, raw)
		}
		var result interaction.BookingNonConversions
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	repeated := readReview()
	if len(repeated.Calls) != 2 || repeated.Calls[0].ID != repeatID || repeated.Report.Total.Searched != 3 {
		t.Fatalf("repeat miss must count as a separate call: %+v", repeated)
	}
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET appointment_outcome='BOOKING',new_appointment_id='repeat-booking',booking_result='{"status":"booked"}' WHERE id=$1::uuid`, repeatID); err != nil {
		t.Fatal(err)
	}
	resolved := readReview()
	if len(resolved.Calls) != 1 || resolved.Calls[0].ID != "10000000-0000-0000-0000-000000000002" || resolved.Report.Total.Converted != 2 {
		t.Fatalf("later booking must not hide earlier missed call: %+v", resolved)
	}

}

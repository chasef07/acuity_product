package interaction

import (
	"encoding/json"
	"testing"
)

func TestEligibilityProjectionKeepsBenefitsAndSeparatesChecks(t *testing.T) {
	raw := json.RawMessage(`{"eligibilityChecks":[{"status":"complete","request":{"firstName":"Ane","lastName":"Example","memberId":"synthetic-4821","plan":"Example Plan"},"result":{"status":"active","checkedAt":"2026-09-23T12:00:00Z","identity":{"status":"matched_with_name_correction","reviewRequired":false},"matchedPatient":{"firstName":"Jane","lastName":"Example"},"providerResponse":{"subscriber":{"memberId":"private-member","dateOfBirth":"19800102"},"x12":"private-x12","benefitsInformation":[{"code":"B","serviceTypeCodes":["98","AL"],"benefitAmount":"0","additionalInformation":[{"description":"Specific provider tier"}],"futureField":{"kept":true,"sequence":9007199254740993}}]}}},{"status":"unavailable","request":{"firstName":"John","lastName":"Other","plan":"Other Plan"},"failureReason":"request_failed"}]}`)
	got := ProjectEligibilityChecks(Interaction{CloseoutPayload: raw})
	if len(got) != 2 || got[0].Status != "active" || got[0].PatientName != "Jane Example" || got[0].SubmittedName != "Ane Example" || got[0].MemberIDLast4 != "4821" || got[1].Status != "unavailable" || got[1].PatientName != "John Other" {
		t.Fatalf("unexpected check projection: %#v", got)
	}
	future := got[0].Benefits[0]["futureField"].(map[string]any)
	if future["sequence"] != json.Number("9007199254740993") {
		t.Fatal("benefit detail lost integer precision")
	}
	if len(got[0].Benefits) != 1 || got[0].Benefits[0]["benefitAmount"] != "0" || got[0].Benefits[0]["futureField"] == nil {
		t.Fatal("benefit evidence lost")
	}
}

func TestEligibilityProjectionDoesNotInferCoverageFromPayerRows(t *testing.T) {
	for _, status := range []string{"review", "unknown", "inactive"} {
		raw := json.RawMessage(`{"eligibilityChecks":[{"status":"complete","result":{"status":"` + status + `","identity":{"status":"exact_name_dob","reviewRequired":false},"providerResponse":{"benefitsInformation":[{"code":"1","serviceTypeCodes":["30"]}]}}}]}`)
		if got := ProjectEligibilityChecks(Interaction{CloseoutPayload: raw}); len(got) != 1 || got[0].Status != status {
			t.Fatalf("status overwritten by raw benefits: %#v", got)
		}
	}
	raw := json.RawMessage(`{"eligibilityChecks":[{"status":"complete","result":{"status":"active","identity":{"status":"identity_conflict","reviewRequired":true}}}]}`)
	if got := ProjectEligibilityChecks(Interaction{CloseoutPayload: raw}); got[0].Status != "review" {
		t.Fatal("unmatched patient shown active")
	}
}

func TestProviderEligibilityUsesExactBatchProviderAndAppointmentEvent(t *testing.T) {
	response := func(profile, status string) map[string]any {
		return map[string]any{"provider": map[string]any{"profileId": profile, "firstName": "Synthetic", "lastName": profile, "npi": "synthetic-npi"}, "status": status, "identity": map[string]any{"status": "exact_name_dob", "reviewRequired": false}, "providerResponse": map[string]any{"benefitsInformation": []any{map[string]any{"code": "B", "serviceTypeCodes": []string{"98"}, "benefitAmount": "80"}}}}
	}
	batch := func(id string) map[string]any {
		return map[string]any{"id": id, "externalPatientId": "synthetic-patient", "status": "complete", "result": map[string]any{"providerResults": []any{response("doctor-a", "active"), response("doctor-b", "unknown"), response("doctor-c", "active")}}}
	}
	outcome := func(batchID, profile, occurredAt, status string) map[string]any {
		return map[string]any{"status": "success", "evidence": map[string]any{"newAppointmentId": "synthetic-appointment", "externalPatientId": "synthetic-patient", "occurredAt": occurredAt, "bookingResult": map[string]any{"status": status, "eligibilityCheckId": batchID, "providerProfileId": profile}}}
	}
	raw, err := json.Marshal(map[string]any{"eligibilityChecks": []any{batch("intake-a"), batch("intake-b")}, "domainOutcomes": []any{outcome("intake-a", "doctor-b", "2026-09-23T12:00:00.123456+00:00", "booked"), outcome("intake-b", "doctor-c", "2026-09-23T12:05:00Z", "booked"), outcome("intake-a", "doctor-a", "2026-09-23T12:10:00Z", "failed"), outcome("intake-a", "doctor-a", "bad-date", "booked")}})
	if err != nil {
		t.Fatal(err)
	}
	checks := ProjectEligibilityChecks(Interaction{ID: "interaction-1", CloseoutPayload: raw})
	if len(checks) != 6 {
		t.Fatalf("lost batch/provider evidence: %d", len(checks))
	}
	for i, check := range checks {
		if !check.ProviderCheck || check.ProviderNPI != "synthetic-npi" || len(check.Benefits) != 1 {
			t.Fatalf("lost doctor evidence: %#v", check)
		}
		switch i {
		case 1:
			if check.Status != "unknown" || len(check.AppointmentReviewKeys) != 1 || check.AppointmentReviewKeys[0] != "interaction-1:2026-09-23T12:00:00.123456Z" {
				t.Fatalf("booked doctor's failure substituted: %#v", check)
			}
		case 5:
			if len(check.AppointmentReviewKeys) != 1 || check.AppointmentReviewKeys[0] != "interaction-1:2026-09-23T12:05:00Z" {
				t.Fatalf("wrong later event: %#v", check)
			}
		default:
			if len(check.AppointmentReviewKeys) != 0 {
				t.Fatalf("unrelated check promoted: %#v", check)
			}
		}
	}
}

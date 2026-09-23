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

package interaction

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/work"
)

// EligibilityCheck is a staff-facing projection of saved intake evidence. It
// never joins patients by phone or treats a payer response as a booking receipt.
type EligibilityCheck struct {
	Status, PatientName, SubmittedName, Plan, PlanName, MemberIDLast4, CheckedAt, Reason string
	Benefits                                                                             []map[string]any
	IdentityReasons                                                                      []string
	CheckID, EligibilitySearchID                                                         string
	ProviderProfileID, ProviderName, ProviderNPI                                         string
	ProviderCheck                                                                        bool
	AppointmentReviewKeys                                                                []string
}

type savedEligibilityCheck struct {
	ID                string `json:"id"`
	ExternalPatientID string `json:"externalPatientId"`
	Status            string `json:"status"`
	FailureReason     string `json:"failureReason"`
	Request           struct {
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
		Plan      string `json:"plan"`
		MemberID  string `json:"memberId"`
	} `json:"request"`
	Result eligibilityResult `json:"result"`
}

type eligibilityResult struct {
	Status              string `json:"status"`
	CheckedAt           string `json:"checkedAt"`
	ReviewReason        string `json:"reviewReason"`
	CheckID             string `json:"checkId"`
	EligibilitySearchID string `json:"eligibilitySearchId"`
	Identity            *struct {
		Status         string   `json:"status"`
		Reasons        []string `json:"reasons"`
		ReviewRequired bool     `json:"reviewRequired"`
	} `json:"identity"`
	MatchedPatient *struct {
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	} `json:"matchedPatient"`
	Provider struct {
		ProfileID string `json:"profileId"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
		NPI       string `json:"npi"`
	} `json:"provider"`
	ProviderResponse json.RawMessage      `json:"providerResponse"`
	ProviderResults  *[]eligibilityResult `json:"providerResults"`
}

func ProjectEligibilityChecks(stored Interaction) []EligibilityCheck {
	var closeout struct {
		Checks   []savedEligibilityCheck `json:"eligibilityChecks"`
		Outcomes []struct {
			Status   string `json:"status"`
			Evidence struct {
				OccurredAt        string `json:"occurredAt"`
				NewAppointmentID  string `json:"newAppointmentId"`
				ExternalPatientID string `json:"externalPatientId"`
				Replayed          bool   `json:"replayed"`
				BookingResult     struct {
					Status             string `json:"status"`
					EligibilityCheckID string `json:"eligibilityCheckId"`
					ProviderProfileID  string `json:"providerProfileId"`
				} `json:"bookingResult"`
			} `json:"evidence"`
		} `json:"domainOutcomes"`
	}
	if json.Unmarshal(stored.CloseoutPayload, &closeout) != nil {
		return nil
	}
	checks := make([]EligibilityCheck, 0, len(closeout.Checks))
	for _, c := range closeout.Checks {
		results := []eligibilityResult{c.Result}
		providerCheck := c.Result.ProviderResults != nil
		if providerCheck {
			results = *c.Result.ProviderResults
			if len(results) == 0 {
				results = []eligibilityResult{{Status: "unknown", ReviewReason: "provider_results_missing"}}
			}
		}
		for _, result := range results {
			out := projectEligibilityResult(c, result)
			out.ProviderCheck = providerCheck
			out.ProviderProfileID = result.Provider.ProfileID
			out.ProviderName = strings.TrimSpace(result.Provider.FirstName + " " + result.Provider.LastName)
			out.ProviderNPI = result.Provider.NPI
			for _, outcome := range closeout.Outcomes {
				evidence := outcome.Evidence
				booking := evidence.BookingResult
				occurredAt, timeErr := time.Parse(time.RFC3339Nano, evidence.OccurredAt)
				if (outcome.Status != "success" && outcome.Status != "partial") || evidence.NewAppointmentID == "" || timeErr != nil || occurredAt.IsZero() || evidence.Replayed || (booking.Status != "booked" && booking.Status != "partial") {
					continue
				}
				if c.ID != "" && c.ExternalPatientID != "" && c.ExternalPatientID == evidence.ExternalPatientID && booking.EligibilityCheckID == c.ID && out.ProviderProfileID != "" && booking.ProviderProfileID == out.ProviderProfileID {
					out.AppointmentReviewKeys = append(out.AppointmentReviewKeys, work.AppointmentReviewKey(stored.ID, occurredAt))
				}
			}
			checks = append(checks, out)
		}
	}
	return checks
}

func projectEligibilityResult(c savedEligibilityCheck, result eligibilityResult) EligibilityCheck {

	out := EligibilityCheck{Status: "unavailable", SubmittedName: strings.TrimSpace(c.Request.FirstName + " " + c.Request.LastName), Plan: c.Request.Plan, CheckedAt: result.CheckedAt, Reason: c.FailureReason, Benefits: []map[string]any{}}
	out.PatientName = out.SubmittedName
	out.CheckID, out.EligibilitySearchID = result.CheckID, result.EligibilitySearchID
	if result.Identity != nil {
		out.IdentityReasons = result.Identity.Reasons
	}
	id := []rune(c.Request.MemberID)
	if len(id) > 4 {
		id = id[len(id)-4:]
	} else {
		id = nil
	}
	out.MemberIDLast4 = string(id)
	if c.Status == "pending" {
		out.Status = "pending"
	}
	if c.Status == "complete" {
		out.Status = "unknown"
		out.Reason = result.ReviewReason
		switch result.Status {
		case "review", "unknown":
			out.Status = result.Status
		case "active", "inactive":
			identity := result.Identity
			if identity != nil && !identity.ReviewRequired && (identity.Status == "exact_name_dob" || identity.Status == "matched_with_name_correction") {
				out.Status = result.Status
			} else {
				out.Status = "review"
				out.Reason = "identity_uncertain"
			}
		}
		if result.MatchedPatient != nil && result.Identity != nil && !result.Identity.ReviewRequired {
			out.PatientName = strings.TrimSpace(result.MatchedPatient.FirstName + " " + result.MatchedPatient.LastName)
		}
	}
	// Expose benefit evidence, not the raw subscriber, address, member ID, or X12.
	var provider struct {
		Benefits   []map[string]any `json:"benefitsInformation"`
		PlanStatus []struct {
			PlanDetails string `json:"planDetails"`
		} `json:"planStatus"`
	}
	decoder := json.NewDecoder(bytes.NewReader(result.ProviderResponse))
	decoder.UseNumber()
	if decoder.Decode(&provider) == nil {
		for _, row := range provider.Benefits {
			if row != nil {
				out.Benefits = append(out.Benefits, row)
				if out.PlanName == "" {
					out.PlanName, _ = row["planCoverage"].(string)
				}
			}
		}
		if out.PlanName == "" {
			for _, plan := range provider.PlanStatus {
				if plan.PlanDetails != "" {
					out.PlanName = plan.PlanDetails
					break
				}
			}
		}
	}
	return out
}

package interaction

import (
	"bytes"
	"encoding/json"
	"strings"
)

// EligibilityCheck is a staff-facing projection of saved intake evidence. It
// never joins patients by phone or treats a payer response as a booking receipt.
type EligibilityCheck struct {
	Status, PatientName, SubmittedName, Plan, PlanName, MemberIDLast4, CheckedAt, Reason string
	Benefits                                                                             []map[string]any
	IdentityReasons                                                                      []string
	CheckID, EligibilitySearchID                                                         string
}

func ProjectEligibilityChecks(stored Interaction) []EligibilityCheck {
	var closeout struct {
		Checks []struct {
			Status        string `json:"status"`
			FailureReason string `json:"failureReason"`
			Request       struct {
				FirstName string `json:"firstName"`
				LastName  string `json:"lastName"`
				Plan      string `json:"plan"`
				MemberID  string `json:"memberId"`
			} `json:"request"`
			Result struct {
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
				ProviderResponse json.RawMessage `json:"providerResponse"`
			} `json:"result"`
		} `json:"eligibilityChecks"`
	}
	if json.Unmarshal(stored.CloseoutPayload, &closeout) != nil {
		return nil
	}
	checks := make([]EligibilityCheck, 0, len(closeout.Checks))
	for _, c := range closeout.Checks {
		out := EligibilityCheck{Status: "unavailable", SubmittedName: strings.TrimSpace(c.Request.FirstName + " " + c.Request.LastName), Plan: c.Request.Plan, CheckedAt: c.Result.CheckedAt, Reason: c.FailureReason, Benefits: []map[string]any{}}
		out.PatientName = out.SubmittedName
		out.CheckID, out.EligibilitySearchID = c.Result.CheckID, c.Result.EligibilitySearchID
		if c.Result.Identity != nil {
			out.IdentityReasons = c.Result.Identity.Reasons
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
			out.Reason = c.Result.ReviewReason
			switch c.Result.Status {
			case "review", "unknown":
				out.Status = c.Result.Status
			case "active", "inactive":
				identity := c.Result.Identity
				if identity != nil && !identity.ReviewRequired && (identity.Status == "exact_name_dob" || identity.Status == "matched_with_name_correction") {
					out.Status = c.Result.Status
				} else {
					out.Status = "review"
					out.Reason = "identity_uncertain"
				}
			}
			if c.Result.MatchedPatient != nil && c.Result.Identity != nil && !c.Result.Identity.ReviewRequired {
				out.PatientName = strings.TrimSpace(c.Result.MatchedPatient.FirstName + " " + c.Result.MatchedPatient.LastName)
			}
		}
		// Expose benefit evidence, not the raw subscriber, address, member ID, or X12.
		var provider struct {
			Benefits   []map[string]any `json:"benefitsInformation"`
			PlanStatus []struct {
				PlanDetails string `json:"planDetails"`
			} `json:"planStatus"`
		}
		decoder := json.NewDecoder(bytes.NewReader(c.Result.ProviderResponse))
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
		checks = append(checks, out)
	}
	return checks
}

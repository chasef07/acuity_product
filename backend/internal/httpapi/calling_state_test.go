package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestCallingStateResponseIncludesInboundOfferPhoneAndDeadline(t *testing.T) {
	deadline := time.Date(2026, time.August, 9, 12, 0, 20, 0, time.UTC)
	response, err := callingStateResponse(humancalling.CallingState{
		Ringing: []humancalling.RingingCallLeg{{
			CallID:         "7f64a4db-9128-4ba2-8045-aa4ea3f50ff6",
			CallLegID:      "4d97ee34-e67e-4aec-b0ce-06036729b5e8",
			MediaToken:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			PracticeID:     "2c1d13b5-0954-4d30-b47f-fcfdbe15de1e",
			LocationID:     "6f36b46e-af2a-4cc4-a6f2-55f18caf9ab1",
			LocationName:   "Main office",
			DisplayName:    "Incoming caller",
			Phone:          "+15555550100",
			TransferReason: "Needs help",
			State:          "RINGING",
			Version:        1,
			CreatedAt:      deadline.Add(-20 * time.Second),
			Deadline:       deadline,
		}},
	})
	if err != nil {
		t.Fatalf("map Calling state: %v", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode Calling state: %v", err)
	}
	var body struct {
		Ringing []struct {
			Phone    string    `json:"phone"`
			Deadline time.Time `json:"deadline"`
		} `json:"ringing"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode Calling state: %v", err)
	}
	if len(body.Ringing) != 1 || body.Ringing[0].Phone != "+15555550100" ||
		!body.Ringing[0].Deadline.Equal(deadline) {
		t.Fatalf("Calling state body = %s", encoded)
	}
}

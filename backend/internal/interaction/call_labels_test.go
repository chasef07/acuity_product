package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func choiceResponse(choice string, criteria map[string]string) map[string]any {
	probabilities := map[string]float64{}
	for key := range criteria {
		probabilities[key] = 0
	}
	probabilities[choice] = 1
	return map[string]any{"type": "choice", "choice": choice, "probabilities": probabilities}
}

func TestCallLabelsUsesTwoChoiceQuestionsAndRecordedEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/alpha/decisions" || r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		var request struct {
			Model     string
			Questions map[string]struct {
				Type     string
				Criteria map[string]string
			}
			Provider struct{ ZDR bool }
			State    json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != callLabelModel || !request.Provider.ZDR || len(request.Questions) != 2 || request.Questions["intent"].Type != "choice" || request.Questions["outcome"].Type != "choice" || len(request.Questions["intent"].Criteria) != 14 || len(request.Questions["outcome"].Criteria) != 3 {
			t.Errorf("invalid labeling payload: %+v", request)
		}
		for _, evidence := range []string{"synthetic conversation", "saved request", "staffConnected"} {
			if !strings.Contains(string(request.State), evidence) {
				t.Errorf("missing evidence %s", evidence)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"intent": choiceResponse("existing_patient_booking", intentCriteria), "outcome": choiceResponse("transferred", outcomeCriteria)}})
	}))
	defer server.Close()
	client := NewCallLabelClassifier("synthetic-key")
	client.endpoint = server.URL + "/api/alpha/decisions"
	evidence := classificationEvidence{Transcript: json.RawMessage(`{"items":[{"role":"user","content":"synthetic conversation"}]}`), StaffTasks: json.RawMessage(`[{"message":"saved request"}]`), StaffConnected: true}
	result, err := client.classify(context.Background(), evidence)
	if err != nil || result.Intent != "existing_patient_booking" || result.Outcome != "transferred" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCallLabelsPreservesValidIntentAndWithholdsUnsupportedTransfer(t *testing.T) {
	cases := []struct {
		name    string
		outcome any
	}{
		{"unsupported transfer", choiceResponse("transferred", outcomeCriteria)},
		{"unknown choice", map[string]any{"type": "choice", "choice": "unknown", "probabilities": map[string]float64{"unknown": 1}}},
		{"not normalized", map[string]any{"type": "choice", "choice": "resolved", "probabilities": map[string]float64{"resolved": 0.8, "transferred": 0.8, "abandoned": 0}}},
		{"null probability", map[string]any{"type": "choice", "choice": "resolved", "probabilities": map[string]any{"resolved": 1, "transferred": nil, "abandoned": 0}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"intent": choiceResponse("office_information", intentCriteria), "outcome": tc.outcome}})
			}))
			defer server.Close()
			client := NewCallLabelClassifier("key")
			client.endpoint = server.URL
			result, err := client.classify(context.Background(), classificationEvidence{})
			if err == nil || result.Intent != "office_information" || result.Outcome != "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestCallLabelsPreservesMeaningfulOtherAndVoicemailUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"intent": choiceResponse("other", intentCriteria), "outcome": choiceResponse("resolved", outcomeCriteria)}})
	}))
	defer server.Close()
	client := NewCallLabelClassifier("key")
	client.endpoint = server.URL
	result, err := client.classify(context.Background(), classificationEvidence{})
	if err != nil || result.Intent != "other" || result.Outcome != "resolved" {
		t.Fatalf("meaningful other discarded: %+v %v", result, err)
	}
	result, err = client.classify(context.Background(), classificationEvidence{VoicemailContentUnavailable: true})
	if err == nil || result.Intent != "other" || result.Outcome != "" {
		t.Fatalf("unreviewed voicemail fabricated outcome: %+v %v", result, err)
	}
}

func TestCallLabelsHTTPTimeoutAndMissingCallerEvidence(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client := NewCallLabelClassifier("key")
	client.endpoint = server.URL
	client.client.Timeout = 10 * time.Millisecond
	if _, err := client.classify(context.Background(), classificationEvidence{}); err == nil || err.Error() != "classification_transport_failed" {
		t.Fatalf("timeout=%v", err)
	}
	for _, transcript := range []string{`{}`, `{"items":[]}`, `{"items":[{"role":"user","content":[" "]}]}`, `{"items":[{"role":"assistant","content":["Hello"]}]}`} {
		if classificationHasConversation(json.RawMessage(transcript)) {
			t.Fatalf("missing caller conversation accepted: %s", transcript)
		}
	}
}

func TestCallLabelsFailuresDoNotExposeProviderBody(t *testing.T) {
	for _, status := range []int{401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private transcript"))
			}))
			defer server.Close()
			client := NewCallLabelClassifier("key")
			client.endpoint = server.URL
			result, err := client.classify(context.Background(), classificationEvidence{})
			if err == nil || strings.Contains(err.Error(), "private transcript") || result.Intent != "" || result.Outcome != "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
	client := NewCallLabelClassifier("")
	if _, err := client.classify(context.Background(), classificationEvidence{}); err == nil || err.Error() != "openrouter_key_missing" {
		t.Fatalf("missing key: %v", err)
	}
}

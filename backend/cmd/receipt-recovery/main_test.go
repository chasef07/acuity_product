package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	testPracticeID       = "30000000-0000-4000-8000-000000000001"
	testCallID           = "30000000-0000-4000-8000-000000000002"
	testReceiptReference = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

func TestRecoveryCommandIsDryRunByDefaultAndSelectsOnlyOneReceipt(t *testing.T) {
	var candidates, mutations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/quarantine-candidate"):
			candidates.Add(1)
			writeTestJSON(t, w, recoveryCandidateFixture())
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+testReceiptReference):
			writeTestJSON(t, w, recoveryStatusFixture("QUARANTINED"))
		case r.Method == http.MethodPost:
			mutations.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	err := run(
		context.Background(),
		[]string{
			"--base-url=" + server.URL,
			"--practice-id=" + testPracticeID,
			"--event-type=call.answered",
			"--error-code=PROJECTION_RETRY_EXHAUSTED",
			"--action=requeue",
		},
		func(key string) string {
			if key == "OPERATOR_TOKEN" {
				return "operator-token"
			}
			return ""
		},
		server.Client(),
		&output,
	)
	if err != nil {
		t.Fatalf("dry-run recovery command: %v", err)
	}
	if candidates.Load() != 1 || mutations.Load() != 0 {
		t.Fatalf("dry run requests = candidates:%d mutations:%d",
			candidates.Load(), mutations.Load())
	}
	if !strings.Contains(output.String(), `"applied": false`) ||
		!strings.Contains(output.String(), testReceiptReference) ||
		strings.Contains(output.String(), "operator-token") {
		t.Fatalf("dry-run output = %s", output.String())
	}
}

func TestRecoveryCommandAppliesAndObservesExactlyOneRequeue(t *testing.T) {
	var candidates, mutations, statusReads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/quarantine-candidate"):
			candidates.Add(1)
			writeTestJSON(t, w, recoveryCandidateFixture())
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+testReceiptReference):
			read := statusReads.Add(1)
			if read == 1 {
				writeTestJSON(t, w, recoveryStatusFixture("QUARANTINED"))
				return
			}
			writeTestJSON(t, w, recoveryStatusFixture("APPLIED"))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/requeue"):
			mutations.Add(1)
			writeTestJSON(t, w, map[string]any{
				"receiptReference": testReceiptReference,
				"state":            "PENDING",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	err := run(
		context.Background(),
		[]string{
			"--base-url=" + server.URL,
			"--practice-id=" + testPracticeID,
			"--event-type=call.answered",
			"--error-code=PROJECTION_RETRY_EXHAUSTED",
			"--action=requeue",
			"--apply",
			"--poll-interval=1ms",
			"--timeout=1s",
		},
		func(key string) string {
			if key == "OPERATOR_TOKEN" {
				return "operator-token"
			}
			return ""
		},
		server.Client(),
		&output,
	)
	if err != nil {
		t.Fatalf("apply one receipt requeue: %v", err)
	}
	if candidates.Load() != 1 || mutations.Load() != 1 || statusReads.Load() != 2 {
		t.Fatalf("apply requests = candidates:%d mutations:%d statuses:%d",
			candidates.Load(), mutations.Load(), statusReads.Load())
	}
	if !strings.Contains(output.String(), `"applied": true`) ||
		!strings.Contains(output.String(), `"state": "APPLIED"`) {
		t.Fatalf("apply output = %s", output.String())
	}
}

func TestRecoveryCommandResolvesExactlyOneReceiptAndPreservesFailureStatus(t *testing.T) {
	var mutations, statusReads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/quarantine-candidate"):
			writeTestJSON(t, w, recoveryCandidateFixture())
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+testReceiptReference):
			read := statusReads.Add(1)
			status := recoveryStatusFixture("QUARANTINED")
			if read > 1 {
				status = recoveryStatusFixture("FAILED")
			}
			writeTestJSON(t, w, status)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/resolve"):
			mutations.Add(1)
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
				body["resolution"] != "UNSAFE_TO_REPLAY" {
				t.Fatalf("resolution body = %#v err=%v", body, err)
			}
			writeTestJSON(t, w, map[string]any{
				"receiptReference": testReceiptReference, "state": "FAILED",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	err := run(context.Background(), []string{
		"--base-url=" + server.URL,
		"--practice-id=" + testPracticeID,
		"--event-type=call.answered",
		"--error-code=PROJECTION_RETRY_EXHAUSTED",
		"--action=resolve",
		"--apply",
	}, func(key string) string {
		if key == "OPERATOR_TOKEN" {
			return "operator-token"
		}
		return ""
	}, server.Client(), &output)
	if err != nil {
		t.Fatalf("resolve one receipt: %v", err)
	}
	if mutations.Load() != 1 || statusReads.Load() != 2 ||
		!strings.Contains(output.String(), `"state": "FAILED"`) {
		t.Fatalf("resolution requests = mutations:%d statuses:%d output:%s",
			mutations.Load(), statusReads.Load(), output.String())
	}
}

func TestRecoveryCommandStopsBeforeMutationOnExistingProviderCommandAnomaly(t *testing.T) {
	var mutations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/quarantine-candidate"):
			writeTestJSON(t, w, recoveryCandidateFixture())
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+testReceiptReference):
			status := recoveryStatusFixture("QUARANTINED")
			status["commandStates"] = []map[string]any{{"state": "AMBIGUOUS", "count": 1}}
			writeTestJSON(t, w, status)
		case r.Method == http.MethodPost:
			mutations.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--base-url=" + server.URL,
		"--practice-id=" + testPracticeID,
		"--event-type=call.answered",
		"--error-code=PROJECTION_RETRY_EXHAUSTED",
		"--action=requeue",
		"--apply",
	}, func(key string) string {
		if key == "OPERATOR_TOKEN" {
			return "operator-token"
		}
		return ""
	}, server.Client(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "ambiguous or failed") || mutations.Load() != 0 {
		t.Fatalf("anomaly stop = err:%v mutations:%d", err, mutations.Load())
	}
}

func recoveryCandidateFixture() map[string]any {
	return map[string]any{
		"practiceId": testPracticeID, "callId": testCallID,
		"receiptReference": testReceiptReference,
		"eventType":        "call.answered", "errorCode": "PROJECTION_RETRY_EXHAUSTED",
		"attempts": 10, "ageSeconds": 300, "remainingGroupCount": 4,
	}
}

func recoveryStatusFixture(state string) map[string]any {
	errorCode := "PROJECTION_RETRY_EXHAUSTED"
	quarantine := 5
	requeueAudits := 0
	resolutionAudits := 0
	if state == "APPLIED" {
		errorCode = ""
		quarantine = 4
		requeueAudits = 1
	}
	if state == "FAILED" {
		quarantine = 4
		resolutionAudits = 1
	}
	return map[string]any{
		"practiceId": testPracticeID, "callId": testCallID,
		"receiptReference": testReceiptReference,
		"eventType":        "call.answered", "errorCode": errorCode,
		"state": state, "attempts": 10, "ageSeconds": 300,
		"duplicateCount": 0, "callState": "RINGING", "callVersion": 1,
		"callLegStates":      []map[string]any{{"state": "RINGING", "count": 1}},
		"commandStates":      []map[string]any{},
		"activeReceiptCount": 0, "quarantinedReceiptCount": quarantine,
		"requeueAuditCount": requeueAudits, "resolutionAuditCount": resolutionAudits,
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode test response: %v", err)
	}
}

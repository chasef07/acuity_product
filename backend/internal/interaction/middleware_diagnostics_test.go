package interaction

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNativeToolDiagnosticsKeepRequestAndProviderEvidence(t *testing.T) {
	transcript := json.RawMessage(`{"items":[{"type":"function_call","name":"add_patient","call_id":"tool-1"},{"type":"function_call_output","call_id":"tool-1","is_error":true}]}`)
	closeout := decodeRecord(json.RawMessage(`{"domainOutcomes":[{"callId":"tool-1","toolName":"add_patient","outcome":"patient_creation_failed","status":"failed","middlewareRequests":[{"requestId":"fb672b37-0211-4e69-baf1-b0f56b181911","operation":"createPatient","attempt":1,"durationMs":1400,"result":"response","httpStatus":200,"responseStatus":"error","outcome":"rejected","failureReason":"request_rejected","retryable":false,"providerErrorCount":1,"providerErrors":[{"operation":"addpatient","category":"rejected","httpStatus":200,"code":"-32602","durationMs":1300}]}]}]}`))
	executions := nativeToolExecutions(transcript, closeout, time.Now())
	if len(executions) != 1 || len(executions[0].MiddlewareRequests) != 1 {
		t.Fatalf("executions=%+v", executions)
	}
	diagnostic := executions[0].MiddlewareRequests[0]
	if diagnostic.RequestID != "fb672b37-0211-4e69-baf1-b0f56b181911" || diagnostic.Outcome != "rejected" || diagnostic.Retryable == nil || *diagnostic.Retryable || len(diagnostic.ProviderErrors) != 1 || diagnostic.ProviderErrors[0].Code != "-32602" {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
	if executions[0].DomainStatus != "failed" || executions[0].Status != "ERROR" {
		t.Fatal("diagnostics changed execution/domain status")
	}
}

func TestMiddlewareDiagnosticsExcludeUntrustedTextAndInvalidRows(t *testing.T) {
	value := decodeRecord(json.RawMessage(`{"requests":[{"requestId":"fb672b37-0211-4e69-baf1-b0f56b181911","operation":"resolvePatient","attempt":1,"durationMs":10,"result":"response","httpStatus":999,"outcome":"patient-secret","category":"patient-secret","failureReason":"patient-secret","retryable":true,"message":"patient-secret","providerErrors":[{"operation":"lookuppatient","category":"rejected","durationMs":2,"code":"patient-secret","message":"patient-secret"},{"operation":"patient-secret","category":"rejected","durationMs":2}]},{"requestId":"patient-secret","operation":"resolvePatient","attempt":1,"durationMs":10,"result":"response"}]}`))["requests"]
	got := middlewareRequestDiagnostics(value)
	if len(got) != 1 || got[0].HTTPStatus != nil || got[0].Retryable != nil || len(got[0].ProviderErrors) != 1 {
		t.Fatalf("got=%+v", got)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "patient-secret") {
		t.Fatalf("unsafe projection=%s", encoded)
	}
}

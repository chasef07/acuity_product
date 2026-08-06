package deploy_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTelnyxCallLegCutoverContractFailsClosed(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Telnyx CallLeg contract")
	}
	directory := filepath.Dir(filename)
	contractPath := filepath.Join(directory, "telnyx-callleg-cutover-contract.json")
	raw, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		SchemaVersion  int            `json:"schemaVersion"`
		Provider       string         `json:"provider"`
		CutoverGates   map[string]any `json:"cutoverGates"`
		RequiredProbes []string       `json:"requiredProbes"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.Now().UTC().Format(time.RFC3339)
	mediaDisabled := map[string]any{
		"automaticRecording": "disabled",
		"siprec":             "disabled", "rtcpCapture": "disabled",
		"mediaFork": "disabled", "connectedCallRecording": "disabled",
	}
	callControl := map[string]any{
		"active":             true,
		"webhookEventUrl":    "https://primary.example/v1/provider/telnyx/webhooks",
		"webhookFailoverUrl": "https://failover.example/v1/provider/telnyx/webhooks",
		"webhookApiVersion":  "2", "webhookTimeoutSeconds": float64(5),
		"firstCommandTimeout": false, "outboundChannelLimit": float64(12),
	}
	webRTC := map[string]any{
		"active":             true,
		"webhookEventUrl":    "https://primary.example/v1/provider/telnyx/webhooks",
		"webhookFailoverUrl": "https://failover.example/v1/provider/telnyx/webhooks",
		"webhookApiVersion":  "2", "webhookTimeoutSeconds": float64(5),
		"sipUriCallingPreference": "internal", "simultaneousRinging": "disabled",
		"srtpRequired": true, "outboundChannelLimit": float64(12),
	}
	outbound := map[string]any{
		"enabled": true, "outboundChannelLimit": float64(12),
		"destinations": []any{"US"}, "trafficType": "conversational",
		"dailySpendLimitUSD": float64(10), "maximumDestinationRateUSD": 0.2,
	}
	for key, value := range mediaDisabled {
		callControl[key], webRTC[key], outbound[key] = value, value, value
	}
	callControlID := "call-control-live"
	webRTCConnectionID := "webrtc-live"
	outboundVoiceProfileID := "outbound-profile-live"
	phoneNumberID := "product-did-live"
	productDID := "+15555550100"
	callControlResource := map[string]any{
		"id":                         callControlID,
		"active":                     true,
		"webhook_event_url":          "https://primary.example/v1/provider/telnyx/webhooks",
		"webhook_event_failover_url": "https://failover.example/v1/provider/telnyx/webhooks",
		"webhook_api_version":        "2", "webhook_timeout_secs": float64(5),
		"first_command_timeout": false,
		"inbound":               map[string]any{},
		"outbound": map[string]any{
			"channel_limit":             float64(12),
			"outbound_voice_profile_id": outboundVoiceProfileID,
		},
		"rtcp_settings": map[string]any{"capture_enabled": false},
	}
	webRTCResource := map[string]any{
		"id":                         webRTCConnectionID,
		"active":                     true,
		"webhook_event_url":          "https://primary.example/v1/provider/telnyx/webhooks",
		"webhook_event_failover_url": "https://failover.example/v1/provider/telnyx/webhooks",
		"webhook_api_version":        "2", "webhook_timeout_secs": float64(5),
		"sip_uri_calling_preference": "internal", "encrypted_media": "SRTP",
		"inbound": map[string]any{"simultaneous_ringing": false},
		"outbound": map[string]any{
			"channel_limit":             float64(12),
			"outbound_voice_profile_id": outboundVoiceProfileID,
		},
		"rtcp_settings": map[string]any{"capture_enabled": false},
	}
	outboundResource := map[string]any{
		"id": outboundVoiceProfileID, "enabled": true,
		"concurrent_call_limit":    float64(12),
		"whitelisted_destinations": []any{"US"},
		"traffic_type":             "conversational", "daily_spend_limit": "10",
		"daily_spend_limit_enabled": true, "max_destination_rate": 0.2,
		"call_recording": nil,
	}
	phoneNumberResource := map[string]any{
		"id": phoneNumberID, "phone_number": productDID,
		"connection_id": callControlID, "status": "active",
		"call_recording_enabled": false,
	}
	phoneNumberVoiceResource := map[string]any{
		"id": phoneNumberID, "connection_id": callControlID,
		"call_recording":  map[string]any{"inbound_call_recording_enabled": false},
		"call_forwarding": map[string]any{"call_forwarding_enabled": false},
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.Header.Get("Authorization") != "Bearer KEY_synthetic" {
			http.Error(response, "invalid read", http.StatusUnauthorized)
			return
		}
		if request.URL.Path == "/v2/phone_numbers" {
			if request.URL.Query().Get("filter[phone_number]") != productDID {
				http.Error(response, "invalid phone filter", http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(map[string]any{
				"data": []any{phoneNumberResource},
			})
			return
		}
		resources := map[string]map[string]any{
			"/v2/call_control_applications/" + callControlID:        callControlResource,
			"/v2/credential_connections/" + webRTCConnectionID:      webRTCResource,
			"/v2/outbound_voice_profiles/" + outboundVoiceProfileID: outboundResource,
			"/v2/phone_numbers/" + phoneNumberID + "/voice":         phoneNumberVoiceResource,
		}
		resource, ok := resources[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"data": resource})
	}))
	defer server.Close()
	hashValue := func(value string) string {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
	}
	providerSnapshotHash := func() string {
		providerSnapshot, err := json.Marshal(map[string]any{
			"callControlApplication": callControlResource,
			"didSummary": map[string]any{
				"id": phoneNumberID, "connection_id": callControlID,
				"status":                 phoneNumberResource["status"],
				"call_recording_enabled": phoneNumberResource["call_recording_enabled"],
			},
			"didVoice": map[string]any{
				"id": phoneNumberID, "connection_id": callControlID,
				"call_recording": phoneNumberVoiceResource["call_recording"],
			},
			"outboundVoiceProfile": outboundResource,
			"webRTCConnection":     webRTCResource,
		})
		if err != nil {
			t.Fatal(err)
		}
		return hashValue(string(providerSnapshot))
	}
	probes := make([]any, 0, len(contract.RequiredProbes))
	for _, name := range contract.RequiredProbes {
		probes = append(probes, map[string]any{
			"name": name, "status": "passed", "observedAt": now,
			"evidenceRef": "operator-proof/" + name,
		})
	}
	evidence := map[string]any{
		"schemaVersion": contract.SchemaVersion,
		"provider":      contract.Provider,
		"capturedAt":    now,
		"provenance": map[string]any{
			"source": "telnyx-v2-read-only", "snapshotSha256": providerSnapshotHash(),
			"resources": []any{
				map[string]any{"type": "call_control_application", "idHash": hashValue(callControlID)},
				map[string]any{"type": "webrtc_connection", "idHash": hashValue(webRTCConnectionID)},
				map[string]any{"type": "outbound_voice_profile", "idHash": hashValue(outboundVoiceProfileID)},
				map[string]any{"type": "product_did", "idHash": hashValue(phoneNumberID)},
				map[string]any{"type": "account_capacity", "idHash": hash},
			},
		},
		"cutoverGates": contract.CutoverGates,
		"liveConfiguration": map[string]any{
			"declaredChannelLimit":   float64(12),
			"callControlApplication": callControl,
			"webRTCConnection":       webRTC,
			"outboundVoiceProfile":   outbound,
			"accountCapacity": map[string]any{
				"concurrentCalls": float64(20), "callsPerSecond": float64(5),
				"approvalReference": "telnyx-support/capacity-approval",
			},
			"acknowledgement": map[string]any{
				"sampleCount": float64(25), "p99Millis": float64(250),
				"evidenceRef": "load-proof/provider-ingress",
			},
			"staffCredentialRingability": map[string]any{
				"eligibleCount": float64(1), "provenCount": float64(1),
				"proofs": []any{map[string]any{
					"staffSubjectHash": hash, "credentialIdHash": hash,
					"status": "ringable", "observedAt": now,
				}},
			},
		},
		"probes": probes,
	}
	evidencePath := filepath.Join(t.TempDir(), "evidence.json")
	writeEvidence := func() {
		encoded, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(evidencePath, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeEvidence()
	check := func() error {
		command := exec.Command(
			"node",
			"--import",
			filepath.Join(directory, "testdata", "redirect-telnyx-api.mjs"),
			filepath.Join(directory, "check-telnyx-callleg-cutover.mjs"),
			contractPath,
			evidencePath,
		)
		command.Env = append(os.Environ(),
			"TELNYX_API_KEY=KEY_synthetic",
			"TELNYX_CALL_CONTROL_ID="+callControlID,
			"TELNYX_CREDENTIAL_CONNECTION_ID="+webRTCConnectionID,
			"TELNYX_FROM_NUMBER="+productDID,
			"TELNYX_TEST_API_BASE_URL="+server.URL+"/v2",
		)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, output)
		}
		return nil
	}
	if err := check(); err != nil {
		t.Fatalf("valid cutover evidence rejected: %v", err)
	}
	evidence["liveConfiguration"].(map[string]any)["notes"] = "+15555550100 SECRET_telnyx"
	writeEvidence()
	if err := check(); err == nil {
		t.Fatal("evidence containing phone material passed the cutover gate")
	} else if strings.Contains(err.Error(), "+15555550100") || strings.Contains(err.Error(), "SECRET_telnyx") {
		t.Fatalf("cutover checker leaked forbidden evidence: %v", err)
	}
	delete(evidence["liveConfiguration"].(map[string]any), "notes")
	evidence["liveConfiguration"].(map[string]any)["apiKey"] = "SECRET_telnyx"
	writeEvidence()
	if err := check(); err == nil {
		t.Fatal("evidence containing an API key passed the cutover gate")
	} else if strings.Contains(err.Error(), "SECRET_telnyx") {
		t.Fatalf("cutover checker leaked forbidden evidence: %v", err)
	}
	delete(evidence["liveConfiguration"].(map[string]any), "apiKey")
	callControlResource["active"] = false
	evidence["provenance"].(map[string]any)["snapshotSha256"] = providerSnapshotHash()
	writeEvidence()
	if err := check(); err == nil {
		t.Fatal("inactive Call Control Application passed the live provider gate")
	}
	callControlResource["active"] = true
	webRTCResource["active"] = false
	evidence["provenance"].(map[string]any)["snapshotSha256"] = providerSnapshotHash()
	writeEvidence()
	if err := check(); err == nil {
		t.Fatal("inactive WebRTC connection passed the live provider gate")
	}
	webRTCResource["active"] = true
	evidence["provenance"].(map[string]any)["snapshotSha256"] = providerSnapshotHash()
	phoneNumberResource["status"] = "port-pending"
	evidence["provenance"].(map[string]any)["snapshotSha256"] = providerSnapshotHash()
	writeEvidence()
	if err := check(); err == nil {
		t.Fatal("inactive Product DID passed the live provider gate")
	}
	phoneNumberResource["status"] = "active"
	phoneNumberVoiceResource["call_forwarding"].(map[string]any)["call_forwarding_enabled"] = true
	evidence["provenance"].(map[string]any)["snapshotSha256"] = providerSnapshotHash()
	writeEvidence()
	if err := check(); err == nil {
		t.Fatal("forwarded Product DID passed the live provider gate")
	}
	phoneNumberVoiceResource["call_forwarding"].(map[string]any)["call_forwarding_enabled"] = false
	evidence["provenance"].(map[string]any)["snapshotSha256"] = providerSnapshotHash()
	evidence["liveConfiguration"].(map[string]any)["callControlApplication"].(map[string]any)["firstCommandTimeout"] = true
	writeEvidence()
	if err := check(); err == nil {
		t.Fatal("operator-authored configuration passed despite live provider mismatch")
	}
	evidence["liveConfiguration"].(map[string]any)["callControlApplication"].(map[string]any)["firstCommandTimeout"] = false
	phoneNumberResource["call_recording_enabled"] = true
	phoneNumberVoiceResource["call_recording"].(map[string]any)["inbound_call_recording_enabled"] = true
	evidence["provenance"].(map[string]any)["snapshotSha256"] = providerSnapshotHash()
	writeEvidence()
	if err := check(); err == nil {
		t.Fatal("enabled Product DID inbound recording passed the live provider gate")
	}
	phoneNumberResource["call_recording_enabled"] = false
	phoneNumberVoiceResource["call_recording"].(map[string]any)["inbound_call_recording_enabled"] = false
	evidence["provenance"].(map[string]any)["snapshotSha256"] = providerSnapshotHash()
	evidence["cutoverGates"].(map[string]any)["oldRevisionCount"] = float64(1)
	writeEvidence()
	if err := check(); err == nil {
		t.Fatal("old live revision passed the Telnyx CallLeg cutover gate")
	}
}

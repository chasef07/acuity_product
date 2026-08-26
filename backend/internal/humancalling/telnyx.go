package humancalling

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/team-telnyx/telnyx-go/v4"
	"github.com/team-telnyx/telnyx-go/v4/option"
)

type TelnyxConfig struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

type TelnyxAdapter struct {
	config TelnyxConfig
	client telnyx.Client
}

type ProviderError struct {
	HTTPStatus   int
	Code         string
	SafeCode     string
	Definitive   bool
	TargetAbsent bool
}

func (err *ProviderError) Error() string {
	return err.SafeCode
}

func (err *ProviderError) Is(target error) bool {
	if target == ErrProviderTargetAbsent {
		return err.TargetAbsent
	}
	if target == ErrDefinitiveProviderFailure {
		return err.Definitive
	}
	return target == ErrAmbiguousEffect && !err.Definitive
}

var errInvalidRecordingLocation = errors.New("invalid recording location")

const telnyxRecordingRequestTimeout = 5 * time.Second

func NewTelnyxAdapter(config TelnyxConfig) (*TelnyxAdapter, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("telnyx API key is required")
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://api.telnyx.com/v2"
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("valid Telnyx API base URL is required")
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	client := telnyx.NewClient(
		option.WithAPIKey(config.APIKey),
		option.WithBaseURL(config.BaseURL),
		option.WithHTTPClient(config.HTTPClient),
		option.WithMaxRetries(0),
	)
	return &TelnyxAdapter{config: config, client: client}, nil
}

func (adapter *TelnyxAdapter) Execute(
	ctx context.Context,
	command ProviderCommand,
) (ProviderResult, error) {
	if command.ID == "" || command.Action == "" ||
		(command.TargetID != "" && !validTelnyxResourceID(command.TargetID)) {
		return ProviderResult{}, ErrInvalidInput
	}
	payload := clonePayload(command.Payload)
	payload["command_id"] = command.ID

	switch command.Action {
	case CommandAnswerCaller:
		if command.TargetID == "" ||
			payload["transcription"] != false ||
			!validWebhookRetryPolicies(payload["webhook_retries_policies"],
				FactCallAnswered, FactCallHangup) ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		var params telnyx.CallActionAnswerParams
		if err := decodeTelnyxParams(payload, &params); err != nil {
			return ProviderResult{}, ErrInvalidInput
		}
		_, err := adapter.client.Calls.Actions.Answer(ctx, command.TargetID, params)
		return ProviderResult{}, classifyTelnyxSDKError(err)
	case CommandStartRingWindow:
		_, hasLoop := payload["loop"]
		if command.TargetID == "" ||
			emptyString(payload["audio_url"]) ||
			hasLoop ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		var params telnyx.CallActionStartPlaybackParams
		if err := decodeTelnyxParams(payload, &params); err != nil {
			return ProviderResult{}, ErrInvalidInput
		}
		_, err := adapter.client.Calls.Actions.StartPlayback(ctx, command.TargetID, params)
		return ProviderResult{}, classifyTelnyxSDKError(err)
	case CommandStopRingWindow:
		if command.TargetID == "" || payload["stop"] != "all" ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		var params telnyx.CallActionStopPlaybackParams
		if err := decodeTelnyxParams(payload, &params); err != nil {
			return ProviderResult{}, ErrInvalidInput
		}
		_, err := adapter.client.Calls.Actions.StopPlayback(ctx, command.TargetID, params)
		return ProviderResult{}, classifyTelnyxSDKError(err)
	case CommandSpeakVoicemail:
		if command.TargetID == "" ||
			emptyString(payload["payload"]) ||
			emptyString(payload["voice"]) ||
			emptyString(payload["language"]) ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		var params telnyx.CallActionSpeakParams
		if err := decodeTelnyxParams(payload, &params); err != nil {
			return ProviderResult{}, ErrInvalidInput
		}
		_, err := adapter.client.Calls.Actions.Speak(ctx, command.TargetID, params)
		return ProviderResult{}, classifyTelnyxSDKError(err)
	case CommandDialStaff:
		timeoutSeconds, validTimeout := payload["timeout_secs"].(float64)
		mediaPrep := payload["media_prep"] == true
		if emptyString(payload["to"]) ||
			emptyString(payload["connection_id"]) ||
			emptyString(payload["from"]) ||
			emptyString(payload["client_state"]) ||
			!validMediaTokenHeader(payload["custom_headers"]) ||
			!validWebhookRetryPolicies(payload["webhook_retries_policies"],
				FactCallInitiated, FactCallAnswered, FactCallHangup) ||
			!validTimeout ||
			timeoutSeconds <= 0 ||
			timeoutSeconds != float64(int(timeoutSeconds)) ||
			payload["retry_on_timeout"] != false ||
			(!mediaPrep &&
				(emptyString(payload["link_to"]) ||
					payload["bridge_intent"] != true ||
					payload["bridge_on_answer"] != false)) {
			return ProviderResult{}, ErrInvalidInput
		}
		return adapter.dial(ctx, payload)
	case CommandDialOutboundStaff:
		timeoutSeconds, validTimeout := payload["timeout_secs"].(float64)
		if emptyString(payload["to"]) ||
			emptyString(payload["connection_id"]) ||
			emptyString(payload["from"]) ||
			emptyString(payload["client_state"]) ||
			!validMediaTokenHeader(payload["custom_headers"]) ||
			!validWebhookRetryPolicies(payload["webhook_retries_policies"],
				FactCallInitiated, FactCallAnswered, FactCallHangup) ||
			!validTimeout || timeoutSeconds <= 0 {
			return ProviderResult{}, ErrInvalidInput
		}
		return adapter.dial(ctx, payload)
	case CommandDialOutboundDestination:
		timeoutSeconds, validTimeout := payload["timeout_secs"].(float64)
		if emptyString(payload["to"]) ||
			emptyString(payload["connection_id"]) ||
			emptyString(payload["from"]) ||
			emptyString(payload["link_to"]) ||
			emptyString(payload["client_state"]) ||
			!validWebhookRetryPolicies(payload["webhook_retries_policies"],
				FactCallInitiated, FactCallAnswered, FactCallHangup) ||
			!validTimeout ||
			timeoutSeconds != 30 ||
			payload["bridge_intent"] != true ||
			payload["bridge_on_answer"] != false ||
			payload["answering_machine_detection"] != "disabled" {
			return ProviderResult{}, ErrInvalidInput
		}
		return adapter.dial(ctx, payload)
	case CommandBridge:
		recordingRequested := payload["record"] != nil ||
			payload["record_channels"] != nil ||
			payload["record_format"] != nil ||
			payload["record_track"] != nil
		if command.TargetID == "" ||
			emptyString(payload["call_control_id"]) ||
			payload["prevent_double_bridge"] != true ||
			emptyString(payload["client_state"]) ||
			(recordingRequested && (payload["record"] != "record-from-answer" ||
				payload["record_channels"] != "dual" ||
				payload["record_format"] != "mp3" ||
				payload["record_track"] != "both")) {
			return ProviderResult{}, ErrInvalidInput
		}
		var params telnyx.CallActionBridgeParams
		if err := decodeTelnyxParams(payload, &params); err != nil {
			return ProviderResult{}, ErrInvalidInput
		}
		_, err := adapter.client.Calls.Actions.Bridge(ctx, command.TargetID, params)
		return ProviderResult{}, classifyTelnyxSDKError(err)
	case CommandTransferStaff:
		timeoutSeconds, validTimeout := payload["timeout_secs"].(float64)
		if command.TargetID == "" || emptyString(payload["to"]) ||
			emptyString(payload["client_state"]) ||
			emptyString(payload["target_leg_client_state"]) ||
			!validMediaTokenHeader(payload["custom_headers"]) ||
			!validWebhookRetryPolicies(payload["webhook_retries_policies"],
				FactCallInitiated, FactCallAnswered, FactCallBridged, FactCallHangup) ||
			!validTimeout || timeoutSeconds <= 0 ||
			timeoutSeconds != float64(int(timeoutSeconds)) {
			return ProviderResult{}, ErrInvalidInput
		}
		var params telnyx.CallActionTransferParams
		if err := decodeTelnyxParams(payload, &params); err != nil {
			return ProviderResult{}, ErrInvalidInput
		}
		_, err := adapter.client.Calls.Actions.Transfer(ctx, command.TargetID, params)
		return ProviderResult{}, classifyTelnyxSDKError(err)
	case CommandHangupLeg:
		if command.TargetID == "" {
			return ProviderResult{}, ErrInvalidInput
		}
		var params telnyx.CallActionHangupParams
		if err := decodeTelnyxParams(payload, &params); err != nil {
			return ProviderResult{}, ErrInvalidInput
		}
		_, err := adapter.client.Calls.Actions.Hangup(ctx, command.TargetID, params)
		return ProviderResult{}, classifyTelnyxSDKError(err)
	case CommandStartVoicemailRecording:
		maxLength, validMaxLength := payload["max_length"].(float64)
		if command.TargetID == "" ||
			payload["format"] != "mp3" ||
			payload["channels"] != "single" ||
			payload["recording_track"] != "inbound" ||
			payload["transcription"] != false ||
			payload["play_beep"] != true ||
			!validMaxLength ||
			maxLength != 120 ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		var params telnyx.CallActionStartRecordingParams
		if err := decodeTelnyxParams(payload, &params); err != nil {
			return ProviderResult{}, ErrInvalidInput
		}
		_, err := adapter.client.Calls.Actions.StartRecording(ctx, command.TargetID, params)
		return ProviderResult{}, classifyTelnyxSDKError(err)
	case CommandCreateCredential:
		if emptyString(payload["connection_id"]) ||
			emptyString(payload["name"]) ||
			emptyString(payload["tag"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		delete(payload, "command_id")
		var params telnyx.TelephonyCredentialNewParams
		if err := decodeTelnyxParams(payload, &params); err != nil {
			return ProviderResult{}, ErrInvalidInput
		}
		response, err := adapter.client.TelephonyCredentials.New(ctx, params)
		if err != nil {
			return ProviderResult{}, classifyTelnyxSDKError(err)
		}
		if response == nil || response.Data.ID == "" || response.Data.SipUsername == "" {
			return ProviderResult{}, fmt.Errorf(
				"%w: invalid Telnyx credential response",
				ErrAmbiguousEffect,
			)
		}
		return ProviderResult{
			CredentialID: response.Data.ID,
			SIPUsername:  response.Data.SipUsername,
		}, nil
	case CommandDisableCredential:
		if command.TargetID == "" {
			return ProviderResult{}, ErrInvalidInput
		}
		_, err := adapter.client.TelephonyCredentials.Delete(ctx, command.TargetID)
		err = classifyTelnyxSDKError(err)
		if errors.Is(err, ErrProviderTargetAbsent) {
			return ProviderResult{}, nil
		}
		return ProviderResult{}, err
	case CommandCreateJWT:
		if command.TargetID == "" {
			return ProviderResult{}, ErrInvalidInput
		}
		token, err := adapter.client.TelephonyCredentials.NewToken(ctx, command.TargetID)
		if err != nil {
			return ProviderResult{}, classifyTelnyxSDKError(err)
		}
		if token == nil || strings.TrimSpace(*token) == "" {
			return ProviderResult{}, fmt.Errorf(
				"%w: invalid Telnyx JWT response",
				ErrAmbiguousEffect,
			)
		}
		return ProviderResult{
			JWT:          *token,
			JWTExpiresAt: jwtExpiration(*token),
		}, nil
	default:
		return ProviderResult{}, ErrInvalidInput
	}
}

func (adapter *TelnyxAdapter) dial(
	ctx context.Context,
	payload map[string]any,
) (ProviderResult, error) {
	var params telnyx.CallDialParams
	if err := decodeTelnyxParams(payload, &params); err != nil {
		return ProviderResult{}, ErrInvalidInput
	}
	response, err := adapter.client.Calls.Dial(ctx, params)
	if err != nil {
		return ProviderResult{}, classifyTelnyxSDKError(err)
	}
	if response == nil || response.Data.CallControlID == "" ||
		response.Data.CallLegID == "" {
		return ProviderResult{}, fmt.Errorf(
			"%w: invalid Telnyx Dial response",
			ErrAmbiguousEffect,
		)
	}
	return ProviderResult{
		CallControlID: response.Data.CallControlID,
		CallLegID:     response.Data.CallLegID,
	}, nil
}

func decodeTelnyxParams(payload map[string]any, target any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func validWebhookRetryPolicies(value any, events ...FactType) bool {
	policies, ok := value.(map[string]any)
	if !ok || len(policies) != len(events) {
		return false
	}
	for _, event := range events {
		policy, ok := policies[string(event)].(map[string]any)
		if !ok || len(policy) != 1 || !validRetryMilliseconds(policy["retries_ms"]) {
			return false
		}
	}
	return true
}

func validRetryMilliseconds(value any) bool {
	values, ok := value.([]any)
	if !ok {
		if integers, integersOK := value.([]int); integersOK {
			if len(integers) != len(telnyxWebhookRetryMilliseconds) {
				return false
			}
			for index := range integers {
				if integers[index] != telnyxWebhookRetryMilliseconds[index] {
					return false
				}
			}
			return true
		}
		return false
	}
	if len(values) != len(telnyxWebhookRetryMilliseconds) {
		return false
	}
	for index, value := range values {
		milliseconds, ok := value.(float64)
		if !ok || milliseconds != float64(telnyxWebhookRetryMilliseconds[index]) {
			return false
		}
	}
	return true
}

func (adapter *TelnyxAdapter) FindCredentialByName(
	ctx context.Context,
	name string,
) (ProviderResult, bool, error) {
	if strings.TrimSpace(name) == "" {
		return ProviderResult{}, false, ErrInvalidInput
	}
	response, err := adapter.client.TelephonyCredentials.List(
		ctx,
		telnyx.TelephonyCredentialListParams{
			PageSize: telnyx.Int(2),
			Filter: telnyx.TelephonyCredentialListParamsFilter{
				Name: telnyx.String(name),
			},
		},
	)
	if err != nil {
		return ProviderResult{}, false, classifyTelnyxSDKError(err)
	}
	if response == nil {
		return ProviderResult{}, false, fmt.Errorf(
			"%w: invalid Telnyx credential lookup response",
			ErrAmbiguousEffect,
		)
	}
	matches := make([]ProviderResult, 0, len(response.Data))
	for _, credential := range response.Data {
		if credential.Name == name &&
			!credential.Expired &&
			credential.ID != "" &&
			credential.SipUsername != "" {
			matches = append(matches, ProviderResult{
				CredentialID: credential.ID,
				SIPUsername:  credential.SipUsername,
			})
		}
	}
	if len(matches) == 0 {
		return ProviderResult{}, false, nil
	}
	if len(matches) != 1 {
		return ProviderResult{}, false, fmt.Errorf(
			"%w: credential lookup returned multiple active matches",
			ErrDefinitiveProviderFailure,
		)
	}
	return matches[0], true, nil
}

func (adapter *TelnyxAdapter) ResolveRecording(
	ctx context.Context,
	callLegID string,
	callSessionID string,
) (ProviderRecording, error) {
	callLegID = strings.TrimSpace(callLegID)
	callSessionID = strings.TrimSpace(callSessionID)
	if callLegID == "" || callSessionID == "" {
		return ProviderRecording{}, ErrInvalidInput
	}
	response, err := adapter.client.Recordings.List(
		ctx,
		telnyx.RecordingListParams{
			PageSize: telnyx.Int(2),
			Filter: telnyx.RecordingListParamsFilter{
				CallLegID:     telnyx.String(callLegID),
				CallSessionID: telnyx.String(callSessionID),
			},
		},
	)
	if err != nil {
		return ProviderRecording{}, classifyTelnyxSDKError(err)
	}
	if response == nil {
		return ProviderRecording{}, fmt.Errorf(
			"%w: invalid Telnyx recording response",
			ErrAmbiguousEffect,
		)
	}
	var resolved *ProviderRecording
	for _, recording := range response.Data {
		startedAt, startedErr := parseTelnyxTime(recording.RecordingStartedAt)
		endedAt, endedErr := parseTelnyxTime(recording.RecordingEndedAt)
		if recording.Status != telnyx.RecordingResponseDataStatusCompleted ||
			recording.ID == "" || recording.CallLegID != callLegID ||
			recording.CallSessionID != callSessionID || startedErr != nil ||
			endedErr != nil || !endedAt.After(startedAt) {
			continue
		}
		if resolved != nil {
			return ProviderRecording{}, fmt.Errorf("%w: multiple completed Telnyx recordings", ErrAmbiguousEffect)
		}
		resolved = &ProviderRecording{
			ID: recording.ID, CallControlID: recording.CallControlID,
			CallLegID: recording.CallLegID, CallSessionID: recording.CallSessionID,
			StartedAt: startedAt, EndedAt: endedAt,
		}
	}
	if resolved == nil {
		failedAt, err := adapter.recordingFailed(ctx, callLegID, callSessionID)
		if err != nil {
			return ProviderRecording{}, err
		}
		if !failedAt.IsZero() {
			return ProviderRecording{}, &providerRecordingFailure{OccurredAt: failedAt}
		}
		return ProviderRecording{}, fmt.Errorf("%w: Telnyx recording is not available", ErrAmbiguousEffect)
	}
	return *resolved, nil
}

func (adapter *TelnyxAdapter) recordingFailed(
	ctx context.Context,
	callLegID string,
	callSessionID string,
) (time.Time, error) {
	response, err := adapter.client.CallEvents.List(
		ctx,
		telnyx.CallEventListParams{
			PageSize: telnyx.Int(2),
			Filter: telnyx.CallEventListParamsFilter{
				LegID:                telnyx.String(callLegID),
				ApplicationSessionID: telnyx.String(callSessionID),
				Name:                 telnyx.String(string(FactRecordingError)),
				Type:                 "webhook",
			},
		},
	)
	if err != nil {
		return time.Time{}, classifyTelnyxSDKError(err)
	}
	if response == nil {
		return time.Time{}, fmt.Errorf(
			"%w: invalid Telnyx recording events response",
			ErrAmbiguousEffect,
		)
	}
	for _, event := range response.Data {
		eventTimestamp, timestampErr := parseTelnyxTime(event.EventTimestamp)
		if event.Name != string(FactRecordingError) ||
			event.CallLegID != callLegID ||
			event.CallSessionID != callSessionID ||
			timestampErr != nil {
			return time.Time{}, fmt.Errorf(
				"%w: contradictory Telnyx recording error identity",
				ErrDefinitiveProviderFailure,
			)
		}
		return eventTimestamp, nil
	}
	return time.Time{}, nil
}

func (adapter *TelnyxAdapter) DeleteRecording(
	ctx context.Context,
	recordingID string,
) error {
	if !validTelnyxResourceID(recordingID) {
		return ErrInvalidInput
	}
	_, err := adapter.client.Recordings.Delete(ctx, recordingID)
	err = classifyTelnyxSDKError(err)
	if errors.Is(err, ErrProviderTargetAbsent) {
		return nil
	}
	return err
}

func (adapter *TelnyxAdapter) ObserveCall(
	ctx context.Context,
	connectionID string,
	callControlID string,
	callLegID string,
	clientState string,
	since time.Time,
) (ProviderCallObservation, error) {
	if !validTelnyxResourceID(connectionID) || since.IsZero() ||
		(strings.TrimSpace(callLegID) == "" && strings.TrimSpace(clientState) == "") {
		return ProviderCallObservation{}, ErrInvalidInput
	}
	activeCalls := adapter.client.Connections.ListActiveCallsAutoPaging(
		ctx,
		connectionID,
		telnyx.ConnectionListActiveCallsParams{PageSize: telnyx.Int(250)},
	)
	observation := ProviderCallObservation{}
	for activeCalls.Next() {
		active := activeCalls.Current()
		matchesLeg := callLegID != "" && active.CallLegID == callLegID
		matchesState := callLegID == "" && clientState != "" && active.ClientState == clientState
		if !matchesLeg && !matchesState {
			continue
		}
		if observation.Active {
			return ProviderCallObservation{}, fmt.Errorf(
				"%w: multiple active Telnyx Calls match one CallLeg",
				ErrAmbiguousEffect,
			)
		}
		observation.Active = true
		observation.CallControlID = active.CallControlID
		observation.CallLegID = active.CallLegID
		observation.CallSessionID = active.CallSessionID
	}
	if err := activeCalls.Err(); err != nil {
		return ProviderCallObservation{}, classifyTelnyxSDKError(err)
	}
	if observation.Active {
		if callControlID != "" && observation.CallControlID != callControlID {
			return ProviderCallObservation{}, fmt.Errorf(
				"%w: active Telnyx Call identity changed",
				ErrDefinitiveProviderFailure,
			)
		}
		callLegID = observation.CallLegID
	}
	if callLegID == "" {
		return observation, nil
	}

	events := adapter.client.CallEvents.ListAutoPaging(
		ctx,
		telnyx.CallEventListParams{
			PageSize: telnyx.Int(100),
			Filter: telnyx.CallEventListParamsFilter{
				LegID: telnyx.String(callLegID),
				Type:  "webhook",
				OccurredAt: telnyx.CallEventListParamsFilterOccurredAt{
					Gte: telnyx.String(since.UTC().Format(time.RFC3339Nano)),
				},
			},
		},
	)
	for events.Next() {
		event := events.Current()
		if raw, ok := rawCallEvent(event.Metadata); ok {
			fact, known, normalizeErr := normalizeTelnyxFact(raw)
			if normalizeErr != nil {
				return ProviderCallObservation{}, fmt.Errorf(
					"%w: invalid Telnyx raw Call event",
					ErrAmbiguousEffect,
				)
			}
			if !known {
				continue
			}
			if fact.CallLegID != event.CallLegID ||
				fact.CallSessionID != event.CallSessionID {
				return ProviderCallObservation{}, fmt.Errorf(
					"%w: contradictory Telnyx raw Call event identity",
					ErrDefinitiveProviderFailure,
				)
			}
			observation.Events = append(observation.Events, fact)
			continue
		}
		factType := FactType(event.Name)
		switch factType {
		case FactCallInitiated, FactCallAnswered, FactCallBridged, FactCallHangup:
		default:
			continue
		}
		eventTimestamp, timestampErr := parseTelnyxTime(event.EventTimestamp)
		if event.CallLegID != callLegID || timestampErr != nil {
			return ProviderCallObservation{}, fmt.Errorf(
				"%w: contradictory Telnyx Call event identity",
				ErrDefinitiveProviderFailure,
			)
		}
		digest := sha256.Sum256([]byte(
			event.Name + "\x00" + event.CallLegID + "\x00" +
				event.CallSessionID + "\x00" + eventTimestamp.UTC().Format(time.RFC3339Nano),
		))
		observation.Events = append(observation.Events, ProviderFact{
			EventID:       fmt.Sprintf("telnyx-call-event-%x", digest[:]),
			Type:          factType,
			OccurredAt:    eventTimestamp,
			CallLegID:     event.CallLegID,
			CallSessionID: event.CallSessionID,
		})
	}
	if err := events.Err(); err != nil {
		return ProviderCallObservation{}, classifyTelnyxSDKError(err)
	}
	return observation, nil
}

func rawCallEvent(metadata map[string]any) ([]byte, bool) {
	for _, key := range []string{"raw", "raw_event", "event"} {
		raw, ok := metadata[key]
		if !ok || raw == nil {
			continue
		}
		if encoded, ok := raw.(string); ok {
			return []byte(encoded), true
		}
		encoded, err := json.Marshal(raw)
		return encoded, err == nil
	}
	return nil, false
}

func parseTelnyxTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil || parsed.IsZero() {
		return time.Time{}, errors.New("invalid Telnyx timestamp")
	}
	return parsed, nil
}

func (adapter *TelnyxAdapter) OpenRecording(
	ctx context.Context,
	recordingID string,
	rangeHeader string,
) (PlaybackContent, error) {
	if !validTelnyxResourceID(recordingID) {
		return PlaybackContent{}, ErrInvalidInput
	}
	metadataContext, cancel := context.WithTimeout(ctx, telnyxRecordingRequestTimeout)
	defer cancel()
	metadata, err := adapter.client.Recordings.Get(metadataContext, recordingID)
	if err != nil {
		return PlaybackContent{}, recordingSDKError(err)
	}
	if metadata == nil {
		return PlaybackContent{}, recordingUnavailable(RecordingInvalidResponse, "")
	}
	recordingURL := strings.TrimSpace(metadata.Data.DownloadURLs.MP3)
	if recordingURL == "" {
		recordingURL = strings.TrimSpace(metadata.Data.DownloadURLs.Wav)
	}
	parsed, err := url.Parse(recordingURL)
	allowLocalHTTP := strings.HasPrefix(adapter.config.BaseURL, "http://")
	allowedHost, locationErr := validateRecordingLocation(
		parsed,
		"",
		allowLocalHTTP,
	)
	if err != nil || locationErr != nil {
		return PlaybackContent{}, recordingUnavailable(
			RecordingInvalidResponse,
			"",
		)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		parsed.String(),
		nil,
	)
	if err != nil {
		return PlaybackContent{}, recordingUnavailable(
			RecordingInvalidResponse,
			"",
		)
	}
	if rangeHeader = strings.TrimSpace(rangeHeader); rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	audioClient := *adapter.config.HTTPClient
	audioClient.Timeout = 0
	previousRedirectCheck := audioClient.CheckRedirect
	audioClient.CheckRedirect = func(
		request *http.Request,
		via []*http.Request,
	) error {
		if len(via) >= 3 {
			return errInvalidRecordingLocation
		}
		if _, err := validateRecordingLocation(
			request.URL,
			allowedHost,
			allowLocalHTTP,
		); err != nil {
			return err
		}
		if previousRedirectCheck != nil {
			return previousRedirectCheck(request, via)
		}
		return nil
	}
	audio, err := doWithResponseHeaderTimeout(
		ctx,
		&audioClient,
		request,
		telnyxRecordingRequestTimeout,
	)
	if err != nil {
		if errors.Is(err, errInvalidRecordingLocation) {
			return PlaybackContent{}, recordingUnavailable(
				RecordingInvalidResponse,
				"",
			)
		}
		return PlaybackContent{}, recordingTransportError(err)
	}
	if audio.StatusCode != http.StatusOK &&
		audio.StatusCode != http.StatusPartialContent {
		audio.Body.Close()
		reason := RecordingProviderFailure
		if audio.StatusCode == http.StatusUnauthorized ||
			audio.StatusCode == http.StatusForbidden ||
			audio.StatusCode == http.StatusNotFound {
			reason = RecordingURLExpired
		} else if audio.StatusCode == http.StatusTooManyRequests {
			reason = RecordingRateLimited
		}
		return PlaybackContent{}, recordingUnavailable(
			reason,
			safeRetryAfter(audio.Header.Get("Retry-After")),
		)
	}
	contentType := strings.TrimSpace(strings.Split(
		audio.Header.Get("Content-Type"),
		";",
	)[0])
	if contentType == "" {
		contentType = "audio/mpeg"
	}
	content := PlaybackContent{
		StatusCode:    audio.StatusCode,
		ContentType:   contentType,
		ContentLength: audio.Header.Get("Content-Length"),
		ContentRange:  audio.Header.Get("Content-Range"),
		Body:          audio.Body,
	}
	if err := content.Validate(rangeHeader); err != nil {
		_ = content.Body.Close()
		return PlaybackContent{}, err
	}
	return content, nil
}

func validateRecordingLocation(
	location *url.URL,
	allowedHost string,
	allowLocalHTTP bool,
) (string, error) {
	if location == nil || location.Host == "" || location.User != nil ||
		(location.Scheme != "https" &&
			!(allowLocalHTTP && location.Scheme == "http")) {
		return "", errInvalidRecordingLocation
	}
	hostname := strings.ToLower(strings.TrimSuffix(location.Hostname(), "."))
	if hostname == "" || strings.Contains(hostname, "%") {
		return "", errInvalidRecordingLocation
	}
	endpoint := hostname
	if port := location.Port(); port != "" {
		endpoint = net.JoinHostPort(hostname, port)
	}
	if allowedHost != "" && endpoint != allowedHost {
		return "", errInvalidRecordingLocation
	}
	if !allowLocalHTTP && internalRecordingHost(hostname) {
		return "", errInvalidRecordingLocation
	}
	return endpoint, nil
}

func internalRecordingHost(hostname string) bool {
	if hostname == "localhost" ||
		strings.HasSuffix(hostname, ".localhost") ||
		strings.HasSuffix(hostname, ".local") ||
		strings.HasSuffix(hostname, ".internal") {
		return true
	}
	address := net.ParseIP(hostname)
	return address != nil &&
		(address.IsPrivate() ||
			address.IsLoopback() ||
			address.IsLinkLocalUnicast() ||
			address.IsUnspecified() ||
			address.IsMulticast())
}

type responseResult struct {
	response *http.Response
	err      error
}

func doWithResponseHeaderTimeout(
	ctx context.Context,
	client *http.Client,
	request *http.Request,
	timeout time.Duration,
) (*http.Response, error) {
	requestContext, cancel := context.WithCancel(ctx)
	request = request.Clone(requestContext)
	result := make(chan responseResult)
	go func() {
		response, err := client.Do(request)
		select {
		case result <- responseResult{response: response, err: err}:
		case <-requestContext.Done():
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case completed := <-result:
		if completed.err != nil {
			cancel()
			return nil, completed.err
		}
		completed.response.Body = &cancelingReadCloser{
			ReadCloser: completed.response.Body,
			cancel:     cancel,
		}
		return completed.response, nil
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	case <-timer.C:
		cancel()
		return nil, context.DeadlineExceeded
	}
}

type cancelingReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *cancelingReadCloser) Close() error {
	err := body.ReadCloser.Close()
	body.cancel()
	return err
}

func recordingTransportError(err error) error {
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &networkError) && networkError.Timeout()) {
		return recordingUnavailable(RecordingProviderTimeout, "")
	}
	return recordingUnavailable(RecordingProviderFailure, "")
}

func recordingSDKError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *telnyx.Error
	if !errors.As(err, &apiErr) {
		return recordingTransportError(err)
	}
	reason := RecordingProviderFailure
	switch apiErr.StatusCode {
	case http.StatusNotFound:
		reason = RecordingNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		reason = RecordingProviderAuth
	case http.StatusTooManyRequests:
		reason = RecordingRateLimited
	}
	retryAfter := ""
	if apiErr.Response != nil {
		retryAfter = safeRetryAfter(apiErr.Response.Header.Get("Retry-After"))
	}
	return recordingUnavailable(reason, retryAfter)
}

func recordingUnavailable(
	reason RecordingUnavailableReason,
	retryAfter string,
) error {
	return &RecordingUnavailableError{Reason: reason, RetryAfter: retryAfter}
}

func safeRetryAfter(value string) string {
	value = strings.TrimSpace(value)
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 || seconds > 3600 {
		return ""
	}
	return strconv.Itoa(seconds)
}

func validTelnyxResourceID(value string) bool {
	return value != "" &&
		value == strings.TrimSpace(value) &&
		value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\?#%")
}

func classifyTelnyxError(status int, body []byte) *ProviderError {
	var envelope struct {
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(body, &envelope)
	code := ""
	if len(envelope.Errors) > 0 {
		code = strings.TrimSpace(envelope.Errors[0].Code)
	}
	result := &ProviderError{HTTPStatus: status, Code: code}
	switch code {
	case "90018":
		result.SafeCode = "TELNYX_CALL_ENDED"
		result.Definitive = true
		result.TargetAbsent = true
		return result
	case "90034":
		result.SafeCode = "TELNYX_CALL_NOT_ANSWERED"
		return result
	case "90041":
		result.SafeCode = "TELNYX_USER_CHANNEL_LIMIT"
		result.Definitive = true
		return result
	case "90042":
		result.SafeCode = "TELNYX_PROFILE_CHANNEL_LIMIT"
		result.Definitive = true
		return result
	case "90043":
		result.SafeCode = "TELNYX_CONNECTION_CHANNEL_LIMIT"
		result.Definitive = true
		return result
	}
	switch {
	case status == http.StatusNotFound:
		result.SafeCode = "TELNYX_TARGET_ABSENT"
		result.Definitive = true
		result.TargetAbsent = true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		result.SafeCode = "TELNYX_AUTH_REJECTED"
		result.Definitive = true
	case status == http.StatusTooManyRequests:
		result.SafeCode = "TELNYX_RATE_LIMITED"
	case status == http.StatusRequestTimeout || status == http.StatusConflict ||
		status == http.StatusTooEarly || status >= http.StatusInternalServerError:
		result.SafeCode = "TELNYX_EFFECT_UNCERTAIN"
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		result.SafeCode = "TELNYX_INVALID_REQUEST"
		result.Definitive = true
	default:
		result.SafeCode = "TELNYX_EFFECT_UNCERTAIN"
	}
	return result
}

func classifyTelnyxSDKError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *telnyx.Error
	if errors.As(err, &apiErr) {
		return classifyTelnyxError(apiErr.StatusCode, []byte(apiErr.RawJSON()))
	}
	return &ProviderError{SafeCode: "TELNYX_TRANSPORT"}
}

func safeProviderErrorCode(err error) string {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.SafeCode
	}
	if errors.Is(err, ErrProviderTargetAbsent) {
		return "PROVIDER_TARGET_ABSENT"
	}
	if errors.Is(err, ErrDefinitiveProviderFailure) {
		return "PROVIDER_REJECTED"
	}
	return "PROVIDER_EFFECT_UNCERTAIN"
}

func clonePayload(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+4)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func emptyString(value any) bool {
	text, ok := value.(string)
	return !ok || strings.TrimSpace(text) == ""
}

func validMediaTokenHeader(value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	var headers []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(encoded, &headers); err != nil ||
		len(headers) != 1 ||
		!strings.EqualFold(headers[0].Name, "X-Acuity-Media-Token") {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(headers[0].Value)
	return err == nil && len(decoded) == 32
}

func jwtExpiration(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64RawURLDecode(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.ExpiresAt <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.ExpiresAt, 0)
}

func base64RawURLDecode(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("invalid JWT payload")
	}
	return decoded, nil
}

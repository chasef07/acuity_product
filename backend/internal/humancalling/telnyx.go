package humancalling

import (
	"bytes"
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
)

type TelnyxConfig struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

type TelnyxAdapter struct {
	config TelnyxConfig
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
	return &TelnyxAdapter{config: config}, nil
}

func (adapter *TelnyxAdapter) Execute(
	ctx context.Context,
	command ProviderCommand,
) (ProviderResult, error) {
	if command.ID == "" || command.Action == "" {
		return ProviderResult{}, ErrInvalidInput
	}
	payload := clonePayload(command.Payload)
	payload["command_id"] = command.ID
	method := http.MethodPost
	path := ""

	switch command.Action {
	case CommandAnswerCaller:
		if command.TargetID == "" ||
			payload["transcription"] != false ||
			!validWebhookRetryPolicies(payload["webhook_retries_policies"],
				FactCallAnswered, FactCallHangup) ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "answer")
	case CommandStartRingWindow:
		_, hasLoop := payload["loop"]
		if command.TargetID == "" ||
			emptyString(payload["audio_url"]) ||
			hasLoop ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "playback_start")
	case CommandStopRingWindow:
		if command.TargetID == "" || payload["stop"] != "all" ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "playback_stop")
	case CommandSpeakVoicemail:
		if command.TargetID == "" ||
			emptyString(payload["payload"]) ||
			emptyString(payload["voice"]) ||
			emptyString(payload["language"]) ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "speak")
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
		path = "/calls"
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
		path = "/calls"
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
		path = "/calls"
	case CommandBridge:
		if command.TargetID == "" ||
			emptyString(payload["call_control_id"]) ||
			payload["prevent_double_bridge"] != true ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "bridge")
	case CommandHangupLeg:
		if command.TargetID == "" {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "hangup")
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
		path = callActionPath(command.TargetID, "record_start")
	case CommandCreateCredential:
		if emptyString(payload["connection_id"]) ||
			emptyString(payload["name"]) ||
			emptyString(payload["tag"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = "/telephony_credentials"
		delete(payload, "command_id")
	case CommandDisableCredential:
		if command.TargetID == "" {
			return ProviderResult{}, ErrInvalidInput
		}
		path = "/telephony_credentials/" + url.PathEscape(command.TargetID)
		method = http.MethodDelete
		payload = nil
	case CommandCreateJWT:
		if command.TargetID == "" {
			return ProviderResult{}, ErrInvalidInput
		}
		path = "/telephony_credentials/" + url.PathEscape(command.TargetID) + "/token"
		delete(payload, "command_id")
	default:
		return ProviderResult{}, ErrInvalidInput
	}

	responseBody, err := adapter.request(ctx, method, path, payload)
	if err != nil {
		if command.Action == CommandDisableCredential &&
			errors.Is(err, ErrProviderTargetAbsent) {
			return ProviderResult{}, nil
		}
		return ProviderResult{}, err
	}
	switch command.Action {
	case CommandDialStaff, CommandDialOutboundStaff, CommandDialOutboundDestination:
		var response struct {
			Data struct {
				CallControlID string `json:"call_control_id"`
				CallLegID     string `json:"call_leg_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(responseBody, &response); err != nil ||
			response.Data.CallControlID == "" ||
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
	case CommandCreateCredential:
		var response struct {
			Data struct {
				ID          string `json:"id"`
				SIPUsername string `json:"sip_username"`
			} `json:"data"`
		}
		if err := json.Unmarshal(responseBody, &response); err != nil ||
			response.Data.ID == "" ||
			response.Data.SIPUsername == "" {
			return ProviderResult{}, fmt.Errorf(
				"%w: invalid Telnyx credential response",
				ErrAmbiguousEffect,
			)
		}
		return ProviderResult{
			CredentialID: response.Data.ID,
			SIPUsername:  response.Data.SIPUsername,
		}, nil
	case CommandCreateJWT:
		var token string
		if err := json.Unmarshal(responseBody, &token); err != nil {
			candidate := strings.TrimSpace(string(responseBody))
			if len(strings.Split(candidate, ".")) == 3 {
				token = candidate
			}
		}
		if token == "" {
			return ProviderResult{}, fmt.Errorf(
				"%w: invalid Telnyx JWT response",
				ErrAmbiguousEffect,
			)
		}
		return ProviderResult{
			JWT:          token,
			JWTExpiresAt: jwtExpiration(token),
		}, nil
	default:
		return ProviderResult{}, nil
	}
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
	query := url.Values{}
	query.Set("filter[name]", name)
	query.Set("page[size]", "2")
	responseBody, err := adapter.request(
		ctx,
		http.MethodGet,
		"/telephony_credentials?"+query.Encode(),
		nil,
	)
	if err != nil {
		return ProviderResult{}, false, err
	}
	var response struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			SIPUsername string `json:"sip_username"`
			Expired     bool   `json:"expired"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
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
			credential.SIPUsername != "" {
			matches = append(matches, ProviderResult{
				CredentialID: credential.ID,
				SIPUsername:  credential.SIPUsername,
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
	query := url.Values{}
	query.Set("filter[call_leg_id]", callLegID)
	query.Set("filter[call_session_id]", callSessionID)
	query.Set("page[size]", "2")
	responseBody, err := adapter.request(
		ctx,
		http.MethodGet,
		"/recordings?"+query.Encode(),
		nil,
	)
	if err != nil {
		return ProviderRecording{}, err
	}
	var response struct {
		Data []struct {
			ID               string    `json:"id"`
			CallControlID    string    `json:"call_control_id"`
			CallLegID        string    `json:"call_leg_id"`
			CallSessionID    string    `json:"call_session_id"`
			Status           string    `json:"status"`
			RecordingStarted time.Time `json:"recording_started_at"`
			RecordingEnded   time.Time `json:"recording_ended_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return ProviderRecording{}, fmt.Errorf("%w: invalid Telnyx recording response", ErrAmbiguousEffect)
	}
	var resolved *ProviderRecording
	for _, recording := range response.Data {
		if recording.Status != "completed" || recording.ID == "" ||
			recording.CallLegID != callLegID || recording.CallSessionID != callSessionID ||
			!recording.RecordingEnded.After(recording.RecordingStarted) {
			continue
		}
		if resolved != nil {
			return ProviderRecording{}, fmt.Errorf("%w: multiple completed Telnyx recordings", ErrAmbiguousEffect)
		}
		resolved = &ProviderRecording{
			ID: recording.ID, CallControlID: recording.CallControlID,
			CallLegID: recording.CallLegID, CallSessionID: recording.CallSessionID,
			StartedAt: recording.RecordingStarted, EndedAt: recording.RecordingEnded,
		}
	}
	if resolved == nil {
		return ProviderRecording{}, fmt.Errorf("%w: Telnyx recording is not available", ErrAmbiguousEffect)
	}
	return *resolved, nil
}

func (adapter *TelnyxAdapter) ObserveCall(
	ctx context.Context,
	connectionID string,
	callControlID string,
	callLegID string,
	clientState string,
	since time.Time,
) (ProviderCallObservation, error) {
	if strings.TrimSpace(connectionID) == "" || since.IsZero() ||
		(strings.TrimSpace(callLegID) == "" && strings.TrimSpace(clientState) == "") {
		return ProviderCallObservation{}, ErrInvalidInput
	}
	activeQuery := url.Values{}
	activeQuery.Set("page[size]", "250")
	responseBody, err := adapter.request(
		ctx,
		http.MethodGet,
		"/connections/"+url.PathEscape(connectionID)+"/active_calls?"+activeQuery.Encode(),
		nil,
	)
	if err != nil {
		return ProviderCallObservation{}, err
	}
	var activeResponse struct {
		Data []struct {
			CallControlID string `json:"call_control_id"`
			CallLegID     string `json:"call_leg_id"`
			CallSessionID string `json:"call_session_id"`
			ClientState   string `json:"client_state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &activeResponse); err != nil {
		return ProviderCallObservation{}, fmt.Errorf(
			"%w: invalid Telnyx active Calls response",
			ErrAmbiguousEffect,
		)
	}
	observation := ProviderCallObservation{}
	for _, active := range activeResponse.Data {
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

	eventQuery := url.Values{}
	eventQuery.Set("filter[leg_id]", callLegID)
	eventQuery.Set("filter[type]", "webhook")
	eventQuery.Set("filter[occurred_at][gte]", since.UTC().Format(time.RFC3339Nano))
	eventQuery.Set("page[size]", "100")
	eventBody, err := adapter.request(
		ctx,
		http.MethodGet,
		"/call_events?"+eventQuery.Encode(),
		nil,
	)
	if err != nil {
		return ProviderCallObservation{}, err
	}
	var eventResponse struct {
		Data []struct {
			Name           string                     `json:"name"`
			CallLegID      string                     `json:"call_leg_id"`
			CallSessionID  string                     `json:"call_session_id"`
			EventTimestamp time.Time                  `json:"event_timestamp"`
			Metadata       map[string]json.RawMessage `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(eventBody, &eventResponse); err != nil {
		return ProviderCallObservation{}, fmt.Errorf(
			"%w: invalid Telnyx Call events response",
			ErrAmbiguousEffect,
		)
	}
	for _, event := range eventResponse.Data {
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
		if event.CallLegID != callLegID || event.EventTimestamp.IsZero() {
			return ProviderCallObservation{}, fmt.Errorf(
				"%w: contradictory Telnyx Call event identity",
				ErrDefinitiveProviderFailure,
			)
		}
		digest := sha256.Sum256([]byte(
			event.Name + "\x00" + event.CallLegID + "\x00" +
				event.CallSessionID + "\x00" + event.EventTimestamp.UTC().Format(time.RFC3339Nano),
		))
		observation.Events = append(observation.Events, ProviderFact{
			EventID:       fmt.Sprintf("telnyx-call-event-%x", digest[:]),
			Type:          factType,
			OccurredAt:    event.EventTimestamp,
			CallLegID:     event.CallLegID,
			CallSessionID: event.CallSessionID,
		})
	}
	return observation, nil
}

func rawCallEvent(metadata map[string]json.RawMessage) ([]byte, bool) {
	for _, key := range []string{"raw", "raw_event", "event"} {
		raw := metadata[key]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var encoded string
		if raw[0] == '"' && json.Unmarshal(raw, &encoded) == nil {
			return []byte(encoded), true
		}
		return raw, true
	}
	return nil, false
}

func (adapter *TelnyxAdapter) OpenVoicemailRecording(
	ctx context.Context,
	recordingID string,
	rangeHeader string,
) (PlaybackContent, error) {
	recordingID = strings.TrimSpace(recordingID)
	if recordingID == "" {
		return PlaybackContent{}, ErrInvalidInput
	}
	metadata, err := adapter.recordingMetadata(ctx, recordingID)
	if err != nil {
		return PlaybackContent{}, err
	}
	var response struct {
		Data struct {
			DownloadURLs        recordingURLs `json:"download_urls"`
			PublicRecordingURLs recordingURLs `json:"public_recording_urls"`
			RecordingURLs       recordingURLs `json:"recording_urls"`
			RecordingURL        string        `json:"recording_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(metadata, &response); err != nil {
		return PlaybackContent{}, voicemailUnavailable(
			VoicemailProviderInvalid,
			"",
		)
	}
	recordingURL := firstRecordingURL(
		response.Data.DownloadURLs,
		response.Data.PublicRecordingURLs,
		response.Data.RecordingURLs,
	)
	if recordingURL == "" {
		recordingURL = strings.TrimSpace(response.Data.RecordingURL)
	}
	parsed, err := url.Parse(recordingURL)
	allowLocalHTTP := strings.HasPrefix(adapter.config.BaseURL, "http://")
	allowedHost, locationErr := validateRecordingLocation(
		parsed,
		"",
		allowLocalHTTP,
	)
	if err != nil || locationErr != nil {
		return PlaybackContent{}, voicemailUnavailable(
			VoicemailProviderInvalid,
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
		return PlaybackContent{}, voicemailUnavailable(
			VoicemailProviderInvalid,
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
			return PlaybackContent{}, voicemailUnavailable(
				VoicemailProviderInvalid,
				"",
			)
		}
		return PlaybackContent{}, voicemailTransportError(err)
	}
	if audio.StatusCode != http.StatusOK &&
		audio.StatusCode != http.StatusPartialContent {
		audio.Body.Close()
		reason := VoicemailProviderUnavailable
		if audio.StatusCode == http.StatusUnauthorized ||
			audio.StatusCode == http.StatusForbidden ||
			audio.StatusCode == http.StatusNotFound {
			reason = VoicemailRecordingURLExpired
		} else if audio.StatusCode == http.StatusTooManyRequests {
			reason = VoicemailProviderRateLimited
		}
		return PlaybackContent{}, voicemailUnavailable(
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

func (adapter *TelnyxAdapter) recordingMetadata(
	ctx context.Context,
	recordingID string,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, telnyxRecordingRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		adapter.config.BaseURL+"/recordings/"+url.PathEscape(recordingID),
		nil,
	)
	if err != nil {
		return nil, voicemailUnavailable(VoicemailProviderInvalid, "")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+adapter.config.APIKey)
	response, err := adapter.config.HTTPClient.Do(request)
	if err != nil {
		return nil, voicemailTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		reason := VoicemailProviderUnavailable
		switch response.StatusCode {
		case http.StatusNotFound:
			reason = VoicemailRecordingNotFound
		case http.StatusUnauthorized, http.StatusForbidden:
			reason = VoicemailProviderAuth
		case http.StatusTooManyRequests:
			reason = VoicemailProviderRateLimited
		}
		return nil, voicemailUnavailable(
			reason,
			safeRetryAfter(response.Header.Get("Retry-After")),
		)
	}
	metadata, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(metadata) == 0 || len(metadata) > 64*1024 {
		return nil, voicemailUnavailable(VoicemailProviderInvalid, "")
	}
	return metadata, nil
}

func voicemailTransportError(err error) error {
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &networkError) && networkError.Timeout()) {
		return voicemailUnavailable(VoicemailProviderTimeout, "")
	}
	return voicemailUnavailable(VoicemailProviderUnavailable, "")
}

func voicemailUnavailable(
	reason VoicemailUnavailableReason,
	retryAfter string,
) error {
	return &VoicemailUnavailableError{Reason: reason, RetryAfter: retryAfter}
}

func safeRetryAfter(value string) string {
	value = strings.TrimSpace(value)
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 || seconds > 3600 {
		return ""
	}
	return strconv.Itoa(seconds)
}

type recordingURLs struct {
	MP3 string `json:"mp3"`
	WAV string `json:"wav"`
}

func firstRecordingURL(groups ...recordingURLs) string {
	for _, group := range groups {
		if value := strings.TrimSpace(group.MP3); value != "" {
			return value
		}
		if value := strings.TrimSpace(group.WAV); value != "" {
			return value
		}
	}
	return ""
}

func (adapter *TelnyxAdapter) request(
	ctx context.Context,
	method string,
	path string,
	payload map[string]any,
) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode Telnyx command: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		adapter.config.BaseURL+path,
		body,
	)
	if err != nil {
		return nil, fmt.Errorf("build Telnyx command: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+adapter.config.APIKey)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := adapter.config.HTTPClient.Do(request)
	if err != nil {
		return nil, &ProviderError{SafeCode: "TELNYX_TRANSPORT"}
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if readErr != nil {
		return nil, &ProviderError{HTTPStatus: response.StatusCode, SafeCode: "TELNYX_INCOMPLETE_RESPONSE"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, classifyTelnyxError(response.StatusCode, responseBody)
	}
	return responseBody, nil
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

func callActionPath(callControlID string, action string) string {
	return "/calls/" + url.PathEscape(callControlID) + "/actions/" + action
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

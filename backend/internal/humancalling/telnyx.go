package humancalling

import (
	"bytes"
	"context"
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

var errInvalidRecordingLocation = errors.New("invalid recording location")

const telnyxRecordingRequestTimeout = 5 * time.Second

func NewTelnyxAdapter(config TelnyxConfig) (*TelnyxAdapter, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("Telnyx API key is required")
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
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "answer")
	case CommandStartRingback:
		if command.TargetID == "" ||
			emptyString(payload["audio_url"]) ||
			payload["loop"] != "infinity" ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "playback_start")
	case CommandPlayVoicemailGreeting:
		if command.TargetID == "" ||
			emptyString(payload["greeting"]) ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		payload["payload"] = payload["greeting"]
		payload["voice"] = "Polly.Matthew"
		payload["language"] = "en-US"
		delete(payload, "greeting")
		path = callActionPath(command.TargetID, "speak")
	case CommandDialStaff:
		timeoutSeconds, validTimeout := payload["timeout_secs"].(float64)
		mediaPrep := payload["media_prep"] == true
		if emptyString(payload["to"]) ||
			emptyString(payload["connection_id"]) ||
			emptyString(payload["from"]) ||
			emptyString(payload["client_state"]) ||
			!validMediaTokenHeader(payload["custom_headers"]) ||
			!validTimeout ||
			timeoutSeconds <= 0 ||
			timeoutSeconds != float64(int(timeoutSeconds)) ||
			(!mediaPrep &&
				(emptyString(payload["link_to"]) ||
					payload["bridge_intent"] != true ||
					payload["bridge_on_answer"] != true ||
					payload["prevent_double_bridge"] != true)) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = "/calls"
	case CommandDialDestination:
		timeoutSeconds, validTimeout := payload["timeout_secs"].(float64)
		if emptyString(payload["to"]) ||
			emptyString(payload["connection_id"]) ||
			emptyString(payload["from"]) ||
			emptyString(payload["link_to"]) ||
			emptyString(payload["client_state"]) ||
			!validTimeout ||
			timeoutSeconds != 30 ||
			payload["bridge_intent"] != true ||
			payload["bridge_on_answer"] != true ||
			payload["answering_machine_detection"] != "disabled" {
			return ProviderResult{}, ErrInvalidInput
		}
		path = "/calls"
	case CommandHangup:
		if command.TargetID == "" {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "hangup")
	case CommandStartRecording:
		if command.TargetID == "" ||
			payload["format"] != "wav" ||
			payload["channels"] != "dual" ||
			payload["recording_track"] != "both" ||
			payload["transcription"] != false ||
			emptyString(payload["client_state"]) {
			return ProviderResult{}, ErrInvalidInput
		}
		path = callActionPath(command.TargetID, "record_start")
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
		if (command.Action == CommandHangup ||
			command.Action == CommandDisableCredential) &&
			errors.Is(err, ErrProviderTargetAbsent) {
			return ProviderResult{}, nil
		}
		return ProviderResult{}, err
	}
	switch command.Action {
	case CommandDialStaff, CommandDialDestination:
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

func (adapter *TelnyxAdapter) IsCallAlive(
	ctx context.Context,
	callControlID string,
) (bool, error) {
	if strings.TrimSpace(callControlID) == "" {
		return false, ErrInvalidInput
	}
	responseBody, err := adapter.request(
		ctx,
		http.MethodGet,
		"/calls/"+url.PathEscape(callControlID),
		nil,
	)
	if errors.Is(err, ErrProviderTargetAbsent) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var response struct {
		Data struct {
			IsAlive *bool `json:"is_alive"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil ||
		response.Data.IsAlive == nil {
		return false, fmt.Errorf(
			"%w: invalid Telnyx Call status response",
			ErrAmbiguousEffect,
		)
	}
	return *response.Data.IsAlive, nil
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
		return nil, fmt.Errorf("%w: Telnyx transport failed", ErrAmbiguousEffect)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if readErr != nil {
		return nil, fmt.Errorf("%w: Telnyx response was incomplete", ErrAmbiguousEffect)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf(
				"%w: %w",
				ErrDefinitiveProviderFailure,
				ErrProviderTargetAbsent,
			)
		}
		if response.StatusCode >= 400 &&
			response.StatusCode < 500 &&
			response.StatusCode != http.StatusRequestTimeout &&
			response.StatusCode != http.StatusConflict &&
			response.StatusCode != http.StatusTooEarly &&
			response.StatusCode != http.StatusTooManyRequests {
			return nil, fmt.Errorf(
				"%w: Telnyx rejected command with status %d",
				ErrDefinitiveProviderFailure,
				response.StatusCode,
			)
		}
		return nil, fmt.Errorf(
			"%w: Telnyx command returned status %d",
			ErrAmbiguousEffect,
			response.StatusCode,
		)
	}
	return responseBody, nil
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

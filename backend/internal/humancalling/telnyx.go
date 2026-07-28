package humancalling

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	case CommandDialStaff:
		timeoutSeconds, validTimeout := payload["timeout_secs"].(float64)
		if emptyString(payload["to"]) ||
			emptyString(payload["connection_id"]) ||
			emptyString(payload["from"]) ||
			emptyString(payload["link_to"]) ||
			emptyString(payload["client_state"]) ||
			!validTimeout ||
			timeoutSeconds <= 0 ||
			timeoutSeconds != float64(int(timeoutSeconds)) ||
			payload["bridge_intent"] != true ||
			payload["bridge_on_answer"] != true ||
			payload["prevent_double_bridge"] != true {
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
	case CommandDialStaff:
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

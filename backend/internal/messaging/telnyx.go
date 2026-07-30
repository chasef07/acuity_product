package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TelnyxConfig struct {
	APIKey         string
	BaseURL        string
	WebhookBaseURL string
	HTTPClient     *http.Client
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
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("valid Telnyx API base URL is required")
	}
	webhookURL, err := url.Parse(config.WebhookBaseURL)
	if err != nil ||
		webhookURL.Scheme != "https" ||
		webhookURL.Host == "" ||
		webhookURL.RawQuery != "" ||
		webhookURL.Fragment != "" {
		return nil, fmt.Errorf("public HTTPS messaging webhook URL is required")
	}
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	config.WebhookBaseURL = strings.TrimRight(config.WebhookBaseURL, "/")
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &TelnyxAdapter{config: config}, nil
}

func (adapter *TelnyxAdapter) Send(
	ctx context.Context,
	command ProviderCommand,
) (ProviderResult, error) {
	if command.ID == "" ||
		command.MessageID == "" ||
		command.CallbackToken == "" ||
		command.MessagingProfileID == "" ||
		!canonicalPhone.MatchString(command.Sender) ||
		!canonicalPhone.MatchString(command.Destination) ||
		(command.Body == "" && command.MediaURL == "") {
		return ProviderResult{}, ErrInvalidInput
	}
	payload := map[string]any{
		"from":                 command.Sender,
		"to":                   command.Destination,
		"messaging_profile_id": command.MessagingProfileID,
		"webhook_url": adapter.config.WebhookBaseURL + "/" +
			url.PathEscape(command.CallbackToken),
		"use_profile_webhooks": false,
	}
	if command.Body != "" {
		payload["text"] = command.Body
	}
	if command.MediaURL != "" {
		payload["type"] = "MMS"
		payload["media_urls"] = []string{command.MediaURL}
	} else {
		payload["type"] = "SMS"
	}
	responseBody, err := adapter.request(
		ctx,
		http.MethodPost,
		"/messages",
		payload,
	)
	if err != nil {
		return ProviderResult{}, err
	}
	var response struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil ||
		strings.TrimSpace(response.Data.ID) == "" {
		return ProviderResult{}, fmt.Errorf(
			"%w: invalid Telnyx Message response",
			ErrAmbiguous,
		)
	}
	return ProviderResult{
		MessageID: response.Data.ID,
		State:     DeliverySent,
	}, nil
}

func (adapter *TelnyxAdapter) Reconcile(
	ctx context.Context,
	providerMessageID string,
) (ProviderResult, error) {
	providerMessageID = strings.TrimSpace(providerMessageID)
	if providerMessageID == "" {
		return ProviderResult{}, ErrInvalidInput
	}
	responseBody, err := adapter.request(
		ctx,
		http.MethodGet,
		"/messages/"+url.PathEscape(providerMessageID),
		nil,
	)
	if err != nil {
		return ProviderResult{}, err
	}
	var response struct {
		Data struct {
			ID string `json:"id"`
			To []struct {
				Status string `json:"status"`
			} `json:"to"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil ||
		response.Data.ID != providerMessageID {
		return ProviderResult{}, fmt.Errorf(
			"%w: invalid Telnyx Message lookup response",
			ErrAmbiguous,
		)
	}
	result := ProviderResult{
		MessageID: providerMessageID,
		State:     DeliverySent,
	}
	if len(response.Data.To) > 0 {
		result.State = providerDeliveryState(response.Data.To[0].Status)
	}
	return result, nil
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
			return nil, fmt.Errorf("encode Telnyx request: %w", err)
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
		return nil, fmt.Errorf("build Telnyx request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+adapter.config.APIKey)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := adapter.config.HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: Telnyx request: %v", ErrAmbiguous, err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if readErr != nil {
		return nil, fmt.Errorf("%w: read Telnyx response: %v", ErrAmbiguous, readErr)
	}
	if response.StatusCode >= http.StatusBadRequest &&
		response.StatusCode < http.StatusInternalServerError {
		return nil, fmt.Errorf(
			"%w: Telnyx returned %s",
			ErrRejected,
			response.Status,
		)
	}
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf(
			"%w: Telnyx returned %s",
			ErrAmbiguous,
			response.Status,
		)
	}
	return responseBody, nil
}

func providerDeliveryState(status string) DeliveryState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "delivered":
		return DeliveryDelivered
	case "sending_failed", "delivery_failed":
		return DeliveryFailed
	default:
		return DeliverySent
	}
}

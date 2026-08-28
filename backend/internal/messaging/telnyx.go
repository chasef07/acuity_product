package messaging

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/team-telnyx/telnyx-go/v4"
	"github.com/team-telnyx/telnyx-go/v4/option"
)

type TelnyxConfig struct {
	APIKey         string
	BaseURL        string
	WebhookBaseURL string
	HTTPClient     *http.Client
}

type TelnyxAdapter struct {
	client         telnyx.Client
	webhookBaseURL string
}

func NewTelnyxAdapter(config TelnyxConfig) (*TelnyxAdapter, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("telnyx API key is required")
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
	config.WebhookBaseURL = strings.TrimRight(config.WebhookBaseURL, "/")
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	client := telnyx.NewClient(
		option.WithAPIKey(config.APIKey),
		option.WithBaseURL(strings.TrimRight(config.BaseURL, "/")+"/"),
		option.WithHTTPClient(config.HTTPClient),
		option.WithMaxRetries(0),
	)
	return &TelnyxAdapter{
		client:         client,
		webhookBaseURL: config.WebhookBaseURL,
	}, nil
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
	params := telnyx.MessageSendParams{
		From:               telnyx.String(command.Sender),
		To:                 command.Destination,
		MessagingProfileID: telnyx.String(command.MessagingProfileID),
		WebhookURL: telnyx.String(adapter.webhookBaseURL + "/" +
			url.PathEscape(command.CallbackToken)),
		UseProfileWebhooks: telnyx.Bool(false),
	}
	if command.Body != "" {
		params.Text = telnyx.String(command.Body)
	}
	if command.MediaURL != "" {
		params.Type = telnyx.MessageSendParamsTypeMms
		params.MediaURLs = []string{command.MediaURL}
	} else {
		params.Type = telnyx.MessageSendParamsTypeSMS
	}
	response, err := adapter.client.Messages.Send(ctx, params)
	if err != nil {
		return ProviderResult{}, classifyTelnyxError(err)
	}
	if response == nil || strings.TrimSpace(response.Data.ID) == "" {
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
	if !validTelnyxResourceID(providerMessageID) {
		return ProviderResult{}, ErrInvalidInput
	}
	response, err := adapter.client.Messages.Get(ctx, providerMessageID)
	if err != nil {
		return ProviderResult{}, classifyTelnyxError(err)
	}
	if response == nil ||
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
	to := response.Data.To.OfMessagingOutboundMessagePayloadToArray
	if len(to) > 0 {
		result.State = providerDeliveryState(to[0].Status)
	}
	return result, nil
}

func validTelnyxResourceID(value string) bool {
	return value != "" &&
		value == strings.TrimSpace(value) &&
		value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\?#%")
}

func classifyTelnyxError(err error) error {
	var apiError *telnyx.Error
	if errors.As(err, &apiError) &&
		apiError.StatusCode >= http.StatusBadRequest &&
		apiError.StatusCode < http.StatusInternalServerError {
		return fmt.Errorf("%w: Telnyx rejected the request", ErrRejected)
	}
	return fmt.Errorf("%w: Telnyx request outcome is unknown", ErrAmbiguous)
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

package telnyxapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/team-telnyx/telnyx-go/v4"
	"github.com/team-telnyx/telnyx-go/v4/option"
)

var ErrUnverifiedWebhook = errors.New("telnyx webhook signature did not verify")

func VerifyWebhook(
	raw []byte,
	timestamp string,
	signature string,
	publicKeys [][]byte,
) (*telnyx.UnwrapWebhookEventUnion, error) {
	headers := http.Header{}
	headers.Set("telnyx-timestamp", timestamp)
	headers.Set("telnyx-signature-ed25519", signature)
	webhooks := telnyx.NewWebhookService()
	for _, publicKey := range publicKeys {
		if len(publicKey) == 0 {
			continue
		}
		event, err := webhooks.Unwrap(
			raw,
			headers,
			option.WithPublicKey(base64.StdEncoding.EncodeToString(publicKey)),
		)
		if err == nil {
			return event, nil
		}
	}
	return nil, ErrUnverifiedWebhook
}

func ValidResourceID(value string) bool {
	return value != "" &&
		value == strings.TrimSpace(value) &&
		value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\?#%")
}

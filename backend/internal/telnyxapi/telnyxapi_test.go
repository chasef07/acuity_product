package telnyxapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestVerifyWebhookTriesEveryKeyAndVerifiesHeadersVerbatim(t *testing.T) {
	oldPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	currentPublicKey, currentPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.answered","id":"verify-event","occurred_at":%q,"payload":{}}}`,
		now.Format(time.RFC3339Nano),
	))
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		currentPrivateKey,
		append([]byte(timestamp+"|"), raw...),
	))

	for name, scenario := range map[string]struct {
		timestamp, signature string
		keys                 [][]byte
		verified             bool
	}{
		"second key verifies": {timestamp, signature, [][]byte{nil, oldPublicKey, currentPublicKey}, true},
		"only the old key":    {timestamp, signature, [][]byte{oldPublicKey}, false},
		"no keys":             {timestamp, signature, nil, false},
		"padded timestamp":    {" " + timestamp + " ", signature, [][]byte{currentPublicKey}, false},
		"padded signature":    {timestamp, " " + signature + " ", [][]byte{currentPublicKey}, false},
		"invalid signature":   {timestamp, base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)), [][]byte{currentPublicKey}, false},
	} {
		t.Run(name, func(t *testing.T) {
			event, err := VerifyWebhook(raw, scenario.timestamp, scenario.signature, scenario.keys)
			if scenario.verified && (err != nil || event.Data.ID != "verify-event") {
				t.Fatalf("event=%#v err=%v", event, err)
			}
			if !scenario.verified && err == nil {
				t.Fatal("webhook verified")
			}
		})
	}
}

func TestValidResourceID(t *testing.T) {
	for value, want := range map[string]bool{
		"v3:abc_123-XYZ": true,
		"":               false,
		" leg":           false,
		".":              false,
		"..":             false,
		"a/b":            false,
		`a\b`:            false,
		"a?b":            false,
		"a#b":            false,
		"a%2Fb":          false,
	} {
		if got := ValidResourceID(value); got != want {
			t.Errorf("ValidResourceID(%q) = %v, want %v", value, got, want)
		}
	}
}

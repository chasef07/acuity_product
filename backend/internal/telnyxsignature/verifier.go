package telnyxsignature

import (
	"crypto/ed25519"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

type Verifier struct {
	keys      []ed25519.PublicKey
	tolerance time.Duration
	now       func() time.Time
}

func New(
	keys []ed25519.PublicKey,
	tolerance time.Duration,
	now func() time.Time,
) (Verifier, bool) {
	if len(keys) < 1 || len(keys) > 2 || tolerance <= 0 {
		return Verifier{}, false
	}
	for _, key := range keys {
		if len(key) != ed25519.PublicKeySize {
			return Verifier{}, false
		}
	}
	if now == nil {
		now = time.Now
	}
	return Verifier{keys: keys, tolerance: tolerance, now: now}, true
}

func (verifier Verifier) Verify(
	raw []byte,
	timestampHeader string,
	signatureHeader string,
) bool {
	timestamp, err := strconv.ParseInt(strings.TrimSpace(timestampHeader), 10, 64)
	if err != nil {
		return false
	}
	age := verifier.now().Sub(time.Unix(timestamp, 0))
	if age < -verifier.tolerance || age > verifier.tolerance {
		return false
	}
	signature, err := base64.StdEncoding.DecodeString(signatureHeader)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return false
	}
	signed := make([]byte, 0, len(timestampHeader)+1+len(raw))
	signed = append(signed, timestampHeader...)
	signed = append(signed, '|')
	signed = append(signed, raw...)
	for _, key := range verifier.keys {
		if ed25519.Verify(key, signed, signature) {
			return true
		}
	}
	return false
}

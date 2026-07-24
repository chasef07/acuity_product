package authn_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/authn"
)

func TestJWKSAuthenticatorAcceptsCurrentCredentialAndRefreshesUnknownKey(t *testing.T) {
	publicOne, privateOne, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicTwo, privateTwo, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	var keysMu sync.RWMutex
	keys := []jwkFixture{{Kid: "key-1", PublicKey: publicOne}}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		keysMu.RLock()
		defer keysMu.RUnlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	defer jwks.Close()

	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	adapter, err := authn.NewJWKSAuthenticator(authn.JWKSConfig{
		URL:        jwks.URL,
		Issuer:     "https://auth.acuity.test",
		Audience:   "https://api.acuity.test",
		HTTPClient: jwks.Client(),
		CacheTTL:   time.Hour,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new JWKS adapter: %v", err)
	}

	first := signedJWT(t, "key-1", privateOne, map[string]any{
		"iss":            "https://auth.acuity.test",
		"aud":            "https://api.acuity.test",
		"sub":            "better-auth-user-1",
		"email":          "USER@ACUITY.TEST",
		"email_verified": true,
		"exp":            now.Add(15 * time.Minute).Unix(),
	})
	identity, err := adapter.Authenticate(context.Background(), first)
	if err != nil {
		t.Fatalf("authenticate first key: %v", err)
	}
	if identity.Subject != "better-auth-user-1" ||
		identity.Email != "user@acuity.test" ||
		!identity.EmailVerified {
		t.Fatalf("identity = %#v", identity)
	}

	keysMu.Lock()
	keys = []jwkFixture{
		{Kid: "key-1", PublicKey: publicOne},
		{Kid: "key-2", PublicKey: publicTwo},
	}
	keysMu.Unlock()
	rotated := signedJWT(t, "key-2", privateTwo, map[string]any{
		"iss":            "https://auth.acuity.test",
		"aud":            []string{"https://other.test", "https://api.acuity.test"},
		"sub":            "better-auth-user-1",
		"email":          "user@acuity.test",
		"email_verified": true,
		"exp":            now.Add(15 * time.Minute).Unix(),
	})
	if _, err := adapter.Authenticate(context.Background(), rotated); err != nil {
		t.Fatalf("authenticate after unknown-kid refresh: %v", err)
	}
}

func TestJWKSAuthenticatorRejectsInvalidObservableCredentials(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []jwkFixture{{Kid: "key-1", PublicKey: publicKey}},
		})
	}))
	defer jwks.Close()

	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	adapter, err := authn.NewJWKSAuthenticator(authn.JWKSConfig{
		URL:        jwks.URL,
		Issuer:     "https://auth.acuity.test",
		Audience:   "https://api.acuity.test",
		HTTPClient: jwks.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	valid := map[string]any{
		"iss":            "https://auth.acuity.test",
		"aud":            "https://api.acuity.test",
		"sub":            "better-auth-user-1",
		"email":          "user@acuity.test",
		"email_verified": true,
		"exp":            now.Add(15 * time.Minute).Unix(),
	}
	cases := map[string]map[string]any{
		"expired":          cloneClaims(valid, "exp", now.Add(-time.Minute).Unix()),
		"wrong issuer":     cloneClaims(valid, "iss", "https://wrong.test"),
		"wrong audience":   cloneClaims(valid, "aud", "https://wrong.test"),
		"missing subject":  cloneClaims(valid, "sub", ""),
		"unverified email": cloneClaims(valid, "email_verified", false),
	}
	for name, claims := range cases {
		t.Run(name, func(t *testing.T) {
			token := signedJWT(t, "key-1", privateKey, claims)
			_, err := adapter.Authenticate(context.Background(), token)
			if !errors.Is(err, authn.ErrInvalidCredential) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

type jwkFixture struct {
	Kid       string
	PublicKey ed25519.PublicKey
}

func (fixture jwkFixture) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{
		"kty": "OKP",
		"crv": "Ed25519",
		"alg": "EdDSA",
		"use": "sig",
		"kid": fixture.Kid,
		"x":   base64.RawURLEncoding.EncodeToString(fixture.PublicKey),
	})
}

func signedJWT(t *testing.T, kid string, key ed25519.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(body)
	signature := ed25519.Sign(key, []byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func cloneClaims(source map[string]any, key string, value any) map[string]any {
	cloned := make(map[string]any, len(source))
	for sourceKey, sourceValue := range source {
		cloned[sourceKey] = sourceValue
	}
	cloned[key] = value
	return cloned
}

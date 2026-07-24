package authn

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

var ErrInvalidCredential = errors.New("invalid credential")

type JWKSConfig struct {
	URL        string
	Issuer     string
	Audience   string
	HTTPClient *http.Client
	CacheTTL   time.Duration
	ClockSkew  time.Duration
	Now        func() time.Time
}

// JWKSAuthenticator is the Better Auth authentication adapter. Its interface
// returns identity only; Access resolves current product authority separately.
type JWKSAuthenticator struct {
	url        string
	issuer     string
	audience   string
	httpClient *http.Client
	cacheTTL   time.Duration
	clockSkew  time.Duration
	now        func() time.Time

	mu        sync.Mutex
	keys      map[string]ed25519.PublicKey
	fetchedAt time.Time
}

func NewJWKSAuthenticator(config JWKSConfig) (*JWKSAuthenticator, error) {
	parsedURL, err := url.Parse(config.URL)
	if err != nil || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid JWKS URL")
	}
	if parsedURL.Scheme != "https" &&
		!(parsedURL.Scheme == "http" &&
			(parsedURL.Hostname() == "localhost" || parsedURL.Hostname() == "127.0.0.1")) {
		return nil, fmt.Errorf("JWKS URL must use HTTPS outside localhost")
	}
	if strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.Audience) == "" {
		return nil, fmt.Errorf("JWT issuer and audience are required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 3 * time.Second}
	}
	if config.CacheTTL <= 0 {
		config.CacheTTL = 5 * time.Minute
	}
	if config.ClockSkew <= 0 {
		config.ClockSkew = 30 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &JWKSAuthenticator{
		url:        config.URL,
		issuer:     config.Issuer,
		audience:   config.Audience,
		httpClient: config.HTTPClient,
		cacheTTL:   config.CacheTTL,
		clockSkew:  config.ClockSkew,
		now:        config.Now,
		keys:       map[string]ed25519.PublicKey{},
	}, nil
}

func (adapter *JWKSAuthenticator) Authenticate(
	ctx context.Context,
	token string,
) (access.Identity, error) {
	if len(token) == 0 || len(token) > 16*1024 {
		return access.Identity{}, invalid("token size")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return access.Identity{}, invalid("token structure")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return access.Identity{}, invalid("header encoding")
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil ||
		header.Algorithm != "EdDSA" ||
		header.KeyID == "" {
		return access.Identity{}, invalid("header")
	}

	key, err := adapter.key(ctx, header.KeyID, false)
	if err != nil {
		return access.Identity{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize {
		return access.Identity{}, invalid("signature encoding")
	}
	if !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), signature) {
		return access.Identity{}, invalid("signature")
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return access.Identity{}, invalid("claims encoding")
	}
	var claims tokenClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return access.Identity{}, invalid("claims")
	}
	now := adapter.now()
	if claims.Issuer != adapter.issuer ||
		!claims.Audience.Includes(adapter.audience) ||
		strings.TrimSpace(claims.Subject) == "" ||
		strings.TrimSpace(claims.Email) == "" ||
		!claims.EmailVerified ||
		claims.ExpiresAt == 0 ||
		!now.Before(time.Unix(claims.ExpiresAt, 0).Add(adapter.clockSkew)) {
		return access.Identity{}, invalid("claims")
	}
	if claims.NotBefore != nil &&
		now.Add(adapter.clockSkew).Before(time.Unix(*claims.NotBefore, 0)) {
		return access.Identity{}, invalid("not before")
	}
	return access.Identity{
		Subject:       claims.Subject,
		Email:         strings.ToLower(strings.TrimSpace(claims.Email)),
		EmailVerified: true,
	}, nil
}

func (adapter *JWKSAuthenticator) key(
	ctx context.Context,
	keyID string,
	forceRefresh bool,
) (ed25519.PublicKey, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	now := adapter.now()
	if forceRefresh || len(adapter.keys) == 0 || now.Sub(adapter.fetchedAt) >= adapter.cacheTTL {
		if err := adapter.refresh(ctx); err != nil {
			return nil, err
		}
	}
	if key, ok := adapter.keys[keyID]; ok {
		return key, nil
	}
	if !forceRefresh {
		if err := adapter.refresh(ctx); err != nil {
			return nil, err
		}
		if key, ok := adapter.keys[keyID]; ok {
			return key, nil
		}
	}
	return nil, invalid("unknown key")
}

func (adapter *JWKSAuthenticator) refresh(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, adapter.url, nil)
	if err != nil {
		return invalid("JWKS request")
	}
	response, err := adapter.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("%w: JWKS unavailable", ErrInvalidCredential)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: JWKS status", ErrInvalidCredential)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: JWKS response", ErrInvalidCredential)
	}
	var document struct {
		Keys []struct {
			KeyType   string `json:"kty"`
			Curve     string `json:"crv"`
			Algorithm string `json:"alg"`
			Use       string `json:"use"`
			KeyID     string `json:"kid"`
			X         string `json:"x"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("%w: JWKS document", ErrInvalidCredential)
	}
	keys := make(map[string]ed25519.PublicKey, len(document.Keys))
	for _, key := range document.Keys {
		if key.KeyType != "OKP" ||
			key.Curve != "Ed25519" ||
			(key.Algorithm != "" && key.Algorithm != "EdDSA") ||
			(key.Use != "" && key.Use != "sig") ||
			key.KeyID == "" {
			continue
		}
		publicKey, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			continue
		}
		keys[key.KeyID] = ed25519.PublicKey(publicKey)
	}
	if len(keys) == 0 {
		return fmt.Errorf("%w: JWKS has no supported signing keys", ErrInvalidCredential)
	}
	adapter.keys = keys
	adapter.fetchedAt = adapter.now()
	return nil
}

func invalid(_ string) error {
	return ErrInvalidCredential
}

type tokenClaims struct {
	Issuer        string   `json:"iss"`
	Audience      audience `json:"aud"`
	Subject       string   `json:"sub"`
	Email         string   `json:"email"`
	EmailVerified bool     `json:"email_verified"`
	ExpiresAt     int64    `json:"exp"`
	NotBefore     *int64   `json:"nbf,omitempty"`
}

type audience []string

func (value *audience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*value = audience{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*value = audience(many)
	return nil
}

func (value audience) Includes(expected string) bool {
	for _, candidate := range value {
		if candidate == expected {
			return true
		}
	}
	return false
}

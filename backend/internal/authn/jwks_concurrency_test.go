package authn_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/authn"
)

func TestJWKSRefreshDoesNotBlockCachedCredentialsOrCanceledWaiters(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 2 {
			close(started)
			<-release
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []jwkFixture{{Kid: "known", PublicKey: public}}})
	}))
	defer server.Close()
	defer unblock()
	adapter := probeAuthenticator(t, server, time.Now)
	valid := probeToken(t, "known", private)
	unknown := probeToken(t, "unknown", private)
	if _, err := adapter.Authenticate(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	refreshDone := make(chan error, 1)
	go func() { _, err := adapter.Authenticate(context.Background(), unknown); refreshDone <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("unknown signing key did not start a refresh")
	}
	cachedDone := make(chan error, 1)
	go func() { _, err := adapter.Authenticate(context.Background(), valid); cachedDone <- err }()
	select {
	case err := <-cachedDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cached credential waited for unrelated network refresh")
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() { _, err := adapter.Authenticate(ctx, unknown); waiterDone <- err }()
	cancel()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter remained blocked on refresh")
	}
	unblock()
	if err := <-refreshDone; !errors.Is(err, authn.ErrInvalidCredential) {
		t.Fatalf("unknown credential error = %v", err)
	}
}

func TestJWKSUnknownKeyBurstsAreBoundedAndRotationRecovers(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var rotated atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		keys := []jwkFixture{{Kid: "known", PublicKey: public}}
		if rotated.Load() {
			keys = append(keys, jwkFixture{Kid: "rotated", PublicKey: public})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	defer server.Close()
	now := time.Now()
	adapter := probeAuthenticator(t, server, func() time.Time { return now })
	if _, err := adapter.Authenticate(context.Background(), probeToken(t, "known", private)); err != nil {
		t.Fatal(err)
	}
	unknown := probeToken(t, "unknown", private)
	var waiters sync.WaitGroup
	for range 20 {
		waiters.Go(func() {
			if _, err := adapter.Authenticate(context.Background(), unknown); !errors.Is(err, authn.ErrInvalidCredential) {
				t.Errorf("unknown credential error = %v", err)
			}
		})
	}
	waiters.Wait()
	if got := requests.Load(); got != 2 {
		t.Fatalf("signing-key fetches = %d, want one initial fetch and one shared refresh", got)
	}
	rotated.Store(true)
	now = now.Add(6 * time.Second)
	if _, err := adapter.Authenticate(context.Background(), probeToken(t, "rotated", private)); err != nil {
		t.Fatalf("rotated credential after bounded retry interval: %v", err)
	}
}

func TestJWKSCanceledRefreshInitiatorDoesNotCancelOtherCredentials(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []jwkFixture{{Kid: "known", PublicKey: public}}})
	}))
	defer server.Close()
	defer unblock()
	adapter := probeAuthenticator(t, server, time.Now)
	valid := probeToken(t, "known", private)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := adapter.Authenticate(ctx, valid); first <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("initial signing-key fetch did not start")
	}
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("initiator error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled initiator waited for network")
	}
	second := make(chan error, 1)
	go func() { _, err := adapter.Authenticate(context.Background(), valid); second <- err }()
	unblock()
	if err := <-second; err != nil {
		t.Fatalf("independent credential failed after initiator canceled: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("shared initial signing-key fetches = %d, want 1", requests.Load())
	}
}

func TestJWKSExpiredKeysFailClosedDuringOutageAndRecover(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var unavailable atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []jwkFixture{{Kid: "known", PublicKey: public}}})
	}))
	defer server.Close()
	now := time.Now()
	adapter := probeAuthenticator(t, server, func() time.Time { return now })
	valid := probeToken(t, "known", private)
	if _, err := adapter.Authenticate(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	unavailable.Store(true)
	for range 5 {
		if _, err := adapter.Authenticate(context.Background(), valid); !errors.Is(err, authn.ErrInvalidCredential) {
			t.Fatalf("expired signing key during outage error = %v", err)
		}
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("signing-key fetches during outage = %d, want 2", got)
	}
	now = now.Add(6 * time.Second)
	unavailable.Store(false)
	if _, err := adapter.Authenticate(context.Background(), valid); err != nil {
		t.Fatalf("credential after provider recovery: %v", err)
	}
}

func probeAuthenticator(t *testing.T, server *httptest.Server, now func() time.Time) *authn.JWKSAuthenticator {
	t.Helper()
	adapter, err := authn.NewJWKSAuthenticator(authn.JWKSConfig{
		URL: server.URL, Issuer: "https://auth.acuity.test", Audience: "https://api.acuity.test",
		HTTPClient: server.Client(), CacheTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func probeToken(t *testing.T, keyID string, private ed25519.PrivateKey) string {
	t.Helper()
	return signedJWT(t, keyID, private, map[string]any{
		"iss": "https://auth.acuity.test", "aud": "https://api.acuity.test", "sub": "synthetic-user",
		"email": "staff@example.test", "email_verified": true, "exp": time.Now().Add(time.Hour).Unix(),
	})
}

package humancalling_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProviderIngressHTTPBurstMeetsCurrentTrafficDeadline(t *testing.T) {
	controlPool := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ingressPool := openLoadPool(t, 1)
	portalPool := openLoadPool(t, 1)
	realtimePool := openLoadPool(t, 1)
	workerPool := openLoadPool(t, 1)
	now := time.Date(2026, time.July, 29, 20, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate provider-ingress key: %v", err)
	}
	config := humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		WebhookPublicKey: publicKey,
		WebhookTolerance: 5 * time.Minute,
	}
	clock := func() time.Time { return now }
	portalAccess := access.New(portalPool, clock)
	realtimeAccess := access.New(realtimePool, clock)
	portal := humancalling.New(portalPool, portalAccess, nil, config, clock)
	ingress := humancalling.New(ingressPool, nil, nil, config, clock)
	worker := humancalling.New(workerPool, nil, nil, config, clock)
	authorization, identities := provisionConcurrentStaff(
		t,
		portalAccess,
		now,
		"http-ingress",
		1,
	)

	const uniqueReceipts = 12
	deliveries := make([][]byte, 0, 25)
	for index := range uniqueReceipts {
		key := fmt.Sprintf("http-ingress-%02d", index+1)
		handoff := createLoadHandoff(t, portal, authorization, key)
		raw := loadWebhook(
			now.Add(time.Duration(index)*time.Microsecond),
			"call.initiated",
			key+"-initiated",
			key,
			handoff.SIPDestination,
		)
		deliveries = append(deliveries, raw, raw)
	}
	deliveries = append(deliveries, deliveries[0])

	handler, err := httpapi.NewProviderIngress(httpapi.Config{
		AcquireTimeout: 1500 * time.Millisecond,
	}, ingressPool, ingress)
	if err != nil {
		t.Fatalf("new provider-ingress HTTP handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 1500 * time.Millisecond

	receiptLock, err := controlPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin provider-receipt lock: %v", err)
	}
	defer func() { _ = receiptLock.Rollback(ctx) }()
	if _, err := receiptLock.Exec(ctx, `
		LOCK TABLE human_calling_provider_receipts IN ACCESS EXCLUSIVE MODE
	`); err != nil {
		t.Fatalf("lock provider receipts: %v", err)
	}

	type deliveryResult struct {
		status   int
		duration time.Duration
		err      error
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	start := make(chan struct{})
	results := make(chan deliveryResult, len(deliveries))
	var ready sync.WaitGroup
	ready.Add(len(deliveries))
	for _, raw := range deliveries {
		go func() {
			signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
				privateKey,
				append([]byte(timestamp+"|"), raw...),
			))
			request, err := http.NewRequestWithContext(
				ctx,
				http.MethodPost,
				server.URL+"/v1/provider/telnyx/webhooks",
				bytes.NewReader(raw),
			)
			if err == nil {
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("telnyx-timestamp", timestamp)
				request.Header.Set("telnyx-signature-ed25519", signature)
			}
			ready.Done()
			<-start
			startedAt := time.Now()
			if err != nil {
				results <- deliveryResult{duration: time.Since(startedAt), err: err}
				return
			}
			response, err := client.Do(request)
			if err != nil {
				results <- deliveryResult{duration: time.Since(startedAt), err: err}
				return
			}
			_ = response.Body.Close()
			results <- deliveryResult{
				status: response.StatusCode, duration: time.Since(startedAt),
			}
		}()
	}
	ready.Wait()
	close(start)
	awaitBlockedIngressWrites(t, controlPool, ingressPool, 1)
	responsiveContext, cancelResponsive := context.WithTimeout(ctx, 250*time.Millisecond)
	offers, err := portal.ListOffers(responsiveContext, identities[0])
	if err != nil || len(offers) != 0 {
		t.Fatalf("portal-api path under ingress pressure: offers=%#v error=%v", offers, err)
	}
	if _, err := realtimeAccess.ResolveActor(
		responsiveContext,
		identities[0],
		authorization.Practice.ID,
		authorization.Locations[0].ID,
	); err != nil {
		t.Fatalf("realtime authorization path under ingress pressure: %v", err)
	}
	cancelResponsive()
	assertPoolResponsive(t, workerPool, "worker")
	if err := receiptLock.Commit(ctx); err != nil {
		t.Fatalf("release provider-receipt lock: %v", err)
	}

	durations := make([]time.Duration, 0, len(deliveries))
	for range deliveries {
		result := <-results
		if result.err != nil || result.status != http.StatusNoContent {
			t.Fatalf(
				"provider-ingress response status=%d duration=%s error=%v",
				result.status,
				result.duration,
				result.err,
			)
		}
		durations = append(durations, result.duration)
	}
	p95 := loadPercentile(durations, 95)
	p99 := loadPercentile(durations, 99)
	if p99 >= time.Second {
		t.Fatalf("provider-ingress HTTP p99 = %s, want under 1s", p99)
	}

	var receipts, duplicates, calls int
	if err := workerPool.QueryRow(ctx, `
		SELECT
			count(*),
			COALESCE(sum(duplicate_count), 0),
			(SELECT count(*) FROM human_calling_calls
			 WHERE call_session_id LIKE 'load-http-ingress-%-session')
		FROM human_calling_provider_receipts
		WHERE event_id LIKE 'http-ingress-%-initiated'
	`).Scan(&receipts, &duplicates, &calls); err != nil {
		t.Fatalf("read acknowledged provider receipts: %v", err)
	}
	if receipts != uniqueReceipts || duplicates != 13 || calls != 0 {
		t.Fatalf(
			"acknowledged state receipts=%d duplicates=%d calls=%d; want 12,13,0",
			receipts,
			duplicates,
			calls,
		)
	}
	for range uniqueReceipts {
		processed, err := worker.ProcessNextReceipt(ctx)
		if err != nil || !processed {
			t.Fatalf("project acknowledged receipt: processed=%t err=%v", processed, err)
		}
	}
	var applied int
	if err := workerPool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE state = 'APPLIED'),
			(SELECT count(*) FROM human_calling_calls
			 WHERE call_session_id LIKE 'load-http-ingress-%-session')
		FROM human_calling_provider_receipts
		WHERE event_id LIKE 'http-ingress-%-initiated'
	`).Scan(&applied, &calls); err != nil {
		t.Fatalf("read projected provider receipts: %v", err)
	}
	if applied != uniqueReceipts || calls != uniqueReceipts {
		t.Fatalf(
			"projected state applied=%d calls=%d; want %d,%d",
			applied,
			calls,
			uniqueReceipts,
			uniqueReceipts,
		)
	}
	t.Logf(
		"deterministic local HTTP proof only (not Cloud Run/Cloud SQL acceptance): requests=%d ack_p95=%s ack_p99_or_max=%s",
		len(deliveries),
		p95,
		p99,
	)
}

func awaitBlockedIngressWrites(
	t *testing.T,
	controlPool *pgxpool.Pool,
	ingressPool *pgxpool.Pool,
	expected int,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		var blocked int
		if err := controlPool.QueryRow(context.Background(), `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
				AND pid <> pg_backend_pid()
				AND state = 'active'
				AND wait_event_type = 'Lock'
				AND query LIKE '%INSERT INTO human_calling_provider_receipts%'
		`).Scan(&blocked); err != nil {
			t.Fatalf("inspect blocked provider-ingress writes: %v", err)
		}
		if blocked == expected &&
			ingressPool.Stat().AcquiredConns() == int32(expected) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"blocked ingress writes=%d acquired=%d; want %d and %d",
				blocked,
				ingressPool.Stat().AcquiredConns(),
				expected,
				expected,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertPoolResponsive(t *testing.T, pool *pgxpool.Pool, role string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("%s pool was not responsive during ingress saturation: %v", role, err)
	}
}

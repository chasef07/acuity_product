package humancalling_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestWebhookBurstConvergesThroughTwoDatabaseConnections(t *testing.T) {
	testdb.Open(t)
	const receiptCount = 64

	pool := openLoadPool(t, 2)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate webhook key: %v", err)
	}
	now := time.Date(2026, time.July, 29, 18, 0, 0, 0, time.UTC)
	calling := humancalling.New(pool, nil, nil, humancalling.Config{
		WebhookPublicKey: publicKey,
		WebhookTolerance: 5 * time.Minute,
	}, func() time.Time { return now })
	timestamp := strconv.FormatInt(now.Unix(), 10)

	deliveries := make([][]byte, 0, receiptCount*2)
	for index := range receiptCount {
		raw := []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"call.future","id":"burst-%03d","occurred_at":"%s","payload":{}}}`,
			index,
			now.Format(time.RFC3339Nano),
		))
		deliveries = append(deliveries, raw, raw)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	collectLoadBurst(
		t,
		startLoadBurst(ctx, calling, privateKey, timestamp, deliveries).results,
		len(deliveries),
	)

	var durableReceipts, duplicates int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(duplicate_count), 0)
		FROM human_calling_provider_receipts
	`).Scan(&durableReceipts, &duplicates); err != nil {
		t.Fatalf("read durable webhook burst: %v", err)
	}
	if durableReceipts != receiptCount || duplicates != receiptCount {
		t.Fatalf(
			"durable webhook burst = %d receipts, %d duplicates; want %d and %d",
			durableReceipts,
			duplicates,
			receiptCount,
			receiptCount,
		)
	}

	var projected atomic.Int64
	var workers sync.WaitGroup
	workerErrors := make(chan error, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for projected.Load() < receiptCount && ctx.Err() == nil {
				processed, err := calling.ProcessNextReceipt(ctx)
				if err != nil {
					workerErrors <- err
					return
				}
				if processed {
					projected.Add(1)
					continue
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}
	workers.Wait()
	close(workerErrors)
	for err := range workerErrors {
		t.Fatalf("project concurrent webhook: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("project webhook burst: %v", ctx.Err())
	}

	var unknownReceipts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM human_calling_provider_receipts
		WHERE state = 'UNKNOWN'
	`).Scan(&unknownReceipts); err != nil {
		t.Fatalf("read projected webhook burst: %v", err)
	}
	if unknownReceipts != receiptCount {
		t.Fatalf("projected webhook burst = %d, want %d", unknownReceipts, receiptCount)
	}
	if total := pool.Stat().TotalConns(); total > 2 {
		t.Fatalf("database pool opened %d connections, want at most 2", total)
	}
}

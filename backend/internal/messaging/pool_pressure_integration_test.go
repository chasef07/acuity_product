package messaging_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInboundAttachmentSurvivesLostCommitAcknowledgement(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	content := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("synthetic"), 16)...)
	media := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(content)
	}))
	t.Cleanup(media.Close)
	lostAcknowledgement := errors.New("synthetic lost commit acknowledgement")
	database := &commitAcknowledgementDatabase{Pool: fixture.pool, failure: lostAcknowledgement}
	store := &commitAcknowledgementStore{
		AttachmentObjectStore: messaging.NewMemoryAttachmentStore(),
		afterPut:              func() { database.loseNextAcknowledgement.Store(true) },
	}
	accessModule := access.New(database, func() time.Time { return fixture.now })
	module := messaging.New(database, accessModule,
		work.New(database, accessModule, func() time.Time { return fixture.now }),
		fixture.provider, messaging.Config{
			AttachmentStore:   store,
			HTTPClient:        media.Client(),
			WebhookPublicKeys: [][]byte{fixture.privateKey.Public().(ed25519.PublicKey)},
		}, func() time.Time { return fixture.now },
	)
	attachmentID := enqueueSyntheticInboundAttachment(t, fixture, module, media.URL)
	processed, err := module.ProcessNextAttachment(context.Background())
	if !processed || !errors.Is(err, lostAcknowledgement) {
		t.Fatalf("lost attachment commit acknowledgement = %t, %v", processed, err)
	}
	opened, err := module.OpenAttachment(context.Background(), fixture.identity, attachmentID)
	if err != nil || opened.Attachment.State != messaging.AttachmentStored || !bytes.Equal(opened.Content, content) {
		t.Fatalf("attachment after committed result lost its acknowledgement: state=%q, matching bytes=%t, err=%v",
			opened.Attachment.State, bytes.Equal(opened.Content, content), err)
	}
	if processed, err := module.ProcessNextAttachment(context.Background()); err != nil || processed {
		t.Fatalf("already stored attachment selected for another copy = %t, %v", processed, err)
	}
}

func TestStaleInboundAttachmentFailurePreservesNewerCopy(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	content := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("synthetic"), 16)...)
	media := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	}))
	t.Cleanup(media.Close)
	clock := func() time.Time { return fixture.now }
	accessModule := access.New(fixture.pool, clock)
	store := messaging.NewMemoryAttachmentStore()
	config := messaging.Config{
		AttachmentStore: store, HTTPClient: media.Client(),
		WebhookPublicKeys: [][]byte{fixture.privateKey.Public().(ed25519.PublicKey)},
	}
	module := messaging.New(fixture.pool, accessModule, work.New(fixture.pool, accessModule, clock),
		fixture.provider, config, clock)
	attachmentID := enqueueSyntheticInboundAttachment(t, fixture, module, media.URL)
	failure := errors.New("synthetic attachment result connection unavailable")
	database := &blockedAttachmentResultDatabase{
		Pool: fixture.pool, started: make(chan struct{}), release: make(chan struct{}), failure: failure,
	}
	t.Cleanup(database.unblock)
	staleModule := messaging.New(database, accessModule, work.New(database, accessModule, clock),
		fixture.provider, config, clock)
	staleResult := make(chan error, 1)
	go func() {
		_, err := staleModule.ProcessNextAttachment(context.Background())
		staleResult <- err
	}()
	select {
	case <-database.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first copy did not reach result finalization")
	}
	// The first attempt has written its bytes, but its claim can now be
	// reclaimed while the result connection remains unavailable.
	fixture.now = fixture.now.Add(time.Minute)
	if processed, err := module.ProcessNextAttachment(context.Background()); err != nil || !processed {
		t.Fatalf("reclaimed attachment copy = %t, %v", processed, err)
	}
	database.unblock()
	select {
	case err := <-staleResult:
		if !errors.Is(err, failure) {
			t.Fatalf("stale copy finalization error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale copy did not finish")
	}
	opened, err := module.OpenAttachment(context.Background(), fixture.identity, attachmentID)
	if err != nil || opened.Attachment.State != messaging.AttachmentStored || !bytes.Equal(opened.Content, content) {
		t.Fatalf("stale copy failure removed newer committed attachment: state=%q, matching bytes=%t, err=%v",
			opened.Attachment.State, bytes.Equal(opened.Content, content), err)
	}
}

func enqueueSyntheticInboundAttachment(t *testing.T, fixture automaticAcknowledgementTestFixture, module *messaging.Module, mediaURL string) string {
	t.Helper()
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"synthetic-commit-ack-event","occurred_at":"%s","payload":{"id":"synthetic-commit-ack-message","from":{"phone_number":"+17275550199"},"to":[{"phone_number":"+17275550100"}],"text":"Synthetic attachment","media":[{"url":%q,"content_type":"application/pdf"}]}}}`,
		fixture.now.Format(time.RFC3339), mediaURL,
	))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.privateKey,
		append([]byte(timestamp+"|"), raw...)))
	if _, err := module.ReceiveWebhook(context.Background(), "", raw, timestamp, signature); err != nil {
		t.Fatalf("receive synthetic inbound attachment: %v", err)
	}
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("project synthetic inbound attachment = %t, %v", processed, err)
	}
	reads := workspace.New(fixture.pool, access.New(fixture.pool, func() time.Time { return fixture.now }))
	timeline, err := reads.QueryPhoneTimeline(context.Background(), workspace.QueryPhoneTimelineCommand{
		Identity: fixture.identity, PracticeID: fixture.practiceID, Phone: "+17275550199",
	})
	if err != nil {
		t.Fatalf("read synthetic inbound attachment timeline: %v", err)
	}
	attachmentID := ""
	for _, item := range timeline.Items {
		if item.Type == "MESSAGE" && item.Message.Attachment != nil {
			attachmentID = item.Message.Attachment.ID
		}
	}
	if attachmentID == "" {
		t.Fatal("inbound receipt did not produce an attachment")
	}
	return attachmentID
}

func TestInterruptedMessageRecoveryAdvancesOneCommandPerPass(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	messageIDs := make([]string, 0, 3)
	for index := range 3 {
		message, _, err := fixture.module.Send(context.Background(), messaging.SendCommand{
			Identity: fixture.identity, PracticeID: fixture.practiceID,
			LocationID: fixture.locationID, Destination: "+17275550199",
			Body:           "Synthetic interrupted Message",
			IdempotencyKey: fmt.Sprintf("interrupted-message-%d", index),
		})
		if err != nil {
			t.Fatalf("create interrupted Message %d: %v", index, err)
		}
		if _, err := fixture.pool.Exec(context.Background(), `
			UPDATE messaging_provider_commands
			SET state = 'WRITING', write_started_at = $2
			WHERE message_id = $1
		`, message.ID, fixture.now.Add(time.Duration(index-3)*time.Minute)); err != nil {
			t.Fatalf("capture interrupted provider write: %v", err)
		}
		messageIDs = append(messageIDs, message.ID)
	}
	for pass := range 3 {
		if err := fixture.module.RecoverInterruptedCommands(context.Background()); err != nil {
			t.Fatalf("recover interrupted Message pass %d: %v", pass, err)
		}
		for index, messageID := range messageIDs {
			message, err := fixture.module.ReadMessage(context.Background(), fixture.identity, messageID)
			want := messaging.DeliverySending
			if index <= pass {
				want = messaging.DeliveryUnknown
			}
			if err != nil || message.Delivery != want {
				t.Fatalf("Message %d after recovery pass %d = %q, %v; want %q",
					index, pass, message.Delivery, err, want)
			}
		}
	}
	if err := fixture.module.RecoverInterruptedCommands(context.Background()); err != nil {
		t.Fatalf("recover converged interrupted Message set: %v", err)
	}
	processed, err := fixture.module.ProcessNextCommand(context.Background())
	if err != nil || processed || len(fixture.provider.commands) != 0 {
		t.Fatalf("recovered uncertain writes were selected again: processed=%t, sends=%d, err=%v",
			processed, len(fixture.provider.commands), err)
	}
}

func TestAttachmentReadDoesNotBlockUnrelatedMessageRead(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse single-connection Messaging pool: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open single-connection Messaging pool: %v", err)
	}
	t.Cleanup(pool.Close)
	store := &blockedAttachmentReadStore{
		AttachmentObjectStore: messaging.NewMemoryAttachmentStore(),
		started:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	t.Cleanup(store.unblock)
	accessModule := access.New(pool, func() time.Time { return fixture.now })
	module := messaging.New(pool, accessModule,
		work.New(pool, accessModule, func() time.Time { return fixture.now }),
		fixture.provider, messaging.Config{AttachmentStore: store},
		func() time.Time { return fixture.now },
	)
	content := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		bytes.Repeat([]byte{0}, 64)...)
	attachment, err := module.UploadAttachment(context.Background(), messaging.UploadAttachmentCommand{
		Identity: fixture.identity, PracticeID: fixture.practiceID,
		LocationID: fixture.locationID, FileName: "synthetic.png",
		DeclaredType: "image/png", Content: content,
	})
	if err != nil {
		t.Fatalf("upload synthetic attachment: %v", err)
	}
	message, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity: fixture.identity, PracticeID: fixture.practiceID,
		LocationID: fixture.locationID, Destination: "+17275550199",
		AttachmentID: attachment.ID, IdempotencyKey: "attachment-read-pool-release",
	})
	if err != nil {
		t.Fatalf("attach synthetic content to Message: %v", err)
	}
	type attachmentResult struct {
		content messaging.AttachmentContent
		err     error
	}
	opened := make(chan attachmentResult, 1)
	go func() {
		content, err := module.OpenAttachment(context.Background(), fixture.identity, attachment.ID)
		opened <- attachmentResult{content: content, err: err}
	}()
	select {
	case <-store.started:
	case <-time.After(5 * time.Second):
		t.Fatal("attachment read did not reach the object store")
	}
	readContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	read, err := module.ReadMessage(readContext, fixture.identity, message.ID)
	if err != nil || read.ID != message.ID {
		t.Fatalf("unrelated Message read while attachment bytes wait = %q, %v", read.ID, err)
	}
	store.unblock()
	select {
	case result := <-opened:
		if result.err != nil || !bytes.Equal(result.content.Content, content) {
			t.Fatalf("authorized attachment read = %v, matching bytes=%t",
				result.err, bytes.Equal(result.content.Content, content))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attachment read did not finish after object storage recovered")
	}
	denied := fixture.identity
	denied.Subject = "unrelated-synthetic-staff"
	if _, err := module.OpenAttachment(context.Background(), denied, attachment.ID); !errors.Is(err, messaging.ErrDenied) {
		t.Fatalf("unrelated staff attachment read error = %v, want access denied", err)
	}
}

func TestAttachmentMetadataTimeoutDoesNotBecomeAccessDenial(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	database, err := productpostgres.NewExecutor(fixture.pool, productpostgres.ExecutorConfig{
		AcquireTimeout: time.Second, OperationTimeout: time.Second, StatementTimeout: 80 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return fixture.now }
	accessModule := access.New(database, clock)
	module := messaging.New(database, accessModule, work.New(database, accessModule, clock),
		fixture.provider, messaging.Config{
			AttachmentStore:    messaging.NewMemoryAttachmentStore(),
			MediaPublicBaseURL: "https://synthetic.invalid", MediaSigningKey: bytes.Repeat([]byte{1}, 32),
		}, clock)
	content := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("synthetic"), 16)...)
	attachment, err := module.UploadAttachment(context.Background(), messaging.UploadAttachmentCommand{
		Identity: fixture.identity, PracticeID: fixture.practiceID, LocationID: fixture.locationID,
		FileName: "synthetic.pdf", DeclaredType: "application/pdf", Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity: fixture.identity, PracticeID: fixture.practiceID, LocationID: fixture.locationID,
		Destination: "+17275550199", AttachmentID: attachment.ID, IdempotencyKey: "metadata-timeout",
	}); err != nil {
		t.Fatal(err)
	}
	mediaURL, err := module.ProviderMediaURL(attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(mediaURL)
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]func() (messaging.AttachmentContent, error){
		"staff": func() (messaging.AttachmentContent, error) {
			return module.OpenAttachment(context.Background(), fixture.identity, attachment.ID)
		},
		"provider": func() (messaging.AttachmentContent, error) {
			return module.OpenProviderAttachment(context.Background(), attachment.ID, parsed.Query().Get("expires"), parsed.Query().Get("signature"))
		},
	}
	blocker, err := fixture.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Rollback(context.Background()) })
	if _, err := blocker.Exec(context.Background(), `LOCK TABLE messaging_attachments IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	for name, read := range reads {
		t.Run(name+" metadata unavailable", func(t *testing.T) {
			if _, err := read(); productpostgres.CauseOf(err) != productpostgres.CauseStatementTimeout {
				t.Fatalf("attachment metadata timeout was replaced: %v", err)
			}
		})
	}
	if err := blocker.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, read := range reads {
		t.Run(name+" metadata recovered", func(t *testing.T) {
			opened, err := read()
			if err != nil || !bytes.Equal(opened.Content, content) {
				t.Fatalf("recovered attachment read = %v, matching bytes=%t", err, bytes.Equal(opened.Content, content))
			}
		})
	}
}

type blockedAttachmentReadStore struct {
	messaging.AttachmentObjectStore
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (store *blockedAttachmentReadStore) Get(ctx context.Context, key string) ([]byte, error) {
	close(store.started)
	select {
	case <-store.release:
		return store.AttachmentObjectStore.Get(ctx, key)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (store *blockedAttachmentReadStore) unblock() {
	store.once.Do(func() { close(store.release) })
}

type commitAcknowledgementDatabase struct {
	*pgxpool.Pool
	loseNextAcknowledgement atomic.Bool
	failure                 error
}

type blockedAttachmentResultDatabase struct {
	*pgxpool.Pool
	transactions atomic.Int32
	started      chan struct{}
	release      chan struct{}
	once         sync.Once
	failure      error
}

func (database *blockedAttachmentResultDatabase) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if database.transactions.Add(1) == 2 {
		close(database.started)
		select {
		case <-database.release:
			return nil, database.failure
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return database.Pool.BeginTx(ctx, options)
}

func (database *blockedAttachmentResultDatabase) unblock() {
	database.once.Do(func() { close(database.release) })
}

func (database *commitAcknowledgementDatabase) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := database.Pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &commitAcknowledgementTransaction{Tx: tx, database: database}, nil
}

type commitAcknowledgementTransaction struct {
	pgx.Tx
	database *commitAcknowledgementDatabase
}

func (tx *commitAcknowledgementTransaction) Commit(ctx context.Context) error {
	if err := tx.Tx.Commit(ctx); err != nil {
		return err
	}
	if tx.database.loseNextAcknowledgement.Swap(false) {
		return tx.database.failure
	}
	return nil
}

type commitAcknowledgementStore struct {
	messaging.AttachmentObjectStore
	afterPut func()
}

func (store *commitAcknowledgementStore) Put(ctx context.Context, key string, content []byte) error {
	if err := store.AttachmentObjectStore.Put(ctx, key, content); err != nil {
		return err
	}
	store.afterPut()
	return nil
}

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
	"os"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/jackc/pgx/v5/pgxpool"
)

type delayedAttachmentStore struct {
	*messaging.MemoryAttachmentStore
	operation string
	entered   chan string
	release   chan struct{}
	once      sync.Once
}

func (s *delayedAttachmentStore) unblock() { s.once.Do(func() { close(s.release) }) }
func (s *delayedAttachmentStore) Put(ctx context.Context, key string, value []byte) error {
	if s.operation == "put" {
		s.entered <- key
		<-s.release
		ctx = context.Background()
	}
	return s.MemoryAttachmentStore.Put(ctx, key, value)
}
func (s *delayedAttachmentStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s.operation == "get" {
		s.entered <- key
		<-s.release
	}
	return s.MemoryAttachmentStore.Get(ctx, key)
}
func (s *delayedAttachmentStore) Delete(ctx context.Context, key string) error {
	if s.operation == "delete" {
		s.entered <- key
		<-s.release
	}
	return s.MemoryAttachmentStore.Delete(ctx, key)
}
func attachmentCapacityFixture(t *testing.T) (automaticAcknowledgementTestFixture, *pgxpool.Pool, *messaging.Module, *delayedAttachmentStore, messaging.UploadAttachmentCommand) {
	t.Helper()
	f := newAutomaticAcknowledgementTestFixture(t, true)
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := &delayedAttachmentStore{MemoryAttachmentStore: messaging.NewMemoryAttachmentStore(), entered: make(chan string, 1), release: make(chan struct{})}
	t.Cleanup(s.unblock)
	a := access.New(pool, func() time.Time { return *f.clock })
	m := messaging.New(pool, a, nil, f.provider, messaging.Config{AttachmentStore: s}, func() time.Time { return *f.clock })
	input := messaging.UploadAttachmentCommand{Identity: f.identity, PracticeID: f.practiceID, LocationID: f.locationID, FileName: "synthetic.pdf", DeclaredType: "application/pdf", Content: append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("synthetic"), 16)...)}
	return f, pool, m, s, input
}
func awaitStorage(t *testing.T, s *delayedAttachmentStore) string {
	t.Helper()
	select {
	case key := <-s.entered:
		return key
	case <-time.After(2 * time.Second):
		t.Fatal("storage operation did not start")
		return ""
	}
}
func requireDatabaseAvailable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("storage retained the only database connection: %v", err)
	}
}
func TestAttachmentUploadReleasesConnectionAndRechecksRevocation(t *testing.T) {
	f, pool, m, s, input := attachmentCapacityFixture(t)
	s.operation = "put"
	done := make(chan error, 1)
	go func() { _, err := m.UploadAttachment(context.Background(), input); done <- err }()
	key := awaitStorage(t, s)
	requireDatabaseAvailable(t, pool)
	if _, err := f.pool.Exec(context.Background(), `UPDATE messaging_attachments SET state='PENDING' WHERE object_key=$1`, key); err == nil {
		t.Fatal("legacy finalization bypassed token ownership")
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE access_memberships SET revoked_at=$2 WHERE user_subject=$1`, f.identity.Subject, *f.clock); err != nil {
		t.Fatal(err)
	}
	s.unblock()
	if err := <-done; !errors.Is(err, messaging.ErrDenied) {
		t.Fatalf("revoked upload error=%v", err)
	}
	*f.clock = f.clock.Add(31 * time.Second)
	if err := m.ExpirePendingAttachments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MemoryAttachmentStore.Get(context.Background(), key); !errors.Is(err, messaging.ErrObjectNotFound) {
		t.Fatalf("uncommitted upload bytes remained: %v", err)
	}
}
func TestAttachmentLateWriteRemainsSweepableAfterItsFirstCleanup(t *testing.T) {
	f, pool, m, s, input := attachmentCapacityFixture(t)
	s.operation = "put"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.UploadAttachment(ctx, input); done <- err }()
	key := awaitStorage(t, s)
	cancel()
	*f.clock = f.clock.Add(31 * time.Second)
	if err := m.ExpirePendingAttachments(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireDatabaseAvailable(t, pool)
	var retained bool
	if err := f.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM messaging_attachment_cleanup WHERE object_key=$1)`, key).Scan(&retained); err != nil || !retained {
		t.Fatalf("uncertain write lost cleanup intent: %t %v", retained, err)
	}
	s.unblock()
	if err := <-done; err == nil {
		t.Fatal("cancelled late upload reported success")
	}
	if _, err := s.MemoryAttachmentStore.Get(context.Background(), key); err != nil {
		t.Fatalf("fixture did not publish its late bytes: %v", err)
	}
	*f.clock = f.clock.Add(time.Hour + time.Second)
	if err := m.ExpirePendingAttachments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MemoryAttachmentStore.Get(context.Background(), key); !errors.Is(err, messaging.ErrObjectNotFound) {
		t.Fatalf("late object escaped cleanup: %v", err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM messaging_attachment_cleanup WHERE object_key=$1)`, key).Scan(&retained); err != nil || retained {
		t.Fatalf("finished deleted write retained intent: %t %v", retained, err)
	}
}
func TestAttachmentReadAndDeletionReleaseDatabaseCapacity(t *testing.T) {
	for _, operation := range []string{"get", "delete"} {
		t.Run(operation, func(t *testing.T) {
			f, pool, m, s, input := attachmentCapacityFixture(t)
			attachment, err := m.UploadAttachment(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "get" {
				_, _, err = m.Send(context.Background(), messaging.SendCommand{Identity: f.identity, PracticeID: f.practiceID, LocationID: f.locationID, Destination: "+15555550199", AttachmentID: attachment.ID, IdempotencyKey: "synthetic-attachment-send"})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				*f.clock = f.clock.Add(16 * time.Minute)
			}
			s.operation = operation
			done := make(chan error, 1)
			go func() {
				if operation == "get" {
					_, err := m.OpenAttachment(context.Background(), f.identity, attachment.ID)
					done <- err
				} else {
					done <- m.ExpirePendingAttachments(context.Background())
				}
			}()
			awaitStorage(t, s)
			requireDatabaseAvailable(t, pool)
			if operation == "get" {
				if _, err := f.pool.Exec(context.Background(), `UPDATE access_memberships SET revoked_at=$2 WHERE user_subject=$1`, f.identity.Subject, *f.clock); err != nil {
					t.Fatal(err)
				}
			}
			s.unblock()
			err = <-done
			if operation == "get" && !errors.Is(err, messaging.ErrDenied) {
				t.Fatalf("read after revocation error=%v", err)
			}
			if operation == "delete" && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInboundAttachmentCannotFinalizeAfterCleanupBegins(t *testing.T) {
	f, pool, _, s, input := attachmentCapacityFixture(t)
	media := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(input.Content)
	}))
	defer media.Close()
	m := messaging.New(pool, access.New(pool, func() time.Time { return *f.clock }), nil, f.provider, messaging.Config{AttachmentStore: s, HTTPClient: media.Client(), WebhookPublicKeys: [][]byte{f.privateKey.Public().(ed25519.PublicKey)}}, func() time.Time { return *f.clock })
	raw := []byte(fmt.Sprintf(`{"data":{"record_type":"event","event_type":"message.received","id":"capacity-inbound-event","occurred_at":"%s","payload":{"id":"capacity-inbound-message","from":{"phone_number":"+15555550199"},"to":[{"phone_number":"+17275550100"}],"text":"Synthetic document.","media":[{"url":%q,"content_type":"application/pdf"}]}}}`, f.now.Format(time.RFC3339), media.URL))
	timestamp := fmt.Sprint(time.Now().Unix())
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(f.privateKey, append([]byte(timestamp+"|"), raw...)))
	if _, err := m.ReceiveWebhook(context.Background(), "", raw, timestamp, signature); err != nil {
		t.Fatal(err)
	}
	if processed, err := m.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("inbound receipt: %t %v", processed, err)
	}
	s.operation = "put"
	done := make(chan error, 1)
	go func() { _, err := m.ProcessNextAttachment(context.Background()); done <- err }()
	key := awaitStorage(t, s)
	*f.clock = f.clock.Add(31 * time.Second)
	if err := m.ExpirePendingAttachments(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.unblock()
	if err := <-done; !errors.Is(err, messaging.ErrConflict) {
		t.Fatalf("late inbound finalization error=%v, want conflict", err)
	}
	var state string
	if err := f.pool.QueryRow(context.Background(), `SELECT state FROM messaging_attachments WHERE object_key=$1`, key).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "STORED" {
		t.Fatal("cleaned attempt became canonical")
	}
	// A new attempt writes a distinct object and can converge normally.
	s.operation = ""
	if processed, err := m.ProcessNextAttachment(context.Background()); err != nil || !processed {
		t.Fatalf("replacement inbound attempt: %t %v", processed, err)
	}
	var id, newKey string
	if err := f.pool.QueryRow(context.Background(), `SELECT id::text,object_key FROM messaging_attachments WHERE direction='INBOUND'`).Scan(&id, &newKey); err != nil {
		t.Fatal(err)
	}
	if newKey == key {
		t.Fatal("replacement reused an uncertain object key")
	}
	content, err := m.OpenAttachment(context.Background(), f.identity, id)
	if err != nil || !bytes.Equal(content.Content, input.Content) {
		t.Fatalf("replacement content unavailable: %v", err)
	}
	*f.clock = f.clock.Add(time.Hour + time.Second)
	if err := m.ExpirePendingAttachments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MemoryAttachmentStore.Get(context.Background(), key); !errors.Is(err, messaging.ErrObjectNotFound) {
		t.Fatalf("superseded object remained: %v", err)
	}
	if _, err := m.OpenAttachment(context.Background(), f.identity, id); err != nil {
		t.Fatalf("cleanup removed replacement: %v", err)
	}
}

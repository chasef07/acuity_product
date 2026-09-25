package messaging_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/work"
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
	m := messaging.New(pool, a, work.New(pool, a, func() time.Time { return *f.clock }), f.provider, messaging.Config{AttachmentStore: s}, func() time.Time { return *f.clock })
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

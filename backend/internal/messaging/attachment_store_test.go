package messaging_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/messaging"
)

func TestFileAttachmentStoreReadsAndDeletesPersistedLegacyKeys(t *testing.T) {
	for _, key := range []string{
		"attachments/00000000-0000-0000-0000-000000000001",
		"attachment-attempts/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002",
	} {
		t.Run(key, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, key)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			content := []byte("previously persisted attachment")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := messaging.NewFileAttachmentStore(root)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			stored, err := store.Get(ctx, key)
			if err != nil || !bytes.Equal(stored, content) {
				t.Fatalf("persisted attachment = %q, %v", stored, err)
			}
			if err := store.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(ctx, key); !errors.Is(err, messaging.ErrObjectNotFound) {
				t.Fatalf("deleted attachment error = %v", err)
			}
		})
	}
}

func TestAttachmentStoresReplayTheSameDeterministicObject(t *testing.T) {
	fileStore, err := messaging.NewFileAttachmentStore(t.TempDir())
	if err != nil {
		t.Fatalf("create file attachment store: %v", err)
	}
	stores := map[string]messaging.AttachmentObjectStore{
		"file":   fileStore,
		"memory": messaging.NewMemoryAttachmentStore(),
	}
	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			content := []byte("same immutable attachment")
			if err := store.Put(ctx, "attachments/fixed", content); err != nil {
				t.Fatalf("put attachment: %v", err)
			}
			if err := store.Put(ctx, "attachments/fixed", content); err != nil {
				t.Fatalf("replay attachment put: %v", err)
			}
			if err := store.Put(
				ctx,
				"attachments/fixed",
				[]byte("contradictory bytes"),
			); !errors.Is(err, messaging.ErrConflict) {
				t.Fatalf("contradictory attachment put error = %v", err)
			}
			stored, err := store.Get(ctx, "attachments/fixed")
			if err != nil || !bytes.Equal(stored, content) {
				t.Fatalf("stored attachment = %q, %v", stored, err)
			}
		})
	}
}

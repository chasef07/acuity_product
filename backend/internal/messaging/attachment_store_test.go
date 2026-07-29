package messaging_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/messaging"
)

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

package messaging

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestAttachmentAttemptCleanupLeavesNoPerAttachmentDirectories(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileAttachmentStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 3 {
		attachmentID := uuid.NewString()
		firstKey := attachmentObjectKey(attachmentID, uuid.NewString())
		replacementKey := attachmentObjectKey(attachmentID, uuid.NewString())
		if err := store.Put(ctx, firstKey, []byte("first attempt")); err != nil {
			t.Fatal(err)
		}
		replacement := []byte("replacement attempt")
		if err := store.Put(ctx, replacementKey, replacement); err != nil {
			t.Fatal(err)
		}
		if err := store.Delete(ctx, firstKey); err != nil {
			t.Fatal(err)
		}
		stored, err := store.Get(ctx, replacementKey)
		if err != nil || !bytes.Equal(stored, replacement) {
			t.Fatalf("replacement after prior attempt cleanup = %q, %v", stored, err)
		}
		if err := store.Delete(ctx, replacementKey); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "attachment-attempts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cleanup retained %d per-attachment filesystem entries", len(entries))
	}
}

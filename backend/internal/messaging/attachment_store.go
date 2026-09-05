package messaging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrObjectNotFound = errors.New("attachment object not found")

type AttachmentObjectStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

type FileAttachmentStore struct {
	root string
}

func NewFileAttachmentStore(root string) (*FileAttachmentStore, error) {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("absolute attachment object directory is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create attachment object directory: %w", err)
	}
	return &FileAttachmentStore{root: filepath.Clean(root)}, nil
}

func (store *FileAttachmentStore) Put(
	ctx context.Context,
	key string,
	value []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := store.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create attachment object prefix: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read existing attachment object: %w", readErr)
		}
		if bytes.Equal(existing, value) {
			return ctx.Err()
		}
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("create attachment object: %w", err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := file.Write(value); err != nil {
		return fmt.Errorf("write attachment object: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync attachment object: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close attachment object: %w", err)
	}
	keep = true
	return ctx.Err()
}

func (store *FileAttachmentStore) Get(
	ctx context.Context,
	key string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := store.path(key)
	if err != nil {
		return nil, err
	}
	value, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read attachment object: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return value, nil
}

func (store *FileAttachmentStore) Delete(
	ctx context.Context,
	key string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := store.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete attachment object: %w", err)
	}
	return ctx.Err()
}

func (store *FileAttachmentStore) path(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || filepath.IsAbs(key) {
		return "", ErrInvalidInput
	}
	clean := filepath.Clean(key)
	if clean == "." ||
		clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrInvalidInput
	}
	path := filepath.Join(store.root, clean)
	relative, err := filepath.Rel(store.root, path)
	if err != nil ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrInvalidInput
	}
	return path, nil
}

type MemoryAttachmentStore struct {
	mutex   sync.RWMutex
	objects map[string][]byte
}

func NewMemoryAttachmentStore() *MemoryAttachmentStore {
	return &MemoryAttachmentStore{objects: map[string][]byte{}}
}

func (store *MemoryAttachmentStore) Put(
	ctx context.Context,
	key string,
	value []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(key) == "" {
		return ErrInvalidInput
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if existing, exists := store.objects[key]; exists {
		if bytes.Equal(existing, value) {
			return ctx.Err()
		}
		return ErrConflict
	}
	store.objects[key] = append([]byte(nil), value...)
	return nil
}

func (store *MemoryAttachmentStore) Get(
	ctx context.Context,
	key string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	value, exists := store.objects[key]
	if !exists {
		return nil, ErrObjectNotFound
	}
	return append([]byte(nil), value...), nil
}

func (store *MemoryAttachmentStore) Delete(
	ctx context.Context,
	key string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.objects, key)
	return nil
}

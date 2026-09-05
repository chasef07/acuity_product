package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func attachmentObjectKey(attachmentID, token string) string {
	return "attachment-attempts/" + attachmentID + "-" + token
}

// Reserve cleanup before touching storage. Attempt keys are never reused, so
// a partial write or late completion cannot corrupt a replacement's bytes.
func reserveAttachmentWrite(ctx context.Context, tx pgx.Tx, attachmentID, token string, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO messaging_attachment_cleanup (object_key, attachment_id, cleanup_after)
		VALUES ($1, $2, $3)
	`, attachmentObjectKey(attachmentID, token), attachmentID, now.Add(attachmentClaimTTL))
	if err != nil {
		return fmt.Errorf("reserve attachment object cleanup: %w", err)
	}
	return nil
}

// Even cancellation must record that the storage call returned. Failure leaves
// its intent durable; an uncertain write is never assumed to have stopped.
func (m *Module) finishAttachmentWrite(ctx context.Context, objectKey string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := m.database.Exec(ctx, `
		UPDATE messaging_attachment_cleanup SET write_finished = true WHERE object_key = $1
	`, objectKey)
	if err != nil {
		return fmt.Errorf("record attachment write completion: %w", err)
	}
	return nil
}

func lockAttachmentWrite(ctx context.Context, tx pgx.Tx, objectKey string, now time.Time) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `
		SELECT write_finished AND cleanup_token IS NULL AND cleanup_after > $2
		FROM messaging_attachment_cleanup WHERE object_key = $1 FOR UPDATE
	`, objectKey, now).Scan(&allowed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("lock attachment write result: %w", err)
	}
	if !allowed {
		return ErrConflict
	}
	return nil
}

func (m *Module) ExpirePendingAttachments(ctx context.Context) error {
	if m.database == nil || m.config.AttachmentStore == nil {
		return ErrInvalidInput
	}
	// This short statement closes admission before deleting any bytes. Healthy
	// PENDING uploads need cleanup intent; PROCESSING uploads already own it.
	if _, err := m.database.Exec(ctx, `
		WITH expired AS MATERIALIZED (
			SELECT id, object_key, state FROM messaging_attachments
			WHERE direction = 'OUTBOUND' AND message_id IS NULL
				AND state IN ('PENDING', 'PROCESSING') AND expires_at <= $1
			ORDER BY expires_at, id FOR UPDATE SKIP LOCKED LIMIT 50
		), cleanup AS (
			INSERT INTO messaging_attachment_cleanup (object_key, attachment_id, write_finished, cleanup_after)
			SELECT object_key, id, true, $1 FROM expired WHERE state = 'PENDING'
			ON CONFLICT DO NOTHING
		)
		UPDATE messaging_attachments attachment
		SET state = 'UNAVAILABLE', storage_token = NULL, copy_started_at = NULL, updated_at = $1
		FROM expired WHERE attachment.id = expired.id
	`, m.now()); err != nil {
		return fmt.Errorf("expire pending attachments: %w", err)
	}
	for range 50 {
		processed, err := m.deleteNextAttachmentObject(ctx)
		if err != nil || !processed {
			return err
		}
	}
	return nil
}

func (m *Module) deleteNextAttachmentObject(ctx context.Context) (bool, error) {
	token, now := uuid.NewString(), m.now()
	var objectKey, attachmentID string
	var writeFinished bool
	// The returned write_finished snapshot matters: a writer may finish AFTER
	// this delete starts, in which case a later delete must confirm its absence.
	if err := m.database.QueryRow(ctx, `
		WITH candidate AS (
			SELECT object_key FROM messaging_attachment_cleanup
			WHERE cleanup_after <= $1
			ORDER BY cleanup_after, object_key FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE messaging_attachment_cleanup cleanup
		SET cleanup_token = $2, cleanup_after = $3
		FROM candidate WHERE cleanup.object_key = candidate.object_key
		RETURNING cleanup.object_key, attachment_id::text, write_finished
	`, now, token, now.Add(attachmentClaimTTL)).Scan(&objectKey, &attachmentID, &writeFinished); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("claim attachment object deletion: %w", err)
	}
	if err := m.config.AttachmentStore.Delete(ctx, objectKey); err != nil {
		// Lease expiry retries failures, including a crash or cancelled context.
		return true, fmt.Errorf("delete expired attachment object: %w", err)
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, fmt.Errorf("begin attachment deletion result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var owned bool
	if writeFinished {
		tag, err := tx.Exec(ctx, `DELETE FROM messaging_attachment_cleanup WHERE object_key = $1 AND cleanup_token = $2`, objectKey, token)
		if err != nil {
			return true, fmt.Errorf("complete attachment object deletion: %w", err)
		}
		owned = tag.RowsAffected() == 1
	} else {
		// Mounted filesystem calls are not forcibly cancellable. Keep only
		// uncertain writes as small tombstones, revisited hourly, until a writer
		// completion followed by deletion proves the object cannot reappear.
		// Retain the cleanup token: once deletion starts, even a late successful
		// write must never promote this attempt to a stored attachment.
		tag, err := tx.Exec(ctx, `
			UPDATE messaging_attachment_cleanup SET cleanup_after = $3
			WHERE object_key = $1 AND cleanup_token = $2
		`, objectKey, token, m.now().Add(time.Hour))
		if err != nil {
			return true, fmt.Errorf("retain uncertain attachment cleanup: %w", err)
		}
		owned = tag.RowsAffected() == 1
	}
	if owned {
		if _, err := tx.Exec(ctx, `
			DELETE FROM messaging_attachments
			WHERE id = $1 AND object_key = $2 AND direction = 'OUTBOUND'
				AND state IN ('UNAVAILABLE', 'PROCESSING') AND message_id IS NULL
		`, attachmentID, objectKey); err != nil {
			return true, fmt.Errorf("remove expired attachment metadata: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit attachment object deletion: %w", err)
	}
	return true, nil
}

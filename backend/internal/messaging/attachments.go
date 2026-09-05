package messaging

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	maximumAttachmentBytes     = 600 * 1024
	attachmentOperationTimeout = 20 * time.Second
	attachmentClaimTTL         = 30 * time.Second
)

type AttachmentState string

const (
	AttachmentPending     AttachmentState = "PENDING"
	AttachmentProcessing  AttachmentState = "PROCESSING"
	AttachmentStored      AttachmentState = "STORED"
	AttachmentUnavailable AttachmentState = "UNAVAILABLE"
)

type Attachment struct {
	ID          string
	MessageID   string
	Direction   Direction
	State       AttachmentState
	FileName    string
	ContentType string
	ByteSize    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type UploadAttachmentCommand struct {
	Identity     access.Identity
	PracticeID   string
	LocationID   string
	FileName     string
	DeclaredType string
	Content      []byte
}

type RetryAttachmentCommand struct {
	Identity     access.Identity
	AttachmentID string
}

type AttachmentContent struct {
	Attachment Attachment
	Content    []byte
}

func (m *Module) UploadAttachment(
	ctx context.Context,
	command UploadAttachmentCommand,
) (Attachment, error) {
	return m.uploadAttachment(ctx, command, "", "", "")
}

func (m *Module) uploadAttachment(
	ctx context.Context,
	command UploadAttachmentCommand,
	requestedID string,
	idempotencyKey string,
	retryOfMessageID string,
) (Attachment, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	command.FileName = strings.TrimSpace(filepath.Base(command.FileName))
	command.DeclaredType = strings.ToLower(strings.TrimSpace(command.DeclaredType))
	contentType, err := detectAttachmentType(command.Content)
	if m.database == nil ||
		m.access == nil ||
		m.config.AttachmentStore == nil ||
		command.Identity.Subject == "" ||
		command.PracticeID == "" ||
		command.LocationID == "" ||
		command.FileName == "" ||
		len(command.FileName) > 255 ||
		err != nil ||
		command.DeclaredType != contentType ||
		!fileNameMatchesType(command.FileName, contentType) {
		return Attachment{}, ErrInvalidInput
	}
	if requestedID == "" {
		requestedID = uuid.NewString()
	}
	if uuid.Validate(requestedID) != nil {
		return Attachment{}, ErrInvalidInput
	}
	// Retried Send-again requests share a durable reservation. Waiting for its
	// writer does not retain a connection or an authorization lock.
	ctx, cancel := context.WithTimeout(ctx, attachmentOperationTimeout)
	defer cancel()
	var result Attachment
	var token string
	for {
		var err error
		result, token, err = m.claimAttachmentUpload(ctx, command, requestedID, idempotencyKey, retryOfMessageID)
		if err != nil {
			return Attachment{}, err
		}
		if result.State != AttachmentProcessing {
			return result, nil
		}
		if token != "" {
			break
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Attachment{}, ctx.Err()
		case <-timer.C:
		}
	}
	objectKey := attachmentObjectKey(result.ID, token)
	writeErr := m.config.AttachmentStore.Put(ctx, objectKey, command.Content)
	if err := m.finishAttachmentWrite(ctx, objectKey); err != nil {
		return Attachment{}, errors.Join(writeErr, err)
	}
	if writeErr != nil {
		return Attachment{}, fmt.Errorf("store pending attachment: %w", writeErr)
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Attachment{}, fmt.Errorf("begin attachment upload result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAttachmentWrite(ctx, tx, objectKey, m.now()); err != nil {
		return Attachment{}, err
	}
	authorization, err := m.access.LockMutationAuthorization(ctx, tx, command.Identity, command.PracticeID, command.LocationID)
	if err != nil {
		return Attachment{}, ErrDenied
	}
	now := m.now()
	tag, err := tx.Exec(ctx, `
		UPDATE messaging_attachments
		SET state = 'PENDING', storage_token = NULL, copy_started_at = NULL, updated_at = $3
		WHERE id = $1 AND state = 'PROCESSING' AND storage_token = $2 AND expires_at > $3
	`, result.ID, token, now)
	if err != nil {
		return Attachment{}, fmt.Errorf("finalize pending attachment: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return Attachment{}, ErrConflict
	}
	if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{
		Action: "attachment.uploaded", ResourceType: "attachment", ResourceID: result.ID,
		ResourceVersion: 1, OccurredAt: now,
	}); err != nil {
		return Attachment{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM messaging_attachment_cleanup WHERE object_key = $1`, objectKey); err != nil {
		return Attachment{}, fmt.Errorf("complete attachment write: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Attachment{}, fmt.Errorf("commit pending attachment: %w", err)
	}
	result.State = AttachmentPending
	result.UpdatedAt = now
	return result, nil
}

// claimAttachmentUpload returns an empty token when another request owns the
// same in-progress reservation. Each resumed writer gets its own immutable key.
func (m *Module) claimAttachmentUpload(
	ctx context.Context, command UploadAttachmentCommand, attachmentID, idempotencyKey, retryOfMessageID string,
) (Attachment, string, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Attachment{}, "", fmt.Errorf("begin attachment upload: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if idempotencyKey != "" {
		if _, err := tx.Exec(ctx, `
			SELECT pg_advisory_xact_lock(hashtextextended($1, 0) # hashtextextended($2, 1) # hashtextextended($3, 2))
		`, command.PracticeID, command.Identity.Subject, idempotencyKey); err != nil {
			return Attachment{}, "", fmt.Errorf("lock attachment retry key: %w", err)
		}
	}
	authorization, err := m.access.LockMutationAuthorization(ctx, tx, command.Identity, command.PracticeID, command.LocationID)
	if err != nil {
		return Attachment{}, "", ErrDenied
	}
	now, token := m.now(), uuid.NewString()
	digest := sha256.Sum256(command.Content)
	tag, err := tx.Exec(ctx, `
		INSERT INTO messaging_attachments (
			id, practice_id, location_id, direction, state, actor_subject,
			file_name, content_type, byte_size, object_key, retry_idempotency_key,
			retry_of_message_id, expires_at, created_at, updated_at, copy_started_at, storage_token, content_sha256
		) VALUES (
			$1, $2, $3, 'OUTBOUND', 'PROCESSING', $4, $5, $6, $7, $8,
			NULLIF($9, ''), NULLIF($10, '')::uuid, $11, $12, $12, $12, $13, $14
		) ON CONFLICT DO NOTHING
	`, attachmentID, command.PracticeID, command.LocationID, command.Identity.Subject,
		command.FileName, command.DeclaredType, len(command.Content), attachmentObjectKey(attachmentID, token),
		idempotencyKey, retryOfMessageID, now.Add(15*time.Minute), now, token, digest[:])
	if err != nil {
		return Attachment{}, "", fmt.Errorf("reserve pending attachment: %w", err)
	}
	var result Attachment
	var practiceID, locationID, actorSubject, storedRetryKey, storedRetryOf string
	var startedAt, expiresAt *time.Time
	var storedDigest []byte
	if err := tx.QueryRow(ctx, `
		SELECT id::text, practice_id::text, location_id::text, direction, state, actor_subject,
			file_name, content_type, byte_size, created_at, updated_at, copy_started_at, expires_at,
			COALESCE(retry_idempotency_key, ''), COALESCE(retry_of_message_id::text, ''), content_sha256
		FROM messaging_attachments WHERE id = $1 FOR UPDATE
	`, attachmentID).Scan(&result.ID, &practiceID, &locationID, &result.Direction, &result.State,
		&actorSubject, &result.FileName, &result.ContentType, &result.ByteSize, &result.CreatedAt,
		&result.UpdatedAt, &startedAt, &expiresAt, &storedRetryKey, &storedRetryOf, &storedDigest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Attachment{}, "", ErrConflict
		}
		return Attachment{}, "", fmt.Errorf("load attachment reservation: %w", err)
	}
	if practiceID != command.PracticeID || locationID != command.LocationID ||
		actorSubject != command.Identity.Subject || result.Direction != DirectionOutbound ||
		result.FileName != command.FileName || result.ContentType != command.DeclaredType ||
		result.ByteSize != len(command.Content) || storedRetryKey != idempotencyKey || storedRetryOf != retryOfMessageID ||
		(storedDigest != nil && !bytes.Equal(storedDigest, digest[:])) ||
		result.State == AttachmentUnavailable ||
		(result.State != AttachmentStored && (expiresAt == nil || !expiresAt.After(now))) {
		return Attachment{}, "", ErrConflict
	}
	if tag.RowsAffected() == 0 {
		token = ""
		if result.State == AttachmentProcessing && (startedAt == nil || !startedAt.Add(attachmentClaimTTL).After(now)) {
			token = uuid.NewString()
			if _, err := tx.Exec(ctx, `
				UPDATE messaging_attachments SET copy_started_at = $2, storage_token = $3, object_key = $4, updated_at = $2 WHERE id = $1
			`, result.ID, now, token, attachmentObjectKey(result.ID, token)); err != nil {
				return Attachment{}, "", fmt.Errorf("resume attachment upload: %w", err)
			}
		}
	} else if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{
		Action: "attachment.upload_requested", ResourceType: "attachment", ResourceID: result.ID,
		ResourceVersion: 1, OccurredAt: now,
	}); err != nil {
		return Attachment{}, "", err
	}
	if token != "" {
		if err := reserveAttachmentWrite(ctx, tx, result.ID, token, now); err != nil {
			return Attachment{}, "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Attachment{}, "", fmt.Errorf("commit attachment reservation: %w", err)
	}
	return result, token, nil
}

func (m *Module) OpenAttachment(
	ctx context.Context,
	identity access.Identity,
	attachmentID string,
) (AttachmentContent, error) {
	attachmentID = strings.TrimSpace(attachmentID)
	if m.database == nil ||
		m.access == nil ||
		m.config.AttachmentStore == nil ||
		attachmentID == "" {
		return AttachmentContent{}, ErrDenied
	}
	attachment, objectKey, err := m.authorizeAttachmentRead(ctx, identity, attachmentID)
	if err != nil {
		return AttachmentContent{}, err
	}
	content, err := m.config.AttachmentStore.Get(ctx, objectKey)
	if err != nil {
		return AttachmentContent{}, ErrDenied
	}
	// Access or attachment state may have changed during a slow storage read.
	current, currentKey, err := m.authorizeAttachmentRead(ctx, identity, attachmentID)
	if err != nil || currentKey != objectKey || current.State != attachment.State {
		return AttachmentContent{}, ErrDenied
	}
	return AttachmentContent{Attachment: current, Content: content}, nil
}

func (m *Module) authorizeAttachmentRead(ctx context.Context, identity access.Identity, attachmentID string) (Attachment, string, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Attachment{}, "", fmt.Errorf("begin attachment read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	attachment, practiceID, locationID, objectKey, err := loadAttachment(ctx, tx, attachmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, "", ErrDenied
	}
	if err != nil {
		return Attachment{}, "", err
	}
	if attachment.State != AttachmentStored || objectKey == "" {
		return Attachment{}, "", ErrDenied
	}
	if _, err := m.access.LockReadAuthorization(ctx, tx, identity, practiceID, locationID); err != nil {
		return Attachment{}, "", ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return Attachment{}, "", fmt.Errorf("commit attachment read: %w", err)
	}
	return attachment, objectKey, nil
}

func (m *Module) ProviderMediaURL(
	attachmentID string,
) (string, error) {
	attachmentID = strings.TrimSpace(attachmentID)
	baseURL := strings.TrimRight(strings.TrimSpace(m.config.MediaPublicBaseURL), "/")
	if attachmentID == "" ||
		baseURL == "" ||
		len(m.config.MediaSigningKey) < 32 ||
		m.config.MediaURLTTL <= 0 {
		return "", ErrInvalidInput
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", ErrInvalidInput
	}
	expires := m.now().Add(m.config.MediaURLTTL).Unix()
	signature := signMediaURL(m.config.MediaSigningKey, attachmentID, expires)
	return fmt.Sprintf(
		"%s/%s?expires=%d&signature=%s",
		baseURL,
		url.PathEscape(attachmentID),
		expires,
		url.QueryEscape(signature),
	), nil
}

func (m *Module) OpenProviderAttachment(
	ctx context.Context,
	attachmentID string,
	expiresRaw string,
	signature string,
) (AttachmentContent, error) {
	attachmentID = strings.TrimSpace(attachmentID)
	expires, err := strconv.ParseInt(strings.TrimSpace(expiresRaw), 10, 64)
	if err != nil ||
		attachmentID == "" ||
		len(m.config.MediaSigningKey) < 32 ||
		m.config.AttachmentStore == nil ||
		m.now().After(time.Unix(expires, 0)) ||
		expires > m.now().Add(m.config.MediaURLTTL+time.Minute).Unix() ||
		!hmac.Equal(
			[]byte(strings.TrimSpace(signature)),
			[]byte(signMediaURL(m.config.MediaSigningKey, attachmentID, expires)),
		) {
		return AttachmentContent{}, ErrDenied
	}
	var attachment Attachment
	var objectKey string
	if err := m.database.QueryRow(ctx, `
		SELECT
			id::text,
			COALESCE(message_id::text, ''),
			direction,
			state,
			file_name,
			content_type,
			COALESCE(byte_size, 0),
			created_at,
			updated_at,
			COALESCE(object_key, '')
		FROM messaging_attachments
		WHERE id = $1
			AND direction = 'OUTBOUND'
			AND state = 'STORED'
			AND message_id IS NOT NULL
	`, attachmentID).Scan(
		&attachment.ID,
		&attachment.MessageID,
		&attachment.Direction,
		&attachment.State,
		&attachment.FileName,
		&attachment.ContentType,
		&attachment.ByteSize,
		&attachment.CreatedAt,
		&attachment.UpdatedAt,
		&objectKey,
	); err != nil {
		return AttachmentContent{}, ErrDenied
	}
	content, err := m.config.AttachmentStore.Get(ctx, objectKey)
	if err != nil {
		return AttachmentContent{}, ErrDenied
	}
	var authorized bool
	if !m.now().Before(time.Unix(expires, 0)) {
		return AttachmentContent{}, ErrDenied
	}
	if err := m.database.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM messaging_attachments
		WHERE id = $1 AND direction = 'OUTBOUND' AND state = 'STORED'
			AND message_id = $2 AND object_key = $3)
	`, attachment.ID, attachment.MessageID, objectKey).Scan(&authorized); err != nil || !authorized {
		return AttachmentContent{}, ErrDenied
	}
	return AttachmentContent{Attachment: attachment, Content: content}, nil
}

func (m *Module) ProcessNextAttachment(ctx context.Context) (bool, error) {
	if m.database == nil ||
		m.access == nil ||
		m.config.AttachmentStore == nil ||
		m.config.HTTPClient == nil {
		return false, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, attachmentOperationTimeout)
	defer cancel()
	token, now := uuid.NewString(), m.now()
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin inbound attachment copy: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var attachment Attachment
	var practiceID, providerURL string
	if err := tx.QueryRow(ctx, `
		SELECT
			id::text,
			message_id::text,
			practice_id::text,
			direction,
			state,
			file_name,
			content_type,
			COALESCE(byte_size, 0),
			provider_media_url,
			created_at,
			updated_at
		FROM messaging_attachments
		WHERE direction = 'INBOUND'
			AND state = 'PROCESSING'
			AND (
				copy_started_at IS NULL
				OR copy_started_at < $1::timestamptz - interval '30 seconds'
			)
		ORDER BY updated_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, m.now()).Scan(
		&attachment.ID,
		&attachment.MessageID,
		&practiceID,
		&attachment.Direction,
		&attachment.State,
		&attachment.FileName,
		&attachment.ContentType,
		&attachment.ByteSize,
		&providerURL,
		&attachment.CreatedAt,
		&attachment.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit empty inbound attachment copy: %w", err)
			}
			return false, nil
		}
		return false, fmt.Errorf("claim inbound attachment copy: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE messaging_attachments
		SET copy_started_at = $2, storage_token = $3, object_key = $4, updated_at = $2
		WHERE id = $1
	`, attachment.ID, now, token, attachmentObjectKey(attachment.ID, token)); err != nil {
		return false, fmt.Errorf("mark inbound attachment copy: %w", err)
	}
	if err := reserveAttachmentWrite(ctx, tx, attachment.ID, token, now); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit inbound attachment claim: %w", err)
	}

	content, contentType, copyErr := m.downloadAttachment(ctx, providerURL)
	objectKey := attachmentObjectKey(attachment.ID, token)
	if copyErr == nil {
		copyErr = m.config.AttachmentStore.Put(ctx, objectKey, content)
	}
	if err := m.finishAttachmentWrite(ctx, objectKey); err != nil {
		return true, errors.Join(copyErr, err)
	}
	finishTx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, fmt.Errorf("begin inbound attachment result: %w", err)
	}
	defer func() { _ = finishTx.Rollback(ctx) }()
	if copyErr == nil {
		if err := lockAttachmentWrite(ctx, finishTx, objectKey, m.now()); err != nil {
			return true, err
		}
	}
	state := AttachmentStored
	if copyErr != nil {
		state = AttachmentUnavailable
	}
	tag, err := finishTx.Exec(ctx, `
		UPDATE messaging_attachments
		SET state = $3, content_type = CASE WHEN $3 = 'STORED' THEN $4 ELSE content_type END,
			byte_size = CASE WHEN $3 = 'STORED' THEN $5 ELSE byte_size END,
			copy_started_at = NULL, storage_token = NULL, updated_at = $6
		WHERE id = $1 AND state = 'PROCESSING' AND storage_token = $2
	`, attachment.ID, token, state, contentType, len(content), m.now())
	if err != nil {
		return true, fmt.Errorf("record inbound attachment result: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return true, ErrConflict
	}
	if copyErr == nil {
		if _, err := finishTx.Exec(ctx, `DELETE FROM messaging_attachment_cleanup WHERE object_key = $1`, objectKey); err != nil {
			return true, fmt.Errorf("complete inbound attachment write: %w", err)
		}
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, finishTx, practiceID); err != nil {
		return true, err
	}
	if err := finishTx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit inbound attachment result: %w", err)
	}
	return true, nil
}

func (m *Module) RetryAttachment(
	ctx context.Context,
	command RetryAttachmentCommand,
) (Attachment, error) {
	command.AttachmentID = strings.TrimSpace(command.AttachmentID)
	if m.database == nil ||
		m.access == nil ||
		command.AttachmentID == "" {
		return Attachment{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Attachment{}, fmt.Errorf("begin attachment copy retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	attachment, practiceID, locationID, _, err := loadAttachment(
		ctx,
		tx,
		command.AttachmentID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, ErrDenied
	}
	if err != nil {
		return Attachment{}, err
	}
	if attachment.Direction != DirectionInbound ||
		attachment.State != AttachmentUnavailable {
		return Attachment{}, ErrConflict
	}
	authorization, err := m.access.LockMutationAuthorization(
		ctx,
		tx,
		command.Identity,
		practiceID,
		locationID,
	)
	if err != nil {
		return Attachment{}, ErrDenied
	}
	now := m.now()
	if _, err := tx.Exec(ctx, `
		UPDATE messaging_attachments
		SET state = 'PROCESSING', copy_started_at = NULL, storage_token = NULL, updated_at = $2
		WHERE id = $1 AND state = 'UNAVAILABLE'
	`, attachment.ID, now); err != nil {
		return Attachment{}, fmt.Errorf("retry inbound attachment copy: %w", err)
	}
	if err := m.access.AuditOperatorMutation(
		ctx,
		tx,
		authorization,
		access.OperatorMutationAudit{
			Action:          "attachment.copy_retried",
			ResourceType:    "attachment",
			ResourceID:      attachment.ID,
			ResourceVersion: 1,
			OccurredAt:      now,
		},
	); err != nil {
		return Attachment{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return Attachment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Attachment{}, fmt.Errorf("commit attachment copy retry: %w", err)
	}
	attachment.State = AttachmentProcessing
	attachment.UpdatedAt = now
	return attachment, nil
}

func (m *Module) downloadAttachment(
	ctx context.Context,
	providerURL string,
) ([]byte, string, error) {
	parsed, err := url.Parse(providerURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, "", ErrInvalidInput
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, providerURL, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := m.config.HTTPClient.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return nil, "", ErrObjectNotFound
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maximumAttachmentBytes+1))
	if err != nil {
		return nil, "", err
	}
	contentType, err := detectAttachmentType(content)
	if err != nil {
		return nil, "", err
	}
	return content, contentType, nil
}

func loadAttachment(
	ctx context.Context,
	tx pgx.Tx,
	attachmentID string,
) (Attachment, string, string, string, error) {
	var attachment Attachment
	var practiceID, locationID, objectKey string
	err := tx.QueryRow(ctx, `
		SELECT
			id::text,
			COALESCE(message_id::text, ''),
			practice_id::text,
			location_id::text,
			direction,
			state,
			file_name,
			content_type,
			COALESCE(byte_size, 0),
			COALESCE(object_key, ''),
			created_at,
			updated_at
		FROM messaging_attachments
		WHERE id = $1
		FOR SHARE
	`, attachmentID).Scan(
		&attachment.ID,
		&attachment.MessageID,
		&practiceID,
		&locationID,
		&attachment.Direction,
		&attachment.State,
		&attachment.FileName,
		&attachment.ContentType,
		&attachment.ByteSize,
		&objectKey,
		&attachment.CreatedAt,
		&attachment.UpdatedAt,
	)
	return attachment, practiceID, locationID, objectKey, err
}

func detectAttachmentType(content []byte) (string, error) {
	if len(content) == 0 || len(content) > maximumAttachmentBytes {
		return "", ErrInvalidInput
	}
	contentType := http.DetectContentType(content)
	if len(content) >= 12 &&
		string(content[0:4]) == "RIFF" &&
		string(content[8:12]) == "WEBP" {
		contentType = "image/webp"
	}
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf":
		return contentType, nil
	default:
		return "", ErrInvalidInput
	}
}

func fileNameMatchesType(fileName string, contentType string) bool {
	extension := strings.ToLower(filepath.Ext(fileName))
	switch contentType {
	case "image/jpeg":
		return extension == ".jpg" || extension == ".jpeg"
	case "image/png":
		return extension == ".png"
	case "image/gif":
		return extension == ".gif"
	case "image/webp":
		return extension == ".webp"
	case "application/pdf":
		return extension == ".pdf"
	default:
		return false
	}
}

func attachmentFileName(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return "attachment.jpg"
	case "image/png":
		return "attachment.png"
	case "image/gif":
		return "attachment.gif"
	case "image/webp":
		return "attachment.webp"
	default:
		return "attachment.pdf"
	}
}

func signMediaURL(key []byte, attachmentID string, expires int64) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(attachmentID))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(strconv.FormatInt(expires, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

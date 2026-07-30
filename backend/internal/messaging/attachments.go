package messaging

import (
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

const maximumAttachmentBytes = 600 * 1024

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
	Identity         access.Identity
	PracticeID       string
	LocationID       string
	FileName         string
	DeclaredType     string
	Content          []byte
	SupportSessionID string
}

type RetryAttachmentCommand struct {
	Identity         access.Identity
	AttachmentID     string
	SupportSessionID string
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
	command.SupportSessionID = strings.TrimSpace(command.SupportSessionID)
	contentType, err := detectAttachmentType(command.Content)
	if m.pool == nil ||
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
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Attachment{}, fmt.Errorf("begin attachment upload: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if idempotencyKey != "" {
		if _, err := tx.Exec(ctx, `
			SELECT pg_advisory_xact_lock(
				hashtextextended($1, 0)
					# hashtextextended($2, 1)
					# hashtextextended($3, 2)
			)
		`, command.PracticeID, command.Identity.Subject,
			idempotencyKey,
		); err != nil {
			return Attachment{}, fmt.Errorf("lock attachment retry key: %w", err)
		}
	}
	authorization, err := m.access.LockMutationAuthorization(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		command.LocationID,
		command.SupportSessionID,
	)
	if err != nil {
		if isSupportAuthorizationError(err) {
			return Attachment{}, err
		}
		return Attachment{}, ErrDenied
	}
	if idempotencyKey != "" {
		var existing Attachment
		var existingPracticeID, existingLocationID, actorSubject string
		if err := tx.QueryRow(ctx, `
			SELECT
				id::text,
				practice_id::text,
				location_id::text,
				direction,
				state,
				actor_subject,
				file_name,
				content_type,
				byte_size,
				created_at,
				updated_at
			FROM messaging_attachments
			WHERE practice_id = $1
				AND actor_subject = $2
				AND retry_idempotency_key = $3
			FOR SHARE
		`, command.PracticeID, command.Identity.Subject,
			idempotencyKey,
		).Scan(
			&existing.ID,
			&existingPracticeID,
			&existingLocationID,
			&existing.Direction,
			&existing.State,
			&actorSubject,
			&existing.FileName,
			&existing.ContentType,
			&existing.ByteSize,
			&existing.CreatedAt,
			&existing.UpdatedAt,
		); err == nil {
			if existing.ID != requestedID ||
				existingPracticeID != command.PracticeID ||
				existingLocationID != command.LocationID ||
				existing.Direction != DirectionOutbound ||
				actorSubject != command.Identity.Subject ||
				existing.FileName != command.FileName ||
				existing.ContentType != contentType ||
				existing.ByteSize != len(command.Content) {
				return Attachment{}, ErrConflict
			}
			if err := tx.Commit(ctx); err != nil {
				return Attachment{}, fmt.Errorf(
					"commit replayed retry attachment: %w",
					err,
				)
			}
			return existing, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return Attachment{}, fmt.Errorf(
				"load attachment retry reservation: %w",
				err,
			)
		}
	}
	now := m.now()
	attachmentID := requestedID
	if attachmentID == "" {
		attachmentID = uuid.NewString()
	}
	if uuid.Validate(attachmentID) != nil {
		return Attachment{}, ErrInvalidInput
	}
	result := Attachment{
		ID:          attachmentID,
		Direction:   DirectionOutbound,
		State:       AttachmentPending,
		FileName:    command.FileName,
		ContentType: contentType,
		ByteSize:    len(command.Content),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	objectKey := "attachments/" + result.ID
	if err := m.config.AttachmentStore.Put(
		ctx,
		objectKey,
		command.Content,
	); err != nil {
		return Attachment{}, fmt.Errorf("store pending attachment: %w", err)
	}
	keepObject := false
	defer func() {
		if !keepObject {
			_ = m.config.AttachmentStore.Delete(context.Background(), objectKey)
		}
	}()
	tag, err := tx.Exec(ctx, `
		INSERT INTO messaging_attachments (
			id,
			practice_id,
			location_id,
			direction,
			state,
			actor_subject,
			file_name,
			content_type,
			byte_size,
			object_key,
			retry_idempotency_key,
			retry_of_message_id,
			expires_at,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, 'OUTBOUND', 'PENDING', $4, $5, $6, $7, $8,
			NULLIF($9, ''), NULLIF($10, '')::uuid, $11, $12, $12
		)
		ON CONFLICT (id) DO NOTHING
	`, result.ID, command.PracticeID, command.LocationID,
		command.Identity.Subject, result.FileName, result.ContentType,
		result.ByteSize, objectKey, idempotencyKey, retryOfMessageID,
		now.Add(15*time.Minute), now,
	)
	if err != nil {
		return Attachment{}, fmt.Errorf("record pending attachment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		keepObject = true
		var existing Attachment
		var practiceID, locationID, actorSubject, existingObjectKey string
		if err := tx.QueryRow(ctx, `
			SELECT
				id::text,
				practice_id::text,
				location_id::text,
				direction,
				state,
				actor_subject,
				file_name,
				content_type,
				byte_size,
				object_key,
				created_at,
				updated_at
			FROM messaging_attachments
			WHERE id = $1
			FOR SHARE
		`, result.ID).Scan(
			&existing.ID,
			&practiceID,
			&locationID,
			&existing.Direction,
			&existing.State,
			&actorSubject,
			&existing.FileName,
			&existing.ContentType,
			&existing.ByteSize,
			&existingObjectKey,
			&existing.CreatedAt,
			&existing.UpdatedAt,
		); err != nil {
			return Attachment{}, fmt.Errorf("load replayed attachment: %w", err)
		}
		if practiceID != command.PracticeID ||
			locationID != command.LocationID ||
			existing.Direction != DirectionOutbound ||
			actorSubject != command.Identity.Subject ||
			existing.FileName != result.FileName ||
			existing.ContentType != result.ContentType ||
			existing.ByteSize != result.ByteSize ||
			existingObjectKey != objectKey {
			return Attachment{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Attachment{}, fmt.Errorf("commit replayed attachment: %w", err)
		}
		return existing, nil
	}
	if err := m.access.AuditSupportedMutation(
		ctx,
		tx,
		authorization,
		access.SupportedMutationAudit{
			Action:          "attachment.uploaded",
			ResourceType:    "attachment",
			ResourceID:      result.ID,
			ResourceVersion: 1,
			OccurredAt:      now,
		},
	); err != nil {
		return Attachment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Attachment{}, fmt.Errorf("commit pending attachment: %w", err)
	}
	keepObject = true
	return result, nil
}

func (m *Module) OpenAttachment(
	ctx context.Context,
	identity access.Identity,
	attachmentID string,
) (AttachmentContent, error) {
	attachmentID = strings.TrimSpace(attachmentID)
	if m.pool == nil ||
		m.access == nil ||
		m.config.AttachmentStore == nil ||
		attachmentID == "" {
		return AttachmentContent{}, ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AttachmentContent{}, fmt.Errorf("begin attachment read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	attachment, practiceID, locationID, objectKey, err := loadAttachment(
		ctx,
		tx,
		attachmentID,
	)
	if errors.Is(err, pgx.ErrNoRows) ||
		attachment.State != AttachmentStored ||
		objectKey == "" {
		return AttachmentContent{}, ErrDenied
	}
	if err != nil {
		return AttachmentContent{}, err
	}
	if _, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		identity,
		practiceID,
		locationID,
	); err != nil {
		return AttachmentContent{}, ErrDenied
	}
	content, err := m.config.AttachmentStore.Get(ctx, objectKey)
	if err != nil {
		return AttachmentContent{}, ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return AttachmentContent{}, fmt.Errorf("commit attachment read: %w", err)
	}
	return AttachmentContent{Attachment: attachment, Content: content}, nil
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
	if err := m.pool.QueryRow(ctx, `
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
	return AttachmentContent{Attachment: attachment, Content: content}, nil
}

func (m *Module) ProcessNextAttachment(ctx context.Context) (bool, error) {
	if m.pool == nil ||
		m.access == nil ||
		m.config.AttachmentStore == nil ||
		m.config.HTTPClient == nil {
		return false, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
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
		SET copy_started_at = $2, updated_at = $2
		WHERE id = $1
	`, attachment.ID, m.now()); err != nil {
		return false, fmt.Errorf("mark inbound attachment copy: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit inbound attachment claim: %w", err)
	}

	content, contentType, copyErr := m.downloadAttachment(ctx, providerURL)
	objectKey := "attachments/" + attachment.ID
	if copyErr == nil {
		copyErr = m.config.AttachmentStore.Put(ctx, objectKey, content)
	}
	finishTx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		if copyErr == nil {
			_ = m.config.AttachmentStore.Delete(context.Background(), objectKey)
		}
		return true, fmt.Errorf("begin inbound attachment result: %w", err)
	}
	defer func() { _ = finishTx.Rollback(ctx) }()
	if copyErr != nil {
		if _, err := finishTx.Exec(ctx, `
			UPDATE messaging_attachments
			SET
				state = 'UNAVAILABLE',
				copy_started_at = NULL,
				updated_at = $2
			WHERE id = $1 AND state = 'PROCESSING'
		`, attachment.ID, m.now()); err != nil {
			return true, fmt.Errorf("record unavailable inbound attachment: %w", err)
		}
	} else {
		if _, err := finishTx.Exec(ctx, `
			UPDATE messaging_attachments
			SET
				state = 'STORED',
				content_type = $2,
				byte_size = $3,
				object_key = $4,
				copy_started_at = NULL,
				updated_at = $5
			WHERE id = $1 AND state = 'PROCESSING'
		`, attachment.ID, contentType, len(content), objectKey, m.now()); err != nil {
			_ = m.config.AttachmentStore.Delete(context.Background(), objectKey)
			return true, fmt.Errorf("record stored inbound attachment: %w", err)
		}
	}
	if _, err := m.access.RecordWorkspaceChange(
		ctx,
		finishTx,
		practiceID,
	); err != nil {
		if copyErr == nil {
			_ = m.config.AttachmentStore.Delete(context.Background(), objectKey)
		}
		return true, err
	}
	if err := finishTx.Commit(ctx); err != nil {
		if copyErr == nil {
			_ = m.config.AttachmentStore.Delete(context.Background(), objectKey)
		}
		return true, fmt.Errorf("commit inbound attachment result: %w", err)
	}
	return true, nil
}

func (m *Module) RetryAttachment(
	ctx context.Context,
	command RetryAttachmentCommand,
) (Attachment, error) {
	command.AttachmentID = strings.TrimSpace(command.AttachmentID)
	command.SupportSessionID = strings.TrimSpace(command.SupportSessionID)
	if m.pool == nil ||
		m.access == nil ||
		command.AttachmentID == "" {
		return Attachment{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
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
		command.SupportSessionID,
	)
	if err != nil {
		if isSupportAuthorizationError(err) {
			return Attachment{}, err
		}
		return Attachment{}, ErrDenied
	}
	now := m.now()
	if _, err := tx.Exec(ctx, `
		UPDATE messaging_attachments
		SET state = 'PROCESSING', copy_started_at = NULL, updated_at = $2
		WHERE id = $1 AND state = 'UNAVAILABLE'
	`, attachment.ID, now); err != nil {
		return Attachment{}, fmt.Errorf("retry inbound attachment copy: %w", err)
	}
	if err := m.access.AuditSupportedMutation(
		ctx,
		tx,
		authorization,
		access.SupportedMutationAudit{
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

func (m *Module) ExpirePendingAttachments(ctx context.Context) error {
	if m.pool == nil || m.config.AttachmentStore == nil {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin pending attachment expiration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		WITH expired AS (
			SELECT id
			FROM messaging_attachments
			WHERE direction = 'OUTBOUND'
				AND state = 'PENDING'
				AND message_id IS NULL
				AND expires_at <= $1
			ORDER BY expires_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 50
		)
		DELETE FROM messaging_attachments attachment
		USING expired
		WHERE attachment.id = expired.id
		RETURNING attachment.object_key
	`, m.now())
	if err != nil {
		return fmt.Errorf("expire pending attachments: %w", err)
	}
	objectKeys := make([]string, 0, 50)
	for rows.Next() {
		var objectKey string
		if err := rows.Scan(&objectKey); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired pending attachment: %w", err)
		}
		objectKeys = append(objectKeys, objectKey)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate expired pending attachments: %w", err)
	}
	rows.Close()
	for _, objectKey := range objectKeys {
		if err := m.config.AttachmentStore.Delete(ctx, objectKey); err != nil {
			return fmt.Errorf("delete expired pending attachment object: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit pending attachment expiration: %w", err)
	}
	return nil
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

func isSupportAuthorizationError(err error) bool {
	return errors.Is(err, access.ErrSupportRequired) ||
		errors.Is(err, access.ErrSupportExpired) ||
		errors.Is(err, access.ErrSupportRevoked) ||
		errors.Is(err, access.ErrSupportPracticeMismatch)
}

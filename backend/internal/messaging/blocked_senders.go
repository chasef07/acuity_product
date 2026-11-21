package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

type BlockSenderCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Phone      string
	Blocked    bool
}

func (m *Module) SetSenderBlocked(ctx context.Context, command BlockSenderCommand) (bool, error) {
	phone, err := normalizePhone(command.Phone)
	if err != nil || m.database == nil || m.access == nil || m.work == nil ||
		strings.TrimSpace(command.PracticeID) == "" || strings.TrimSpace(command.LocationID) == "" {
		return false, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin sender block: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockMutationAuthorization(ctx, tx, command.Identity, command.PracticeID, command.LocationID)
	if err != nil {
		return false, messagingAccessError(err)
	}
	if command.Blocked {
		newBlock := false
		err := tx.QueryRow(ctx, `
			INSERT INTO messaging_blocked_senders (practice_id, phone, blocked_by_email, blocked_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (practice_id, phone) DO NOTHING
			RETURNING true
		`, command.PracticeID, phone, authorization.Actor.Email, m.now()).Scan(&newBlock)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf("block sender: %w", err)
		}
		if err := m.work.CloseTextReviewsForBlockedSender(ctx, tx, command.PracticeID, phone, work.ActorSnapshot{
			Kind: access.ActorHuman, Subject: authorization.Actor.Subject, Email: authorization.Actor.Email,
		}); err != nil {
			return false, err
		}
		if newBlock {
			if err := m.queueStopText(ctx, tx, authorization, command, phone); err != nil {
				return false, err
			}
		}
	} else if _, err := tx.Exec(ctx, `
		DELETE FROM messaging_blocked_senders WHERE practice_id = $1 AND phone = $2
	`, command.PracticeID, phone); err != nil {
		return false, fmt.Errorf("unblock sender: %w", err)
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, command.PracticeID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit sender block: %w", err)
	}
	return command.Blocked, nil
}

const blockedSenderStopText = "STOP"

func (m *Module) queueStopText(ctx context.Context, tx pgx.Tx, authorization access.Authorization, command BlockSenderCommand, phone string) error {
	var sender, profileID string
	if err := tx.QueryRow(ctx, `
		SELECT sender, messaging_profile_id
		FROM messaging_location_configurations
		WHERE practice_id = $1 AND location_id = $2 AND active
		FOR SHARE
	`, command.PracticeID, command.LocationID).Scan(&sender, &profileID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("load Location texting configuration: %w", err)
	}
	now := m.now()
	thread, err := findOrCreateMessageThread(ctx, tx, command.PracticeID, command.LocationID, sender, phone, now)
	if err != nil {
		return fmt.Errorf("find or create Message Thread: %w", err)
	}
	if thread.OutboundBlocked {
		return nil
	}
	stop := SendCommand{
		Identity:       command.Identity,
		PracticeID:     command.PracticeID,
		LocationID:     command.LocationID,
		Destination:    phone,
		Body:           blockedSenderStopText,
		IdempotencyKey: fmt.Sprintf("block-stop:%s:%d", strings.TrimPrefix(phone, "+"), now.UnixMicro()),
	}
	fingerprint, err := sendFingerprint(stop)
	if err != nil {
		return err
	}
	messageID, err := insertOutboundMessage(ctx, tx, outboundMessageInput{
		ThreadID:       thread.ID,
		PracticeID:     command.PracticeID,
		LocationID:     command.LocationID,
		Body:           blockedSenderStopText,
		Sender:         sender,
		Destination:    phone,
		Delivery:       DeliverySending,
		CreatorKind:    access.ActorHuman,
		CreatorSubject: command.Identity.Subject,
		CreatedAt:      now,
	})
	if err != nil {
		return fmt.Errorf("queue STOP text: %w", err)
	}
	callbackToken, err := newOpaqueToken()
	if err != nil {
		return err
	}
	if err := insertMessageProviderCommand(ctx, tx, messageProviderCommandInput{
		MessageID:          messageID,
		PracticeID:         command.PracticeID,
		LocationID:         command.LocationID,
		ActorSubject:       command.Identity.Subject,
		IdempotencyKey:     stop.IdempotencyKey,
		InputFingerprint:   fingerprint[:],
		CallbackToken:      callbackToken,
		MessagingProfileID: profileID,
		CreatedAt:          now,
	}); err != nil {
		return fmt.Errorf("queue STOP provider command: %w", err)
	}
	return m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{
		Action:          "message.sent",
		ResourceType:    "message",
		ResourceID:      messageID,
		ResourceVersion: 1,
		OccurredAt:      now,
	})
}

func (m *Module) SenderBlocked(ctx context.Context, identity access.Identity, practiceID, phone string) (bool, error) {
	phone, err := normalizePhone(phone)
	if err != nil || m.database == nil || m.access == nil || strings.TrimSpace(practiceID) == "" {
		return false, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin sender block read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := m.access.LockReadAuthorization(ctx, tx, identity, practiceID, ""); err != nil {
		return false, messagingAccessError(err)
	}
	blocked, err := isBlockedSender(ctx, tx, practiceID, phone)
	if err != nil {
		return false, err
	}
	return blocked, tx.Commit(ctx)
}

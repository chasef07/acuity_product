package humancalling

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (m *Module) CreateHandoff(
	ctx context.Context,
	command CreateHandoffCommand,
) (Handoff, error) {
	if m.config.HandoffAdmissionClosed {
		return Handoff{}, ErrHandoffAdmissionClosed
	}
	if (strings.TrimSpace(command.OfficeKey) == "") ==
		(strings.TrimSpace(command.LocationID) == "") {
		return Handoff{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Handoff{}, fmt.Errorf("begin handoff: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if command.OfficeKey != "" {
		if m.access == nil {
			return Handoff{}, ErrDenied
		}
		authorization, err := m.access.LockServiceAuthorization(
			ctx,
			tx,
			command.Service,
			command.OfficeKey,
			access.ServiceCapabilityHumanHandoff,
		)
		if err != nil {
			if errors.Is(err, access.ErrDenied) {
				return Handoff{}, ErrDenied
			}
			return Handoff{}, fmt.Errorf("resolve Abita Office Route: %w", err)
		}
		command.LocationID = authorization.LocationID
	}
	if err := validateHandoff(command, m.config.HandoffSIPDomain); err != nil {
		return Handoff{}, err
	}
	fingerprint, err := handoffFingerprint(command)
	if err != nil {
		return Handoff{}, err
	}

	var existing Handoff
	var existingFingerprint []byte
	err = tx.QueryRow(ctx, `
		SELECT id::text, expires_at, input_fingerprint
		FROM human_calling_handoffs
		WHERE service_subject = $1 AND idempotency_key = $2
		FOR UPDATE
	`, command.Service.Subject, command.IdempotencyKey).Scan(
		&existing.ID, &existing.ExpiresAt, &existingFingerprint,
	)
	if err == nil {
		if !hmac.Equal(existingFingerprint, fingerprint[:]) {
			return Handoff{}, fmt.Errorf("%w: idempotency key belongs to another handoff", ErrConflict)
		}
		existing.SIPDestination = m.sipDestination()
		if err := tx.Commit(ctx); err != nil {
			return Handoff{}, fmt.Errorf("commit replayed handoff: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Handoff{}, fmt.Errorf("find replayed handoff: %w", err)
	}

	issuedAt := m.now()
	result := Handoff{ID: uuid.NewString(), ExpiresAt: issuedAt.Add(m.config.HandoffLifetime)}
	var insertedID string
	err = tx.QueryRow(ctx, `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, phone, phone_source,
			display_name, name_source, transfer_reason, reason_source, expires_at,
			created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''),
			NULLIF($12, ''), NULLIF($13, ''), $14, $15
		)
		ON CONFLICT DO NOTHING
		RETURNING id::text
	`, result.ID, command.Service.Subject, command.Service.PracticeID,
		command.LocationID, command.SourceCallID, command.IdempotencyKey,
		fingerprint[:], command.Contact.Phone,
		command.Contact.PhoneSource, command.Contact.DisplayName,
		command.Contact.NameSource, command.Contact.TransferReason,
		command.Contact.ReasonSource, result.ExpiresAt, issuedAt,
	).Scan(&insertedID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Handoff{}, fmt.Errorf("create handoff: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			SELECT id::text, expires_at, input_fingerprint
			FROM human_calling_handoffs
			WHERE (service_subject = $1 AND idempotency_key = $2)
				OR (service_subject = $1 AND source_call_id = $3)
			ORDER BY (idempotency_key = $2) DESC
			LIMIT 1
			FOR UPDATE
		`, command.Service.Subject, command.IdempotencyKey,
			command.SourceCallID).Scan(
			&result.ID, &result.ExpiresAt, &existingFingerprint,
		); err != nil {
			return Handoff{}, fmt.Errorf("reload concurrent handoff: %w", err)
		}
		if !hmac.Equal(existingFingerprint, fingerprint[:]) {
			return Handoff{}, fmt.Errorf("%w: handoff identity belongs to another request", ErrConflict)
		}
	} else {
		result.ID = insertedID
	}
	if err := tx.Commit(ctx); err != nil {
		return Handoff{}, fmt.Errorf("commit handoff: %w", err)
	}
	result.SIPDestination = m.sipDestination()
	return result, nil
}

func (m *Module) resolveHandoffForRefer(
	ctx context.Context,
	tx pgx.Tx,
	fact ProviderFact,
) (string, string, string, error) {
	if !canonicalE164.MatchString(fact.From) || !canonicalE164.MatchString(fact.To) {
		return "", "", "", ErrInvalidHandoff
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, practice_id::text, location_id::text
		FROM human_calling_handoffs
		WHERE phone = $1 AND consumed_at IS NULL
			AND created_at <= $2 AND expires_at > $2
		ORDER BY created_at, id
		LIMIT 2
		FOR UPDATE
	`, fact.From, fact.OccurredAt)
	if err != nil {
		return "", "", "", fmt.Errorf("correlate handoff admission: %w", err)
	}
	defer rows.Close()

	var handoffID, practiceID, locationID string
	candidateCount := 0
	for rows.Next() {
		candidateCount++
		if err := rows.Scan(&handoffID, &practiceID, &locationID); err != nil {
			return "", "", "", fmt.Errorf("scan handoff admission: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", "", fmt.Errorf("read handoff admission: %w", err)
	}
	if candidateCount != 1 {
		return "", "", "", ErrInvalidHandoff
	}
	return handoffID, practiceID, locationID, nil
}

func appendTimeline(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	practiceID string,
	kind string,
	actorSubject string,
	eventID string,
	commandID string,
	opaque string,
	errorCode string,
	occurredAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_timeline (
			call_id, practice_id, kind, actor_subject, provider_event_id,
			provider_command_id, opaque_reference, error_code, occurred_at
		)
		VALUES (
			$1, $2, $3, NULLIF($4, ''), NULLIF($5, ''),
			NULLIF($6, '')::uuid, NULLIF($7, ''), NULLIF($8, ''), $9
		)
		ON CONFLICT DO NOTHING
	`, callID, practiceID, kind, actorSubject, eventID, commandID,
		opaque, errorCode, occurredAt); err != nil {
		return fmt.Errorf("append Call timeline: %w", err)
	}
	if eventID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_receipts
			SET call_id = COALESCE(call_id, $2)
			WHERE event_id = $1
		`, eventID, callID); err != nil {
			return fmt.Errorf("attach provider receipt to Call: %w", err)
		}
	}
	return nil
}

func validateHandoff(command CreateHandoffCommand, sipDomain string) error {
	if strings.TrimSpace(command.Service.Subject) == "" ||
		strings.TrimSpace(command.Service.PracticeID) == "" ||
		strings.TrimSpace(command.LocationID) == "" ||
		strings.TrimSpace(command.SourceCallID) == "" ||
		strings.TrimSpace(command.IdempotencyKey) == "" ||
		!canonicalE164.MatchString(command.Contact.Phone) ||
		strings.TrimSpace(sipDomain) == "" ||
		len(command.Contact.TransferReason) > 500 ||
		len(command.Contact.DisplayName) > 200 {
		return ErrInvalidInput
	}
	return nil
}

func handoffFingerprint(command CreateHandoffCommand) ([32]byte, error) {
	type service struct {
		Subject    string
		PracticeID string
	}
	encoded, err := json.Marshal(struct {
		Service        service
		LocationID     string
		SourceCallID   string
		IdempotencyKey string
		Contact        ContactContext
	}{
		Service:    service{Subject: command.Service.Subject, PracticeID: command.Service.PracticeID},
		LocationID: command.LocationID, SourceCallID: command.SourceCallID,
		IdempotencyKey: command.IdempotencyKey, Contact: command.Contact,
	})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode handoff fingerprint: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

func (m *Module) sipDestination() string {
	return "sip:acuity-handoff@" + m.config.HandoffSIPDomain
}

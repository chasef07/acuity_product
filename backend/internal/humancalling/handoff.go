package humancalling

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	if err := validateHandoff(command, m.config.HandoffSIPDomain); err != nil {
		return Handoff{}, err
	}
	fingerprint, err := handoffFingerprint(command)
	if err != nil {
		return Handoff{}, err
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Handoff{}, fmt.Errorf("begin handoff: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

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
		existing.SIPDestination = m.sipDestination(existing.ID)
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
	routeToken := m.handoffRouteToken(result.ID)
	tokenHash := sha256.Sum256([]byte(routeToken))
	var insertedID string
	err = tx.QueryRow(ctx, `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, token_hash, phone, phone_source,
			display_name, name_source, transfer_reason, reason_source, expires_at,
			created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''),
			NULLIF($13, ''), NULLIF($14, ''), $15, $16
		)
		ON CONFLICT DO NOTHING
		RETURNING id::text
	`, result.ID, command.Service.Subject, command.Service.PracticeID,
		command.LocationID, command.SourceCallID, command.IdempotencyKey,
		fingerprint[:], tokenHash[:], command.Contact.Phone,
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
	result.SIPDestination = m.sipDestination(result.ID)
	return result, nil
}

func (m *Module) resolveHandoffForRefer(
	ctx context.Context,
	tx pgx.Tx,
	fact ProviderFact,
) (string, string, string, error) {
	routeToken, ok := parseHandoffRouteToken(fact.To, m.config.HandoffSIPDomain)
	if !ok {
		return "", "", "", ErrInvalidHandoff
	}
	tokenHash := sha256.Sum256([]byte(routeToken))
	var handoffID, practiceID, locationID string
	err := tx.QueryRow(ctx, `
		SELECT id::text, practice_id::text, location_id::text
		FROM human_calling_handoffs
		WHERE token_hash = $1 AND consumed_at IS NULL
			AND created_at <= $2 AND expires_at > $2
		FOR UPDATE
	`, tokenHash[:], m.now()).Scan(&handoffID, &practiceID, &locationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrInvalidHandoff
	}
	if err != nil {
		return "", "", "", fmt.Errorf("correlate handoff admission: %w", err)
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

func (m *Module) handoffRouteToken(handoffID string) string {
	mac := hmac.New(sha256.New, m.tokenKey)
	_, _ = mac.Write([]byte("handoff-route-v1\x00" + handoffID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Module) sipDestination(handoffID string) string {
	return "sip:h_" + m.handoffRouteToken(handoffID) + "@" + m.config.HandoffSIPDomain
}

func parseHandoffRouteToken(destination string, domain string) (string, bool) {
	if len(destination) >= 256 || !strings.HasPrefix(destination, "sip:h_") {
		return "", false
	}
	user, destinationDomain, ok := strings.Cut(destination[4:], "@")
	if !ok || strings.Contains(destinationDomain, "@") || destinationDomain != domain ||
		!strings.HasPrefix(user, "h_") {
		return "", false
	}
	token := strings.TrimPrefix(user, "h_")
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(token) != 43 || len(decoded) != sha256.Size {
		return "", false
	}
	return token, true
}

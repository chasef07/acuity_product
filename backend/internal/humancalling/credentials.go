package humancalling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (m *Module) ReconcileCredentials(ctx context.Context) error {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin credential reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := m.now()
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_credentials (user_subject, state)
		SELECT user_subject, 'PENDING' FROM access_operational_users
		ON CONFLICT (user_subject) DO UPDATE SET
			provider_credential_id = NULL,
			provider_sip_username = NULL,
			state = 'PENDING', last_error_code = NULL, updated_at = $1
		WHERE human_calling_credentials.state = 'DISABLED'
	`, now); err != nil {
		return fmt.Errorf("discover credential owners: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_credentials credential
		SET state = 'DISABLING', updated_at = $1
		WHERE credential.provider_credential_id IS NOT NULL
			AND credential.state IN ('ACTIVE', 'FAILED')
			AND NOT EXISTS (
				SELECT 1 FROM access_operational_users operational
				WHERE operational.user_subject = credential.user_subject
			)
	`, now); err != nil {
		return fmt.Errorf("mark revoked credentials: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_credentials credential
		SET state = 'DISABLED', last_error_code = NULL, updated_at = $1
		WHERE credential.provider_credential_id IS NULL
			AND credential.state IN ('PENDING', 'FAILED')
			AND NOT EXISTS (
				SELECT 1 FROM access_operational_users operational
				WHERE operational.user_subject = credential.user_subject
			)
	`, now); err != nil {
		return fmt.Errorf("disable absent revoked credentials: %w", err)
	}

	type credentialIntent struct {
		subject string
		action  CommandAction
		target  string
	}
	rows, err := tx.Query(ctx, `
		SELECT credential.user_subject,
			CASE WHEN credential.state = 'PENDING'
				THEN 'CREATE_CREDENTIAL' ELSE 'DISABLE_CREDENTIAL' END,
			COALESCE(credential.provider_credential_id, '')
		FROM human_calling_credentials credential
		WHERE credential.state IN ('PENDING', 'DISABLING')
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_provider_commands command
				WHERE command.user_subject = credential.user_subject
					AND command.action = CASE WHEN credential.state = 'PENDING'
						THEN 'CREATE_CREDENTIAL' ELSE 'DISABLE_CREDENTIAL' END
					AND command.state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
			)
		ORDER BY credential.user_subject
		FOR UPDATE OF credential
	`)
	if err != nil {
		return fmt.Errorf("list credential intents: %w", err)
	}
	intents := []credentialIntent{}
	for rows.Next() {
		var intent credentialIntent
		if err := rows.Scan(&intent.subject, &intent.action, &intent.target); err != nil {
			rows.Close()
			return fmt.Errorf("scan credential intent: %w", err)
		}
		intents = append(intents, intent)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate credential intents: %w", err)
	}
	rows.Close()
	for _, intent := range intents {
		payload := map[string]any{}
		if intent.action == CommandCreateCredential {
			payload = map[string]any{
				"connection_id": m.config.CredentialConnectionID,
				"name":          "acuity-" + opaqueReference(intent.subject),
				"tag":           "acuity-portal",
			}
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode credential command: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_provider_commands (
				id, user_subject, action, target_id, payload, next_attempt_at
			)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)
		`, uuid.NewString(), intent.subject, intent.action, intent.target,
			encoded, now); err != nil {
			return fmt.Errorf("commit credential intent: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit credential reconciliation: %w", err)
	}
	return nil
}

func (m *Module) ProcessNextCredentialReconciliation(
	ctx context.Context,
) (bool, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin credential state reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recoveryNow := m.now()
	var interruptedCommandID string
	err = tx.QueryRow(ctx, `
		SELECT command.id::text
		FROM human_calling_provider_commands command
		WHERE command.call_id IS NULL
			AND command.action IN ('CREATE_CREDENTIAL', 'DISABLE_CREDENTIAL')
			AND command.state = 'SENDING'
			AND command.updated_at <= $1::timestamptz - interval '30 seconds'
		ORDER BY command.updated_at, command.id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, recoveryNow).Scan(&interruptedCommandID)
	recovered := false
	if err == nil {
		recovered, err = m.recoverInterruptedCommandOwnership(
			ctx, tx, credentialCommandOwner, interruptedCommandID, recoveryNow,
		)
	} else if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return false, fmt.Errorf("recover interrupted credential commands: %w", err)
	}
	provider, canObserve := m.provider.(CredentialStateProvider)
	if !canObserve {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit interrupted credential recovery: %w", err)
		}
		return recovered, nil
	}
	var command ProviderCommand
	var subject string
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT id::text, user_subject, action, COALESCE(target_id, ''), created_at
		FROM human_calling_provider_commands
		WHERE state = 'AMBIGUOUS'
			AND action IN ('CREATE_CREDENTIAL', 'DISABLE_CREDENTIAL')
			AND next_attempt_at <= $1
		ORDER BY updated_at, id
		FOR UPDATE SKIP LOCKED LIMIT 1
	`, recoveryNow).Scan(
		&command.ID, &subject, &command.Action, &command.TargetID, &createdAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return recovered, tx.Commit(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("claim credential reconciliation: %w", err)
	}
	if !createdAt.After(m.now().Add(-credentialRetryLifetime)) {
		now := m.now()
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET state = 'FAILED', last_error_code = $2, updated_at = $3
			WHERE id = $1 AND state = 'AMBIGUOUS'
		`, command.ID, credentialRetryExhaustedCode, now); err != nil {
			return true, fmt.Errorf("quarantine exhausted credential command: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_credentials
			SET state = 'FAILED', last_error_code = $2, updated_at = $3
			WHERE user_subject = $1
		`, subject, credentialRetryExhaustedCode, now); err != nil {
			return true, fmt.Errorf("quarantine exhausted Staff credential: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return true, fmt.Errorf("commit exhausted credential quarantine: %w", err)
		}
		return true, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'SENDING', last_error_code = 'PROVIDER_STATE_QUERY', updated_at = $2
		WHERE id = $1
	`, command.ID, m.now()); err != nil {
		return false, fmt.Errorf("mark credential state query: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit credential state query: %w", err)
	}

	result, found, lookupErr := provider.FindCredentialByName(
		ctx, "acuity-"+opaqueReference(subject),
	)
	tx, err = m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, fmt.Errorf("begin credential state result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if lookupErr != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET state = 'AMBIGUOUS', last_error_code = 'PROVIDER_STATE_UNAVAILABLE',
				next_attempt_at = $2::timestamptz + interval '5 seconds', updated_at = $2
			WHERE id = $1
		`, command.ID, m.now()); err != nil {
			return true, err
		}
		if err := tx.Commit(ctx); err != nil {
			return true, err
		}
		return true, fmt.Errorf("query provider credential state: %w", lookupErr)
	}
	var authorized bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM access_operational_users WHERE user_subject = $1
		)
	`, subject).Scan(&authorized); err != nil {
		return true, fmt.Errorf("read credential owner state: %w", err)
	}
	now := m.now()
	switch command.Action {
	case CommandCreateCredential:
		if found && authorized {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_credentials SET state = 'ACTIVE',
					provider_credential_id = $2, provider_sip_username = $3,
					last_error_code = NULL, updated_at = $4
				WHERE user_subject = $1
			`, subject, result.CredentialID, result.SIPUsername, now); err != nil {
				return true, err
			}
			_, err = tx.Exec(ctx, `
				UPDATE human_calling_provider_commands
				SET state = 'RECONCILED', last_error_code = NULL, updated_at = $2
				WHERE id = $1
			`, command.ID, now)
		} else if found {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_credentials SET state = 'DISABLING',
					provider_credential_id = $2, provider_sip_username = $3,
					updated_at = $4 WHERE user_subject = $1
			`, subject, result.CredentialID, result.SIPUsername, now); err != nil {
				return true, err
			}
			_, err = tx.Exec(ctx, `
				UPDATE human_calling_provider_commands
				SET state = 'RECONCILED', last_error_code = 'ACCESS_OBSOLETE_AFTER_CREATE',
					updated_at = $2 WHERE id = $1
			`, command.ID, now)
		} else {
			state, code := "PENDING", "PROVIDER_STATE_ABSENT"
			if !authorized {
				state, code = "FAILED", "ACCESS_OBSOLETE"
			}
			_, err = tx.Exec(ctx, `
				UPDATE human_calling_provider_commands SET state = $2,
					last_error_code = $3, next_attempt_at = $4, updated_at = $4
				WHERE id = $1
			`, command.ID, state, code, now)
		}
	case CommandDisableCredential:
		if authorized {
			_, err = tx.Exec(ctx, `
				UPDATE human_calling_provider_commands SET state = 'RECONCILED',
					last_error_code = 'MEMBERSHIP_REAUTHORIZED', updated_at = $2
				WHERE id = $1
			`, command.ID, now)
		} else if found {
			_, err = tx.Exec(ctx, `
				UPDATE human_calling_provider_commands SET state = 'PENDING',
					last_error_code = 'PROVIDER_STATE_PRESENT', next_attempt_at = $2,
					updated_at = $2 WHERE id = $1
			`, command.ID, now)
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_credentials SET state = 'DISABLED',
					last_error_code = NULL, updated_at = $2 WHERE user_subject = $1
			`, subject, now); err != nil {
				return true, err
			}
			_, err = tx.Exec(ctx, `
				UPDATE human_calling_provider_commands SET state = 'RECONCILED',
					last_error_code = NULL, updated_at = $2 WHERE id = $1
			`, command.ID, now)
		}
	}
	if err != nil {
		return true, fmt.Errorf("record credential reconciliation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit credential reconciliation result: %w", err)
	}
	return true, nil
}

func (m *Module) IssueMediaJWT(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
) (MediaToken, error) {
	if sessionID == "" || identity.Subject == "" {
		return MediaToken{}, ErrDenied
	}
	credentialID, err := m.authorizeMediaJWT(ctx, identity, sessionID, "")
	if err != nil {
		return MediaToken{}, err
	}
	command := ProviderCommand{
		ID: uuid.NewString(), Action: CommandCreateJWT,
		TargetID: credentialID, Payload: map[string]any{},
	}
	if _, err := m.database.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			id, user_subject, action, target_id, payload, next_attempt_at
		) VALUES ($1, $2, 'CREATE_JWT', $3, '{}'::jsonb, $4)
	`, command.ID, identity.Subject, credentialID, m.now()); err != nil {
		return MediaToken{}, fmt.Errorf("commit media JWT command: %w", err)
	}
	result, err := m.processCommand(ctx, command.ID)
	if err != nil {
		return MediaToken{}, err
	}
	if result.JWT == "" || !result.JWTExpiresAt.After(m.now()) ||
		result.JWTExpiresAt.After(m.now().Add(29*24*time.Hour)) {
		return MediaToken{}, fmt.Errorf("provider returned an invalid media JWT")
	}
	if _, err := m.authorizeMediaJWT(ctx, identity, sessionID, credentialID); err != nil {
		return MediaToken{}, err
	}
	return MediaToken{Token: result.JWT, ExpiresAt: result.JWTExpiresAt}, nil
}

func (m *Module) authorizeMediaJWT(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
	expectedCredentialID string,
) (string, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin media JWT authorization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := m.access.LockOperationalActor(ctx, tx, identity); err != nil {
		return "", ErrDenied
	}
	var leaseCurrent bool
	err = tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1 FOR SHARE
	`, identity.Subject, sessionID, m.now()).Scan(&leaseCurrent)
	if errors.Is(err, pgx.ErrNoRows) || !leaseCurrent {
		return "", ErrDenied
	}
	if err != nil {
		return "", fmt.Errorf("verify media lease: %w", err)
	}
	var credentialID string
	err = tx.QueryRow(ctx, `
		SELECT provider_credential_id FROM human_calling_credentials
		WHERE user_subject = $1 AND state = 'ACTIVE' FOR SHARE
	`, identity.Subject).Scan(&credentialID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrConflict
	}
	if err != nil {
		return "", fmt.Errorf("load active media credential: %w", err)
	}
	if expectedCredentialID != "" && credentialID != expectedCredentialID {
		return "", ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit media JWT authorization: %w", err)
	}
	return credentialID, nil
}

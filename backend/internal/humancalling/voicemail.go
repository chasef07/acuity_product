package humancalling

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RecoveryOutcome string

const (
	RecoveryVoicemail  RecoveryOutcome = "VOICEMAIL"
	RecoveryMissedCall RecoveryOutcome = "MISSED_CALL"
)

type VoicemailAudioState string

const (
	VoicemailProcessing       VoicemailAudioState = "PROCESSING"
	VoicemailReady            VoicemailAudioState = "READY"
	VoicemailUnavailable      VoicemailAudioState = "UNAVAILABLE"
	voicemailRecordingMaximum                     = 120 * time.Second
	voicemailCallbackGrace                        = 30 * time.Second
)

type Voicemail struct {
	Outcome         RecoveryOutcome
	AudioState      VoicemailAudioState
	TaskID          string
	DurationSeconds int64
}

type PlaybackCapability struct {
	Token     string
	ExpiresAt time.Time
}

type PlaybackContent struct {
	ContentType string
	Content     []byte
}

type VoicemailObjectStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
}

type RecordingDownloader interface {
	Download(context.Context, string) ([]byte, string, error)
}

type HTTPRecordingDownloader struct {
	client       *http.Client
	allowedHosts map[string]struct{}
}

func NewHTTPRecordingDownloader(
	client *http.Client,
	allowedHosts ...string,
) *HTTPRecordingDownloader {
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	if clientCopy.Timeout <= 0 {
		clientCopy.Timeout = 10 * time.Second
	}
	hosts := map[string]struct{}{}
	for _, host := range allowedHosts {
		host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if host != "" {
			hosts[host] = struct{}{}
		}
	}
	if len(hosts) == 0 {
		hosts["s3.amazonaws.com"] = struct{}{}
	}
	clientCopy.CheckRedirect = func(
		request *http.Request,
		via []*http.Request,
	) error {
		if len(via) >= 3 {
			return errors.New("provider recording redirected too many times")
		}
		if err := validateRecordingLocation(request.URL, hosts); err != nil {
			return err
		}
		return nil
	}
	return &HTTPRecordingDownloader{
		client:       &clientCopy,
		allowedHosts: hosts,
	}
}

func (downloader *HTTPRecordingDownloader) Download(
	ctx context.Context,
	recordingURL string,
) ([]byte, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(recordingURL))
	if err != nil {
		return nil, "", errors.New("invalid provider recording location")
	}
	if err := validateRecordingLocation(parsed, downloader.allowedHosts); err != nil {
		return nil, "", errors.New("invalid provider recording location")
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		parsed.String(),
		nil,
	)
	if err != nil {
		return nil, "", fmt.Errorf("create recording copy request: %w", err)
	}
	response, err := downloader.client.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("copy provider recording: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", errors.New("provider recording copy was rejected")
	}
	const maximumVoicemailBytes = 25 << 20
	content, err := io.ReadAll(
		io.LimitReader(response.Body, maximumVoicemailBytes+1),
	)
	if err != nil {
		return nil, "", fmt.Errorf("read provider recording: %w", err)
	}
	if len(content) == 0 || len(content) > maximumVoicemailBytes {
		return nil, "", errors.New("provider recording size is invalid")
	}
	contentType := strings.ToLower(strings.TrimSpace(
		strings.Split(response.Header.Get("Content-Type"), ";")[0],
	))
	return content, contentType, nil
}

func validateRecordingLocation(
	location *url.URL,
	allowedHosts map[string]struct{},
) error {
	if location == nil ||
		!strings.EqualFold(location.Scheme, "https") ||
		location.User != nil ||
		location.Host == "" {
		return errors.New("invalid provider recording location")
	}
	host := strings.ToLower(strings.TrimSuffix(location.Hostname(), "."))
	endpoint := host
	if location.Port() != "" {
		endpoint = net.JoinHostPort(host, location.Port())
	}
	if _, ok := allowedHosts[endpoint]; !ok {
		return errors.New("provider recording host is not allowed")
	}
	if address := net.ParseIP(host); address != nil &&
		(address.IsPrivate() ||
			address.IsLoopback() ||
			address.IsLinkLocalUnicast() ||
			address.IsUnspecified() ||
			address.IsMulticast()) {
		return errors.New("provider recording address is not public")
	}
	return nil
}

type playbackClaims struct {
	CallID    string `json:"callId"`
	ExpiresAt int64  `json:"expiresAt"`
	Nonce     string `json:"nonce"`
}

func (m *Module) voicemailGreeting(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationID string,
) string {
	var configured string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(voicemail_greeting_url, '')
		FROM human_calling_location_voice_numbers
		WHERE practice_id = $1
			AND location_id = $2
			AND enabled
		ORDER BY id
		LIMIT 1
	`, practiceID, locationID).Scan(&configured)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return m.config.SafeGreetingURL
	}
	configured = strings.TrimSpace(configured)
	parsed, err := url.Parse(configured)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return m.config.SafeGreetingURL
	}
	return configured
}

func (m *Module) startVoicemailGreeting(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	practiceID string,
	locationID string,
	callerCallControlID string,
	occurredAt time.Time,
) error {
	greetingURL := m.voicemailGreeting(ctx, tx, practiceID, locationID)
	if strings.TrimSpace(greetingURL) == "" {
		return fmt.Errorf("%w: safe voicemail greeting is unavailable", ErrConflict)
	}
	return insertCommand(
		ctx,
		tx,
		callID,
		"",
		CommandPlayVoicemailGreeting,
		callerCallControlID,
		map[string]any{
			"audio_url":    greetingURL,
			"client_state": opaqueClientState(callID, "voicemail"),
		},
		occurredAt,
	)
}

func (m *Module) applyVoicemailGreetingEnded(
	ctx context.Context,
	fact ProviderFact,
) error {
	state, ok := parseOpaqueClientState(fact.ClientState)
	if !ok || state.Version != 1 || state.Leg != "voicemail" {
		return nil
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin voicemail greeting projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}
	var practiceID, callerControlID, callerLegID, callSessionID string
	var callState CallState
	if err := tx.QueryRow(ctx, `
		SELECT
			practice_id::text,
			state,
			caller_call_control_id,
			caller_call_leg_id,
			call_session_id
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, state.CallID).Scan(
		&practiceID,
		&callState,
		&callerControlID,
		&callerLegID,
		&callSessionID,
	); err != nil {
		return fmt.Errorf("lock voicemail greeting Call: %w", err)
	}
	if !matchesVoicemailCaller(
		fact,
		callerControlID,
		callerLegID,
		callSessionID,
	) {
		return tx.Commit(ctx)
	}
	if callState != CallUnanswered {
		return tx.Commit(ctx)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM human_calling_provider_commands
			WHERE call_id = $1
				AND action = 'START_VOICEMAIL_RECORDING'
		)
	`, state.CallID).Scan(&exists); err != nil {
		return fmt.Errorf("check voicemail recording intent: %w", err)
	}
	if !exists {
		if err := insertCommand(
			ctx,
			tx,
			state.CallID,
			"",
			CommandStartVoicemailRecording,
			callerControlID,
			map[string]any{
				"format":           "wav",
				"channels":         "single",
				"recording_track":  "inbound",
				"play_beep":        true,
				"max_length":       120,
				"transcription":    false,
				"custom_file_name": "voicemail-" + strings.ReplaceAll(state.CallID, "-", ""),
				"client_state":     opaqueClientState(state.CallID, "voicemail"),
			},
			m.now(),
		); err != nil {
			return err
		}
	}
	if err := appendTimeline(
		ctx,
		tx,
		state.CallID,
		practiceID,
		"voicemail.greeting_completed",
		"",
		fact.EventID,
		"",
		"",
		"",
		fact.OccurredAt,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit voicemail greeting projection: %w", err)
	}
	return nil
}

func (m *Module) applyVoicemailRecordingSaved(
	ctx context.Context,
	fact ProviderFact,
	callID string,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin voicemail recording projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}
	hasArtifact := fact.RecordingEndedAt.After(fact.RecordingStartedAt) &&
		fact.RecordingID != "" &&
		fact.RecordingURL != ""
	var practiceID, callerControlID, callerLegID, callSessionID string
	if err := tx.QueryRow(ctx, `
		SELECT
			practice_id::text,
			caller_call_control_id,
			caller_call_leg_id,
			call_session_id
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, callID).Scan(
		&practiceID,
		&callerControlID,
		&callerLegID,
		&callSessionID,
	); err != nil {
		return fmt.Errorf("lock voicemail recording Call: %w", err)
	}
	if !matchesVoicemailCaller(
		fact,
		callerControlID,
		callerLegID,
		callSessionID,
	) {
		return tx.Commit(ctx)
	}
	outcome := RecoveryMissedCall
	if hasArtifact {
		outcome = RecoveryVoicemail
	}
	if _, err := m.ensureRecoveryOutcome(
		ctx,
		tx,
		callID,
		outcome,
		fact,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			voicemail_failure_deadline = NULL,
			voicemail_failure_event_id = NULL
		WHERE id = $1
	`, callID); err != nil {
		return fmt.Errorf("clear superseded voicemail failure: %w", err)
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit voicemail recording projection: %w", err)
	}
	return nil
}

func (m *Module) applyVoicemailRecordingError(
	ctx context.Context,
	fact ProviderFact,
	callID string,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin voicemail recording failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}
	var practiceID, callerControlID, callerLegID, callSessionID string
	if err := tx.QueryRow(ctx, `
		SELECT
			practice_id::text,
			caller_call_control_id,
			caller_call_leg_id,
			call_session_id
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, callID).Scan(
		&practiceID,
		&callerControlID,
		&callerLegID,
		&callSessionID,
	); err != nil {
		return fmt.Errorf("lock failed voicemail Call: %w", err)
	}
	if !matchesVoicemailCaller(
		fact,
		callerControlID,
		callerLegID,
		callSessionID,
	) {
		return tx.Commit(ctx)
	}
	if err := m.deferVoicemailFailure(
		ctx,
		tx,
		callID,
		fact.EventID,
		m.now(),
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit voicemail recording failure: %w", err)
	}
	return nil
}

func (m *Module) deferVoicemailFailure(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	eventID string,
	now time.Time,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			voicemail_failure_event_id = COALESCE(
				voicemail_failure_event_id,
				$2
			),
			voicemail_failure_deadline = COALESCE(
				voicemail_failure_deadline,
				$3
			),
			version = version + 1,
			updated_at = $4
		WHERE id = $1
			AND direction = 'INBOUND'
			AND state = 'UNANSWERED'
	`, callID, eventID, now.Add(voicemailCallbackGrace), now)
	if err != nil {
		return fmt.Errorf("defer voicemail failure outcome: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	return nil
}

func (m *Module) watchVoicemailRecording(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	eventID string,
	now time.Time,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			voicemail_failure_deadline = $3,
			voicemail_failure_event_id = $2,
			version = version + 1,
			updated_at = $4
		WHERE id = $1
			AND direction = 'INBOUND'
			AND state = 'UNANSWERED'
	`, callID, eventID,
		now.Add(voicemailRecordingMaximum+voicemailCallbackGrace), now)
	if err != nil {
		return fmt.Errorf("watch voicemail recording callback: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	return nil
}

func matchesVoicemailCaller(
	fact ProviderFact,
	callControlID string,
	callLegID string,
	callSessionID string,
) bool {
	return fact.CallControlID == callControlID &&
		fact.CallLegID == callLegID &&
		fact.CallSessionID == callSessionID
}

func (m *Module) finalizeVoicemailFailure(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	eventID string,
	occurredAt time.Time,
) error {
	var state CallState
	if err := tx.QueryRow(ctx, `
		SELECT state
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, callID).Scan(&state); err != nil {
		return fmt.Errorf("lock voicemail failure Call: %w", err)
	}
	if state != CallUnanswered {
		return nil
	}
	_, err := m.ensureRecoveryOutcome(
		ctx,
		tx,
		callID,
		RecoveryMissedCall,
		ProviderFact{
			EventID:    eventID,
			OccurredAt: occurredAt,
		},
	)
	return err
}

func (m *Module) ExpireVoicemailFailures(
	ctx context.Context,
) (int, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin voicemail failure expiry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT id::text, voicemail_failure_event_id, voicemail_failure_deadline
		FROM human_calling_calls
		WHERE state = 'UNANSWERED'
			AND voicemail_failure_deadline <= $1
		ORDER BY voicemail_failure_deadline, id
		FOR UPDATE
	`, m.now())
	if err != nil {
		return 0, fmt.Errorf("list expired voicemail failures: %w", err)
	}
	type expiredFailure struct {
		callID     string
		eventID    string
		occurredAt time.Time
	}
	expired := []expiredFailure{}
	for rows.Next() {
		var failure expiredFailure
		if err := rows.Scan(
			&failure.callID,
			&failure.eventID,
			&failure.occurredAt,
		); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired voicemail failure: %w", err)
		}
		expired = append(expired, failure)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired voicemail failures: %w", err)
	}
	rows.Close()
	for _, failure := range expired {
		if err := m.finalizeVoicemailFailure(
			ctx,
			tx,
			failure.callID,
			failure.eventID,
			failure.occurredAt,
		); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				voicemail_failure_deadline = NULL,
				voicemail_failure_event_id = NULL
			WHERE id = $1
		`, failure.callID); err != nil {
			return 0, fmt.Errorf("clear expired voicemail failure: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit voicemail failure expiry: %w", err)
	}
	return len(expired), nil
}

func (m *Module) ensureRecoveryOutcome(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	outcome RecoveryOutcome,
	fact ProviderFact,
) (string, error) {
	if m.work == nil {
		return "", ErrInvalidInput
	}
	var practiceID, locationID, phone, callerName string
	if err := tx.QueryRow(ctx, `
		SELECT
			call.practice_id::text,
			call.location_id::text,
			COALESCE(handoff.phone, ''),
			COALESCE(handoff.display_name, '')
		FROM human_calling_calls call
		JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id
		WHERE call.id = $1
		FOR UPDATE OF call
	`, callID).Scan(
		&practiceID,
		&locationID,
		&phone,
		&callerName,
	); err != nil {
		return "", fmt.Errorf("lock recovery Call: %w", err)
	}
	workOutcome := work.RecoveryOutcomeMissedCall
	nextState := CallMissed
	if outcome == RecoveryVoicemail {
		workOutcome = work.RecoveryOutcomeVoicemail
		nextState = CallVoicemail
	}
	task, err := m.work.EnsureRecoveryTask(
		ctx,
		tx,
		work.EnsureRecoveryTaskCommand{
			CallID:     callID,
			PracticeID: practiceID,
			LocationID: locationID,
			Phone:      phone,
			CallerName: callerName,
			Outcome:    workOutcome,
			OccurredAt: fact.OccurredAt,
		},
	)
	if err != nil {
		return "", err
	}
	switch outcome {
	case RecoveryVoicemail:
		duration := fact.RecordingEndedAt.Sub(fact.RecordingStartedAt)
		if fact.RecordingID == "" ||
			fact.RecordingURL == "" ||
			!fact.RecordingEndedAt.After(fact.RecordingStartedAt) {
			return "", ErrInvalidInput
		}
		objectKey := "voicemails/" + callID + ".wav"
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_voicemails (
				call_id,
				practice_id,
				location_id,
				task_id,
				outcome,
				audio_state,
				provider_recording_id,
				provider_recording_url,
				recording_started_at,
				recording_ended_at,
				duration_millis,
				object_key,
				next_copy_at,
				created_at,
				updated_at
			)
			VALUES (
				$1, $2, $3, $4, 'VOICEMAIL', 'PROCESSING', $5, $6,
				$7, $8, $9, $10, $11, $11, $11
			)
			ON CONFLICT (call_id) DO NOTHING
		`, callID, practiceID, locationID, task.ID, fact.RecordingID,
			fact.RecordingURL, fact.RecordingStartedAt, fact.RecordingEndedAt,
			duration.Milliseconds(), objectKey, m.now(),
		); err != nil {
			return "", fmt.Errorf("commit voicemail source: %w", err)
		}
	case RecoveryMissedCall:
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_voicemails (
				call_id,
				practice_id,
				location_id,
				task_id,
				outcome,
				created_at,
				updated_at
			)
			VALUES ($1, $2, $3, $4, 'MISSED_CALL', $5, $5)
			ON CONFLICT (call_id) DO NOTHING
		`, callID, practiceID, locationID, task.ID, m.now()); err != nil {
			return "", fmt.Errorf("commit missed-call source: %w", err)
		}
	default:
		return "", ErrInvalidInput
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			state = $2,
			ended_at = COALESCE(ended_at, $3),
			version = version + 1,
			updated_at = $3
		WHERE id = $1
			AND state NOT IN ('VOICEMAIL', 'MISSED')
	`, callID, nextState, fact.OccurredAt); err != nil {
		return "", fmt.Errorf("commit recovery Call outcome: %w", err)
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"call.recovery_task_created",
		"",
		fact.EventID,
		"",
		opaqueReference(task.ID),
		string(outcome),
		fact.OccurredAt,
	); err != nil {
		return "", err
	}
	return task.ID, nil
}

func (m *Module) ProcessNextVoicemailCopy(ctx context.Context) (bool, error) {
	if m.config.VoicemailStore == nil || m.config.RecordingDownloader == nil {
		return false, nil
	}
	claimTx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin voicemail copy: %w", err)
	}
	defer func() { _ = claimTx.Rollback(ctx) }()
	var callID, practiceID, providerURL, objectKey string
	var attempts int
	err = claimTx.QueryRow(ctx, `
		SELECT
			call_id::text,
			practice_id::text,
			provider_recording_url,
			object_key,
			copy_attempts
		FROM human_calling_voicemails
		WHERE audio_state = 'PROCESSING'
			AND next_copy_at <= $1
		ORDER BY next_copy_at, created_at, call_id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, m.now()).Scan(
		&callID,
		&practiceID,
		&providerURL,
		&objectKey,
		&attempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, claimTx.Commit(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("claim voicemail copy: %w", err)
	}
	attempts++
	claimExpiresAt := m.now().Add(5 * time.Minute)
	if _, err := claimTx.Exec(ctx, `
		UPDATE human_calling_voicemails
		SET
			copy_attempts = $2,
			next_copy_at = $3,
			updated_at = $4
		WHERE call_id = $1
			AND audio_state = 'PROCESSING'
	`, callID, attempts, claimExpiresAt, m.now()); err != nil {
		return false, fmt.Errorf("lease voicemail copy: %w", err)
	}
	if err := claimTx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit voicemail copy claim: %w", err)
	}

	content, contentType, copyErr := m.config.RecordingDownloader.Download(
		ctx,
		providerURL,
	)
	if copyErr == nil {
		contentType = strings.ToLower(strings.TrimSpace(contentType))
		if contentType != "audio/wav" || len(content) == 0 {
			copyErr = errors.New("invalid voicemail recording")
		}
	}
	if copyErr == nil {
		copyErr = m.config.VoicemailStore.Put(ctx, objectKey, content)
	}
	finalizeTx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin voicemail copy finalization: %w", err)
	}
	defer func() { _ = finalizeTx.Rollback(ctx) }()
	finalized := false
	if copyErr != nil {
		if attempts >= 3 {
			tag, err := finalizeTx.Exec(ctx, `
				UPDATE human_calling_voicemails
				SET
					audio_state = 'UNAVAILABLE',
					provider_recording_url = NULL,
					next_copy_at = NULL,
					last_error_code = 'COPY_FAILED',
					updated_at = $4
				WHERE call_id = $1
					AND audio_state = 'PROCESSING'
					AND copy_attempts = $2
					AND next_copy_at = $3
			`, callID, attempts, claimExpiresAt, m.now())
			if err != nil {
				return false, fmt.Errorf("finalize unavailable voicemail: %w", err)
			}
			finalized = tag.RowsAffected() == 1
		} else {
			tag, err := finalizeTx.Exec(ctx, `
				UPDATE human_calling_voicemails
				SET
					next_copy_at = $4,
					last_error_code = 'COPY_FAILED',
					updated_at = $5
				WHERE call_id = $1
					AND audio_state = 'PROCESSING'
					AND copy_attempts = $2
					AND next_copy_at = $3
			`, callID, attempts, claimExpiresAt,
				m.now().Add(time.Duration(attempts)*time.Minute), m.now())
			if err != nil {
				return false, fmt.Errorf("schedule voicemail copy retry: %w", err)
			}
			finalized = tag.RowsAffected() == 1
		}
	} else {
		tag, err := finalizeTx.Exec(ctx, `
			UPDATE human_calling_voicemails
			SET
				audio_state = 'READY',
				provider_recording_url = NULL,
				content_type = $4,
				byte_size = $5,
				next_copy_at = NULL,
				last_error_code = NULL,
				copied_at = $6,
				updated_at = $6
			WHERE call_id = $1
				AND audio_state = 'PROCESSING'
				AND copy_attempts = $2
				AND next_copy_at = $3
		`, callID, attempts, claimExpiresAt, contentType, len(content),
			m.now())
		if err != nil {
			return false, fmt.Errorf("finalize voicemail copy: %w", err)
		}
		finalized = tag.RowsAffected() == 1
	}
	if !finalized {
		if err := finalizeTx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit stale voicemail copy: %w", err)
		}
		return true, nil
	}
	if _, err := m.access.RecordWorkspaceChange(
		ctx,
		finalizeTx,
		practiceID,
	); err != nil {
		return false, err
	}
	if err := finalizeTx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit voicemail copy: %w", err)
	}
	return true, nil
}

func (m *Module) IssueVoicemailPlayback(
	ctx context.Context,
	identity access.Identity,
	callID string,
) (PlaybackCapability, error) {
	call, err := m.ReadCall(ctx, identity, callID)
	if err != nil {
		return PlaybackCapability{}, err
	}
	if call.Voicemail.AudioState != VoicemailReady {
		return PlaybackCapability{}, ErrConflict
	}
	expiresAt := m.now().Add(5 * time.Minute)
	raw, err := json.Marshal(playbackClaims{
		CallID:    callID,
		ExpiresAt: expiresAt.Unix(),
		Nonce:     uuid.NewString(),
	})
	if err != nil {
		return PlaybackCapability{}, fmt.Errorf("encode playback capability: %w", err)
	}
	mac := hmac.New(sha256.New, m.playbackKey)
	_, _ = mac.Write(raw)
	token := base64.RawURLEncoding.EncodeToString(raw) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return PlaybackCapability{Token: token, ExpiresAt: expiresAt}, nil
}

func (m *Module) OpenVoicemailPlayback(
	ctx context.Context,
	identity access.Identity,
	token string,
) (PlaybackContent, error) {
	claims, err := m.parsePlaybackCapability(token)
	if err != nil {
		return PlaybackContent{}, ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PlaybackContent{}, fmt.Errorf("begin voicemail playback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID, objectKey, contentType string
	if err := tx.QueryRow(ctx, `
		SELECT
			practice_id::text,
			location_id::text,
			object_key,
			content_type
		FROM human_calling_voicemails
		WHERE call_id = $1 AND audio_state = 'READY'
	`, claims.CallID).Scan(
		&practiceID,
		&locationID,
		&objectKey,
		&contentType,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlaybackContent{}, ErrDenied
		}
		return PlaybackContent{}, fmt.Errorf("read voicemail playback source: %w", err)
	}
	if _, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		identity,
		practiceID,
		locationID,
	); err != nil {
		return PlaybackContent{}, ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return PlaybackContent{}, fmt.Errorf("commit voicemail playback: %w", err)
	}
	content, err := m.config.VoicemailStore.Get(ctx, objectKey)
	if err != nil {
		return PlaybackContent{}, fmt.Errorf("open voicemail object: %w", err)
	}
	return PlaybackContent{ContentType: contentType, Content: content}, nil
}

func (m *Module) parsePlaybackCapability(token string) (playbackClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return playbackClaims{}, ErrDenied
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return playbackClaims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return playbackClaims{}, err
	}
	mac := hmac.New(sha256.New, m.playbackKey)
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return playbackClaims{}, ErrDenied
	}
	var claims playbackClaims
	if err := json.Unmarshal(raw, &claims); err != nil ||
		claims.CallID == "" ||
		claims.Nonce == "" ||
		!m.now().Before(time.Unix(claims.ExpiresAt, 0)) {
		return playbackClaims{}, ErrDenied
	}
	return claims, nil
}

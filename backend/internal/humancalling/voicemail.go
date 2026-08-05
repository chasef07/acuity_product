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
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	defaultVoicemailGreeting                      = "Please leave a message after the beep."
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
	StatusCode    int
	ContentType   string
	ContentLength string
	ContentRange  string
	Body          io.ReadCloser
	completion    *playbackCompletion
}

type playbackCompletion struct {
	once sync.Once
	fn   func(error)
}

func (content PlaybackContent) Complete(err error) {
	if content.completion != nil {
		content.completion.once.Do(func() {
			content.completion.fn(err)
		})
	}
}

func (content PlaybackContent) Validate(rangeHeader string) error {
	requested, requestedRange := parseSingleByteRange(rangeHeader)
	if strings.TrimSpace(rangeHeader) != "" && !requestedRange {
		return voicemailUnavailable(VoicemailProviderInvalid, "")
	}
	if content.Body == nil ||
		(content.StatusCode != http.StatusOK &&
			content.StatusCode != http.StatusPartialContent) {
		return voicemailUnavailable(VoicemailProviderInvalid, "")
	}
	if content.StatusCode == http.StatusPartialContent &&
		(!requestedRange ||
			!matchesContentRange(requested, content.ContentRange)) {
		return voicemailUnavailable(VoicemailProviderInvalid, "")
	}
	return nil
}

type byteRange struct {
	start    uint64
	end      uint64
	hasStart bool
	hasEnd   bool
}

func parseSingleByteRange(value string) (byteRange, bool) {
	value = strings.TrimSpace(value)
	if len(value) > 128 || !strings.HasPrefix(value, "bytes=") ||
		strings.Contains(value, ",") {
		return byteRange{}, false
	}
	bounds := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(bounds) != 2 || (bounds[0] == "" && bounds[1] == "") {
		return byteRange{}, false
	}
	result := byteRange{}
	var err error
	if bounds[0] != "" {
		result.start, err = strconv.ParseUint(bounds[0], 10, 64)
		if err != nil {
			return byteRange{}, false
		}
		result.hasStart = true
	}
	if bounds[1] != "" {
		result.end, err = strconv.ParseUint(bounds[1], 10, 64)
		if err != nil || (!result.hasStart && result.end == 0) {
			return byteRange{}, false
		}
		result.hasEnd = true
	}
	if result.hasStart && result.hasEnd && result.end < result.start {
		return byteRange{}, false
	}
	return result, true
}

func matchesContentRange(requested byteRange, value string) bool {
	value = strings.TrimSpace(value)
	if len(value) > 128 || !strings.HasPrefix(value, "bytes ") {
		return false
	}
	rangeAndLength := strings.Split(strings.TrimPrefix(value, "bytes "), "/")
	if len(rangeAndLength) != 2 {
		return false
	}
	bounds := strings.Split(rangeAndLength[0], "-")
	if len(bounds) != 2 {
		return false
	}
	start, startErr := strconv.ParseUint(bounds[0], 10, 64)
	end, endErr := strconv.ParseUint(bounds[1], 10, 64)
	length, lengthErr := strconv.ParseUint(rangeAndLength[1], 10, 64)
	if startErr != nil || endErr != nil || lengthErr != nil ||
		start > end || end >= length {
		return false
	}
	if requested.hasStart {
		return start == requested.start &&
			(!requested.hasEnd || end <= requested.end)
	}
	return requested.hasEnd && end == length-1 &&
		end-start+1 <= requested.end
}

type VoicemailAudioProvider interface {
	OpenVoicemailRecording(context.Context, string, string) (PlaybackContent, error)
}

type VoicemailUnavailableReason string

const (
	VoicemailRecordingNotFound   VoicemailUnavailableReason = "recording_not_found"
	VoicemailProviderAuth        VoicemailUnavailableReason = "provider_auth"
	VoicemailProviderRateLimited VoicemailUnavailableReason = "provider_rate_limited"
	VoicemailProviderTimeout     VoicemailUnavailableReason = "provider_timeout"
	VoicemailProviderUnavailable VoicemailUnavailableReason = "provider_unavailable"
	VoicemailProviderInvalid     VoicemailUnavailableReason = "provider_invalid_response"
	VoicemailRecordingURLExpired VoicemailUnavailableReason = "recording_url_expired"
)

type VoicemailUnavailableError struct {
	Reason     VoicemailUnavailableReason
	RetryAfter string
}

func (err *VoicemailUnavailableError) Error() string {
	return "voicemail is unavailable"
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
) (string, error) {
	var configured string
	err := tx.QueryRow(ctx, `
		SELECT voicemail_greeting
		FROM human_calling_location_voice_numbers
		WHERE practice_id = $1
			AND location_id = $2
			AND enabled
		ORDER BY id
		LIMIT 1
	`, practiceID, locationID).Scan(&configured)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultVoicemailGreeting, nil
	}
	if err != nil {
		return "", fmt.Errorf("read voicemail greeting: %w", err)
	}
	return configured, nil
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
	greeting, err := m.voicemailGreeting(ctx, tx, practiceID, locationID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(greeting) == "" {
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
			"greeting": greeting,
			// Telnyx stops the infinite ringback and starts the greeting as one
			// idempotent command, so callers never hear both audio streams.
			"stop":         "all",
			"client_state": opaqueClientState(callID, "voicemail"),
		},
		occurredAt,
	)
}

func (m *Module) applyVoicemailGreetingStarted(
	ctx context.Context,
	fact ProviderFact,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin voicemail greeting start projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := claimProviderFact(ctx, tx, fact, m.now()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit voicemail greeting start: %w", err)
	}
	return nil
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
				"format":           "mp3",
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
	hasArtifact := fact.RecordingEndedAt.Sub(fact.RecordingStartedAt).Milliseconds() > 0 &&
		strings.TrimSpace(fact.RecordingID) != ""
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
			!fact.RecordingEndedAt.After(fact.RecordingStartedAt) {
			return "", ErrInvalidInput
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_voicemails (
				call_id,
				practice_id,
				location_id,
				task_id,
				outcome,
				audio_state,
				provider_recording_id,
				recording_started_at,
				recording_ended_at,
				duration_millis,
				created_at,
				updated_at
			)
			VALUES (
				$1, $2, $3, $4, 'VOICEMAIL', 'READY', $5,
				$6, $7, $8, $9, $9
			)
			ON CONFLICT (call_id) DO NOTHING
		`, callID, practiceID, locationID, task.ID, fact.RecordingID,
			fact.RecordingStartedAt, fact.RecordingEndedAt,
			duration.Milliseconds(), m.now(),
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
	authorizationContext context.Context,
	streamContext context.Context,
	identity access.Identity,
	token string,
	rangeHeader string,
) (content PlaybackContent, resultErr error) {
	startedAt := time.Now()
	defer func() {
		if resultErr != nil {
			m.recordVoicemailPlayback(resultErr, time.Since(startedAt))
		}
	}()
	claims, err := m.parsePlaybackCapability(token)
	if err != nil {
		return PlaybackContent{}, ErrDenied
	}
	tx, err := m.pool.BeginTx(authorizationContext, pgx.TxOptions{})
	if err != nil {
		return PlaybackContent{}, fmt.Errorf("begin voicemail playback: %w", err)
	}
	defer func() { _ = tx.Rollback(authorizationContext) }()
	var practiceID, locationID, providerRecordingID string
	if err := tx.QueryRow(authorizationContext, `
		SELECT
			practice_id::text,
			location_id::text,
			provider_recording_id
		FROM human_calling_voicemails
		WHERE call_id = $1
			AND outcome = 'VOICEMAIL'
			AND provider_recording_id IS NOT NULL
	`, claims.CallID).Scan(
		&practiceID,
		&locationID,
		&providerRecordingID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlaybackContent{}, ErrDenied
		}
		return PlaybackContent{}, fmt.Errorf("read voicemail playback source: %w", err)
	}
	if _, err := m.access.LockReadAuthorization(
		authorizationContext,
		tx,
		identity,
		practiceID,
		locationID,
	); err != nil {
		return PlaybackContent{}, ErrDenied
	}
	if err := appendTimeline(
		authorizationContext,
		tx,
		claims.CallID,
		practiceID,
		"voicemail.playback_authorized",
		identity.Subject,
		"",
		"",
		opaqueReference(claims.Nonce),
		"",
		m.now(),
	); err != nil {
		return PlaybackContent{}, err
	}
	if err := tx.Commit(authorizationContext); err != nil {
		return PlaybackContent{}, fmt.Errorf("commit voicemail playback: %w", err)
	}
	if m.config.VoicemailAudioProvider == nil {
		return PlaybackContent{}, ErrConflict
	}
	content, resultErr = m.config.VoicemailAudioProvider.OpenVoicemailRecording(
		streamContext,
		providerRecordingID,
		rangeHeader,
	)
	if resultErr != nil {
		return PlaybackContent{}, resultErr
	}
	content.completion = &playbackCompletion{fn: func(streamErr error) {
		m.recordVoicemailPlayback(streamErr, time.Since(startedAt))
	}}
	return content, nil
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

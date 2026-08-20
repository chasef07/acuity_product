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
	VoicemailProcessing  VoicemailAudioState = "PROCESSING"
	VoicemailReady       VoicemailAudioState = "READY"
	VoicemailUnavailable VoicemailAudioState = "UNAVAILABLE"

	voicemailRecordingMaximum        = 120 * time.Second
	defaultVoicemailGreeting         = "Please leave a message after the beep."
	voicemailRecordingStartTolerance = 5 * time.Second
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

type PlaybackKind string

const (
	PlaybackVoicemail     PlaybackKind = "voicemail"
	PlaybackCallRecording PlaybackKind = "call_recording"
)

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
		content.completion.once.Do(func() { content.completion.fn(err) })
	}
}

func (content PlaybackContent) Validate(rangeHeader string) error {
	requested, hasRange := parseSingleByteRange(rangeHeader)
	if strings.TrimSpace(rangeHeader) != "" && !hasRange {
		return recordingUnavailable(RecordingInvalidResponse, "")
	}
	if content.Body == nil || (content.StatusCode != http.StatusOK &&
		content.StatusCode != http.StatusPartialContent) {
		return recordingUnavailable(RecordingInvalidResponse, "")
	}
	if content.StatusCode == http.StatusPartialContent &&
		(!hasRange || !matchesContentRange(requested, content.ContentRange)) {
		return recordingUnavailable(RecordingInvalidResponse, "")
	}
	return nil
}

type byteRange struct {
	start, end       uint64
	hasStart, hasEnd bool
}

func parseSingleByteRange(value string) (byteRange, bool) {
	value = strings.TrimSpace(value)
	if len(value) > 128 || !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
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
	parts := strings.Split(strings.TrimPrefix(value, "bytes "), "/")
	if len(parts) != 2 {
		return false
	}
	bounds := strings.Split(parts[0], "-")
	if len(bounds) != 2 {
		return false
	}
	start, startErr := strconv.ParseUint(bounds[0], 10, 64)
	end, endErr := strconv.ParseUint(bounds[1], 10, 64)
	length, lengthErr := strconv.ParseUint(parts[1], 10, 64)
	if startErr != nil || endErr != nil || lengthErr != nil || start > end || end >= length {
		return false
	}
	if requested.hasStart {
		return start == requested.start && (!requested.hasEnd || end <= requested.end)
	}
	return requested.hasEnd && end == length-1 && end-start+1 <= requested.end
}

type RecordingAudioProvider interface {
	OpenRecording(context.Context, string, string) (PlaybackContent, error)
}

type RecordingUnavailableReason string

const (
	RecordingNotFound        RecordingUnavailableReason = "recording_not_found"
	RecordingProviderAuth    RecordingUnavailableReason = "provider_auth"
	RecordingRateLimited     RecordingUnavailableReason = "provider_rate_limited"
	RecordingProviderTimeout RecordingUnavailableReason = "provider_timeout"
	RecordingProviderFailure RecordingUnavailableReason = "provider_unavailable"
	RecordingInvalidResponse RecordingUnavailableReason = "provider_invalid_response"
	RecordingURLExpired      RecordingUnavailableReason = "recording_url_expired"
)

type RecordingUnavailableError struct {
	Reason     RecordingUnavailableReason
	RetryAfter string
}

func (err *RecordingUnavailableError) Error() string { return "recording is unavailable" }

type playbackClaims struct {
	CallID    string       `json:"callId"`
	Kind      PlaybackKind `json:"kind"`
	Subject   string       `json:"subject"`
	ExpiresAt int64        `json:"expiresAt"`
	Nonce     string       `json:"nonce"`
}

func (m *Module) applyVoicemailGreetingStarted(
	ctx context.Context,
	fact ProviderFact,
) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Role != "CALLER" || state.Kind != "voicemail_greeting" {
		return ErrConflict
	}
	return m.applyVoicemailSpeechFact(ctx, fact, state, false)
}

func (m *Module) applyVoicemailGreetingEnded(
	ctx context.Context,
	fact ProviderFact,
) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Role != "CALLER" || state.Kind != "voicemail_greeting" {
		return ErrConflict
	}
	return m.applyVoicemailSpeechFact(ctx, fact, state, true)
}

func (m *Module) applyVoicemailSpeechFact(
	ctx context.Context,
	fact ProviderFact,
	state callLegClientState,
	ended bool,
) error {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin voicemail speech projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var practiceID, callerControlID, callerLegID, callerSessionID, terminal string
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, caller.provider_call_control_id,
			caller.provider_call_leg_id,
			COALESCE(caller.provider_call_session_id, ''),
			COALESCE(call.terminal_outcome, '')
		FROM human_calling_calls call
		JOIN human_calling_call_legs caller
			ON caller.call_id = call.id AND caller.id = $2 AND caller.role = 'CALLER'
		WHERE call.id = $1 FOR UPDATE OF call, caller
	`, state.CallID, state.CallLegID).Scan(
		&practiceID, &callerControlID, &callerLegID, &callerSessionID, &terminal,
	); err != nil {
		return fmt.Errorf("lock voicemail caller: %w", err)
	}
	if fact.CallControlID != callerControlID || fact.CallLegID != callerLegID ||
		fact.CallSessionID != callerSessionID {
		return ErrConflict
	}
	if terminal != "" && terminal != "VOICEMAIL" {
		return errTerminalOrObsoleteProviderFact
	}
	if ended && fact.PlaybackStatus == "completed" {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands SET state = 'RECONCILED',
				sent_at = COALESCE(sent_at, $2), last_error_code = NULL, updated_at = $3
			WHERE call_leg_id = $1 AND action = 'SPEAK_VOICEMAIL'
				AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
		`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
			return fmt.Errorf("reconcile voicemail Speak: %w", err)
		}
		if _, err := m.insertCallLegCommand(ctx, tx, state.CallID,
			state.CallLegID, "", "", CommandStartVoicemailRecording,
			callerControlID, map[string]any{
				"format": "mp3", "channels": "single",
				"recording_track": "inbound", "transcription": false,
				"play_beep": true, "max_length": int(voicemailRecordingMaximum.Seconds()),
				"client_state": encodeCallLegClientState(
					state.CallID, state.CallLegID, "CALLER", "voicemail_recording",
				),
			}, ""); err != nil {
			return err
		}
	} else if ended {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands SET state = 'FAILED',
				last_error_code = $2, updated_at = $3
			WHERE call_leg_id = $1 AND action = 'SPEAK_VOICEMAIL'
				AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
		`, state.CallLegID, "SPEAK_"+strings.ToUpper(fact.PlaybackStatus), m.now()); err != nil {
			return fmt.Errorf("fail voicemail Speak: %w", err)
		}
		if _, err := m.ensureRecoveryOutcome(ctx, tx, state.CallID,
			RecoveryMissedCall, "MISSED", fact); err != nil {
			return err
		}
		if err := m.endVoicemailCaller(ctx, tx, state.CallID, state.CallLegID); err != nil {
			return err
		}
	}
	kind := "voicemail.greeting.started"
	if ended && fact.PlaybackStatus == "completed" {
		kind = "voicemail.greeting.completed"
	} else if ended {
		kind = "voicemail.greeting.failed"
	}
	if err := appendTimeline(ctx, tx, state.CallID, practiceID, kind, "",
		fact.EventID, "", opaqueReference(fact.CallLegID), fact.PlaybackStatus,
		fact.OccurredAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) applyVoicemailRecordingSaved(
	ctx context.Context,
	fact ProviderFact,
) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Role != "CALLER" ||
		!fact.RecordingEndedAt.After(fact.RecordingStartedAt) {
		return ErrConflict
	}
	if fact.RecordingID == "" {
		var alreadyApplied bool
		if err := m.database.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM human_calling_projected_facts WHERE event_id = $1
			)
		`, fact.EventID).Scan(&alreadyApplied); err != nil {
			return fmt.Errorf("check recording callback replay: %w", err)
		}
		if alreadyApplied {
			return nil
		}
		provider, ok := m.provider.(RecordingStateProvider)
		if !ok {
			return fmt.Errorf("%w: provider cannot resolve recording", ErrAmbiguousEffect)
		}
		recording, err := provider.ResolveRecording(ctx, fact.CallLegID, fact.CallSessionID)
		if err != nil {
			return err
		}
		fact.RecordingID = recording.ID
		fact.CallControlID = recording.CallControlID
		fact.RecordingStartedAt = recording.StartedAt
		fact.RecordingEndedAt = recording.EndedAt
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin voicemail recording save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := m.requireExactVoicemailCaller(ctx, tx, state, fact); err != nil {
		return err
	}
	var recordingOwned bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM human_calling_provider_commands command
			JOIN human_calling_call_legs caller ON caller.id = command.call_leg_id
			WHERE command.call_id = $1 AND command.call_leg_id = $2
				AND command.action = 'START_VOICEMAIL_RECORDING'
				AND command.state IN ('SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED', 'FAILED')
				AND ($3 = '' OR caller.provider_connection_id = $3)
				AND COALESCE(command.sent_at, command.created_at)
					<= $4::timestamptz + $5::interval
		)
	`, state.CallID, state.CallLegID, fact.ConnectionID,
		fact.RecordingStartedAt,
		voicemailRecordingStartTolerance.String()).Scan(&recordingOwned); err != nil {
		return fmt.Errorf("read voicemail recording ownership: %w", err)
	}
	if !recordingOwned {
		return ErrConflict
	}
	var existingAudioState string
	err = tx.QueryRow(ctx, `
		SELECT audio_state FROM human_calling_voicemails
		WHERE call_id = $1
		FOR UPDATE
	`, state.CallID).Scan(&existingAudioState)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lock voicemail outcome: %w", err)
	}
	if existingAudioState == string(VoicemailReady) {
		return tx.Commit(ctx)
	}
	if _, err := m.ensureRecoveryOutcome(ctx, tx, state.CallID,
		RecoveryVoicemail, "VOICEMAIL", fact); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands SET state = 'RECONCILED',
			sent_at = COALESCE(sent_at, $2), last_error_code = NULL, updated_at = $3
		WHERE call_leg_id = $1 AND action = 'START_VOICEMAIL_RECORDING'
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
		return err
	}
	if err := m.endVoicemailCaller(ctx, tx, state.CallID, state.CallLegID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) applyVoicemailRecordingError(
	ctx context.Context,
	fact ProviderFact,
) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Role != "CALLER" || state.Kind != "voicemail_recording" {
		return ErrConflict
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin voicemail recording failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := m.requireExactVoicemailCaller(ctx, tx, state, fact); err != nil {
		return err
	}
	var existingAudioState string
	err = tx.QueryRow(ctx, `
		SELECT audio_state FROM human_calling_voicemails
		WHERE call_id = $1
		FOR UPDATE
	`, state.CallID).Scan(&existingAudioState)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lock voicemail outcome: %w", err)
	}
	if existingAudioState == string(VoicemailReady) {
		return tx.Commit(ctx)
	}
	if _, err := m.ensureRecoveryOutcome(ctx, tx, state.CallID,
		RecoveryMissedCall, "MISSED", fact); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_voicemails SET audio_state = 'UNAVAILABLE',
			last_error_code = 'RECORDING_FAILED', updated_at = $2 WHERE call_id = $1
	`, state.CallID, m.now()); err != nil {
		return err
	}
	if err := m.endVoicemailCaller(ctx, tx, state.CallID, state.CallLegID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) requireExactVoicemailCaller(
	ctx context.Context,
	tx pgx.Tx,
	state callLegClientState,
	fact ProviderFact,
) error {
	var controlID, legID, sessionID string
	if err := tx.QueryRow(ctx, `
		SELECT caller.provider_call_control_id, caller.provider_call_leg_id,
			COALESCE(caller.provider_call_session_id, '')
		FROM human_calling_calls call
		JOIN human_calling_call_legs caller
			ON caller.call_id = call.id AND caller.id = $2 AND caller.role = 'CALLER'
		WHERE call.id = $1
		FOR UPDATE OF call, caller
	`, state.CallID, state.CallLegID).Scan(&controlID, &legID, &sessionID); err != nil {
		return fmt.Errorf("lock voicemail recording caller: %w", err)
	}
	if (fact.CallControlID != "" && fact.CallControlID != controlID) ||
		fact.CallLegID != legID ||
		fact.CallSessionID != sessionID {
		return ErrConflict
	}
	return nil
}

func (m *Module) ensureRecoveryOutcome(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	outcome RecoveryOutcome,
	terminalOutcome string,
	fact ProviderFact,
) (string, error) {
	if m.work == nil {
		return "", ErrInvalidInput
	}
	var practiceID, locationID, phone, callerName string
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, call.location_id::text,
			COALESCE(call.caller_phone, ''), COALESCE(handoff.display_name, '')
		FROM human_calling_calls call
		LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
		WHERE call.id = $1 FOR UPDATE OF call
	`, callID).Scan(&practiceID, &locationID, &phone, &callerName); err != nil {
		return "", fmt.Errorf("lock recovery Call: %w", err)
	}
	workOutcome := work.RecoveryOutcomeMissedCall
	if outcome == RecoveryVoicemail {
		workOutcome = work.RecoveryOutcomeVoicemail
	}
	task, err := m.work.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{
		CallID: callID, PracticeID: practiceID, LocationID: locationID,
		Phone: phone, CallerName: callerName, Outcome: workOutcome,
		OccurredAt: fact.OccurredAt,
	})
	if err != nil {
		return "", err
	}
	if outcome == RecoveryVoicemail {
		duration := fact.RecordingEndedAt.Sub(fact.RecordingStartedAt)
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_voicemails (
				call_id, practice_id, location_id, task_id, outcome, audio_state,
				provider_recording_id, recording_started_at, recording_ended_at,
				duration_millis, created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'VOICEMAIL', 'READY', $5, $6, $7, $8, $9, $9)
			ON CONFLICT (call_id) DO UPDATE SET
				task_id = EXCLUDED.task_id, outcome = EXCLUDED.outcome,
				audio_state = EXCLUDED.audio_state,
				provider_recording_id = EXCLUDED.provider_recording_id,
				recording_started_at = EXCLUDED.recording_started_at,
				recording_ended_at = EXCLUDED.recording_ended_at,
				duration_millis = EXCLUDED.duration_millis, updated_at = EXCLUDED.updated_at
		`, callID, practiceID, locationID, task.ID, fact.RecordingID,
			fact.RecordingStartedAt, fact.RecordingEndedAt, duration.Milliseconds(),
			m.now()); err != nil {
			return "", fmt.Errorf("commit voicemail evidence: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_voicemails (
				call_id, practice_id, location_id, task_id, outcome, audio_state,
				last_error_code, created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'MISSED_CALL', 'UNAVAILABLE',
				'VOICEMAIL_UNAVAILABLE', $5, $5)
			ON CONFLICT (call_id) DO UPDATE SET
				audio_state = 'UNAVAILABLE', last_error_code = 'VOICEMAIL_UNAVAILABLE',
				updated_at = EXCLUDED.updated_at
		`, callID, practiceID, locationID, task.ID, m.now()); err != nil {
			return "", fmt.Errorf("commit missed Call evidence: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET terminal_outcome = $2,
			ended_at = COALESCE(ended_at, $3), version = version + 1, updated_at = $3
		WHERE id = $1
	`, callID, terminalOutcome, fact.OccurredAt); err != nil {
		return "", fmt.Errorf("commit recovery outcome: %w", err)
	}
	if err := appendTimeline(ctx, tx, callID, practiceID,
		"call.recovery_task_created", "", fact.EventID, "",
		opaqueReference(task.ID), string(outcome), fact.OccurredAt); err != nil {
		return "", err
	}
	return task.ID, nil
}

func (m *Module) endVoicemailCaller(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	callerLegID string,
) error {
	var controlID string
	if err := tx.QueryRow(ctx, `
		SELECT provider_call_control_id FROM human_calling_call_legs
		WHERE id = $1 AND call_id = $2 AND role = 'CALLER' FOR UPDATE
	`, callerLegID, callID).Scan(&controlID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs SET state = 'ENDING',
			ending_at = COALESCE(
				ending_at,
				GREATEST($2, COALESCE(answered_at, $2))
			), updated_at = $2
		WHERE id = $1 AND state NOT IN ('ENDED', 'FAILED')
	`, callerLegID, m.now()); err != nil {
		return err
	}
	_, err := m.insertCallLegCommand(ctx, tx, callID, callerLegID, "", "",
		CommandHangupLeg, controlID, map[string]any{
			"client_state": encodeCallLegClientState(
				callID, callerLegID, "CALLER", "voicemail_complete",
			),
		}, "")
	return err
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
	return m.issuePlaybackCapability(
		callID, PlaybackVoicemail, identity, call.Voicemail.DurationSeconds,
	)
}

func (m *Module) IssueCallRecordingPlayback(
	ctx context.Context,
	identity access.Identity,
	callID string,
) (PlaybackCapability, error) {
	call, err := m.ReadCall(ctx, identity, callID)
	if err != nil {
		return PlaybackCapability{}, err
	}
	if call.Recording.AudioState != RecordingReady {
		return PlaybackCapability{}, ErrConflict
	}
	return m.issuePlaybackCapability(
		callID, PlaybackCallRecording, identity, call.Recording.DurationSeconds,
	)
}

func (m *Module) issuePlaybackCapability(
	callID string,
	kind PlaybackKind,
	identity access.Identity,
	durationSeconds int64,
) (PlaybackCapability, error) {
	const minimumLifetime = 5 * time.Minute
	const maximumLifetime = 4 * time.Hour
	lifetime := minimumLifetime
	maximumDurationSeconds := int64((maximumLifetime - minimumLifetime) / time.Second)
	if durationSeconds >= maximumDurationSeconds {
		lifetime = maximumLifetime
	} else if durationSeconds > 0 {
		lifetime = time.Duration(durationSeconds)*time.Second + minimumLifetime
	}
	expiresAt := m.now().Add(lifetime)
	raw, err := json.Marshal(playbackClaims{
		CallID: callID, Kind: kind, Subject: identity.Subject,
		ExpiresAt: expiresAt.Unix(), Nonce: uuid.NewString(),
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
	token string,
	rangeHeader string,
) (content PlaybackContent, resultErr error) {
	return m.openPlayback(
		authorizationContext, streamContext, token, rangeHeader, PlaybackVoicemail,
	)
}

func (m *Module) OpenCallRecordingPlayback(
	authorizationContext context.Context,
	streamContext context.Context,
	token string,
	rangeHeader string,
) (content PlaybackContent, resultErr error) {
	return m.openPlayback(
		authorizationContext, streamContext, token, rangeHeader, PlaybackCallRecording,
	)
}

func (m *Module) openPlayback(
	authorizationContext context.Context,
	streamContext context.Context,
	token string,
	rangeHeader string,
	kind PlaybackKind,
) (content PlaybackContent, resultErr error) {
	startedAt := time.Now()
	defer func() {
		if resultErr != nil {
			m.recordPlayback(kind, resultErr, time.Since(startedAt))
		}
	}()
	claims, err := m.parsePlaybackCapability(token)
	if err != nil || claims.Kind != kind {
		return PlaybackContent{}, ErrDenied
	}
	identity := access.Identity{Subject: claims.Subject, EmailVerified: true}
	tx, err := m.database.BeginTx(authorizationContext, pgx.TxOptions{})
	if err != nil {
		return PlaybackContent{}, fmt.Errorf("begin recording playback: %w", err)
	}
	defer func() { _ = tx.Rollback(authorizationContext) }()
	var practiceID, locationID, recordingID string
	var contentExpiresAt *time.Time
	timelineKind := "voicemail.playback_authorized"
	var row pgx.Row
	if kind == PlaybackCallRecording {
		row = tx.QueryRow(authorizationContext, `
			SELECT recording.practice_id::text, recording.location_id::text,
				recording.provider_recording_id, recording.content_expires_at
			FROM human_calling_call_recordings recording
			WHERE recording.call_id = $1 AND recording.audio_state = 'READY'
				AND recording.provider_recording_id IS NOT NULL
				AND recording.content_expires_at > $2
		`, claims.CallID, m.now())
		timelineKind = "call.recording.playback_authorized"
	} else {
		row = tx.QueryRow(authorizationContext, `
			SELECT practice_id::text, location_id::text, provider_recording_id,
				NULL::timestamptz
			FROM human_calling_voicemails
			WHERE call_id = $1 AND outcome = 'VOICEMAIL'
				AND audio_state = 'READY' AND provider_recording_id IS NOT NULL
		`, claims.CallID)
	}
	if err := row.Scan(
		&practiceID, &locationID, &recordingID, &contentExpiresAt,
	); err != nil {
		return PlaybackContent{}, ErrDenied
	}
	if _, err := m.access.LockReadAuthorization(authorizationContext, tx,
		identity, practiceID, locationID); err != nil {
		return PlaybackContent{}, ErrDenied
	}
	if err := appendTimeline(authorizationContext, tx, claims.CallID, practiceID,
		timelineKind, identity.Subject, "", "",
		opaqueReference(claims.Nonce), "", m.now()); err != nil {
		return PlaybackContent{}, err
	}
	if err := tx.Commit(authorizationContext); err != nil {
		return PlaybackContent{}, err
	}
	if m.config.RecordingAudioProvider == nil {
		return PlaybackContent{}, ErrConflict
	}
	if contentExpiresAt == nil {
		content, resultErr = m.config.RecordingAudioProvider.OpenRecording(
			streamContext, recordingID, rangeHeader,
		)
	} else {
		providerContext, cancelProviderContext := context.WithDeadline(
			streamContext,
			*contentExpiresAt,
		)
		content, resultErr = m.config.RecordingAudioProvider.OpenRecording(
			providerContext, recordingID, rangeHeader,
		)
		if resultErr != nil || content.Body == nil {
			cancelProviderContext()
		} else {
			content.Body = &cancelingReadCloser{
				ReadCloser: content.Body,
				cancel:     cancelProviderContext,
			}
		}
	}
	if resultErr != nil {
		return PlaybackContent{}, resultErr
	}
	content.completion = &playbackCompletion{fn: func(streamErr error) {
		m.recordPlayback(kind, streamErr, time.Since(startedAt))
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
	if err := json.Unmarshal(raw, &claims); err != nil || claims.CallID == "" ||
		(claims.Kind != PlaybackVoicemail && claims.Kind != PlaybackCallRecording) ||
		claims.Subject == "" || claims.Nonce == "" ||
		!m.now().Before(time.Unix(claims.ExpiresAt, 0)) {
		return playbackClaims{}, ErrDenied
	}
	return claims, nil
}

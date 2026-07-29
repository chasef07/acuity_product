package humancalling

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CallState string

const (
	CallOffering         CallState = "OFFERING"
	CallConnecting       CallState = "CONNECTING"
	CallConnected        CallState = "CONNECTED"
	CallReconciling      CallState = "RECONCILING"
	CallUnanswered       CallState = "UNANSWERED"
	CallNeedsDisposition CallState = "NEEDS_DISPOSITION"
	CallResolved         CallState = "RESOLVED"
	CallFollowUpRequired CallState = "FOLLOW_UP_REQUIRED"
)

type FactType string

const (
	FactCallInitiated   FactType = "call.initiated"
	FactCallAnswered    FactType = "call.answered"
	FactCallBridged     FactType = "call.bridged"
	FactCallHangup      FactType = "call.hangup"
	FactPlaybackStarted FactType = "call.playback.started"
	FactRecordingSaved  FactType = "call.recording.saved"
	FactRecordingError  FactType = "call.recording.error"
)

type CommandAction string

const (
	CommandAnswerCaller      CommandAction = "ANSWER_CALLER"
	CommandStartRingback     CommandAction = "START_RINGBACK"
	CommandDialStaff         CommandAction = "DIAL_STAFF"
	CommandHangup            CommandAction = "HANGUP"
	CommandStartRecording    CommandAction = "START_RECORDING"
	CommandCreateCredential  CommandAction = "CREATE_CREDENTIAL"
	CommandDisableCredential CommandAction = "DISABLE_CREDENTIAL"
	CommandCreateJWT         CommandAction = "CREATE_JWT"
)

const (
	interruptedCommandTimeout = 30 * time.Second
	safeProviderRetryWindow   = 55 * time.Second
	hangupReconciliationDelay = 5 * time.Second
)

var (
	ErrDenied                    = errors.New("human calling access denied")
	ErrInvalidInput              = errors.New("invalid human calling input")
	ErrConflict                  = errors.New("human calling transition conflict")
	ErrExpired                   = errors.New("human calling deadline expired")
	ErrAlreadyClaimed            = errors.New("call already claimed")
	ErrIneligible                = errors.New("user is not currently call eligible")
	ErrInvalidHandoff            = errors.New("invalid handoff token")
	ErrAmbiguousEffect           = errors.New("provider effect is ambiguous")
	ErrDefinitiveProviderFailure = errors.New("provider effect definitely failed")
	ErrProviderTargetAbsent      = errors.New("provider target is absent")
	ErrInvalidWebhook            = errors.New("invalid provider webhook")
)

var canonicalE164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

type AcceptStatus string

const (
	Accepted         AcceptStatus = "ACCEPTED"
	AlreadyClaimed   AcceptStatus = "ALREADY_CLAIMED"
	AcceptExpired    AcceptStatus = "EXPIRED"
	AcceptIneligible AcceptStatus = "INELIGIBLE"
)

type Config struct {
	HandoffSIPDomain       string
	StaffSIPDomain         string
	OfferDuration          time.Duration
	ConnectionTimeout      time.Duration
	HandoffLifetime        time.Duration
	HandoffTokenKey        []byte
	LeaseDuration          time.Duration
	ReadinessGrace         time.Duration
	CallControlID          string
	CredentialConnectionID string
	FromNumber             string
	RingbackURL            string
	RecordingBucket        string
	WebhookPublicKey       ed25519.PublicKey
	WebhookTolerance       time.Duration
	Observer               observability.Observer
}

type ServiceIdentity = access.ServiceIdentity

type ContactContext struct {
	Phone          string
	PhoneSource    string
	DisplayName    string
	NameSource     string
	TransferReason string
	ReasonSource   string
}

type CreateHandoffCommand struct {
	Service        ServiceIdentity
	LocationID     string
	SourceCallID   string
	IdempotencyKey string
	Contact        ContactContext
}

type Handoff struct {
	ID             string
	SIPDestination string
	ExpiresAt      time.Time
}

type ProviderFact struct {
	EventID            string
	Type               FactType
	OccurredAt         time.Time
	CallControlID      string
	CallLegID          string
	CallSessionID      string
	ClientState        string
	To                 string
	HandoffToken       string
	HangupCause        string
	RecordingID        string
	RecordingBucket    string
	RecordingObjectKey string
}

type ProviderCommand struct {
	ID        string
	AttemptID string
	Action    CommandAction
	TargetID  string
	Payload   map[string]any
}

type ProviderResult struct {
	CallControlID string
	CallLegID     string
	CredentialID  string
	SIPUsername   string
	JWT           string
	JWTExpiresAt  time.Time
}

type Provider interface {
	Execute(context.Context, ProviderCommand) (ProviderResult, error)
}

type CredentialStateProvider interface {
	FindCredentialByName(context.Context, string) (ProviderResult, bool, error)
}

type CallStateProvider interface {
	IsCallAlive(context.Context, string) (bool, error)
}

type SoftphoneState struct {
	SessionID      string
	LeaseExpiresAt time.Time
	Owner          bool
	Available      bool
	ActiveCallID   string
}

type MediaToken struct {
	Token     string
	ExpiresAt time.Time
}

type ReadinessCommand struct {
	Identity        access.Identity
	SessionID       string
	Registered      bool
	MicrophoneReady bool
	AudioReady      bool
	SessionHealthy  bool
	Available       bool
}

type Offer struct {
	ID             string
	PracticeID     string
	LocationID     string
	LocationName   string
	DisplayName    string
	NameSource     string
	TransferReason string
	ReasonSource   string
	Phone          string
	Deadline       time.Time
	State          CallState
	Version        int64
}

type AcceptResult struct {
	Status AcceptStatus
	CallID string
	State  CallState
}

type RecordingState string

const (
	RecordingIntended  RecordingState = "INTENDED"
	RecordingRecording RecordingState = "RECORDING"
	RecordingReady     RecordingState = "READY"
	RecordingFailed    RecordingState = "FAILED"
)

type Recording struct {
	State       RecordingState
	Bucket      string
	ObjectKey   string
	ProviderID  string
	FailureCode string
}

type Call struct {
	ID                  string
	PracticeID          string
	LocationID          string
	LocationName        string
	State               CallState
	Deadline            time.Time
	ClaimantSubject     string
	WinnerSubject       string
	ExpectedStaffLegID  string
	ExpectedMediaToken  string
	currentAttemptID    string
	Phone               string
	PhoneSource         string
	DisplayName         string
	NameSource          string
	TransferReason      string
	ReasonSource        string
	ProviderTermination string
	ConnectedAt         *time.Time
	Version             int64
	Recording           Recording
}

type CallHistoryQuery struct {
	Identity          access.Identity
	PracticeID        string
	Phone             string
	CurrentCallID     string
	OriginatingCallID string
	Cursor            string
	Limit             int
}

type CallHistoryItem struct {
	ID              string
	Type            string
	Direction       string
	StartedAt       time.Time
	EndedAt         *time.Time
	DurationSeconds int64
	LocationID      string
	LocationName    string
	AnsweredByEmail string
	TransferReason  string
	Outcome         CallState
	Current         bool
	Originating     bool
}

type CallHistoryPage struct {
	Items      []CallHistoryItem
	NextCursor string
}

type OperatorTimeline struct {
	CallID     string
	PracticeID string
	State      CallState
	Version    int64
	Entries    []TimelineEntry
}

type TimelineEntry struct {
	Kind            string
	OpaqueReference string
	ErrorCode       string
	CommandAction   string
	CommandState    string
	CommandAttempts int
	ReceiptState    string
	AgeSeconds      int64
	OccurredAt      time.Time
}

type Disposition string

const (
	DispositionResolved         Disposition = "RESOLVED"
	DispositionFollowUpRequired Disposition = "FOLLOW_UP_REQUIRED"
)

type DispositionResult struct {
	Call   Call
	TaskID string
}

type Module struct {
	pool     *pgxpool.Pool
	access   *access.Module
	work     *work.Module
	provider Provider
	config   Config
	now      func() time.Time
	tokenKey []byte
	observer observability.Observer
}

func New(
	pool *pgxpool.Pool,
	accessModule *access.Module,
	provider Provider,
	config Config,
	now func() time.Time,
) *Module {
	if now == nil {
		now = time.Now
	}
	if config.OfferDuration <= 0 {
		config.OfferDuration = 20 * time.Second
	}
	if config.ConnectionTimeout <= 0 {
		config.ConnectionTimeout = 15 * time.Second
	}
	if config.HandoffLifetime <= 0 {
		config.HandoffLifetime = 2 * time.Minute
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 30 * time.Second
	}
	if config.ReadinessGrace <= 0 {
		config.ReadinessGrace = 15 * time.Second
	}
	if config.WebhookTolerance <= 0 {
		config.WebhookTolerance = 5 * time.Minute
	}
	if config.StaffSIPDomain == "" {
		config.StaffSIPDomain = config.HandoffSIPDomain
	}
	tokenKey := append([]byte(nil), config.HandoffTokenKey...)
	if len(tokenKey) == 0 {
		tokenKey = make([]byte, 32)
		if _, err := rand.Read(tokenKey); err != nil {
			panic("secure random source unavailable")
		}
	}
	module := &Module{
		pool:     pool,
		access:   accessModule,
		provider: provider,
		config:   config,
		now:      now,
		tokenKey: tokenKey,
		observer: config.Observer,
	}
	if pool != nil && accessModule != nil {
		module.work = work.New(pool, accessModule, now)
	}
	return module
}

func (m *Module) CreateHandoff(
	ctx context.Context,
	command CreateHandoffCommand,
) (Handoff, error) {
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
		&existing.ID,
		&existing.ExpiresAt,
		&existingFingerprint,
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

	result := Handoff{
		ID:        uuid.NewString(),
		ExpiresAt: m.now().Add(m.config.HandoffLifetime),
	}
	tokenHash := sha256.Sum256([]byte(m.handoffToken(result.ID)))
	var insertedID string
	err = tx.QueryRow(ctx, `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, token_hash, phone, phone_source,
			display_name, name_source, transfer_reason, reason_source, expires_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, NULLIF($9, ''), NULLIF($10, ''),
			NULLIF($11, ''), NULLIF($12, ''), NULLIF($13, ''), NULLIF($14, ''), $15
		)
		ON CONFLICT DO NOTHING
		RETURNING id::text
	`,
		result.ID,
		command.Service.Subject,
		command.Service.PracticeID,
		command.LocationID,
		command.SourceCallID,
		command.IdempotencyKey,
		fingerprint[:],
		tokenHash[:],
		command.Contact.Phone,
		command.Contact.PhoneSource,
		command.Contact.DisplayName,
		command.Contact.NameSource,
		command.Contact.TransferReason,
		command.Contact.ReasonSource,
		result.ExpiresAt,
	).Scan(&insertedID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Handoff{}, fmt.Errorf("create handoff: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			SELECT id::text, expires_at, input_fingerprint
			FROM human_calling_handoffs
			WHERE
				(service_subject = $1 AND idempotency_key = $2)
				OR (service_subject = $1 AND source_call_id = $3)
			ORDER BY (idempotency_key = $2) DESC
			LIMIT 1
			FOR UPDATE
		`,
			command.Service.Subject,
			command.IdempotencyKey,
			command.SourceCallID,
		).Scan(
			&result.ID,
			&result.ExpiresAt,
			&existingFingerprint,
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

func (m *Module) ApplyProviderFact(ctx context.Context, fact ProviderFact) error {
	if fact.EventID == "" || fact.Type == "" || fact.OccurredAt.IsZero() {
		return ErrInvalidInput
	}
	switch fact.Type {
	case FactCallInitiated:
		if state, ok := parseOpaqueClientState(fact.ClientState); ok &&
			state.Version == 1 &&
			state.Leg == "staff" {
			return m.applyStaffInitiated(ctx, fact, state.CallID)
		}
		return m.admitHandoff(ctx, fact)
	case FactCallAnswered:
		if state, ok := parseOpaqueClientState(fact.ClientState); ok &&
			state.Version == 1 &&
			state.Leg == "staff" {
			return m.applyStaffInitiated(ctx, fact, state.CallID)
		}
		return m.applyCallerAnswered(ctx, fact)
	case FactCallBridged:
		return m.applyBridge(ctx, fact)
	case FactCallHangup:
		return m.applyHangup(ctx, fact)
	case FactPlaybackStarted:
		return m.applyRingbackStarted(ctx, fact)
	case FactRecordingSaved:
		return m.applyRecordingSaved(ctx, fact)
	case FactRecordingError:
		return m.applyRecordingError(ctx, fact)
	default:
		return nil
	}
}

func (m *Module) AcquireSoftphone(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
	takeover bool,
) (SoftphoneState, error) {
	if sessionID == "" || !identity.EmailVerified || identity.Subject == "" {
		return SoftphoneState{}, ErrDenied
	}
	discovery, err := m.access.DiscoverActor(ctx, identity)
	if err != nil || discovery.PlatformOperator || len(discovery.Practices) == 0 {
		return SoftphoneState{}, ErrDenied
	}
	now := m.now()
	expiresAt := now.Add(m.config.LeaseDuration)
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("begin softphone lease acquisition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	practiceIDs, err := m.access.LockOperationalActor(ctx, tx, identity)
	if err != nil {
		return SoftphoneState{}, ErrDenied
	}
	callRows, err := tx.Query(ctx, `
		SELECT id
		FROM human_calling_calls
		WHERE
			(
				claimant_subject = $1
				AND state IN ('CONNECTING', 'CONNECTED', 'RECONCILING', 'NEEDS_DISPOSITION')
			)
			OR (winner_subject = $1 AND state = 'NEEDS_DISPOSITION')
		FOR UPDATE
	`, identity.Subject)
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("lock active Calls for softphone acquisition: %w", err)
	}
	for callRows.Next() {
	}
	if err := callRows.Err(); err != nil {
		callRows.Close()
		return SoftphoneState{}, fmt.Errorf("iterate active Call locks: %w", err)
	}
	callRows.Close()
	var previousSessionID string
	if err := tx.QueryRow(ctx, `
		SELECT session_id
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
		FOR UPDATE
	`, identity.Subject).Scan(&previousSessionID); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return SoftphoneState{}, fmt.Errorf("lock existing softphone lease: %w", err)
	}
	command := `
		INSERT INTO human_calling_softphone_leases (
			user_subject, session_id, lease_expires_at, readiness_updated_at
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_subject) DO UPDATE
		SET
			session_id = EXCLUDED.session_id,
			lease_expires_at = EXCLUDED.lease_expires_at,
			desired_available = CASE
				WHEN human_calling_softphone_leases.session_id = EXCLUDED.session_id
					THEN human_calling_softphone_leases.desired_available
				ELSE false
			END,
			registered = CASE
				WHEN human_calling_softphone_leases.session_id = EXCLUDED.session_id
					THEN human_calling_softphone_leases.registered
				ELSE false
			END,
			microphone_ready = CASE
				WHEN human_calling_softphone_leases.session_id = EXCLUDED.session_id
					THEN human_calling_softphone_leases.microphone_ready
				ELSE false
			END,
			audio_ready = CASE
				WHEN human_calling_softphone_leases.session_id = EXCLUDED.session_id
					THEN human_calling_softphone_leases.audio_ready
				ELSE false
			END,
			session_healthy = CASE
				WHEN human_calling_softphone_leases.session_id = EXCLUDED.session_id
					THEN human_calling_softphone_leases.session_healthy
				ELSE false
			END,
			readiness_updated_at = CASE
				WHEN human_calling_softphone_leases.session_id = EXCLUDED.session_id
					THEN human_calling_softphone_leases.readiness_updated_at
				ELSE EXCLUDED.readiness_updated_at
			END,
			version = human_calling_softphone_leases.version + 1,
			updated_at = EXCLUDED.readiness_updated_at
		WHERE
			human_calling_softphone_leases.session_id = EXCLUDED.session_id
			OR human_calling_softphone_leases.lease_expires_at <= EXCLUDED.readiness_updated_at
			OR $5
		RETURNING session_id, lease_expires_at
	`
	var state SoftphoneState
	if err := tx.QueryRow(
		ctx,
		command,
		identity.Subject,
		sessionID,
		expiresAt,
		now,
		takeover,
	).Scan(&state.SessionID, &state.LeaseExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return SoftphoneState{}, fmt.Errorf("commit losing softphone acquisition: %w", err)
			}
			state.Owner = false
			if err := m.loadCurrentCallCapacity(ctx, identity.Subject, &state); err != nil {
				return SoftphoneState{}, err
			}
			return state, nil
		}
		return SoftphoneState{}, fmt.Errorf("acquire softphone lease: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			claimant_session_id = $2,
			version = version + 1,
			updated_at = $3
		WHERE
			(
				claimant_subject = $1
				OR (winner_subject = $1 AND state = 'NEEDS_DISPOSITION')
			)
			AND claimant_session_id IS DISTINCT FROM $2
			AND state IN ('CONNECTING', 'CONNECTED', 'RECONCILING', 'NEEDS_DISPOSITION')
	`, identity.Subject, sessionID, now); err != nil {
		return SoftphoneState{}, fmt.Errorf("transfer active Call softphone ownership: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_connection_attempts attempt
		SET claimant_session_id = $2, updated_at = $3
		FROM human_calling_calls call
		WHERE call.current_attempt_id = attempt.id
			AND (
				call.claimant_subject = $1
				OR (call.winner_subject = $1 AND call.state = 'NEEDS_DISPOSITION')
			)
			AND attempt.claimant_session_id IS DISTINCT FROM $2
			AND call.state IN ('CONNECTING', 'CONNECTED', 'RECONCILING', 'NEEDS_DISPOSITION')
	`, identity.Subject, sessionID, now); err != nil {
		return SoftphoneState{}, fmt.Errorf("transfer current connection attempt ownership: %w", err)
	}
	if previousSessionID != "" && previousSessionID != sessionID {
		for _, practiceID := range practiceIDs {
			if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
				return SoftphoneState{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SoftphoneState{}, fmt.Errorf("commit softphone lease acquisition: %w", err)
	}
	state.Owner = state.SessionID == sessionID
	if err := m.loadCurrentCallCapacity(ctx, identity.Subject, &state); err != nil {
		return SoftphoneState{}, err
	}
	return state, nil
}

func (m *Module) SetReadiness(
	ctx context.Context,
	command ReadinessCommand,
) (SoftphoneState, error) {
	if command.Identity.Subject == "" || command.SessionID == "" {
		return SoftphoneState{}, ErrDenied
	}
	now := m.now()
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("begin calling readiness: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := m.access.LockOperationalActor(ctx, tx, command.Identity); err != nil {
		return SoftphoneState{}, ErrDenied
	}
	var state SoftphoneState
	err = tx.QueryRow(ctx, `
		UPDATE human_calling_softphone_leases
		SET
			registered = $3,
			microphone_ready = $4,
			audio_ready = $5,
			session_healthy = $6,
			desired_available = $7,
			readiness_updated_at = $8,
			lease_expires_at = $8 + $9::interval,
			version = version + 1,
			updated_at = $8
		WHERE user_subject = $1
			AND session_id = $2
			AND lease_expires_at > $8
		RETURNING
			session_id,
			lease_expires_at,
			(
				desired_available
				AND registered
				AND microphone_ready
				AND audio_ready
				AND session_healthy
			)
	`,
		command.Identity.Subject,
		command.SessionID,
		command.Registered,
		command.MicrophoneReady,
		command.AudioReady,
		command.SessionHealthy,
		command.Available,
		now,
		m.config.LeaseDuration.String(),
	).Scan(&state.SessionID, &state.LeaseExpiresAt, &state.Available)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SoftphoneState{}, ErrDenied
		}
		return SoftphoneState{}, fmt.Errorf("update calling readiness: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SoftphoneState{}, fmt.Errorf("commit calling readiness: %w", err)
	}
	state.Owner = true
	if err := m.loadCurrentCallCapacity(ctx, command.Identity.Subject, &state); err != nil {
		return SoftphoneState{}, err
	}
	return state, nil
}

func (m *Module) loadCurrentCallCapacity(
	ctx context.Context,
	subject string,
	state *SoftphoneState,
) error {
	if err := m.pool.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT id::text
			FROM human_calling_calls
			WHERE
				(
					claimant_subject = $1
					AND state IN (
						'CONNECTING',
						'CONNECTED',
						'RECONCILING',
						'NEEDS_DISPOSITION'
					)
				)
				OR (winner_subject = $1 AND state = 'NEEDS_DISPOSITION')
			ORDER BY
				(state = 'NEEDS_DISPOSITION'),
				updated_at DESC,
				id
			LIMIT 1
		), '')
	`, subject).Scan(&state.ActiveCallID); err != nil {
		return fmt.Errorf("read current Call capacity: %w", err)
	}
	if state.ActiveCallID != "" {
		state.Available = false
	}
	return nil
}

func (m *Module) ReconcileCredentials(ctx context.Context) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin credential reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands command
		SET
			state = 'FAILED',
			last_error_code = 'MEMBERSHIP_STATE_OBSOLETE',
			updated_at = $1
		WHERE command.state = 'PENDING'
			AND (
				(
					command.action = 'DISABLE_CREDENTIAL'
					AND EXISTS (
						SELECT 1
						FROM access_operational_users operational
						WHERE operational.user_subject = command.user_subject
					)
				)
				OR (
					command.action = 'CREATE_CREDENTIAL'
					AND NOT EXISTS (
						SELECT 1
						FROM access_operational_users operational
						WHERE operational.user_subject = command.user_subject
					)
				)
			)
	`, m.now()); err != nil {
		return fmt.Errorf("fence obsolete credential commands: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_credentials credential
		SET
			state = 'DISABLED',
			last_error_code = NULL,
			updated_at = $1
		WHERE credential.state IN ('PENDING', 'FAILED')
			AND credential.provider_credential_id IS NULL
			AND NOT EXISTS (
				SELECT 1
				FROM access_operational_users operational
				WHERE operational.user_subject = credential.user_subject
			)
	`, m.now()); err != nil {
		return fmt.Errorf("disable unprovisioned revoked credentials: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_credentials (user_subject, state)
		SELECT user_subject, 'PENDING'
		FROM access_operational_users
		ON CONFLICT (user_subject) DO UPDATE
		SET
			provider_credential_id = NULL,
			provider_sip_username = NULL,
			state = 'PENDING',
			last_error_code = NULL,
			updated_at = $1
		WHERE human_calling_credentials.state = 'DISABLED'
			OR (
				human_calling_credentials.state = 'FAILED'
				AND human_calling_credentials.provider_credential_id IS NULL
			)
	`, m.now()); err != nil {
		return fmt.Errorf("discover operational credential owners: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_credentials credential
		SET
			state = 'ACTIVE',
			last_error_code = NULL,
			updated_at = $1
		WHERE credential.state IN ('DISABLING', 'FAILED')
			AND credential.provider_credential_id IS NOT NULL
			AND EXISTS (
				SELECT 1
				FROM access_operational_users operational
				WHERE operational.user_subject = credential.user_subject
			)
	`, m.now()); err != nil {
		return fmt.Errorf("restore reauthorized credential: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT c.user_subject
		FROM human_calling_credentials c
		WHERE c.state = 'PENDING'
			AND NOT EXISTS (
				SELECT 1
				FROM human_calling_provider_commands command
				WHERE command.user_subject = c.user_subject
					AND command.action = 'CREATE_CREDENTIAL'
					AND command.state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
			)
		ORDER BY c.user_subject
		FOR UPDATE
	`)
	if err != nil {
		return fmt.Errorf("list pending managed credentials: %w", err)
	}
	subjects := []string{}
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			rows.Close()
			return fmt.Errorf("scan managed credential owner: %w", err)
		}
		subjects = append(subjects, subject)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate managed credential owners: %w", err)
	}
	rows.Close()
	for _, subject := range subjects {
		commandID := uuid.NewString()
		payload, err := json.Marshal(map[string]any{
			"connection_id": m.config.CredentialConnectionID,
			"name":          "acuity-" + opaqueReference(subject),
			"tag":           "acuity-portal",
		})
		if err != nil {
			return fmt.Errorf("encode credential command: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_provider_commands (
				id, user_subject, action, payload, next_attempt_at
			)
			VALUES ($1, $2, 'CREATE_CREDENTIAL', $3, $4)
			ON CONFLICT DO NOTHING
		`, commandID, subject, payload, m.now()); err != nil {
			return fmt.Errorf("commit credential command: %w", err)
		}
	}

	rows, err = tx.Query(ctx, `
		UPDATE human_calling_credentials c
		SET state = 'DISABLING', updated_at = $1
		WHERE c.state IN ('ACTIVE', 'FAILED')
			AND c.provider_credential_id IS NOT NULL
			AND NOT EXISTS (
				SELECT 1
				FROM access_operational_users operational
				WHERE operational.user_subject = c.user_subject
			)
			AND NOT EXISTS (
				SELECT 1
				FROM human_calling_provider_commands command
				WHERE command.user_subject = c.user_subject
					AND command.action = 'DISABLE_CREDENTIAL'
					AND command.state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
			)
		RETURNING c.user_subject, c.provider_credential_id
	`, m.now())
	if err != nil {
		return fmt.Errorf("find credentials without Memberships: %w", err)
	}
	type disabledCredential struct {
		subject      string
		credentialID string
	}
	toDisable := []disabledCredential{}
	for rows.Next() {
		var credential disabledCredential
		if err := rows.Scan(&credential.subject, &credential.credentialID); err != nil {
			rows.Close()
			return fmt.Errorf("scan disabled credential: %w", err)
		}
		toDisable = append(toDisable, credential)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate disabled credentials: %w", err)
	}
	rows.Close()
	for _, credential := range toDisable {
		commandID := uuid.NewString()
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_provider_commands (
				id, user_subject, action, target_id, next_attempt_at
			)
			VALUES ($1, $2, 'DISABLE_CREDENTIAL', $3, $4)
		`, commandID, credential.subject, credential.credentialID, m.now()); err != nil {
			return fmt.Errorf("commit disable credential command: %w", err)
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
	provider, ok := m.provider.(CredentialStateProvider)
	if !ok {
		return false, nil
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin credential state reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var command ProviderCommand
	var userSubject string
	var encoded []byte
	if err := tx.QueryRow(ctx, `
		SELECT id::text, user_subject, action, COALESCE(target_id, ''), payload
		FROM human_calling_provider_commands
		WHERE state = 'AMBIGUOUS'
			AND action IN ('CREATE_CREDENTIAL', 'DISABLE_CREDENTIAL')
			AND next_attempt_at <= $1
		ORDER BY updated_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, m.now()).Scan(
		&command.ID,
		&userSubject,
		&command.Action,
		&command.TargetID,
		&encoded,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit empty credential reconciliation: %w", err)
			}
			return false, nil
		}
		return false, fmt.Errorf("claim ambiguous credential command: %w", err)
	}
	if err := json.Unmarshal(encoded, &command.Payload); err != nil {
		return false, fmt.Errorf("decode ambiguous credential command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = 'SENDING',
			last_error_code = 'PROVIDER_STATE_QUERY',
			updated_at = $2
		WHERE id = $1 AND state = 'AMBIGUOUS'
	`, command.ID, m.now()); err != nil {
		return false, fmt.Errorf("mark credential reconciliation query: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit credential reconciliation claim: %w", err)
	}

	name := "acuity-" + opaqueReference(userSubject)
	result, found, lookupErr := provider.FindCredentialByName(ctx, name)

	tx, err = m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, fmt.Errorf("begin credential reconciliation result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if lookupErr != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET
				state = 'AMBIGUOUS',
				last_error_code = 'PROVIDER_STATE_UNAVAILABLE',
				next_attempt_at = $2 + interval '5 seconds',
				updated_at = $2
			WHERE id = $1 AND state = 'SENDING'
		`, command.ID, m.now()); err != nil {
			return true, fmt.Errorf("defer credential state reconciliation: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return true, fmt.Errorf("commit deferred credential reconciliation: %w", err)
		}
		return true, nil
	}
	var authorized bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM access_operational_users
			WHERE user_subject = $1
		)
	`, userSubject).Scan(&authorized); err != nil {
		return true, fmt.Errorf("read credential owner membership: %w", err)
	}

	switch command.Action {
	case CommandCreateCredential:
		if found {
			credentialState := "ACTIVE"
			reconciliationCode := ""
			if !authorized {
				credentialState = "DISABLING"
				reconciliationCode = "ACCESS_OBSOLETE_AFTER_CREATE"
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_provider_commands
				SET
					state = 'RECONCILED',
					last_error_code = NULLIF($2, ''),
					updated_at = $3
				WHERE id = $1 AND state = 'SENDING'
			`, command.ID, reconciliationCode, m.now()); err != nil {
				return true, fmt.Errorf("reconcile credential creation command: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_credentials
				SET
					state = $2,
					provider_credential_id = $3,
					provider_sip_username = $4,
					last_error_code = NULLIF($5, ''),
					updated_at = $6
				WHERE user_subject = $1
			`,
				userSubject,
				credentialState,
				result.CredentialID,
				result.SIPUsername,
				reconciliationCode,
				m.now(),
			); err != nil {
				return true, fmt.Errorf("reconcile created credential: %w", err)
			}
			if !authorized {
				if _, err := tx.Exec(ctx, `
					INSERT INTO human_calling_provider_commands (
						id, user_subject, action, target_id, next_attempt_at
					)
					SELECT $1, $2, 'DISABLE_CREDENTIAL', $3, $4
					WHERE NOT EXISTS (
						SELECT 1
						FROM human_calling_provider_commands
						WHERE user_subject = $2
							AND action = 'DISABLE_CREDENTIAL'
							AND state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
					)
				`, uuid.NewString(), userSubject, result.CredentialID, m.now()); err != nil {
					return true, fmt.Errorf("commit reconciled credential cleanup: %w", err)
				}
			}
		} else {
			nextState := "PENDING"
			errorCode := "PROVIDER_STATE_ABSENT"
			credentialState := "PENDING"
			if !authorized {
				nextState = "FAILED"
				errorCode = "ACCESS_OBSOLETE"
				credentialState = "DISABLED"
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_provider_commands
				SET
					state = $2,
					last_error_code = $3,
					next_attempt_at = $4,
					updated_at = $4
				WHERE id = $1 AND state = 'SENDING'
			`, command.ID, nextState, errorCode, m.now()); err != nil {
				return true, fmt.Errorf("repair absent credential creation: %w", err)
			}
			if !authorized {
				if _, err := tx.Exec(ctx, `
					UPDATE human_calling_credentials
					SET state = $2, last_error_code = NULL, updated_at = $3
					WHERE user_subject = $1
				`, userSubject, credentialState, m.now()); err != nil {
					return true, fmt.Errorf("disable absent obsolete credential: %w", err)
				}
			}
		}
	case CommandDisableCredential:
		if authorized {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_provider_commands
				SET
					state = 'RECONCILED',
					last_error_code = 'MEMBERSHIP_REAUTHORIZED',
					updated_at = $2
				WHERE id = $1 AND state = 'SENDING'
			`, command.ID, m.now()); err != nil {
				return true, fmt.Errorf("fence reauthorized credential disable: %w", err)
			}
			nextState := "PENDING"
			credentialID := ""
			sipUsername := ""
			if found {
				nextState = "ACTIVE"
				credentialID = result.CredentialID
				sipUsername = result.SIPUsername
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_credentials
				SET
					state = $2,
					provider_credential_id = NULLIF($3, ''),
					provider_sip_username = NULLIF($4, ''),
					last_error_code = NULL,
					updated_at = $5
				WHERE user_subject = $1
			`,
				userSubject,
				nextState,
				credentialID,
				sipUsername,
				m.now(),
			); err != nil {
				return true, fmt.Errorf("restore reauthorized credential state: %w", err)
			}
			break
		}
		if found && result.CredentialID != command.TargetID {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_provider_commands
				SET
					state = 'AMBIGUOUS',
					last_error_code = 'PROVIDER_CREDENTIAL_ID_MISMATCH',
					next_attempt_at = $2 + interval '30 seconds',
					updated_at = $2
				WHERE id = $1 AND state = 'SENDING'
			`, command.ID, m.now()); err != nil {
				return true, fmt.Errorf("record credential identity mismatch: %w", err)
			}
		} else if found {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_provider_commands
				SET
					state = 'PENDING',
					last_error_code = 'PROVIDER_STATE_PRESENT',
					next_attempt_at = $2,
					updated_at = $2
				WHERE id = $1 AND state = 'SENDING'
			`, command.ID, m.now()); err != nil {
				return true, fmt.Errorf("repair present credential disable: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_provider_commands
				SET state = 'RECONCILED', last_error_code = NULL, updated_at = $2
				WHERE id = $1 AND state = 'SENDING'
			`, command.ID, m.now()); err != nil {
				return true, fmt.Errorf("reconcile credential disable command: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_credentials
				SET
					state = 'DISABLED',
					last_error_code = NULL,
					updated_at = $2
				WHERE user_subject = $1
			`, userSubject, m.now()); err != nil {
				return true, fmt.Errorf("reconcile disabled credential: %w", err)
			}
		}
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
		ID:       uuid.NewString(),
		Action:   CommandCreateJWT,
		TargetID: credentialID,
		Payload:  map[string]any{},
	}
	encoded, err := json.Marshal(command.Payload)
	if err != nil {
		return MediaToken{}, fmt.Errorf("encode media JWT command: %w", err)
	}
	if _, err := m.pool.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			id, user_subject, action, target_id, payload, next_attempt_at
		)
		VALUES ($1, $2, 'CREATE_JWT', $3, $4, $5)
	`, command.ID, identity.Subject, credentialID, encoded, m.now()); err != nil {
		return MediaToken{}, fmt.Errorf("commit media JWT command: %w", err)
	}
	result, err := m.processCommand(ctx, command.ID)
	if err != nil {
		return MediaToken{}, err
	}
	if result.JWT == "" ||
		!result.JWTExpiresAt.After(m.now()) ||
		result.JWTExpiresAt.After(m.now().Add(29*24*time.Hour)) {
		return MediaToken{}, fmt.Errorf("provider returned an invalid media JWT")
	}
	if _, err := m.authorizeMediaJWT(ctx, identity, sessionID, credentialID); err != nil {
		return MediaToken{}, err
	}
	return MediaToken{
		Token:     result.JWT,
		ExpiresAt: result.JWTExpiresAt,
	}, nil
}

func (m *Module) authorizeMediaJWT(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
	expectedCredentialID string,
) (string, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin media JWT authorization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := m.access.LockOperationalActor(ctx, tx, identity); err != nil {
		return "", ErrDenied
	}
	var leaseCurrent bool
	if err := tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
		FOR SHARE
	`, identity.Subject, sessionID, m.now()).Scan(&leaseCurrent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrDenied
		}
		return "", fmt.Errorf("verify media lease: %w", err)
	}
	if !leaseCurrent {
		return "", ErrDenied
	}
	var credentialID string
	if err := tx.QueryRow(ctx, `
		SELECT provider_credential_id
		FROM human_calling_credentials
		WHERE user_subject = $1 AND state = 'ACTIVE'
		FOR SHARE
	`, identity.Subject).Scan(&credentialID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrConflict
		}
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

func (m *Module) ListOffers(
	ctx context.Context,
	identity access.Identity,
) ([]Offer, error) {
	discovery, err := m.access.DiscoverActor(ctx, identity)
	if err != nil || discovery.PlatformOperator {
		return nil, ErrDenied
	}
	var available bool
	err = m.pool.QueryRow(ctx, `
		SELECT
			desired_available
			AND registered
			AND microphone_ready
			AND audio_ready
			AND session_healthy
			AND lease_expires_at > $2
			AND readiness_updated_at > $2 - $3::interval
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
	`, identity.Subject, m.now(), m.config.ReadinessGrace.String()).Scan(&available)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []Offer{}, nil
		}
		return nil, fmt.Errorf("read calling eligibility: %w", err)
	}
	if !available {
		return []Offer{}, nil
	}
	var hasCurrentCall bool
	if err := m.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM human_calling_calls
			WHERE
				(
					claimant_subject = $1
					AND state IN (
						'CONNECTING',
						'CONNECTED',
						'RECONCILING',
						'NEEDS_DISPOSITION'
					)
				)
				OR (winner_subject = $1 AND state = 'NEEDS_DISPOSITION')
		)
	`, identity.Subject).Scan(&hasCurrentCall); err != nil {
		return nil, fmt.Errorf("read current Call capacity: %w", err)
	}
	if hasCurrentCall {
		return []Offer{}, nil
	}

	authorized := make(map[string]string)
	for _, practice := range discovery.Practices {
		for _, location := range practice.Locations {
			authorized[practice.ID+"/"+location.ID] = location.Name
		}
	}
	rows, err := m.pool.Query(ctx, `
		SELECT
			c.id::text,
			c.practice_id::text,
			c.location_id::text,
			COALESCE(h.display_name, ''),
			COALESCE(h.name_source, ''),
			COALESCE(h.transfer_reason, ''),
			COALESCE(h.reason_source, ''),
			c.offer_deadline,
			c.state,
			c.version
		FROM human_calling_calls c
		JOIN human_calling_handoffs h ON h.id = c.handoff_id
		WHERE c.state = 'OFFERING' AND c.offer_deadline > $1
		ORDER BY c.offer_deadline, c.id
	`, m.now())
	if err != nil {
		return nil, fmt.Errorf("list current offers: %w", err)
	}
	defer rows.Close()
	offers := []Offer{}
	for rows.Next() {
		var offer Offer
		if err := rows.Scan(
			&offer.ID,
			&offer.PracticeID,
			&offer.LocationID,
			&offer.DisplayName,
			&offer.NameSource,
			&offer.TransferReason,
			&offer.ReasonSource,
			&offer.Deadline,
			&offer.State,
			&offer.Version,
		); err != nil {
			return nil, fmt.Errorf("scan current offer: %w", err)
		}
		locationName, ok := authorized[offer.PracticeID+"/"+offer.LocationID]
		if !ok {
			continue
		}
		offer.LocationName = locationName
		offers = append(offers, offer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current offers: %w", err)
	}
	return offers, nil
}

func (m *Module) AcceptOffer(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
	callID string,
) (AcceptResult, error) {
	if identity.Subject == "" || sessionID == "" || callID == "" {
		return AcceptResult{}, ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AcceptResult{}, fmt.Errorf("begin offer acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var practiceID, locationID, callerCallControlID string
	var state CallState
	var deadline time.Time
	var claimant *string
	if err := tx.QueryRow(ctx, `
		SELECT
			practice_id::text,
			location_id::text,
			state,
			offer_deadline,
			claimant_subject,
			caller_call_control_id
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, callID).Scan(
		&practiceID,
		&locationID,
		&state,
		&deadline,
		&claimant,
		&callerCallControlID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AcceptResult{}, ErrDenied
		}
		return AcceptResult{}, fmt.Errorf("lock offered Call: %w", err)
	}
	if state != CallOffering || claimant != nil {
		if err := tx.Commit(ctx); err != nil {
			return AcceptResult{}, fmt.Errorf("commit losing offer acceptance: %w", err)
		}
		return AcceptResult{
			Status: AlreadyClaimed,
			CallID: callID,
			State:  state,
		}, nil
	}
	if !m.now().Before(deadline) {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				state = 'UNANSWERED',
				ended_at = $2,
				version = version + 1,
				updated_at = $2
			WHERE id = $1
		`, callID, m.now()); err != nil {
			return AcceptResult{}, fmt.Errorf("expire offered Call: %w", err)
		}
		if err := insertCommand(
			ctx,
			tx,
			callID,
			"",
			CommandHangup,
			callerCallControlID,
			map[string]any{"client_state": opaqueClientState(callID, "caller")},
			m.now(),
		); err != nil {
			return AcceptResult{}, err
		}
		if err := appendTimeline(
			ctx,
			tx,
			callID,
			practiceID,
			"offer.expired",
			"",
			"",
			"",
			"",
			"OFFER_TIMEOUT",
			m.now(),
		); err != nil {
			return AcceptResult{}, err
		}
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
			return AcceptResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return AcceptResult{}, fmt.Errorf("commit expired offer: %w", err)
		}
		return AcceptResult{Status: AcceptExpired, CallID: callID, State: CallUnanswered}, nil
	}
	if _, err := m.access.LockMembershipAuthorization(
		ctx,
		tx,
		identity,
		practiceID,
		locationID,
	); err != nil {
		if errors.Is(err, access.ErrDenied) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return AcceptResult{}, fmt.Errorf("commit ineligible offer acceptance: %w", commitErr)
			}
			return AcceptResult{Status: AcceptIneligible, CallID: callID, State: state}, nil
		}
		return AcceptResult{}, err
	}

	var eligible bool
	if err := tx.QueryRow(ctx, `
		SELECT
			desired_available
			AND registered
			AND microphone_ready
			AND audio_ready
			AND session_healthy
			AND lease_expires_at > $3
			AND readiness_updated_at > $3 - $4::interval
		FROM human_calling_softphone_leases
		WHERE user_subject = $1 AND session_id = $2
		FOR UPDATE
	`,
		identity.Subject,
		sessionID,
		m.now(),
		m.config.ReadinessGrace.String(),
	).Scan(&eligible); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return AcceptResult{}, fmt.Errorf("lock offer eligibility: %w", err)
		}
		eligible = false
	}
	if !eligible {
		if err := tx.Commit(ctx); err != nil {
			return AcceptResult{}, fmt.Errorf("commit ineligible offer acceptance: %w", err)
		}
		return AcceptResult{Status: AcceptIneligible, CallID: callID, State: state}, nil
	}
	var needsDisposition bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM human_calling_calls
			WHERE winner_subject = $1
				AND state = 'NEEDS_DISPOSITION'
		)
	`, identity.Subject).Scan(&needsDisposition); err != nil {
		return AcceptResult{}, fmt.Errorf("read pending Call disposition: %w", err)
	}
	if needsDisposition {
		if err := tx.Commit(ctx); err != nil {
			return AcceptResult{}, fmt.Errorf("commit disposition-ineligible offer acceptance: %w", err)
		}
		return AcceptResult{Status: AcceptIneligible, CallID: callID, State: state}, nil
	}

	var sipUsername string
	if err := tx.QueryRow(ctx, `
		SELECT provider_sip_username
		FROM human_calling_credentials
		WHERE user_subject = $1 AND state = 'ACTIVE'
		FOR UPDATE
	`, identity.Subject).Scan(&sipUsername); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return AcceptResult{}, fmt.Errorf("commit credential-ineligible offer acceptance: %w", commitErr)
			}
			return AcceptResult{Status: AcceptIneligible, CallID: callID, State: state}, nil
		}
		return AcceptResult{}, fmt.Errorf("load managed SIP credential: %w", err)
	}

	commandID := uuid.NewString()
	attemptID := uuid.NewString()
	acceptedAt := m.now()
	connectionDeadline := acceptedAt.Add(m.config.ConnectionTimeout)
	payload := map[string]any{
		"to":                    managedSIPDestination(sipUsername, m.config.StaffSIPDomain),
		"connection_id":         m.config.CallControlID,
		"from":                  m.config.FromNumber,
		"link_to":               "",
		"bridge_intent":         true,
		"bridge_on_answer":      true,
		"prevent_double_bridge": true,
		"client_state":          opaqueClientState(callID, "staff", attemptID),
		"timeout_secs":          int(m.config.ConnectionTimeout.Seconds()),
		"custom_headers": []map[string]string{{
			"name":  "X-Acuity-Media-Token",
			"value": m.staffMediaToken(callID, attemptID),
		}},
	}
	payload["link_to"] = callerCallControlID
	encoded, err := json.Marshal(payload)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("encode Dial command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			state = 'CONNECTING',
			claimant_subject = $2,
			claimant_session_id = $3,
			connection_deadline = $4,
			current_attempt_id = $6,
			provider_termination = NULL,
			ended_at = NULL,
			version = version + 1,
			updated_at = $5
		WHERE id = $1
		`,
		callID,
		identity.Subject,
		sessionID,
		connectionDeadline,
		acceptedAt,
		attemptID,
	); err != nil {
		if isUniqueViolation(err) {
			return AcceptResult{
				Status: AcceptIneligible,
				CallID: callID,
				State:  CallOffering,
			}, nil
		}
		return AcceptResult{}, fmt.Errorf("commit current Call claimant: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_connection_attempts (
			id,
			call_id,
			claimant_subject,
			claimant_session_id,
			connection_deadline,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
	`, attemptID, callID, identity.Subject, sessionID, connectionDeadline, acceptedAt); err != nil {
		return AcceptResult{}, fmt.Errorf("commit connection attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			id, call_id, attempt_id, user_subject, action, target_id, payload,
			depends_on_command_id, next_attempt_at
		)
		VALUES (
			$1,
			$2,
			$3,
			$4,
			'DIAL_STAFF',
			$5,
			$6,
			(
				SELECT id
				FROM human_calling_provider_commands
				WHERE call_id = $2 AND action = 'START_RINGBACK'
				ORDER BY created_at, id
				LIMIT 1
			),
			$7
		)
	`,
		commandID,
		callID,
		attemptID,
		identity.Subject,
		callerCallControlID,
		encoded,
		acceptedAt,
	); err != nil {
		return AcceptResult{}, fmt.Errorf("commit winning Dial command: %w", err)
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"offer.claimed",
		identity.Subject,
		"",
		commandID,
		"",
		"",
		acceptedAt,
	); err != nil {
		return AcceptResult{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return AcceptResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		if isUniqueViolation(err) {
			return AcceptResult{
				Status: AlreadyClaimed,
				CallID: callID,
				State:  CallConnecting,
			}, nil
		}
		return AcceptResult{}, fmt.Errorf("commit offer acceptance: %w", err)
	}
	return AcceptResult{
		Status: Accepted,
		CallID: callID,
		State:  CallConnecting,
	}, nil
}

func (m *Module) ReadCall(
	ctx context.Context,
	identity access.Identity,
	callID string,
) (Call, error) {
	if identity.Subject == "" || callID == "" {
		return Call{}, ErrDenied
	}
	call, err := m.loadCall(ctx, callID)
	if err != nil {
		return Call{}, err
	}
	authorization, err := m.access.ResolveActor(
		ctx,
		identity,
		call.PracticeID,
		call.LocationID,
	)
	if err != nil || authorization.PlatformOperator {
		return Call{}, ErrDenied
	}
	if call.ClaimantSubject != identity.Subject && call.WinnerSubject != identity.Subject {
		return Call{}, ErrDenied
	}
	if call.currentAttemptID != "" &&
		(call.State == CallConnecting ||
			call.State == CallReconciling ||
			call.State == CallConnected) {
		call.ExpectedMediaToken = m.staffMediaToken(call.ID, call.currentAttemptID)
	}
	return call, nil
}

func (m *Module) QueryCallHistory(
	ctx context.Context,
	query CallHistoryQuery,
) (CallHistoryPage, error) {
	if m.access == nil ||
		strings.TrimSpace(query.PracticeID) == "" ||
		!canonicalE164.MatchString(query.Phone) {
		return CallHistoryPage{}, ErrInvalidInput
	}
	limit := query.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 20 {
		return CallHistoryPage{}, ErrInvalidInput
	}
	cursor, err := decodeCallHistoryCursor(query.Cursor)
	if err != nil {
		return CallHistoryPage{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CallHistoryPage{}, fmt.Errorf("begin Call history: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		query.Identity,
		query.PracticeID,
		"",
	)
	if err != nil {
		return CallHistoryPage{}, ErrDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		locationIDs = append(locationIDs, location.ID)
	}
	if len(locationIDs) == 0 {
		return CallHistoryPage{}, ErrDenied
	}
	rows, err := tx.Query(ctx, `
		SELECT
			call.id::text,
			call.created_at,
			call.ended_at,
			CASE
				WHEN call.connected_at IS NOT NULL AND call.ended_at IS NOT NULL
				THEN GREATEST(
					0,
					EXTRACT(EPOCH FROM (call.ended_at - call.connected_at))::bigint
				)
				ELSE 0
			END,
			call.location_id::text,
			location.name,
			COALESCE(membership.email, ''),
			COALESCE(handoff.transfer_reason, ''),
			call.state
		FROM human_calling_calls call
		JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id
		JOIN access_locations location
			ON location.practice_id = call.practice_id
			AND location.id = call.location_id
		LEFT JOIN access_memberships membership
			ON membership.practice_id = call.practice_id
			AND membership.user_subject = call.winner_subject
		WHERE call.practice_id = $1
			AND call.location_id::text = ANY($2::text[])
			AND handoff.phone = $3
			AND (
				NOT $4
				OR call.created_at < $5
				OR (call.created_at = $5 AND call.id::text < $6)
			)
		ORDER BY call.created_at DESC, call.id DESC
		LIMIT $7
	`, query.PracticeID, locationIDs, query.Phone, cursor.Present,
		cursor.CreatedAt, cursor.ID, limit+1,
	)
	if err != nil {
		return CallHistoryPage{}, fmt.Errorf("query Call history: %w", err)
	}
	defer rows.Close()
	items := make([]CallHistoryItem, 0, limit+1)
	for rows.Next() {
		var item CallHistoryItem
		if err := rows.Scan(
			&item.ID,
			&item.StartedAt,
			&item.EndedAt,
			&item.DurationSeconds,
			&item.LocationID,
			&item.LocationName,
			&item.AnsweredByEmail,
			&item.TransferReason,
			&item.Outcome,
		); err != nil {
			return CallHistoryPage{}, fmt.Errorf("scan Call history: %w", err)
		}
		item.Type = "CALL"
		item.Direction = "INBOUND"
		item.Current = item.ID == query.CurrentCallID
		item.Originating = item.ID == query.OriginatingCallID
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return CallHistoryPage{}, fmt.Errorf("iterate Call history: %w", err)
	}
	rows.Close()

	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		nextCursor, err = encodeCallHistoryCursor(items[len(items)-1])
		if err != nil {
			return CallHistoryPage{}, err
		}
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	if err := tx.Commit(ctx); err != nil {
		return CallHistoryPage{}, fmt.Errorf("commit Call history: %w", err)
	}
	return CallHistoryPage{Items: items, NextCursor: nextCursor}, nil
}

type callHistoryCursor struct {
	Present   bool      `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

func encodeCallHistoryCursor(item CallHistoryItem) (string, error) {
	encoded, err := json.Marshal(callHistoryCursor{
		CreatedAt: item.StartedAt,
		ID:        item.ID,
	})
	if err != nil {
		return "", fmt.Errorf("encode Call history cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCallHistoryCursor(encoded string) (callHistoryCursor, error) {
	if encoded == "" {
		return callHistoryCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return callHistoryCursor{}, err
	}
	var cursor callHistoryCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return callHistoryCursor{}, err
	}
	if cursor.CreatedAt.IsZero() || strings.TrimSpace(cursor.ID) == "" {
		return callHistoryCursor{}, ErrInvalidInput
	}
	cursor.Present = true
	return cursor, nil
}

func (m *Module) ReadOperatorTimeline(
	ctx context.Context,
	identity access.Identity,
	callID string,
) (OperatorTimeline, error) {
	if m.access == nil || callID == "" {
		return OperatorTimeline{}, ErrDenied
	}
	discovery, err := m.access.DiscoverActor(ctx, identity)
	if err != nil || !discovery.PlatformOperator {
		return OperatorTimeline{}, ErrDenied
	}
	result := OperatorTimeline{
		CallID:  callID,
		Entries: []TimelineEntry{},
	}
	if err := m.pool.QueryRow(ctx, `
		SELECT practice_id::text, state, version
		FROM human_calling_calls
		WHERE id = $1
	`, callID).Scan(&result.PracticeID, &result.State, &result.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OperatorTimeline{}, ErrDenied
		}
		return OperatorTimeline{}, fmt.Errorf("read operator Call summary: %w", err)
	}
	rows, err := m.pool.Query(ctx, `
		WITH entries AS (
			SELECT
				t.kind,
				COALESCE(t.opaque_reference, '') AS opaque_reference,
				COALESCE(
					t.error_code,
					command.last_error_code,
					receipt.projection_error_code,
					''
				) AS error_code,
				COALESCE(command.action, '') AS command_action,
				COALESCE(command.state, '') AS command_state,
				COALESCE(command.attempts, 0) AS command_attempts,
				COALESCE(receipt.state, '') AS receipt_state,
				COALESCE(command.created_at, receipt.received_at, t.occurred_at) AS started_at,
				t.occurred_at,
				t.id::text AS stable_id
			FROM human_calling_timeline t
			LEFT JOIN human_calling_provider_commands command
				ON command.id = t.provider_command_id
			LEFT JOIN human_calling_provider_receipts receipt
				ON receipt.event_id = t.provider_event_id
			WHERE t.call_id = $1

			UNION ALL

			SELECT
				'provider.command.committed',
				'',
				COALESCE(command.last_error_code, ''),
				command.action,
				command.state,
				command.attempts,
				'',
				command.created_at,
				command.created_at,
				command.id::text
			FROM human_calling_provider_commands command
			WHERE command.call_id = $1
				AND NOT EXISTS (
					SELECT 1
					FROM human_calling_timeline timeline
					WHERE timeline.provider_command_id = command.id
				)

			UNION ALL

			SELECT
				'provider.receipt.' || lower(receipt.state),
				'',
				COALESCE(receipt.projection_error_code, ''),
				'',
				'',
				0,
				receipt.state,
				receipt.received_at,
				COALESCE(receipt.occurred_at, receipt.received_at),
				receipt.event_id
			FROM human_calling_provider_receipts receipt
			WHERE receipt.call_id = $1
				AND NOT EXISTS (
					SELECT 1
					FROM human_calling_timeline timeline
					WHERE timeline.provider_event_id = receipt.event_id
				)
		)
		SELECT
			kind,
			opaque_reference,
			error_code,
			command_action,
			command_state,
			command_attempts,
			receipt_state,
			GREATEST(
				0,
				EXTRACT(EPOCH FROM ($2::timestamptz - started_at))::bigint
			),
			occurred_at
		FROM entries
		ORDER BY occurred_at, stable_id
	`, callID, m.now())
	if err != nil {
		return OperatorTimeline{}, fmt.Errorf("read operator Call timeline: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry TimelineEntry
		if err := rows.Scan(
			&entry.Kind,
			&entry.OpaqueReference,
			&entry.ErrorCode,
			&entry.CommandAction,
			&entry.CommandState,
			&entry.CommandAttempts,
			&entry.ReceiptState,
			&entry.AgeSeconds,
			&entry.OccurredAt,
		); err != nil {
			return OperatorTimeline{}, fmt.Errorf("scan operator Call timeline: %w", err)
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return OperatorTimeline{}, fmt.Errorf("iterate operator Call timeline: %w", err)
	}
	return result, nil
}

func (m *Module) RecordDisposition(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
	callID string,
	disposition Disposition,
) (DispositionResult, error) {
	if sessionID == "" ||
		m.work == nil ||
		(disposition != DispositionResolved &&
			disposition != DispositionFollowUpRequired) {
		return DispositionResult{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DispositionResult{}, fmt.Errorf("begin Call disposition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := m.access.LockOperationalActor(ctx, tx, identity); err != nil {
		return DispositionResult{}, ErrDenied
	}

	var practiceID, locationID, winnerSubject, claimantSessionID string
	var phone, transferReason string
	var state CallState
	if err := tx.QueryRow(ctx, `
		SELECT
			call.practice_id::text,
			call.location_id::text,
			call.state,
			COALESCE(call.winner_subject, ''),
			COALESCE(call.claimant_session_id, ''),
			COALESCE(handoff.phone, ''),
			COALESCE(handoff.transfer_reason, '')
		FROM human_calling_calls call
		JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id
		WHERE call.id = $1
		FOR UPDATE OF call
	`, callID).Scan(
		&practiceID,
		&locationID,
		&state,
		&winnerSubject,
		&claimantSessionID,
		&phone,
		&transferReason,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DispositionResult{}, ErrDenied
		}
		return DispositionResult{}, fmt.Errorf("lock Call disposition: %w", err)
	}
	if winnerSubject != identity.Subject ||
		claimantSessionID != sessionID {
		return DispositionResult{}, ErrConflict
	}
	authorization, err := m.access.LockMembershipAuthorization(
		ctx,
		tx,
		identity,
		practiceID,
		locationID,
	)
	if err != nil {
		return DispositionResult{}, ErrDenied
	}
	var ownsCurrentLease bool
	if err := tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
		FOR UPDATE
	`, identity.Subject, sessionID, m.now()).Scan(&ownsCurrentLease); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DispositionResult{}, ErrConflict
		}
		return DispositionResult{}, fmt.Errorf("verify disposition lease: %w", err)
	}
	if !ownsCurrentLease {
		return DispositionResult{}, ErrConflict
	}
	nextState := CallResolved
	if disposition == DispositionFollowUpRequired {
		nextState = CallFollowUpRequired
	}
	if state != CallNeedsDisposition && state != nextState {
		return DispositionResult{}, ErrConflict
	}
	taskID := ""
	if disposition == DispositionFollowUpRequired {
		task, err := m.work.EnsureCallFollowUp(
			ctx,
			tx,
			work.EnsureCallFollowUpCommand{
				CallID:     callID,
				PracticeID: practiceID,
				LocationID: locationID,
				Phone:      phone,
				Reason:     transferReason,
				Creator:    authorization.Actor,
			},
		)
		if err != nil {
			return DispositionResult{}, err
		}
		taskID = task.ID
	}
	if state == nextState {
		if err := tx.Commit(ctx); err != nil {
			return DispositionResult{}, fmt.Errorf("commit replayed Call disposition: %w", err)
		}
		call, err := m.ReadCall(ctx, identity, callID)
		if err != nil {
			return DispositionResult{}, err
		}
		return DispositionResult{Call: call, TaskID: taskID}, nil
	}
	dispositionAt := m.now()
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			state = $2,
			disposition_actor_subject = $3,
			disposition_at = $4,
			version = version + 1,
			updated_at = $4
		WHERE id = $1
	`, callID, nextState, identity.Subject, dispositionAt); err != nil {
		return DispositionResult{}, fmt.Errorf("record Call disposition: %w", err)
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"call.dispositioned",
		identity.Subject,
		"",
		"",
		"",
		"",
		dispositionAt,
	); err != nil {
		return DispositionResult{}, err
	}
	if disposition == DispositionResolved {
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
			return DispositionResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return DispositionResult{}, fmt.Errorf("commit Call disposition: %w", err)
	}
	call, err := m.ReadCall(ctx, identity, callID)
	if err != nil {
		return DispositionResult{}, err
	}
	return DispositionResult{Call: call, TaskID: taskID}, nil
}

func (m *Module) RequestHangup(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
	callID string,
) (Call, error) {
	if sessionID == "" {
		return Call{}, ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Call{}, fmt.Errorf("begin Call hangup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := m.access.LockOperationalActor(ctx, tx, identity); err != nil {
		return Call{}, ErrDenied
	}

	var practiceID, locationID, winnerSubject, claimantSessionID, targetID string
	var attemptID string
	var state CallState
	if err := tx.QueryRow(ctx, `
		SELECT
			practice_id::text,
			location_id::text,
			state,
			COALESCE(winner_subject, ''),
			COALESCE(claimant_session_id, ''),
			COALESCE(expected_staff_call_control_id, caller_call_control_id),
			COALESCE(current_attempt_id::text, '')
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, callID).Scan(
		&practiceID,
		&locationID,
		&state,
		&winnerSubject,
		&claimantSessionID,
		&targetID,
		&attemptID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Call{}, ErrDenied
		}
		return Call{}, fmt.Errorf("lock Call hangup: %w", err)
	}
	if state != CallConnected ||
		winnerSubject != identity.Subject ||
		claimantSessionID != sessionID {
		return Call{}, ErrConflict
	}
	if _, err := m.access.LockMembershipAuthorization(
		ctx,
		tx,
		identity,
		practiceID,
		locationID,
	); err != nil {
		return Call{}, ErrDenied
	}
	var ownsCurrentLease bool
	if err := tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
		FOR UPDATE
	`, identity.Subject, sessionID, m.now()).Scan(&ownsCurrentLease); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Call{}, ErrConflict
		}
		return Call{}, fmt.Errorf("verify Call control lease: %w", err)
	}
	if !ownsCurrentLease {
		return Call{}, ErrConflict
	}
	var hangupExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM human_calling_provider_commands
			WHERE call_id = $1
				AND attempt_id = NULLIF($2, '')::uuid
				AND action = 'HANGUP'
				AND target_id = $3
				AND state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED')
		)
	`, callID, attemptID, targetID).Scan(&hangupExists); err != nil {
		return Call{}, fmt.Errorf("check committed Hangup intent: %w", err)
	}
	if !hangupExists {
		if err := ensureHangupCommand(
			ctx,
			tx,
			callID,
			attemptID,
			identity.Subject,
			targetID,
			"staff",
			m.now(),
		); err != nil {
			return Call{}, err
		}
		if err := appendTimeline(
			ctx,
			tx,
			callID,
			practiceID,
			"call.hangup_requested",
			identity.Subject,
			"",
			"",
			"",
			"",
			m.now(),
		); err != nil {
			return Call{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Call{}, fmt.Errorf("commit Call hangup: %w", err)
	}
	return m.ReadCall(ctx, identity, callID)
}

func (m *Module) ExpireOffers(ctx context.Context) (int, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin offer expiry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id::text, practice_id::text, caller_call_control_id
		FROM human_calling_calls
		WHERE state IN ('OFFERING', 'RECONCILING')
			AND claimant_subject IS NULL
			AND offer_deadline <= $1
		ORDER BY offer_deadline, id
		FOR UPDATE SKIP LOCKED
		LIMIT 100
	`, m.now())
	if err != nil {
		return 0, fmt.Errorf("claim expired offers: %w", err)
	}
	type expiredOffer struct {
		callID        string
		practiceID    string
		callControlID string
	}
	expired := []expiredOffer{}
	for rows.Next() {
		var offer expiredOffer
		if err := rows.Scan(
			&offer.callID,
			&offer.practiceID,
			&offer.callControlID,
		); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired offer: %w", err)
		}
		expired = append(expired, offer)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired offers: %w", err)
	}
	rows.Close()

	for _, offer := range expired {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				state = 'UNANSWERED',
				ended_at = $2,
				version = version + 1,
				updated_at = $2
			WHERE id = $1
				AND state IN ('OFFERING', 'RECONCILING')
				AND claimant_subject IS NULL
		`, offer.callID, m.now()); err != nil {
			return 0, fmt.Errorf("expire offered Call: %w", err)
		}
		if err := insertCommand(
			ctx,
			tx,
			offer.callID,
			"",
			CommandHangup,
			offer.callControlID,
			map[string]any{"client_state": opaqueClientState(offer.callID, "caller")},
			m.now(),
		); err != nil {
			return 0, err
		}
		if err := appendTimeline(
			ctx,
			tx,
			offer.callID,
			offer.practiceID,
			"offer.expired",
			"",
			"",
			"",
			"",
			"OFFER_TIMEOUT",
			m.now(),
		); err != nil {
			return 0, err
		}
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, offer.practiceID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit offer expiry: %w", err)
	}
	return len(expired), nil
}

func (m *Module) ExpireConnections(ctx context.Context) (int, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin connection expiry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT
			id::text,
			practice_id::text,
			caller_call_control_id,
			COALESCE(expected_staff_call_control_id, ''),
			offer_deadline,
			connection_deadline
		FROM human_calling_calls
		WHERE state IN ('CONNECTING', 'RECONCILING')
			AND claimant_subject IS NOT NULL
			AND connection_deadline <= $1
		ORDER BY connection_deadline, id
		FOR UPDATE SKIP LOCKED
		LIMIT 100
	`, m.now())
	if err != nil {
		return 0, fmt.Errorf("claim expired connections: %w", err)
	}
	type expiredConnection struct {
		callID             string
		practiceID         string
		callerControl      string
		staffControl       string
		offerDeadline      time.Time
		connectionDeadline time.Time
	}
	expired := []expiredConnection{}
	for rows.Next() {
		var connection expiredConnection
		if err := rows.Scan(
			&connection.callID,
			&connection.practiceID,
			&connection.callerControl,
			&connection.staffControl,
			&connection.offerDeadline,
			&connection.connectionDeadline,
		); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired connection: %w", err)
		}
		expired = append(expired, connection)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired connections: %w", err)
	}
	rows.Close()

	for _, connection := range expired {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_connection_attempts attempt
			SET
				ended_at = COALESCE(attempt.ended_at, $2),
				provider_termination = COALESCE(
					attempt.provider_termination,
					'CONNECTION_TIMEOUT'
				),
				updated_at = $3
			FROM human_calling_calls call
			WHERE call.id = $1
				AND call.current_attempt_id = attempt.id
		`, connection.callID, connection.connectionDeadline, m.now()); err != nil {
			return 0, fmt.Errorf("end expired connection attempt: %w", err)
		}
		stillOfferable := m.now().Before(connection.offerDeadline)
		nextState := CallUnanswered
		termination := "CONNECTION_TIMEOUT"
		endedAtValue := m.now()
		endedAt := &endedAtValue
		var nextDeadline *time.Time
		if stillOfferable {
			nextState = CallReconciling
			termination = "CONNECTION_TIMEOUT_PENDING"
			endedAt = nil
			nextDeadline = &connection.offerDeadline
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				state = $2,
				provider_termination = $3,
				ended_at = $4,
				connection_deadline = $5,
				version = version + 1,
				updated_at = $6
			WHERE id = $1
				AND state IN ('CONNECTING', 'RECONCILING')
				AND claimant_subject IS NOT NULL
		`,
			connection.callID,
			nextState,
			termination,
			endedAt,
			nextDeadline,
			m.now(),
		); err != nil {
			return 0, fmt.Errorf("expire connecting Call: %w", err)
		}
		targets := []struct {
			id  string
			leg string
		}{}
		if !stillOfferable {
			targets = append(targets, struct {
				id  string
				leg string
			}{id: connection.callerControl, leg: "caller"})
		}
		if connection.staffControl != "" {
			targets = append(targets, struct {
				id  string
				leg string
			}{id: connection.staffControl, leg: "staff"})
		}
		for _, target := range targets {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM human_calling_provider_commands
					WHERE call_id = $1
						AND action = 'HANGUP'
						AND target_id = $2
						AND state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED')
				)
			`, connection.callID, target.id).Scan(&exists); err != nil {
				return 0, fmt.Errorf("check connection-timeout hangup: %w", err)
			}
			if !exists {
				if err := insertCommand(
					ctx,
					tx,
					connection.callID,
					"",
					CommandHangup,
					target.id,
					map[string]any{
						"client_state": opaqueClientState(connection.callID, target.leg),
					},
					m.now(),
				); err != nil {
					return 0, err
				}
			}
		}
		if err := appendTimeline(
			ctx,
			tx,
			connection.callID,
			connection.practiceID,
			"connection.timeout",
			"",
			"",
			"",
			"",
			termination,
			m.now(),
		); err != nil {
			return 0, err
		}
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, connection.practiceID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit connection expiry: %w", err)
	}
	return len(expired), nil
}

func (m *Module) RecoverInterruptedCommands(ctx context.Context) error {
	now := m.now()
	if _, err := m.pool.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = 'PENDING',
			last_error_code = 'SAFE_SAME_ID_RETRY',
			next_attempt_at = $1,
			updated_at = $1
		WHERE state = 'AMBIGUOUS'
			AND action = 'HANGUP'
			AND next_attempt_at <= $1
	`, now); err != nil {
		return fmt.Errorf("schedule ambiguous Hangup reconciliation: %w", err)
	}

	type interruptedCommand struct {
		id        string
		callID    *string
		attemptID string
		action    CommandAction
		updatedAt time.Time
	}
	var command interruptedCommand
	err := m.pool.QueryRow(ctx, `
		SELECT
			id::text,
			call_id::text,
			COALESCE(attempt_id::text, ''),
			action,
			updated_at
		FROM human_calling_provider_commands
		WHERE state = 'SENDING'
			AND updated_at <= $1
		ORDER BY updated_at, id
		LIMIT 1
	`, now.Add(-interruptedCommandTimeout)).Scan(
		&command.id,
		&command.callID,
		&command.attemptID,
		&command.action,
		&command.updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find interrupted provider command: %w", err)
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin interrupted provider command recovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	practiceID := ""
	if command.callID != nil {
		if err := tx.QueryRow(ctx, `
			SELECT practice_id::text
			FROM human_calling_calls
			WHERE id = $1
			FOR UPDATE
		`, *command.callID).Scan(&practiceID); err != nil {
			return fmt.Errorf("load interrupted provider Call: %w", err)
		}
	}

	safeRetry := command.callID != nil &&
		(command.action == CommandHangup ||
			!command.updatedAt.Before(now.Add(-safeProviderRetryWindow)))
	nextState := "AMBIGUOUS"
	errorCode := "DUPLICATE_SUPPRESSION_EXPIRED"
	timelineKind := "provider.command.retry_window_expired"
	if command.callID == nil {
		errorCode = "WORKER_INTERRUPTED"
		timelineKind = "provider.command.interrupted"
	} else if safeRetry {
		nextState = "PENDING"
		errorCode = "SAFE_SAME_ID_RETRY"
		timelineKind = "provider.command.safe_same_id_retry"
	}
	tag, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = $2,
			last_error_code = $3,
			next_attempt_at = CASE WHEN $2 = 'PENDING' THEN $4 ELSE next_attempt_at END,
			updated_at = $4
		WHERE id = $1
			AND state = 'SENDING'
			AND updated_at = $5
	`, command.id, nextState, errorCode, now, command.updatedAt)
	if err != nil {
		return fmt.Errorf("recover interrupted provider command: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	if command.callID != nil &&
		command.action == CommandDialStaff &&
		!safeRetry {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET state = 'RECONCILING', version = version + 1, updated_at = $2
			WHERE id = $1
				AND current_attempt_id = NULLIF($3, '')::uuid
				AND state = 'CONNECTING'
		`, *command.callID, now, command.attemptID); err != nil {
			return fmt.Errorf("mark expired interrupted Dial reconciling: %w", err)
		}
	}

	if command.callID != nil {
		if err := appendTimeline(
			ctx,
			tx,
			*command.callID,
			practiceID,
			timelineKind,
			"",
			"",
			command.id,
			"",
			errorCode,
			now,
		); err != nil {
			return err
		}
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit interrupted provider command recovery: %w", err)
	}
	return nil
}

func (m *Module) ReconcileConfirmedHangups(ctx context.Context) (int, error) {
	provider, ok := m.provider.(CallStateProvider)
	if !ok {
		return 0, nil
	}
	now := m.now()
	claim, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin Hangup reconciliation claim: %w", err)
	}
	defer func() { _ = claim.Rollback(ctx) }()
	type pendingHangup struct {
		callID      string
		attemptID   string
		commandID   string
		targetID    string
		commandSent time.Time
	}
	var item pendingHangup
	err = claim.QueryRow(ctx, `
		WITH candidate AS (
			SELECT command.id
			FROM human_calling_provider_commands command
			JOIN human_calling_calls call ON call.id = command.call_id
			WHERE command.action = 'HANGUP'
				AND command.state = 'SENT'
				AND command.updated_at <= $1
				AND command.next_attempt_at <= $2
				AND call.state = 'CONNECTED'
				AND command.target_id = COALESCE(
					call.expected_staff_call_control_id,
					call.caller_call_control_id
				)
				AND command.attempt_id = call.current_attempt_id
			ORDER BY command.next_attempt_at, command.updated_at, command.id
			FOR UPDATE OF command SKIP LOCKED
			LIMIT 1
		)
		UPDATE human_calling_provider_commands command
		SET next_attempt_at = $3
		FROM candidate
		WHERE command.id = candidate.id
		RETURNING
			command.call_id::text,
			command.attempt_id::text,
			command.id::text,
			command.target_id,
			command.updated_at
	`,
		now.Add(-hangupReconciliationDelay),
		now,
		now.Add(hangupReconciliationDelay),
	).Scan(
		&item.callID,
		&item.attemptID,
		&item.commandID,
		&item.targetID,
		&item.commandSent,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := claim.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit empty Hangup reconciliation claim: %w", err)
		}
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("claim Hangup awaiting provider state: %w", err)
	}
	if err := claim.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit Hangup reconciliation claim: %w", err)
	}

	alive, err := provider.IsCallAlive(ctx, item.targetID)
	if err != nil {
		return 0, fmt.Errorf("read exact provider Call state: %w", err)
	}
	if alive {
		return 0, nil
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin absent provider Call projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID string
	err = tx.QueryRow(ctx, `
		UPDATE human_calling_calls
		SET
			state = 'NEEDS_DISPOSITION',
			provider_termination = 'PROVIDER_CONFIRMED_NOT_ALIVE',
			ended_at = $2,
			version = version + 1,
			updated_at = $2
		WHERE id = $1
			AND current_attempt_id = $3
			AND state = 'CONNECTED'
		RETURNING practice_id::text
	`, item.callID, now, item.attemptID).Scan(&practiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit superseded provider Call state: %w", err)
		}
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("project absent provider Call: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', last_error_code = NULL, updated_at = $2
		WHERE id = $1 AND state = 'SENT'
	`, item.commandID, now); err != nil {
		return 0, fmt.Errorf("reconcile provider-confirmed Hangup: %w", err)
	}
	if err := appendTimeline(
		ctx,
		tx,
		item.callID,
		practiceID,
		"call.hangup_provider_confirmed",
		"",
		"",
		item.commandID,
		"",
		"PROVIDER_CONFIRMED_NOT_ALIVE",
		item.commandSent,
	); err != nil {
		return 0, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit provider-confirmed Hangup: %w", err)
	}
	return 1, nil
}

func (m *Module) ProcessNextCommand(ctx context.Context) (bool, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin provider command claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		WITH obsolete AS (
			UPDATE human_calling_provider_commands command
			SET
				state = 'FAILED',
				last_error_code = 'MEMBERSHIP_STATE_OBSOLETE',
				updated_at = $1
			WHERE command.state = 'PENDING'
				AND (
					(
						command.action = 'DISABLE_CREDENTIAL'
						AND EXISTS (
							SELECT 1
							FROM access_operational_users operational
							WHERE operational.user_subject = command.user_subject
						)
					)
					OR (
						command.action = 'CREATE_CREDENTIAL'
						AND NOT EXISTS (
							SELECT 1
							FROM access_operational_users operational
							WHERE operational.user_subject = command.user_subject
						)
					)
				)
			RETURNING user_subject, action
		)
		UPDATE human_calling_credentials credential
		SET
			state = CASE
				WHEN obsolete.action = 'DISABLE_CREDENTIAL'
					AND credential.provider_credential_id IS NOT NULL
					THEN 'ACTIVE'
				ELSE 'DISABLED'
			END,
			last_error_code = NULL,
			updated_at = $1
		FROM obsolete
		WHERE credential.user_subject = obsolete.user_subject
	`, m.now()); err != nil {
		return false, fmt.Errorf("fence obsolete credential command at claim: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		WITH obsolete AS (
		UPDATE human_calling_provider_commands command
		SET
			state = 'FAILED',
			last_error_code = 'CALL_STATE_OBSOLETE',
			updated_at = $1
		FROM human_calling_calls call
		WHERE command.call_id = call.id
			AND command.state = 'PENDING'
			AND (
				(command.action = 'ANSWER_CALLER'
					AND call.state NOT IN ('OFFERING', 'CONNECTING', 'RECONCILING'))
				OR (command.action = 'START_RINGBACK'
					AND call.state NOT IN ('OFFERING', 'CONNECTING', 'RECONCILING'))
				OR (command.action = 'DIAL_STAFF'
					AND (
						call.state NOT IN ('CONNECTING', 'RECONCILING')
						OR command.attempt_id IS DISTINCT FROM call.current_attempt_id
						OR EXISTS (
							SELECT 1
							FROM human_calling_connection_attempts attempt
							WHERE attempt.id = command.attempt_id
								AND attempt.ended_at IS NOT NULL
						)
					))
				OR (command.action = 'START_RECORDING'
					AND call.state <> 'CONNECTED')
			)
		RETURNING command.call_id, command.action
		)
		UPDATE human_calling_recordings recording
		SET
			state = 'FAILED',
			failure_code = 'RECORDING_START_OBSOLETE',
			updated_at = $1
		FROM obsolete
		WHERE obsolete.action = 'START_RECORDING'
			AND recording.call_id = obsolete.call_id
			AND recording.state = 'INTENDED'
	`, m.now()); err != nil {
		return false, fmt.Errorf("fence obsolete provider commands: %w", err)
	}

	var command ProviderCommand
	var callID, userSubject *string
	var encoded []byte
	// The parent Call row is the durable serialization key. Credential commands
	// have no Call, so they keep their independent SKIP LOCKED claim path.
	err = tx.QueryRow(ctx, `
		WITH call_candidate AS MATERIALIZED (
			SELECT command.id, command.created_at
			FROM human_calling_provider_commands command
			JOIN human_calling_calls call ON call.id = command.call_id
			WHERE command.state = 'PENDING'
				AND command.next_attempt_at <= $1
				AND command.action <> 'CREATE_JWT'
				AND (
					command.depends_on_command_id IS NULL
					OR EXISTS (
						SELECT 1
						FROM human_calling_provider_commands dependency
						WHERE dependency.id = command.depends_on_command_id
							AND dependency.state IN ('SENT', 'RECONCILED')
					)
				)
				AND NOT EXISTS (
					SELECT 1
					FROM human_calling_provider_commands active
					WHERE active.call_id = command.call_id
						AND active.state IN ('SENDING', 'AMBIGUOUS')
				)
			ORDER BY command.created_at, command.id
			FOR UPDATE OF call, command SKIP LOCKED
			LIMIT 1
		),
		credential_candidate AS MATERIALIZED (
			SELECT command.id, command.created_at
			FROM human_calling_provider_commands command
			WHERE command.call_id IS NULL
				AND command.state = 'PENDING'
				AND command.next_attempt_at <= $1
				AND command.action <> 'CREATE_JWT'
				AND (
					command.depends_on_command_id IS NULL
					OR EXISTS (
						SELECT 1
						FROM human_calling_provider_commands dependency
						WHERE dependency.id = command.depends_on_command_id
							AND dependency.state IN ('SENT', 'RECONCILED')
					)
				)
			ORDER BY command.created_at, command.id
			FOR UPDATE OF command SKIP LOCKED
			LIMIT 1
		),
		candidate AS (
			SELECT id, created_at FROM call_candidate
			UNION ALL
			SELECT id, created_at FROM credential_candidate
			ORDER BY created_at, id
			LIMIT 1
		)
		SELECT
			command.id::text,
			COALESCE(command.attempt_id::text, ''),
			command.call_id::text,
			command.user_subject,
			command.action,
			COALESCE(command.target_id, ''),
			command.payload
		FROM candidate
		JOIN human_calling_provider_commands command ON command.id = candidate.id
	`, m.now()).Scan(
		&command.ID,
		&command.AttemptID,
		&callID,
		&userSubject,
		&command.Action,
		&command.TargetID,
		&encoded,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit empty provider command claim: %w", err)
			}
			return false, nil
		}
		return false, fmt.Errorf("claim provider command: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = 'SENDING',
			attempts = attempts + 1,
			payload = CASE
				WHEN action = 'ANSWER_CALLER' AND NOT payload ? 'transcription'
					THEN payload || '{"transcription":false}'::jsonb
				ELSE payload
			END,
			updated_at = $2
		WHERE id = $1
		RETURNING payload
	`, command.ID, m.now()).Scan(&encoded); err != nil {
		return false, fmt.Errorf("mark provider command sending: %w", err)
	}
	if err := json.Unmarshal(encoded, &command.Payload); err != nil {
		return false, fmt.Errorf("decode provider command: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit provider command claim: %w", err)
	}

	result, executeErr := m.provider.Execute(ctx, command)
	if err := m.finishCommand(ctx, command, callID, userSubject, result, executeErr); err != nil {
		return true, err
	}
	return true, nil
}

func (m *Module) processCommand(
	ctx context.Context,
	commandID string,
) (ProviderResult, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProviderResult{}, fmt.Errorf("begin provider command: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var command ProviderCommand
	var callID, userSubject *string
	var encoded []byte
	if err := tx.QueryRow(ctx, `
		SELECT
			id::text,
			COALESCE(attempt_id::text, ''),
			call_id::text,
			user_subject,
			action,
			COALESCE(target_id, ''),
			payload
		FROM human_calling_provider_commands
		WHERE id = $1 AND state = 'PENDING'
		FOR UPDATE
	`, commandID).Scan(
		&command.ID,
		&command.AttemptID,
		&callID,
		&userSubject,
		&command.Action,
		&command.TargetID,
		&encoded,
	); err != nil {
		return ProviderResult{}, fmt.Errorf("claim committed provider command: %w", err)
	}
	if err := json.Unmarshal(encoded, &command.Payload); err != nil {
		return ProviderResult{}, fmt.Errorf("decode committed provider command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'SENDING', attempts = attempts + 1, updated_at = $2
		WHERE id = $1
	`, command.ID, m.now()); err != nil {
		return ProviderResult{}, fmt.Errorf("mark committed provider command sending: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProviderResult{}, fmt.Errorf("commit provider command claim: %w", err)
	}
	result, executeErr := m.provider.Execute(ctx, command)
	if err := m.finishCommand(
		ctx,
		command,
		callID,
		userSubject,
		result,
		executeErr,
	); err != nil {
		return ProviderResult{}, err
	}
	if executeErr != nil {
		return ProviderResult{}, executeErr
	}
	return result, nil
}

func (m *Module) finishCommand(
	ctx context.Context,
	command ProviderCommand,
	callID *string,
	userSubject *string,
	result ProviderResult,
	executeErr error,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin provider command result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	callPracticeID := ""
	if callID != nil {
		// Provider facts and command results always lock Call first. Keeping one
		// order prevents concurrent webhooks and completions from deadlocking.
		if err := tx.QueryRow(ctx, `
			SELECT practice_id::text
			FROM human_calling_calls
			WHERE id = $1
			FOR UPDATE
		`, *callID).Scan(&callPracticeID); err != nil {
			return fmt.Errorf("lock provider command Call: %w", err)
		}
	}

	state := "SENT"
	errorCode := ""
	if executeErr != nil {
		errorCode = "PROVIDER_EFFECT_UNCERTAIN"
		state = "AMBIGUOUS"
		if errors.Is(executeErr, ErrDefinitiveProviderFailure) {
			errorCode = "PROVIDER_REJECTED"
			state = "FAILED"
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = $2,
			last_error_code = NULLIF($3, ''),
			sent_at = CASE WHEN $2 = 'SENT' THEN $4 ELSE sent_at END,
			next_attempt_at = CASE
				WHEN $2 = 'AMBIGUOUS' THEN $4 + interval '5 seconds'
				ELSE next_attempt_at
			END,
			updated_at = $4
		WHERE id = $1 AND state = 'SENDING'
	`, command.ID, state, errorCode, m.now())
	if err != nil {
		return fmt.Errorf("record provider command result: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if callID != nil && command.Action == CommandAnswerCaller && executeErr != nil {
		if state == "AMBIGUOUS" {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET state = 'RECONCILING', version = version + 1, updated_at = $2
				WHERE id = $1 AND state IN ('OFFERING', 'CONNECTING')
			`, *callID, m.now()); err != nil {
				return fmt.Errorf("mark ambiguous caller answer reconciling: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET
					state = 'UNANSWERED',
					provider_termination = 'ANSWER_REJECTED',
					ended_at = $2,
					version = version + 1,
					updated_at = $2
				WHERE id = $1 AND state IN ('OFFERING', 'CONNECTING', 'RECONCILING')
			`, *callID, m.now()); err != nil {
				return fmt.Errorf("terminate definitively rejected caller answer: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_connection_attempts
				SET
					ended_at = COALESCE(ended_at, $2),
					provider_termination = 'ANSWER_REJECTED',
					updated_at = $2
				WHERE id = (
					SELECT current_attempt_id
					FROM human_calling_calls
					WHERE id = $1
				)
			`, *callID, m.now()); err != nil {
				return fmt.Errorf("end rejected connection attempt: %w", err)
			}
			if err := insertCommand(
				ctx,
				tx,
				*callID,
				"",
				CommandHangup,
				command.TargetID,
				map[string]any{
					"client_state": opaqueClientState(*callID, "caller"),
				},
				m.now(),
			); err != nil {
				return err
			}
		}
	}
	if callID != nil && command.Action == CommandStartRingback && executeErr != nil {
		if state == "AMBIGUOUS" {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET state = 'RECONCILING', version = version + 1, updated_at = $2
				WHERE id = $1 AND state IN ('OFFERING', 'CONNECTING')
			`, *callID, m.now()); err != nil {
				return fmt.Errorf("mark ambiguous ringback reconciling: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET
					state = 'UNANSWERED',
					provider_termination = 'RINGBACK_REJECTED',
					ended_at = $2,
					version = version + 1,
					updated_at = $2
				WHERE id = $1
					AND state IN ('OFFERING', 'CONNECTING', 'RECONCILING')
			`, *callID, m.now()); err != nil {
				return fmt.Errorf("terminate definitively rejected ringback: %w", err)
			}
			if err := insertCommand(
				ctx,
				tx,
				*callID,
				"",
				CommandHangup,
				command.TargetID,
				map[string]any{
					"client_state": opaqueClientState(*callID, "caller"),
				},
				m.now(),
			); err != nil {
				return err
			}
		}
	}
	if callID != nil && command.Action == CommandDialStaff {
		if command.AttemptID == "" {
			return fmt.Errorf("Dial command omitted connection attempt identity")
		}
		if executeErr == nil {
			if result.CallControlID == "" || result.CallLegID == "" {
				return fmt.Errorf("successful Dial omitted provider leg identity")
			}
			var attemptEndedAt *time.Time
			if err := tx.QueryRow(ctx, `
				UPDATE human_calling_connection_attempts
				SET
					staff_call_control_id = COALESCE(staff_call_control_id, $2),
					staff_call_leg_id = COALESCE(staff_call_leg_id, $3),
					updated_at = $4
				WHERE id = $1
				RETURNING ended_at
			`,
				command.AttemptID,
				result.CallControlID,
				result.CallLegID,
				m.now(),
			).Scan(&attemptEndedAt); err != nil {
				return fmt.Errorf("record connection-attempt staff leg: %w", err)
			}
			callTag, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET
					expected_staff_call_control_id = $2,
					expected_staff_call_leg_id = $3,
					provider_termination = NULL,
					ended_at = NULL,
					version = version + 1,
					updated_at = $4
				WHERE id = $1
					AND current_attempt_id = $5
					AND state IN ('CONNECTING', 'RECONCILING')
					AND $6::timestamptz IS NULL
			`,
				*callID,
				result.CallControlID,
				result.CallLegID,
				m.now(),
				command.AttemptID,
				attemptEndedAt,
			)
			if err != nil {
				return fmt.Errorf("record expected staff provider leg: %w", err)
			}
			if callTag.RowsAffected() == 0 {
				var alreadyConnected bool
				if err := tx.QueryRow(ctx, `
					SELECT EXISTS (
						SELECT 1
						FROM human_calling_calls
						WHERE id = $1
							AND current_attempt_id = $2
							AND state = 'CONNECTED'
							AND expected_staff_call_control_id = $3
							AND expected_staff_call_leg_id = $4
					)
				`,
					*callID,
					command.AttemptID,
					result.CallControlID,
					result.CallLegID,
				).Scan(&alreadyConnected); err != nil {
					return fmt.Errorf("reconcile Dial result after bridge: %w", err)
				}
				if !alreadyConnected {
					cleanupOwner := ""
					if userSubject != nil {
						cleanupOwner = *userSubject
					}
					if err := ensureHangupCommand(
						ctx,
						tx,
						*callID,
						command.AttemptID,
						cleanupOwner,
						result.CallControlID,
						"staff",
						m.now(),
					); err != nil {
						return err
					}
				}
			}
		} else if state == "AMBIGUOUS" {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET state = 'RECONCILING', version = version + 1, updated_at = $2
				WHERE id = $1
					AND current_attempt_id = $3
					AND state = 'CONNECTING'
			`, *callID, m.now(), command.AttemptID); err != nil {
				return fmt.Errorf("mark ambiguous Call reconciling: %w", err)
			}
		} else {
			var deadline time.Time
			if err := tx.QueryRow(ctx, `
				SELECT offer_deadline
				FROM human_calling_calls
				WHERE id = $1
			`, *callID).Scan(&deadline); err != nil {
				return fmt.Errorf("load definitively failed Call: %w", err)
			}
			nextState := CallUnanswered
			if m.now().Before(deadline) {
				nextState = CallOffering
			}
			callTag, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET
					state = $2,
					claimant_subject = NULL,
					claimant_session_id = NULL,
					expected_staff_call_control_id = NULL,
					expected_staff_call_leg_id = NULL,
					provider_termination = CASE
						WHEN $2 = 'OFFERING' THEN NULL
						ELSE 'DIAL_REJECTED'
					END,
					ended_at = CASE
						WHEN $2 = 'OFFERING' THEN NULL
						ELSE $3::timestamptz
					END,
					version = version + 1,
					updated_at = $3
				WHERE id = $1
					AND current_attempt_id = $4
					AND state = 'CONNECTING'
			`, *callID, nextState, m.now(), command.AttemptID)
			if err != nil {
				return fmt.Errorf("reopen definitively failed Call: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_connection_attempts
				SET
					ended_at = COALESCE(ended_at, $2),
					provider_termination = 'DIAL_REJECTED',
					updated_at = $2
				WHERE id = $1
			`, command.AttemptID, m.now()); err != nil {
				return fmt.Errorf("end rejected connection attempt: %w", err)
			}
			if callTag.RowsAffected() == 1 && nextState == CallUnanswered {
				if err := insertCommand(
					ctx,
					tx,
					*callID,
					"",
					CommandHangup,
					command.TargetID,
					map[string]any{
						"client_state": opaqueClientState(*callID, "caller"),
					},
					m.now(),
				); err != nil {
					return err
				}
			}
			if _, err := m.access.RecordWorkspaceChange(ctx, tx, callPracticeID); err != nil {
				return err
			}
		}
	}
	if userSubject != nil && command.Action == CommandCreateCredential {
		var operational bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM access_operational_users
				WHERE user_subject = $1
			)
		`, *userSubject).Scan(&operational); err != nil {
			return fmt.Errorf("read credential owner after create: %w", err)
		}
		credentialState := "ACTIVE"
		credentialError := errorCode
		if state == "AMBIGUOUS" {
			credentialState = "PENDING"
		} else if executeErr != nil {
			credentialState = "FAILED"
		} else if !operational {
			credentialState = "DISABLING"
			credentialError = "ACCESS_OBSOLETE_AFTER_CREATE"
		}
		if executeErr == nil && (result.CredentialID == "" || result.SIPUsername == "") {
			return fmt.Errorf("successful credential command omitted provider identity")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_credentials
			SET
				state = $2,
				provider_credential_id = CASE
					WHEN $2 IN ('ACTIVE', 'DISABLING') THEN $3
					ELSE provider_credential_id
				END,
				provider_sip_username = CASE
					WHEN $2 IN ('ACTIVE', 'DISABLING') THEN $4
					ELSE provider_sip_username
				END,
				last_error_code = NULLIF($5, ''),
				updated_at = $6
			WHERE user_subject = $1
		`,
			*userSubject,
			credentialState,
			result.CredentialID,
			result.SIPUsername,
			credentialError,
			m.now(),
		); err != nil {
			return fmt.Errorf("record managed credential result: %w", err)
		}
		if credentialState == "DISABLING" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO human_calling_provider_commands (
					id, user_subject, action, target_id, next_attempt_at
				)
				SELECT $1, $2, 'DISABLE_CREDENTIAL', $3, $4
				WHERE NOT EXISTS (
					SELECT 1
					FROM human_calling_provider_commands
					WHERE user_subject = $2
						AND action = 'DISABLE_CREDENTIAL'
						AND state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
				)
			`, uuid.NewString(), *userSubject, result.CredentialID, m.now()); err != nil {
				return fmt.Errorf("commit obsolete credential cleanup: %w", err)
			}
		}
	}
	if userSubject != nil && command.Action == CommandDisableCredential {
		var authorized bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM access_operational_users
				WHERE user_subject = $1
			)
		`, *userSubject).Scan(&authorized); err != nil {
			return fmt.Errorf("read credential owner after disable: %w", err)
		}
		credentialState := "DISABLED"
		if state == "AMBIGUOUS" {
			credentialState = "DISABLING"
		} else if executeErr != nil {
			credentialState = "FAILED"
		}
		clearProviderIdentity := false
		if authorized {
			switch {
			case executeErr == nil:
				credentialState = "PENDING"
				clearProviderIdentity = true
			case state == "FAILED":
				credentialState = "ACTIVE"
				errorCode = ""
			default:
				credentialState = "DISABLING"
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_credentials
			SET
				state = $2,
				provider_credential_id = CASE
					WHEN $5 THEN NULL
					ELSE provider_credential_id
				END,
				provider_sip_username = CASE
					WHEN $5 THEN NULL
					ELSE provider_sip_username
				END,
				last_error_code = NULLIF($3, ''),
				updated_at = $4
			WHERE user_subject = $1
		`,
			*userSubject,
			credentialState,
			errorCode,
			m.now(),
			clearProviderIdentity,
		); err != nil {
			return fmt.Errorf("record disabled credential result: %w", err)
		}
	}
	if callID != nil {
		if state == "FAILED" {
			if _, err := tx.Exec(ctx, `
				WITH RECURSIVE descendants AS (
					SELECT id
					FROM human_calling_provider_commands
					WHERE depends_on_command_id = $1
					UNION ALL
					SELECT command.id
					FROM human_calling_provider_commands command
					JOIN descendants parent
						ON command.depends_on_command_id = parent.id
				)
				UPDATE human_calling_provider_commands command
				SET
					state = 'FAILED',
					last_error_code = 'DEPENDENCY_FAILED',
					updated_at = $2
				WHERE command.id IN (SELECT id FROM descendants)
					AND command.state = 'PENDING'
			`, command.ID, m.now()); err != nil {
				return fmt.Errorf("fail dependent provider commands: %w", err)
			}
		}
		if command.Action == CommandStartRecording && executeErr != nil {
			recordingError := "PROVIDER_RECORDING_REJECTED"
			if state == "AMBIGUOUS" {
				recordingError = "RECORDING_EFFECT_UNCERTAIN"
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_recordings
				SET state = 'FAILED', failure_code = $2, updated_at = $3
				WHERE call_id = $1 AND state IN ('INTENDED', 'RECORDING')
			`, *callID, recordingError, m.now()); err != nil {
				return fmt.Errorf("record recording command failure: %w", err)
			}
		}
		if err := appendTimeline(
			ctx,
			tx,
			*callID,
			callPracticeID,
			"provider.command."+strings.ToLower(state),
			"",
			"",
			command.ID,
			"",
			errorCode,
			m.now(),
		); err != nil {
			return err
		}
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, callPracticeID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit provider command result: %w", err)
	}
	return nil
}

func (m *Module) applyStaffInitiated(
	ctx context.Context,
	fact ProviderFact,
	callID string,
) error {
	if fact.CallControlID == "" || fact.CallLegID == "" || fact.CallSessionID == "" {
		return ErrConflict
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin staff leg projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}

	var practiceID, callSessionID, currentClaimant, currentSession string
	var state CallState
	if err := tx.QueryRow(ctx, `
		SELECT
			practice_id::text,
			state,
			call_session_id,
			COALESCE(claimant_subject, ''),
			COALESCE(claimant_session_id, '')
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, callID).Scan(
		&practiceID,
		&state,
		&callSessionID,
		&currentClaimant,
		&currentSession,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("correlate initiated staff leg: %w", err)
	}
	if callSessionID != fact.CallSessionID {
		return ErrConflict
	}
	clientState, _ := parseOpaqueClientState(fact.ClientState)

	var attemptID, claimantSubject, claimantSession string
	var attemptEndedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT
			id::text,
			claimant_subject,
			claimant_session_id,
			ended_at
		FROM human_calling_connection_attempts
		WHERE call_id = $1
			AND (
				($5 <> '' AND id = NULLIF($5, '')::uuid)
				OR (
					$5 = ''
					AND created_at <= $2
					AND $2 <= COALESCE(ended_at, connection_deadline)
				)
			)
			AND (
				staff_call_control_id IS NULL
				OR (
					staff_call_control_id = $3
					AND staff_call_leg_id = $4
				)
			)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
		FOR UPDATE
	`,
		callID,
		fact.OccurredAt,
		fact.CallControlID,
		fact.CallLegID,
		clientState.AttemptID,
	).Scan(
		&attemptID,
		&claimantSubject,
		&claimantSession,
		&attemptEndedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("correlate initiated staff attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_connection_attempts
		SET
			staff_call_control_id = COALESCE(staff_call_control_id, $2),
			staff_call_leg_id = COALESCE(staff_call_leg_id, $3),
			updated_at = $4
		WHERE id = $1
	`, attemptID, fact.CallControlID, fact.CallLegID, m.now()); err != nil {
		return fmt.Errorf("project initiated staff attempt: %w", err)
	}

	var currentAttemptID string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(current_attempt_id::text, '')
		FROM human_calling_calls
		WHERE id = $1
	`, callID).Scan(&currentAttemptID); err != nil {
		return fmt.Errorf("read current connection attempt: %w", err)
	}
	currentActiveAttempt := currentAttemptID == attemptID &&
		attemptEndedAt == nil &&
		(state == CallConnecting || state == CallReconciling)
	if currentActiveAttempt {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				state = CASE WHEN state = 'RECONCILING' THEN 'CONNECTING' ELSE state END,
				expected_staff_call_control_id = COALESCE(expected_staff_call_control_id, $2),
				expected_staff_call_leg_id = COALESCE(expected_staff_call_leg_id, $3),
				version = version + 1,
				updated_at = $4
			WHERE id = $1
				AND current_attempt_id = $5
				AND state IN ('CONNECTING', 'RECONCILING')
		`, callID, fact.CallControlID, fact.CallLegID, m.now(), attemptID); err != nil {
			return fmt.Errorf("project initiated current staff leg: %w", err)
		}
	}
	if attemptEndedAt != nil || state == CallUnanswered {
		if err := ensureHangupCommand(
			ctx,
			tx,
			callID,
			attemptID,
			claimantSubject,
			fact.CallControlID,
			"staff",
			m.now(),
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = 'RECONCILED',
			sent_at = COALESCE(sent_at, $2),
			last_error_code = NULL,
			updated_at = $2
		WHERE call_id = $1
			AND attempt_id = $3
			AND action = 'DIAL_STAFF'
			AND state IN ('SENDING', 'AMBIGUOUS')
	`, callID, fact.OccurredAt, attemptID); err != nil {
		return fmt.Errorf("reconcile initiated staff Dial: %w", err)
	}
	timelineKind := "provider.staff_leg.initiated"
	if fact.Type == FactCallAnswered {
		timelineKind = "provider.staff_leg.answered"
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		timelineKind,
		claimantSubject,
		fact.EventID,
		"",
		opaqueReference(fact.CallLegID),
		"",
		fact.OccurredAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit initiated staff leg: %w", err)
	}
	return nil
}

func (m *Module) applyCallerAnswered(ctx context.Context, fact ProviderFact) error {
	if fact.CallControlID == "" || fact.CallSessionID == "" {
		return ErrConflict
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin caller answer projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}
	var callID, practiceID string
	var state CallState
	var deadline time.Time
	var claimantSubject string
	if err := tx.QueryRow(ctx, `
		SELECT
			id::text,
			practice_id::text,
			state,
			offer_deadline,
			COALESCE(claimant_subject, '')
		FROM human_calling_calls
		WHERE caller_call_control_id = $1 AND call_session_id = $2
		FOR UPDATE
	`, fact.CallControlID, fact.CallSessionID).Scan(
		&callID,
		&practiceID,
		&state,
		&deadline,
		&claimantSubject,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("correlate caller answer: %w", err)
	}
	var reconciledAnswer bool
	if err := tx.QueryRow(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = 'RECONCILED',
			sent_at = COALESCE(sent_at, $2),
			last_error_code = NULL,
			updated_at = $2
		WHERE call_id = $1
			AND action = 'ANSWER_CALLER'
			AND state IN ('SENDING', 'AMBIGUOUS')
		RETURNING true
	`, callID, fact.OccurredAt).Scan(&reconciledAnswer); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("reconcile caller answer: %w", err)
	}
	if reconciledAnswer && state == CallReconciling && claimantSubject == "" {
		if m.now().Before(deadline) {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET state = 'OFFERING', version = version + 1, updated_at = $2
				WHERE id = $1 AND state = 'RECONCILING' AND claimant_subject IS NULL
			`, callID, m.now()); err != nil {
				return fmt.Errorf("reopen reconciled caller answer: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET state = 'UNANSWERED', ended_at = $2,
					version = version + 1, updated_at = $2
				WHERE id = $1 AND state = 'RECONCILING' AND claimant_subject IS NULL
			`, callID, m.now()); err != nil {
				return fmt.Errorf("expire reconciled caller answer: %w", err)
			}
			if err := insertCommand(
				ctx,
				tx,
				callID,
				"",
				CommandHangup,
				fact.CallControlID,
				map[string]any{
					"client_state": opaqueClientState(callID, "caller"),
				},
				m.now(),
			); err != nil {
				return err
			}
		}
	} else if reconciledAnswer && state == CallReconciling {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET state = 'CONNECTING', version = version + 1, updated_at = $2
			WHERE id = $1 AND state = 'RECONCILING' AND claimant_subject IS NOT NULL
		`, callID, m.now()); err != nil {
			return fmt.Errorf("resume reconciled accepted Call: %w", err)
		}
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"provider.caller.answered",
		"",
		fact.EventID,
		"",
		opaqueReference(fact.CallLegID),
		"",
		fact.OccurredAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit caller answer: %w", err)
	}
	return nil
}

func (m *Module) applyRingbackStarted(ctx context.Context, fact ProviderFact) error {
	if fact.CallControlID == "" || fact.CallSessionID == "" {
		return ErrConflict
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin ringback projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}
	var callID, practiceID, claimantSubject string
	var state CallState
	var deadline time.Time
	if err := tx.QueryRow(ctx, `
		SELECT
			id::text,
			practice_id::text,
			state,
			offer_deadline,
			COALESCE(claimant_subject, '')
		FROM human_calling_calls
		WHERE caller_call_control_id = $1 AND call_session_id = $2
		FOR UPDATE
	`, fact.CallControlID, fact.CallSessionID).Scan(
		&callID,
		&practiceID,
		&state,
		&deadline,
		&claimantSubject,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("correlate ringback evidence: %w", err)
	}
	var reconciled bool
	if err := tx.QueryRow(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = 'RECONCILED',
			sent_at = COALESCE(sent_at, $2),
			last_error_code = NULL,
			updated_at = $2
		WHERE call_id = $1
			AND action = 'START_RINGBACK'
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
		RETURNING true
	`, callID, fact.OccurredAt).Scan(&reconciled); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("reconcile ringback command: %w", err)
	}
	if reconciled && state == CallReconciling {
		nextState := CallConnecting
		if claimantSubject == "" {
			nextState = CallOffering
			if !m.now().Before(deadline) {
				nextState = CallUnanswered
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				state = $2,
				ended_at = CASE WHEN $2 = 'UNANSWERED' THEN $3 ELSE ended_at END,
				version = version + 1,
				updated_at = $3
			WHERE id = $1 AND state = 'RECONCILING'
		`, callID, nextState, m.now()); err != nil {
			return fmt.Errorf("project reconciled ringback state: %w", err)
		}
		if nextState == CallUnanswered {
			if err := insertCommand(
				ctx,
				tx,
				callID,
				"",
				CommandHangup,
				fact.CallControlID,
				map[string]any{
					"client_state": opaqueClientState(callID, "caller"),
				},
				m.now(),
			); err != nil {
				return err
			}
		}
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"provider.ringback.started",
		"",
		fact.EventID,
		"",
		opaqueReference(fact.CallLegID),
		"",
		fact.OccurredAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ringback projection: %w", err)
	}
	return nil
}

func (m *Module) applyBridge(ctx context.Context, fact ProviderFact) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin bridge projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}

	var callID, practiceID, currentClaimant, currentSession, currentWinner string
	var callerControlID, currentStaffControlID string
	var state CallState
	var currentConnectedAt *time.Time
	var bridgeOnCaller bool
	clientState, hasClientState := parseOpaqueClientState(fact.ClientState)
	query := `
		SELECT
			c.id::text,
			c.practice_id::text,
			c.state,
			COALESCE(c.claimant_subject, ''),
			COALESCE(c.claimant_session_id, ''),
			COALESCE(c.winner_subject, ''),
			c.caller_call_control_id,
			COALESCE(c.expected_staff_call_control_id, ''),
			c.connected_at,
			(c.caller_call_control_id = $1 AND c.caller_call_leg_id = $2)
		FROM human_calling_calls c
		WHERE c.call_session_id = $3
			AND (
				(c.caller_call_control_id = $1 AND c.caller_call_leg_id = $2)
				OR EXISTS (
					SELECT 1
					FROM human_calling_connection_attempts attempt
					WHERE attempt.call_id = c.id
						AND attempt.staff_call_control_id = $1
						AND attempt.staff_call_leg_id = $2
				)
			)
		FOR UPDATE
	`
	arguments := []any{fact.CallControlID, fact.CallLegID, fact.CallSessionID}
	if hasClientState &&
		clientState.Version == 1 {
		switch clientState.Leg {
		case "staff":
			query = `
			SELECT
				id::text,
				practice_id::text,
				state,
				COALESCE(claimant_subject, ''),
				COALESCE(claimant_session_id, ''),
				COALESCE(winner_subject, ''),
				caller_call_control_id,
				COALESCE(expected_staff_call_control_id, ''),
				connected_at,
				false
			FROM human_calling_calls
			WHERE id = $1
				AND call_session_id = $2
			FOR UPDATE
		`
			arguments = []any{clientState.CallID, fact.CallSessionID}
		case "caller":
			query = `
				SELECT
					id::text,
					practice_id::text,
					state,
					COALESCE(claimant_subject, ''),
					COALESCE(claimant_session_id, ''),
					COALESCE(winner_subject, ''),
					caller_call_control_id,
					COALESCE(expected_staff_call_control_id, ''),
					connected_at,
					true
				FROM human_calling_calls
				WHERE id = $1
					AND call_session_id = $2
					AND caller_call_control_id = $3
					AND caller_call_leg_id = $4
				FOR UPDATE
			`
			arguments = []any{
				clientState.CallID,
				fact.CallSessionID,
				fact.CallControlID,
				fact.CallLegID,
			}
		}
	}
	err = tx.QueryRow(ctx, query, arguments...).Scan(
		&callID,
		&practiceID,
		&state,
		&currentClaimant,
		&currentSession,
		&currentWinner,
		&callerControlID,
		&currentStaffControlID,
		&currentConnectedAt,
		&bridgeOnCaller,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("correlate provider bridge: %w", err)
	}

	var attemptID, claimantSubject, claimantSession, staffControlID, staffLegID string
	var attemptEndedAt *time.Time
	attemptQuery := `
		SELECT
			id::text,
			claimant_subject,
			claimant_session_id,
			COALESCE(staff_call_control_id, ''),
			COALESCE(staff_call_leg_id, ''),
			ended_at
		FROM human_calling_connection_attempts
		WHERE call_id = $1
			AND (
				($7 <> '' AND id = NULLIF($7, '')::uuid)
				OR (
					$7 = ''
					AND created_at <= $2
					AND $2 <= COALESCE(ended_at, connection_deadline)
				)
			)
			AND (
				$3
				OR (
					staff_call_control_id = $4
					AND staff_call_leg_id = $5
				)
				OR (
					$6
					AND staff_call_control_id IS NULL
					AND staff_call_leg_id IS NULL
				)
			)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
		FOR UPDATE
	`
	opaqueStaff := hasClientState &&
		clientState.Version == 1 &&
		clientState.Leg == "staff"
	if err := tx.QueryRow(
		ctx,
		attemptQuery,
		callID,
		fact.OccurredAt,
		bridgeOnCaller,
		fact.CallControlID,
		fact.CallLegID,
		opaqueStaff,
		clientState.AttemptID,
	).Scan(
		&attemptID,
		&claimantSubject,
		&claimantSession,
		&staffControlID,
		&staffLegID,
		&attemptEndedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("correlate provider bridge attempt: %w", err)
	}
	if !bridgeOnCaller {
		staffControlID = fact.CallControlID
		staffLegID = fact.CallLegID
	}
	if staffControlID == "" || staffLegID == "" {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_connection_attempts
		SET
			staff_call_control_id = COALESCE(staff_call_control_id, $2),
			staff_call_leg_id = COALESCE(staff_call_leg_id, $3),
			updated_at = $4
		WHERE id = $1
	`, attemptID, staffControlID, staffLegID, m.now()); err != nil {
		return fmt.Errorf("record bridged provider leg on attempt: %w", err)
	}
	if attemptEndedAt != nil {
		if err := ensureHangupCommand(
			ctx,
			tx,
			callID,
			attemptID,
			claimantSubject,
			staffControlID,
			"staff",
			m.now(),
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_connection_attempts
		SET
			bridge_occurred_at = CASE
				WHEN bridge_occurred_at IS NULL OR $2 < bridge_occurred_at THEN $2
				ELSE bridge_occurred_at
			END,
			updated_at = $3
		WHERE id = $1
	`, attemptID, fact.OccurredAt, m.now()); err != nil {
		return fmt.Errorf("project provider bridge attempt: %w", err)
	}
	if state == CallResolved || state == CallFollowUpRequired {
		if err := appendTimeline(
			ctx,
			tx,
			callID,
			practiceID,
			"provider.bridge.terminal_ignored",
			claimantSubject,
			fact.EventID,
			"",
			opaqueReference(staffLegID),
			"TERMINAL_STATE_PRESERVED",
			fact.OccurredAt,
		); err != nil {
			return err
		}
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if attemptEndedAt != nil && fact.OccurredAt.After(*attemptEndedAt) {
		if err := appendTimeline(
			ctx,
			tx,
			callID,
			practiceID,
			"provider.bridge.after_attempt",
			claimantSubject,
			fact.EventID,
			"",
			opaqueReference(staffLegID),
			"ATTEMPT_ALREADY_ENDED",
			fact.OccurredAt,
		); err != nil {
			return err
		}
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if attemptEndedAt != nil {
		if err := ensureHangupCommand(
			ctx,
			tx,
			callID,
			attemptID,
			claimantSubject,
			callerControlID,
			"caller",
			m.now(),
		); err != nil {
			return err
		}
	}

	var winningAttemptID string
	var acceptedAt, bridgeAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT
			id::text,
			claimant_subject,
			claimant_session_id,
			staff_call_control_id,
			staff_call_leg_id,
			bridge_occurred_at,
			ended_at,
			created_at
		FROM human_calling_connection_attempts
		WHERE call_id = $1 AND bridge_occurred_at IS NOT NULL
		ORDER BY bridge_occurred_at, created_at, id
		LIMIT 1
		FOR UPDATE
	`, callID).Scan(
		&winningAttemptID,
		&claimantSubject,
		&claimantSession,
		&staffControlID,
		&staffLegID,
		&bridgeAt,
		&attemptEndedAt,
		&acceptedAt,
	); err != nil {
		return fmt.Errorf("select provider-confirmed winning attempt: %w", err)
	}
	if currentConnectedAt != nil &&
		!bridgeAt.Before(*currentConnectedAt) &&
		currentWinner == claimantSubject {
		return tx.Commit(ctx)
	}
	if err := tx.QueryRow(ctx, `
		SELECT session_id
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
		FOR UPDATE
	`, claimantSubject).Scan(&claimantSession); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("resolve winning softphone session: %w", err)
		}
	}
	nextState := CallConnected
	if attemptEndedAt != nil {
		nextState = CallNeedsDisposition
	} else if (state == CallResolved || state == CallFollowUpRequired) &&
		currentWinner == claimantSubject {
		nextState = state
	}
	if currentStaffControlID != "" &&
		currentStaffControlID != staffControlID &&
		currentClaimant != "" {
		if err := insertCommand(
			ctx,
			tx,
			callID,
			currentClaimant,
			CommandHangup,
			currentStaffControlID,
			map[string]any{"client_state": opaqueClientState(callID, "staff")},
			m.now(),
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			state = $5,
			claimant_subject = CASE
				WHEN $8::timestamptz IS NULL THEN $6
				ELSE NULL
			END,
			claimant_session_id = $7,
			winner_subject = $6,
			current_attempt_id = $10,
			expected_staff_call_control_id = $3,
			expected_staff_call_leg_id = $4,
			provider_termination = NULL,
			connected_at = $2,
			ended_at = CASE WHEN $8::timestamptz IS NOT NULL THEN $8 ELSE ended_at END,
			version = version + 1,
			updated_at = $9
		WHERE id = $1
	`,
		callID,
		bridgeAt,
		staffControlID,
		staffLegID,
		nextState,
		claimantSubject,
		claimantSession,
		attemptEndedAt,
		m.now(),
		winningAttemptID,
	); err != nil {
		return fmt.Errorf("project provider-confirmed bridge: %w", err)
	}

	if attemptEndedAt == nil {
		objectKey := "calls/" + callID + ".wav"
		recordingIntent, err := tx.Exec(ctx, `
			INSERT INTO human_calling_recordings (
				call_id, practice_id, bucket, object_key, state, started_at
			)
			VALUES ($1, $2, $3, $4, 'INTENDED', $5)
			ON CONFLICT (call_id) DO NOTHING
		`, callID, practiceID, m.config.RecordingBucket, objectKey, bridgeAt)
		if err != nil {
			return fmt.Errorf("commit post-bridge recording intent: %w", err)
		}
		if recordingIntent.RowsAffected() == 1 {
			if err := insertCommand(
				ctx,
				tx,
				callID,
				claimantSubject,
				CommandStartRecording,
				callerControlID,
				map[string]any{
					"format":           "wav",
					"channels":         "dual",
					"recording_track":  "both",
					"transcription":    false,
					"custom_file_name": "call-" + strings.ReplaceAll(callID, "-", ""),
					"client_state":     opaqueClientState(callID, "recording"),
				},
				m.now(),
			); err != nil {
				return err
			}
		}
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"call.connected",
		claimantSubject,
		fact.EventID,
		"",
		opaqueReference(fact.CallLegID),
		"",
		bridgeAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit bridge projection: %w", err)
	}
	if nextState == CallConnected {
		observability.Record(
			m.observer,
			observability.CallBridged(bridgeAt.Sub(acceptedAt)),
		)
	}
	return nil
}

func (m *Module) applyHangup(ctx context.Context, fact ProviderFact) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin hangup projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}

	var callID, practiceID, callerControlID, currentStaffControlID, currentAttemptID string
	var currentClaimant, currentSession string
	var state CallState
	var deadline time.Time
	clientState, hasClientState := parseOpaqueClientState(fact.ClientState)
	query := `
		SELECT
			c.id::text,
			c.practice_id::text,
			c.state,
			c.offer_deadline,
			c.caller_call_control_id,
			COALESCE(c.expected_staff_call_control_id, ''),
			COALESCE(c.claimant_subject, ''),
			COALESCE(c.claimant_session_id, ''),
			COALESCE(c.current_attempt_id::text, '')
		FROM human_calling_calls c
		WHERE c.call_session_id = $1
			AND (
				c.caller_call_control_id = $2
				OR c.expected_staff_call_control_id = $2
				OR EXISTS (
					SELECT 1
					FROM human_calling_connection_attempts attempt
					WHERE attempt.call_id = c.id
						AND attempt.staff_call_control_id = $2
				)
			)
		FOR UPDATE
	`
	arguments := []any{fact.CallSessionID, fact.CallControlID}
	if hasClientState && clientState.Version == 1 {
		query = `
			SELECT
				id::text,
				practice_id::text,
				state,
				offer_deadline,
				caller_call_control_id,
				COALESCE(expected_staff_call_control_id, ''),
				COALESCE(claimant_subject, ''),
				COALESCE(claimant_session_id, ''),
				COALESCE(current_attempt_id::text, '')
			FROM human_calling_calls
			WHERE id = $1 AND call_session_id = $2
			FOR UPDATE
		`
		arguments = []any{clientState.CallID, fact.CallSessionID}
	}
	err = tx.QueryRow(ctx, query, arguments...).Scan(
		&callID,
		&practiceID,
		&state,
		&deadline,
		&callerControlID,
		&currentStaffControlID,
		&currentClaimant,
		&currentSession,
		&currentAttemptID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("correlate provider hangup: %w", err)
	}

	callerHangup := fact.CallControlID == callerControlID
	var attemptID, attemptClaimant, attemptSession string
	if !callerHangup || currentClaimant != "" {
		err := tx.QueryRow(ctx, `
			SELECT
				id::text,
				claimant_subject,
				claimant_session_id
			FROM human_calling_connection_attempts
			WHERE call_id = $1
				AND (
					($8 <> '' AND id = NULLIF($8, '')::uuid)
					OR ($8 = '' AND created_at <= $2)
				)
				AND (
					staff_call_control_id = $3
					OR (
						$4
						AND staff_call_control_id IS NULL
						AND $2 <= connection_deadline
					)
					OR (
						$5
						AND claimant_subject = $6
						AND claimant_session_id = $7
					)
				)
			ORDER BY created_at DESC, id DESC
			LIMIT 1
			FOR UPDATE
		`,
			callID,
			fact.OccurredAt,
			fact.CallControlID,
			hasClientState && clientState.Version == 1 && clientState.Leg == "staff",
			callerHangup,
			currentClaimant,
			currentSession,
			clientState.AttemptID,
		).Scan(
			&attemptID,
			&attemptClaimant,
			&attemptSession,
		)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("correlate provider hangup attempt: %w", err)
		}
		if errors.Is(err, pgx.ErrNoRows) && !callerHangup {
			return ErrConflict
		}
	}
	currentAttempt := attemptID != "" && attemptID == currentAttemptID
	if attemptID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_connection_attempts
			SET
				staff_call_control_id = CASE
					WHEN $2 THEN staff_call_control_id
					ELSE COALESCE(staff_call_control_id, $3)
				END,
				staff_call_leg_id = CASE
					WHEN $2 THEN staff_call_leg_id
					ELSE COALESCE(staff_call_leg_id, $4)
				END,
				ended_at = CASE
					WHEN ended_at IS NULL OR $5 < ended_at THEN $5
					ELSE ended_at
				END,
				provider_termination = NULLIF($6, ''),
				updated_at = $7
			WHERE id = $1
		`,
			attemptID,
			callerHangup,
			fact.CallControlID,
			fact.CallLegID,
			fact.OccurredAt,
			fact.HangupCause,
			m.now(),
		); err != nil {
			return fmt.Errorf("project provider hangup attempt: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = 'RECONCILED',
			sent_at = COALESCE(sent_at, $3),
			last_error_code = NULL,
			updated_at = $4
		WHERE call_id = $1
			AND action = 'HANGUP'
			AND target_id = $2
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, callID, fact.CallControlID, fact.OccurredAt, m.now()); err != nil {
		return fmt.Errorf("reconcile provider hangup command: %w", err)
	}

	switch state {
	case CallConnected:
		if !callerHangup && !currentAttempt {
			break
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				state = 'NEEDS_DISPOSITION',
				provider_termination = NULLIF($2, ''),
				ended_at = $3,
				version = version + 1,
				updated_at = $3
			WHERE id = $1
		`, callID, fact.HangupCause, fact.OccurredAt); err != nil {
			return fmt.Errorf("project connected Call termination: %w", err)
		}
	case CallConnecting, CallReconciling:
		if !callerHangup && !currentAttempt {
			break
		}
		nextState := CallUnanswered
		reopen := !callerHangup && currentAttempt && m.now().Before(deadline)
		if reopen {
			nextState = CallOffering
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				state = $2,
				claimant_subject = CASE WHEN $3 THEN NULL ELSE claimant_subject END,
				claimant_session_id = CASE WHEN $3 THEN NULL ELSE claimant_session_id END,
				expected_staff_call_control_id = CASE WHEN $3 THEN NULL ELSE expected_staff_call_control_id END,
				expected_staff_call_leg_id = CASE WHEN $3 THEN NULL ELSE expected_staff_call_leg_id END,
				provider_termination = CASE
					WHEN $3 THEN NULL
					ELSE NULLIF($4, '')
				END,
				ended_at = CASE WHEN $3 THEN NULL ELSE $5::timestamptz END,
				version = version + 1,
				updated_at = $5
			WHERE id = $1
			`, callID, nextState, reopen, fact.HangupCause, fact.OccurredAt); err != nil {
			return fmt.Errorf("project pre-bridge termination: %w", err)
		}
		if nextState == CallUnanswered && fact.CallControlID != callerControlID {
			if err := insertCommand(
				ctx,
				tx,
				callID,
				"",
				CommandHangup,
				callerControlID,
				map[string]any{
					"client_state": opaqueClientState(callID, "caller"),
				},
				m.now(),
			); err != nil {
				return err
			}
		}
	case CallOffering:
		if fact.CallControlID == callerControlID {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET state = 'UNANSWERED', provider_termination = NULLIF($2, ''),
					ended_at = $3, version = version + 1, updated_at = $3
				WHERE id = $1
			`, callID, fact.HangupCause, fact.OccurredAt); err != nil {
				return fmt.Errorf("project caller termination while offering: %w", err)
			}
		}
	case CallNeedsDisposition, CallResolved, CallFollowUpRequired, CallUnanswered:
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"call.terminated",
		"",
		fact.EventID,
		"",
		opaqueReference(fact.CallLegID),
		sanitizeCode(fact.HangupCause),
		fact.OccurredAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit hangup projection: %w", err)
	}
	return nil
}

func (m *Module) applyRecordingSaved(ctx context.Context, fact ProviderFact) error {
	if fact.RecordingBucket != m.config.RecordingBucket ||
		fact.RecordingObjectKey == "" {
		fact.RecordingObjectKey = ""
		return m.applyRecordingFact(
			ctx,
			fact,
			RecordingFailed,
			"GCS_OBJECT_NOT_READY",
		)
	}
	return m.applyRecordingFact(ctx, fact, RecordingReady, "")
}

func (m *Module) applyRecordingError(ctx context.Context, fact ProviderFact) error {
	return m.applyRecordingFact(
		ctx,
		fact,
		RecordingFailed,
		"PROVIDER_RECORDING_FAILED",
	)
}

func (m *Module) applyRecordingFact(
	ctx context.Context,
	fact ProviderFact,
	recordingState RecordingState,
	failureCode string,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin recording projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}

	var callID, practiceID, currentObjectKey string
	err = tx.QueryRow(ctx, `
		SELECT c.id::text, c.practice_id::text, r.object_key
		FROM human_calling_calls c
		JOIN human_calling_recordings r ON r.call_id = c.id
		WHERE c.call_session_id = $1
			AND (
				c.caller_call_control_id = $2
				OR c.expected_staff_call_control_id = $2
			)
		FOR UPDATE OF r
	`, fact.CallSessionID, fact.CallControlID).Scan(
		&callID,
		&practiceID,
		&currentObjectKey,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("correlate recording evidence: %w", err)
	}
	objectKey := currentObjectKey
	if fact.RecordingObjectKey != "" {
		objectKey = fact.RecordingObjectKey
	}
	projected, err := tx.Exec(ctx, `
		UPDATE human_calling_recordings
		SET
			state = $2,
			provider_recording_id = NULLIF($3, ''),
			object_key = $4,
			ready_at = CASE WHEN $2 = 'READY' THEN $5 ELSE ready_at END,
			failure_code = NULLIF($6, ''),
			last_event_at = $5,
			updated_at = $5
		WHERE call_id = $1
			AND (
				last_event_at IS NULL
				OR last_event_at < $5
				OR (
					last_event_at = $5
					AND $2 = 'READY'
					AND state <> 'READY'
				)
			)
	`, callID, recordingState, fact.RecordingID, objectKey, fact.OccurredAt, failureCode)
	if err != nil {
		return fmt.Errorf("project recording evidence: %w", err)
	}
	timelineKind := "recording." + strings.ToLower(string(recordingState))
	timelineError := failureCode
	if projected.RowsAffected() == 0 {
		timelineKind = "recording.fact_ignored"
		timelineError = "STALE_RECORDING_FACT"
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		timelineKind,
		"",
		fact.EventID,
		"",
		opaqueReference(fact.RecordingID),
		timelineError,
		fact.OccurredAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit recording projection: %w", err)
	}
	return nil
}

func (m *Module) loadCall(ctx context.Context, callID string) (Call, error) {
	var result Call
	err := m.pool.QueryRow(ctx, `
		SELECT
			c.id::text,
			c.practice_id::text,
			c.location_id::text,
			l.name,
			c.state,
			c.offer_deadline,
			COALESCE(c.claimant_subject, ''),
			COALESCE(c.winner_subject, ''),
			COALESCE(c.expected_staff_call_leg_id, ''),
			COALESCE(c.current_attempt_id::text, ''),
			COALESCE(h.phone, ''),
			COALESCE(h.phone_source, ''),
			COALESCE(h.display_name, ''),
			COALESCE(h.name_source, ''),
			COALESCE(h.transfer_reason, ''),
			COALESCE(h.reason_source, ''),
			COALESCE(c.provider_termination, ''),
			c.connected_at,
			c.version,
			COALESCE(r.state, ''),
			COALESCE(r.bucket, ''),
			COALESCE(r.object_key, ''),
			COALESCE(r.provider_recording_id, ''),
			COALESCE(r.failure_code, '')
		FROM human_calling_calls c
		JOIN human_calling_handoffs h ON h.id = c.handoff_id
		JOIN access_locations l ON l.id = c.location_id AND l.practice_id = c.practice_id
		LEFT JOIN human_calling_recordings r ON r.call_id = c.id
		WHERE c.id = $1
	`, callID).Scan(
		&result.ID,
		&result.PracticeID,
		&result.LocationID,
		&result.LocationName,
		&result.State,
		&result.Deadline,
		&result.ClaimantSubject,
		&result.WinnerSubject,
		&result.ExpectedStaffLegID,
		&result.currentAttemptID,
		&result.Phone,
		&result.PhoneSource,
		&result.DisplayName,
		&result.NameSource,
		&result.TransferReason,
		&result.ReasonSource,
		&result.ProviderTermination,
		&result.ConnectedAt,
		&result.Version,
		&result.Recording.State,
		&result.Recording.Bucket,
		&result.Recording.ObjectKey,
		&result.Recording.ProviderID,
		&result.Recording.FailureCode,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Call{}, ErrDenied
		}
		return Call{}, fmt.Errorf("read Call: %w", err)
	}
	return result, nil
}

func (m *Module) admitHandoff(ctx context.Context, fact ProviderFact) error {
	token := fact.HandoffToken
	if token == "" {
		var err error
		token, err = tokenFromDestination(fact.To)
		if err != nil {
			return err
		}
	}
	tokenHash := sha256.Sum256([]byte(token))
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin handoff admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}

	var handoffID, practiceID, locationID string
	err = tx.QueryRow(ctx, `
		SELECT id::text, practice_id::text, location_id::text
		FROM human_calling_handoffs
		WHERE token_hash = $1
			AND consumed_at IS NULL
			AND expires_at > $2
		FOR UPDATE
	`, tokenHash[:], m.now()).Scan(&handoffID, &practiceID, &locationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidHandoff
		}
		return fmt.Errorf("resolve handoff admission: %w", err)
	}

	callID := uuid.NewString()
	deadline := m.now().Add(m.config.OfferDuration)
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id
		)
		VALUES ($1, $2, $3, $4, 'OFFERING', $5, $6, $7, $8)
	`,
		callID,
		handoffID,
		practiceID,
		locationID,
		deadline,
		fact.CallControlID,
		fact.CallLegID,
		fact.CallSessionID,
	); err != nil {
		return fmt.Errorf("create admitted Call: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE human_calling_handoffs SET consumed_at = $2 WHERE id = $1`,
		handoffID,
		m.now(),
	); err != nil {
		return fmt.Errorf("consume admitted handoff: %w", err)
	}
	answerCommandID, err := insertCommandWithDependency(
		ctx,
		tx,
		callID,
		"",
		CommandAnswerCaller,
		fact.CallControlID,
		map[string]any{
			"transcription": false,
			"client_state":  opaqueClientState(callID, "caller"),
		},
		m.now(),
		"",
	)
	if err != nil {
		return err
	}
	if _, err := insertCommandWithDependency(
		ctx,
		tx,
		callID,
		"",
		CommandStartRingback,
		fact.CallControlID,
		map[string]any{
			"audio_url":    m.config.RingbackURL,
			"loop":         "infinity",
			"client_state": opaqueClientState(callID, "caller"),
		},
		m.now(),
		answerCommandID,
	); err != nil {
		return err
	}
	if err := appendTimeline(ctx, tx, callID, practiceID, "offer.created", "", fact.EventID, "", "", "", fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit handoff admission: %w", err)
	}
	return nil
}

func insertCommand(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	userSubject string,
	action CommandAction,
	targetID string,
	payload map[string]any,
	nextAttemptAt time.Time,
) error {
	_, err := insertCommandWithDependency(
		ctx,
		tx,
		callID,
		userSubject,
		action,
		targetID,
		payload,
		nextAttemptAt,
		"",
	)
	return err
}

func ensureHangupCommand(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	attemptID string,
	userSubject string,
	targetID string,
	leg string,
	nextAttemptAt time.Time,
) error {
	commandID := uuid.NewString()
	encoded, err := json.Marshal(map[string]any{
		"client_state": opaqueClientState(callID, leg, attemptID),
	})
	if err != nil {
		return fmt.Errorf("encode Hangup command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			id, call_id, attempt_id, user_subject, action, target_id, payload,
			next_attempt_at
		)
		SELECT
			$1,
			$2,
			NULLIF($3, '')::uuid,
			NULLIF($4, ''),
			'HANGUP',
			$5,
			$6,
			$7
		WHERE NOT EXISTS (
			SELECT 1
			FROM human_calling_provider_commands
			WHERE call_id = $2
				AND action = 'HANGUP'
				AND target_id = $5
				AND state IN (
					'PENDING',
					'SENDING',
					'SENT',
					'AMBIGUOUS',
					'RECONCILED'
				)
		)
	`,
		commandID,
		callID,
		attemptID,
		userSubject,
		targetID,
		encoded,
		nextAttemptAt,
	); err != nil {
		return fmt.Errorf("commit exact-leg Hangup command: %w", err)
	}
	return nil
}

func insertCommandWithDependency(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	userSubject string,
	action CommandAction,
	targetID string,
	payload map[string]any,
	nextAttemptAt time.Time,
	dependencyID string,
) (string, error) {
	commandID := uuid.NewString()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode provider command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			id, call_id, user_subject, action, target_id, payload,
			next_attempt_at, depends_on_command_id
		)
		VALUES (
			$1, $2, NULLIF($3, ''), $4, NULLIF($5, ''), $6, $7,
			NULLIF($8, '')::uuid
		)
	`, commandID, callID, userSubject, action, targetID, encoded, nextAttemptAt, dependencyID); err != nil {
		return "", fmt.Errorf("commit provider command: %w", err)
	}
	return commandID, nil
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
	opaqueReference string,
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
	`, callID, practiceID, kind, actorSubject, eventID, commandID, opaqueReference, errorCode, occurredAt); err != nil {
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
		strings.TrimSpace(sipDomain) == "" {
		return ErrInvalidInput
	}
	if len(command.Contact.TransferReason) > 500 ||
		len(command.Contact.DisplayName) > 200 ||
		len(command.Contact.Phone) > 40 {
		return ErrInvalidInput
	}
	return nil
}

func handoffFingerprint(command CreateHandoffCommand) ([32]byte, error) {
	// Keep the pre-Access-generalization payload stable so a handoff committed
	// by an overlapping revision remains safely replayable.
	type fingerprintService struct {
		Subject    string
		PracticeID string
	}
	encoded, err := json.Marshal(struct {
		Service        fingerprintService
		LocationID     string
		SourceCallID   string
		IdempotencyKey string
		Contact        ContactContext
	}{
		Service: fingerprintService{
			Subject:    command.Service.Subject,
			PracticeID: command.Service.PracticeID,
		},
		LocationID:     command.LocationID,
		SourceCallID:   command.SourceCallID,
		IdempotencyKey: command.IdempotencyKey,
		Contact:        command.Contact,
	})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode handoff fingerprint: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

func (m *Module) handoffToken(handoffID string) string {
	mac := hmac.New(sha256.New, m.tokenKey)
	_, _ = mac.Write([]byte(handoffID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Module) staffMediaToken(callID string, attemptID string) string {
	mac := hmac.New(sha256.New, m.tokenKey)
	_, _ = mac.Write([]byte("staff-media-v1\x00" + callID + "\x00" + attemptID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Module) sipDestination(handoffID string) string {
	return "sip:" + m.handoffToken(handoffID) + "@" + m.config.HandoffSIPDomain
}

func tokenFromDestination(destination string) (string, error) {
	value := strings.TrimSpace(destination)
	if !strings.HasPrefix(strings.ToLower(value), "sip:") {
		return "", ErrInvalidHandoff
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque == "" {
		return "", ErrInvalidHandoff
	}
	token := parsed.Opaque
	if at := strings.IndexByte(token, '@'); at >= 0 {
		token = token[:at]
	}
	if token == "" {
		return "", ErrInvalidHandoff
	}
	return token, nil
}

func opaqueClientState(callID string, leg string, attemptID ...string) string {
	state := clientState{Version: 1, CallID: callID, Leg: leg}
	if len(attemptID) > 0 {
		state.AttemptID = attemptID[0]
	}
	payload, _ := json.Marshal(state)
	return base64.StdEncoding.EncodeToString(payload)
}

type clientState struct {
	Version   int    `json:"v"`
	CallID    string `json:"call"`
	Leg       string `json:"leg"`
	AttemptID string `json:"attempt,omitempty"`
}

func parseOpaqueClientState(value string) (clientState, bool) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) > 512 {
		return clientState{}, false
	}
	var result clientState
	if err := json.Unmarshal(decoded, &result); err != nil {
		return clientState{}, false
	}
	if result.CallID == "" || result.Leg == "" {
		return clientState{}, false
	}
	return result, true
}

func opaqueReference(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:8])
}

func managedSIPDestination(username string, domain string) string {
	return "sip:" + url.PathEscape(username) + "@" + domain
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

func sanitizeCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var sanitized strings.Builder
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' {
			sanitized.WriteRune(character)
		}
	}
	result := sanitized.String()
	if len(result) > 64 {
		result = result[:64]
	}
	return result
}

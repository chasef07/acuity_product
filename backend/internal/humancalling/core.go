package humancalling

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CallState string

const (
	CallPreparing        CallState = "PREPARING"
	CallRinging          CallState = "RINGING"
	CallConnecting       CallState = "CONNECTING"
	CallConnected        CallState = "CONNECTED"
	CallUnanswered       CallState = "UNANSWERED"
	CallVoicemail        CallState = "VOICEMAIL"
	CallMissed           CallState = "MISSED"
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
	FactPlaybackEnded   FactType = "call.playback.ended"
	FactSpeakStarted    FactType = "call.speak.started"
	FactSpeakEnded      FactType = "call.speak.ended"
	FactRecordingSaved  FactType = "call.recording.saved"
	FactRecordingError  FactType = "call.recording.error"
)

type CommandAction string

const (
	CommandAnswerCaller            CommandAction = "ANSWER_CALLER"
	CommandStartRingWindow         CommandAction = "START_RING_WINDOW"
	CommandDialStaff               CommandAction = "DIAL_STAFF"
	CommandBridge                  CommandAction = "BRIDGE"
	CommandStopRingWindow          CommandAction = "STOP_RING_WINDOW"
	CommandHangupLeg               CommandAction = "HANGUP_LEG"
	CommandSpeakVoicemail          CommandAction = "SPEAK_VOICEMAIL"
	CommandStartVoicemailRecording CommandAction = "START_VOICEMAIL_RECORDING"
	CommandDialOutboundStaff       CommandAction = "DIAL_OUTBOUND_STAFF"
	CommandDialOutboundDestination CommandAction = "DIAL_OUTBOUND_DESTINATION"
	CommandCreateCredential        CommandAction = "CREATE_CREDENTIAL"
	CommandDisableCredential       CommandAction = "DISABLE_CREDENTIAL"
	CommandCreateJWT               CommandAction = "CREATE_JWT"
)

const safeProviderRetryWindow = 55 * time.Second

var (
	ErrDenied                    = errors.New("human calling access denied")
	ErrInvalidInput              = errors.New("invalid human calling input")
	ErrConflict                  = errors.New("human calling transition conflict")
	ErrExpired                   = errors.New("human calling deadline expired")
	ErrIneligible                = errors.New("user is not currently call eligible")
	ErrInvalidHandoff            = errors.New("invalid handoff")
	ErrHandoffAdmissionClosed    = errors.New("human calling handoff admission is closed")
	ErrAmbiguousEffect           = errors.New("provider effect is ambiguous")
	ErrDefinitiveProviderFailure = errors.New("provider effect definitely failed")
	ErrProviderTargetAbsent      = errors.New("provider target is absent")
	ErrInvalidWebhook            = errors.New("invalid provider webhook")
)

var canonicalE164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

type CallDirection string

const (
	CallInbound  CallDirection = "INBOUND"
	CallOutbound CallDirection = "OUTBOUND"
)

type CallEntryPoint string

const (
	CallEntryAIHandoff  CallEntryPoint = "AI_HANDOFF"
	CallEntryTask       CallEntryPoint = "TASK"
	CallEntryStandalone CallEntryPoint = "STANDALONE"
)

type Config struct {
	HandoffSIPDomain       string
	HandoffAdmissionClosed bool
	StaffSIPDomain         string
	RingWindowDuration     time.Duration
	HandoffLifetime        time.Duration
	HandoffTokenKey        []byte
	LeaseDuration          time.Duration
	ReadinessGrace         time.Duration
	DispositionDuration    time.Duration
	CallControlID          string
	CredentialConnectionID string
	FromNumber             string
	RingbackURL            string
	VoicemailAudioProvider VoicemailAudioProvider
	PlaybackSigningKey     []byte
	WebhookPublicKeys      []ed25519.PublicKey
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
	ConnectionID       string
	CallControlID      string
	CallLegID          string
	CallSessionID      string
	ClientState        string
	From               string
	To                 string
	HangupCause        string
	TerminationSource  string
	SIPCause           string
	PlaybackStatus     string
	CallQualityStats   map[string]any
	RecordingID        string
	RecordingStartedAt time.Time
	RecordingEndedAt   time.Time
}

type ProviderCommand struct {
	ID            string
	CallLegID     string
	PeerCallLegID string
	Action        CommandAction
	TargetID      string
	Payload       map[string]any
	createdAt     time.Time
}

type ProviderResult struct {
	CallControlID string
	CallLegID     string
	CredentialID  string
	SIPUsername   string
	JWT           string
	JWTExpiresAt  time.Time
}

type ProviderCallObservation struct {
	Active        bool
	CallControlID string
	CallLegID     string
	CallSessionID string
	Events        []ProviderFact
}

type Provider interface {
	Execute(context.Context, ProviderCommand) (ProviderResult, error)
}

type CredentialStateProvider interface {
	FindCredentialByName(context.Context, string) (ProviderResult, bool, error)
}

type CallStateProvider interface {
	ObserveCall(
		context.Context,
		string,
		string,
		string,
		string,
		time.Time,
	) (ProviderCallObservation, error)
}

type SoftphoneState struct {
	SessionID            string
	LeaseExpiresAt       time.Time
	Owner                bool
	Available            bool
	ActiveCallID         string
	PendingOutcomeCallID string
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

type Call struct {
	ID                  string
	PracticeID          string
	LocationID          string
	LocationName        string
	Direction           CallDirection
	EntryPoint          CallEntryPoint
	TaskID              string
	State               CallState
	DispositionDeadline *time.Time
	Phone               string
	CallerID            string
	PhoneSource         string
	DisplayName         string
	NameSource          string
	TransferReason      string
	ReasonSource        string
	ProviderTermination string
	ConnectedAt         *time.Time
	Version             int64
	Voicemail           Voicemail
	RetryOfCallID       string
	RetryAllowed        bool
	RecoveryTask        *RecoveryTask
}

type RecoveryTask struct {
	ID                      string
	Title                   string
	State                   work.TaskState
	RelatedInteractionCount int
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
	Kind              string
	OpaqueReference   string
	RecoveryReference string
	ErrorCode         string
	CommandAction     string
	CommandState      string
	CommandAttempts   int
	ReceiptState      string
	AgeSeconds        int64
	OccurredAt        time.Time
}

type Disposition string

const (
	DispositionResolved         Disposition = "RESOLVED"
	DispositionFollowUpRequired Disposition = "FOLLOW_UP_REQUIRED"
	DispositionCompleteTask     Disposition = "COMPLETE_TASK"
	DispositionKeepOpen         Disposition = "KEEP_OPEN"
	DispositionCreateTask       Disposition = "CREATE_TASK"
	DispositionNoFollowUp       Disposition = "NO_FOLLOW_UP"
)

type DispositionResult struct {
	Call   Call
	TaskID string
}

type Module struct {
	pool        *pgxpool.Pool
	access      *access.Module
	work        *work.Module
	provider    Provider
	config      Config
	now         func() time.Time
	tokenKey    []byte
	playbackKey []byte
	observer    observability.Observer
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
	if config.RingWindowDuration <= 0 {
		config.RingWindowDuration = 20 * time.Second
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
	if config.DispositionDuration <= 0 {
		config.DispositionDuration = 20 * time.Second
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
		pool:        pool,
		access:      accessModule,
		provider:    provider,
		config:      config,
		now:         now,
		tokenKey:    tokenKey,
		playbackKey: append([]byte(nil), config.PlaybackSigningKey...),
		observer:    config.Observer,
	}
	if len(module.playbackKey) == 0 {
		module.playbackKey = append([]byte(nil), tokenKey...)
	}
	if pool != nil && accessModule != nil {
		module.work = work.New(pool, accessModule, now)
	}
	return module
}

func (m *Module) ApplyProviderFact(ctx context.Context, fact ProviderFact) error {
	if fact.EventID == "" || fact.Type == "" || fact.OccurredAt.IsZero() {
		return ErrInvalidInput
	}
	state, hasState := parseCallLegClientState(fact.ClientState)
	switch fact.Type {
	case FactCallInitiated:
		if hasState && state.Role == "STAFF" {
			return m.applyStaffInitiated(ctx, fact, state.CallID)
		}
		if hasState && state.Role == "DESTINATION" {
			return m.applyOutboundDestinationFact(ctx, fact, state.CallID)
		}
		if hasState {
			return ErrConflict
		}
		return m.admitHandoff(ctx, fact)
	case FactCallAnswered:
		if hasState && state.Role == "STAFF" {
			return m.applyStaffInitiated(ctx, fact, state.CallID)
		}
		if hasState && state.Role == "DESTINATION" {
			return m.applyOutboundDestinationFact(ctx, fact, state.CallID)
		}
		return m.applyCallerAnswered(ctx, fact)
	case FactCallBridged:
		if hasState && state.Role == "DESTINATION" {
			return m.applyOutboundBridge(ctx, fact)
		}
		if hasState && state.Role == "CALLER" {
			return m.applyCallerBridge(ctx, fact)
		}
		if hasState && state.Role == "STAFF" && state.Kind != "bridge" {
			return m.applyOutboundStaffBridge(ctx, fact)
		}
		return m.applyBridge(ctx, fact)
	case FactCallHangup:
		return m.applyHangup(ctx, fact)
	case FactPlaybackStarted:
		return m.applyRingbackStarted(ctx, fact)
	case FactPlaybackEnded:
		return m.applyRingWindowEnded(ctx, fact)
	case FactSpeakStarted:
		return m.applyVoicemailGreetingStarted(ctx, fact)
	case FactSpeakEnded:
		return m.applyVoicemailGreetingEnded(ctx, fact)
	case FactRecordingSaved:
		return m.applyVoicemailRecordingSaved(ctx, fact)
	case FactRecordingError:
		return m.applyVoicemailRecordingError(ctx, fact)
	default:
		return ErrInvalidInput
	}
}

func (m *Module) staffMediaToken(callID string, callLegID string) string {
	mac := hmac.New(sha256.New, m.tokenKey)
	_, _ = mac.Write([]byte("staff-media-v2\x00" + callID + "\x00" + callLegID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Module) receiptRecoveryReference(eventID string) string {
	mac := hmac.New(sha256.New, m.tokenKey)
	_, _ = mac.Write([]byte("provider-receipt-recovery-v1\x00" + eventID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func opaqueReference(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:8])
}

func managedSIPDestination(username string, domain string) string {
	return "sip:" + url.PathEscape(username) + "@" + domain
}

func sanitizeCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	var sanitized strings.Builder
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' {
			sanitized.WriteRune(character)
		}
	}
	result := sanitized.String()
	if len(result) > 64 {
		return result[:64]
	}
	return result
}

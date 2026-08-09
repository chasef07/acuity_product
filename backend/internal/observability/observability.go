// Package observability owns the PHI-free call-center metric contract.
package observability

import (
	"log/slog"
	"regexp"
	"sync/atomic"
	"time"
)

const ContractVersion = "1"

type RuntimeRole string

const (
	RuntimePortalAPI       RuntimeRole = "portal-api"
	RuntimeProviderIngress RuntimeRole = "provider-ingress"
	RuntimeRealtime        RuntimeRole = "realtime"
	RuntimeWorker          RuntimeRole = "worker"
	RuntimeMigrate         RuntimeRole = "migrate"
)

type WebhookOutcome string

const (
	WebhookAccepted    WebhookOutcome = "accepted"
	WebhookDuplicate   WebhookOutcome = "duplicate"
	WebhookInvalid     WebhookOutcome = "invalid"
	WebhookUnavailable WebhookOutcome = "unavailable"
)

type ReceiptOutcome string

const (
	ReceiptApplied     ReceiptOutcome = "applied"
	ReceiptUnknown     ReceiptOutcome = "unknown"
	ReceiptFailed      ReceiptOutcome = "failed"
	ReceiptRetry       ReceiptOutcome = "retry"
	ReceiptQuarantined ReceiptOutcome = "quarantined"
)

type CommandAction string

const (
	CommandAnswerCaller            CommandAction = "answer_caller"
	CommandStartRingWindow         CommandAction = "start_ring_window"
	CommandDialStaff               CommandAction = "dial_staff"
	CommandBridge                  CommandAction = "bridge"
	CommandStopRingWindow          CommandAction = "stop_ring_window"
	CommandHangupLeg               CommandAction = "hangup_leg"
	CommandSpeakVoicemail          CommandAction = "speak_voicemail"
	CommandStartVoicemailRecording CommandAction = "start_voicemail_recording"
	CommandDialOutboundStaff       CommandAction = "dial_outbound_staff"
	CommandDialOutboundDestination CommandAction = "dial_outbound_destination"
	CommandCreateCredential        CommandAction = "create_credential"
	CommandDisableCredential       CommandAction = "disable_credential"
	CommandCreateJWT               CommandAction = "create_jwt"
)

type CommandOutcome string

const (
	CommandSent       CommandOutcome = "sent"
	CommandAmbiguous  CommandOutcome = "ambiguous"
	CommandRejected   CommandOutcome = "rejected"
	CommandReconciled CommandOutcome = "reconciled"
	CommandObsolete   CommandOutcome = "obsolete"
)

type PoolAcquireOutcome string

const (
	PoolAcquireSucceeded PoolAcquireOutcome = "succeeded"
	PoolAcquireCanceled  PoolAcquireOutcome = "canceled"
	PoolAcquireTimeout   PoolAcquireOutcome = "timeout"
	PoolAcquireFailed    PoolAcquireOutcome = "failed"
)

type SSECloseReason string

const (
	SSEClientClosed       SSECloseReason = "client"
	SSELifetimeEnded      SSECloseReason = "lifetime"
	SSEWriteFailed        SSECloseReason = "write_failed"
	SSERevalidationFailed SSECloseReason = "revalidation_failed"
	SSEListenerChanged    SSECloseReason = "listener_changed"
	SSEShutdown           SSECloseReason = "shutdown"
)

type StaffAnswerOutcome string

const (
	StaffAnswerWinner   StaffAnswerOutcome = "winner"
	StaffAnswerLostRace StaffAnswerOutcome = "lost_race"
	StaffAnswerOccupied StaffAnswerOutcome = "occupied"
	StaffAnswerTerminal StaffAnswerOutcome = "terminal"
	StaffAnswerOutbound StaffAnswerOutcome = "outbound"
)

type VoicemailPlaybackOutcome string

const (
	VoicemailPlaybackSucceeded       VoicemailPlaybackOutcome = "succeeded"
	VoicemailPlaybackDenied          VoicemailPlaybackOutcome = "denied"
	VoicemailPlaybackNotFound        VoicemailPlaybackOutcome = "not_found"
	VoicemailPlaybackProviderAuth    VoicemailPlaybackOutcome = "provider_auth"
	VoicemailPlaybackRateLimited     VoicemailPlaybackOutcome = "rate_limited"
	VoicemailPlaybackTimeout         VoicemailPlaybackOutcome = "timeout"
	VoicemailPlaybackUnavailable     VoicemailPlaybackOutcome = "unavailable"
	VoicemailPlaybackInvalidResponse VoicemailPlaybackOutcome = "invalid_response"
	VoicemailPlaybackURLExpired      VoicemailPlaybackOutcome = "url_expired"
)

// Event values can only be created through the fixed constructors below.
// Their private fields cannot carry identifiers, errors, SQL, or evidence.
type Event struct {
	signal   string
	fields   []any
	sseDelta int64
}

func WebhookAcknowledged(outcome WebhookOutcome, duration time.Duration) Event {
	return event("acuity_call_center_webhook_acknowledgement",
		"outcome", bounded(string(outcome), "accepted", "duplicate", "invalid", "unavailable"),
		"seconds", positive(duration).Seconds())
}

func ReceiptQueue(
	depth int64,
	oldestAge time.Duration,
	quarantinedDepth int64,
) Event {
	return event("acuity_call_center_receipt_queue",
		"depth", max(depth, 0),
		"oldest_age_seconds", positive(oldestAge).Seconds(),
		"quarantined_depth", max(quarantinedDepth, 0))
}

func TerminalCleanup(
	staffOccupancy int64,
	oldestStaffOccupancy time.Duration,
	unresolvedHangups int64,
	oldestHangup time.Duration,
) Event {
	return event("acuity_call_center_terminal_cleanup",
		"staff_occupancy", max(staffOccupancy, 0),
		"oldest_staff_occupancy_seconds", positive(oldestStaffOccupancy).Seconds(),
		"unresolved_hangups", max(unresolvedHangups, 0),
		"oldest_hangup_seconds", positive(oldestHangup).Seconds())
}

func ReceiptProcessed(outcome ReceiptOutcome, queueAge, duration time.Duration) Event {
	return event("acuity_call_center_receipt_processing",
		"outcome", bounded(string(outcome), "applied", "unknown", "failed", "retry", "quarantined"),
		"queue_seconds", positive(queueAge).Seconds(),
		"processing_seconds", positive(duration).Seconds())
}

func ProviderCommandCompleted(
	action CommandAction,
	outcome CommandOutcome,
	queueAge, duration time.Duration,
) Event {
	return event("acuity_call_center_provider_command",
		"action", bounded(string(action), "answer_caller", "start_ring_window", "dial_staff",
			"bridge", "stop_ring_window", "hangup_leg", "speak_voicemail",
			"start_voicemail_recording", "dial_outbound_staff",
			"dial_outbound_destination", "create_credential", "disable_credential", "create_jwt"),
		"outcome", bounded(string(outcome), "sent", "ambiguous", "rejected", "reconciled", "obsolete"),
		"queue_seconds", positive(queueAge).Seconds(),
		"duration_seconds", positive(duration).Seconds())
}

func DatabasePoolAcquired(outcome PoolAcquireOutcome, duration time.Duration) Event {
	return event("acuity_call_center_database_pool_acquire",
		"outcome", bounded(string(outcome), "succeeded", "canceled", "timeout", "failed"),
		"seconds", positive(duration).Seconds())
}

func DatabasePoolState(acquired, idle, maximum int32) Event {
	ratio := float64(0)
	if maximum > 0 {
		ratio = min(float64(max(acquired, 0))/float64(maximum), 1)
	}
	return event("acuity_call_center_database_pool",
		"acquired", max(acquired, 0), "idle", max(idle, 0),
		"max", max(maximum, 0), "saturation_ratio", ratio)
}

func SSEStreamOpened() Event {
	return Event{signal: "acuity_call_center_sse_stream", fields: []any{"state", "opened"}, sseDelta: 1}
}

func SSEStreamClosed(reason SSECloseReason) Event {
	return Event{signal: "acuity_call_center_sse_stream", fields: []any{
		"state", "closed", "reason", bounded(string(reason),
			"client", "lifetime", "write_failed", "revalidation_failed", "listener_changed", "shutdown"),
	}, sseDelta: -1}
}

func SSEListenerConnected(reconnect bool) Event {
	return event("acuity_call_center_sse_listener", "state", "connected", "reconnect", reconnect)
}

func SSEListenerDisconnected() Event {
	return event("acuity_call_center_sse_listener", "state", "disconnected")
}

func SSEListenerReconnectFailed() Event {
	return event("acuity_call_center_sse_listener", "state", "reconnect_failed")
}

func StaffAnswered(outcome StaffAnswerOutcome) Event {
	return event("acuity_call_center_staff_answer",
		"outcome", bounded(string(outcome),
			"winner", "lost_race", "occupied", "terminal", "outbound"))
}

func CallLegBridged(answerToBridge time.Duration) Event {
	return event("acuity_call_center_answer_to_bridge",
		"seconds", positive(answerToBridge).Seconds())
}

func VoicemailPlayback(
	outcome VoicemailPlaybackOutcome,
	duration time.Duration,
) Event {
	return event(
		"acuity_call_center_voicemail_playback",
		"outcome",
		bounded(
			string(outcome),
			"succeeded",
			"denied",
			"not_found",
			"provider_auth",
			"rate_limited",
			"timeout",
			"unavailable",
			"invalid_response",
			"url_expired",
		),
		"seconds",
		positive(duration).Seconds(),
	)
}

type Observer interface{ Observe(Event) }

func Record(observer Observer, event Event) {
	if observer != nil {
		observer.Observe(event)
	}
}

// Logger emits fixed structured observations. Cloud Logging can derive
// counters and distributions without a public endpoint or vendor SDK.
type Logger struct {
	logger    *slog.Logger
	role      string
	revision  string
	activeSSE atomic.Int64
}

var safeRevision = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func NewLogger(role RuntimeRole, revision string, logger *slog.Logger) *Logger {
	if logger == nil {
		logger = slog.Default()
	}
	if !safeRevision.MatchString(revision) {
		revision = "unknown"
	}
	return &Logger{
		logger: logger,
		role: bounded(string(role),
			"portal-api", "provider-ingress", "realtime", "worker", "migrate"),
		revision: revision,
	}
}

func (observer *Logger) Observe(event Event) {
	if event.signal == "" {
		return
	}
	fields := []any{
		"metric_contract", ContractVersion,
		"metric", event.signal,
		"runtime_role", observer.role,
		"revision", observer.revision,
	}
	if event.sseDelta != 0 {
		fields = append(fields, "active", observer.changeActiveSSE(event.sseDelta))
	}
	observer.logger.Info("call_center_metric", append(fields, event.fields...)...)
}

func (observer *Logger) changeActiveSSE(delta int64) int64 {
	for {
		current := observer.activeSSE.Load()
		next := max(current+delta, 0)
		if observer.activeSSE.CompareAndSwap(current, next) {
			return next
		}
	}
}

func event(signal string, fields ...any) Event {
	return Event{signal: signal, fields: fields}
}

func bounded(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "other"
}

func positive(duration time.Duration) time.Duration { return max(duration, 0) }

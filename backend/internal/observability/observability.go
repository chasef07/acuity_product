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
	CommandAnswerCaller      CommandAction = "answer_caller"
	CommandStartRingback     CommandAction = "start_ringback"
	CommandDialStaff         CommandAction = "dial_staff"
	CommandHangup            CommandAction = "hangup"
	CommandStartRecording    CommandAction = "start_recording"
	CommandCreateCredential  CommandAction = "create_credential"
	CommandDisableCredential CommandAction = "disable_credential"
	CommandCreateJWT         CommandAction = "create_jwt"
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
	PoolAcquireTimeout   PoolAcquireOutcome = "timeout"
	PoolAcquireFailed    PoolAcquireOutcome = "failed"
)

type SSECloseReason string

const (
	SSEClientClosed       SSECloseReason = "client"
	SSELifetimeEnded      SSECloseReason = "lifetime"
	SSEWriteFailed        SSECloseReason = "write_failed"
	SSERevalidationFailed SSECloseReason = "revalidation_failed"
	SSEShutdown           SSECloseReason = "shutdown"
)

type AcceptOutcome string

const (
	AcceptWon            AcceptOutcome = "won"
	AcceptAlreadyClaimed AcceptOutcome = "already_claimed"
	AcceptExpired        AcceptOutcome = "expired"
	AcceptIneligible     AcceptOutcome = "ineligible"
	AcceptDenied         AcceptOutcome = "denied"
	AcceptFailed         AcceptOutcome = "failed"
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

func ReceiptQueue(depth int64, oldestAge time.Duration) Event {
	return event("acuity_call_center_receipt_queue",
		"depth", max(depth, 0), "oldest_age_seconds", positive(oldestAge).Seconds())
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
		"action", bounded(string(action), "answer_caller", "start_ringback", "dial_staff",
			"hangup", "start_recording", "create_credential", "disable_credential", "create_jwt"),
		"outcome", bounded(string(outcome), "sent", "ambiguous", "rejected", "reconciled", "obsolete"),
		"queue_seconds", positive(queueAge).Seconds(),
		"duration_seconds", positive(duration).Seconds())
}

func DatabasePoolAcquired(outcome PoolAcquireOutcome, duration time.Duration) Event {
	return event("acuity_call_center_database_pool_acquire",
		"outcome", bounded(string(outcome), "succeeded", "timeout", "failed"),
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
			"client", "lifetime", "write_failed", "revalidation_failed", "shutdown"),
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

func CallAccepted(outcome AcceptOutcome) Event {
	return event("acuity_call_center_call_accept",
		"outcome", bounded(string(outcome),
			"won", "already_claimed", "expired", "ineligible", "denied", "failed"))
}

func CallBridged(acceptToBridge time.Duration) Event {
	return event("acuity_call_center_accept_to_bridge",
		"seconds", positive(acceptToBridge).Seconds())
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

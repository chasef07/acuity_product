package worker

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

// CallingWork is the existing durable HumanCalling work consumed by the worker.
type CallingWork interface {
	ProcessNextReceipt(context.Context) (bool, error)
	ReportReceiptQueue(context.Context) error
	ProcessNextCommand(context.Context) (bool, error)
	ProcessNextCredentialReconciliation(context.Context) (bool, error)
	ProcessNextRecoveryReconciliation(context.Context) (bool, error)
	ReconcileStaleCalls(context.Context) (int, error)
	ExpireDispositions(context.Context) (int, error)
	RecoverInterruptedCommands(context.Context) error
	ReconcileCredentials(context.Context) error
}

type MessagingWork interface {
	ProcessNextReceipt(context.Context) (bool, error)
	ProcessNextCommand(context.Context) (bool, error)
	RecoverInterruptedCommands(context.Context) error
	ReconcileNextCommand(context.Context) (bool, error)
	ProcessNextAttachment(context.Context) (bool, error)
	ExpirePendingAttachments(context.Context) error
}

type InteractionWork interface {
	ProcessNextReceipt(context.Context) (bool, error)
}

type Dependency interface {
	Ping(context.Context) error
}

type Config struct {
	WorkInterval       time.Duration
	WorkTimeout        time.Duration
	CredentialInterval time.Duration
	CredentialTimeout  time.Duration
	HealthInterval     time.Duration
	HealthTimeout      time.Duration
	MetricInterval     time.Duration
	MetricTimeout      time.Duration
	ReceiptBatchSize   int
	CommandBatchSize   int
	CommandWorkers     int
	IdleBackoffMax     time.Duration
	ErrorBackoffMin    time.Duration
	ErrorBackoffMax    time.Duration
}

type Runner struct {
	config       Config
	work         CallingWork
	messages     MessagingWork
	interactions InteractionWork
	dependency   Dependency
	jitter       func(time.Duration) time.Duration
	wait         func(context.Context, time.Duration) bool
}

func New(
	config Config,
	work CallingWork,
	messages MessagingWork,
	interactions InteractionWork,
	dependency Dependency,
) (*Runner, error) {
	if work == nil || messages == nil || interactions == nil || dependency == nil {
		return nil, fmt.Errorf("worker dependencies are required")
	}
	if config.WorkInterval <= 0 ||
		config.WorkTimeout <= 0 ||
		config.CredentialInterval <= 0 ||
		config.CredentialTimeout <= 0 ||
		config.HealthInterval <= 0 ||
		config.HealthTimeout <= 0 ||
		config.ReceiptBatchSize <= 0 ||
		config.CommandBatchSize <= 0 ||
		config.CommandWorkers <= 0 {
		return nil, fmt.Errorf("positive worker limits are required")
	}
	if config.ErrorBackoffMin == 0 {
		config.ErrorBackoffMin = config.WorkInterval
	}
	if config.IdleBackoffMax == 0 {
		config.IdleBackoffMax = 2 * time.Second
		if config.WorkInterval > config.IdleBackoffMax {
			config.IdleBackoffMax = config.WorkInterval
		}
	}
	if config.ErrorBackoffMax == 0 {
		config.ErrorBackoffMax = 10 * time.Second
		if config.ErrorBackoffMin > config.ErrorBackoffMax {
			config.ErrorBackoffMax = config.ErrorBackoffMin
		}
	}
	if config.MetricInterval == 0 {
		config.MetricInterval = 30 * time.Second
	}
	if config.MetricTimeout == 0 {
		config.MetricTimeout = config.HealthTimeout
	}
	if config.IdleBackoffMax < config.WorkInterval ||
		config.ErrorBackoffMin < 0 ||
		config.ErrorBackoffMax < config.ErrorBackoffMin {
		return nil, fmt.Errorf("valid worker backoff bounds are required")
	}
	if config.MetricInterval < 0 || config.MetricTimeout < 0 {
		return nil, fmt.Errorf("positive worker metric limits are required")
	}
	return &Runner{
		config:       config,
		work:         work,
		messages:     messages,
		interactions: interactions,
		dependency:   dependency,
		jitter:       equalJitter,
		wait:         wait,
	}, nil
}

func (runner *Runner) Run(ctx context.Context) error {
	var lanes sync.WaitGroup
	lanes.Add(6 + runner.config.CommandWorkers)
	go func() {
		defer lanes.Done()
		runner.runQueueLane(
			ctx,
			runner.config.ReceiptBatchSize,
			"provider_receipt_processing_failed",
			runner.work.ProcessNextReceipt,
		)
	}()
	for range runner.config.CommandWorkers {
		go func() {
			defer lanes.Done()
			runner.runQueueLane(
				ctx,
				runner.config.CommandBatchSize,
				"provider_command_processing_failed",
				runner.work.ProcessNextCommand,
			)
		}()
	}
	go func() {
		defer lanes.Done()
		runner.runQueueLane(
			ctx,
			runner.config.ReceiptBatchSize,
			"messaging_receipt_processing_failed",
			runner.messages.ProcessNextReceipt,
		)
	}()
	go func() {
		defer lanes.Done()
		runner.runQueueLane(
			ctx,
			runner.config.CommandBatchSize,
			"work_recovery_reconciliation_failed",
			runner.work.ProcessNextRecoveryReconciliation,
		)
	}()
	go func() {
		defer lanes.Done()
		runner.runQueueLane(
			ctx,
			runner.config.CommandBatchSize,
			"messaging_command_processing_failed",
			runner.messages.ProcessNextCommand,
		)
	}()
	go func() {
		defer lanes.Done()
		runner.runQueueLane(
			ctx,
			runner.config.ReceiptBatchSize,
			"ai_interaction_receipt_processing_failed",
			runner.interactions.ProcessNextReceipt,
		)
	}()
	go func() {
		defer lanes.Done()
		runner.runMaintenanceLane(ctx)
	}()
	lanes.Wait()
	return nil
}

func (runner *Runner) runQueueLane(
	ctx context.Context,
	batchSize int,
	failureEvent string,
	process func(context.Context) (bool, error),
) {
	backoff := newFailureBackoff(
		runner.config.ErrorBackoffMin,
		runner.config.ErrorBackoffMax,
	)
	idleMaximum := runner.config.IdleBackoffMax
	if idleMaximum < runner.config.WorkInterval {
		idleMaximum = runner.config.WorkInterval
	}
	idleBackoff := newFailureBackoff(runner.config.WorkInterval, idleMaximum)
	for ctx.Err() == nil {
		failed := false
		progressed := false
		for range batchSize {
			processed, err := runBoolWork(
				ctx,
				runner.config.WorkTimeout,
				process,
			)
			if err != nil {
				warn(ctx, failureEvent, err)
				failed = true
				break
			}
			backoff.reset()
			if !processed {
				break
			}
			progressed = true
		}
		delay := runner.config.WorkInterval
		if failed {
			idleBackoff.reset()
			delay = backoff.fail(runner.jitter)
		} else if progressed {
			idleBackoff.reset()
		} else {
			delay = idleBackoff.step()
		}
		if !runner.wait(ctx, delay) {
			return
		}
	}
}

func (runner *Runner) runMaintenanceLane(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	runner.reportReceiptQueue(ctx)
	backoff := newFailureBackoff(
		runner.config.ErrorBackoffMin,
		runner.config.ErrorBackoffMax,
	)
	delay := runner.runMaintenanceAndDelay(ctx, backoff)
	if ctx.Err() != nil {
		return
	}

	workTimer := time.NewTimer(delay)
	defer workTimer.Stop()
	credentialTicker := time.NewTicker(runner.config.CredentialInterval)
	defer credentialTicker.Stop()
	healthTicker := time.NewTicker(runner.config.HealthInterval)
	defer healthTicker.Stop()
	metricTicker := time.NewTicker(runner.config.MetricInterval)
	defer metricTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-workTimer.C:
			delay = runner.runMaintenanceAndDelay(ctx, backoff)
			if ctx.Err() != nil {
				return
			}
			workTimer.Reset(delay)
		case <-credentialTicker.C:
			runner.reconcileCredentials(ctx)
		case <-healthTicker.C:
			runner.checkDependency(ctx)
		case <-metricTicker.C:
			runner.reportReceiptQueue(ctx)
		}
	}
}

func (runner *Runner) runMaintenanceAndDelay(
	ctx context.Context,
	backoff *failureBackoff,
) time.Duration {
	if runner.runMaintenance(ctx) {
		return backoff.fail(runner.jitter)
	}
	backoff.reset()
	return runner.config.WorkInterval
}

func (runner *Runner) runMaintenance(ctx context.Context) bool {
	failed := false
	if _, err := runCountWork(ctx, runner.config.WorkTimeout, runner.work.ReconcileStaleCalls); err != nil {
		warn(ctx, "calling_stale_reconciliation_failed", err)
		failed = true
	}
	if ctx.Err() != nil {
		return failed
	}
	if _, err := runCountWork(ctx, runner.config.WorkTimeout, runner.work.ExpireDispositions); err != nil {
		warn(ctx, "calling_disposition_expiry_failed", err)
		failed = true
	}
	if ctx.Err() != nil {
		return failed
	}
	if err := runWork(ctx, runner.config.WorkTimeout, runner.work.RecoverInterruptedCommands); err != nil {
		warn(ctx, "provider_command_recovery_failed", err)
		failed = true
	}
	if ctx.Err() != nil {
		return failed
	}
	if _, err := runBoolWork(
		ctx,
		runner.config.WorkTimeout,
		runner.work.ProcessNextCredentialReconciliation,
	); err != nil {
		warn(ctx, "provider_credential_reconciliation_failed", err)
		failed = true
	}
	if ctx.Err() != nil {
		return failed
	}
	if err := runWork(
		ctx,
		runner.config.WorkTimeout,
		runner.messages.RecoverInterruptedCommands,
	); err != nil {
		warn(ctx, "messaging_command_recovery_failed", err)
		failed = true
	}
	if ctx.Err() != nil {
		return failed
	}
	if _, err := runBoolWork(
		ctx,
		runner.config.WorkTimeout,
		runner.messages.ReconcileNextCommand,
	); err != nil {
		warn(ctx, "messaging_command_reconciliation_failed", err)
		failed = true
	}
	if ctx.Err() != nil {
		return failed
	}
	if _, err := runBoolWork(
		ctx,
		runner.config.WorkTimeout,
		runner.messages.ProcessNextAttachment,
	); err != nil {
		warn(ctx, "messaging_attachment_processing_failed", err)
		failed = true
	}
	if ctx.Err() != nil {
		return failed
	}
	if err := runWork(
		ctx,
		runner.config.WorkTimeout,
		runner.messages.ExpirePendingAttachments,
	); err != nil {
		warn(ctx, "messaging_attachment_expiry_failed", err)
		failed = true
	}
	return failed
}

func (runner *Runner) reconcileCredentials(ctx context.Context) {
	if err := runWork(
		ctx,
		runner.config.CredentialTimeout,
		runner.work.ReconcileCredentials,
	); err != nil {
		warn(ctx, "calling_credential_reconciliation_failed", err)
	}
}

func (runner *Runner) checkDependency(ctx context.Context) {
	if err := runWork(ctx, runner.config.HealthTimeout, runner.dependency.Ping); err != nil {
		warn(ctx, "worker_dependency_unavailable", err)
	}
}

func (runner *Runner) reportReceiptQueue(ctx context.Context) {
	if err := runWork(
		ctx,
		runner.config.MetricTimeout,
		runner.work.ReportReceiptQueue,
	); err != nil {
		warn(ctx, "provider_receipt_queue_observation_failed", err)
	}
}

func runWork(
	ctx context.Context,
	timeout time.Duration,
	work func(context.Context) error,
) error {
	workContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return work(workContext)
}

func runBoolWork(
	ctx context.Context,
	timeout time.Duration,
	work func(context.Context) (bool, error),
) (bool, error) {
	workContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return work(workContext)
}

func runCountWork(
	ctx context.Context,
	timeout time.Duration,
	work func(context.Context) (int, error),
) (int, error) {
	workContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return work(workContext)
}

func warn(ctx context.Context, event string, err error) {
	if ctx.Err() == nil {
		slog.Warn(event, "error", err)
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type failureBackoff struct {
	min  time.Duration
	max  time.Duration
	next time.Duration
}

func newFailureBackoff(minimum, maximum time.Duration) *failureBackoff {
	return &failureBackoff{min: minimum, max: maximum, next: minimum}
}

func (backoff *failureBackoff) fail(
	jitter func(time.Duration) time.Duration,
) time.Duration {
	return jitter(backoff.step())
}

func (backoff *failureBackoff) step() time.Duration {
	delay := backoff.next
	if backoff.next < backoff.max {
		if backoff.next > backoff.max/2 {
			backoff.next = backoff.max
		} else {
			backoff.next *= 2
		}
	}
	return delay
}

func (backoff *failureBackoff) reset() {
	backoff.next = backoff.min
}

func equalJitter(delay time.Duration) time.Duration {
	minimum := delay/2 + delay%2
	spread := delay - minimum
	if spread == 0 {
		return delay
	}
	return minimum + time.Duration(rand.Int64N(int64(spread)+1))
}

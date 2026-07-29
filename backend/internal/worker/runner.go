package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// CallingWork is the existing durable HumanCalling work consumed by the worker.
type CallingWork interface {
	ProcessNextReceipt(context.Context) (bool, error)
	ProcessNextCommand(context.Context) (bool, error)
	ProcessNextCredentialReconciliation(context.Context) (bool, error)
	ExpireOffers(context.Context) (int, error)
	ExpireConnections(context.Context) (int, error)
	RecoverInterruptedCommands(context.Context) error
	ReconcileConfirmedHangups(context.Context) (int, error)
	ReconcileCredentials(context.Context) error
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
	ReceiptBatchSize   int
	CommandBatchSize   int
	CommandWorkers     int
}

type Runner struct {
	config     Config
	work       CallingWork
	dependency Dependency
}

func New(config Config, work CallingWork, dependency Dependency) (*Runner, error) {
	if work == nil || dependency == nil {
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
	return &Runner{config: config, work: work, dependency: dependency}, nil
}

func (runner *Runner) Run(ctx context.Context) error {
	var lanes sync.WaitGroup
	lanes.Add(2 + runner.config.CommandWorkers)
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
	for ctx.Err() == nil {
		for range batchSize {
			processed, err := runBoolWork(
				ctx,
				runner.config.WorkTimeout,
				process,
			)
			if err != nil {
				warn(ctx, failureEvent, err)
				break
			}
			if !processed {
				break
			}
		}
		if !wait(ctx, runner.config.WorkInterval) {
			return
		}
	}
}

func (runner *Runner) runMaintenanceLane(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	runner.runMaintenance(ctx)
	if ctx.Err() != nil {
		return
	}

	workTicker := time.NewTicker(runner.config.WorkInterval)
	defer workTicker.Stop()
	credentialTicker := time.NewTicker(runner.config.CredentialInterval)
	defer credentialTicker.Stop()
	healthTicker := time.NewTicker(runner.config.HealthInterval)
	defer healthTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-workTicker.C:
			runner.runMaintenance(ctx)
		case <-credentialTicker.C:
			runner.reconcileCredentials(ctx)
		case <-healthTicker.C:
			runner.checkDependency(ctx)
		}
	}
}

func (runner *Runner) runMaintenance(ctx context.Context) {
	if _, err := runCountWork(ctx, runner.config.WorkTimeout, runner.work.ExpireOffers); err != nil {
		warn(ctx, "calling_offer_expiry_failed", err)
	}
	if ctx.Err() != nil {
		return
	}
	if _, err := runCountWork(ctx, runner.config.WorkTimeout, runner.work.ExpireConnections); err != nil {
		warn(ctx, "calling_connection_expiry_failed", err)
	}
	if ctx.Err() != nil {
		return
	}
	if err := runWork(ctx, runner.config.WorkTimeout, runner.work.RecoverInterruptedCommands); err != nil {
		warn(ctx, "provider_command_recovery_failed", err)
	}
	if ctx.Err() != nil {
		return
	}
	if _, err := runCountWork(
		ctx,
		runner.config.WorkTimeout,
		runner.work.ReconcileConfirmedHangups,
	); err != nil {
		warn(ctx, "provider_hangup_reconciliation_failed", err)
	}
	if ctx.Err() != nil {
		return
	}
	if _, err := runBoolWork(
		ctx,
		runner.config.WorkTimeout,
		runner.work.ProcessNextCredentialReconciliation,
	); err != nil {
		warn(ctx, "provider_credential_reconciliation_failed", err)
	}
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

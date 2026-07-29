package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunnerKeepsReceiptsAndReadyCommandsMovingDuringSlowProviderWork(t *testing.T) {
	work := newControlledWork()
	work.reconciliationStarted = make(chan struct{}, 1)
	work.blockReconciliation = true
	runner, err := New(Config{
		WorkInterval:       time.Millisecond,
		WorkTimeout:        time.Second,
		CredentialInterval: time.Hour,
		CredentialTimeout:  time.Second,
		HealthInterval:     time.Hour,
		HealthTimeout:      time.Second,
		ReceiptBatchSize:   1,
		CommandBatchSize:   1,
		CommandWorkers:     2,
	}, work, healthyDependency{})
	if err != nil {
		t.Fatalf("create worker runner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()

	waitForSignal(t, work.slowCommandStarted, "slow provider command to start")
	waitForSignal(t, work.reconciliationStarted, "slow provider reconciliation to start")
	waitForSignal(t, work.receiptProjected, "receipt projection during slow provider command")
	waitForSignal(t, work.readyCommandFinished, "another ready provider command")

	close(work.releaseSlowCommand)
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run worker: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker runner did not stop after cancellation")
	}
}

func TestRunnerDoesNotStartLaneWorkAfterCancellation(t *testing.T) {
	work := newControlledWork()
	work.maintenanceStarted = make(chan struct{}, 1)
	runner, err := New(Config{
		WorkInterval:       time.Millisecond,
		WorkTimeout:        time.Second,
		CredentialInterval: time.Hour,
		CredentialTimeout:  time.Second,
		HealthInterval:     time.Hour,
		HealthTimeout:      time.Second,
		ReceiptBatchSize:   1,
		CommandBatchSize:   1,
		CommandWorkers:     2,
	}, work, healthyDependency{})
	if err != nil {
		t.Fatalf("create worker runner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("run cancelled worker: %v", err)
	}
	select {
	case <-work.maintenanceStarted:
		t.Fatal("maintenance started after worker cancellation")
	default:
	}
}

func TestRunnerBoundsReceiptDrainBeforeYielding(t *testing.T) {
	work := &hotReceiptWork{
		controlledWork: newControlledWork(),
		projected:      make(chan struct{}, 4),
	}
	runner, err := New(Config{
		WorkInterval:       time.Hour,
		WorkTimeout:        time.Second,
		CredentialInterval: time.Hour,
		CredentialTimeout:  time.Second,
		HealthInterval:     time.Hour,
		HealthTimeout:      time.Second,
		ReceiptBatchSize:   3,
		CommandBatchSize:   1,
		CommandWorkers:     2,
	}, work, healthyDependency{})
	if err != nil {
		t.Fatalf("create worker runner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()
	for range 3 {
		waitForSignal(t, work.projected, "receipt in bounded drain")
	}
	select {
	case <-work.projected:
		t.Fatal("receipt lane exceeded its drain bound before yielding")
	case <-time.After(25 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run worker: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker runner did not stop after cancellation")
	}
}

func TestRunnerStopsMaintenanceLaneBetweenOperations(t *testing.T) {
	work := newControlledWork()
	work.offerExpiryStarted = make(chan struct{}, 1)
	work.blockOfferExpiry = true
	work.maintenanceStarted = make(chan struct{}, 1)
	runner, err := New(Config{
		WorkInterval:       time.Hour,
		WorkTimeout:        time.Second,
		CredentialInterval: time.Hour,
		CredentialTimeout:  time.Second,
		HealthInterval:     time.Hour,
		HealthTimeout:      time.Second,
		ReceiptBatchSize:   1,
		CommandBatchSize:   1,
		CommandWorkers:     2,
	}, work, healthyDependency{})
	if err != nil {
		t.Fatalf("create worker runner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()
	waitForSignal(t, work.offerExpiryStarted, "maintenance operation to start")
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run worker: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker runner did not stop after cancellation")
	}
	select {
	case <-work.maintenanceStarted:
		t.Fatal("maintenance continued after worker cancellation")
	default:
	}
}

type controlledWork struct {
	commandCalls          atomic.Int32
	receiptCalls          atomic.Int32
	slowCommandStarted    chan struct{}
	releaseSlowCommand    chan struct{}
	readyCommandFinished  chan struct{}
	receiptProjected      chan struct{}
	maintenanceStarted    chan struct{}
	offerExpiryStarted    chan struct{}
	reconciliationStarted chan struct{}
	blockOfferExpiry      bool
	blockReconciliation   bool
}

func newControlledWork() *controlledWork {
	return &controlledWork{
		slowCommandStarted:   make(chan struct{}, 1),
		releaseSlowCommand:   make(chan struct{}),
		readyCommandFinished: make(chan struct{}, 1),
		receiptProjected:     make(chan struct{}, 1),
	}
}

func (work *controlledWork) ProcessNextReceipt(context.Context) (bool, error) {
	if work.receiptCalls.Add(1) == 1 {
		work.receiptProjected <- struct{}{}
		return true, nil
	}
	return false, nil
}

type hotReceiptWork struct {
	*controlledWork
	projected chan struct{}
}

func (work *hotReceiptWork) ProcessNextReceipt(context.Context) (bool, error) {
	work.projected <- struct{}{}
	return true, nil
}

func (work *controlledWork) ProcessNextCommand(ctx context.Context) (bool, error) {
	switch work.commandCalls.Add(1) {
	case 1:
		work.slowCommandStarted <- struct{}{}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-work.releaseSlowCommand:
			return true, nil
		}
	case 2:
		work.readyCommandFinished <- struct{}{}
		return true, nil
	default:
		return false, nil
	}
}

func (*controlledWork) ProcessNextCredentialReconciliation(context.Context) (bool, error) {
	return false, nil
}

func (work *controlledWork) ExpireOffers(ctx context.Context) (int, error) {
	if work.offerExpiryStarted != nil {
		work.offerExpiryStarted <- struct{}{}
	}
	if work.blockOfferExpiry {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return 0, nil
}

func (work *controlledWork) signalMaintenance() {
	if work.maintenanceStarted != nil {
		work.maintenanceStarted <- struct{}{}
	}
}

func (work *controlledWork) ExpireConnections(context.Context) (int, error) {
	work.signalMaintenance()
	return 0, nil
}

func (*controlledWork) RecoverInterruptedCommands(context.Context) error {
	return nil
}

func (work *controlledWork) ReconcileConfirmedHangups(ctx context.Context) (int, error) {
	if work.reconciliationStarted != nil {
		work.reconciliationStarted <- struct{}{}
	}
	if work.blockReconciliation {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return 0, nil
}

func (*controlledWork) ReconcileCredentials(context.Context) error {
	return nil
}

type healthyDependency struct{}

func (healthyDependency) Ping(context.Context) error {
	return nil
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunnerDoesNotStartLaneWorkAfterCancellation(t *testing.T) {
	work := newControlledWork()
	work.maintenanceStarted = make(chan struct{}, 1)
	runner, err := New(Config{
		WorkInterval:                  time.Millisecond,
		WorkTimeout:                   time.Second,
		CredentialInterval:            time.Hour,
		CredentialTimeout:             time.Second,
		HealthInterval:                time.Hour,
		HealthTimeout:                 time.Second,
		ReceiptBatchSize:              1,
		RecoveryAndMessagingBatchSize: 1,
		ProviderCommandBatchSize:      1,
		CommandWorkers:                2,
	}, work, &controlledMessagingWork{}, &controlledInteractionWork{}, healthyDependency{})
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
		WorkInterval:                  time.Hour,
		WorkTimeout:                   time.Second,
		CredentialInterval:            time.Hour,
		CredentialTimeout:             time.Second,
		HealthInterval:                time.Hour,
		HealthTimeout:                 time.Second,
		ReceiptBatchSize:              3,
		RecoveryAndMessagingBatchSize: 1,
		ProviderCommandBatchSize:      1,
		CommandWorkers:                2,
	}, work, &controlledMessagingWork{}, &controlledInteractionWork{}, healthyDependency{})
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
	work.staleReconciliationStarted = make(chan struct{}, 1)
	work.blockStaleReconciliation = true
	work.maintenanceStarted = make(chan struct{}, 1)
	runner, err := New(Config{
		WorkInterval:                  time.Hour,
		WorkTimeout:                   time.Second,
		CredentialInterval:            time.Hour,
		CredentialTimeout:             time.Second,
		HealthInterval:                time.Hour,
		HealthTimeout:                 time.Second,
		ReceiptBatchSize:              1,
		RecoveryAndMessagingBatchSize: 1,
		ProviderCommandBatchSize:      1,
		CommandWorkers:                2,
	}, work, &controlledMessagingWork{}, &controlledInteractionWork{}, healthyDependency{})
	if err != nil {
		t.Fatalf("create worker runner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()
	waitForSignal(t, work.staleReconciliationStarted, "maintenance operation to start")
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

func TestQueueLaneBacksOffConsecutiveErrorsAndResetsAfterProgress(t *testing.T) {
	var delays []time.Duration
	runner := &Runner{
		config: Config{
			WorkInterval:    10 * time.Millisecond,
			WorkTimeout:     time.Second,
			ErrorBackoffMin: 100 * time.Millisecond,
			ErrorBackoffMax: 400 * time.Millisecond,
		},
		jitter: func(delay time.Duration) time.Duration {
			return delay - time.Millisecond
		},
		wait: func(_ context.Context, delay time.Duration) bool {
			delays = append(delays, delay)
			return len(delays) < 7
		},
	}
	results := []struct {
		processed bool
		err       error
	}{
		{err: errors.New("first failure")},
		{err: errors.New("second failure")},
		{err: errors.New("third failure")},
		{err: errors.New("bounded failure")},
		{processed: true},
		{},
		{err: errors.New("failure after progress")},
	}
	next := 0
	runner.runQueueLane(
		context.Background(),
		1,
		"test_failure",
		func(context.Context) (bool, error) {
			result := results[next]
			next++
			return result.processed, result.err
		},
	)

	want := []time.Duration{
		99 * time.Millisecond,
		199 * time.Millisecond,
		399 * time.Millisecond,
		399 * time.Millisecond,
		10 * time.Millisecond,
		10 * time.Millisecond,
		99 * time.Millisecond,
	}
	if len(delays) != len(want) {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
	for index := range want {
		if delays[index] != want[index] {
			t.Fatalf("delays = %v, want %v", delays, want)
		}
	}
}

func TestProviderCommandCoordinatorBoundsClaimFailuresAndResetsAfterSuccess(t *testing.T) {
	work := &sequencedProviderCommandWork{results: []providerCommandClaimResult{
		{err: errors.New("first claim failure")},
		{err: errors.New("second claim failure")},
		{claimed: true},
		{},
	}}
	var delays []time.Duration
	runner := &Runner{
		config: Config{
			WorkInterval:             10 * time.Millisecond,
			WorkTimeout:              time.Second,
			ProviderCommandBatchSize: 1,
			CommandWorkers:           1,
			ErrorBackoffMin:          100 * time.Millisecond,
			ErrorBackoffMax:          400 * time.Millisecond,
		},
		work: work,
		jitter: func(delay time.Duration) time.Duration {
			return delay - time.Millisecond
		},
		wait: func(_ context.Context, delay time.Duration) bool {
			delays = append(delays, delay)
			return len(delays) < 4
		},
	}

	runner.runProviderCommands(context.Background())

	want := []time.Duration{
		99 * time.Millisecond,
		199 * time.Millisecond,
		0,
		10 * time.Millisecond,
	}
	if len(delays) != len(want) {
		t.Fatalf("provider-command claim delays = %v, want %v", delays, want)
	}
	for index := range want {
		if delays[index] != want[index] {
			t.Fatalf("provider-command claim delays = %v, want %v", delays, want)
		}
	}
	if got := work.executed.Load(); got != 1 {
		t.Fatalf("provider-command effects after claim recovery = %d, want 1", got)
	}
}

func TestProviderCommandExecutorBoundsFailuresAndResetsAfterSuccess(t *testing.T) {
	providerErrors := []error{
		errors.New("first provider failure"),
		errors.New("second provider failure"),
		errors.New("third provider failure"),
		errors.New("bounded provider failure"),
		nil,
		errors.New("provider failure after success"),
	}
	commands := make(chan func(context.Context) error, len(providerErrors))
	for _, providerErr := range providerErrors {
		commands <- func(context.Context) error { return providerErr }
	}
	close(commands)
	available := make(chan struct{}, len(providerErrors))
	var delays []time.Duration
	runner := &Runner{
		config: Config{
			WorkTimeout:     time.Second,
			ErrorBackoffMin: 100 * time.Millisecond,
			ErrorBackoffMax: 400 * time.Millisecond,
		},
		jitter: func(delay time.Duration) time.Duration {
			return delay - time.Millisecond
		},
		wait: func(_ context.Context, delay time.Duration) bool {
			delays = append(delays, delay)
			return true
		},
	}

	runner.runProviderCommandExecutor(context.Background(), commands, available)

	want := []time.Duration{
		99 * time.Millisecond,
		199 * time.Millisecond,
		399 * time.Millisecond,
		399 * time.Millisecond,
		99 * time.Millisecond,
	}
	if len(delays) != len(want) {
		t.Fatalf("provider-command executor delays = %v, want %v", delays, want)
	}
	for index := range want {
		if delays[index] != want[index] {
			t.Fatalf("provider-command executor delays = %v, want %v", delays, want)
		}
	}
}

type controlledWork struct {
	commandCalls               atomic.Int32
	receiptCalls               atomic.Int32
	slowCommandStarted         chan struct{}
	releaseSlowCommand         chan struct{}
	readyCommandFinished       chan struct{}
	receiptProjected           chan struct{}
	maintenanceStarted         chan struct{}
	staleReconciliationStarted chan struct{}
	receiptQueueReported       chan struct{}
	blockStaleReconciliation   bool
}

type providerCommandClaimResult struct {
	claimed bool
	err     error
}

type sequencedProviderCommandWork struct {
	*controlledWork
	results  []providerCommandClaimResult
	next     int
	executed atomic.Int32
}

func (work *sequencedProviderCommandWork) ClaimNextCommand(
	context.Context,
) (func(context.Context) error, bool, error) {
	result := work.results[work.next]
	work.next++
	if !result.claimed || result.err != nil {
		return nil, result.claimed, result.err
	}
	return func(context.Context) error {
		work.executed.Add(1)
		return nil
	}, true, nil
}

func newControlledWork() *controlledWork {
	return &controlledWork{
		slowCommandStarted:   make(chan struct{}, 1),
		releaseSlowCommand:   make(chan struct{}),
		readyCommandFinished: make(chan struct{}, 1),
		receiptProjected:     make(chan struct{}, 1),
		receiptQueueReported: make(chan struct{}, 1),
	}
}

func (work *controlledWork) ReportReceiptQueue(context.Context) error {
	if work.receiptQueueReported != nil {
		select {
		case work.receiptQueueReported <- struct{}{}:
		default:
		}
	}
	return nil
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
	command, claimed, err := work.ClaimNextCommand(ctx)
	if err != nil || !claimed {
		return claimed, err
	}
	return true, command(ctx)
}

func (work *controlledWork) ClaimNextCommand(
	context.Context,
) (func(context.Context) error, bool, error) {
	switch work.commandCalls.Add(1) {
	case 1:
		return func(ctx context.Context) error {
			work.slowCommandStarted <- struct{}{}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-work.releaseSlowCommand:
				return nil
			}
		}, true, nil
	case 2:
		return func(context.Context) error {
			work.readyCommandFinished <- struct{}{}
			return nil
		}, true, nil
	default:
		return nil, false, nil
	}
}

func (*controlledWork) ProcessNextCredentialReconciliation(context.Context) (bool, error) {
	return false, nil
}

func (*controlledWork) ProcessNextRecoveryReconciliation(context.Context) (bool, error) {
	return false, nil
}

func (*controlledWork) ProcessNextRecordingReconciliation(context.Context) (bool, error) {
	return false, nil
}

func (*controlledWork) ProcessNextRecordingRetention(context.Context) (bool, error) {
	return false, nil
}

func (work *controlledWork) MaintainOutgoingCallLegs(ctx context.Context) (bool, error) {
	if work.staleReconciliationStarted != nil {
		work.staleReconciliationStarted <- struct{}{}
	}
	if work.blockStaleReconciliation {
		<-ctx.Done()
		return false, ctx.Err()
	}
	work.signalMaintenance()
	return false, nil
}

func (*controlledWork) ExpireDispositions(context.Context) (int, error) {
	return 0, nil
}

func (*controlledWork) ExpireStaffTransfers(context.Context) (int, error) {
	return 0, nil
}

func (work *controlledWork) signalMaintenance() {
	if work.maintenanceStarted != nil {
		work.maintenanceStarted <- struct{}{}
	}
}

func (*controlledWork) ReconcileCredentials(context.Context) error {
	return nil
}

type healthyDependency struct{}

func (healthyDependency) Ping(context.Context) error {
	return nil
}

type controlledMessagingWork struct {
	receiptProcessed      chan struct{}
	acknowledgementQueued chan struct{}
	commandProcessed      chan struct{}
	attachmentProcessed   chan struct{}
}

func (work *controlledMessagingWork) QueueNextTaskAcknowledgement(context.Context) (bool, error) {
	select {
	case work.acknowledgementQueued <- struct{}{}:
		return true, nil
	default:
		return false, nil
	}
}

type controlledInteractionWork struct {
	processed chan struct{}
}

func (work *controlledInteractionWork) ProcessNextReceipt(context.Context) (bool, error) {
	select {
	case work.processed <- struct{}{}:
		return true, nil
	default:
		return false, nil
	}
}

func (work *controlledMessagingWork) ProcessNextReceipt(context.Context) (bool, error) {
	select {
	case work.receiptProcessed <- struct{}{}:
		return true, nil
	default:
		return false, nil
	}
}

func (work *controlledMessagingWork) ProcessNextCommand(context.Context) (bool, error) {
	select {
	case work.commandProcessed <- struct{}{}:
		return true, nil
	default:
		return false, nil
	}
}

func (*controlledMessagingWork) RecoverInterruptedCommands(context.Context) error {
	return nil
}

func (*controlledMessagingWork) ReconcileNextCommand(context.Context) (bool, error) {
	return false, nil
}

func (work *controlledMessagingWork) ProcessNextAttachment(context.Context) (bool, error) {
	select {
	case work.attachmentProcessed <- struct{}{}:
		return true, nil
	default:
		return false, nil
	}
}

func (*controlledMessagingWork) ExpirePendingAttachments(context.Context) error {
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

package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderCommandWorkersShareOneIdlePoll(t *testing.T) {
	work := &idleProviderCommandWork{controlledWork: newControlledWork()}
	runner := &Runner{
		config: Config{
			WorkInterval:             250 * time.Millisecond,
			WorkTimeout:              10 * time.Second,
			ProviderCommandBatchSize: 8,
			CommandWorkers:           10,
			IdleBackoffMax:           2 * time.Second,
		},
		work: work,
		wait: func(context.Context, time.Duration) bool {
			return false
		},
	}

	runner.runProviderCommands(context.Background())

	got := work.commandCalls.Load()
	if got > 1 {
		t.Fatalf("idle provider-command acquisition attempts = %d, want at most 1 per polling interval", got)
	}
	reduction := 1 - float64(got)/float64(runner.config.CommandWorkers)
	if reduction < 0.75 {
		t.Fatalf("idle provider-command acquisition reduction = %.0f%%, want at least 75%%", reduction*100)
	}
	t.Logf("idle provider-command acquisition attempts per interval = %d (%.0f%% reduction from %d workers)",
		got, reduction*100, runner.config.CommandWorkers)
}

func TestProviderCommandCoordinatorKeepsPickupInsideWorkInterval(t *testing.T) {
	var delays []time.Duration
	work := &eligibleProviderCommandWork{}
	runner := &Runner{
		config: Config{
			WorkInterval:             250 * time.Millisecond,
			WorkTimeout:              time.Second,
			ProviderCommandBatchSize: 8,
			CommandWorkers:           10,
			IdleBackoffMax:           2 * time.Second,
		},
		work: work,
		wait: func(_ context.Context, delay time.Duration) bool {
			delays = append(delays, delay)
			if len(delays) == 1 {
				work.eligible.Store(true)
				return true
			}
			return false
		},
	}

	runner.runProviderCommands(context.Background())

	want := []time.Duration{250 * time.Millisecond, 250 * time.Millisecond}
	if len(delays) != len(want) {
		t.Fatalf("provider-command polling delays = %v, want %v", delays, want)
	}
	for index := range want {
		if delays[index] != want[index] {
			t.Fatalf("provider-command polling delays = %v, want %v", delays, want)
		}
	}
	if got := work.executed.Load(); got != 1 {
		t.Fatalf("newly eligible provider-command effects = %d, want 1", got)
	}
	if pickupBound := delays[0]; pickupBound >= time.Second {
		t.Fatalf("provider-command pickup bound = %s, want under 1s", pickupBound)
	}
}

func TestProviderCommandCoordinatorClaimsExactBatchBeforeYielding(t *testing.T) {
	work := &readyProviderCommandWork{}
	var delays []time.Duration
	runner := &Runner{
		config: Config{
			WorkInterval:             50 * time.Millisecond,
			WorkTimeout:              time.Second,
			ProviderCommandBatchSize: 8,
			CommandWorkers:           10,
		},
		work: work,
		wait: func(_ context.Context, delay time.Duration) bool {
			delays = append(delays, delay)
			return false
		},
	}

	runner.runProviderCommands(context.Background())

	if got := work.claimed.Load(); got != 8 {
		t.Fatalf("provider commands claimed before yield = %d, want 8", got)
	}
	if got := work.executed.Load(); got != 8 {
		t.Fatalf("provider-command effects before yield = %d, want 8", got)
	}
	want := []time.Duration{50 * time.Millisecond}
	if len(delays) != len(want) || delays[0] != want[0] {
		t.Fatalf("provider command yield delays = %v, want %v", delays, want)
	}
}

func TestRunnerKeepsReceiptsAndReadyCommandsMovingDuringSlowProviderWork(t *testing.T) {
	work := newControlledWork()
	work.staleReconciliationStarted = make(chan struct{}, 1)
	work.blockStaleReconciliation = true
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
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()

	waitForSignal(t, work.slowCommandStarted, "slow provider command to start")
	waitForSignal(t, work.receiptQueueReported, "receipt queue metric")
	waitForSignal(t, work.staleReconciliationStarted, "slow CallLeg reconciliation to start")
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

func TestRunnerProcessesMessagingInIndependentLanes(t *testing.T) {
	calling := newControlledWork()
	messages := &controlledMessagingWork{
		receiptProcessed:      make(chan struct{}, 1),
		acknowledgementQueued: make(chan struct{}, 1),
		commandProcessed:      make(chan struct{}, 1),
		attachmentProcessed:   make(chan struct{}, 1),
	}
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
		CommandWorkers:                1,
	}, calling, messages, &controlledInteractionWork{}, healthyDependency{})
	if err != nil {
		t.Fatalf("create messaging worker runner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()

	waitForSignal(t, messages.receiptProcessed, "Message receipt")
	waitForSignal(t, messages.acknowledgementQueued, "automatic Task acknowledgement")
	waitForSignal(t, messages.commandProcessed, "Message command")
	waitForSignal(t, messages.attachmentProcessed, "Message attachment")
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run messaging worker: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("messaging worker did not stop after cancellation")
	}
}

func TestRunnerRecoversAIInteractionReceiptsInIndependentLane(t *testing.T) {
	calling := newControlledWork()
	messages := &controlledMessagingWork{
		receiptProcessed:    make(chan struct{}, 1),
		commandProcessed:    make(chan struct{}, 1),
		attachmentProcessed: make(chan struct{}, 1),
	}
	interactions := &controlledInteractionWork{processed: make(chan struct{}, 1)}
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
		CommandWorkers:                1,
	}, calling, messages, interactions, healthyDependency{})
	if err != nil {
		t.Fatalf("create AI Interaction worker runner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()
	waitForSignal(t, interactions.processed, "AI Interaction receipt")
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run AI Interaction worker: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AI Interaction worker runner did not stop after cancellation")
	}
}

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
		10 * time.Millisecond,
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

func TestProviderCommandsStopCleanlyWhenEffectsAreCanceled(t *testing.T) {
	const workerCount = 10
	work := &cancelableProviderCommandWork{
		controlledWork: newControlledWork(),
		started:        make(chan struct{}, workerCount),
	}
	runner := &Runner{
		config: Config{
			WorkInterval:             time.Millisecond,
			WorkTimeout:              time.Second,
			ProviderCommandBatchSize: 8,
			CommandWorkers:           workerCount,
			ErrorBackoffMin:          time.Millisecond,
			ErrorBackoffMax:          10 * time.Millisecond,
		},
		work:   work,
		jitter: func(delay time.Duration) time.Duration { return delay },
		wait:   wait,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.runProviderCommands(ctx)
		close(done)
	}()
	for index := 1; index <= workerCount; index++ {
		waitForSignal(t, work.started, "provider-command effect")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("provider-command coordinator and executors did not stop after cancellation")
	}
	if got := work.executed.Load(); got != workerCount {
		t.Fatalf("provider-command effects started before cancellation = %d, want %d", got, workerCount)
	}
}

func TestQueueLaneBacksOffConsecutiveEmptyClaimsAndResetsAfterProgress(t *testing.T) {
	var delays []time.Duration
	runner := &Runner{
		config: Config{
			WorkInterval:   10 * time.Millisecond,
			WorkTimeout:    time.Second,
			IdleBackoffMax: 80 * time.Millisecond,
		},
		wait: func(_ context.Context, delay time.Duration) bool {
			delays = append(delays, delay)
			return len(delays) < 7
		},
	}
	results := []bool{false, false, false, false, false, true, false}
	next := 0
	runner.runQueueLane(
		context.Background(),
		1,
		"test_failure",
		func(context.Context) (bool, error) {
			processed := results[next]
			next++
			return processed, nil
		},
	)

	want := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
		80 * time.Millisecond,
		10 * time.Millisecond,
		10 * time.Millisecond,
	}
	if len(delays) != len(want) {
		t.Fatalf("empty-claim delays = %v, want %v", delays, want)
	}
	for index := range want {
		if delays[index] != want[index] {
			t.Fatalf("empty-claim delays = %v, want %v", delays, want)
		}
	}
}

func TestMaintenanceLaneBacksOffErrorsAndResetsAfterSuccess(t *testing.T) {
	work := &credentialReconciliationFailureWork{
		controlledWork: newControlledWork(),
	}
	runner := &Runner{
		config: Config{
			WorkInterval:    10 * time.Millisecond,
			WorkTimeout:     time.Second,
			ErrorBackoffMin: 100 * time.Millisecond,
			ErrorBackoffMax: 400 * time.Millisecond,
		},
		work:     work,
		messages: &controlledMessagingWork{},
		jitter: func(delay time.Duration) time.Duration {
			return delay - time.Millisecond
		},
	}
	backoff := newFailureBackoff(
		runner.config.ErrorBackoffMin,
		runner.config.ErrorBackoffMax,
	)
	var delays []time.Duration
	for range 4 {
		delays = append(
			delays,
			runner.runMaintenanceAndDelay(context.Background(), backoff),
		)
	}
	want := []time.Duration{
		99 * time.Millisecond,
		199 * time.Millisecond,
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

type idleProviderCommandWork struct {
	*controlledWork
}

func (work *idleProviderCommandWork) ClaimNextCommand(
	context.Context,
) (func(context.Context) error, bool, error) {
	work.commandCalls.Add(1)
	return nil, false, nil
}

type eligibleProviderCommandWork struct {
	*controlledWork
	eligible atomic.Bool
	executed atomic.Int32
}

func (work *eligibleProviderCommandWork) ClaimNextCommand(
	context.Context,
) (func(context.Context) error, bool, error) {
	if !work.eligible.CompareAndSwap(true, false) {
		return nil, false, nil
	}
	return func(context.Context) error {
		work.executed.Add(1)
		return nil
	}, true, nil
}

type readyProviderCommandWork struct {
	*controlledWork
	claimed  atomic.Int32
	executed atomic.Int32
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

type cancelableProviderCommandWork struct {
	*controlledWork
	started  chan struct{}
	claimed  atomic.Int32
	executed atomic.Int32
}

func (work *cancelableProviderCommandWork) ClaimNextCommand(
	context.Context,
) (func(context.Context) error, bool, error) {
	if work.claimed.Add(1) > 10 {
		return nil, false, nil
	}
	return func(ctx context.Context) error {
		work.executed.Add(1)
		work.started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}, true, nil
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

func (work *readyProviderCommandWork) ClaimNextCommand(
	context.Context,
) (func(context.Context) error, bool, error) {
	work.claimed.Add(1)
	return func(context.Context) error {
		work.executed.Add(1)
		return nil
	}, true, nil
}

type credentialReconciliationFailureWork struct {
	*controlledWork
	reconciliationCalls int
}

func (work *credentialReconciliationFailureWork) ProcessNextCredentialReconciliation(
	context.Context,
) (bool, error) {
	work.reconciliationCalls++
	if work.reconciliationCalls == 1 ||
		work.reconciliationCalls == 2 ||
		work.reconciliationCalls == 4 {
		return true, errors.New("provider credential lookup unavailable")
	}
	return false, nil
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

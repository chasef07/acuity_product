package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type slowStorageMaintenance struct {
	controlledMessagingWork
	started chan struct{}
}

func (s *slowStorageMaintenance) ProcessNextAttachment(ctx context.Context) (bool, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return false, ctx.Err()
}

type observedCallingMaintenance struct {
	*controlledWork
	passes atomic.Int32
}

func (w *observedCallingMaintenance) MaintainOutgoingCallLegs(context.Context) (bool, error) {
	w.passes.Add(1)
	return false, nil
}

func TestCallingMaintenanceContinuesWhileAttachmentStorageIsBlocked(t *testing.T) {
	calls := &observedCallingMaintenance{controlledWork: newControlledWork()}
	storage := &slowStorageMaintenance{started: make(chan struct{}, 1)}
	runner, err := New(Config{WorkInterval: 10 * time.Millisecond, WorkTimeout: 5 * time.Second, CredentialInterval: time.Hour, CredentialTimeout: time.Second, HealthInterval: time.Hour, HealthTimeout: time.Second, ReceiptBatchSize: 1, RecoveryAndMessagingBatchSize: 1, ProviderCommandBatchSize: 1, CommandWorkers: 1}, calls, storage, &controlledInteractionWork{}, healthyDependency{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	waitForSignal(t, storage.started, "blocked attachment operation")
	before := calls.passes.Load()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for calls.passes.Load() < before+2 {
		select {
		case <-deadline:
			t.Fatal("storage stalled call deadline maintenance")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestCleanupUsesItsOwnNonurgentCadence(t *testing.T) {
	var delays []time.Duration
	runner := &Runner{
		config:   Config{WorkTimeout: time.Second, ErrorBackoffMin: time.Second, ErrorBackoffMax: 10 * time.Second},
		messages: &controlledMessagingWork{},
		jitter:   func(d time.Duration) time.Duration { return d },
		wait: func(_ context.Context, d time.Duration) bool {
			delays = append(delays, d)
			return len(delays) < 3
		},
	}
	runner.runCleanupLane(context.Background())
	for _, delay := range delays {
		if delay != 5*time.Second {
			t.Fatalf("idle cleanup delay=%s, want 5s", delay)
		}
	}
}

type failingBackgroundCalling struct{ *controlledWork }

func (*failingBackgroundCalling) ProcessNextRecordingReconciliation(context.Context) (bool, error) {
	return false, errors.New("recording provider unavailable")
}

type observedBackgroundMessaging struct {
	controlledMessagingWork
	reconciled bool
}

func (*observedBackgroundMessaging) RecoverInterruptedCommands(context.Context) error {
	return errors.New("command recovery unavailable")
}
func (m *observedBackgroundMessaging) ReconcileNextCommand(context.Context) (bool, error) {
	m.reconciled = true
	return true, nil
}
func TestBackgroundFailuresDoNotStarveIndependentReconciliation(t *testing.T) {
	messages := &observedBackgroundMessaging{}
	runner := &Runner{work: &failingBackgroundCalling{newControlledWork()}, messages: messages}
	processed, err := runner.reconcileBackgroundWork(context.Background())
	if !messages.reconciled || !processed || err == nil {
		t.Fatalf("processed=%v reconciled=%v error=%v", processed, messages.reconciled, err)
	}
}

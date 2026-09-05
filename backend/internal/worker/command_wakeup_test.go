package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestReceiptProgressWakesProviderCommandsWithoutAdvancingPollTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var pendingReceipt, ready atomic.Bool
		var executed atomic.Int32
		work := &commandWakeTestWork{controlledWork: newControlledWork()}
		work.claim = func(context.Context) (func(context.Context) error, bool, error) {
			if !ready.CompareAndSwap(true, false) {
				return nil, false, nil
			}
			return func(context.Context) error { executed.Add(1); return nil }, true, nil
		}
		work.receipt = func(context.Context) (bool, error) {
			if !pendingReceipt.CompareAndSwap(true, false) {
				return false, nil
			}
			ready.Store(true)
			return true, nil
		}
		runner := newCommandWakeTestRunner(t, work)
		go runner.runProviderCommands(ctx)
		synctest.Wait()
		started := time.Now()
		pendingReceipt.Store(true)
		go runner.runCallingReceipts(ctx)
		synctest.Wait()
		if got := executed.Load(); got != 1 {
			t.Fatalf("receipt-created command effects before polling timer advanced=%d, want1", got)
		}
		if !time.Now().Equal(started) {
			t.Fatal("wake required advancing the polling timer")
		}
	})
}

func TestCommandCompletionWakesDependentCommandWithoutAdvancingPollTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		release := make(chan struct{})
		var claims, executed atomic.Int32
		var ready atomic.Bool
		work := &commandWakeTestWork{controlledWork: newControlledWork()}
		work.claim = func(context.Context) (func(context.Context) error, bool, error) {
			if claims.Add(1) == 1 {
				return func(ctx context.Context) error {
					select {
					case <-release:
						ready.Store(true)
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}, true, nil
			}
			if !ready.CompareAndSwap(true, false) {
				return nil, false, nil
			}
			return func(context.Context) error { executed.Add(1); return nil }, true, nil
		}
		runner := newCommandWakeTestRunner(t, work)
		go runner.runProviderCommands(ctx)
		synctest.Wait()
		started := time.Now()
		close(release)
		synctest.Wait()
		if got := executed.Load(); got != 1 {
			t.Fatalf("dependent command effects before polling timer advanced=%d, want1", got)
		}
		if !time.Now().Equal(started) {
			t.Fatal("completion required advancing the polling timer")
		}
	})
}

type commandWakeTestWork struct {
	*controlledWork
	claim   func(context.Context) (func(context.Context) error, bool, error)
	receipt func(context.Context) (bool, error)
}

func (work *commandWakeTestWork) ClaimNextCommand(ctx context.Context) (func(context.Context) error, bool, error) {
	return work.claim(ctx)
}
func (work *commandWakeTestWork) ProcessNextReceipt(ctx context.Context) (bool, error) {
	if work.receipt == nil {
		return false, nil
	}
	return work.receipt(ctx)
}
func newCommandWakeTestRunner(t *testing.T, work *commandWakeTestWork) *Runner {
	t.Helper()
	runner, err := New(Config{
		WorkInterval: 250 * time.Millisecond, WorkTimeout: 10 * time.Second,
		CredentialInterval: time.Hour, CredentialTimeout: time.Second, HealthInterval: time.Hour, HealthTimeout: time.Second,
		MetricInterval: time.Hour, MetricTimeout: time.Second, ReceiptBatchSize: 8, RecoveryAndMessagingBatchSize: 1,
		ProviderCommandBatchSize: 8, CommandWorkers: 10, ErrorBackoffMin: time.Second, ErrorBackoffMax: 4 * time.Second,
	}, work, &controlledMessagingWork{}, &controlledInteractionWork{}, healthyDependency{})
	if err != nil {
		t.Fatal(err)
	}
	runner.jitter = func(delay time.Duration) time.Duration { return delay }
	return runner
}

func TestCommandHintsCoalesceAndDoNotSpinOrReplaceFallbackPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var claims atomic.Int32
		work := &commandWakeTestWork{controlledWork: newControlledWork(), claim: func(context.Context) (func(context.Context) error, bool, error) {
			claims.Add(1)
			return nil, false, nil
		}}
		runner := newCommandWakeTestRunner(t, work)
		for range 100 {
			runner.notifyProviderCommands()
		}
		if len(runner.commandReady) != 1 {
			t.Fatalf("queued hints=%d want1", len(runner.commandReady))
		}
		go runner.runProviderCommands(ctx)
		synctest.Wait()
		if got := claims.Load(); got != 1 {
			t.Fatalf("stale hints caused%d initial scans, want1", got)
		}
		runner.notifyProviderCommands()
		synctest.Wait()
		if got := claims.Load(); got != 2 {
			t.Fatalf("false hint caused%d scans, want2 total", got)
		}
		synctest.Wait()
		if got := claims.Load(); got != 2 {
			t.Fatalf("false hint spun to%d scans", got)
		}
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if got := claims.Load(); got != 3 {
			t.Fatalf("fallback polling scans=%d want3", got)
		}
	})
}

func TestCommandHintDuringEmptyClaimIsNotLostBeforeWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		releaseClaim := make(chan struct{})
		var claims, executed atomic.Int32
		var ready atomic.Bool
		work := &commandWakeTestWork{controlledWork: newControlledWork()}
		work.claim = func(ctx context.Context) (func(context.Context) error, bool, error) {
			if claims.Add(1) == 1 {
				// The query observed no eligible work before a concurrent commit hint.
				select {
				case <-releaseClaim:
					return nil, false, nil
				case <-ctx.Done():
					return nil, false, ctx.Err()
				}
			}
			if !ready.CompareAndSwap(true, false) {
				return nil, false, nil
			}
			return func(context.Context) error { executed.Add(1); return nil }, true, nil
		}
		runner := newCommandWakeTestRunner(t, work)
		go runner.runProviderCommands(ctx)
		synctest.Wait()
		started := time.Now()
		ready.Store(true)
		runner.notifyProviderCommands()
		close(releaseClaim)
		synctest.Wait()
		if executed.Load() != 1 {
			t.Fatal("hint arriving during an empty claim was lost before wait")
		}
		if !time.Now().Equal(started) {
			t.Fatal("claim-race recovery waited for fallback timer")
		}
	})
}

func TestCommandHintDoesNotBypassClaimErrorBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var claims, executed atomic.Int32
		var ready atomic.Bool
		ready.Store(true)
		work := &commandWakeTestWork{controlledWork: newControlledWork()}
		work.claim = func(context.Context) (func(context.Context) error, bool, error) {
			if claims.Add(1) == 1 {
				return nil, false, errors.New("synthetic claim interruption")
			}
			if !ready.CompareAndSwap(true, false) {
				return nil, false, nil
			}
			return func(context.Context) error { executed.Add(1); return nil }, true, nil
		}
		runner := newCommandWakeTestRunner(t, work)
		go runner.runProviderCommands(ctx)
		synctest.Wait()
		runner.notifyProviderCommands()
		synctest.Wait()
		if claims.Load() != 1 {
			t.Fatal("hint bypassed claim error backoff")
		}
		time.Sleep(time.Second - time.Nanosecond)
		synctest.Wait()
		if claims.Load() != 1 {
			t.Fatal("claim retried before backoff deadline")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if executed.Load() != 1 {
			t.Fatal("claim did not resume after its full backoff")
		}
	})
}

func TestCommandHintDoesNotBypassExecutorErrorBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var claims, executed atomic.Int32
		var ready atomic.Bool
		ready.Store(true)
		work := &commandWakeTestWork{controlledWork: newControlledWork()}
		work.claim = func(context.Context) (func(context.Context) error, bool, error) {
			if claims.Add(1) == 1 {
				return func(context.Context) error { return errors.New("synthetic effect interruption") }, true, nil
			}
			if !ready.CompareAndSwap(true, false) {
				return nil, false, nil
			}
			return func(context.Context) error { executed.Add(1); return nil }, true, nil
		}
		runner := newCommandWakeTestRunner(t, work)
		runner.config.CommandWorkers = 1
		go runner.runProviderCommands(ctx)
		synctest.Wait()
		runner.notifyProviderCommands()
		synctest.Wait()
		if claims.Load() != 1 {
			t.Fatal("hint reclaimed an executor during its error backoff")
		}
		time.Sleep(time.Second - time.Nanosecond)
		synctest.Wait()
		if claims.Load() != 1 {
			t.Fatal("executor capacity returned before backoff deadline")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if executed.Load() != 1 {
			t.Fatal("executor did not resume after its full backoff")
		}
	})
}

func TestFailedOrEmptyReceiptsAndFailedCommandsDoNotPublishHints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		work := &commandWakeTestWork{controlledWork: newControlledWork()}
		runner := newCommandWakeTestRunner(t, work)
		work.receipt = func(context.Context) (bool, error) { return true, errors.New("synthetic receipt interruption") }
		if _, err := runner.processNextCallingReceipt(ctx); err == nil {
			t.Fatal("receipt error disappeared")
		}
		work.receipt = func(context.Context) (bool, error) { return false, nil }
		if _, err := runner.processNextCallingReceipt(ctx); err != nil {
			t.Fatal(err)
		}
		commands := make(chan func(context.Context) error, 1)
		commands <- func(context.Context) error { return errors.New("synthetic command interruption") }
		close(commands)
		available := make(chan struct{}, 1)
		go runner.runProviderCommandExecutor(ctx, commands, available)
		synctest.Wait()
		if len(runner.commandReady) != 0 {
			t.Fatal("unsuccessful work published a wake hint")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if len(runner.commandReady) != 0 {
			t.Fatal("failed command published a hint after its backoff completed")
		}
	})
}

func TestPendingCommandHintDoesNotStartWorkAfterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var claims atomic.Int32
		work := &commandWakeTestWork{controlledWork: newControlledWork(), claim: func(context.Context) (func(context.Context) error, bool, error) {
			claims.Add(1)
			return nil, false, nil
		}}
		runner := newCommandWakeTestRunner(t, work)
		runner.notifyProviderCommands()
		cancel()
		runner.runProviderCommands(ctx)
		if claims.Load() != 0 {
			t.Fatal("pending hint started work after cancellation")
		}
	})
}

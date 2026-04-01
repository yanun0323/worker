package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPushBeforeRunReturnsErrNotRunning(t *testing.T) {
	t.Parallel()

	pool := New()

	if err := pool.Push(func() {}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Push() error = %v, want %v", err, ErrNotRunning)
	}
}

func TestTryPushReturnsErrQueueFull(t *testing.T) {
	t.Parallel()

	pool := New(PoolOption{
		WorkerCount: 1,
		JobCap:      1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pool.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})

	if err := pool.Push(func() {
		close(started)
		<-release
	}); err != nil {
		t.Fatalf("Push() blocking job error = %v", err)
	}

	waitForSignal(t, started, time.Second, "first job start")

	if err := pool.TryPush(func() {}); err != nil {
		t.Fatalf("TryPush() queued job error = %v", err)
	}

	if err := pool.TryPush(func() {}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("TryPush() error = %v, want %v", err, ErrQueueFull)
	}

	close(release)

	if err := pool.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestStopDrainsQueuedJobsAndRejectsNewOnes(t *testing.T) {
	t.Parallel()

	pool := New(PoolOption{
		WorkerCount: 1,
		JobCap:      1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pool.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondDone := make(chan struct{})

	if err := pool.Push(func() {
		close(firstStarted)
		<-firstRelease
	}); err != nil {
		t.Fatalf("Push() first job error = %v", err)
	}

	waitForSignal(t, firstStarted, time.Second, "first job start")

	if err := pool.TryPush(func() {
		close(secondDone)
	}); err != nil {
		t.Fatalf("TryPush() second job error = %v", err)
	}

	stopErr := make(chan error, 1)
	go func() {
		stopErr <- pool.Stop(context.Background())
	}()

	waitForCondition(t, time.Second, func() bool {
		err := pool.TryPush(func() {})
		return errors.Is(err, ErrProcessingStopping)
	}, "pool enters stopping state")

	close(firstRelease)

	waitForSignal(t, secondDone, time.Second, "second job completion")

	if err := waitForError(stopErr, time.Second); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestPanicJobDoesNotBreakPool(t *testing.T) {
	t.Parallel()

	pool := New(PoolOption{
		WorkerCount: 1,
		JobCap:      2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pool.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	completed := make(chan struct{})

	if err := pool.Push(func() {
		panic("boom")
	}); err != nil {
		t.Fatalf("Push() panic job error = %v", err)
	}

	if err := pool.Push(func() {
		close(completed)
	}); err != nil {
		t.Fatalf("Push() follow-up job error = %v", err)
	}

	if err := pool.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	waitForSignal(t, completed, time.Second, "follow-up job completion")
}

func TestStopDeadlineExceededKeepsStoppingUntilWorkersExit(t *testing.T) {
	t.Parallel()

	pool := New(PoolOption{
		WorkerCount: 1,
		JobCap:      1,
	})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	if err := pool.Run(runCtx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})

	if err := pool.Push(func() {
		close(started)
		<-release
	}); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	waitForSignal(t, started, time.Second, "blocking job start")

	stopCtx, cancelStop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelStop()

	if err := pool.Stop(stopCtx); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want %v", err, ErrDeadlineExceeded)
	}

	if err := pool.TryPush(func() {}); !errors.Is(err, ErrProcessingStopping) {
		t.Fatalf("TryPush() after timeout error = %v, want %v", err, ErrProcessingStopping)
	}

	close(release)

	waitForCondition(t, time.Second, func() bool {
		err := pool.Run(context.Background())
		if err == nil {
			return true
		}
		if errors.Is(err, ErrProcessingStopping) {
			return false
		}

		t.Fatalf("Run() error after stop timeout = %v", err)
		return false
	}, "pool becomes runnable again")

	if err := pool.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() after rerun error = %v", err)
	}
}

func TestStopDrainsAllQueuedJobs(t *testing.T) {
	t.Parallel()

	pool := New(PoolOption{
		WorkerCount: 2,
		JobCap:      4,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pool.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var completed atomic.Int64
	for range 4 {
		if err := pool.Push(func() {
			completed.Add(1)
		}); err != nil {
			t.Fatalf("Push() error = %v", err)
		}
	}

	if err := pool.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if got := completed.Load(); got != 4 {
		t.Fatalf("completed jobs = %d, want 4", got)
	}
}

func waitForSignal(t *testing.T, ch <-chan struct{}, timeout time.Duration, label string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool, label string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", label)
}

func waitForError(ch <-chan error, timeout time.Duration) error {
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		return ErrDeadlineExceeded
	}
}

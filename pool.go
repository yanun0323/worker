package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrProcessingStopping = errors.New("worker pool is still processing stopping")
	ErrNotRunning         = errors.New("worker pool not running")
	ErrDeadlineExceeded   = context.DeadlineExceeded
	ErrQueueFull          = errors.New("job queue of worker pool is full")
)

var (
	defaultOption = PoolOption{
		WorkerCount: 8,
		JobCap:      1 << 10,
	}
)

type Job func()

type PoolOption struct {
	WorkerCount uint64
	JobCap      uint64
}

type Pool struct {
	stopping    atomic.Bool
	cancel      atomic.Pointer[context.CancelFunc]
	waitGroup   sync.WaitGroup
	workerCount uint64
	jobQueue    chan Job
}

func New(opt ...PoolOption) *Pool {
	option := defaultOption
	if len(opt) != 0 {
		option = opt[0]
	}

	return &Pool{
		workerCount: option.WorkerCount,
		jobQueue:    make(chan Job, option.JobCap),
	}
}

func (wp *Pool) Run(ctx context.Context) error {
	if wp.stopping.Load() {
		return ErrProcessingStopping
	}

	workerCtx, workerCancel := context.WithCancel(ctx)
	if !wp.cancel.CompareAndSwap(nil, &workerCancel) {
		workerCancel()
		return nil
	}

	wp.waitGroup.Go(func() {
		<-workerCtx.Done()
		_ = wp.cancel.CompareAndSwap(&workerCancel, nil)
	})

	for range wp.workerCount {
		wp.waitGroup.Go(func() {
			for {
				select {
				case job := <-wp.jobQueue:
					job()
				case <-workerCtx.Done():
					return
				}
			}
		})
	}

	return nil
}

func (wp *Pool) Stop(ctx context.Context) error {
	if wp.isProcessingStopping() {
		return ErrProcessingStopping
	}
	defer wp.stopping.Store(false)

	cancel := wp.cancel.Swap(nil)
	if cancel == nil {
		return nil
	}
	(*cancel)()

	wpDone := make(chan struct{})

	go func() {
		wp.waitGroup.Wait()
		wpDone <- struct{}{}
	}()

	select {
	case <-wpDone:
		return nil
	case <-ctx.Done():
		return ErrDeadlineExceeded
	}
}

func (wp *Pool) isProcessingStopping() bool {
	return wp.stopping.Load()
}

func (wp *Pool) notRunning() bool {
	return wp.cancel.Load() == nil
}

func (wp *Pool) Push(job Job) error {
	if wp.isProcessingStopping() {
		return ErrProcessingStopping
	}

	if wp.notRunning() {
		return ErrNotRunning
	}

	wp.jobQueue <- job
	return nil
}

func (wp *Pool) TryPush(job Job) error {
	if wp.isProcessingStopping() {
		return ErrProcessingStopping
	}

	if wp.notRunning() {
		return ErrNotRunning
	}

	select {
	case wp.jobQueue <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

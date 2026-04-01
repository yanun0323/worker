package worker

import (
	"context"
	"errors"
	"sync"
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

type poolState uint8

const (
	stateStopped poolState = iota
	stateRunning
	stateStopping
)

type poolSession struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	workers sync.WaitGroup
}

type Pool struct {
	mu          sync.Mutex
	state       poolState
	session     *poolSession
	queue       []Job
	head        int
	size        int
	workerCount int
	notEmpty    *sync.Cond
	notFull     *sync.Cond
}

func New(opt ...PoolOption) *Pool {
	option := defaultOption
	if len(opt) != 0 {
		option = opt[0]
	}

	workerCount := normalizeOption(option.WorkerCount, defaultOption.WorkerCount)
	jobCap := normalizeOption(option.JobCap, defaultOption.JobCap)

	wp := &Pool{
		state:       stateStopped,
		queue:       make([]Job, jobCap),
		workerCount: workerCount,
	}
	wp.notEmpty = sync.NewCond(&wp.mu)
	wp.notFull = sync.NewCond(&wp.mu)

	return wp
}

func (wp *Pool) Run(ctx context.Context) error {
	wp.mu.Lock()
	switch wp.state {
	case stateRunning:
		wp.mu.Unlock()
		return nil
	case stateStopping:
		wp.mu.Unlock()
		return ErrProcessingStopping
	}

	workerCtx, workerCancel := context.WithCancel(ctx)
	session := &poolSession{
		ctx:    workerCtx,
		cancel: workerCancel,
		done:   make(chan struct{}),
	}
	session.workers.Add(wp.workerCount)

	wp.session = session
	wp.state = stateRunning
	workerCount := wp.workerCount
	wp.mu.Unlock()

	for range workerCount {
		go wp.worker(session)
	}
	go wp.awaitSessionStop(session)

	return nil
}

func (wp *Pool) Stop(ctx context.Context) error {
	done := wp.beginStop()
	if done == nil {
		return nil
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ErrDeadlineExceeded
	}
}

func (wp *Pool) Push(job Job) error {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	for {
		switch wp.state {
		case stateStopped:
			return ErrNotRunning
		case stateStopping:
			return ErrProcessingStopping
		case stateRunning:
			if wp.size < len(wp.queue) {
				wp.enqueue(job)
				wp.notEmpty.Signal()
				return nil
			}
			wp.notFull.Wait()
		}
	}
}

func (wp *Pool) TryPush(job Job) error {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	switch wp.state {
	case stateStopped:
		return ErrNotRunning
	case stateStopping:
		return ErrProcessingStopping
	case stateRunning:
		if wp.size >= len(wp.queue) {
			return ErrQueueFull
		}
		wp.enqueue(job)
		wp.notEmpty.Signal()
		return nil
	default:
		return ErrNotRunning
	}
}

func (wp *Pool) beginStop() chan struct{} {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	switch wp.state {
	case stateStopped:
		return nil
	case stateStopping:
		if wp.session == nil {
			return nil
		}
		return wp.session.done
	case stateRunning:
		session := wp.session
		if session == nil {
			wp.state = stateStopped
			return nil
		}

		wp.state = stateStopping
		wp.notEmpty.Broadcast()
		wp.notFull.Broadcast()
		session.cancel()

		return session.done
	default:
		return nil
	}
}

func (wp *Pool) awaitSessionStop(session *poolSession) {
	<-session.ctx.Done()

	wp.mu.Lock()
	if wp.session == session && wp.state == stateRunning {
		wp.state = stateStopping
		wp.notEmpty.Broadcast()
		wp.notFull.Broadcast()
	}
	wp.mu.Unlock()

	session.workers.Wait()

	wp.mu.Lock()
	if wp.session == session {
		wp.session = nil
		wp.state = stateStopped
		wp.head = 0
		wp.size = 0
		wp.notEmpty.Broadcast()
		wp.notFull.Broadcast()
	}
	wp.mu.Unlock()

	close(session.done)
}

func (wp *Pool) worker(session *poolSession) {
	defer session.workers.Done()

	for {
		job, ok := wp.nextJob(session)
		if !ok {
			return
		}
		runJob(job)
	}
}

func (wp *Pool) nextJob(session *poolSession) (Job, bool) {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	for {
		if wp.session != session {
			return nil, false
		}

		if wp.size > 0 {
			job := wp.queue[wp.head]
			wp.queue[wp.head] = nil
			wp.head++
			if wp.head == len(wp.queue) {
				wp.head = 0
			}
			wp.size--
			wp.notFull.Signal()
			return job, true
		}

		if wp.state != stateRunning {
			return nil, false
		}

		wp.notEmpty.Wait()
	}
}

func (wp *Pool) enqueue(job Job) {
	index := (wp.head + wp.size) % len(wp.queue)
	wp.queue[index] = job
	wp.size++
}

func normalizeOption(value uint64, fallback uint64) int {
	const maxInt = int(^uint(0) >> 1)

	if value == 0 {
		value = fallback
	}
	if value > uint64(maxInt) {
		return maxInt
	}

	return int(value)
}

func runJob(job Job) {
	if job == nil {
		return
	}

	defer func() {
		_ = recover()
	}()

	job()
}

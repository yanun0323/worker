# worker

`worker` is a small Go worker pool with a bounded queue, graceful shutdown, and panic-safe job execution.

## Features

- Bounded in-memory queue
- Configurable worker count and queue capacity
- Blocking `Push` and non-blocking `TryPush`
- Graceful `Stop` that drains queued jobs before exit
- Panic recovery per job so one bad task does not break the pool
- Context-aware startup and shutdown

## Installation

```bash
go get github.com/yanun0323/worker
```

## Quick Start

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/yanun0323/worker"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := worker.New(worker.PoolOption{
		WorkerCount: 4,
		JobCap:      16,
	})

	if err := pool.Run(ctx); err != nil {
		log.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		index := i
		if err := pool.Push(func() {
			log.Printf("job %d", index)
			time.Sleep(50 * time.Millisecond)
		}); err != nil {
			log.Fatal(err)
		}
	}

	if err := pool.Stop(context.Background()); err != nil {
		log.Fatal(err)
	}
}
```

## API

### `worker.New(opt ...worker.PoolOption) *worker.Pool`

Creates a pool with optional configuration.

Default values:

- `WorkerCount`: `8`
- `JobCap`: `1024`

If either option is `0`, the default value is used.

### `(*Pool).Run(ctx context.Context) error`

Starts the pool. Calling `Run` on an already running pool is a no-op.

If the pool is already stopping, `Run` returns `worker.ErrProcessingStopping`.

### `(*Pool).Push(job worker.Job) error`

Adds a job to the queue.

- Returns `worker.ErrNotRunning` if the pool has not been started.
- Returns `worker.ErrProcessingStopping` if shutdown has started.
- Blocks when the queue is full until capacity is available or the pool stops.

### `(*Pool).TryPush(job worker.Job) error`

Adds a job without blocking.

- Returns `worker.ErrNotRunning` if the pool has not been started.
- Returns `worker.ErrProcessingStopping` if shutdown has started.
- Returns `worker.ErrQueueFull` if the queue is full.

### `(*Pool).Stop(ctx context.Context) error`

Stops the pool and waits for workers to finish queued jobs.

- Returns `nil` after all queued jobs have been drained.
- Returns `worker.ErrDeadlineExceeded` if `ctx` expires first.
- If the stop deadline is exceeded, the pool continues stopping in the background and rejects new jobs until shutdown completes.

## Behavior Notes

- A job panic is recovered internally and does not stop the worker pool.
- When the run context is canceled, the pool enters shutdown and drains the remaining queued jobs.
- `nil` jobs are ignored.

## Example

An executable example is available in [example/main.go](./example/main.go):

```bash
go run ./example
```

## License

MIT. See [LICENSE](./LICENSE).

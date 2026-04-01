package worker

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

var benchmarkNoopJob Job = func() {}

func BenchmarkPoolPush(b *testing.B) {
	pool := New(PoolOption{
		WorkerCount: 8,
		JobCap:      1024,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pool.Run(ctx); err != nil {
		b.Fatalf("Run() error = %v", err)
	}
	defer func() {
		if err := pool.Stop(context.Background()); err != nil {
			b.Fatalf("Stop() error = %v", err)
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		if err := pool.Push(benchmarkNoopJob); err != nil {
			b.Fatalf("Push() error = %v", err)
		}
	}
}

func BenchmarkPoolTryPush(b *testing.B) {
	pool := New(PoolOption{
		WorkerCount: 8,
		JobCap:      1024,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pool.Run(ctx); err != nil {
		b.Fatalf("Run() error = %v", err)
	}
	defer func() {
		if err := pool.Stop(context.Background()); err != nil {
			b.Fatalf("Stop() error = %v", err)
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		for {
			err := pool.TryPush(benchmarkNoopJob)
			if err == nil {
				break
			}
			if !errors.Is(err, ErrQueueFull) {
				b.Fatalf("TryPush() error = %v", err)
			}

			runtime.Gosched()
		}
	}
}

func BenchmarkPoolRunStop(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		pool := New(PoolOption{
			WorkerCount: 1,
			JobCap:      1,
		})

		ctx, cancel := context.WithCancel(context.Background())
		if err := pool.Run(ctx); err != nil {
			cancel()
			b.Fatalf("Run() error = %v", err)
		}
		cancel()

		if err := pool.Stop(context.Background()); err != nil {
			b.Fatalf("Stop() error = %v", err)
		}
	}
}

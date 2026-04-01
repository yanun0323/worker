package main

import (
	"context"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yanun0323/worker"
)

var printNow = func() {
	now := time.Now()
	time.Sleep(time.Millisecond * time.Duration(rand.Int63n(1000)))
	println(now.Format(time.RFC3339Nano))
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Print("launching server...")

	w := worker.New(worker.PoolOption{
		WorkerCount: 3,
		JobCap:      10,
	})
	do(w.Run(ctx))
	for range 100 {
		do(w.Push(printNow))
	}
}

func do(err error) {
	if err != nil {
		log.Fatalf("%+v", err)
	}
}

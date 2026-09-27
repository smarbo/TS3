package main

import (
	"context"
	"testing"
	"time"

	"ts3/internal/book"
	"ts3/internal/watchdog"
)

func TestTimedWatchdogWithdrawsStalledGate(t *testing.T) {
	start := time.Now()
	var gate watchdog.Gate
	gate.Progress(0)
	gate.Applied(book.Healthy, true, 0)
	if !gate.Open() {
		t.Fatal("fixture gate did not open")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trip := make(chan time.Duration, 1)
	go watchGate(ctx, &gate, start, func(_, _ time.Duration) { trip <- time.Since(start) })
	select {
	case elapsed := <-trip:
		if elapsed <= 2*time.Second || elapsed > 2750*time.Millisecond {
			t.Fatalf("watchdog trip outside deadline: %v", elapsed)
		}
		if gate.Open() || !gate.Tripped() {
			t.Fatal("stalled gate remained available")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog did not close stalled gate")
	}
}

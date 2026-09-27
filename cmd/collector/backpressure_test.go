package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ts3/internal/domain"
)

func TestBoundedIngressHandoffAndAcknowledgment(t *testing.T) {
	o := domain.Observation{SourceID: "fixture", Kind: domain.ClockTick}
	full := make(chan queued, 1)
	full <- queued{}
	start := time.Now()
	err := enqueue(context.Background(), full, o, false, 30*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "handoff") || time.Since(start) > 250*time.Millisecond {
		t.Fatalf("full queue did not fail within bound: %v, %v", err, time.Since(start))
	}
	if len(full) != 1 {
		t.Fatal("timed-out observation entered queue")
	}
	noCommit := make(chan queued, 1)
	start = time.Now()
	err = enqueue(context.Background(), noCommit, o, true, 30*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "acknowledgment") || time.Since(start) > 250*time.Millisecond {
		t.Fatalf("missing acknowledgment did not fail within bound: %v, %v", err, time.Since(start))
	}
	if len(noCommit) != 1 || (<-noCommit).ack == nil {
		t.Fatal("accepted observation lost its acknowledgment channel")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := enqueue(ctx, make(chan queued), o, false, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	committed := make(chan queued, 1)
	go func() { q := <-committed; q.ack <- nil }()
	if err := enqueue(context.Background(), committed, o, true, time.Second); err != nil {
		t.Fatal(err)
	}
}

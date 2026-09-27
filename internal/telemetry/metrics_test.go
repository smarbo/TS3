package telemetry

import (
	"testing"
	"time"

	"ts3/internal/book"
)

func TestHistogramQuantilesAndNegativeSamples(t *testing.T) {
	var h Histogram
	for _, v := range []int64{-1, 0, 100000, 200000, 300000, 400000, 500000, 600000, 700000, 800000, 900000} {
		h.Add(v)
	}
	s := h.Summary()
	if s.Samples != 10 || s.NegativeSamples != 1 || s.P50NS != 400000 || s.P99NS != 900000 || s.MaxNS != 900000 {
		t.Fatal(s)
	}
}

func TestSnapshotsDoNotAccumulateOpenHealthInterval(t *testing.T) {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	c := New("run", start)
	c.Health(book.View{Health: book.Healthy}, 0)
	c.ConfirmHealthy(start.Add(time.Second))
	first := c.Snapshot(start.Add(5*time.Second), 5*time.Second)
	second := c.Snapshot(start.Add(10*time.Second), 10*time.Second)
	if first.HealthDurationNS[book.Healthy] != int64(5*time.Second) || second.HealthDurationNS[book.Healthy] != int64(10*time.Second) {
		t.Fatalf("open health interval counted twice: first=%d second=%d", first.HealthDurationNS[book.Healthy], second.HealthDurationNS[book.Healthy])
	}
	if c.LiveState().LatestConfirmedHealthyAt.IsZero() {
		t.Fatal("missing durable healthy confirmation")
	}
}

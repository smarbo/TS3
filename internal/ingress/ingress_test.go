package ingress

import (
	"errors"
	"testing"
	"time"

	"ts3/internal/domain"
)

func TestOvertakenObservationAndEpoch(t *testing.T) {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	c := New("r", start)
	accept := func(k domain.RawKind, epoch uint64, received, admitted time.Time, elapsed time.Duration) domain.RawRecord {
		r, err := c.Accept(domain.Observation{SourceID: "fixture", Epoch: epoch, Kind: k, ReceiveTime: received}, admitted, elapsed)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	accept(domain.Connected, 1, start, start, 0)
	tick := accept(domain.ClockTick, 1, start.Add(time.Second), start.Add(time.Second), time.Second)
	frame := accept(domain.WSFrame, 1, start.Add(900*time.Millisecond), start.Add(1100*time.Millisecond), 1100*time.Millisecond)
	if tick.Ordinal >= frame.Ordinal || !frame.ReceiveTime.Before(tick.ReceiveTime) || frame.UsableFromTime.Before(tick.UsableFromTime) {
		t.Fatal("backdated frame")
	}
	accept(domain.Disconnected, 1, start.Add(2*time.Second), start.Add(2*time.Second), 2*time.Second)
	accept(domain.Connected, 2, start.Add(2100*time.Millisecond), start.Add(2100*time.Millisecond), 2100*time.Millisecond)
	if _, err := c.Accept(domain.Observation{SourceID: "fixture", Epoch: 1, Kind: domain.WSFrame, ReceiveTime: start.Add(2200 * time.Millisecond)}, start.Add(2200*time.Millisecond), 2200*time.Millisecond); err == nil {
		t.Fatal("old epoch accepted")
	}
}

func TestForwardJumpBreaksRun(t *testing.T) {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	c := New("r", start)
	anomaly, err := c.Accept(domain.Observation{SourceID: "kraken-clock", Kind: domain.ClockTick, ReceiveTime: start.Add(time.Hour)}, start.Add(time.Hour), time.Second)
	if !errors.Is(err, ErrClockBreak) {
		t.Fatal(err)
	}
	if anomaly.Kind != domain.ClockAnomaly || anomaly.SourceID != "kraken-clock" || anomaly.Ordinal != 1 {
		t.Fatal(anomaly)
	}
	_, err = c.Accept(domain.Observation{SourceID: "tick", Kind: domain.ClockTick}, start.Add(2*time.Second), 2*time.Second)
	if !errors.Is(err, ErrClockBreak) {
		t.Fatal("run recovered after jump")
	}
}

func TestSmallWallCorrectionPreservesAdmissionAndMonotoneUsable(t *testing.T) {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	c := New("r", start)
	one, err := c.Accept(domain.Observation{SourceID: "tick", Kind: domain.ClockTick, ReceiveTime: start.Add(time.Second)}, start.Add(time.Second), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	actualAdmission := start.Add(900 * time.Millisecond)
	two, err := c.Accept(domain.Observation{SourceID: "tick", Kind: domain.ClockTick, ReceiveTime: actualAdmission}, actualAdmission, 1100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !two.AdmissionTime.Equal(actualAdmission) || !two.UsableFromTime.Equal(one.UsableFromTime) || two.Ordinal != one.Ordinal+1 {
		t.Fatalf("first=%+v second=%+v", one, two)
	}
}

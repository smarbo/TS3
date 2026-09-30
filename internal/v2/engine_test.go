package v2

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
	"ts3/internal/v1"
)

func tick(at time.Time, ordinal, generation uint64, mid float64, bidSize, askSize string) (book.View, book.Quote) {
	bid := fmt.Sprintf("%.2f", mid-0.01)
	ask := fmt.Sprintf("%.2f", mid+0.01)
	v := book.View{Ordinal: ordinal, AsOf: at, Epoch: generation, Generation: generation,
		Health: book.Healthy, BestBid: bid, BestAsk: ask, LastBookOrdinal: ordinal}
	q := book.Quote{Epoch: generation, Generation: generation, LastBookOrdinal: ordinal, LastBookAt: at}
	for n := 0; n < 5; n++ {
		q.Bids = append(q.Bids, domain.Level{Price: fmt.Sprintf("%.2f", mid-0.01-float64(n)*0.01), Size: bidSize})
		q.Asks = append(q.Asks, domain.Level{Price: fmt.Sprintf("%.2f", mid+0.01+float64(n)*0.01), Size: askSize})
	}
	return v, q
}

func TestFrozenConfigAndTopFivePressure(t *testing.T) {
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.PressureThresholdPPM++
	if c.Validate() == nil {
		t.Fatal("unregistered parameter variant accepted")
	}
	_, q := tick(time.Unix(0, 0), 1, 1, 100, "2", "1")
	got, err := topFivePressure(q)
	if err != nil || got != 333333 {
		t.Fatalf("pressure = %d, %v", got, err)
	}
	q.Asks = q.Asks[:4]
	if _, err := topFivePressure(q); err == nil {
		t.Fatal("missing fifth level accepted")
	}
}

func signal(status Status, direction v1.Action) SignalResult {
	return SignalResult{Status: status, Direction: direction}
}

func TestAggregateSeparatesPriceAndBookMechanisms(t *testing.T) {
	tests := []struct {
		name     string
		in       [3]SignalResult
		reason   string
		vote     v1.Action
		conflict string
	}{
		{"three agree still two slots", [3]SignalResult{signal(Active, v1.Long), signal(Active, v1.Long), signal(Active, v1.Long)}, "CONSENSUS", v1.Long, "0"},
		{"price internal conflict", [3]SignalResult{signal(Active, v1.Long), signal(Active, v1.Short), signal(Active, v1.Long)}, "SIGNAL_DISAGREEMENT", v1.Flat, "1"},
		{"book opposes price", [3]SignalResult{signal(Active, v1.Long), signal(Neutral, v1.Flat), signal(Active, v1.Short)}, "SIGNAL_DISAGREEMENT", v1.Flat, "1"},
		{"only price", [3]SignalResult{signal(Active, v1.Long), signal(Neutral, v1.Flat), signal(Unavailable, v1.Flat)}, "INSUFFICIENT_SIGNAL_DIVERSITY", v1.Flat, "UNDEFINED"},
		{"all neutral", [3]SignalResult{signal(Neutral, v1.Flat), signal(Neutral, v1.Flat), signal(Neutral, v1.Flat)}, "NO_SUPPORTED_DIRECTION", v1.Flat, "UNDEFINED"},
		{"invalid fails closed", [3]SignalResult{signal(Active, v1.Long), signal(Neutral, v1.Flat), signal(Invalid, v1.Flat)}, "SIGNAL_INVALID", v1.Flat, "UNDEFINED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregate(tc.in)
			if got.Reason != tc.reason || got.Direction != tc.vote || got.Disagreement != tc.conflict {
				t.Fatalf("aggregate: %+v", got)
			}
		})
	}
}

func TestPressureNeedsDistinctBookUpdatesAndResets(t *testing.T) {
	e, err := NewEngine("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for n := 0; n <= 25; n++ {
		v, q := tick(start.Add(time.Duration(n)*time.Second), uint64(n+1), 1, 100, "2", "1")
		if err := e.samplePressure(v, q); err != nil {
			t.Fatal(err)
		}
	}
	v, q := tick(start.Add(26*time.Second), 26, 1, 100, "2", "1")
	if err := e.samplePressure(v, q); err != nil {
		t.Fatal(err)
	}
	if len(e.pressure) != 26 { // unchanged LastBookOrdinal is not a new observation
		t.Fatalf("duplicate book counted: %d", len(e.pressure))
	}
	if got := e.pressureSignal(v, q, nil); got.Status != Active || got.Direction != v1.Long {
		t.Fatalf("mature pressure: %+v", got)
	}
	v, q = tick(start.Add(27*time.Second), 28, 1, 100, "2", "1")
	q.LastBookAt = start.Add(24 * time.Second)
	if err := e.samplePressure(v, q); err != nil || len(e.pressure) != 0 {
		t.Fatalf("stale quote retained pressure: %d %v", len(e.pressure), err)
	}
	v, q = tick(start.Add(28*time.Second), 29, 2, 100, "2", "1")
	if err := e.samplePressure(v, q); err != nil || len(e.pressure) != 1 {
		t.Fatalf("new generation did not restart: %d %v", len(e.pressure), err)
	}
	v, q = tick(start.Add(28*time.Second), 30, 2, 100, "2", "1")
	if err := e.samplePressure(v, q); err != nil || len(e.pressure) != 1 {
		t.Fatalf("equal-time tick added duplicate sample: %d %v", len(e.pressure), err)
	}
	v, q = tick(start.Add(31*time.Second), 31, 2, 100, "2", "1")
	if err := e.samplePressure(v, q); err != nil || len(e.pressure) != 1 {
		t.Fatalf("large tick gap retained pressure: %d %v", len(e.pressure), err)
	}
}

func TestEngineConsensusCostAndFuturePoison(t *testing.T) {
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	engines := make([]*Engine, 2)
	for n := range engines {
		var err error
		engines[n], err = NewEngine("r", DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
	}
	var at960 *Intent
	for n := 0; n <= 960; n++ {
		mid := 100.0
		if n >= 780 {
			mid += float64(n-780) * 0.012
		}
		v, q := tick(start.Add(time.Duration(n)*time.Second), uint64(n+1), 1, mid, "2", "1")
		for index, e := range engines {
			i, err := e.ApplyTick(v, q)
			if err != nil {
				t.Fatal(err)
			}
			if n == 960 && index == 0 {
				at960 = i
			}
		}
	}
	if at960 == nil || at960.Action != v1.Long || at960.Evidence.Reason != "CONSENSUS" ||
		at960.Signals[0].Status != Active || at960.Signals[1].Reason != "TREND_SUPPRESSED" ||
		at960.Signals[2].Status != Active || !at960.Opportunity.PassesScreen {
		t.Fatalf("V2 consensus: %+v", at960)
	}
	before, err := domain.CanonicalJSON(at960)
	if err != nil {
		t.Fatal(err)
	}
	// A later, extreme quote cannot revise an already emitted decision.
	v, q := tick(start.Add(961*time.Second), 962, 1, 10_000, "1", "2")
	if _, err := engines[0].ApplyTick(v, q); err != nil {
		t.Fatal(err)
	}
	after, err := domain.CanonicalJSON(at960)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("future quote revised a past intent")
	}
	if got := engines[0].configSHA; got == "" || got != engines[1].configSHA {
		t.Fatal("configuration digest unstable")
	}
}

func TestUnhealthyMinuteNeverActs(t *testing.T) {
	e, err := NewEngine("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	v, q := tick(at, 1, 1, 100, "2", "1")
	v.Health = book.Unhealthy
	i, err := e.ApplyTick(v, q)
	if err != nil || i == nil || i.Action != v1.NoTrade || i.ReasonCodes[0] != "DATA_UNHEALTHY" {
		t.Fatalf("unhealthy action: %+v %v", i, err)
	}
}

func TestWideRegimeMakesAllSignalResultsUnavailable(t *testing.T) {
	e, err := NewEngine("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	var at960 *Intent
	for n := 0; n <= 960; n++ {
		v, q := tick(start.Add(time.Duration(n)*time.Second), uint64(n+1), 1, 100, "2", "1")
		if n == 960 {
			v.BestBid, v.BestAsk = "99.90", "100.10"
			for level := 0; level < 5; level++ {
				q.Bids[level].Price = fmt.Sprintf("%.2f", 99.90-float64(level)*0.01)
				q.Asks[level].Price = fmt.Sprintf("%.2f", 100.10+float64(level)*0.01)
			}
		}
		i, err := e.ApplyTick(v, q)
		if err != nil {
			t.Fatal(err)
		}
		if n == 960 {
			at960 = i
		}
	}
	if at960 == nil || at960.Baseline.Regime != "WIDE" || at960.Action != v1.NoTrade ||
		at960.ReasonCodes[0] != "REGIME_CONFLICT" || at960.Evidence.UnavailableFamily != 3 {
		t.Fatalf("wide decision: %+v", at960)
	}
	for _, s := range at960.Signals {
		if s.Status != Unavailable || s.Direction != v1.Flat || s.Reason != "WIDE_REGIME" ||
			s.SourceBookOrdinal != at960.Baseline.QuoteAsOfOrdinal {
			t.Fatalf("wide family was not suppressed: %+v", s)
		}
	}
}

func BenchmarkApplyTick(b *testing.B) {
	e, err := NewEngine("bench", DefaultConfig())
	if err != nil {
		b.Fatal(err)
	}
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for n := 0; n < 901; n++ {
		v, q := tick(start.Add(time.Duration(n)*time.Second), uint64(n+1), 1, 100, "2", "1")
		if _, err := e.ApplyTick(v, q); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		v, q := tick(start.Add(time.Duration(n+901)*time.Second), uint64(n+902), 1, 100, "2", "1")
		if _, err := e.ApplyTick(v, q); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTopFivePressure(b *testing.B) {
	_, q := tick(time.Unix(0, 0), 1, 1, 100, "2", "1")
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		if _, err := topFivePressure(q); err != nil {
			b.Fatal(err)
		}
	}
}

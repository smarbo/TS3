package v1

import (
	"bytes"
	"fmt"
	"math"
	"testing"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
)

func fixture(t time.Time, ordinal uint64, bid, ask string) (book.View, book.Quote) {
	v := book.View{Ordinal: ordinal, AsOf: t, Epoch: 1, Generation: 1, Health: book.Healthy,
		BestBid: bid, BestAsk: ask, LastBookOrdinal: ordinal - 1}
	q := book.Quote{Epoch: 1, Generation: 1, LastBookOrdinal: ordinal - 1, LastBookAt: t,
		Bids: []domain.Level{{Price: bid, Size: "10"}}, Asks: []domain.Level{{Price: ask, Size: "10"}}}
	return v, q
}

func TestAvailableTimeWindowsWithIrregularTicks(t *testing.T) {
	e, err := NewEngine("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	var final *Intent
	for n := 0; n <= 600; n++ {
		seconds := 1.5 * float64(n)
		mid := 100 + seconds/600
		at := start.Add(time.Duration(seconds * float64(time.Second)))
		v, q := fixture(at, uint64(n+2), fmt.Sprintf("%.6f", mid-0.1), fmt.Sprintf("%.6f", mid+0.1))
		final, err = e.ApplyTick(v, q)
		if err != nil {
			t.Fatal(err)
		}
	}
	if final == nil || final.Features == nil {
		t.Fatalf("900-second warmup failed: %+v", final)
	}
	wantReturn, err := scaled(10000 * math.Log(101.5/101.4)) // t=840s is the exact 60s boundary
	if err != nil {
		t.Fatal(err)
	}
	if final.Features.Return60MicroBps != wantReturn {
		t.Fatalf("time boundary: got %d want %d", final.Features.Return60MicroBps, wantReturn)
	}
	sum, count := 0.0, 0
	for n := 0; n <= 600; n++ {
		seconds := 1.5 * float64(n)
		if seconds > 600 {
			sum += 100 + seconds/600
			count++
		}
	}
	wantDisplacement, err := scaled(10000 * math.Log(101.5/(sum/float64(count))))
	if err != nil {
		t.Fatal(err)
	}
	if final.Features.DisplacementMicroBps != wantDisplacement {
		t.Fatalf("300-second boundary: got %d want %d", final.Features.DisplacementMicroBps, wantDisplacement)
	}
}

func TestWarmupMomentumStaleAndGeneration(t *testing.T) {
	e, err := NewEngine("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	for n := 0; n <= 900; n++ {
		bid, ask := "99.9", "100.1"
		if n == 900 {
			bid, ask = "100.9", "101.1"
		}
		v, q := fixture(start.Add(time.Duration(n)*time.Second), uint64(n+2), bid, ask)
		i, err := e.ApplyTick(v, q)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 && (i == nil || i.ReasonCodes[0] != "WARMUP") {
			t.Fatalf("initial: %+v", i)
		}
		if n == 900 {
			if i == nil || i.Action != Long || i.Features == nil || !i.Eligible {
				t.Fatalf("warm baseline: %+v", i)
			}
			if i.Momentum.Direction != Long || i.MeanReversion.Direction != Short {
				t.Fatalf("signals: %+v", i)
			}
		}
	}
	v, q := fixture(start.Add(960*time.Second), 962, "100.9", "101.1")
	q.LastBookAt = v.AsOf.Add(-3 * time.Second)
	i, err := e.ApplyTick(v, q)
	if err != nil {
		t.Fatal(err)
	}
	if i == nil || i.Action != NoTrade || i.ReasonCodes[0] != "QUOTE_STALE" {
		t.Fatalf("stale: %+v", i)
	}
	v, q = fixture(start.Add(1020*time.Second), 1022, "100.9", "101.1")
	v.Generation, q.Generation = 2, 2
	i, err = e.ApplyTick(v, q)
	if err != nil {
		t.Fatal(err)
	}
	if i == nil || i.ReasonCodes[0] != "WARMUP" {
		t.Fatalf("new generation: %+v", i)
	}
}

func TestDirectionBoundaryAndShortFeasibility(t *testing.T) {
	if direction(10*micro, 10).Direction != Flat || direction(-10*micro, 10).Direction != Flat {
		t.Fatal("threshold equality must be flat")
	}
	e, err := NewEngine("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	i := &Intent{Momentum: Signal{Direction: Short, ScoreMicroBP: -100 * micro}, FrictionMicroBps: 60 * micro}
	e.decide(i)
	if i.Action != NoTrade || i.ReasonCodes[0] != "SHORT_FEASIBILITY_UNKNOWN" {
		t.Fatalf("short: %+v", i)
	}
	i.Momentum = Signal{Direction: Long, ScoreMicroBP: 70 * micro}
	e.decide(i)
	if i.Action != NoTrade || i.ReasonCodes[0] != "INSUFFICIENT_EDGE" {
		t.Fatalf("margin equality: %+v", i)
	}
}

func TestFuturePoisonCannotRevisePastIntents(t *testing.T) {
	run := func(poison bool) []byte {
		e, err := NewEngine("r", DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
		var prior bytes.Buffer
		for n := 0; n <= 960; n++ {
			bid, ask := "99.9", "100.1"
			if n >= 900 {
				bid, ask = "100.9", "101.1"
			}
			if poison && n > 900 {
				bid, ask = "199.9", "200.1"
			}
			v, q := fixture(start.Add(time.Duration(n)*time.Second), uint64(n+2), bid, ask)
			i, err := e.ApplyTick(v, q)
			if err != nil {
				t.Fatal(err)
			}
			if i != nil && n <= 900 {
				b, err := domain.CanonicalJSON(i)
				if err != nil {
					t.Fatal(err)
				}
				prior.Write(b)
				prior.WriteByte('\n')
			}
		}
		return prior.Bytes()
	}
	if !bytes.Equal(run(false), run(true)) {
		t.Fatal("later quote changed earlier intent bytes")
	}
}

func TestRepeatableIntentBytes(t *testing.T) {
	run := func() []byte {
		e, err := NewEngine("r", DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
		var out bytes.Buffer
		for n := 0; n < 1200; n++ {
			bid, ask := "99.9", "100.1"
			if n >= 900 {
				bid, ask = "100.9", "101.1"
			}
			v, q := fixture(start.Add(time.Duration(n)*time.Second), uint64(n+2), bid, ask)
			i, err := e.ApplyTick(v, q)
			if err != nil {
				t.Fatal(err)
			}
			if i != nil {
				b, err := domain.CanonicalJSON(i)
				if err != nil {
					t.Fatal(err)
				}
				out.Write(b)
				out.WriteByte('\n')
			}
		}
		return out.Bytes()
	}
	if !bytes.Equal(run(), run()) {
		t.Fatal("same input emitted different bytes")
	}
}

func TestEqualTickTimeDoesNotAdvanceWindow(t *testing.T) {
	e, err := NewEngine("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	v, q := fixture(start, 2, "99.9", "100.1")
	if _, err := e.ApplyTick(v, q); err != nil {
		t.Fatal(err)
	}
	v, q = fixture(start, 3, "199.9", "200.1")
	if _, err := e.ApplyTick(v, q); err != nil {
		t.Fatal(err)
	}
	if len(e.samples) != 1 || e.samples[len(e.samples)-1].mid != 100 {
		t.Fatalf("tie advanced window: count=%d mid=%f", len(e.samples), e.samples[len(e.samples)-1].mid)
	}
	v, q = fixture(start.Add(time.Second), 4, "100.9", "101.1")
	if _, err := e.ApplyTick(v, q); err != nil {
		t.Fatal(err)
	}
	if len(e.samples) != 2 {
		t.Fatal("next distinct tick not sampled")
	}
}

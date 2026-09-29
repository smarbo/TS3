package v1

import (
	"testing"
	"time"
)

func TestEvaluatorUsesFirstQuoteAfterEntryAndExit(t *testing.T) {
	c := DefaultConfig()
	r, err := NewResearch("r", c)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine("r", c)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	for n := 0; n <= 1202; n++ {
		bid, ask := "99.9", "100.1"
		if n >= 900 {
			bid, ask = "100.9", "101.1"
		}
		if n >= 902 {
			bid, ask = "101.9", "102.1"
		}
		if n >= 1202 {
			bid, ask = "102.9", "103.1"
		}
		v, q := fixture(start.Add(time.Duration(n)*time.Second), uint64(n+2), bid, ask)
		if err := r.ObserveTick(v, q); err != nil {
			t.Fatal(err)
		}
		i, err := e.ApplyTick(v, q)
		if err != nil {
			t.Fatal(err)
		}
		if i != nil {
			if err := r.AddDecision(*i); err != nil {
				t.Fatal(err)
			}
		}
	}
	report := r.Finalize()
	if report.EligibleDecisions == 0 || report.PairedEpisodes == 0 {
		t.Fatalf("missing paired episode: %+v", report)
	}
	if report.Comparators["always_long"].Episodes != report.PairedEpisodes {
		t.Fatal("unpaired control")
	}
	if report.Comparators["no_trade"].SumNetBps != 0 {
		t.Fatal("NO_TRADE has return")
	}
}

func TestEvaluatorCensorsUnhealthyPath(t *testing.T) {
	c := DefaultConfig()
	r, err := NewResearch("r", c)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	i := Intent{RunID: "r", ConfigSHA256: r.report.ConfigSHA256, AsOfTime: start, QuoteGeneration: 1,
		DecisionID: "r:1:v1", Eligible: true, Features: &Features{MidUSD: 100}, Regime: "NORMAL"}
	if err := r.AddDecision(i); err != nil {
		t.Fatal(err)
	}
	v, q := fixture(start.Add(time.Second), 2, "99", "101")
	v.Health = "UNHEALTHY"
	if err := r.ObserveTick(v, q); err != nil {
		t.Fatal(err)
	}
	report := r.Finalize()
	if report.Censored["UNHEALTHY_PATH"] != 1 || report.PairedEpisodes != 0 {
		t.Fatalf("censor: %+v", report)
	}
}

func TestEvaluatorCannotUseFavorableQuoteBeforeLatency(t *testing.T) {
	c := DefaultConfig()
	r, err := NewResearch("r", c)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	i := Intent{RunID: "r", ConfigSHA256: r.report.ConfigSHA256, AsOfTime: start,
		DecisionID: "r:1:v1", Eligible: true, QuoteGeneration: 1,
		Features: &Features{MidUSD: 100}, Regime: "NORMAL"}
	if err := r.AddDecision(i); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 302; n++ {
		bid, ask := "99", "101"
		if n >= 2 {
			bid, ask = "109", "111"
		}
		if n >= 302 {
			bid, ask = "112", "114"
		}
		v, q := fixture(start.Add(time.Duration(n)*time.Second), uint64(n+1), bid, ask)
		if err := r.ObserveTick(v, q); err != nil {
			t.Fatal(err)
		}
	}
	report := r.Finalize()
	if report.PairedEpisodes != 1 {
		t.Fatalf("paired: %+v", report)
	}
	near(t, report.Comparators["always_long"].SumGrossMidBps, 10000*3.0/110.0)
}

func TestEvaluatorCensorsMissingTicksAndNewGeneration(t *testing.T) {
	c := DefaultConfig()
	for _, mode := range []string{"gap", "generation"} {
		t.Run(mode, func(t *testing.T) {
			r, err := NewResearch("r", c)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
			i := Intent{RunID: "r", ConfigSHA256: r.report.ConfigSHA256, AsOfTime: start,
				DecisionID: "r:1:v1", Eligible: true, QuoteGeneration: 1,
				Features: &Features{MidUSD: 100}, Regime: "NORMAL"}
			if err := r.AddDecision(i); err != nil {
				t.Fatal(err)
			}
			v, q := fixture(start.Add(time.Second), 2, "99", "101")
			if err := r.ObserveTick(v, q); err != nil {
				t.Fatal(err)
			}
			v, q = fixture(start.Add(2*time.Second), 3, "99", "101")
			if err := r.ObserveTick(v, q); err != nil {
				t.Fatal(err)
			}
			v, q = fixture(start.Add(3*time.Second), 4, "99", "101")
			if mode == "gap" {
				v.AsOf = start.Add(6 * time.Second)
			}
			if mode == "generation" {
				v.Generation, q.Generation = 2, 2
			}
			if err := r.ObserveTick(v, q); err != nil {
				t.Fatal(err)
			}
			report := r.Finalize()
			want := "TICK_GAP"
			if mode == "generation" {
				want = "GENERATION_CHANGED"
			}
			if report.Censored[want] != 1 || report.PairedEpisodes != 0 {
				t.Fatalf("missing censor %s: %+v", want, report)
			}
		})
	}
}

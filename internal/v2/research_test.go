package v2

import (
	"fmt"
	"math"
	"testing"
	"time"

	"ts3/internal/book"
	"ts3/internal/v1"
)

func manualV2(t *testing.T, at time.Time, ordinal uint64, action v1.Action) Intent {
	t.Helper()
	v1SHA, err := v1.DefaultConfig().Digest()
	if err != nil {
		t.Fatal(err)
	}
	v2SHA, err := DefaultConfig().Digest()
	if err != nil {
		t.Fatal(err)
	}
	base := v1.Intent{SchemaVersion: v1.SchemaVersion, DecisionID: "r:" + u64(ordinal) + ":v1",
		RunID: "r", AsOfOrdinal: ordinal, AsOfTime: at, Action: v1.NoTrade,
		HorizonSeconds: 300, Eligible: true, QuoteGeneration: 1,
		Features: &v1.Features{MidUSD: 100}, Regime: "NORMAL", ConfigSHA256: v1SHA}
	return Intent{SchemaVersion: SchemaVersion, DecisionID: "r:" + u64(ordinal) + ":v2",
		RunID: "r", AsOfOrdinal: ordinal, AsOfTime: at, Action: action, Baseline: base,
		ReasonCodes: []string{"MULTI_SIGNAL_CONSENSUS"}, ConfigSHA256: v2SHA,
		Signals:     [3]SignalResult{signal(Active, action), signal(Neutral, v1.Flat), signal(Active, action)},
		Evidence:    Evidence{PriceVote: action, BookVote: action, Direction: action, Reason: "CONSENSUS", Disagreement: "0"},
		Opportunity: Opportunity{Available: true, PassesScreen: true, PastMoveProxyMicroBps: 70 * micro, Reason: "SCREEN_PASSED"}}
}

func u64(n uint64) string {
	return fmt.Sprintf("%d", n)
}

func TestResearchUsesV1PairedEndpointsAndDeduplicates(t *testing.T) {
	r, err := NewResearch("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for n := 0; n <= 362; n++ {
		v, q := tick(start.Add(time.Duration(n)*time.Second), uint64(n+1), 1, 100, "2", "1")
		if err := r.ObserveTick(v, q); err != nil {
			t.Fatal(err)
		}
		if n == 0 || n == 60 {
			if err := r.AddDecision(manualV2(t, v.AsOf, v.Ordinal, v1.Long)); err != nil {
				t.Fatal(err)
			}
		}
	}
	report := r.Finalize()
	if report.ObserverMismatches != 0 || report.Evaluator.PairedEpisodes != 2 ||
		report.Pressure30Paired != 2 || report.Pressure30Controls["pressure"].Actions != 2 ||
		report.Evaluator.Comparators["policy"].Actions != 2 ||
		report.V1Policy.Actions != 0 || report.V1Policy.Episodes != 2 ||
		report.DedupTheses != 1 || report.SuppressedRepeats != 1 ||
		report.CohortPolicy["all_paired"].Episodes != 2 ||
		report.FamilyHypothetical["book_pressure_confirmation_300s"].Actions != 2 {
		t.Fatalf("paired/deduplicated research: %+v", report)
	}
}

func TestAdverseUnhealthyPathStaysCensoredInV2(t *testing.T) {
	r, err := NewResearch("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for n := 0; n <= 5; n++ {
		v, q := tick(start.Add(time.Duration(n)*time.Second), uint64(n+1), 1, 100, "2", "1")
		if n == 3 {
			v.Health = book.Unhealthy
		}
		if err := r.ObserveTick(v, q); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			if err := r.AddDecision(manualV2(t, v.AsOf, v.Ordinal, v1.Long)); err != nil {
				t.Fatal(err)
			}
		}
	}
	report := r.Finalize()
	if report.Evaluator.PairedEpisodes != 0 || report.Evaluator.Censored["UNHEALTHY_PATH"] != 1 ||
		report.Pressure30Paired != 0 || report.Pressure30Censored["UNHEALTHY_PATH"] != 1 ||
		report.CohortPolicy["all_paired"].Episodes != 0 {
		t.Fatalf("adverse outage hidden: %+v", report)
	}
}

func TestPressureThirtySecondEndpointsRejectHeldPreBoundaryQuotes(t *testing.T) {
	r, err := NewResearch("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	var entry, exit book.Quote
	for n := 0; n <= 34; n++ {
		mid := 100.0
		if n == 1 || n == 2 {
			mid = 90
		}
		if n == 32 || n == 33 {
			mid = 120
		}
		if n == 34 {
			mid = 101
		}
		v, q := tick(start.Add(time.Duration(n)*time.Second), uint64(n+1), 1, mid, "2", "1")
		if n == 2 || n == 33 {
			q.LastBookAt = start.Add(time.Duration(n-1) * time.Second)
			q.LastBookOrdinal = uint64(n)
			v.LastBookOrdinal = uint64(n)
		}
		if n == 3 {
			entry = q
		}
		if n == 34 {
			exit = q
		}
		if err := r.ObserveTick(v, q); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			if err := r.AddDecision(manualV2(t, v.AsOf, v.Ordinal, v1.Long)); err != nil {
				t.Fatal(err)
			}
		}
	}
	report := r.Finalize()
	expected, err := v1.EvaluateSide(entry, exit, 1, v1.Long, v1.DefaultConfig(), 31*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if report.Pressure30Paired != 1 || report.Pressure30Controls["pressure"].Actions != 1 ||
		math.Abs(report.Pressure30Controls["pressure"].SumNetBps-expected.NetBps) > 1e-9 {
		t.Fatalf("held pre-boundary quote selected: %+v expected %+v", report.Pressure30Controls["pressure"], expected)
	}
}

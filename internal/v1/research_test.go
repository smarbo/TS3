package v1

import (
	"reflect"
	"testing"
	"time"

	"ts3/internal/book"
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
	if report.FoldComparators["fold_0"]["always_long"].Episodes != report.PairedEpisodes {
		t.Fatal("chronological fold lost paired episodes")
	}
	if report.Comparators["no_trade"].SumNetBps != 0 {
		t.Fatal("NO_TRADE has return")
	}
}

func TestBlockUncertaintyIsDeterministic(t *testing.T) {
	r, err := NewResearch("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"policy", "momentum", "mean_reversion", "always_long", "random_matched", "no_trade"}
	for block := int64(0); block < 4; block++ {
		r.blocks[block] = map[string]blockSum{}
		for _, name := range names {
			r.blocks[block][name] = blockSum{net: float64(block), episodes: 1}
			r.report.Comparators[name] = ResultStats{Episodes: 4, SumNetBps: 6}
		}
	}
	r.bootstrap()
	first := r.report.Uncertainty["policy"]
	if first.Blocks != 4 || first.MeanNetBpsPerEpisode != 1.5 || first.P05Bps > first.MeanNetBpsPerEpisode || first.P95Bps < first.MeanNetBpsPerEpisode {
		t.Fatalf("invalid interval: %+v", first)
	}
	saved := r.report.Uncertainty
	r.report.Uncertainty = map[string]BootstrapInterval{}
	r.bootstrap()
	if !reflect.DeepEqual(saved, r.report.Uncertainty) {
		t.Fatal("bootstrap changed on repeat")
	}
}

func TestChronologicalFoldBoundary(t *testing.T) {
	r, err := NewResearch("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	r.report.FirstTick = start
	entryAt := start.Add(6*time.Hour + 2*time.Second)
	_, entry := fixture(entryAt, 10, "99", "101")
	_, exit := fixture(entryAt.Add(5*time.Minute), 11, "100", "102")
	p := episode{intent: Intent{AsOfTime: start.Add(6 * time.Hour), Action: NoTrade, Regime: "NORMAL"},
		quantity: 1, entry: entry, entryTime: entryAt}
	if err := r.complete(p, exit, entryAt.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if r.report.FoldComparators["fold_1"]["always_long"].Episodes != 1 ||
		r.report.FoldComparators["fold_0"]["always_long"].Episodes != 0 {
		t.Fatalf("wrong fold: %+v", r.report.FoldComparators)
	}
}

func TestEvaluatorCensorsUnhealthyPath(t *testing.T) {
	c := DefaultConfig()
	r, err := NewResearch("r", c)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	addManualDecision(t, r, start)
	v, q := fixture(start.Add(time.Second), 3, "99", "101")
	v.Health = "UNHEALTHY"
	if err := r.ObserveTick(v, q); err != nil {
		t.Fatal(err)
	}
	report := r.Finalize()
	if report.Censored["UNHEALTHY_PATH"] != 1 || report.PairedEpisodes != 0 {
		t.Fatalf("censor: %+v", report)
	}
}

func TestAdverseOutageIsReportedAsCoverageLoss(t *testing.T) {
	r, err := NewResearch("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	addManualDecision(t, r, start)
	for n := 1; n <= 4; n++ {
		bid, ask := "99", "101"
		if n == 3 {
			bid, ask = "79", "81" // adverse price during an unhealthy interval
		}
		v, q := fixture(start.Add(time.Duration(n)*time.Second), uint64(n+2), bid, ask)
		if n == 3 {
			v.Health = book.Unhealthy
		}
		if err := r.ObserveTick(v, q); err != nil {
			t.Fatal(err)
		}
	}
	report := r.Finalize()
	if report.EligibleDecisions != 1 || report.PairedEpisodes != 0 ||
		report.Censored["UNHEALTHY_PATH"] != 1 || len(report.Comparators) != 0 {
		t.Fatalf("adverse outage disappeared from coverage: %+v", report)
	}
}

func TestEvaluatorCannotUseFavorableQuoteBeforeLatency(t *testing.T) {
	c := DefaultConfig()
	r, err := NewResearch("r", c)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	addManualDecision(t, r, start)
	for n := 1; n <= 303; n++ {
		bid, ask := "99", "101"
		if n >= 3 {
			bid, ask = "109", "111"
		}
		if n >= 303 {
			bid, ask = "112", "114"
		}
		v, q := fixture(start.Add(time.Duration(n)*time.Second), uint64(n+2), bid, ask)
		if n == 2 {
			q.LastBookAt = start.Add(time.Second)
		} // still healthy, but observed before entry boundary
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
			addManualDecision(t, r, start)
			v, q := fixture(start.Add(time.Second), 3, "99", "101")
			if err := r.ObserveTick(v, q); err != nil {
				t.Fatal(err)
			}
			v, q = fixture(start.Add(2*time.Second), 4, "99", "101")
			if err := r.ObserveTick(v, q); err != nil {
				t.Fatal(err)
			}
			v, q = fixture(start.Add(3*time.Second), 5, "99", "101")
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

func addManualDecision(t *testing.T, r *Research, start time.Time) {
	t.Helper()
	v, q := fixture(start, 2, "99", "101")
	if err := r.ObserveTick(v, q); err != nil {
		t.Fatal(err)
	}
	i := Intent{SchemaVersion: SchemaVersion, RunID: "r", ConfigSHA256: r.report.ConfigSHA256,
		AsOfTime: start, AsOfOrdinal: 2, DecisionID: "r:2:v1", HorizonSeconds: 300, Action: NoTrade,
		Eligible: true, QuoteGeneration: 1, Features: &Features{MidUSD: 100}, Regime: "NORMAL"}
	if err := r.AddDecision(i); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluatorRejectsFutureOrDuplicateIntent(t *testing.T) {
	r, err := NewResearch("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	v, q := fixture(start, 2, "99", "101")
	if err := r.ObserveTick(v, q); err != nil {
		t.Fatal(err)
	}
	i := Intent{SchemaVersion: SchemaVersion, RunID: "r", ConfigSHA256: r.report.ConfigSHA256,
		AsOfTime: start.Add(time.Second), AsOfOrdinal: 2, DecisionID: "r:2:v1", HorizonSeconds: 300, Action: NoTrade}
	if err := r.AddDecision(i); err == nil {
		t.Fatal("future decision accepted")
	}
	i.AsOfTime = start
	if err := r.AddDecision(i); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDecision(i); err == nil {
		t.Fatal("duplicate decision accepted")
	}
}

func TestRandomComparatorMatchesFilteredPolicyFrequency(t *testing.T) {
	for _, intent := range []Intent{
		{Action: NoTrade, Momentum: Signal{Direction: Long}},
		{Action: NoTrade, Momentum: Signal{Direction: Short}},
		{Action: NoTrade, Momentum: Signal{Direction: Flat}},
	} {
		if got := randomAction(intent); got != NoTrade {
			t.Fatalf("filtered policy acted in random control: %s", got)
		}
	}
	for _, action := range []Action{Long, Short} {
		got := randomAction(Intent{Action: action, DecisionID: "r:2:v1"})
		if got != Long && got != Short {
			t.Fatalf("policy action absent from random control: %s", got)
		}
	}
}

func TestPairedObserverDoesNotChangeFrozenV1Report(t *testing.T) {
	plain, err := NewResearch("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	observed, err := NewResearch("r", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	observed.ObservePaired(func(p PairedObservation) {
		count++
		if p.Intent.AsOfOrdinal != 2 || !p.ExitAt.After(p.EntryAt) {
			t.Fatalf("bad paired observer event: %+v", p)
		}
	})
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	addManualDecision(t, plain, start)
	addManualDecision(t, observed, start)
	for n := 1; n <= 302; n++ {
		v, q := fixture(start.Add(time.Duration(n)*time.Second), uint64(n+2), "99", "101")
		for _, r := range []*Research{plain, observed} {
			if err := r.ObserveTick(v, q); err != nil {
				t.Fatal(err)
			}
		}
	}
	if count != 1 || !reflect.DeepEqual(plain.Finalize(), observed.Finalize()) {
		t.Fatal("downstream observer changed accepted V1 report")
	}
}

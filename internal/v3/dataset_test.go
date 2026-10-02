package v3

import (
	"testing"
	"time"

	"ts3/internal/v1"
	"ts3/internal/v2"
)

func pairedFixture() (v2.Intent, v1.PairedObservation) {
	at := time.Date(2026, 9, 27, 22, 0, 0, 0, time.UTC)
	baseline := v1.Intent{SchemaVersion: 1, DecisionID: "run:100:v1", RunID: "run",
		AsOfOrdinal: 100, AsOfTime: at, Eligible: true, QuoteGeneration: 2,
		QuoteAsOfOrdinal: 99, ConfigSHA256: "v1", Regime: "NORMAL",
		Features: &v1.Features{Version: "features.v1", MidUSD: 80000,
			Return60MicroBps: 12000000, DisplacementMicroBps: -3000000,
			RMS900MicroBps: 100000, SpreadMicroBps: 200000}}
	i := v2.Intent{SchemaVersion: 2, DecisionID: "run:100:v2", RunID: "run",
		AsOfOrdinal: 100, AsOfTime: at, Baseline: baseline, ConfigSHA256: "v2",
		Evidence: v2.Evidence{Disagreement: "UNDEFINED"},
		Signals: [3]v2.SignalResult{
			{ID: "trend.v2.1", AsOfOrdinal: 100, SourceBookOrdinal: 99, Generation: 2},
			{ID: "reversion.v2.1", AsOfOrdinal: 100, SourceBookOrdinal: 99, Generation: 2},
			{ID: "book_pressure.v2.1", Status: v2.Neutral, Score: 50000,
				AsOfOrdinal: 100, SourceBookOrdinal: 99, Generation: 2},
		}}
	p := v1.PairedObservation{Intent: baseline, EntryAt: at.Add(2 * time.Second),
		ExitAt: at.Add(302 * time.Second), Long: v1.Outcome{DepthBps: 1, NetBps: -49},
		Short: v1.Outcome{NetBps: -51}}
	return i, p
}

func TestFromPairedCausalityAndLabels(t *testing.T) {
	i, p := pairedFixture()
	row, ok, err := FromPaired(i, p, "manifest")
	if err != nil || !ok || !row.DepthUp || row.LongNetPositive || row.Features[0] != 12 ||
		row.Features[4] != 0.05 || row.Features[6] != -1 {
		t.Fatalf("unexpected row %+v, available %v, error %v", row, ok, err)
	}
	i.Signals[2].SourceBookOrdinal = 101
	if _, _, err := FromPaired(i, p, "manifest"); err == nil {
		t.Fatal("future book ordinal accepted")
	}
	i, p = pairedFixture()
	p.EntryAt = i.AsOfTime.Add(time.Second)
	if _, _, err := FromPaired(i, p, "manifest"); err == nil {
		t.Fatal("entry before declared delay accepted")
	}
	i, p = pairedFixture()
	i.Signals[2].Status = v2.Unavailable
	if _, ok, err := FromPaired(i, p, "manifest"); err != nil || ok {
		t.Fatalf("unavailable pressure admitted: %v %v", ok, err)
	}
}

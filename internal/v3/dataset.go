package v3

import (
	"errors"
	"fmt"
	"math"
	"time"

	"ts3/internal/v1"
	"ts3/internal/v2"
)

const FeatureSchema = "v3.features.1"
const TargetVersion = "depth_up_300_v1"
const CostVersion = "v1.default.20fee.5allowance.usd100"

var FeatureNames = [7]string{"return60", "displacement300", "rms900", "spread", "book_pressure30", "volatile", "disagreement"}

// Row binds a causal decision snapshot to a later, downstream paired outcome.
// Outcomes are never available to the live engine.
type Row struct {
	SchemaVersion      int        `json:"schema_version"`
	FeatureSchema      string     `json:"feature_schema"`
	TargetVersion      string     `json:"target_version"`
	CostVersion        string     `json:"cost_version"`
	SourceManifestSHA  string     `json:"source_manifest_sha256"`
	RunID              string     `json:"run_id"`
	DecisionOrdinal    uint64     `json:"decision_ordinal,string"`
	DecisionTime       time.Time  `json:"decision_time"`
	EntryTime          time.Time  `json:"entry_time"`
	ExitTime           time.Time  `json:"exit_time"`
	QuoteGeneration    uint64     `json:"quote_generation,string"`
	QuoteBookOrdinal   uint64     `json:"quote_book_ordinal,string"`
	V1ConfigSHA        string     `json:"v1_config_sha256"`
	V2ConfigSHA        string     `json:"v2_config_sha256"`
	V1FeatureVersion   string     `json:"v1_feature_version"`
	SignalVersions     [3]string  `json:"signal_versions"`
	Regime             string     `json:"regime"`
	Features           [7]float64 `json:"features"`
	DepthUp            bool       `json:"depth_up"`
	LongDepthBps       float64    `json:"long_depth_bps"`
	LongGrossMidBps    float64    `json:"long_gross_mid_bps"`
	LongNetBps         float64    `json:"long_net_bps"`
	ShortNetBps        float64    `json:"hypothetical_short_net_bps"`
	LongNetPositive    bool       `json:"long_net_positive"`
	V1Action           v1.Action  `json:"v1_action"`
	V2Action           v1.Action  `json:"v2_action"`
	MomentumDirection  v1.Action  `json:"momentum_direction"`
	ReversionDirection v1.Action  `json:"reversion_direction"`
}

// FromPaired rejects mismatched or future-dependent provenance. A false return
// means the fixed V3 feature schema is unavailable, not a negative label.
func FromPaired(i v2.Intent, p v1.PairedObservation, sourceManifestSHA string) (Row, bool, error) {
	if i.RunID != p.Intent.RunID || i.AsOfOrdinal != p.Intent.AsOfOrdinal ||
		!i.AsOfTime.Equal(p.Intent.AsOfTime) || i.Baseline.DecisionID != p.Intent.DecisionID ||
		i.Baseline.ConfigSHA256 != p.Intent.ConfigSHA256 ||
		p.Intent.QuoteAsOfOrdinal > p.Intent.AsOfOrdinal ||
		p.Intent.QuoteGeneration == 0 || !p.Intent.Eligible ||
		!p.EntryAt.After(p.Intent.AsOfTime) || !p.ExitAt.After(p.EntryAt) ||
		p.EntryAt.Before(p.Intent.AsOfTime.Add(2*time.Second)) ||
		p.ExitAt.Before(p.EntryAt.Add(300*time.Second)) {
		return Row{}, false, errors.New("V3 paired provenance/time mismatch")
	}
	if i.Baseline.Features == nil || p.Intent.Features == nil ||
		*i.Baseline.Features != *p.Intent.Features {
		return Row{}, false, errors.New("V3 baseline feature mismatch")
	}
	features, available, err := FeaturesFromIntent(i)
	if err != nil || !available {
		return Row{}, available, err
	}
	f := p.Intent.Features
	row := Row{SchemaVersion: 1, FeatureSchema: FeatureSchema, TargetVersion: TargetVersion,
		CostVersion: CostVersion, SourceManifestSHA: sourceManifestSHA, RunID: i.RunID,
		DecisionOrdinal: i.AsOfOrdinal, DecisionTime: i.AsOfTime, EntryTime: p.EntryAt,
		ExitTime: p.ExitAt, QuoteGeneration: p.Intent.QuoteGeneration,
		QuoteBookOrdinal: p.Intent.QuoteAsOfOrdinal, V1ConfigSHA: p.Intent.ConfigSHA256,
		V2ConfigSHA: i.ConfigSHA256, V1FeatureVersion: f.Version,
		SignalVersions: [3]string{i.Signals[0].ID, i.Signals[1].ID, i.Signals[2].ID},
		Regime:         p.Intent.Regime, Features: features, DepthUp: p.Long.DepthBps > 0,
		LongDepthBps: p.Long.DepthBps, LongGrossMidBps: p.Long.GrossMidBps,
		LongNetBps: p.Long.NetBps, ShortNetBps: p.Short.NetBps,
		LongNetPositive: p.Long.NetBps > 0, V1Action: p.Intent.Action,
		V2Action: i.Action, MomentumDirection: p.Intent.Momentum.Direction,
		ReversionDirection: p.Intent.MeanReversion.Direction}
	return row, true, nil
}

// FeaturesFromIntent is the exact causal feature mapping shared by offline
// row export and live/replay inference. It never sees a future outcome.
func FeaturesFromIntent(i v2.Intent) ([7]float64, bool, error) {
	if i.AsOfOrdinal == 0 || i.Baseline.AsOfOrdinal != i.AsOfOrdinal ||
		!i.Baseline.AsOfTime.Equal(i.AsOfTime) ||
		i.Baseline.QuoteAsOfOrdinal > i.AsOfOrdinal {
		return [7]float64{}, false, errors.New("V3 intent as-of provenance mismatch")
	}
	for _, s := range i.Signals {
		if s.AsOfOrdinal > i.AsOfOrdinal || s.SourceBookOrdinal > i.AsOfOrdinal ||
			s.Generation != i.Baseline.QuoteGeneration {
			return [7]float64{}, false, errors.New("V3 future/cross-generation signal")
		}
	}
	if i.Baseline.Features == nil || !i.Baseline.Eligible ||
		(i.Baseline.Regime != "NORMAL" && i.Baseline.Regime != "VOLATILE") ||
		(i.Signals[2].Status != v2.Active && i.Signals[2].Status != v2.Neutral) {
		return [7]float64{}, false, nil
	}
	disagreement := -1.0
	switch i.Evidence.Disagreement {
	case "UNDEFINED":
	case "0":
		disagreement = 0
	case "1":
		disagreement = 1
	default:
		return [7]float64{}, false, fmt.Errorf("unknown disagreement %q", i.Evidence.Disagreement)
	}
	f := i.Baseline.Features
	features := [7]float64{
		float64(f.Return60MicroBps) / 1e6,
		float64(f.DisplacementMicroBps) / 1e6,
		float64(f.RMS900MicroBps) / 1e6,
		float64(f.SpreadMicroBps) / 1e6,
		float64(i.Signals[2].Score) / 1e6,
		0,
		disagreement,
	}
	if i.Baseline.Regime == "VOLATILE" {
		features[5] = 1
	}
	for _, value := range features {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return [7]float64{}, false, errors.New("nonfinite V3 feature")
		}
	}
	return features, true, nil
}

package v2

import (
	"fmt"
	"time"

	"ts3/internal/book"
	"ts3/internal/v1"
)

// Research consumes immutable V2 intents and later quotes. V1's accepted
// endpoint selection and accounting are reused, with a read-only paired hook.
// Neither this type nor the hook is reachable from the decision engine.
type Research struct {
	base           *v1.Research
	runID          string
	configSHA      string
	intents        map[uint64]Intent
	report         Report
	lastOrdinal    uint64
	thesisUntil    time.Time
	lastThesisSide v1.Action
}

type Report struct {
	SchemaVersion       int                       `json:"schema_version"`
	RunID               string                    `json:"run_id"`
	ConfigSHA256        string                    `json:"config_sha256"`
	Evaluator           v1.Report                 `json:"paired_evaluator"`
	RawActions          map[v1.Action]int         `json:"raw_actions"`
	DedupTheses         int                       `json:"deduplicated_theses"`
	SuppressedRepeats   int                       `json:"suppressed_repeated_actions"`
	SuppressedReversals int                       `json:"suppressed_reversals"`
	SignalStatus        map[string]map[Status]int `json:"signal_status"`
	Reasons             map[string]int            `json:"reasons"`
	Disagreement        map[string]int            `json:"disagreement"`
	OpportunityReasons  map[string]int            `json:"opportunity_reasons"`
	FamilyHypothetical  map[string]v1.ResultStats `json:"family_hypothetical"`
	CohortPolicy        map[string]v1.ResultStats `json:"cohort_policy"`
	CohortPrice         map[string]v1.ResultStats `json:"cohort_price_hypothetical"`
	V1Policy            v1.ResultStats            `json:"frozen_v1_policy"`
	ObserverMismatches  int                       `json:"observer_mismatches"`
	Limitations         []string                  `json:"limitations"`
}

func NewResearch(runID string, c Config) (*Research, error) {
	sha, err := c.Digest()
	if err != nil {
		return nil, err
	}
	b, err := v1.NewResearch(runID, v1.DefaultConfig())
	if err != nil {
		return nil, err
	}
	r := &Research{base: b, runID: runID, configSHA: sha, intents: map[uint64]Intent{},
		report: Report{SchemaVersion: SchemaVersion, RunID: runID, ConfigSHA256: sha,
			RawActions: map[v1.Action]int{}, SignalStatus: map[string]map[Status]int{},
			Reasons: map[string]int{}, Disagreement: map[string]int{}, OpportunityReasons: map[string]int{},
			FamilyHypothetical: map[string]v1.ResultStats{}, CohortPolicy: map[string]v1.ResultStats{},
			CohortPrice: map[string]v1.ResultStats{},
			Limitations: []string{"One exposed development day; no sealed economic test or calibrated expected edge.",
				"Displayed depth, fixed fee/allowance, and two-second latency are hypothetical, not fills.",
				"The pressure family is short-horizon confirmation, not proven independent five-minute alpha."}}}
	b.ObservePaired(r.onPaired)
	return r, nil
}

func (r *Research) ObserveTick(v book.View, q book.Quote) error {
	if err := r.base.ObserveTick(v, q); err != nil {
		return err
	}
	for ordinal, i := range r.intents {
		if v.AsOf.After(i.AsOfTime.Add(310 * time.Second)) {
			delete(r.intents, ordinal)
		}
	}
	return nil
}

func (r *Research) AddDecision(i Intent) error {
	if i.SchemaVersion != SchemaVersion || i.RunID != r.runID || i.ConfigSHA256 != r.configSHA ||
		i.AsOfOrdinal == 0 || i.AsOfOrdinal <= r.lastOrdinal ||
		i.DecisionID != fmt.Sprintf("%s:%d:v2", i.RunID, i.AsOfOrdinal) ||
		i.Baseline.AsOfOrdinal != i.AsOfOrdinal || !i.Baseline.AsOfTime.Equal(i.AsOfTime) ||
		(i.Action != v1.Long && i.Action != v1.Short && i.Action != v1.NoTrade) {
		return fmt.Errorf("V2 research provenance/order mismatch at %d", i.AsOfOrdinal)
	}
	r.lastOrdinal = i.AsOfOrdinal
	shadow := i.Baseline
	shadow.Action = i.Action
	shadow.ReasonCodes = i.ReasonCodes
	if err := r.base.AddDecision(shadow); err != nil {
		return err
	}
	if i.Baseline.Eligible {
		r.intents[i.AsOfOrdinal] = i
	}
	r.report.RawActions[i.Action]++
	for _, signal := range i.Signals {
		if r.report.SignalStatus[signal.ID] == nil {
			r.report.SignalStatus[signal.ID] = map[Status]int{}
		}
		r.report.SignalStatus[signal.ID][signal.Status]++
	}
	for _, reason := range i.ReasonCodes {
		r.report.Reasons[reason]++
	}
	r.report.Disagreement[i.Evidence.Disagreement]++
	if i.Opportunity.Reason != "" {
		r.report.OpportunityReasons[i.Opportunity.Reason]++
	}
	if i.Action == v1.Long || i.Action == v1.Short {
		// Downstream thesis grouping never suppresses or alters an engine intent.
		// A thesis remains open until 300 seconds after its earliest entry.
		if !i.AsOfTime.Before(r.thesisUntil) {
			r.report.DedupTheses++
			r.thesisUntil = i.AsOfTime.Add(302 * time.Second)
			r.lastThesisSide = i.Action
		} else {
			if i.Action == r.lastThesisSide {
				r.report.SuppressedRepeats++
			} else {
				r.report.SuppressedReversals++
			}
		}
	}
	return nil
}

func pairedAdd(m map[string]v1.ResultStats, key string, action v1.Action, long, short v1.Outcome) {
	s := m[key]
	s.Add(action, long, short)
	m[key] = s
}

func cohortKeys(i Intent) []string {
	keys := []string{"all_paired", "regime_" + i.Baseline.Regime,
		"disagreement_" + i.Evidence.Disagreement}
	if len(i.ReasonCodes) > 0 {
		keys = append(keys, "reason_"+i.ReasonCodes[0])
	}
	active := 0
	for _, s := range i.Signals {
		if s.Status == Active {
			active++
		}
	}
	if active == 1 {
		for _, s := range i.Signals {
			if s.Status == Active {
				keys = append(keys, "alone_"+s.ID)
			}
		}
	}
	if i.Signals[0].Status == Active && i.Signals[2].Status == Active &&
		i.Signals[0].Direction == i.Signals[2].Direction {
		keys = append(keys, "trend_book_agree")
	}
	if i.Signals[0].Status == Active && i.Signals[1].Status == Active &&
		i.Signals[0].Direction != i.Signals[1].Direction {
		keys = append(keys, "trend_reversion_disagree")
	}
	if i.Evidence.Reason == "CONSENSUS" {
		if i.Opportunity.PassesScreen {
			keys = append(keys, "consensus_screen_pass")
		} else {
			keys = append(keys, "consensus_screen_reject")
		}
	}
	if i.Opportunity.Reason != "" {
		keys = append(keys, "opportunity_"+i.Opportunity.Reason)
	}
	if !i.Opportunity.Available {
		keys = append(keys, "proxy_unavailable")
	} else {
		proxy := i.Opportunity.PastMoveProxyMicroBps
		switch {
		case proxy < 10*micro:
			keys = append(keys, "proxy_lt_10bps")
		case proxy < 30*micro:
			keys = append(keys, "proxy_10_30bps")
		case proxy < 60*micro:
			keys = append(keys, "proxy_30_60bps")
		default:
			keys = append(keys, "proxy_ge_60bps")
		}
	}
	return keys
}

func (r *Research) onPaired(p v1.PairedObservation) {
	i, ok := r.intents[p.Intent.AsOfOrdinal]
	if !ok {
		r.report.ObserverMismatches++
		return
	}
	delete(r.intents, p.Intent.AsOfOrdinal)
	pairedAdd(r.report.FamilyHypothetical, "trend", i.Signals[0].Direction, p.Long, p.Short)
	pairedAdd(r.report.FamilyHypothetical, "reversion", i.Signals[1].Direction, p.Long, p.Short)
	pairedAdd(r.report.FamilyHypothetical, "book_pressure", i.Signals[2].Direction, p.Long, p.Short)
	r.report.V1Policy.Add(i.Baseline.Action, p.Long, p.Short)
	for _, key := range cohortKeys(i) {
		pairedAdd(r.report.CohortPolicy, key, i.Action, p.Long, p.Short)
		pairedAdd(r.report.CohortPrice, key, i.Evidence.PriceVote, p.Long, p.Short)
	}
}

func (r *Research) Finalize() Report {
	r.report.Evaluator = r.base.Finalize()
	return r.report
}

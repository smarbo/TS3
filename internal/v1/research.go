package v1

import (
	"crypto/sha256"
	"fmt"
	"time"

	"ts3/internal/book"
)

const ResearchBorrowBpsPerDay = 10

type ResultStats struct {
	Episodes        int     `json:"episodes"`
	Actions         int     `json:"actions"`
	PositiveActions int     `json:"positive_actions"`
	SumNetBps       float64 `json:"sum_net_bps"`
	SumGrossMidBps  float64 `json:"sum_gross_mid_bps"`
	SumFrictionBps  float64 `json:"sum_friction_bps"`
}

func (s *ResultStats) Add(action Action, long, short Outcome) {
	s.Episodes++
	if action == NoTrade || action == Flat {
		return
	}
	s.Actions++
	o := long
	if action == Short {
		o = short
	}
	s.SumNetBps += o.NetBps
	s.SumGrossMidBps += o.GrossMidBps
	s.SumFrictionBps += o.GrossMidBps - o.NetBps
	if o.NetBps > 0 {
		s.PositiveActions++
	}
}

type Report struct {
	SchemaVersion     int                    `json:"schema_version"`
	RunID             string                 `json:"run_id"`
	ConfigSHA256      string                 `json:"config_sha256"`
	FirstTick         time.Time              `json:"first_tick"`
	LastTick          time.Time              `json:"last_tick"`
	LastOrdinal       uint64                 `json:"last_ordinal,string"`
	CalendarMinutes   int                    `json:"calendar_minutes"`
	Decisions         int                    `json:"decisions"`
	EligibleDecisions int                    `json:"eligible_decisions"`
	PairedEpisodes    int                    `json:"paired_episodes"`
	Censored          map[string]int         `json:"censored"`
	Reasons           map[string]int         `json:"reasons"`
	RegimeCounts      map[string]int         `json:"regime_counts"`
	Comparators       map[string]ResultStats `json:"comparators"`
	RegimePolicy      map[string]ResultStats `json:"regime_policy"`
	PolicyCostLow     ResultStats            `json:"policy_cost_allowance_0"`
	PolicyCostHigh    ResultStats            `json:"policy_cost_allowance_10"`
	BorrowBpsPerDay   int                    `json:"research_short_borrow_bps_per_day"`
	UncertaintyNote   string                 `json:"uncertainty_note"`
	Limitations       []string               `json:"limitations"`
}

type episode struct {
	intent     Intent
	quantity   float64
	entryAfter time.Time
	entry      book.Quote
	entryTime  time.Time
	exitAfter  time.Time
}

type Research struct {
	config  Config
	report  Report
	pending []episode
}

func NewResearch(runID string, config Config) (*Research, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	digest, err := config.Digest()
	if err != nil {
		return nil, err
	}
	return &Research{config: config, report: Report{SchemaVersion: SchemaVersion, RunID: runID,
		ConfigSHA256: digest, Censored: map[string]int{}, Reasons: map[string]int{},
		RegimeCounts: map[string]int{}, Comparators: map[string]ResultStats{}, RegimePolicy: map[string]ResultStats{},
		BorrowBpsPerDay: ResearchBorrowBpsPerDay,
		UncertaintyNote: "Overlapping five-minute labels and one roughly day-long tape are not independent trials or durable edge evidence.",
		Limitations:     []string{"Exploratory development tape; no sealed final period opened.", "Displayed depth and assumed taker costs are not fills or a current fee quote.", "Short comparator uses a hypothetical borrow scenario; spot data do not establish short feasibility."}}}, nil
}

func eligibleQuote(v book.View, q book.Quote, maxAge time.Duration) bool {
	return v.Health == book.Healthy && q.Generation != 0 && q.Generation == v.Generation && q.Epoch == v.Epoch &&
		!q.LastBookAt.IsZero() && !v.AsOf.Before(q.LastBookAt) && v.AsOf.Sub(q.LastBookAt) <= maxAge &&
		len(q.Bids) > 0 && len(q.Asks) > 0
}

// ObserveTick sees one current tick and can only settle prior decisions.
// AddDecision is called afterward, so the current tick cannot be a zero-latency
// entry for its own decision.
func (r *Research) ObserveTick(v book.View, q book.Quote) error {
	if r.report.FirstTick.IsZero() {
		r.report.FirstTick = v.AsOf
	}
	if !r.report.LastTick.IsZero() && v.AsOf.Before(r.report.LastTick) {
		return fmt.Errorf("research time regressed at ordinal %d", v.Ordinal)
	}
	if !r.report.LastTick.IsZero() && v.AsOf.Sub(r.report.LastTick) > 2*time.Second {
		r.report.Censored["TICK_GAP"] += len(r.pending)
		r.pending = nil
	}
	r.report.LastTick = v.AsOf
	r.report.LastOrdinal = v.Ordinal
	good := eligibleQuote(v, q, time.Duration(r.config.QuoteMaxAgeMS)*time.Millisecond)
	keep := make([]episode, 0, len(r.pending))
	for _, p := range r.pending {
		if q.Generation != p.intent.QuoteGeneration || q.Epoch == 0 {
			r.report.Censored["GENERATION_CHANGED"]++
			continue
		}
		if !good {
			r.report.Censored["UNHEALTHY_PATH"]++
			continue
		}
		if p.entryTime.IsZero() {
			if v.AsOf.After(p.entryAfter.Add(2 * time.Second)) {
				r.report.Censored["ENTRY_MISSING"]++
				continue
			}
			if !v.AsOf.Before(p.entryAfter) {
				p.entry = q
				p.entryTime = v.AsOf
				p.exitAfter = v.AsOf.Add(time.Duration(r.config.HorizonSeconds) * time.Second)
			}
			keep = append(keep, p)
			continue
		}
		if v.AsOf.After(p.exitAfter.Add(2 * time.Second)) {
			r.report.Censored["EXIT_MISSING"]++
			continue
		}
		if v.AsOf.Before(p.exitAfter) {
			keep = append(keep, p)
			continue
		}
		if err := r.complete(p, q, v.AsOf); err != nil {
			r.report.Censored["DEPTH_MISSING"]++
		}
	}
	r.pending = keep
	return nil
}

func (r *Research) AddDecision(i Intent) error {
	if i.RunID != r.report.RunID || i.ConfigSHA256 != r.report.ConfigSHA256 {
		return fmt.Errorf("research provenance mismatch at %d", i.AsOfOrdinal)
	}
	r.report.Decisions++
	r.report.RegimeCounts[i.Regime]++
	for _, reason := range i.ReasonCodes {
		r.report.Reasons[reason]++
	}
	if !i.Eligible {
		return nil
	}
	r.report.EligibleDecisions++
	if i.Features == nil || i.Features.MidUSD <= 0 {
		return fmt.Errorf("eligible intent has no midpoint")
	}
	r.pending = append(r.pending, episode{intent: i,
		quantity:   float64(r.config.ReferenceNotionalUSD) / i.Features.MidUSD,
		entryAfter: i.AsOfTime.Add(2 * time.Second)})
	return nil
}

func randomAction(i Intent) Action {
	if i.Momentum.Direction == Flat {
		return NoTrade
	}
	h := sha256.Sum256([]byte(i.DecisionID))
	if h[0]&1 == 0 {
		return Long
	}
	return Short
}

func (r *Research) complete(p episode, exit book.Quote, exitTime time.Time) error {
	shortConfig := r.config
	shortConfig.BorrowBpsPerDay = ResearchBorrowBpsPerDay
	holding := exitTime.Sub(p.entryTime)
	long, err := EvaluateSide(p.entry, exit, p.quantity, Long, r.config, holding)
	if err != nil {
		return err
	}
	short, err := EvaluateSide(p.entry, exit, p.quantity, Short, shortConfig, holding)
	if err != nil {
		return err
	}
	r.report.PairedEpisodes++
	actions := []struct {
		name   string
		action Action
	}{
		{"policy", p.intent.Action}, {"momentum", p.intent.Momentum.Direction},
		{"mean_reversion", p.intent.MeanReversion.Direction}, {"always_long", Long},
		{"random_matched", randomAction(p.intent)}, {"no_trade", NoTrade},
	}
	for _, entry := range actions {
		name, action := entry.name, entry.action
		s := r.report.Comparators[name]
		s.Add(action, long, short)
		r.report.Comparators[name] = s
	}
	s := r.report.RegimePolicy[p.intent.Regime]
	s.Add(p.intent.Action, long, short)
	r.report.RegimePolicy[p.intent.Regime] = s
	low := r.config
	low.AllowanceBpsPerSide = 0
	high := r.config
	high.AllowanceBpsPerSide = 10
	for _, variant := range []struct {
		cfg Config
		out *ResultStats
	}{{low, &r.report.PolicyCostLow}, {high, &r.report.PolicyCostHigh}} {
		l, err := EvaluateSide(p.entry, exit, p.quantity, Long, variant.cfg, holding)
		if err != nil {
			return err
		}
		variant.cfg.BorrowBpsPerDay = ResearchBorrowBpsPerDay
		h, err := EvaluateSide(p.entry, exit, p.quantity, Short, variant.cfg, holding)
		if err != nil {
			return err
		}
		variant.out.Add(p.intent.Action, l, h)
	}
	return nil
}

func (r *Research) Finalize() Report {
	r.report.Censored["RUN_END"] += len(r.pending)
	r.pending = nil
	if !r.report.FirstTick.IsZero() {
		r.report.CalendarMinutes = int(r.report.LastTick.Truncate(time.Minute).Sub(r.report.FirstTick.Truncate(time.Minute))/time.Minute) + 1
	}
	return r.report
}

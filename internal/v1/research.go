package v1

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
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

type BootstrapInterval struct {
	BlockMinutes         int     `json:"block_minutes"`
	Blocks               int     `json:"blocks"`
	MeanNetBpsPerEpisode float64 `json:"mean_net_bps_per_episode"`
	P05Bps               float64 `json:"p05_bps"`
	P95Bps               float64 `json:"p95_bps"`
}

type blockSum struct {
	net      float64
	episodes int
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
	SchemaVersion     int                               `json:"schema_version"`
	RunID             string                            `json:"run_id"`
	ConfigSHA256      string                            `json:"config_sha256"`
	FirstTick         time.Time                         `json:"first_tick"`
	LastTick          time.Time                         `json:"last_tick"`
	LastOrdinal       uint64                            `json:"last_ordinal,string"`
	CalendarMinutes   int                               `json:"calendar_minutes"`
	Decisions         int                               `json:"decisions"`
	EligibleDecisions int                               `json:"eligible_decisions"`
	PairedEpisodes    int                               `json:"paired_episodes"`
	Censored          map[string]int                    `json:"censored"`
	Reasons           map[string]int                    `json:"reasons"`
	RegimeCounts      map[string]int                    `json:"regime_counts"`
	Comparators       map[string]ResultStats            `json:"comparators"`
	FoldComparators   map[string]map[string]ResultStats `json:"fold_comparators"`
	RegimePolicy      map[string]ResultStats            `json:"regime_policy"`
	Uncertainty       map[string]BootstrapInterval      `json:"uncertainty"`
	PolicyCostLow     ResultStats                       `json:"policy_cost_allowance_0"`
	PolicyCostHigh    ResultStats                       `json:"policy_cost_allowance_10"`
	BorrowBpsPerDay   int                               `json:"research_short_borrow_bps_per_day"`
	UncertaintyNote   string                            `json:"uncertainty_note"`
	Limitations       []string                          `json:"limitations"`
}

type episode struct {
	intent     Intent
	quantity   float64
	entryAfter time.Time
	entry      book.Quote
	entryTime  time.Time
	exitAfter  time.Time
}

// PairedObservation exposes the already selected, side-correct hypothetical
// endpoints to a downstream research consumer. It is never fed to an engine.
type PairedObservation struct {
	Intent  Intent
	EntryAt time.Time
	ExitAt  time.Time
	Long    Outcome
	Short   Outcome
}

type Research struct {
	config              Config
	report              Report
	pending             []episode
	blocks              map[int64]map[string]blockSum
	lastDecisionOrdinal uint64
	pairedObserver      func(PairedObservation)
}

// ObservePaired adds a downstream-only observer. A nil observer preserves the
// accepted V1 report and intent behavior byte for byte.
func (r *Research) ObservePaired(fn func(PairedObservation)) {
	r.pairedObserver = fn
}

func NewResearch(runID string, config Config) (*Research, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	digest, err := config.Digest()
	if err != nil {
		return nil, err
	}
	return &Research{config: config, blocks: map[int64]map[string]blockSum{}, report: Report{SchemaVersion: SchemaVersion, RunID: runID,
		ConfigSHA256: digest, Censored: map[string]int{}, Reasons: map[string]int{},
		RegimeCounts: map[string]int{}, Comparators: map[string]ResultStats{}, FoldComparators: map[string]map[string]ResultStats{}, RegimePolicy: map[string]ResultStats{}, Uncertainty: map[string]BootstrapInterval{},
		BorrowBpsPerDay: ResearchBorrowBpsPerDay,
		UncertaintyNote: "Exploratory 30-minute UTC block bootstrap (1,000 deterministic resamples, 5th/95th percentiles) for mean net bps per paired eligible decision. Five-minute outcomes overlap; one day and dependent blocks do not establish durable edge.",
		Limitations:     []string{"Exploratory development tape; no sealed final period opened.", "Displayed depth and assumed taker costs are not fills or a current fee quote.", "Short comparator uses a hypothetical borrow scenario; spot data do not establish short feasibility."}}}, nil
}

func eligibleQuote(v book.View, q book.Quote, maxAge time.Duration) bool {
	return v.Health == book.Healthy && q.Generation != 0 && q.Generation == v.Generation && q.Epoch == v.Epoch &&
		q.LastBookOrdinal == v.LastBookOrdinal &&
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
	if (!r.report.LastTick.IsZero() && v.AsOf.Before(r.report.LastTick)) ||
		(r.report.LastOrdinal > 0 && v.Ordinal <= r.report.LastOrdinal) {
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
			if !v.AsOf.Before(p.entryAfter) && !q.LastBookAt.Before(p.entryAfter) {
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
		if v.AsOf.Before(p.exitAfter) || q.LastBookAt.Before(p.exitAfter) {
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
	if i.SchemaVersion != SchemaVersion || i.AsOfOrdinal == 0 || i.AsOfOrdinal <= r.lastDecisionOrdinal ||
		i.AsOfOrdinal != r.report.LastOrdinal || !i.AsOfTime.Equal(r.report.LastTick) ||
		i.DecisionID != fmt.Sprintf("%s:%d:v1", i.RunID, i.AsOfOrdinal) ||
		i.HorizonSeconds != r.config.HorizonSeconds ||
		(i.Action != Long && i.Action != Short && i.Action != NoTrade) {
		return fmt.Errorf("research decision order/schema mismatch at %d", i.AsOfOrdinal)
	}
	r.lastDecisionOrdinal = i.AsOfOrdinal
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
	if i.Action != Long && i.Action != Short {
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
	if r.pairedObserver != nil {
		r.pairedObserver(PairedObservation{Intent: p.intent, EntryAt: p.entryTime,
			ExitAt: exitTime, Long: long, Short: short})
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
		fold := int(p.intent.AsOfTime.Sub(r.report.FirstTick) / (6 * time.Hour))
		if fold < 0 {
			return fmt.Errorf("decision before first tick")
		}
		if fold > 3 {
			fold = 3
		}
		foldName := fmt.Sprintf("fold_%d", fold)
		if r.report.FoldComparators[foldName] == nil {
			r.report.FoldComparators[foldName] = map[string]ResultStats{}
		}
		fs := r.report.FoldComparators[foldName][name]
		fs.Add(action, long, short)
		r.report.FoldComparators[foldName][name] = fs
		blockID := p.intent.AsOfTime.UTC().Unix() / (30 * 60)
		if r.blocks[blockID] == nil {
			r.blocks[blockID] = map[string]blockSum{}
		}
		value := 0.0
		if action == Long {
			value = long.NetBps
		}
		if action == Short {
			value = short.NetBps
		}
		bs := r.blocks[blockID][name]
		bs.net += value
		bs.episodes++
		r.blocks[blockID][name] = bs
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
	r.bootstrap()
	return r.report
}

func (r *Research) bootstrap() {
	ids := make([]int64, 0, len(r.blocks))
	for id := range r.blocks {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) < 4 {
		return
	}
	for _, name := range []string{"policy", "momentum", "mean_reversion", "always_long", "random_matched", "no_trade"} {
		values := make([]float64, 1000)
		for iteration := range values {
			net, count := 0.0, 0
			for draw := range ids {
				var seed [16]byte
				binary.LittleEndian.PutUint64(seed[:8], uint64(iteration))
				binary.LittleEndian.PutUint64(seed[8:16], uint64(draw))
				h := sha256.Sum256(append(seed[:], name...))
				selected := ids[int(binary.LittleEndian.Uint64(h[:8])%uint64(len(ids)))]
				b := r.blocks[selected][name]
				net += b.net
				count += b.episodes
			}
			if count > 0 {
				values[iteration] = net / float64(count)
			}
		}
		sort.Float64s(values)
		mean := 0.0
		stats := r.report.Comparators[name]
		if stats.Episodes > 0 {
			mean = stats.SumNetBps / float64(stats.Episodes)
		}
		r.report.Uncertainty[name] = BootstrapInterval{BlockMinutes: 30, Blocks: len(ids), MeanNetBpsPerEpisode: mean,
			P05Bps: values[49], P95Bps: values[949]}
	}
}

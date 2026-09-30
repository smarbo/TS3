package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
	"ts3/internal/v1"
)

const SchemaVersion = 2
const micro = int64(1_000_000)

type Config struct {
	SchemaVersion            int `json:"schema_version"`
	TrendThresholdBps        int `json:"trend_threshold_bps"`
	ReversionThresholdBps    int `json:"reversion_threshold_bps"`
	ReversionTrendCeilingBps int `json:"reversion_trend_ceiling_bps"`
	PressureThresholdPPM     int `json:"pressure_threshold_ppm"`
	PressureWindowSeconds    int `json:"pressure_window_seconds"`
	PressureMinUpdates       int `json:"pressure_min_updates"`
	PressureMinSpanSeconds   int `json:"pressure_min_span_seconds"`
	OpportunityMarginBps     int `json:"opportunity_margin_bps"`
}

func DefaultConfig() Config {
	return Config{SchemaVersion: SchemaVersion, TrendThresholdBps: 10,
		ReversionThresholdBps: 10, ReversionTrendCeilingBps: 20,
		PressureThresholdPPM: 100_000, PressureWindowSeconds: 30,
		PressureMinUpdates: 10, PressureMinSpanSeconds: 20, OpportunityMarginBps: 10}
}

func (c Config) Validate() error {
	if c != DefaultConfig() {
		return errors.New("V2-001 permits only the frozen default configuration")
	}
	return nil
}

func (c Config) Digest() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	b, err := domain.CanonicalJSON(c)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

type Status string

const (
	Active      Status = "ACTIVE"
	Neutral     Status = "NEUTRAL"
	Unavailable Status = "UNAVAILABLE"
	Invalid     Status = "INVALID"
)

type SignalResult struct {
	ID                string    `json:"id"`
	Status            Status    `json:"status"`
	Direction         v1.Action `json:"direction"`
	Score             int64     `json:"score"`
	ScoreUnit         string    `json:"score_unit"`
	HorizonSeconds    int       `json:"horizon_seconds"`
	Reason            string    `json:"reason"`
	AsOfOrdinal       uint64    `json:"as_of_ordinal,string"`
	SourceBookOrdinal uint64    `json:"source_book_ordinal,string"`
	Generation        uint64    `json:"generation,string"`
}

type Evidence struct {
	PriceVote         v1.Action `json:"price_vote"`
	BookVote          v1.Action `json:"book_vote"`
	Direction         v1.Action `json:"direction"`
	Disagreement      string    `json:"disagreement"` // UNDEFINED, 0, or 1
	ActiveFamilies    int       `json:"active_families"`
	NeutralFamilies   int       `json:"neutral_families"`
	UnavailableFamily int       `json:"unavailable_families"`
	InvalidFamilies   int       `json:"invalid_families"`
	Reason            string    `json:"reason"`
}

type Opportunity struct {
	Available             bool   `json:"available"`
	ReferenceNotionalUSD  int    `json:"reference_notional_usd"`
	QuoteAgeMS            int64  `json:"quote_age_ms"`
	QuoteGeneration       uint64 `json:"quote_generation,string"`
	SpreadMicroBps        int64  `json:"spread_micro_bps"`
	FrictionMicroBps      int64  `json:"friction_micro_bps"`
	PastMoveProxyMicroBps int64  `json:"past_move_proxy_micro_bps"`
	MarginMicroBps        int64  `json:"margin_micro_bps"`
	PassesScreen          bool   `json:"passes_screen"`
	Reason                string `json:"reason"`
}

type Intent struct {
	SchemaVersion int             `json:"schema_version"`
	DecisionID    string          `json:"decision_id"`
	RunID         string          `json:"run_id"`
	AsOfOrdinal   uint64          `json:"as_of_ordinal,string"`
	AsOfTime      time.Time       `json:"as_of_time"`
	Action        v1.Action       `json:"action"`
	HorizonSecs   int             `json:"horizon_seconds"`
	ExpiresAt     time.Time       `json:"expires_at"`
	Baseline      v1.Intent       `json:"v1_baseline"`
	Signals       [3]SignalResult `json:"signals"`
	Evidence      Evidence        `json:"evidence"`
	Opportunity   Opportunity     `json:"opportunity"`
	ReasonCodes   []string        `json:"reason_codes"`
	ConfigSHA256  string          `json:"config_sha256"`
}

type pressureSample struct {
	at      time.Time
	ordinal uint64
	ppm     int64
}

type Engine struct {
	runID, configSHA string
	config           Config
	baseline         *v1.Engine
	pressure         []pressureSample
	lastBookOrdinal  uint64
	lastTick         time.Time
	generation       uint64
	previousMinute   time.Time
	previousReturn   int64
	previousGen      uint64
	timing           TimingSink
}

// TimingSink observes stage durations without returning clock values to the
// analytical engine. It cannot contribute to a canonical intent.
type TimingSink interface {
	Begin(stage string) func()
}

func (e *Engine) SetTimingSink(s TimingSink) { e.timing = s }

func (e *Engine) begin(stage string) func() {
	if e.timing == nil {
		return func() {}
	}
	return e.timing.Begin(stage)
}

func NewEngine(runID string, c Config) (*Engine, error) {
	if runID == "" {
		return nil, errors.New("missing run ID")
	}
	sha, err := c.Digest()
	if err != nil {
		return nil, err
	}
	b, err := v1.NewEngine(runID, v1.DefaultConfig())
	if err != nil {
		return nil, err
	}
	return &Engine{runID: runID, config: c, configSHA: sha, baseline: b}, nil
}

func (e *Engine) reset() {
	e.pressure = e.pressure[:0]
	e.lastBookOrdinal = 0
	e.previousMinute = time.Time{}
	e.previousReturn = 0
	e.previousGen = 0
}

func validQuote(v book.View, q book.Quote) bool {
	if len(q.Bids) == 0 || len(q.Asks) == 0 ||
		v.BestBid == "" || v.BestAsk == "" ||
		q.Bids[0].Price != v.BestBid || q.Asks[0].Price != v.BestAsk {
		return false
	}
	if _, err := domain.Decimal(v.BestBid, false); err != nil {
		return false
	}
	if _, err := domain.Decimal(v.BestAsk, false); err != nil {
		return false
	}
	bid, be := strconv.ParseFloat(v.BestBid, 64)
	ask, ae := strconv.ParseFloat(v.BestAsk, 64)
	if be != nil || ae != nil || math.IsNaN(bid) || math.IsNaN(ask) ||
		math.IsInf(bid, 0) || math.IsInf(ask, 0) || bid <= 0 || bid >= ask {
		return false
	}
	return v.Health == book.Healthy && q.Generation != 0 && q.Generation == v.Generation &&
		q.Epoch == v.Epoch && q.LastBookOrdinal == v.LastBookOrdinal &&
		q.LastBookOrdinal > 0 && q.LastBookOrdinal <= v.Ordinal &&
		!q.LastBookAt.IsZero() && !v.AsOf.Before(q.LastBookAt) &&
		v.AsOf.Sub(q.LastBookAt) <= 2*time.Second
}

func levelSize(s string) (float64, error) {
	if _, err := domain.Decimal(s, false); err != nil {
		return 0, err
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
		return 0, errors.New("invalid depth size")
	}
	return n, nil
}

func topFivePressure(q book.Quote) (int64, error) {
	if len(q.Bids) < 5 || len(q.Asks) < 5 {
		return 0, errors.New("fewer than five depth levels")
	}
	var bid, ask float64
	for n := 0; n < 5; n++ {
		b, err := levelSize(q.Bids[n].Size)
		if err != nil {
			return 0, err
		}
		a, err := levelSize(q.Asks[n].Size)
		if err != nil {
			return 0, err
		}
		bid += b
		ask += a
	}
	if bid+ask <= 0 || math.IsNaN(bid+ask) || math.IsInf(bid+ask, 0) {
		return 0, errors.New("invalid depth total")
	}
	return int64(math.Round((bid - ask) / (bid + ask) * float64(micro))), nil
}

func (e *Engine) samplePressure(v book.View, q book.Quote) error {
	if q.Generation != e.generation {
		e.reset()
		e.generation = q.Generation
	}
	if !e.lastTick.IsZero() && v.AsOf.Sub(e.lastTick) > 2*time.Second {
		e.reset()
	}
	if !e.lastTick.IsZero() && v.AsOf.Equal(e.lastTick) {
		return nil
	}
	e.lastTick = v.AsOf
	if !validQuote(v, q) {
		e.reset()
		return nil
	}
	if q.LastBookOrdinal != e.lastBookOrdinal {
		ppm, err := topFivePressure(q)
		if err != nil {
			e.reset()
			return err
		}
		e.pressure = append(e.pressure, pressureSample{at: v.AsOf, ordinal: q.LastBookOrdinal, ppm: ppm})
		e.lastBookOrdinal = q.LastBookOrdinal
	}
	start := v.AsOf.Add(-time.Duration(e.config.PressureWindowSeconds) * time.Second)
	drop := 0
	for drop < len(e.pressure) && !e.pressure[drop].at.After(start) {
		drop++
	}
	if drop > 0 {
		copy(e.pressure, e.pressure[drop:])
		e.pressure = e.pressure[:len(e.pressure)-drop]
	}
	if len(e.pressure) > 32 {
		e.pressure = e.pressure[len(e.pressure)-32:]
	}
	return nil
}

func blankSignal(id, unit string, horizon int, ordinal, generation uint64) SignalResult {
	return SignalResult{ID: id, Status: Unavailable, Direction: v1.Flat, ScoreUnit: unit,
		HorizonSeconds: horizon, Reason: "DEPENDENCY_UNAVAILABLE", AsOfOrdinal: ordinal, Generation: generation}
}

func (e *Engine) pressureSignal(v book.View, q book.Quote, sampleErr error) SignalResult {
	s := blankSignal("book_pressure.v2.1", "imbalance_ppm", 30, v.Ordinal, q.Generation)
	s.SourceBookOrdinal = q.LastBookOrdinal
	if sampleErr != nil {
		s.Status = Invalid
		s.Reason = "INVALID_DEPTH"
		return s
	}
	if len(e.pressure) < e.config.PressureMinUpdates ||
		v.AsOf.Sub(e.pressure[0].at) < time.Duration(e.config.PressureMinSpanSeconds)*time.Second {
		s.Reason = "PRESSURE_WARMUP"
		return s
	}
	var sum int64
	for _, sample := range e.pressure {
		sum += sample.ppm
	}
	s.Score = int64(math.Round(float64(sum) / float64(len(e.pressure))))
	s.Status = Neutral
	s.Reason = "BELOW_TRIGGER"
	s.Direction = direction(s.Score, int64(e.config.PressureThresholdPPM))
	if s.Direction != v1.Flat {
		s.Status = Active
		s.Reason = "PERSISTENT_DEPTH_PRESSURE"
	}
	return s
}

func direction(score, threshold int64) v1.Action {
	if score > threshold {
		return v1.Long
	}
	if score < -threshold {
		return v1.Short
	}
	return v1.Flat
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func baselineUnavailableReason(i v1.Intent) string {
	if len(i.ReasonCodes) == 0 {
		return "ANALYSIS_UNAVAILABLE"
	}
	switch i.ReasonCodes[0] {
	case "WARMUP":
		return "INSUFFICIENT_WARMUP"
	case "INVALID_DEPTH":
		return "INVALID_DEPTH"
	case "INVALID_COST":
		return "INVALID_COST"
	case "INVALID_QUOTE", "INVALID_FEATURE", "INVALID_WINDOW":
		return "SIGNAL_INVALID"
	case "TICK_TIME_TIE":
		return "TICK_TIME_TIE"
	default:
		return "ANALYSIS_UNAVAILABLE"
	}
}

// ApplyTick has one serialized owner and receives only the current applied tick.
// The V1 engine is unchanged and remains independently reproducible.
func (e *Engine) ApplyTick(v book.View, q book.Quote) (*Intent, error) {
	endBaseline := e.begin("v1_baseline")
	baseline, err := e.baseline.ApplyTick(v, q)
	endBaseline()
	if err != nil {
		return nil, err
	}
	endPressure := e.begin("book_pressure")
	pressureErr := e.samplePressure(v, q)
	endPressure()
	if baseline == nil {
		return nil, nil
	}
	i := &Intent{SchemaVersion: SchemaVersion, DecisionID: fmt.Sprintf("%s:%d:v2", e.runID, v.Ordinal),
		RunID: e.runID, AsOfOrdinal: v.Ordinal, AsOfTime: v.AsOf,
		Action: v1.NoTrade, HorizonSecs: baseline.HorizonSeconds, ExpiresAt: baseline.ExpiresAt,
		Baseline: *baseline, ConfigSHA256: e.configSHA}
	i.Signals = [3]SignalResult{
		blankSignal("trend.v2.1", "micro_bps", 300, v.Ordinal, q.Generation),
		blankSignal("reversion.v2.1", "micro_bps", 300, v.Ordinal, q.Generation),
		blankSignal("book_pressure.v2.1", "imbalance_ppm", 30, v.Ordinal, q.Generation),
	}
	for n := range i.Signals {
		i.Signals[n].SourceBookOrdinal = q.LastBookOrdinal
	}
	i.Evidence = Evidence{PriceVote: v1.Flat, BookVote: v1.Flat, Direction: v1.Flat, Disagreement: "UNDEFINED"}
	i.Opportunity = Opportunity{ReferenceNotionalUSD: baseline.ReferenceNotionalUSD,
		QuoteAgeMS: baseline.QuoteAgeMS, QuoteGeneration: q.Generation,
		MarginMicroBps: int64(e.config.OpportunityMarginBps) * micro}
	if !validQuote(v, q) {
		e.reset()
		i.Evidence = aggregate(i.Signals)
		i.ReasonCodes = []string{"DATA_UNHEALTHY"}
		return i, nil
	}
	i.Signals[2] = e.pressureSignal(v, q, pressureErr)
	if baseline.Features == nil {
		i.Evidence = aggregate(i.Signals)
		primary := baselineUnavailableReason(*baseline)
		if primary == "SIGNAL_INVALID" {
			for n := 0; n < 2; n++ {
				i.Signals[n].Status = Invalid
				i.Signals[n].Reason = primary
			}
			if len(baseline.ReasonCodes) > 0 && baseline.ReasonCodes[0] == "INVALID_QUOTE" {
				i.Signals[2].Status = Invalid
				i.Signals[2].Direction = v1.Flat
				i.Signals[2].Reason = primary
			}
			i.Evidence = aggregate(i.Signals)
		}
		i.ReasonCodes = []string{primary}
		if len(baseline.ReasonCodes) > 0 {
			if baseline.ReasonCodes[0] != primary {
				i.ReasonCodes = append(i.ReasonCodes, baseline.ReasonCodes[0])
			}
		}
		return i, nil
	}
	f := baseline.Features
	i.Opportunity.SpreadMicroBps = f.SpreadMicroBps
	if baseline.Regime == "WIDE" {
		e.reset()
		for n := range i.Signals {
			i.Signals[n].Status = Unavailable
			i.Signals[n].Direction = v1.Flat
			i.Signals[n].Score = 0
			i.Signals[n].Reason = "WIDE_REGIME"
		}
		i.Evidence = aggregate(i.Signals)
		i.ReasonCodes = []string{"REGIME_CONFLICT"}
		if !baseline.Eligible {
			i.ReasonCodes = []string{baselineUnavailableReason(*baseline)}
			i.Opportunity.Reason = i.ReasonCodes[0]
		}
		return i, nil
	}
	minute := v.AsOf.UTC().Truncate(time.Minute)
	endTrend := e.begin("trend")
	trend := &i.Signals[0]
	if !e.previousMinute.IsZero() && e.previousMinute.Equal(minute.Add(-time.Minute)) && e.previousGen == q.Generation {
		trend.Status = Neutral
		trend.Reason = "BELOW_TRIGGER_OR_NO_PERSISTENCE"
		trend.Score = f.Return60MicroBps
		if direction(f.Return60MicroBps, int64(e.config.TrendThresholdBps)*micro) != v1.Flat &&
			(f.Return60MicroBps > 0) == (e.previousReturn > 0) && e.previousReturn != 0 {
			trend.Status = Active
			trend.Direction = direction(f.Return60MicroBps, int64(e.config.TrendThresholdBps)*micro)
			trend.Reason = "PERSISTENT_RETURN"
		}
	} else {
		trend.Reason = "PREVIOUS_MINUTE_UNAVAILABLE"
	}
	e.previousMinute, e.previousReturn, e.previousGen = minute, f.Return60MicroBps, q.Generation
	endTrend()
	endReversion := e.begin("reversion")
	reversion := &i.Signals[1]
	reversion.Status = Neutral
	reversion.Score = -f.DisplacementMicroBps
	reversion.Reason = "BELOW_TRIGGER"
	if abs(f.DisplacementMicroBps) > int64(e.config.ReversionThresholdBps)*micro {
		if abs(f.Return60MicroBps) > int64(e.config.ReversionTrendCeilingBps)*micro {
			reversion.Reason = "TREND_SUPPRESSED"
		} else {
			reversion.Status = Active
			reversion.Direction = direction(-f.DisplacementMicroBps, int64(e.config.ReversionThresholdBps)*micro)
			reversion.Reason = "DISPLACEMENT"
		}
	}
	endReversion()
	endAggregate := e.begin("aggregate")
	i.Evidence = aggregate(i.Signals)
	endAggregate()
	if !baseline.Eligible {
		primary := baselineUnavailableReason(*baseline)
		i.ReasonCodes = []string{primary}
		i.Opportunity.Reason = primary
		return i, nil
	}
	if i.Evidence.Reason != "CONSENSUS" {
		i.ReasonCodes = []string{i.Evidence.Reason}
		return i, nil
	}
	proxy := abs(f.Return60MicroBps)
	if i.Signals[0].Status != Active && i.Signals[1].Status == Active {
		proxy = abs(f.DisplacementMicroBps)
	} else if i.Signals[0].Status == Active && i.Signals[1].Status == Active && abs(f.DisplacementMicroBps) > proxy {
		proxy = abs(f.DisplacementMicroBps)
	}
	i.Opportunity.PastMoveProxyMicroBps = proxy
	endOpportunity := e.begin("opportunity")
	friction, err := v1.CurrentRoundTripFriction(q, float64(baseline.ReferenceNotionalUSD)/f.MidUSD, v1.DefaultConfig())
	endOpportunity()
	if err != nil {
		i.Opportunity.Reason = "INVALID_DEPTH"
		i.ReasonCodes = []string{"INVALID_DEPTH"}
		return i, nil
	}
	i.Opportunity.FrictionMicroBps = int64(math.Round(friction * float64(micro)))
	i.Opportunity.Available = true
	if proxy <= i.Opportunity.FrictionMicroBps+i.Opportunity.MarginMicroBps {
		i.Opportunity.Reason = "COST_EXCEEDS_EDGE"
		i.ReasonCodes = []string{"COST_EXCEEDS_EDGE"}
		return i, nil
	}
	i.Opportunity.PassesScreen = true
	if i.Evidence.Direction == v1.Short && v1.DefaultConfig().ShortMechanism == "" {
		i.Opportunity.Reason = "SHORT_FEASIBILITY_UNKNOWN"
		i.ReasonCodes = []string{"SHORT_FEASIBILITY_UNKNOWN"}
		return i, nil
	}
	i.Opportunity.Reason = "SCREEN_PASSED"
	i.Action = i.Evidence.Direction
	i.ReasonCodes = []string{"MULTI_SIGNAL_CONSENSUS"}
	return i, nil
}

func aggregate(signals [3]SignalResult) Evidence {
	e := Evidence{PriceVote: v1.Flat, BookVote: v1.Flat, Direction: v1.Flat, Disagreement: "UNDEFINED"}
	for _, s := range signals {
		switch s.Status {
		case Active:
			e.ActiveFamilies++
		case Neutral:
			e.NeutralFamilies++
		case Unavailable:
			e.UnavailableFamily++
		case Invalid:
			e.InvalidFamilies++
		}
	}
	if e.InvalidFamilies > 0 {
		e.Reason = "SIGNAL_INVALID"
		return e
	}
	a, b, p := signals[0], signals[1], signals[2]
	if a.Status == Active && b.Status == Active && a.Direction != b.Direction {
		e.Disagreement = "1"
		e.Reason = "SIGNAL_DISAGREEMENT"
		return e
	}
	if a.Status == Active {
		e.PriceVote = a.Direction
	} else if b.Status == Active {
		e.PriceVote = b.Direction
	}
	if p.Status == Active {
		e.BookVote = p.Direction
	}
	if e.PriceVote == v1.Flat && e.BookVote == v1.Flat {
		e.Reason = "NO_SUPPORTED_DIRECTION"
		return e
	}
	if e.PriceVote == v1.Flat || e.BookVote == v1.Flat {
		e.Reason = "INSUFFICIENT_SIGNAL_DIVERSITY"
		return e
	}
	if e.PriceVote != e.BookVote {
		e.Disagreement = "1"
		e.Reason = "SIGNAL_DISAGREEMENT"
		return e
	}
	e.Disagreement = "0"
	e.Direction = e.PriceVote
	e.Reason = "CONSENSUS"
	return e
}

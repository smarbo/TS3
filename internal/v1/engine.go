package v1

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
)

const SchemaVersion = 1
const micro = 1_000_000

type Action string

const (
	Long    Action = "LONG"
	Short   Action = "SHORT"
	NoTrade Action = "NO_TRADE"
	Flat    Action = "FLAT"
)

type Config struct {
	SchemaVersion        int    `json:"schema_version"`
	HorizonSeconds       int    `json:"horizon_seconds"`
	QuoteMaxAgeMS        int    `json:"quote_max_age_ms"`
	ReferenceNotionalUSD int    `json:"reference_notional_usd"`
	FeeBpsPerSide        int    `json:"fee_bps_per_side"`
	AllowanceBpsPerSide  int    `json:"allowance_bps_per_side"`
	MarginBps            int    `json:"margin_bps"`
	SignalThresholdBps   int    `json:"signal_threshold_bps"`
	ShortMechanism       string `json:"short_mechanism,omitempty"`
	BorrowBpsPerDay      int    `json:"borrow_bps_per_day,omitempty"`
}

func DefaultConfig() Config {
	return Config{SchemaVersion: SchemaVersion, HorizonSeconds: 300, QuoteMaxAgeMS: 2000,
		ReferenceNotionalUSD: 100, FeeBpsPerSide: 20, AllowanceBpsPerSide: 5,
		MarginBps: 10, SignalThresholdBps: 10}
}

func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion || c.HorizonSeconds != 300 || c.QuoteMaxAgeMS != 2000 ||
		c.ReferenceNotionalUSD <= 0 || c.FeeBpsPerSide < 0 || c.AllowanceBpsPerSide < 0 ||
		c.MarginBps < 0 || c.SignalThresholdBps <= 0 || c.BorrowBpsPerDay < 0 {
		return errors.New("invalid V1 configuration")
	}
	if c.ShortMechanism == "" && c.BorrowBpsPerDay != 0 {
		return errors.New("borrow cost without short mechanism")
	}
	return nil
}

func (c Config) Digest() (string, error) {
	b, err := domain.CanonicalJSON(c)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

type Features struct {
	Version              string  `json:"version"`
	MidUSD               float64 `json:"mid_usd"`
	SpreadMicroBps       int64   `json:"spread_micro_bps"`
	Return60MicroBps     int64   `json:"return_60_micro_bps"`
	DisplacementMicroBps int64   `json:"displacement_300_micro_bps"`
	RMS900MicroBps       int64   `json:"rms_900_micro_bps_sqrt_s"`
}

type Signal struct {
	Direction    Action `json:"direction"`
	ScoreMicroBP int64  `json:"score_micro_bps"`
}

type Intent struct {
	SchemaVersion        int         `json:"schema_version"`
	DecisionID           string      `json:"decision_id"`
	RunID                string      `json:"run_id"`
	Instrument           string      `json:"instrument"`
	AsOfOrdinal          uint64      `json:"as_of_ordinal,string"`
	AsOfTime             time.Time   `json:"as_of_time"`
	Action               Action      `json:"action"`
	HorizonSeconds       int         `json:"horizon_seconds"`
	ExpiresAt            time.Time   `json:"expires_at"`
	QuoteGeneration      uint64      `json:"quote_generation,string"`
	QuoteAsOfOrdinal     uint64      `json:"quote_as_of_ordinal,string"`
	QuoteAgeMS           int64       `json:"quote_age_ms"`
	BestBid              string      `json:"best_bid,omitempty"`
	BestAsk              string      `json:"best_ask,omitempty"`
	ReferenceNotionalUSD int         `json:"reference_notional_usd"`
	FeeBpsPerSide        int         `json:"fee_bps_per_side"`
	AllowanceBpsPerSide  int         `json:"allowance_bps_per_side"`
	FrictionMicroBps     int64       `json:"friction_micro_bps,omitempty"`
	Health               book.Health `json:"health"`
	Regime               string      `json:"regime"`
	Features             *Features   `json:"features,omitempty"`
	Momentum             Signal      `json:"momentum"`
	MeanReversion        Signal      `json:"mean_reversion"`
	Eligible             bool        `json:"eligible"`
	ReasonCodes          []string    `json:"reason_codes"`
	ConfigSHA256         string      `json:"config_sha256"`
}

type sample struct {
	mid    float64
	retBps float64
}

type Engine struct {
	runID, configSHA string
	config           Config
	lastOrdinal      uint64
	lastTickTime     time.Time
	lastTime         time.Time
	lastMinute       time.Time
	generation       uint64
	window           [901]sample
	count, next      int
}

func NewEngine(runID string, config Config) (*Engine, error) {
	if runID == "" {
		return nil, errors.New("missing run ID")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	digest, err := config.Digest()
	if err != nil {
		return nil, err
	}
	return &Engine{runID: runID, config: config, configSHA: digest}, nil
}

func (e *Engine) reset() {
	e.count = 0
	e.next = 0
	e.lastTime = time.Time{}
}

func (e *Engine) add(mid float64) {
	r := 0.0
	if e.count > 0 {
		r = 10000 * math.Log(mid/e.ago(0).mid)
	}
	e.window[e.next] = sample{mid: mid, retBps: r}
	e.next = (e.next + 1) % len(e.window)
	if e.count < len(e.window) {
		e.count++
	}
}

func (e *Engine) ago(n int) sample {
	return e.window[(e.next-1-n+len(e.window))%len(e.window)]
}

func scaled(bps float64) (int64, error) {
	if math.IsNaN(bps) || math.IsInf(bps, 0) || math.Abs(bps) > 1e8 {
		return 0, errors.New("nonfinite or unreasonable bps")
	}
	return int64(math.Round(bps * micro)), nil
}

func decimal(s string) (float64, error) {
	if _, err := domain.Decimal(s, false); err != nil {
		return 0, err
	}
	x, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(x, 0) || math.IsNaN(x) || x <= 0 {
		return 0, errors.New("invalid numeric quote")
	}
	return x, nil
}

// ApplyTick is called once, and only once, after the shared V0 processor has
// applied a recorded ClockTick. It never reads a clock or future record.
func (e *Engine) ApplyTick(v book.View, q book.Quote) (*Intent, error) {
	if v.Ordinal <= e.lastOrdinal || (!e.lastTickTime.IsZero() && v.AsOf.Before(e.lastTickTime)) {
		return nil, fmt.Errorf("nonmonotone V1 tick %d", v.Ordinal)
	}
	e.lastOrdinal = v.Ordinal
	e.lastTickTime = v.AsOf
	minute := v.AsOf.UTC().Truncate(time.Minute)
	emit := !minute.Equal(e.lastMinute)
	if emit {
		e.lastMinute = minute
	}
	i := &Intent{SchemaVersion: SchemaVersion, DecisionID: fmt.Sprintf("%s:%d:v1", e.runID, v.Ordinal),
		RunID: e.runID, Instrument: "kraken-spot:BTC/USD", AsOfOrdinal: v.Ordinal, AsOfTime: v.AsOf,
		Action: NoTrade, HorizonSeconds: e.config.HorizonSeconds, ExpiresAt: v.AsOf.Add(time.Duration(e.config.HorizonSeconds) * time.Second),
		QuoteGeneration: q.Generation, QuoteAsOfOrdinal: q.LastBookOrdinal, BestBid: v.BestBid, BestAsk: v.BestAsk,
		ReferenceNotionalUSD: e.config.ReferenceNotionalUSD, FeeBpsPerSide: e.config.FeeBpsPerSide,
		AllowanceBpsPerSide: e.config.AllowanceBpsPerSide, Health: v.Health, Regime: "UNKNOWN",
		Momentum: Signal{Direction: Flat}, MeanReversion: Signal{Direction: Flat}, ConfigSHA256: e.configSHA}
	if q.Generation != e.generation {
		e.reset()
		e.generation = q.Generation
	}
	if v.Health != book.Healthy || q.Generation == 0 || q.Generation != v.Generation || q.Epoch != v.Epoch || q.LastBookOrdinal != v.LastBookOrdinal {
		e.reset()
		i.ReasonCodes = []string{"DATA_UNHEALTHY"}
	} else if q.LastBookAt.IsZero() || v.AsOf.Before(q.LastBookAt) || v.AsOf.Sub(q.LastBookAt) > time.Duration(e.config.QuoteMaxAgeMS)*time.Millisecond {
		e.reset()
		i.ReasonCodes = []string{"QUOTE_STALE"}
	} else if v.BestBid == "" || v.BestAsk == "" || len(q.Bids) == 0 || len(q.Asks) == 0 {
		e.reset()
		i.ReasonCodes = []string{"INVALID_QUOTE"}
	} else {
		bid, be := decimal(v.BestBid)
		ask, ae := decimal(v.BestAsk)
		if be != nil || ae != nil || bid >= ask {
			e.reset()
			i.ReasonCodes = []string{"INVALID_QUOTE"}
		} else {
			if !e.lastTime.IsZero() && v.AsOf.Equal(e.lastTime) {
				i.ReasonCodes = []string{"TICK_TIME_TIE"}
				if !emit {
					return nil, nil
				}
				return i, nil
			}
			if !e.lastTime.IsZero() && v.AsOf.Sub(e.lastTime) > 2*time.Second {
				e.reset()
			}
			mid := (bid + ask) / 2
			e.add(mid)
			e.lastTime = v.AsOf
			i.QuoteAgeMS = v.AsOf.Sub(q.LastBookAt).Milliseconds()
			if e.count < len(e.window) {
				i.ReasonCodes = []string{"WARMUP"}
			} else {
				features, err := e.features(mid, bid, ask)
				if err != nil {
					e.reset()
					i.ReasonCodes = []string{"INVALID_FEATURE"}
				} else {
					i.Features = &features
					i.Regime = regime(features)
					i.Momentum = direction(features.Return60MicroBps, e.config.SignalThresholdBps)
					i.MeanReversion = direction(-features.DisplacementMicroBps, e.config.SignalThresholdBps)
					friction, err := CurrentRoundTripFriction(q, float64(e.config.ReferenceNotionalUSD)/mid, e.config)
					if err != nil {
						i.ReasonCodes = []string{"INVALID_DEPTH"}
					} else {
						i.FrictionMicroBps, err = scaled(friction)
						if err != nil {
							i.ReasonCodes = []string{"INVALID_COST"}
						} else {
							i.Eligible = true
							e.decide(i)
						}
					}
				}
			}
		}
	}
	if !emit {
		return nil, nil
	}
	return i, nil
}

func (e *Engine) features(mid, bid, ask float64) (Features, error) {
	var f Features
	f.Version = "tick-bbo-v1"
	f.MidUSD = mid
	var err error
	f.SpreadMicroBps, err = scaled(10000 * (ask - bid) / mid)
	if err != nil {
		return Features{}, err
	}
	f.Return60MicroBps, err = scaled(10000 * math.Log(mid/e.ago(60).mid))
	if err != nil {
		return Features{}, err
	}
	sum := 0.0
	for n := 0; n < 300; n++ {
		sum += e.ago(n).mid
	}
	f.DisplacementMicroBps, err = scaled(10000 * math.Log(mid/(sum/300)))
	if err != nil {
		return Features{}, err
	}
	squares := 0.0
	for n := 0; n < 900; n++ {
		r := e.ago(n).retBps
		squares += r * r
	}
	f.RMS900MicroBps, err = scaled(math.Sqrt(squares / 900))
	return f, err
}

func regime(f Features) string {
	if f.SpreadMicroBps > 10*micro {
		return "WIDE"
	}
	if f.RMS900MicroBps > micro {
		return "VOLATILE"
	}
	return "NORMAL"
}

func direction(score int64, threshold int) Signal {
	s := Signal{Direction: Flat, ScoreMicroBP: score}
	if score > int64(threshold)*micro {
		s.Direction = Long
	}
	if score < -int64(threshold)*micro {
		s.Direction = Short
	}
	return s
}

func (e *Engine) decide(i *Intent) {
	i.Action = NoTrade
	if i.Momentum.Direction == Flat {
		i.ReasonCodes = []string{"FLAT_SIGNAL"}
		return
	}
	strength := i.Momentum.ScoreMicroBP
	if strength < 0 {
		strength = -strength
	}
	if strength <= i.FrictionMicroBps+int64(e.config.MarginBps)*micro {
		i.ReasonCodes = []string{"INSUFFICIENT_EDGE"}
		return
	}
	if i.Momentum.Direction == Short && e.config.ShortMechanism == "" {
		i.ReasonCodes = []string{"SHORT_FEASIBILITY_UNKNOWN"}
		return
	}
	if i.Momentum.Direction == Short {
		borrow := float64(e.config.BorrowBpsPerDay) * float64(e.config.HorizonSeconds) / 86400
		borrowMicro, err := scaled(borrow)
		if err != nil || strength <= i.FrictionMicroBps+borrowMicro+int64(e.config.MarginBps)*micro {
			i.ReasonCodes = []string{"INSUFFICIENT_EDGE"}
			return
		}
	}
	i.Action = i.Momentum.Direction
	i.ReasonCodes = []string{"BASELINE_MOMENTUM"}
}

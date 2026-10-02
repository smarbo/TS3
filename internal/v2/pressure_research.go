package v2

import (
	"crypto/sha256"
	"time"

	"ts3/internal/book"
	"ts3/internal/v1"
)

// pressureEpisode is a separate, research-only 30-second endpoint study. It
// cannot feed feature, signal or policy state and shares V1's quote convention.
type pressureEpisode struct {
	intent     Intent
	quantity   float64
	entryAfter time.Time
	entry      book.Quote
	entryTime  time.Time
	exitAfter  time.Time
}

func (r *Research) addPressureDecision(i Intent) {
	if i.Baseline.Features == nil || i.Baseline.Features.MidUSD <= 0 {
		return
	}
	r.report.Pressure30Eligible++
	r.pressurePending = append(r.pressurePending, pressureEpisode{intent: i,
		quantity:   float64(v1.DefaultConfig().ReferenceNotionalUSD) / i.Baseline.Features.MidUSD,
		entryAfter: i.AsOfTime.Add(2 * time.Second)})
}

func (r *Research) observePressureTick(v book.View, q book.Quote) {
	if !r.lastPressureTick.IsZero() && v.AsOf.Sub(r.lastPressureTick) > 2*time.Second {
		r.report.Pressure30Censored["TICK_GAP"] += len(r.pressurePending)
		r.pressurePending = nil
	}
	r.lastPressureTick = v.AsOf
	good := validQuote(v, q)
	keep := r.pressurePending[:0]
	for _, p := range r.pressurePending {
		if q.Generation != p.intent.Baseline.QuoteGeneration || q.Epoch == 0 {
			r.report.Pressure30Censored["GENERATION_CHANGED"]++
			continue
		}
		if !good {
			r.report.Pressure30Censored["UNHEALTHY_PATH"]++
			continue
		}
		if p.entryTime.IsZero() {
			if v.AsOf.After(p.entryAfter.Add(2 * time.Second)) {
				r.report.Pressure30Censored["ENTRY_MISSING"]++
				continue
			}
			if !v.AsOf.Before(p.entryAfter) && !q.LastBookAt.Before(p.entryAfter) {
				p.entry = q
				p.entryTime = v.AsOf
				p.exitAfter = v.AsOf.Add(30 * time.Second)
			}
			keep = append(keep, p)
			continue
		}
		if v.AsOf.After(p.exitAfter.Add(2 * time.Second)) {
			r.report.Pressure30Censored["EXIT_MISSING"]++
			continue
		}
		if v.AsOf.Before(p.exitAfter) || q.LastBookAt.Before(p.exitAfter) {
			keep = append(keep, p)
			continue
		}
		if err := r.completePressure(p, q, v.AsOf); err != nil {
			r.report.Pressure30Censored["DEPTH_MISSING"]++
		}
	}
	r.pressurePending = keep
}

func pressureRandomAction(i Intent) v1.Action {
	if i.Signals[2].Status != Active {
		return v1.NoTrade
	}
	h := sha256.Sum256([]byte(i.DecisionID))
	if h[0]&1 == 0 {
		return v1.Long
	}
	return v1.Short
}

func (r *Research) completePressure(p pressureEpisode, exit book.Quote, at time.Time) error {
	cfg := v1.DefaultConfig()
	holding := at.Sub(p.entryTime)
	long, err := v1.EvaluateSide(p.entry, exit, p.quantity, v1.Long, cfg, holding)
	if err != nil {
		return err
	}
	cfg.BorrowBpsPerDay = v1.ResearchBorrowBpsPerDay
	short, err := v1.EvaluateSide(p.entry, exit, p.quantity, v1.Short, cfg, holding)
	if err != nil {
		return err
	}
	r.report.Pressure30Paired++
	pressureAction := v1.NoTrade
	if p.intent.Signals[2].Status == Active {
		pressureAction = p.intent.Signals[2].Direction
	}
	for _, comparator := range []struct {
		name   string
		action v1.Action
	}{
		{"pressure", pressureAction}, {"v1_policy", p.intent.Baseline.Action},
		{"v2_policy", p.intent.Action}, {"always_long", v1.Long},
		{"random_matched", pressureRandomAction(p.intent)}, {"no_trade", v1.NoTrade},
	} {
		pairedAdd(r.report.Pressure30Controls, comparator.name, comparator.action, long, short)
	}
	return nil
}

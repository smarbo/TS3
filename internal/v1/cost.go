package v1

import (
	"errors"
	"math"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
)

var ErrInsufficientDepth = errors.New("insufficient displayed depth")

// Sweep returns the quote-currency notional needed to buy, or received from
// selling, a fixed base quantity through the displayed side of the book.
func Sweep(levels []domain.Level, quantity float64) (float64, error) {
	if !(quantity > 0) || math.IsInf(quantity, 0) || math.IsNaN(quantity) {
		return 0, errors.New("invalid reference quantity")
	}
	remaining := quantity
	notional := 0.0
	lastPrice := 0.0
	for _, level := range levels {
		price, err := decimal(level.Price)
		if err != nil {
			return 0, err
		}
		size, err := decimal(level.Size)
		if err != nil {
			return 0, err
		}
		if lastPrice > 0 && price == lastPrice {
			return 0, errors.New("duplicate depth price")
		}
		lastPrice = price
		take := math.Min(remaining, size)
		notional += take * price
		remaining -= take
		if remaining <= quantity*1e-12 {
			return notional, nil
		}
	}
	return 0, ErrInsufficientDepth
}

func quoteMid(q book.Quote) (float64, error) {
	if len(q.Bids) == 0 || len(q.Asks) == 0 {
		return 0, errors.New("empty quote")
	}
	b, err := decimal(q.Bids[0].Price)
	if err != nil {
		return 0, err
	}
	a, err := decimal(q.Asks[0].Price)
	if err != nil {
		return 0, err
	}
	if b >= a {
		return 0, errors.New("crossed quote")
	}
	return (b + a) / 2, nil
}

func CurrentRoundTripFriction(q book.Quote, quantity float64, c Config) (float64, error) {
	mid, err := quoteMid(q)
	if err != nil {
		return 0, err
	}
	ask, err := Sweep(q.Asks, quantity)
	if err != nil {
		return 0, err
	}
	bid, err := Sweep(q.Bids, quantity)
	if err != nil {
		return 0, err
	}
	rate := float64(c.FeeBpsPerSide+c.AllowanceBpsPerSide) / 10000
	loss := ask*(1+rate) - bid*(1-rate)
	return loss / (quantity * mid) * 10000, nil
}

// Outcome is a hypothetical quote-based result, never a claimed fill.
type Outcome struct {
	GrossMidBps  float64
	NetBps       float64
	DepthBps     float64
	FeeBps       float64
	AllowanceBps float64
	BorrowBps    float64
}

func EvaluateSide(entry, exit book.Quote, quantity float64, direction Action, c Config, holding time.Duration) (Outcome, error) {
	if direction != Long && direction != Short {
		return Outcome{}, errors.New("invalid outcome direction")
	}
	entryMid, err := quoteMid(entry)
	if err != nil {
		return Outcome{}, err
	}
	exitMid, err := quoteMid(exit)
	if err != nil {
		return Outcome{}, err
	}
	var entryNotional, exitNotional float64
	if direction == Long {
		entryNotional, err = Sweep(entry.Asks, quantity)
		if err != nil {
			return Outcome{}, err
		}
		exitNotional, err = Sweep(exit.Bids, quantity)
	} else {
		entryNotional, err = Sweep(entry.Bids, quantity)
		if err != nil {
			return Outcome{}, err
		}
		exitNotional, err = Sweep(exit.Asks, quantity)
	}
	if err != nil {
		return Outcome{}, err
	}
	base := quantity * entryMid
	orientation := 1.0
	if direction == Short {
		orientation = -1
	}
	gross := orientation * (exitMid - entryMid) / entryMid * 10000
	depth := orientation * (exitNotional - entryNotional) / base * 10000
	fee := float64(c.FeeBpsPerSide) / 10000 * (entryNotional + exitNotional) / base * 10000
	allowance := float64(c.AllowanceBpsPerSide) / 10000 * (entryNotional + exitNotional) / base * 10000
	borrow := 0.0
	if direction == Short {
		borrow = float64(c.BorrowBpsPerDay) * holding.Hours() / 24
	}
	return Outcome{GrossMidBps: gross, NetBps: depth - fee - allowance - borrow,
		DepthBps: depth, FeeBps: fee, AllowanceBps: allowance, BorrowBps: borrow}, nil
}

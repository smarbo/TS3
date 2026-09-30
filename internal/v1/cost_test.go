package v1

import (
	"math"
	"testing"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
)

func depth(bids, asks []domain.Level) book.Quote { return book.Quote{Bids: bids, Asks: asks} }
func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %.8f want %.8f", got, want)
	}
}

func TestSideSpecificCostAndNoDoubleSpread(t *testing.T) {
	c := DefaultConfig()
	entry := depth([]domain.Level{{Price: "99", Size: "2"}}, []domain.Level{{Price: "101", Size: "2"}})
	exit := depth([]domain.Level{{Price: "102", Size: "2"}}, []domain.Level{{Price: "104", Size: "2"}})
	friction, err := CurrentRoundTripFriction(entry, 1, c)
	if err != nil {
		t.Fatal(err)
	}
	near(t, friction, 250)
	long, err := EvaluateSide(entry, exit, 1, Long, c, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	near(t, long.GrossMidBps, 300)
	near(t, long.DepthBps, 100)
	near(t, long.FeeBps, 40.6)
	near(t, long.AllowanceBps, 10.15)
	near(t, long.NetBps, 49.25)
	short, err := EvaluateSide(entry, exit, 1, Short, c, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	near(t, short.GrossMidBps, -300)
	near(t, short.NetBps, -550.75)
	c.BorrowBpsPerDay = 10
	shortWithBorrow, err := EvaluateSide(entry, exit, 1, Short, c, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	near(t, shortWithBorrow.BorrowBps, 10*5.0/1440.0)
	near(t, shortWithBorrow.NetBps, short.NetBps-10*5.0/1440.0)
}

func TestDepthSweepAndCensoring(t *testing.T) {
	levels := []domain.Level{{Price: "100", Size: "0.25"}, {Price: "101", Size: "0.75"}}
	n, err := Sweep(levels, 1)
	if err != nil {
		t.Fatal(err)
	}
	near(t, n, 100.75)
	if _, err := Sweep(levels, 1.01); err != ErrInsufficientDepth {
		t.Fatalf("expected depth error, got %v", err)
	}
}

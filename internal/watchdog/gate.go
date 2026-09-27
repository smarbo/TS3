package watchdog

import (
	"sync/atomic"
	"time"

	"ts3/internal/book"
)

// Gate separates monotonic live availability from replayable analytical health.
// The state owner calls Applied/Progress; a supervised watchdog calls Check.
type Gate struct {
	open, armed, tripped, tickSeen atomic.Bool
	lastTick, lastProgress         atomic.Int64
}

func (g *Gate) Applied(health book.Health, tick bool, elapsed time.Duration) (newlyArmed bool) {
	if tick {
		g.lastTick.Store(elapsed.Nanoseconds())
		g.tickSeen.Store(true)
	}
	if health == book.Healthy && g.tickSeen.Load() {
		newlyArmed = g.armed.CompareAndSwap(false, true)
	}
	g.open.Store(health == book.Healthy && g.tickSeen.Load() && !g.tripped.Load())
	return newlyArmed
}
func (g *Gate) Progress(elapsed time.Duration) { g.lastProgress.Store(elapsed.Nanoseconds()) }
func (g *Gate) Open() bool                     { return g.open.Load() && !g.tripped.Load() }
func (g *Gate) Tripped() bool                  { return g.tripped.Load() }
func (g *Gate) Fail() {
	g.tripped.Store(true)
	g.open.Store(false)
}

// Check returns true exactly once when a previously armed gate exceeds the
// two-second bound. The caller records an incident and cancels intake.
func (g *Gate) Check(elapsed time.Duration) (tripped bool, tickLag, progressLag time.Duration) {
	if !g.armed.Load() || g.tripped.Load() {
		return false, 0, 0
	}
	now := elapsed.Nanoseconds()
	tickLag = time.Duration(now - g.lastTick.Load())
	progressLag = time.Duration(now - g.lastProgress.Load())
	if tickLag <= 2*time.Second && progressLag <= 2*time.Second {
		return false, tickLag, progressLag
	}
	if g.tripped.CompareAndSwap(false, true) {
		g.open.Store(false)
		return true, tickLag, progressLag
	}
	return false, tickLag, progressLag
}

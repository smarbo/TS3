package watchdog

import (
	"testing"
	"time"
	"ts3/internal/book"
)

func TestGateNeedsHealthyAndAppliedTick(t *testing.T) {
	var g Gate
	if g.Open() {
		t.Fatal("open at startup")
	}
	if g.Applied(book.Healthy, false, time.Second) || g.Open() {
		t.Fatal("armed without tick")
	}
	if g.Applied(book.Unhealthy, true, 1100*time.Millisecond) || g.Open() {
		t.Fatal("armed without health")
	}
	g.Progress(1200 * time.Millisecond)
	if !g.Applied(book.Healthy, false, 1300*time.Millisecond) || !g.Open() {
		t.Fatal("healthy ticked book did not arm")
	}
	if trip, _, _ := g.Check(2 * time.Second); trip {
		t.Fatal("early trip")
	}
	g.Applied(book.Unhealthy, false, 2100*time.Millisecond)
	if g.Open() {
		t.Fatal("unhealthy book served")
	}
	g.Applied(book.Healthy, false, 2200*time.Millisecond)
	if !g.Open() {
		t.Fatal("healthy recovery did not reopen")
	}
}

func TestStaleProgressTripIsPermanent(t *testing.T) {
	var g Gate
	g.Progress(time.Second)
	g.Applied(book.Healthy, true, time.Second)
	if trip, _, _ := g.Check(3 * time.Second); trip {
		t.Fatal("boundary trip")
	}
	trip, tickLag, progressLag := g.Check(3250 * time.Millisecond)
	if !trip || tickLag != 2250*time.Millisecond || progressLag != 2250*time.Millisecond || g.Open() == true {
		t.Fatal(trip, tickLag, progressLag)
	}
	g.Progress(3300 * time.Millisecond)
	g.Applied(book.Healthy, true, 3300*time.Millisecond)
	if g.Open() {
		t.Fatal("tripped run reopened")
	}
	if trip, _, _ := g.Check(6 * time.Second); trip {
		t.Fatal("incident emitted twice")
	}
}

func TestPipelineFailureWithdrawsAvailabilityPermanently(t *testing.T) {
	var g Gate
	g.Progress(time.Second)
	g.Applied(book.Healthy, true, time.Second)
	if !g.Open() {
		t.Fatal("fixture gate never opened")
	}
	g.Fail()
	if g.Open() == true || !g.Tripped() {
		t.Fatal("fatal pipeline failure left gate open")
	}
	g.Progress(2 * time.Second)
	g.Applied(book.Healthy, true, 2*time.Second)
	if g.Open() {
		t.Fatal("failed run reopened")
	}
}

package ingress

import (
	"errors"
	"fmt"
	"time"

	"ts3/internal/domain"
)

var ErrClockBreak = errors.New("wall/monotonic clock discrepancy")

type Coordinator struct {
	RunID      string
	StartWall  time.Time
	lastWall   time.Time
	lastMono   time.Duration
	lastUsable time.Time
	ordinal    uint64
	epoch      uint64
	active     bool
	broken     bool
}

func New(runID string, start time.Time) *Coordinator {
	return &Coordinator{RunID: runID, StartWall: start.UTC(), lastWall: start.UTC(), lastUsable: start.UTC()}
}

func (c *Coordinator) Accept(o domain.Observation, admission time.Time, elapsed time.Duration) (domain.RawRecord, error) {
	if c.broken {
		return domain.RawRecord{}, ErrClockBreak
	}
	if elapsed < c.lastMono {
		return domain.RawRecord{}, errors.New("monotonic time moved backward")
	}
	admission = admission.UTC()
	wallDelta := admission.Sub(c.lastWall)
	monoDelta := elapsed - c.lastMono
	diff := wallDelta - monoDelta
	if diff < 0 {
		diff = -diff
	}
	if diff > 2*time.Second {
		c.broken = true
		sourceID := o.SourceID
		if sourceID == "" {
			sourceID = "clock"
		}
		return c.makeRecord(domain.Observation{SourceID: sourceID, Epoch: c.epoch, Kind: domain.ClockAnomaly, ReceiveTime: admission, Payload: []byte(`{"reason":"WALL_MONOTONIC_DIVERGENCE"}`)}, admission, elapsed), ErrClockBreak
	}
	switch o.Kind {
	case domain.Connected:
		if c.active || o.Epoch != c.epoch+1 {
			return domain.RawRecord{}, fmt.Errorf("invalid connection epoch %d", o.Epoch)
		}
		c.epoch = o.Epoch
		c.active = true
	case domain.Disconnected:
		if !c.active || o.Epoch != c.epoch {
			return domain.RawRecord{}, fmt.Errorf("invalid close epoch %d", o.Epoch)
		}
		c.active = false
	case domain.WSFrame:
		if !c.active || o.Epoch != c.epoch {
			return domain.RawRecord{}, fmt.Errorf("frame from inactive epoch %d", o.Epoch)
		}
	case domain.MetadataRequest, domain.MetadataResponse:
		if c.active {
			return domain.RawRecord{}, errors.New("metadata request/response interleaved with socket")
		}
	}
	if o.ReceiveTime.IsZero() {
		o.ReceiveTime = admission
	}
	c.lastWall = admission
	c.lastMono = elapsed
	return c.makeRecord(o, admission, elapsed), nil
}

func (c *Coordinator) makeRecord(o domain.Observation, admission time.Time, elapsed time.Duration) domain.RawRecord {
	c.ordinal++
	usable := admission
	if usable.Before(c.lastUsable) {
		usable = c.lastUsable
	}
	c.lastUsable = usable
	r := domain.RawRecord{SchemaVersion: domain.SchemaVersion, RunID: c.RunID, Ordinal: c.ordinal, SourceID: o.SourceID, ConnectionEpoch: o.Epoch, Kind: o.Kind, ReceiveTime: o.ReceiveTime.UTC(), AdmissionTime: admission, UsableFromTime: usable, MonotonicElapsedNS: elapsed.Nanoseconds()}
	r.SetPayload(o.Payload)
	return r
}

func (c *Coordinator) ActiveEpoch() uint64 { return c.epoch }
func (c *Coordinator) IsActive() bool      { return c.active }

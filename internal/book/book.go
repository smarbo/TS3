package book

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"ts3/internal/domain"
)

type Health string

const (
	Starting   Health = "STARTING"
	Recovering Health = "RECOVERING"
	Healthy    Health = "HEALTHY"
	Degraded   Health = "DEGRADED"
	Unhealthy  Health = "UNHEALTHY"
)

type View struct {
	Ordinal         uint64    `json:"ordinal,string"`
	AsOf            time.Time `json:"as_of"`
	Epoch           uint64    `json:"epoch,string"`
	Generation      uint64    `json:"generation,string"`
	Health          Health    `json:"health"`
	Reason          string    `json:"reason"`
	BestBid         string    `json:"best_bid,omitempty"`
	BestAsk         string    `json:"best_ask,omitempty"`
	BookSHA256      string    `json:"book_sha256,omitempty"`
	LastBookOrdinal uint64    `json:"last_book_ordinal,string"`
}

type State struct {
	metadata                     bool
	epoch                        uint64
	connected, subscribed, ready bool
	lastHeartbeat, lastBook      time.Time
	lastBookOrdinal              uint64
	generation                   uint64
	bids, asks                   map[string]string
	rawBids, rawAsks             map[string]rawLevel
	health                       Health
	reason                       string
	lastOrdinal                  uint64
}

func New() *State { return &State{health: Starting} }

func (s *State) Apply(e domain.Event) (View, error) {
	if e.Ordinal != s.lastOrdinal+1 {
		return View{}, fmt.Errorf("book ordinal gap %d after %d", e.Ordinal, s.lastOrdinal)
	}
	s.lastOrdinal = e.Ordinal
	if e.ConnectionEpoch > 0 && e.ConnectionEpoch < s.epoch {
		return s.view(e), nil
	}
	if e.Kind != domain.ProductMetadata && e.Kind != domain.Tick && e.Kind != domain.SourceStatus && e.ConnectionEpoch != s.epoch {
		s.invalidate("OLD_EPOCH")
		return s.view(e), nil
	}
	switch e.Kind {
	case domain.ProductMetadata:
		if e.Metadata == nil || !strings.HasSuffix(e.Instrument, ":"+e.Metadata.ProductID) || e.Metadata.Status != "online" || (e.Metadata.TradingDisabled != nil && *e.Metadata.TradingDisabled) {
			s.invalidate("PRODUCT_UNAVAILABLE")
		} else {
			s.metadata = true
		}
	case domain.SourceStatus:
		if e.Status == nil {
			s.invalidate("INVALID_STATUS")
			break
		}
		switch e.Status.State {
		case "connected":
			if !s.metadata || e.ConnectionEpoch != s.epoch+1 {
				s.invalidate("BAD_EPOCH")
				break
			}
			s.epoch = e.ConnectionEpoch
			s.connected = true
			s.subscribed = false
			s.ready = false
			s.bids = nil
			s.asks = nil
			s.rawBids = nil
			s.rawAsks = nil
			s.lastHeartbeat = time.Time{}
			s.lastBook = time.Time{}
			s.health = Recovering
			s.reason = "WAIT_SUBSCRIPTION"
		case "subscribed":
			if !s.connected || s.subscribed || e.ConnectionEpoch != s.epoch {
				s.invalidate("SUBSCRIPTION_MISMATCH")
				break
			}
			s.subscribed = true
			s.reason = "WAIT_SNAPSHOT"
		case "disconnected", "error", "capture_gap", "shutdown":
			if e.ConnectionEpoch == 0 || e.ConnectionEpoch == s.epoch || e.Status.State == "capture_gap" {
				s.invalidate(e.Status.Reason)
				s.connected = false
			}
		}
	case domain.BookSnapshot:
		if !s.subscribed || !s.connected || e.Snapshot == nil {
			s.invalidate("SNAPSHOT_BEFORE_ACK")
			break
		}
		b, a, err := validatedSnapshot(e.Snapshot)
		if err != nil {
			s.invalidate("INVALID_SNAPSHOT")
			break
		}
		rb, ra := rawLevels(e.Snapshot.Bids), rawLevels(e.Snapshot.Asks)
		if e.Snapshot.Checksum != "" {
			if e.Snapshot.Depth <= 0 || !verifyKrakenChecksum(rb, ra, e.Snapshot.Checksum) {
				s.invalidate("BOOK_CHECKSUM")
				break
			}
		}
		s.bids, s.asks = b, a
		s.rawBids, s.rawAsks = rb, ra
		s.ready = true
		s.generation++
		s.lastBook = e.UsableFromTime
		if e.Snapshot.Depth > 0 {
			s.lastHeartbeat = e.UsableFromTime
		}
		s.lastBookOrdinal = e.Ordinal
		s.refresh(e.UsableFromTime)
	case domain.BookDelta:
		if !s.ready || !s.connected || e.Delta == nil {
			s.invalidate("DELTA_WITHOUT_BOOK")
			break
		}
		b, a := clone(s.bids), clone(s.asks)
		rb, ra := cloneRaw(s.rawBids), cloneRaw(s.rawAsks)
		valid := true
		for _, c := range e.Delta.Changes {
			if c.Side != "buy" && c.Side != "sell" {
				valid = false
				break
			}
			p, err := domain.Decimal(c.Price, false)
			if err != nil {
				valid = false
				break
			}
			z, err := domain.Decimal(c.AbsoluteSize, true)
			if err != nil {
				valid = false
				break
			}
			side := b
			rawSide := rb
			if c.Side == "sell" {
				side = a
				rawSide = ra
			}
			if z == "0" {
				delete(side, p)
				delete(rawSide, p)
			} else {
				side[p] = z
				sp, sq := c.SourcePrice, c.SourceSize
				if sp == "" {
					sp = c.Price
				}
				if sq == "" {
					sq = c.AbsoluteSize
				}
				rawSide[p] = rawLevel{price: sp, size: sq}
			}
		}
		if e.Delta.Checksum != "" && e.Delta.Depth > 0 {
			truncate(b, rb, e.Delta.Depth, true)
			truncate(a, ra, e.Delta.Depth, false)
		}
		if !valid || !validBook(b, a) {
			s.invalidate("INVALID_DELTA_BOOK")
			break
		}
		if e.Delta.Checksum != "" && !verifyKrakenChecksum(rb, ra, e.Delta.Checksum) {
			s.invalidate("BOOK_CHECKSUM")
			break
		}
		s.bids = b
		s.asks = a
		s.rawBids, s.rawAsks = rb, ra
		s.lastBook = e.UsableFromTime
		if e.Delta.Depth > 0 {
			s.lastHeartbeat = e.UsableFromTime
		}
		s.lastBookOrdinal = e.Ordinal
		s.refresh(e.UsableFromTime)
	case domain.Heartbeat:
		if !s.subscribed || !s.connected || e.Heartbeat == nil {
			s.invalidate("HEARTBEAT_BEFORE_ACK")
			break
		}
		s.lastHeartbeat = e.UsableFromTime
		s.refresh(e.UsableFromTime)
	case domain.Tick:
		s.refresh(e.UsableFromTime)
	}
	return s.view(e), nil
}

func (s *State) invalidate(reason string) { s.ready = false; s.health = Unhealthy; s.reason = reason }
func (s *State) refresh(now time.Time) {
	if !s.connected || !s.subscribed || !s.ready {
		return
	}
	if s.lastHeartbeat.IsZero() {
		if !s.lastBook.IsZero() && now.Sub(s.lastBook) > 3*time.Second {
			s.invalidate("HEARTBEAT_STALE")
		} else {
			s.health = Recovering
			s.reason = "WAIT_HEARTBEAT"
		}
		return
	}
	if now.Sub(s.lastHeartbeat) > 3*time.Second {
		s.invalidate("HEARTBEAT_STALE")
		return
	}
	if now.Sub(s.lastBook) > 30*time.Second {
		s.health = Degraded
		s.reason = "BOOK_QUIET"
		return
	}
	s.health = Healthy
	s.reason = ""
}

func (s *State) view(e domain.Event) View {
	v := View{Ordinal: e.Ordinal, AsOf: e.UsableFromTime, Epoch: s.epoch, Generation: s.generation, Health: s.health, Reason: s.reason, LastBookOrdinal: s.lastBookOrdinal}
	if s.ready && len(s.bids) > 0 && len(s.asks) > 0 {
		v.BestBid = best(s.bids, true)
		v.BestAsk = best(s.asks, false)
		v.BookSHA256 = Hash(s.bids, s.asks)
	}
	return v
}

// Skip advances the raw ordinal for a control request or unknown future frame.
func (s *State) Skip(r domain.RawRecord) (View, error) {
	if r.Ordinal != s.lastOrdinal+1 {
		return View{}, fmt.Errorf("book ordinal gap %d after %d", r.Ordinal, s.lastOrdinal)
	}
	s.lastOrdinal = r.Ordinal
	return s.view(domain.Event{Ordinal: r.Ordinal, UsableFromTime: r.UsableFromTime}), nil
}

func validatedSnapshot(x *domain.SnapshotData) (map[string]string, map[string]string, error) {
	if len(x.Bids) == 0 || len(x.Asks) == 0 || len(x.Bids) > 250000 || len(x.Asks) > 250000 {
		return nil, nil, fmt.Errorf("level count")
	}
	b, a := make(map[string]string, len(x.Bids)), make(map[string]string, len(x.Asks))
	for _, part := range []struct {
		levels []domain.Level
		target map[string]string
	}{{x.Bids, b}, {x.Asks, a}} {
		for _, l := range part.levels {
			p, err := domain.Decimal(l.Price, false)
			if err != nil {
				return nil, nil, err
			}
			z, err := domain.Decimal(l.Size, false)
			if err != nil {
				return nil, nil, err
			}
			if _, ok := part.target[p]; ok {
				return nil, nil, fmt.Errorf("duplicate price")
			}
			part.target[p] = z
		}
	}
	if !validBook(b, a) {
		return nil, nil, fmt.Errorf("crossed or empty book")
	}
	return b, a, nil
}
func validBook(b, a map[string]string) bool {
	return len(b) > 0 && len(a) > 0 && domain.CompareDecimal(best(b, true), best(a, false)) < 0
}
func clone(x map[string]string) map[string]string {
	y := make(map[string]string, len(x))
	for k, v := range x {
		y[k] = v
	}
	return y
}
func best(x map[string]string, high bool) string {
	v := ""
	for k := range x {
		if v == "" || (high && domain.CompareDecimal(k, v) > 0) || (!high && domain.CompareDecimal(k, v) < 0) {
			v = k
		}
	}
	return v
}
func Hash(b, a map[string]string) string {
	var sb strings.Builder
	for _, part := range []struct {
		name string
		x    map[string]string
		high bool
	}{{"bid", b, true}, {"ask", a, false}} {
		keys := make([]string, 0, len(part.x))
		for k := range part.x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			c := domain.CompareDecimal(keys[i], keys[j])
			if part.high {
				return c > 0
			}
			return c < 0
		})
		for _, k := range keys {
			sb.WriteString(part.name)
			sb.WriteByte(',')
			sb.WriteString(k)
			sb.WriteByte(',')
			sb.WriteString(part.x[k])
			sb.WriteByte('\n')
		}
	}
	h := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(h[:])
}

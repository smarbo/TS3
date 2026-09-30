package book

import (
	"testing"
	"time"

	"ts3/internal/domain"
	"ts3/internal/normalize"
)

func TestQuoteAccessorIsSortedAndDetached(t *testing.T) {
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	s := &State{ready: true, epoch: 2, generation: 2, lastBookOrdinal: 42, lastBook: now,
		bids: map[string]string{"99": "2", "100": "1"}, asks: map[string]string{"102": "3", "101": "4"}}
	q := s.Quote()
	if q.Bids[0].Price != "100" || q.Asks[0].Price != "101" || q.LastBookOrdinal != 42 || !q.LastBookAt.Equal(now) {
		t.Fatalf("quote order/provenance: %+v", q)
	}
	s.bids["100"] = "5"
	q.Bids[0].Size = "9"
	if q.Bids[0].Size != "9" || s.bids["100"] != "5" {
		t.Fatal("quote aliases book state")
	}
}

func TestBookHealthReplayAndAtomicInvalidation(t *testing.T) {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	n := &normalize.Normalizer{}
	s := New()
	ordinal := uint64(0)
	epoch := uint64(0)
	step := func(kind domain.RawKind, payload string, offset time.Duration) View {
		ordinal++
		tm := start.Add(offset)
		r := domain.RawRecord{SchemaVersion: 1, RunID: "t", Ordinal: ordinal, SourceID: "fixture", ConnectionEpoch: epoch, Kind: kind, ReceiveTime: tm, AdmissionTime: tm, UsableFromTime: tm}
		r.SetPayload([]byte(payload))
		e, err := n.Convert(r)
		if err != nil {
			t.Fatal(err)
		}
		var v View
		if e == nil {
			v, err = s.Skip(r)
		} else {
			v, err = s.Apply(*e)
		}
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	step(domain.MetadataRequest, "", 0)
	step(domain.MetadataResponse, `{"id":"BTC-USD","status":"online"}`, 0)
	epoch = 1
	step(domain.Connected, "", 0)
	step(domain.WSFrame, `{"type":"subscriptions","channels":[{"name":"level2","product_ids":["BTC-USD"]},{"name":"heartbeat","product_ids":["BTC-USD"]}]}`, 0)
	v := step(domain.WSFrame, `{"type":"snapshot","product_id":"BTC-USD","bids":[["100.00","2"],["99","3"]],"asks":[["101","4"]]}`, 0)
	if v.Health == Healthy {
		t.Fatal("healthy without heartbeat")
	}
	v = step(domain.WSFrame, `{"type":"heartbeat","product_id":"BTC-USD","sequence":1,"last_trade_id":1,"time":"2026-09-27T12:00:00Z"}`, 0)
	if v.Health != Healthy || v.BestBid != "100" || v.BestAsk != "101" {
		t.Fatal(v)
	}
	hash := v.BookSHA256
	v = step(domain.WSFrame, `{"type":"l2update","product_id":"BTC-USD","time":"2026-09-27T11:59:59Z","changes":[["buy","100","0"],["buy","100.50","1"]]}`, time.Second)
	if v.Health != Healthy || v.BestBid != "100.5" || v.BookSHA256 == hash {
		t.Fatal(v)
	}
	v = step(domain.ClockTick, "", 5*time.Second)
	if v.Health != Unhealthy || v.Reason != "HEARTBEAT_STALE" {
		t.Fatal(v)
	}
	// A later heartbeat cannot revive an invalid book without a new snapshot.
	v = step(domain.WSFrame, `{"type":"heartbeat","product_id":"BTC-USD","sequence":2,"last_trade_id":1,"time":"2026-09-27T12:00:06Z"}`, 6*time.Second)
	if v.Health == Healthy {
		t.Fatal(v)
	}
}

func TestSnapshotValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		bids []domain.Level
	}{
		{"duplicate", []domain.Level{{Price: "1.0", Size: "1"}, {Price: "1.00", Size: "2"}}},
		{"zero", []domain.Level{{Price: "1", Size: "0"}}},
		{"scale", []domain.Level{{Price: "1.0000000000000000001", Size: "1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := validatedSnapshot(&domain.SnapshotData{Bids: tc.bids, Asks: []domain.Level{{Price: "2", Size: "1"}}})
			if err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
	if _, _, err := validatedSnapshot(&domain.SnapshotData{Bids: []domain.Level{{Price: "2", Size: "1"}}, Asks: []domain.Level{{Price: "1", Size: "1"}}}); err == nil {
		t.Fatal("crossed book accepted")
	}
}

func TestOldEpochEventsCannotInvalidateNewBook(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	s := New()
	n := &normalize.Normalizer{}
	ordinal := uint64(0)
	step := func(kind domain.RawKind, epoch uint64, payload string) View {
		ordinal++
		r := domain.RawRecord{SchemaVersion: 1, RunID: "t", Ordinal: ordinal, SourceID: "fixture", ConnectionEpoch: epoch, Kind: kind, ReceiveTime: base, AdmissionTime: base, UsableFromTime: base}
		r.SetPayload([]byte(payload))
		e, err := n.Convert(r)
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			v, err := s.Skip(r)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		v, err := s.Apply(*e)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	step(domain.MetadataRequest, 0, "")
	step(domain.MetadataResponse, 0, `{"id":"BTC-USD","status":"online"}`)
	for epoch := uint64(1); epoch <= 2; epoch++ {
		step(domain.Connected, epoch, "")
		step(domain.WSFrame, epoch, `{"type":"subscriptions","channels":[{"name":"level2","product_ids":["BTC-USD"]},{"name":"heartbeat","product_ids":["BTC-USD"]}]}`)
		step(domain.WSFrame, epoch, `{"type":"snapshot","product_id":"BTC-USD","bids":[["100","1"]],"asks":[["101","1"]]}`)
		v := step(domain.WSFrame, epoch, `{"type":"heartbeat","product_id":"BTC-USD","sequence":1,"last_trade_id":1,"time":"2026-09-27T12:00:00Z"}`)
		if v.Health != Healthy {
			t.Fatal(v)
		}
		if epoch == 1 {
			step(domain.Disconnected, 1, "network")
		}
	}
	before := s.view(domain.Event{Ordinal: ordinal, UsableFromTime: base})
	for _, kind := range []domain.RawKind{domain.WSFrame, domain.Disconnected} {
		v := step(kind, 1, `{"type":"l2update","product_id":"BTC-USD","time":"2026-09-27T12:00:00Z","changes":[["buy","100.5","1"]]}`)
		if v.Health != Healthy || v.BookSHA256 != before.BookSHA256 || v.Epoch != 2 {
			t.Fatal(v)
		}
	}
}

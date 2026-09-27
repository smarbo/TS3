package book

import (
	"strconv"
	"testing"
	"time"
	"ts3/internal/domain"
)

func TestKrakenPublishedChecksumExample(t *testing.T) {
	asks := []rawLevel{{"45285.2", "0.00100000"}, {"45286.4", "1.54571953"}, {"45286.6", "1.54571109"}, {"45289.6", "1.54560911"}, {"45290.2", "0.15890660"}, {"45291.8", "1.54553491"}, {"45294.7", "0.04454749"}, {"45296.1", "0.35380000"}, {"45297.5", "0.09945542"}, {"45299.5", "0.18772827"}}
	bids := []rawLevel{{"45283.5", "0.10000000"}, {"45283.4", "1.54582015"}, {"45282.1", "0.10000000"}, {"45281.0", "0.10000000"}, {"45280.3", "1.54592586"}, {"45279.0", "0.07990000"}, {"45277.6", "0.03310103"}, {"45277.5", "0.30000000"}, {"45277.3", "1.54602737"}, {"45276.6", "0.15445238"}}
	a, b := map[string]rawLevel{}, map[string]rawLevel{}
	for _, x := range asks {
		a[x.price] = x
	}
	for _, x := range bids {
		b[x.price] = x
	}
	if !verifyKrakenChecksum(b, a, "3310070434") {
		t.Fatal("published checksum mismatch")
	}
	a["45285.2"] = rawLevel{"45285.2", "0.00100001"}
	if verifyKrakenChecksum(b, a, "3310070434") {
		t.Fatal("changed book accepted")
	}
}

func TestKrakenChecksumFailureIsAtomic(t *testing.T) {
	s := New()
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	ordinal := uint64(0)
	apply := func(kind domain.EventKind, epoch uint64, payload any) View {
		ordinal++
		e := domain.Event{Ordinal: ordinal, Kind: kind, Venue: "kraken-spot", Instrument: "kraken-spot:BTC/USD", ConnectionEpoch: epoch, UsableFromTime: base}
		switch v := payload.(type) {
		case *domain.MetadataData:
			e.Metadata = v
		case *domain.StatusData:
			e.Status = v
		case *domain.SnapshotData:
			e.Snapshot = v
		case *domain.DeltaData:
			e.Delta = v
		}
		view, err := s.Apply(e)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	apply(domain.ProductMetadata, 0, &domain.MetadataData{ProductID: "BTC/USD", Status: "online"})
	apply(domain.SourceStatus, 1, &domain.StatusData{State: "connected"})
	apply(domain.SourceStatus, 1, &domain.StatusData{State: "subscribed"})
	b := map[string]rawLevel{"100": {price: "100.0", size: "1.00000000"}}
	a := map[string]rawLevel{"101": {price: "101.0", size: "1.00000000"}}
	snap := &domain.SnapshotData{Bids: []domain.Level{{Price: "100", Size: "1", SourcePrice: "100.0", SourceSize: "1.00000000"}}, Asks: []domain.Level{{Price: "101", Size: "1", SourcePrice: "101.0", SourceSize: "1.00000000"}}, Checksum: strconv.FormatUint(uint64(krakenChecksum(b, a)), 10), Depth: 100}
	v := apply(domain.BookSnapshot, 1, snap)
	if v.Health != Healthy {
		t.Fatal(v)
	}
	finalBids := map[string]rawLevel{"100.5": {price: "100.50", size: "2.00000000"}}
	validDelta := &domain.DeltaData{Changes: []domain.Change{
		{Side: "buy", Price: "100.5", AbsoluteSize: "1", SourcePrice: "100.50", SourceSize: "1.00000000"},
		{Side: "buy", Price: "100.5", AbsoluteSize: "2", SourcePrice: "100.50", SourceSize: "2.00000000"},
		{Side: "sell", Price: "102", AbsoluteSize: "0", SourcePrice: "102.00", SourceSize: "0.00000000"},
		{Side: "buy", Price: "100", AbsoluteSize: "0", SourcePrice: "100.0", SourceSize: "0.00000000"},
	}, Checksum: strconv.FormatUint(uint64(krakenChecksum(finalBids, a)), 10), Depth: 100}
	v = apply(domain.BookDelta, 1, validDelta)
	if v.Health != Healthy || v.BestBid != "100.5" || len(s.bids) != 1 || s.bids["100.5"] != "2" || s.rawBids["100.5"].size != "2.00000000" {
		t.Fatal(v)
	}
	before := Hash(s.bids, s.asks)
	v = apply(domain.BookDelta, 1, &domain.DeltaData{Changes: []domain.Change{{Side: "buy", Price: "100.5", AbsoluteSize: "2", SourcePrice: "100.5", SourceSize: "2.00000000"}}, Checksum: "0", Depth: 100})
	if v.Health != Unhealthy || v.Reason != "BOOK_CHECKSUM" || Hash(s.bids, s.asks) != before {
		t.Fatal(v)
	}
}

func TestKrakenDepthTruncation(t *testing.T) {
	bids, asks := map[string]string{}, map[string]string{}
	rb, ra := map[string]rawLevel{}, map[string]rawLevel{}
	for i := 1; i <= 101; i++ {
		p := strconv.Itoa(i)
		bids[p] = "1"
		rb[p] = rawLevel{price: p, size: "1"}
		q := strconv.Itoa(i + 101)
		asks[q] = "1"
		ra[q] = rawLevel{price: q, size: "1"}
	}
	truncate(bids, rb, 100, true)
	truncate(asks, ra, 100, false)
	if len(bids) != 100 || len(asks) != 100 || bids["1"] != "" || asks["202"] != "" || bids["101"] != "1" || asks["102"] != "1" {
		t.Fatal("incorrect depth bounds")
	}
}

func TestKrakenLifecycleAndQuietBook(t *testing.T) {
	s := New()
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	var ordinal uint64
	apply := func(kind domain.EventKind, epoch uint64, elapsed time.Duration, metadata *domain.MetadataData, status *domain.StatusData, snapshot *domain.SnapshotData) View {
		ordinal++
		e := domain.Event{Ordinal: ordinal, Kind: kind, Venue: "kraken-spot", Instrument: "kraken-spot:BTC/USD", ConnectionEpoch: epoch, UsableFromTime: base.Add(elapsed), Metadata: metadata, Status: status, Snapshot: snapshot}
		if kind == domain.Heartbeat {
			e.Heartbeat = &domain.HeartbeatData{}
		}
		v, err := s.Apply(e)
		if err != nil || v.Ordinal != ordinal {
			t.Fatalf("ordinal=%d view=%+v err=%v", ordinal, v, err)
		}
		return v
	}
	checksum := strconv.FormatUint(uint64(krakenChecksum(
		map[string]rawLevel{"100": {price: "100.00", size: "1.00000000"}},
		map[string]rawLevel{"101": {price: "101.00", size: "2.00000000"}},
	)), 10)
	snapshot := &domain.SnapshotData{Bids: []domain.Level{{Price: "100", Size: "1", SourcePrice: "100.00", SourceSize: "1.00000000"}}, Asks: []domain.Level{{Price: "101", Size: "2", SourcePrice: "101.00", SourceSize: "2.00000000"}}, Checksum: checksum, Depth: 100}
	apply(domain.ProductMetadata, 0, 0, &domain.MetadataData{ProductID: "BTC/USD", Status: "online"}, nil, nil)
	apply(domain.SourceStatus, 1, 0, nil, &domain.StatusData{State: "connected"}, nil)
	if v := apply(domain.BookSnapshot, 1, 0, nil, nil, snapshot); v.Health != Unhealthy || v.Reason != "SNAPSHOT_BEFORE_ACK" || v.BookSHA256 != "" {
		t.Fatal(v)
	}
	apply(domain.SourceStatus, 1, 0, nil, &domain.StatusData{State: "subscribed"}, nil)
	if v := apply(domain.BookSnapshot, 1, 0, nil, nil, snapshot); v.Health != Healthy || v.Generation != 1 || v.BestBid != "100" || v.BestAsk != "101" {
		t.Fatal(v)
	}
	if v := apply(domain.SourceStatus, 1, 0, nil, &domain.StatusData{State: "subscribed"}, nil); v.Health != Unhealthy || v.BookSHA256 != "" {
		t.Fatal(v)
	}
	apply(domain.SourceStatus, 1, 0, nil, &domain.StatusData{State: "disconnected"}, nil)
	apply(domain.SourceStatus, 2, 0, nil, &domain.StatusData{State: "connected"}, nil)
	apply(domain.SourceStatus, 2, 0, nil, &domain.StatusData{State: "subscribed"}, nil)
	v := apply(domain.BookSnapshot, 2, 0, nil, nil, snapshot)
	if v.Health != Healthy || v.Generation != 2 {
		t.Fatal(v)
	}
	hash := v.BookSHA256
	v = apply(domain.SourceStatus, 1, 0, nil, &domain.StatusData{State: "disconnected", Reason: "late old epoch"}, nil)
	if v.Health != Healthy || v.BookSHA256 != hash || v.Epoch != 2 {
		t.Fatal(v)
	}
	for seconds := 2; seconds <= 32; seconds += 2 {
		v = apply(domain.Heartbeat, 2, time.Duration(seconds)*time.Second, nil, nil, nil)
		if seconds <= 30 && v.Health != Healthy {
			t.Fatalf("second=%d view=%+v", seconds, v)
		}
	}
	if v.Health != Degraded || v.Reason != "BOOK_QUIET" || v.BookSHA256 != hash {
		t.Fatal(v)
	}
	v = apply(domain.Tick, 0, 36*time.Second, nil, nil, nil)
	if v.Health != Unhealthy || v.Reason != "HEARTBEAT_STALE" || v.BookSHA256 != "" {
		t.Fatal(v)
	}
	v = apply(domain.Heartbeat, 2, 37*time.Second, nil, nil, nil)
	if v.Health == Healthy || v.BookSHA256 != "" {
		t.Fatal(v)
	}
}

func TestKrakenStatusAtEveryOrdinal(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	bids := map[string]rawLevel{"100": {price: "100.00", size: "1.00000000"}}
	asks := map[string]rawLevel{"101": {price: "101.00", size: "2.00000000"}}
	snapshot := &domain.SnapshotData{Bids: []domain.Level{{Price: "100", Size: "1", SourcePrice: "100.00", SourceSize: "1.00000000"}}, Asks: []domain.Level{{Price: "101", Size: "2", SourcePrice: "101.00", SourceSize: "2.00000000"}}, Checksum: strconv.FormatUint(uint64(krakenChecksum(bids, asks)), 10), Depth: 100}
	goodHash := Hash(map[string]string{"100": "1"}, map[string]string{"101": "2"})
	cases := []struct {
		kind       domain.EventKind
		epoch      uint64
		seconds    int
		status     *domain.StatusData
		snapshot   *domain.SnapshotData
		metadata   *domain.MetadataData
		wantHealth Health
		wantReason string
		wantGen    uint64
		wantBook   bool
	}{
		{kind: domain.ProductMetadata, metadata: &domain.MetadataData{ProductID: "BTC/USD", Status: "online"}, wantHealth: Starting},
		{kind: domain.SourceStatus, epoch: 1, status: &domain.StatusData{State: "connected"}, wantHealth: Recovering, wantReason: "WAIT_SUBSCRIPTION"},
		{kind: domain.BookSnapshot, epoch: 1, snapshot: snapshot, wantHealth: Unhealthy, wantReason: "SNAPSHOT_BEFORE_ACK"},
		{kind: domain.SourceStatus, epoch: 1, status: &domain.StatusData{State: "subscribed"}, wantHealth: Unhealthy, wantReason: "WAIT_SNAPSHOT"},
		{kind: domain.BookSnapshot, epoch: 1, snapshot: snapshot, wantHealth: Healthy, wantGen: 1, wantBook: true},
		{kind: domain.Tick, seconds: 4, wantHealth: Unhealthy, wantReason: "HEARTBEAT_STALE", wantGen: 1},
		{kind: domain.Heartbeat, epoch: 1, seconds: 5, wantHealth: Unhealthy, wantReason: "HEARTBEAT_STALE", wantGen: 1},
		{kind: domain.SourceStatus, epoch: 1, seconds: 5, status: &domain.StatusData{State: "disconnected", Reason: "network"}, wantHealth: Unhealthy, wantReason: "network", wantGen: 1},
		{kind: domain.SourceStatus, epoch: 2, seconds: 5, status: &domain.StatusData{State: "connected"}, wantHealth: Recovering, wantReason: "WAIT_SUBSCRIPTION", wantGen: 1},
		{kind: domain.SourceStatus, epoch: 2, seconds: 5, status: &domain.StatusData{State: "subscribed"}, wantHealth: Recovering, wantReason: "WAIT_SNAPSHOT", wantGen: 1},
		{kind: domain.BookSnapshot, epoch: 2, seconds: 5, snapshot: snapshot, wantHealth: Healthy, wantGen: 2, wantBook: true},
		{kind: domain.SourceStatus, epoch: 1, seconds: 5, status: &domain.StatusData{State: "disconnected", Reason: "late old epoch"}, wantHealth: Healthy, wantGen: 2, wantBook: true},
	}
	state := New()
	for i, tc := range cases {
		e := domain.Event{Ordinal: uint64(i + 1), Kind: tc.kind, Venue: "kraken-spot", Instrument: "kraken-spot:BTC/USD", ConnectionEpoch: tc.epoch, UsableFromTime: base.Add(time.Duration(tc.seconds) * time.Second), Status: tc.status, Snapshot: tc.snapshot, Metadata: tc.metadata}
		if tc.kind == domain.Heartbeat {
			e.Heartbeat = &domain.HeartbeatData{}
		}
		view, err := state.Apply(e)
		if err != nil {
			t.Fatal(err)
		}
		if view.Ordinal != e.Ordinal || view.Health != tc.wantHealth || view.Reason != tc.wantReason || view.Generation != tc.wantGen {
			t.Fatalf("ordinal %d: %+v", e.Ordinal, view)
		}
		if tc.wantBook {
			if view.BestBid != "100" || view.BestAsk != "101" || view.BookSHA256 != goodHash {
				t.Fatalf("ordinal %d book: %+v", e.Ordinal, view)
			}
		} else if view.BestBid != "" || view.BestAsk != "" || view.BookSHA256 != "" {
			t.Fatalf("ordinal %d exposed invalid book: %+v", e.Ordinal, view)
		}
	}
}

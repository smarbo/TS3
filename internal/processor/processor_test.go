package processor

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ts3/internal/domain"
	"ts3/internal/ingress"
	"ts3/internal/progress"
	"ts3/internal/record"
	"ts3/internal/source/replay"
)

func TestRecordedLiveReplayParityAndFutureBarrier(t *testing.T) {
	root := t.TempDir()
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	c := ingress.New("run", start)
	var records []domain.RawRecord
	add := func(kind domain.RawKind, epoch uint64, p string, d time.Duration) {
		tm := start.Add(d)
		r, err := c.Accept(domain.Observation{SourceID: "fixture", Epoch: epoch, Kind: kind, ReceiveTime: tm, Payload: []byte(p)}, tm, d)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	add(domain.MetadataRequest, 0, "", 0)
	add(domain.MetadataResponse, 0, `{"id":"BTC-USD","status":"online"}`, 0)
	add(domain.Connected, 1, "", 0)
	add(domain.WSFrame, 1, `{"type":"subscriptions","channels":[{"name":"level2","product_ids":["BTC-USD"]},{"name":"heartbeat","product_ids":["BTC-USD"]}]}`, 0)
	add(domain.WSFrame, 1, `{"type":"snapshot","product_id":"BTC-USD","bids":[["100","1"]],"asks":[["101","1"]]}`, 0)
	add(domain.WSFrame, 1, `{"type":"heartbeat","product_id":"BTC-USD","sequence":1,"last_trade_id":1,"time":"2026-09-27T12:00:00Z"}`, 0)
	add(domain.ClockTick, 1, "", time.Second)
	add(domain.WSFrame, 1, `{"type":"l2update","product_id":"BTC-USD","time":"2026-09-27T11:59:59Z","changes":[["buy","100.5","2"]]}`, time.Second)
	w, err := record.NewWriter(root, "run")
	if err != nil {
		t.Fatal(err)
	}
	live, err := New(filepath.Join(root, "run"), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, batch := range [][]domain.RawRecord{records[:6], records[6:]} {
		if err = w.AppendBatch(batch); err != nil {
			t.Fatal(err)
		}
		for _, r := range batch {
			if _, err = live.Apply(r); err != nil {
				t.Fatal(err)
			}
		}
		if err = live.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(true); err != nil {
		t.Fatal(err)
	}
	lh, ls := live.Hashes()
	if err = live.Close(); err != nil {
		t.Fatal(err)
	}
	marks, err := progress.Recover(filepath.Join(root, "run", "progress.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if marks.Applied != 8 || marks.Published != 8 {
		t.Fatal(marks)
	}
	r, err := record.NewReader(root, "run")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	replayed, err := New(filepath.Join(root, "replayed"), false)
	if err != nil {
		t.Fatal(err)
	}
	for {
		raw, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = replayed.Apply(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err = replayed.Flush(); err != nil {
		t.Fatal(err)
	}
	rh, rs := replayed.Hashes()
	if lh != rh || ls != rs {
		t.Fatalf("live/replay mismatch %s/%s %s/%s", lh, ls, rh, rs)
	}
	if err = replayed.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(filepath.Join(root, "run", "state.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "replayed", "state.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("state bytes differ")
	}
	// Reopen the same committed tape with source pacing. Time spent sleeping may
	// change operational timestamps, but cannot change canonical outputs.
	second, err := replay.Open(root, "run", true)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	other, err := New(filepath.Join(root, "paced"), false)
	if err != nil {
		t.Fatal(err)
	}
	for {
		raw, err := second.Next(t.Context())
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Apply(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := other.Flush(); err != nil {
		t.Fatal(err)
	}
	ph, ps := other.Hashes()
	if ph != lh || ps != ls {
		t.Fatalf("paced replay changed hashes %s/%s", ph, ps)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	// The last, earlier-event-time delta cannot alter an already emitted row.
	lines := bytes.Split(bytes.TrimSpace(a), []byte("\n"))
	if len(lines) != 8 || bytes.Contains(lines[6], []byte("100.5")) {
		t.Fatal("future delta leaked into prior state")
	}
}

func TestFutureMetadataCannotChangePastPrefix(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	inputs := []struct {
		kind    domain.RawKind
		epoch   uint64
		payload string
	}{
		{domain.MetadataRequest, 0, ""},
		{domain.MetadataResponse, 0, `{"id":"BTC-USD","status":"online"}`},
		{domain.Connected, 1, ""},
		{domain.WSFrame, 1, `{"type":"subscriptions","channels":[{"name":"level2","product_ids":["BTC-USD"]},{"name":"heartbeat","product_ids":["BTC-USD"]}]}`},
		{domain.WSFrame, 1, `{"type":"snapshot","product_id":"BTC-USD","bids":[["100","1"]],"asks":[["101","1"]]}`},
		{domain.WSFrame, 1, `{"type":"heartbeat","product_id":"BTC-USD","sequence":1,"last_trade_id":1,"time":"2026-09-27T12:00:00Z"}`},
		{domain.ClockTick, 0, ""},
		{domain.Disconnected, 1, "network"},
	}
	makeRaw := func(n int, kind domain.RawKind, epoch uint64, p string) domain.RawRecord {
		r := domain.RawRecord{SchemaVersion: 1, RunID: "poison", Ordinal: uint64(n), SourceID: "fixture", ConnectionEpoch: epoch, Kind: kind, ReceiveTime: base, AdmissionTime: base, UsableFromTime: base}
		r.SetPayload([]byte(p))
		return r
	}
	var prefixes [][]byte
	for i, future := range []string{`{"id":"BTC-USD","status":"online"}`, `{"id":"BTC-USD","status":"offline"}`} {
		p, err := New(filepath.Join(root, string(rune('a'+i))), false)
		if err != nil {
			t.Fatal(err)
		}
		for n, x := range inputs {
			if _, err := p.Apply(makeRaw(n+1, x.kind, x.epoch, x.payload)); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Flush(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(root, string(rune('a'+i)), "state.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		prefixes = append(prefixes, b)
		if _, err := p.Apply(makeRaw(len(inputs)+1, domain.MetadataResponse, 0, future)); err != nil {
			t.Fatal(err)
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(prefixes[0], prefixes[1]) {
		t.Fatal("future metadata changed earlier state")
	}
}

func TestDerivedOutputWriteFailureFreezesAcknowledgedPrefix(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	makeRaw := func(ordinal uint64, kind domain.RawKind, payload string) domain.RawRecord {
		r := domain.RawRecord{SchemaVersion: 1, RunID: "failed-output", Ordinal: ordinal, SourceID: "kraken-product", Kind: kind, ReceiveTime: base, AdmissionTime: base, UsableFromTime: base}
		r.SetPayload([]byte(payload))
		return r
	}
	first := makeRaw(1, domain.MetadataRequest, "")
	second := makeRaw(2, domain.MetadataResponse, `{"error":[],"result":{"XXBTZUSD":{"altname":"XBTUSD","status":"online"}}}`)
	w, err := record.NewWriter(root, "failed-output")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendBatch([]domain.RawRecord{first, second}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(true); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(root, "live")
	p, err := New(outputDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(first); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := p.state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(second); err == nil {
		t.Fatal("closed derived state file accepted a write")
	}
	if _, err := p.Apply(makeRaw(3, domain.ClockTick, "")); err == nil {
		t.Fatal("processor continued after output failure")
	}
	if err := p.Flush(); err == nil {
		t.Fatal("failed output was acknowledged")
	}
	if marks := p.Progress(); marks.Applied != 1 || marks.Published != 1 {
		t.Fatal(marks)
	}
	if err := p.Close(); err == nil {
		t.Fatal("injected derived-output close failure went unreported")
	}
	marks, err := progress.Recover(filepath.Join(outputDir, "progress.jsonl"))
	if err != nil || marks.Applied != 1 || marks.Published != 1 {
		t.Fatalf("marks=%+v err=%v", marks, err)
	}
	r, err := record.NewReader(root, "failed-output")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for ordinal := uint64(1); ordinal <= 2; ordinal++ {
		v, err := r.Next()
		if err != nil || v.Ordinal != ordinal {
			t.Fatalf("record=%+v err=%v", v, err)
		}
	}
	if _, err := r.Next(); err != io.EOF || r.Report().CommittedOrdinal != 2 {
		t.Fatalf("raw=%+v err=%v", r.Report(), err)
	}
}

package record

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
	"ts3/internal/watchdog"
)

func fixture(n uint64) domain.RawRecord {
	t := time.Date(2026, 9, 27, 12, 0, int(n), 0, time.UTC)
	r := domain.RawRecord{SchemaVersion: 1, RunID: "test", Ordinal: n, SourceID: "fixture", Kind: domain.WSFrame, ReceiveTime: t, AdmissionTime: t, UsableFromTime: t}
	r.SetPayload([]byte(`{"type":"heartbeat","product_id":"BTC-USD"}`))
	return r
}

func TestRawFrameLengthAndCRC(t *testing.T) {
	r := fixture(1)
	frame, err := makeFrame(frameHeader{RecordType: "event", Record: &r}, r.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.BigEndian.Uint32(frame[:4]); int(got) != len(frame)-4 {
		t.Fatalf("frame length=%d bytes=%d", got, len(frame))
	}
	if got := binary.BigEndian.Uint32(frame[len(frame)-4:]); got != crc32.Checksum(frame[4:len(frame)-4], crcTable) {
		t.Fatalf("CRC mismatch: %d", got)
	}
	h, payload, parsed, err := parseFrame(bytes.NewReader(frame))
	if err != nil || h.RecordType != "event" || h.Record.Ordinal != 1 || !bytes.Equal(payload, r.Payload) || !bytes.Equal(parsed, frame) {
		t.Fatalf("header=%+v payload=%q err=%v", h, payload, err)
	}
	frame[len(frame)-5] ^= 1
	if _, _, _, err := parseFrame(bytes.NewReader(frame)); err == nil {
		t.Fatal("corrupted frame accepted")
	}
}

func TestCommittedPrefix(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.AppendBatch([]domain.RawRecord{fixture(1), fixture(2)}); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(true); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for n := uint64(1); n <= 2; n++ {
		v, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if v.Ordinal != n || !bytes.Equal(v.Payload, fixture(n).Payload) {
			t.Fatalf("bad record %d: %+v", n, v)
		}
	}
	if _, err = r.Next(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
	if r.Report().CommittedOrdinal != 2 || r.Report().Incomplete {
		t.Fatal(r.Report())
	}
}

func TestUncommittedSuffixExcluded(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.AppendBatch([]domain.RawRecord{fixture(1)}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "test-000001.raw"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	x := fixture(2)
	b, err := makeFrame(frameHeader{RecordType: "event", Record: &x}, x.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(b); err != nil {
		t.Fatal(err)
	}
	f.Close()
	w.f.Close()
	r, err := NewReader(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = r.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Next(); err != io.EOF {
		t.Fatalf("unexpected suffix: %v", err)
	}
	if r.Report().ValidOrdinal != 2 || r.Report().CommittedOrdinal != 1 || r.Report().UncommittedEvents != 1 {
		t.Fatal(r.Report())
	}
}

func TestCorruptionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.AppendBatch([]domain.RawRecord{fixture(1)}); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(true); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "test-000001.raw")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b[30] ^= 1
	if err = os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(dir, "test")
	if err == nil {
		defer r.Close()
		_, err = r.Next()
	}
	if err == nil || err == io.EOF {
		t.Fatalf("corruption accepted: %v", err)
	}
}

func TestMissingManifestDoesNotEraseCommittedPrefix(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendBatch([]domain.RawRecord{fixture(1)}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "test.manifest.json")); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "test-000001.raw"), old, old); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, err := r.Next(); err != nil || v.Ordinal != 1 {
		t.Fatalf("record=%+v err=%v", v, err)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatal(err)
	}
	if !r.Report().Incomplete || r.Report().CommittedOrdinal != 1 {
		t.Fatal(r.Report())
	}
	if m, err := RebuildManifest(dir, "test"); err != nil || m.Clean || m.CommittedOrdinal != 1 || len(m.Segments) != 1 {
		t.Fatalf("manifest=%+v err=%v", m, err)
	}
	r2, err := NewReader(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if v, err := r2.Next(); err != nil || v.Ordinal != 1 {
		t.Fatalf("record=%+v err=%v", v, err)
	}
	if _, err := r2.Next(); err != io.EOF {
		t.Fatal(err)
	}
	if !r2.Report().Incomplete {
		t.Fatal("recovered manifest must remain unclean")
	}
}

func TestPartialManifestRecoveryPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendBatch([]domain.RawRecord{fixture(1)}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(true); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "test.manifest.json")
	partial := []byte(`{"run_id":"test","segments":[`)
	if err := os.WriteFile(p, partial, 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "test-000001.raw"), old, old); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := r.Next(); err != nil || v.Ordinal != 1 {
		t.Fatalf("record=%+v err=%v", v, err)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatal(err)
	}
	if !r.Report().Incomplete || r.Report().Error == "" || r.Report().CommittedOrdinal != 1 {
		t.Fatal(r.Report())
	}
	r.Close()
	m, err := RebuildManifest(dir, "test")
	if err != nil || m.Clean || m.CommittedOrdinal != 1 {
		t.Fatalf("manifest=%+v err=%v", m, err)
	}
	b, err := os.ReadFile(p + ".invalid.json")
	if err != nil || !bytes.Equal(b, partial) {
		t.Fatalf("preserved bytes=%q err=%v", b, err)
	}
	r, err = NewReader(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, err := r.Next(); err != nil || v.Ordinal != 1 {
		t.Fatalf("recovered record=%+v err=%v", v, err)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatal(err)
	}
	if !r.Report().Incomplete || r.Report().Error != "" {
		t.Fatal(r.Report())
	}
}

func TestManifestRecoveryRejectsRecentlyWrittenRawSegment(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendBatch([]domain.RawRecord{fixture(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := RebuildManifest(dir, "test"); err == nil {
		t.Fatal("recovery accepted a tape still being written")
	}
	if err := w.Close(true); err != nil {
		t.Fatal(err)
	}
}

func TestManifestHashDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendBatch([]domain.RawRecord{fixture(1)}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(true); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "test-000001.raw")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("tamper")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if r, err := NewReader(dir, "test"); err == nil {
		r.Close()
		t.Fatal("tampered segment accepted")
	}
}

func TestTornUncommittedTailKeepsPriorCommit(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendBatch([]domain.RawRecord{fixture(1)}); err != nil {
		t.Fatal(err)
	}
	x := fixture(2)
	frame, err := makeFrame(frameHeader{RecordType: "event", Record: &x}, x.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.Write(frame[:len(frame)/2]); err != nil {
		t.Fatal(err)
	}
	if err := w.f.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, err := r.Next(); err != nil || v.Ordinal != 1 {
		t.Fatalf("record=%+v err=%v", v, err)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatal(err)
	}
	if r.Report().CommittedOrdinal != 1 || !r.Report().Incomplete || r.Report().Error == "" {
		t.Fatal(r.Report())
	}
}

func TestTornCommitMarkerExcludesNewBatch(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendBatch([]domain.RawRecord{fixture(1)}); err != nil {
		t.Fatal(err)
	}
	x := fixture(2)
	frame, err := makeFrame(frameHeader{RecordType: "event", Record: &x}, x.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.Write(frame); err != nil {
		t.Fatal(err)
	}
	_, _ = w.prefix.Write(frame)
	marker, err := makeFrame(frameHeader{RecordType: "commit", LastOrdinal: 2, PrefixSHA256: hex.EncodeToString(w.prefix.Sum(nil))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.Write(marker[:len(marker)/2]); err != nil {
		t.Fatal(err)
	}
	if err := w.f.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, err := r.Next(); err != nil || v.Ordinal != 1 {
		t.Fatalf("record=%+v err=%v", v, err)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("uncommitted batch published: %v", err)
	}
	if got := r.Report(); got.ValidOrdinal != 2 || got.CommittedOrdinal != 1 || got.UncommittedEvents != 1 || got.Error == "" {
		t.Fatal(got)
	}
}

func TestStorageWriteFailureDoesNotAdvanceCommit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux /dev/full to inject ENOSPC")
	}
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendBatch([]domain.RawRecord{fixture(1)}); err != nil {
		t.Fatal(err)
	}
	if err := w.f.Close(); err != nil {
		t.Fatal(err)
	}
	w.f, err = os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.f.Close()
	if err := w.AppendBatch([]domain.RawRecord{fixture(2)}); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("expected ENOSPC, got %v", err)
	}
	if w.CommittedOrdinal() != 1 {
		t.Fatal("failed batch advanced commit mark")
	}
	r, err := NewReader(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, err := r.Next(); err != nil || v.Ordinal != 1 {
		t.Fatalf("record=%+v err=%v", v, err)
	}
	if _, err := r.Next(); err != io.EOF || r.Report().CommittedOrdinal != 1 {
		t.Fatalf("recorded failed batch: %v %+v", err, r.Report())
	}
}

func TestFsyncFailureBoundaries(t *testing.T) {
	for _, failureAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("sync_%d", failureAt), func(t *testing.T) {
			dir := t.TempDir()
			w, err := NewWriter(dir, "test")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			w.syncFn = func(f *os.File) error {
				calls++
				if calls == failureAt {
					return syscall.EIO
				}
				return f.Sync()
			}
			if err := w.AppendBatch([]domain.RawRecord{fixture(1)}); !errors.Is(err, syscall.EIO) {
				t.Fatalf("wanted injected EIO, got %v", err)
			}
			if w.CommittedOrdinal() != 0 || calls != failureAt {
				t.Fatalf("writer acknowledged failed batch: ordinal=%d calls=%d", w.CommittedOrdinal(), calls)
			}
			if err := w.f.Close(); err != nil {
				t.Fatal(err)
			}
			r, err := NewReader(dir, "test")
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			v, nextErr := r.Next()
			if failureAt == 1 {
				if nextErr != io.EOF || r.Report().CommittedOrdinal != 0 || r.Report().UncommittedEvents != 1 {
					t.Fatalf("data-sync failure exposed batch: %+v, %v", r.Report(), nextErr)
				}
			} else {
				if nextErr != nil || v.Ordinal != 1 {
					t.Fatalf("persisted marker not recoverable: %+v, %v", v, nextErr)
				}
				if _, err := r.Next(); err != io.EOF || r.Report().CommittedOrdinal != 1 || !r.Report().Incomplete {
					t.Fatalf("marker-sync failure not marked incomplete: %+v, %v", r.Report(), err)
				}
			}
		})
	}
}

func TestBlockedDataFsyncTripsLiveGate(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(false)
	entered := make(chan struct{})
	release := make(chan struct{})
	w.syncFn = func(f *os.File) error {
		select {
		case <-entered:
		default:
			close(entered)
			<-release
		}
		return f.Sync()
	}
	start := time.Now()
	var gate watchdog.Gate
	gate.Progress(0)
	gate.Applied(book.Healthy, true, 0)
	appendDone := make(chan error, 1)
	go func() { appendDone <- w.AppendBatch([]domain.RawRecord{fixture(1)}) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer never reached data fsync")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	tripped := false
	deadline := time.After(3 * time.Second)
	for !tripped {
		select {
		case <-ticker.C:
			tripped, _, _ = gate.Check(time.Since(start))
		case <-deadline:
			close(release)
			<-appendDone
			t.Fatal("blocked recorder left operational gate open")
		}
	}
	elapsed := time.Since(start)
	open := gate.Open()
	close(release)
	if err := <-appendDone; err != nil {
		t.Fatal(err)
	}
	if elapsed > 2750*time.Millisecond || open {
		t.Fatalf("late or open gate after blocked fsync: %v", elapsed)
	}
	if gate.Open() {
		t.Fatal("completed fsync reopened tripped gate")
	}
}

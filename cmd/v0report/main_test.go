package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
)

func TestStateScanRecordsQuietIntervalAndChecksumIncident(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for i, x := range []struct {
		seconds int
		health  book.Health
		reason  string
	}{
		{0, book.Healthy, ""},
		{31, book.Degraded, "BOOK_QUIET"},
		{32, book.Degraded, "BOOK_QUIET"},
		{33, book.Unhealthy, "BOOK_CHECKSUM"},
		{34, book.Healthy, ""},
	} {
		v := book.View{Ordinal: uint64(i + 1), AsOf: base.Add(time.Duration(x.seconds) * time.Second), Epoch: 1, Health: x.health, Reason: x.reason}
		b, err := domain.CanonicalJSON(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	out := report{EndedAt: base.Add(35 * time.Second)}
	if err := scanState(p, &out); err != nil {
		t.Fatal(err)
	}
	if out.StateRows != 5 || out.HealthySpanSeconds != 34 || out.ChecksumFailures != 1 || len(out.QuietBookIntervals) != 1 || out.QuietBookIntervals[0].DurationNS != int64(2*time.Second) {
		t.Fatal(out)
	}
}

func TestOutputParityUsesBothFiles(t *testing.T) {
	root := t.TempDir()
	live, replay := filepath.Join(root, "live"), filepath.Join(root, "replay")
	for _, dir := range []string{live, replay} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"normalized.jsonl", "state.jsonl"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("same\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	var p parity
	if err := compareOutputs(live, replay, &p); err != nil || !p.Checked || !p.Equal {
		t.Fatalf("parity=%+v err=%v", p, err)
	}
	if err := os.WriteFile(filepath.Join(replay, "state.jsonl"), []byte("different\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := compareOutputs(live, replay, &p); err != nil || !p.Checked || p.Equal {
		t.Fatalf("parity=%+v err=%v", p, err)
	}
}

package soakreport

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAtomicReplacementAndInterruptedRun(t *testing.T) {
	path := Path(t.TempDir())
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	r := Report{RunID: "run-one", Status: Running, CodeRevision: "abc123", StartedAt: start, UpdatedAt: start, EventCounts: map[string]uint64{"ws:book:update": 4}}
	if err := Write(path, r); err != nil {
		t.Fatal(err)
	}
	r.EventCounts["ws:book:update"] = 9
	if err := Write(path, r); err != nil {
		t.Fatal(err)
	}
	if err := MarkInterrupted(path, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Incomplete || got.CodeRevision != "abc123" || got.EventCounts["ws:book:update"] != 9 || len(got.Warnings) != 1 {
		t.Fatalf("lost interrupted evidence: %+v", got)
	}
	if err := MarkInterrupted(path, start.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err = Read(path)
	if err != nil || len(got.Warnings) != 1 {
		t.Fatalf("interruption marking was not idempotent: %+v %v", got, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".run-report-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary report files remain: %v %v", matches, err)
	}
}

func TestFailedReplacementPreservesPreviousReport(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	if err := Write(path, Report{RunID: "original", Status: Running}); err != nil {
		t.Fatal(err)
	}
	// Renaming a file over a directory fails on both Unix and Windows.
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Write(blocked, Report{RunID: "replacement", Status: Complete}); err == nil {
		t.Fatal("expected replacement failure")
	}
	got, err := Read(path)
	if err != nil || got.RunID != "original" || got.Status != Running {
		t.Fatalf("previous report changed: %+v %v", got, err)
	}
}

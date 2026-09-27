package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ts3/internal/processor"
	"ts3/internal/record"
	"ts3/internal/soakreport"
	"ts3/internal/telemetry"
)

func TestRunReportStartsBeforeCaptureAndSurvivesInterruption(t *testing.T) {
	dir := t.TempDir()
	runID := "interrupted"
	writer, err := record.NewWriter(dir, runID)
	if err != nil {
		t.Fatal(err)
	}
	proc, err := processor.New(filepath.Join(dir, runID), true)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	metrics := telemetry.New(runID, start)
	base := soakreport.Report{RunID: runID, CodeRevision: "test-commit", StartedAt: start.UTC()}
	if err := writeRunReport(dir, runID, base, metrics, writer, proc, start, soakreport.Running); err != nil {
		t.Fatal(err)
	}
	path := soakreport.Path(filepath.Join(dir, runID))
	r, err := soakreport.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != soakreport.Running || r.CodeRevision != "test-commit" || r.SequenceGaps != nil || r.EvidenceFiles[runID+"-000001.raw"] == 0 {
		t.Fatalf("missing initial evidence: %+v", r)
	}
	if err := proc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(false); err != nil {
		t.Fatal(err)
	}
	if err := markPriorInterrupted(dir); err != nil {
		t.Fatal(err)
	}
	r, err = soakreport.Read(path)
	if err != nil || r.Status != soakreport.Incomplete || r.CodeRevision != "test-commit" {
		t.Fatalf("interrupted evidence changed: %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(dir, runID+"-000001.raw")); err != nil {
		t.Fatalf("raw evidence lost: %v", err)
	}
}

func TestEvidenceFilesExcludeSimilarRunIDs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"run-000001.raw", "run-extra-000001.raw"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("raw"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := evidenceFiles(dir, "run")
	if err != nil || len(files) != 1 || files["run-000001.raw"] != 3 {
		t.Fatalf("wrong run evidence: %v %v", files, err)
	}
}

package v3

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ts3/internal/v1"
	"ts3/internal/v2"
)

func inferenceArtifact(t *testing.T) Artifact {
	t.Helper()
	start := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	a := Artifact{Version: ArtifactVersion, ModelType: "l2_logistic",
		FeatureSchema: FeatureSchema, FeatureNames: FeatureNames,
		TargetVersion: TargetVersion, CostVersion: CostVersion,
		V1ConfigSHA: "v1", V2ConfigSHA: "v2", DatasetSHA: "dataset",
		TrainingRevision: "commit", TrainingStart: start, TrainingEnd: start.Add(time.Hour),
		LastTrainOrdinal: 100, TrainingRows: 100, Lambda: 1,
		CalibrationStatus: "UNAVAILABLE_INSUFFICIENT_EVIDENCE"}
	for j := range a.Scale {
		a.Scale[j] = 1
		a.Min[j] = -100
		a.Max[j] = 100
	}
	a.Coefficient[0] = 0.1
	if err := a.Seal(); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSharedInferenceFailsClosedAndRepeats(t *testing.T) {
	i, _ := pairedFixture()
	a := inferenceArtifact(t)
	first := EvaluateIntent(i, &a)
	second := EvaluateIntent(i, &a)
	if first.Action != v1.NoTrade || first.ModelStatus != "AVAILABLE_UNCALIBRATED" ||
		first.RawLogitMicro == nil || *first.RawLogitMicro != *second.RawLogitMicro ||
		first.ModelArtifact != a.SHA256 {
		t.Fatalf("unexpected inference %+v", first)
	}
	i.Signals[2].Status = v2.Unavailable
	if got := EvaluateIntent(i, &a); got.Action != v1.NoTrade || got.RawLogitMicro != nil ||
		got.Reason != "FEATURE_UNAVAILABLE" {
		t.Fatalf("unavailable signal produced model output: %+v", got)
	}
	i, _ = pairedFixture()
	i.Baseline.Features.Return60MicroBps = 1_000_000_000
	if got := EvaluateIntent(i, &a); got.Reason != "FEATURE_OUT_OF_RANGE" || got.RawLogitMicro != nil {
		t.Fatalf("OOD feature accepted: %+v", got)
	}
	i, _ = pairedFixture()
	a.Coefficient[0]++
	if got := EvaluateIntent(i, &a); got.Reason != "MODEL_INCOMPATIBLE" {
		t.Fatalf("corrupt artifact accepted: %+v", got)
	}
	if got := EvaluateIntent(i, nil); got.Reason != "MODEL_UNAVAILABLE" || got.Action != v1.NoTrade {
		t.Fatalf("missing artifact produced action: %+v", got)
	}
}

func TestJournalPersistsIntentBeforeAck(t *testing.T) {
	dir := t.TempDir()
	a := inferenceArtifact(t)
	j, err := NewJournal(dir, "run", a)
	if err != nil {
		t.Fatal(err)
	}
	i, _ := pairedFixture()
	intent := EvaluateIntent(i, &a)
	if err := j.Append(intent, i.AsOfTime.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := j.Flush(false); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	s := j.Snapshot()
	if s.Intents != 1 || s.Acknowledged != 1 || s.Reasons["UNCALIBRATED_NO_EDGE"] != 1 {
		t.Fatalf("journal snapshot %+v", s)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "v3-intents.jsonl")); err != nil || len(b) == 0 {
		t.Fatalf("missing durable intent: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "v3-live-acks.jsonl")); err != nil || len(b) == 0 {
		t.Fatalf("missing durable acknowledgment: %v", err)
	}
}

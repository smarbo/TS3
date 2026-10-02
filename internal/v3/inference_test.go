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
		TrainingRunID:    "historical-training-run",
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
		first.RawLogitMicro == nil || *first.RawLogitMicro != 1_200_000 ||
		*first.RawLogitMicro != *second.RawLogitMicro ||
		first.ModelArtifact != a.SHA256 {
		t.Fatalf("unexpected inference %+v", first)
	}
	inSample := i
	inSample.AsOfTime = a.TrainingEnd
	inSample.Baseline.AsOfTime = a.TrainingEnd
	if got := EvaluateIntent(inSample, &a); got.Reason != "IN_SAMPLE_PERIOD" || got.RawLogitMicro != nil {
		t.Fatalf("in-sample score exposed: %+v", got)
	}
	// An artifact trained from a five-minute outcome is also unavailable
	// between its last decision and that outcome's exit. TrainingEnd is
	// the last label availability time, not merely the last decision time.
	inSample.AsOfTime = a.TrainingEnd.Add(-time.Minute)
	inSample.Baseline.AsOfTime = inSample.AsOfTime
	if got := EvaluateIntent(inSample, &a); got.Reason != "IN_SAMPLE_PERIOD" || got.RawLogitMicro != nil {
		t.Fatalf("pre-exit score exposed: %+v", got)
	}
	sameRun := i
	sameRun.RunID = a.TrainingRunID
	sameRun.Baseline.RunID = a.TrainingRunID
	if got := EvaluateIntent(sameRun, &a); got.Reason != "IN_SAMPLE_PERIOD" || got.RawLogitMicro != nil {
		t.Fatalf("same-source retroactive score exposed: %+v", got)
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

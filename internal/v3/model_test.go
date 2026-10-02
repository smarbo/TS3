package v3

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func syntheticRows(n int) []Row {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]Row, n)
	for j := range rows {
		at := start.Add(time.Duration(j) * time.Minute)
		x := float64((j % 20) - 10)
		rows[j] = Row{DecisionOrdinal: uint64(j + 1), DecisionTime: at,
			EntryTime: at.Add(2 * time.Second), ExitTime: at.Add(302 * time.Second),
			Features: [7]float64{x, x * 1.000001, float64(j % 5), 0.1, x / 10, 0, -1},
			DepthUp:  x > 0, LongNetBps: -50, Regime: "NORMAL"}
	}
	return rows
}

func TestTrainingOnlyScaleAndCorrelatedFixture(t *testing.T) {
	train := syntheticRows(100)
	s, beta, err := FitLogistic(train, 10, -1)
	if err != nil {
		t.Fatal(err)
	}
	withFuture := append(append([]Row(nil), train...), Row{Features: [7]float64{1e9}})
	global, err := FitStandardizer(withFuture, -1)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mean[0] == global.Mean[0] {
		t.Fatal("future outlier failed to expose global scaling leakage")
	}
	if math.Abs(beta[1]-beta[2]) > 0.01 {
		t.Fatalf("redundant correlated predictors got misleadingly different weights: %v", beta)
	}
	_, withoutOne, err := FitLogistic(train, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	full := sigmoid(Score(beta, s, train[90], -1))
	sAblated, _ := FitStandardizer(train, 1)
	ablated := sigmoid(Score(withoutOne, sAblated, train[90], 1))
	if math.Abs(full-ablated) > 0.03 {
		t.Fatalf("removing redundant feature changed prediction too much: %.6f vs %.6f", full, ablated)
	}
}

func TestArtifactHashSchemaAndOOD(t *testing.T) {
	rows := syntheticRows(100)
	s, beta, err := FitLogistic(rows, 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	a := Artifact{Version: ArtifactVersion, ModelType: "l2_logistic",
		FeatureSchema: FeatureSchema, FeatureNames: FeatureNames, TargetVersion: TargetVersion,
		CostVersion: CostVersion, V1ConfigSHA: "v1", V2ConfigSHA: "v2", DatasetSHA: "dataset",
		TrainingRevision: "commit", TrainingStart: rows[0].DecisionTime,
		TrainingEnd: rows[len(rows)-1].DecisionTime, LastTrainOrdinal: rows[len(rows)-1].DecisionOrdinal,
		TrainingRows: len(rows), Lambda: 1, Mean: s.Mean, Scale: s.Scale,
		Intercept: beta[0], CalibrationStatus: "UNAVAILABLE_INSUFFICIENT_EVIDENCE"}
	for j := range FeatureNames {
		a.Coefficient[j] = beta[j+1]
		a.Min[j], a.Max[j] = rows[0].Features[j], rows[0].Features[j]
		for _, row := range rows[1:] {
			a.Min[j] = math.Min(a.Min[j], row.Features[j])
			a.Max[j] = math.Max(a.Max[j], row.Features[j])
		}
	}
	if err := a.Seal(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "artifact.json")
	b, _ := json.Marshal(a)
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadArtifact(path, "v1", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.RawScore(rows[90].Features); err != nil {
		t.Fatal(err)
	}
	ood := rows[90].Features
	ood[0] = 1e9
	if _, err := loaded.RawScore(ood); err == nil {
		t.Fatal("out-of-range feature accepted")
	}
	if _, err := LoadArtifact(path, "wrong", "v2"); err == nil {
		t.Fatal("upstream schema/config mismatch accepted")
	}
	loaded.Coefficient[0]++
	if err := loaded.Validate(); err == nil {
		t.Fatal("corrupt coefficient accepted")
	}
	loaded = a
	loaded.FeatureNames[0] = "future_return"
	if err := loaded.Validate(); err == nil {
		t.Fatal("feature-schema mismatch accepted")
	}
}

func TestFutureFittedRegimeFixture(t *testing.T) {
	train := syntheticRows(10)
	for j := range train {
		train[j].Features[0] = 0
		train[j].DepthUp = false
	}
	validationStart := train[0].DecisionTime.Add(20 * time.Minute)
	validation := make([]Row, 100)
	for j := range validation {
		at := validationStart.Add(time.Duration(j) * time.Minute)
		validation[j] = Row{DecisionOrdinal: uint64(100 + j), DecisionTime: at,
			EntryTime: at.Add(2 * time.Second), ExitTime: at.Add(302 * time.Second),
			Features: [7]float64{float64(j)}, DepthUp: j >= 50}
	}
	all := append(append([]Row(nil), train...), validation...)
	median := func(rows []Row) float64 {
		values := make([]float64, len(rows))
		for j, row := range rows {
			values[j] = row.Features[0]
		}
		sort.Float64s(values)
		return values[len(values)/2]
	}
	localThreshold := median(train)
	futureThreshold := median(all)
	localCorrect, futureCorrect := 0, 0
	for _, row := range validation {
		if (row.Features[0] > localThreshold) == row.DepthUp {
			localCorrect++
		}
		if (row.Features[0] > futureThreshold) == row.DepthUp {
			futureCorrect++
		}
	}
	if futureCorrect < 90 || localCorrect > 60 {
		t.Fatalf("fixture failed to demonstrate future-fit illusion: future %d, local %d", futureCorrect, localCorrect)
	}
	if _, _, err := FitLogisticFold(all, validationStart, 1, -1); err == nil {
		t.Fatal("pipeline accepted future-fitted regime/scaling rows")
	}
}

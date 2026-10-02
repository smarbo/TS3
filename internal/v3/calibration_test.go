package v3

import (
	"testing"
	"time"
)

func TestCalibrationRejectsInSampleAndFutureFit(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	predictions := make([]OOFPrediction, 60)
	for j := range predictions {
		at := start.Add(6*time.Hour + time.Duration(j)*time.Minute)
		predictions[j] = OOFPrediction{DecisionOrdinal: uint64(j + 1), DecisionTime: at,
			ExitTime: at.Add(5 * time.Minute), ValidationStart: start.Add(6 * time.Hour),
			ValidationEnd: start.Add(8 * time.Hour), TrainMaxExit: start.Add(5 * time.Hour),
			Fold: 0, Score: float64(j%2)*2 - 1, Label: j%2 == 1}
	}
	if err := ValidateOOF(predictions); err != nil {
		t.Fatal(err)
	}
	if eligible, _ := CalibrationEligible(predictions); eligible {
		t.Fatal("one-day tiny calibration sample passed")
	}
	if _, err := FitPlatt(predictions, start.Add(7*time.Hour)); err == nil {
		t.Fatal("calibrator used outcomes from its evaluation interval")
	}
	if _, err := FitPlatt(predictions, start.Add(9*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A future-fitted regime threshold or an in-sample calibration model
	// would carry a fit end inside validation. The same OOF barrier rejects it.
	contaminated := append([]OOFPrediction(nil), predictions...)
	contaminated[0].TrainMaxExit = start.Add(7 * time.Hour)
	if err := ValidateOOF(contaminated); err == nil {
		t.Fatal("future-fitted transform accepted as out-of-fold")
	}
}

func TestInSampleCalibrationIllusionFailsLater(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	makeOOF := func(index int, at time.Time, flipped bool) OOFPrediction {
		label := index%2 == 1
		if flipped {
			label = !label
		}
		return OOFPrediction{DecisionOrdinal: uint64(index + 1), DecisionTime: at,
			ExitTime: at.Add(5 * time.Minute), ValidationStart: at.Truncate(2 * time.Hour),
			ValidationEnd: at.Truncate(2 * time.Hour).Add(2 * time.Hour),
			TrainMaxExit: start.Add(-time.Minute), Fold: 0,
			Score: float64(index%2)*4 - 2, Label: label}
	}
	var early, later []OOFPrediction
	for j := 0; j < 60; j++ {
		early = append(early, makeOOF(j, start.Add(6*time.Hour+time.Duration(j)*time.Minute), false))
		later = append(later, makeOOF(j+60, start.Add(12*time.Hour+time.Duration(j)*time.Minute), true))
	}
	model, err := FitPlatt(early, start.Add(12*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	meanLoss := func(rows []OOFPrediction) float64 {
		loss := 0.0
		for _, row := range rows {
			loss += LogLoss(row.Label, model.Intercept+model.Slope*row.Score)
		}
		return loss / float64(len(rows))
	}
	if meanLoss(early) >= 0.1 || meanLoss(later) <= 2 {
		t.Fatalf("fixture failed to expose calibration reversal: early %.3f, later %.3f",
			meanLoss(early), meanLoss(later))
	}
	if eligible, _ := CalibrationEligible(append(early, later...)); eligible {
		t.Fatal("short correlated calibration history passed the evidence gate")
	}
}

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

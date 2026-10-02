package v3

import (
	"errors"
	"math"
	"sort"
	"time"
)

// OOFPrediction is generated on a validation fold only after the model has
// been trained on earlier, purged observations. Its score is uncalibrated.
type OOFPrediction struct {
	DecisionOrdinal uint64
	DecisionTime    time.Time
	ExitTime        time.Time
	ValidationStart time.Time
	ValidationEnd   time.Time
	TrainMaxExit    time.Time
	Fold            int
	Score           float64
	Label           bool
}

func ValidateOOF(predictions []OOFPrediction) error {
	if len(predictions) == 0 {
		return errors.New("empty OOF predictions")
	}
	for j, p := range predictions {
		if p.DecisionOrdinal == 0 || p.DecisionTime.IsZero() ||
			p.DecisionTime.Before(p.ValidationStart) || !p.DecisionTime.Before(p.ValidationEnd) ||
			!p.ExitTime.After(p.DecisionTime) ||
			!p.TrainMaxExit.Before(p.ValidationStart) || !finite(p.Score) {
			return errors.New("in-sample or temporally invalid OOF prediction")
		}
		if j > 0 && (p.DecisionOrdinal <= predictions[j-1].DecisionOrdinal ||
			!p.DecisionTime.After(predictions[j-1].DecisionTime) ||
			p.Fold < predictions[j-1].Fold) {
			return errors.New("OOF predictions not chronological")
		}
	}
	return nil
}

type Platt struct {
	Intercept float64   `json:"intercept"`
	Slope     float64   `json:"slope"`
	FitUntil  time.Time `json:"fit_until"`
}

// FitPlatt uses only completed OOF predictions strictly before the requested
// evaluation start. It does not itself declare calibration acceptable.
func FitPlatt(predictions []OOFPrediction, evaluationStart time.Time) (Platt, error) {
	if err := ValidateOOF(predictions); err != nil {
		return Platt{}, err
	}
	positive := 0
	for _, p := range predictions {
		if !p.ExitTime.Before(evaluationStart) || !p.DecisionTime.Before(evaluationStart) {
			return Platt{}, errors.New("calibration uses future or overlapping outcomes")
		}
		if p.Label {
			positive++
		}
	}
	if positive == 0 || positive == len(predictions) {
		return Platt{}, errors.New("single-class calibration history")
	}
	a := math.Log((float64(positive) + 1) / (float64(len(predictions)-positive) + 1))
	b := 0.0
	for iteration := 0; iteration < 50; iteration++ {
		g0, g1 := 0.0, b // unit L2 slope penalty
		h00, h01, h11 := 0.0, 0.0, 1.0
		for _, p := range predictions {
			prob := sigmoid(a + b*p.Score)
			y := 0.0
			if p.Label {
				y = 1
			}
			d := prob - y
			w := prob * (1 - prob)
			g0 += d
			g1 += d * p.Score
			h00 += w
			h01 += w * p.Score
			h11 += w * p.Score * p.Score
		}
		det := h00*h11 - h01*h01
		if det <= 1e-12 {
			return Platt{}, errors.New("singular calibration fit")
		}
		step0 := (h11*g0 - h01*g1) / det
		step1 := (-h01*g0 + h00*g1) / det
		a -= step0
		b -= step1
		if !finite(a) || !finite(b) {
			return Platt{}, errors.New("nonfinite calibration fit")
		}
		if math.Max(math.Abs(step0), math.Abs(step1)) < 1e-10 {
			return Platt{Intercept: a, Slope: b, FitUntil: predictions[len(predictions)-1].ExitTime}, nil
		}
	}
	return Platt{}, errors.New("calibration fit did not converge")
}

// CalibrationEligible is a conservative gate on effective, non-overlapping
// evidence. It is independent of apparent Brier/ECE performance.
func CalibrationEligible(predictions []OOFPrediction) (bool, string) {
	if err := ValidateOOF(predictions); err != nil {
		return false, err.Error()
	}
	selected := make([]OOFPrediction, 0, len(predictions))
	var priorExit time.Time
	days := map[string]struct{}{}
	positive := 0
	for _, p := range predictions {
		if !priorExit.IsZero() && p.DecisionTime.Before(priorExit) {
			continue
		}
		selected = append(selected, p)
		priorExit = p.ExitTime
		days[p.DecisionTime.UTC().Format("2006-01-02")] = struct{}{}
		if p.Label {
			positive++
		}
	}
	if len(selected) < 1000 || len(days) < 30 || positive < 200 || len(selected)-positive < 200 {
		return false, "insufficient independent episodes, days or class counts"
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Score < selected[j].Score })
	for bucket := 0; bucket < 5; bucket++ {
		start := bucket * len(selected) / 5
		end := (bucket + 1) * len(selected) / 5
		if end-start < 200 {
			return false, "calibration bucket below effective count gate"
		}
	}
	return true, "minimum counts met; performance and uncertainty still required"
}

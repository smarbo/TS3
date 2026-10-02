package v3

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

type Fold struct {
	Index           int       `json:"index"`
	TrainStart      time.Time `json:"train_start"`
	TrainCutoff     time.Time `json:"train_cutoff"`
	PurgeStart      time.Time `json:"purge_start"`
	ValidationStart time.Time `json:"validation_start"`
	ValidationEnd   time.Time `json:"validation_end"`
	EmbargoEnd      time.Time `json:"embargo_end"`
	Train           []int     `json:"-"`
	Validation      []int     `json:"-"`
}

// DevelopmentFolds uses fixed expanding history and two-hour validation
// windows. An actual outcome touching validation is purged even if its nominal
// five-minute horizon appears safe.
func DevelopmentFolds(rows []Row, firstTick time.Time) ([]Fold, error) {
	if firstTick.IsZero() || len(rows) == 0 {
		return nil, errors.New("empty V3 chronology")
	}
	for j := 1; j < len(rows); j++ {
		if !rows[j].DecisionTime.After(rows[j-1].DecisionTime) ||
			rows[j].DecisionOrdinal <= rows[j-1].DecisionOrdinal {
			return nil, errors.New("V3 rows not in strict decision order")
		}
	}
	var folds []Fold
	var priorEmbargo [][2]time.Time
	for j, hour := range []int{6, 9, 12, 15, 18} {
		start := firstTick.Add(time.Duration(hour) * time.Hour)
		end := start.Add(2 * time.Hour)
		f := Fold{Index: j, TrainStart: firstTick, TrainCutoff: start.Add(-6 * time.Minute),
			PurgeStart: start.Add(-6 * time.Minute), ValidationStart: start,
			ValidationEnd: end, EmbargoEnd: end.Add(30 * time.Minute)}
		for k, row := range rows {
			if row.DecisionTime.Before(firstTick) {
				return nil, fmt.Errorf("row %d before first tick", k)
			}
			if row.DecisionTime.Before(f.TrainCutoff) && row.ExitTime.Before(start) {
				embargoed := false
				for _, interval := range priorEmbargo {
					if !row.DecisionTime.Before(interval[0]) && row.DecisionTime.Before(interval[1]) {
						embargoed = true
						break
					}
				}
				if !embargoed {
					f.Train = append(f.Train, k)
				}
			} else if !row.DecisionTime.Before(start) && row.DecisionTime.Before(end) {
				f.Validation = append(f.Validation, k)
			}
		}
		if len(f.Train) == 0 || len(f.Validation) == 0 {
			return nil, fmt.Errorf("empty V3 development fold %d", j)
		}
		folds = append(folds, f)
		priorEmbargo = append(priorEmbargo, [2]time.Time{end, f.EmbargoEnd})
	}
	return folds, nil
}

func Select(rows []Row, indices []int) []Row {
	selected := make([]Row, len(indices))
	for j, k := range indices {
		selected[j] = rows[k]
	}
	return selected
}

// Nonoverlap selects earliest decisions, then skips any episode whose entry
// precedes the previous selected exit. It is a descriptive effective count.
func Nonoverlap(rows []Row) int {
	if len(rows) == 0 {
		return 0
	}
	ordered := append([]Row(nil), rows...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].DecisionOrdinal < ordered[j].DecisionOrdinal
	})
	count := 0
	var lastExit time.Time
	for _, row := range ordered {
		if !lastExit.IsZero() && row.EntryTime.Before(lastExit) {
			continue
		}
		count++
		lastExit = row.ExitTime
	}
	return count
}

type Metrics struct {
	Rows         int     `json:"rows"`
	Positive     int     `json:"positive"`
	Negative     int     `json:"negative"`
	Nonoverlap   int     `json:"nonoverlapping_episodes"`
	LogLoss      float64 `json:"log_loss"`
	Brier        float64 `json:"brier"`
	GrossLongBps float64 `json:"gross_long_bps_mean"`
	NetLongBps   float64 `json:"net_long_bps_mean"`
	VolatileRows int     `json:"volatile_rows"`
	NormalRows   int     `json:"normal_rows"`
}

func Evaluate(rows []Row, scores []float64) (Metrics, error) {
	if len(rows) == 0 || len(scores) != len(rows) {
		return Metrics{}, errors.New("V3 metric population mismatch")
	}
	m := Metrics{Rows: len(rows), Nonoverlap: Nonoverlap(rows)}
	for j, row := range rows {
		if !finite(scores[j]) {
			return Metrics{}, errors.New("nonfinite V3 score")
		}
		if row.DepthUp {
			m.Positive++
		} else {
			m.Negative++
		}
		p := sigmoid(scores[j])
		y := 0.0
		if row.DepthUp {
			y = 1
		}
		m.LogLoss += LogLoss(row.DepthUp, scores[j])
		m.Brier += math.Pow(p-y, 2)
		m.GrossLongBps += row.LongGrossMidBps
		m.NetLongBps += row.LongNetBps
		if row.Regime == "VOLATILE" {
			m.VolatileRows++
		} else if row.Regime == "NORMAL" {
			m.NormalRows++
		}
	}
	n := float64(len(rows))
	m.LogLoss /= n
	m.Brier /= n
	m.GrossLongBps /= n
	m.NetLongBps /= n
	return m, nil
}

func BaseRateScore(train []Row) (float64, error) {
	if len(train) == 0 {
		return 0, errors.New("empty V3 base-rate training")
	}
	positive := 0
	for _, row := range train {
		if row.DepthUp {
			positive++
		}
	}
	return math.Log((float64(positive) + 1) / (float64(len(train)-positive) + 1)), nil
}

package v3

import (
	"testing"
	"time"
)

func TestPurgedExpandingFoldsAndEmbargo(t *testing.T) {
	rows := syntheticRows(24 * 60)
	first := rows[0].DecisionTime
	folds, err := DevelopmentFolds(rows, first)
	if err != nil || len(folds) != 5 {
		t.Fatalf("folds %d: %v", len(folds), err)
	}
	for _, f := range folds {
		for _, k := range f.Train {
			row := rows[k]
			if !row.DecisionTime.Before(f.TrainCutoff) || !row.ExitTime.Before(f.ValidationStart) {
				t.Fatalf("fold %d leaked training row %d", f.Index, k)
			}
		}
		for _, k := range f.Validation {
			if rows[k].DecisionTime.Before(f.ValidationStart) ||
				!rows[k].DecisionTime.Before(f.ValidationEnd) {
				t.Fatalf("fold %d validation boundary error", f.Index)
			}
		}
	}
	if Nonoverlap(rows[:20]) != 4 {
		t.Fatalf("unexpected effective episode count: %d", Nonoverlap(rows[:20]))
	}
	// The first validation ends at hour 8. Its 30-minute post-validation
	// embargo must not enter the next fold's expanded training history.
	for _, k := range folds[1].Train {
		at := rows[k].DecisionTime
		if !at.Before(first.Add(8*time.Hour)) && at.Before(first.Add(8*time.Hour+30*time.Minute)) {
			t.Fatal("embargo row entered later training")
		}
	}
	poison := append([]Row(nil), rows...)
	poison[0].ExitTime = first.Add(7 * time.Hour)
	poisonFolds, err := DevelopmentFolds(poison, first)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range poisonFolds[0].Train {
		if k == 0 {
			t.Fatal("future-overlapping label entered training")
		}
	}
}

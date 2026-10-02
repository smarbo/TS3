package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"ts3/internal/domain"
	"ts3/internal/v1"
	"ts3/internal/v3"
)

type datasetReport struct {
	AnalysisRevision    string `json:"analysis_revision"`
	FullCommittedPrefix bool   `json:"full_committed_prefix"`
	SourceRunID         string `json:"source_run_id"`
	SourceManifestSHA   string `json:"source_manifest_sha256"`
	V0NormalizedSHA     string `json:"v0_normalized_sha256"`
	V0StateSHA          string `json:"v0_state_sha256"`
	DatasetSHA          string `json:"dataset_sha256"`
	Rows                int    `json:"rows"`
	V1IntentSHA         string `json:"v1_intent_sha256"`
	V2IntentSHA         string `json:"v2_intent_sha256"`
	V1Research          struct {
		FirstTick time.Time `json:"first_tick"`
	} `json:"v1_research"`
}

type control struct {
	Episodes int     `json:"episodes"`
	Actions  int     `json:"actions"`
	SumNet   float64 `json:"sum_net_bps"`
}

type foldResult struct {
	Index                  int                `json:"index"`
	TrainStart             time.Time          `json:"train_start"`
	TrainCutoff            time.Time          `json:"train_cutoff"`
	ValidationStart        time.Time          `json:"validation_start"`
	ValidationEnd          time.Time          `json:"validation_end"`
	EmbargoEnd             time.Time          `json:"embargo_end"`
	TrainRows              int                `json:"train_rows"`
	TrainNonoverlap        int                `json:"train_nonoverlap"`
	TrainPositive          int                `json:"train_positive"`
	TrainLastOrdinal       uint64             `json:"train_last_ordinal,string"`
	TrainMaxLabelExit      time.Time          `json:"train_max_label_exit"`
	ValidationFirstOrdinal uint64             `json:"validation_first_ordinal,string"`
	ValidationLastOrdinal  uint64             `json:"validation_last_ordinal,string"`
	Validation             v3.Metrics         `json:"validation"`
	Null                   v3.Metrics         `json:"null"`
	Logistic1              v3.Metrics         `json:"logistic_lambda_1"`
	Logistic10             v3.Metrics         `json:"logistic_lambda_10"`
	Coefficients1          [8]float64         `json:"coefficients_lambda_1"`
	Coefficients10         [8]float64         `json:"coefficients_lambda_10"`
	Controls               map[string]control `json:"controls"`
}

type summary struct {
	SchemaVersion       int                   `json:"schema_version"`
	DatasetSHA          string                `json:"dataset_sha256"`
	DatasetRevision     string                `json:"dataset_revision"`
	TrainingRevision    string                `json:"training_revision"`
	GoVersion           string                `json:"go_version"`
	Rows                int                   `json:"rows"`
	Nonoverlap          int                   `json:"nonoverlapping_episodes"`
	Positive            int                   `json:"positive"`
	Negative            int                   `json:"negative"`
	LongNetPositive     int                   `json:"long_net_positive"`
	SourceManifestSHA   string                `json:"source_manifest_sha256"`
	V1IntentSHA         string                `json:"v1_intent_sha256"`
	V2IntentSHA         string                `json:"v2_intent_sha256"`
	Folds               []foldResult          `json:"folds"`
	AggregateLogLoss    map[string]float64    `json:"aggregate_log_loss"`
	AggregateBrier      map[string]float64    `json:"aggregate_brier"`
	SelectedLambda      float64               `json:"selected_development_lambda"`
	SelectedVsNull      float64               `json:"selected_minus_null_log_loss"`
	AblationLogLoss     map[string]float64    `json:"ablation_log_loss"`
	RegimeMetrics       map[string]v3.Metrics `json:"selected_regime_metrics"`
	CalibrationEligible bool                  `json:"calibration_count_gate"`
	CalibrationReason   string                `json:"calibration_reason"`
	ArtifactSHA         string                `json:"artifact_sha256"`
	Conclusion          string                `json:"conclusion"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func revision() string {
	info, _ := debug.ReadBuildInfo()
	if info == nil {
		return "unknown"
	}
	rev, dirty := "unknown", false
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			rev = s.Value
		}
		if s.Key == "vcs.modified" {
			dirty = s.Value == "true"
		}
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}

func readDataset(dir string) ([]v3.Row, datasetReport, error) {
	var report datasetReport
	b, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		return nil, report, err
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, report, err
	}
	if !report.FullCommittedPrefix {
		return nil, report, errors.New("V3 dataset was not generated from a complete committed prefix")
	}
	if report.SourceRunID == "kraken-v0-server-20260927T203224Z" &&
		(report.SourceManifestSHA != "8144ec61b4da66aa0e392df5e96b140a25dbaef7bbc6abdd015ca92ebf5bd7dc" ||
			report.V0NormalizedSHA != "06950e8b672a10949d97339c42fb294b72300544eb3eb64aba6f34e647e0e953" ||
			report.V0StateSHA != "200fb2785cdc278dce732d97a75162cb0b7fb2550e24ff45835b06cbc027f132" ||
			report.V1IntentSHA != "a0dde5ffab48f2e0624ec5b5580765124ea63fb8e2bf8a44d85bf39e9c597f5f" ||
			report.V2IntentSHA != "b43a39952c12348b7f340af79c2b36e0596f978a5709519d9c953bd6f55ef505") {
		return nil, report, errors.New("accepted V0/V1/V2 benchmark hash mismatch")
	}
	path := filepath.Join(dir, "rows.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, report, err
	}
	defer f.Close()
	h := sha256.New()
	rows := make([]v3.Row, 0, report.Rows)
	scanner := bufio.NewScanner(io.TeeReader(f, h))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var row v3.Row
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, report, err
		}
		if row.SchemaVersion != 1 || row.FeatureSchema != v3.FeatureSchema ||
			row.TargetVersion != v3.TargetVersion || row.CostVersion != v3.CostVersion ||
			row.SourceManifestSHA != report.SourceManifestSHA || row.RunID != report.SourceRunID {
			return nil, report, errors.New("V3 dataset row schema/provenance mismatch")
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, report, err
	}
	if len(rows) != report.Rows || hex.EncodeToString(h.Sum(nil)) != report.DatasetSHA {
		return nil, report, errors.New("V3 dataset hash/count mismatch")
	}
	return rows, report, nil
}

func countPositive(rows []v3.Row) int {
	n := 0
	for _, r := range rows {
		if r.DepthUp {
			n++
		}
	}
	return n
}

func predict(rows []v3.Row, score func(v3.Row) float64) []float64 {
	values := make([]float64, len(rows))
	for j, row := range rows {
		values[j] = score(row)
	}
	return values
}

func controls(rows []v3.Row) map[string]control {
	names := []string{"v1_policy", "v2_policy", "no_trade", "always_long", "momentum", "reversion", "random_full_frequency"}
	result := map[string]control{}
	for _, row := range rows {
		random := v1.Long
		seed := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:v3random", row.RunID, row.DecisionOrdinal)))
		if seed[0]&1 != 0 {
			random = v1.Short
		}
		actions := []v1.Action{row.V1Action, row.V2Action, v1.NoTrade,
			v1.Long, row.MomentumDirection, row.ReversionDirection, random}
		for j, name := range names {
			s := result[name]
			s.Episodes++
			if actions[j] == v1.Long || actions[j] == v1.Short {
				s.Actions++
				if actions[j] == v1.Long {
					s.SumNet += row.LongNetBps
				} else {
					s.SumNet += row.ShortNetBps
				}
			}
			result[name] = s
		}
	}
	return result
}

func run() error {
	datasetDir := flag.String("dataset", "", "completed V3 dataset directory")
	out := flag.String("out", "", "new training output directory")
	flag.Parse()
	if *datasetDir == "" || *out == "" {
		return errors.New("-dataset and -out required")
	}
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		return errors.New("V3 training output directory must not exist")
	}
	rows, source, err := readDataset(*datasetDir)
	if err != nil {
		return err
	}
	folds, err := v3.DevelopmentFolds(rows, source.V1Research.FirstTick)
	if err != nil {
		return err
	}
	result := summary{SchemaVersion: 1, DatasetSHA: source.DatasetSHA,
		DatasetRevision: source.AnalysisRevision, TrainingRevision: revision(),
		GoVersion: runtime.Version(), Rows: len(rows), Nonoverlap: v3.Nonoverlap(rows),
		Positive: countPositive(rows), Negative: len(rows) - countPositive(rows),
		SourceManifestSHA: source.SourceManifestSHA, V1IntentSHA: source.V1IntentSHA,
		V2IntentSHA: source.V2IntentSHA, AggregateLogLoss: map[string]float64{},
		AggregateBrier: map[string]float64{}, AblationLogLoss: map[string]float64{},
		RegimeMetrics: map[string]v3.Metrics{}}
	for _, row := range rows {
		if row.LongNetPositive {
			result.LongNetPositive++
		}
	}
	bestScores := make([]float64, len(rows))
	bestPresent := make([]bool, len(rows))
	totalValidation := 0
	for _, fold := range folds {
		train := v3.Select(rows, fold.Train)
		validation := v3.Select(rows, fold.Validation)
		base, err := v3.BaseRateScore(train)
		if err != nil {
			return err
		}
		nullMetrics, err := v3.Evaluate(validation, predict(validation, func(v3.Row) float64 { return base }))
		if err != nil {
			return err
		}
		s1, b1, err := v3.FitLogisticFold(train, fold.ValidationStart, 1, -1)
		if err != nil {
			return fmt.Errorf("fold %d lambda 1: %w", fold.Index, err)
		}
		s10, b10, err := v3.FitLogisticFold(train, fold.ValidationStart, 10, -1)
		if err != nil {
			return fmt.Errorf("fold %d lambda 10: %w", fold.Index, err)
		}
		m1, err := v3.Evaluate(validation, predict(validation, func(r v3.Row) float64 { return v3.Score(b1, s1, r, -1) }))
		if err != nil {
			return err
		}
		m10, err := v3.Evaluate(validation, predict(validation, func(r v3.Row) float64 { return v3.Score(b10, s10, r, -1) }))
		if err != nil {
			return err
		}
		fr := foldResult{Index: fold.Index, TrainStart: fold.TrainStart,
			TrainCutoff: fold.TrainCutoff, ValidationStart: fold.ValidationStart,
			ValidationEnd: fold.ValidationEnd, EmbargoEnd: fold.EmbargoEnd,
			TrainRows: len(train), TrainNonoverlap: v3.Nonoverlap(train),
			TrainPositive: countPositive(train), TrainLastOrdinal: train[len(train)-1].DecisionOrdinal,
			ValidationFirstOrdinal: validation[0].DecisionOrdinal,
			ValidationLastOrdinal:  validation[len(validation)-1].DecisionOrdinal,
			Validation:             nullMetrics, Null: nullMetrics, Logistic1: m1,
			Logistic10: m10, Coefficients1: b1, Coefficients10: b10,
			Controls: controls(validation)}
		for _, row := range train {
			if row.ExitTime.After(fr.TrainMaxLabelExit) {
				fr.TrainMaxLabelExit = row.ExitTime
			}
		}
		if !fr.TrainMaxLabelExit.Before(fold.ValidationStart) {
			return errors.New("purge failed before validation")
		}
		result.Folds = append(result.Folds, fr)
		for name, metric := range map[string]v3.Metrics{"null": nullMetrics, "lambda_1": m1, "lambda_10": m10} {
			result.AggregateLogLoss[name] += metric.LogLoss * float64(len(validation))
			result.AggregateBrier[name] += metric.Brier * float64(len(validation))
		}
		totalValidation += len(validation)
	}
	for name := range result.AggregateLogLoss {
		result.AggregateLogLoss[name] /= float64(totalValidation)
		result.AggregateBrier[name] /= float64(totalValidation)
	}
	result.SelectedLambda = 1
	if result.AggregateLogLoss["lambda_10"] < result.AggregateLogLoss["lambda_1"] {
		result.SelectedLambda = 10
	}
	selectedName := "lambda_1"
	if result.SelectedLambda == 10 {
		selectedName = "lambda_10"
	}
	result.SelectedVsNull = result.AggregateLogLoss[selectedName] - result.AggregateLogLoss["null"]
	for _, excluded := range []int{0, 1, 4, 5, 6, 3} {
		sum := 0.0
		for _, fold := range folds {
			train := v3.Select(rows, fold.Train)
			validation := v3.Select(rows, fold.Validation)
			s, beta, err := v3.FitLogisticFold(train, fold.ValidationStart, result.SelectedLambda, excluded)
			if err != nil {
				return err
			}
			m, err := v3.Evaluate(validation, predict(validation, func(r v3.Row) float64 { return v3.Score(beta, s, r, excluded) }))
			if err != nil {
				return err
			}
			sum += m.LogLoss * float64(len(validation))
		}
		result.AblationLogLoss[v3.FeatureNames[excluded]] = sum / float64(totalValidation)
	}
	// Development OOF scores are ordered by decision ordinal and remain raw.
	var oof []v3.OOFPrediction
	for _, fold := range folds {
		train := v3.Select(rows, fold.Train)
		s, beta, err := v3.FitLogisticFold(train, fold.ValidationStart, result.SelectedLambda, -1)
		if err != nil {
			return err
		}
		maxExit := time.Time{}
		for _, row := range train {
			if row.ExitTime.After(maxExit) {
				maxExit = row.ExitTime
			}
		}
		for _, k := range fold.Validation {
			row := rows[k]
			score := v3.Score(beta, s, row, -1)
			bestScores[k] = score
			bestPresent[k] = true
			oof = append(oof, v3.OOFPrediction{DecisionOrdinal: row.DecisionOrdinal,
				DecisionTime: row.DecisionTime, ExitTime: row.ExitTime,
				ValidationStart: fold.ValidationStart, ValidationEnd: fold.ValidationEnd,
				TrainMaxExit: maxExit, Fold: fold.Index, Score: score, Label: row.DepthUp})
		}
	}
	result.CalibrationEligible, result.CalibrationReason = v3.CalibrationEligible(oof)
	for _, regime := range []string{"NORMAL", "VOLATILE"} {
		var cohort []v3.Row
		var scores []float64
		for k, present := range bestPresent {
			if present && rows[k].Regime == regime {
				cohort = append(cohort, rows[k])
				scores = append(scores, bestScores[k])
			}
		}
		if len(cohort) > 0 {
			m, err := v3.Evaluate(cohort, scores)
			if err != nil {
				return err
			}
			result.RegimeMetrics[regime] = m
		}
	}
	// The artifact is engineering-only: no validated calibration or return
	// magnitude exists, so its live policy must abstain.
	s, beta, err := v3.FitLogistic(rows, result.SelectedLambda, -1)
	if err != nil {
		return err
	}
	a := v3.Artifact{Version: v3.ArtifactVersion, ModelType: "l2_logistic",
		FeatureSchema: v3.FeatureSchema, FeatureNames: v3.FeatureNames,
		TargetVersion: v3.TargetVersion, CostVersion: v3.CostVersion,
		V1ConfigSHA: rows[0].V1ConfigSHA, V2ConfigSHA: rows[0].V2ConfigSHA,
		DatasetSHA: source.DatasetSHA, TrainingRevision: revision(),
		TrainingStart: rows[0].DecisionTime, TrainingEnd: rows[len(rows)-1].DecisionTime,
		LastTrainOrdinal: rows[len(rows)-1].DecisionOrdinal, TrainingRows: len(rows),
		Lambda: result.SelectedLambda, Mean: s.Mean, Scale: s.Scale,
		Intercept: beta[0], CalibrationStatus: "UNAVAILABLE_INSUFFICIENT_EVIDENCE"}
	for j := range v3.FeatureNames {
		a.Coefficient[j] = beta[j+1]
		a.Min[j], a.Max[j] = rows[0].Features[j], rows[0].Features[j]
		for _, row := range rows[1:] {
			a.Min[j] = math.Min(a.Min[j], row.Features[j])
			a.Max[j] = math.Max(a.Max[j], row.Features[j])
		}
	}
	if err := a.Seal(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	result.ArtifactSHA = a.SHA256
	result.Conclusion = "Development-only uncalibrated directional model. No model-driven directional intent or economic claim is authorized."
	if err := os.Mkdir(*out, 0755); err != nil {
		return err
	}
	for name, value := range map[string]any{"artifact.json": a, "report.json": result} {
		b, err := domain.CanonicalJSON(value)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*out, name), append(b, '\n'), 0644); err != nil {
			return err
		}
	}
	return nil
}

package v3

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"ts3/internal/domain"
)

const ArtifactVersion = "v3.logistic.1"

type Artifact struct {
	Version           string     `json:"version"`
	ModelType         string     `json:"model_type"`
	FeatureSchema     string     `json:"feature_schema"`
	FeatureNames      [7]string  `json:"feature_names"`
	TargetVersion     string     `json:"target_version"`
	CostVersion       string     `json:"cost_version"`
	V1ConfigSHA       string     `json:"v1_config_sha256"`
	V2ConfigSHA       string     `json:"v2_config_sha256"`
	DatasetSHA        string     `json:"dataset_sha256"`
	TrainingRevision  string     `json:"training_revision"`
	TrainingStart     time.Time  `json:"training_start"`
	TrainingEnd       time.Time  `json:"training_end"`
	LastTrainOrdinal  uint64     `json:"last_train_ordinal,string"`
	TrainingRows      int        `json:"training_rows"`
	Lambda            float64    `json:"lambda"`
	Mean              [7]float64 `json:"mean"`
	Scale             [7]float64 `json:"scale"`
	Min               [7]float64 `json:"min"`
	Max               [7]float64 `json:"max"`
	Intercept         float64    `json:"intercept"`
	Coefficient       [7]float64 `json:"coefficient"`
	CalibrationStatus string     `json:"calibration_status"`
	SHA256            string     `json:"sha256,omitempty"`
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (a Artifact) digest() (string, error) {
	a.SHA256 = ""
	b, err := domain.CanonicalJSON(a)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func (a *Artifact) Seal() error {
	h, err := a.digest()
	if err != nil {
		return err
	}
	a.SHA256 = h
	return nil
}

func (a Artifact) Validate() error {
	if a.Version != ArtifactVersion || a.ModelType != "l2_logistic" ||
		a.FeatureSchema != FeatureSchema || a.FeatureNames != FeatureNames ||
		a.TargetVersion != TargetVersion || a.CostVersion != CostVersion ||
		a.V1ConfigSHA == "" || a.V2ConfigSHA == "" || a.DatasetSHA == "" ||
		a.TrainingRevision == "" || a.TrainingRevision == "unknown" ||
		a.TrainingStart.IsZero() || !a.TrainingEnd.After(a.TrainingStart) ||
		a.LastTrainOrdinal == 0 || a.TrainingRows < 2 ||
		(a.Lambda != 1 && a.Lambda != 10) ||
		a.CalibrationStatus != "UNAVAILABLE_INSUFFICIENT_EVIDENCE" ||
		!finite(a.Intercept) {
		return errors.New("invalid V3 artifact contract")
	}
	for j := range FeatureNames {
		if !finite(a.Mean[j]) || !finite(a.Scale[j]) || a.Scale[j] <= 0 ||
			!finite(a.Min[j]) || !finite(a.Max[j]) || a.Min[j] > a.Max[j] ||
			!finite(a.Coefficient[j]) {
			return fmt.Errorf("invalid V3 artifact parameter %d", j)
		}
	}
	h, err := a.digest()
	if err != nil || h != a.SHA256 {
		return errors.New("V3 artifact hash mismatch")
	}
	return nil
}

func LoadArtifact(path, expectedV1, expectedV2 string) (Artifact, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, err
	}
	var a Artifact
	if err := json.Unmarshal(b, &a); err != nil {
		return Artifact{}, err
	}
	if err := a.Validate(); err != nil {
		return Artifact{}, err
	}
	if a.V1ConfigSHA != expectedV1 || a.V2ConfigSHA != expectedV2 {
		return Artifact{}, errors.New("V3 artifact upstream config mismatch")
	}
	return a, nil
}

func sigmoid(x float64) float64 {
	if x >= 0 {
		z := math.Exp(-x)
		return 1 / (1 + z)
	}
	z := math.Exp(x)
	return z / (1 + z)
}

// RawScore is an uncalibrated logit. It must not populate a confidence field.
func (a Artifact) RawScore(features [7]float64) (float64, error) {
	if err := a.Validate(); err != nil {
		return 0, err
	}
	score := a.Intercept
	for j, v := range features {
		if !finite(v) {
			return 0, errors.New("nonfinite V3 inference feature")
		}
		if v < a.Min[j] || v > a.Max[j] {
			return 0, fmt.Errorf("V3 feature %s outside training range", FeatureNames[j])
		}
		score += a.Coefficient[j] * (v - a.Mean[j]) / a.Scale[j]
	}
	if !finite(score) {
		return 0, errors.New("nonfinite V3 score")
	}
	return score, nil
}

type Standardizer struct {
	Mean  [7]float64
	Scale [7]float64
}

func FitStandardizer(rows []Row, excluded int) (Standardizer, error) {
	if len(rows) < 2 {
		return Standardizer{}, errors.New("too few training rows")
	}
	var s Standardizer
	for _, row := range rows {
		for j, v := range row.Features {
			if j == excluded {
				continue
			}
			if !finite(v) {
				return s, errors.New("nonfinite training feature")
			}
			s.Mean[j] += v / float64(len(rows))
		}
	}
	for _, row := range rows {
		for j, v := range row.Features {
			if j == excluded {
				continue
			}
			d := v - s.Mean[j]
			s.Scale[j] += d * d / float64(len(rows))
		}
	}
	for j := range s.Scale {
		if j == excluded {
			s.Scale[j] = 1
			continue
		}
		s.Scale[j] = math.Sqrt(s.Scale[j])
		if s.Scale[j] == 0 {
			s.Scale[j] = 1
		}
	}
	return s, nil
}

func (s Standardizer) vector(row Row, excluded int) [8]float64 {
	x := [8]float64{1}
	for j, v := range row.Features {
		if j != excluded {
			x[j+1] = (v - s.Mean[j]) / s.Scale[j]
		}
	}
	return x
}

func solve8(a [8][8]float64, b [8]float64) ([8]float64, error) {
	for col := 0; col < 8; col++ {
		pivot := col
		for row := col + 1; row < 8; row++ {
			if math.Abs(a[row][col]) > math.Abs(a[pivot][col]) {
				pivot = row
			}
		}
		if math.Abs(a[pivot][col]) < 1e-12 {
			return [8]float64{}, errors.New("singular V3 Newton system")
		}
		a[col], a[pivot] = a[pivot], a[col]
		b[col], b[pivot] = b[pivot], b[col]
		for row := col + 1; row < 8; row++ {
			factor := a[row][col] / a[col][col]
			for k := col; k < 8; k++ {
				a[row][k] -= factor * a[col][k]
			}
			b[row] -= factor * b[col]
		}
	}
	var x [8]float64
	for row := 7; row >= 0; row-- {
		v := b[row]
		for k := row + 1; k < 8; k++ {
			v -= a[row][k] * x[k]
		}
		x[row] = v / a[row][row]
	}
	return x, nil
}

// FitLogistic minimizes summed log loss plus lambda/2 times squared slopes.
// excluded=-1 fits the full schema; a valid column index runs an ablation.
func FitLogistic(rows []Row, lambda float64, excluded int) (Standardizer, [8]float64, error) {
	if (lambda != 1 && lambda != 10) || excluded < -1 || excluded >= len(FeatureNames) {
		return Standardizer{}, [8]float64{}, errors.New("unregistered V3 fit")
	}
	positive := 0
	for _, row := range rows {
		if row.DepthUp {
			positive++
		}
	}
	if positive == 0 || positive == len(rows) {
		return Standardizer{}, [8]float64{}, errors.New("single-class V3 training fold")
	}
	s, err := FitStandardizer(rows, excluded)
	if err != nil {
		return s, [8]float64{}, err
	}
	var beta [8]float64
	beta[0] = math.Log((float64(positive) + 1) / (float64(len(rows)-positive) + 1))
	for iteration := 0; iteration < 50; iteration++ {
		var gradient [8]float64
		var hessian [8][8]float64
		for _, row := range rows {
			x := s.vector(row, excluded)
			z := 0.0
			for j := range beta {
				z += beta[j] * x[j]
			}
			prob := sigmoid(z)
			y := 0.0
			if row.DepthUp {
				y = 1
			}
			w := prob * (1 - prob)
			for j := range beta {
				gradient[j] += (prob - y) * x[j]
				for k := range beta {
					hessian[j][k] += w * x[j] * x[k]
				}
			}
		}
		for j := 1; j < 8; j++ {
			gradient[j] += lambda * beta[j]
			hessian[j][j] += lambda
		}
		step, err := solve8(hessian, gradient)
		if err != nil {
			return s, beta, err
		}
		maxStep := 0.0
		for j := range beta {
			beta[j] -= step[j]
			if !finite(beta[j]) {
				return s, beta, errors.New("nonfinite V3 coefficient")
			}
			maxStep = math.Max(maxStep, math.Abs(step[j]))
		}
		if maxStep < 1e-10 {
			return s, beta, nil
		}
	}
	return s, beta, errors.New("V3 Newton solver did not converge")
}

// FitLogisticFold is the only development-fold fitting entry point. It rejects
// any label or feature snapshot reaching the validation boundary, including a
// future-fitted regime or normalization row disguised as training data.
func FitLogisticFold(rows []Row, validationStart time.Time, lambda float64, excluded int) (Standardizer, [8]float64, error) {
	if validationStart.IsZero() {
		return Standardizer{}, [8]float64{}, errors.New("missing validation boundary")
	}
	for _, row := range rows {
		if !row.DecisionTime.Before(validationStart.Add(-6*time.Minute)) ||
			!row.ExitTime.Before(validationStart) {
			return Standardizer{}, [8]float64{}, errors.New("future-fitted V3 training row")
		}
	}
	return FitLogistic(rows, lambda, excluded)
}

func Score(beta [8]float64, s Standardizer, row Row, excluded int) float64 {
	x := s.vector(row, excluded)
	z := 0.0
	for j := range beta {
		z += beta[j] * x[j]
	}
	return z
}

func LogLoss(y bool, score float64) float64 {
	if y {
		return math.Log1p(math.Exp(-math.Abs(score))) + math.Max(0, -score)
	}
	return math.Log1p(math.Exp(-math.Abs(score))) + math.Max(0, score)
}

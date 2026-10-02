package v3

import (
	"math"
	"strconv"
	"time"

	"ts3/internal/v1"
	"ts3/internal/v2"
)

const IntentSchemaVersion = 3

type Intent struct {
	SchemaVersion int       `json:"schema_version"`
	DecisionID    string    `json:"decision_id"`
	RunID         string    `json:"run_id"`
	AsOfOrdinal   uint64    `json:"as_of_ordinal,string"`
	AsOfTime      time.Time `json:"as_of_time"`
	Action        v1.Action `json:"action"`
	Reason        string    `json:"reason"`
	ModelStatus   string    `json:"model_status"`
	ModelArtifact string    `json:"model_artifact_sha256,omitempty"`
	FeatureSchema string    `json:"feature_schema"`
	TargetVersion string    `json:"target_version"`
	RawLogitMicro *int64    `json:"raw_logit_micro,omitempty"`
	V2Baseline    v2.Intent `json:"v2_baseline"`
}

// EvaluateIntent is shared by live and replay. It has no clock, training data,
// outcome callback or network handle. An uncalibrated score is research-only.
func EvaluateIntent(i v2.Intent, artifact *Artifact) Intent {
	result := Intent{SchemaVersion: IntentSchemaVersion,
		DecisionID: i.RunID + ":" + strconv.FormatUint(i.AsOfOrdinal, 10) + ":v3",
		RunID:      i.RunID, AsOfOrdinal: i.AsOfOrdinal, AsOfTime: i.AsOfTime,
		Action: v1.NoTrade, FeatureSchema: FeatureSchema,
		TargetVersion: TargetVersion, V2Baseline: i}
	if artifact == nil {
		result.Reason, result.ModelStatus = "MODEL_UNAVAILABLE", "UNAVAILABLE"
		return result
	}
	result.ModelArtifact = artifact.SHA256
	if artifact.V1ConfigSHA != i.Baseline.ConfigSHA256 ||
		artifact.V2ConfigSHA != i.ConfigSHA256 || artifact.Validate() != nil {
		result.Reason, result.ModelStatus = "MODEL_INCOMPATIBLE", "UNAVAILABLE"
		return result
	}
	features, available, err := FeaturesFromIntent(i)
	if err != nil || !available {
		result.Reason, result.ModelStatus = "FEATURE_UNAVAILABLE", "UNAVAILABLE"
		return result
	}
	score, err := artifact.RawScore(features)
	if err != nil || math.Abs(score) > float64(math.MaxInt64)/1e6 {
		result.Reason, result.ModelStatus = "FEATURE_OUT_OF_RANGE", "OOD"
		return result
	}
	rounded := int64(math.Round(score * 1e6))
	result.RawLogitMicro = &rounded
	result.Reason, result.ModelStatus = "UNCALIBRATED_NO_EDGE", "AVAILABLE_UNCALIBRATED"
	return result
}

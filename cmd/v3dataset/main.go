package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	"ts3/internal/domain"
	"ts3/internal/processor"
	"ts3/internal/record"
	"ts3/internal/source/replay"
	"ts3/internal/v1"
	"ts3/internal/v2"
	"ts3/internal/v3"
)

type report struct {
	SchemaVersion        int            `json:"schema_version"`
	SourceRunID          string         `json:"source_run_id"`
	SourceManifestSHA    string         `json:"source_manifest_sha256"`
	AnalysisRevision     string         `json:"analysis_revision"`
	GoVersion            string         `json:"go_version"`
	Input                record.Report  `json:"input"`
	ProcessedOrdinal     uint64         `json:"processed_ordinal,string"`
	FullCommittedPrefix  bool           `json:"full_committed_prefix"`
	V0NormalizedSHA      string         `json:"v0_normalized_sha256"`
	V0StateSHA           string         `json:"v0_state_sha256"`
	V1IntentSHA          string         `json:"v1_intent_sha256"`
	V2IntentSHA          string         `json:"v2_intent_sha256"`
	V1Research           v1.Report      `json:"v1_research"`
	FeatureSchema        string         `json:"feature_schema"`
	TargetVersion        string         `json:"target_version"`
	Rows                 int            `json:"rows"`
	PairedButUnavailable int            `json:"paired_but_feature_unavailable"`
	DepthUp              int            `json:"depth_up"`
	DepthNonpositive     int            `json:"depth_nonpositive"`
	LongNetPositive      int            `json:"long_net_positive"`
	Regimes              map[string]int `json:"regimes"`
	DatasetSHA           string         `json:"dataset_sha256"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func revision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, modified := "unknown", false
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			rev = setting.Value
		}
		if setting.Key == "vcs.modified" {
			modified = setting.Value == "true"
		}
	}
	if modified {
		rev += "+dirty"
	}
	return rev
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}

func addCanonical(h hash.Hash, value any) error {
	b, err := domain.CanonicalJSON(value)
	if err != nil {
		return err
	}
	_, _ = h.Write(append(b, '\n'))
	return nil
}

func run() error {
	dir := flag.String("dir", "data", "raw V0 recording directory")
	runID := flag.String("run", "", "source run ID")
	out := flag.String("out", "", "new V3 output directory")
	flag.Parse()
	if *runID == "" || *out == "" || strings.Contains(*out, "..") {
		return errors.New("-run and a non-traversing -out are required")
	}
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		return fmt.Errorf("V3 output directory must not exist: %s", *out)
	}
	manifestPath := filepath.Join(*dir, *runID+".manifest.json")
	manifestHash, err := hashFile(manifestPath)
	if err != nil {
		return err
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest record.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return err
	}
	if !manifest.Clean || manifest.RunID != *runID {
		return errors.New("source manifest is not a clean matching run")
	}
	src, err := replay.Open(*dir, *runID, false)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.Mkdir(*out, 0755); err != nil {
		return err
	}
	p, err := processor.New(filepath.Join(*out, "v0"), false)
	if err != nil {
		return err
	}
	defer p.Close()
	engine, err := v2.NewEngine(*runID, v2.DefaultConfig())
	if err != nil {
		return err
	}
	research, err := v1.NewResearch(*runID, v1.DefaultConfig())
	if err != nil {
		return err
	}
	intents := map[uint64]v2.Intent{}
	rows := make([]v3.Row, 0, 1500)
	unavailable := 0
	var pairedErr error
	research.ObservePaired(func(paired v1.PairedObservation) {
		if pairedErr != nil {
			return
		}
		intent, ok := intents[paired.Intent.AsOfOrdinal]
		if !ok {
			pairedErr = fmt.Errorf("missing V2 decision at %d", paired.Intent.AsOfOrdinal)
			return
		}
		delete(intents, paired.Intent.AsOfOrdinal)
		row, available, err := v3.FromPaired(intent, paired, manifestHash)
		if err != nil {
			pairedErr = err
			return
		}
		if available {
			rows = append(rows, row)
		} else {
			unavailable++
		}
	})
	hV1, hV2 := sha256.New(), sha256.New()
	for {
		raw, err := src.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		view, err := p.Apply(raw)
		if err != nil {
			return err
		}
		if raw.Kind != domain.ClockTick {
			continue
		}
		quote := p.Quote()
		if err := research.ObserveTick(view, quote); err != nil {
			return err
		}
		if pairedErr != nil {
			return pairedErr
		}
		intent, err := engine.ApplyTick(view, quote)
		if err != nil {
			return err
		}
		if intent == nil {
			continue
		}
		if err := addCanonical(hV2, intent); err != nil {
			return err
		}
		if err := addCanonical(hV1, intent.Baseline); err != nil {
			return err
		}
		if err := research.AddDecision(intent.Baseline); err != nil {
			return err
		}
		intents[intent.AsOfOrdinal] = *intent
	}
	if pairedErr != nil {
		return pairedErr
	}
	if err := p.Flush(); err != nil {
		return err
	}
	nh, sh := p.Hashes()
	processed := p.Last()
	if err := p.Close(); err != nil {
		return err
	}
	input := src.Report()
	full := !input.Incomplete && processed == input.CommittedOrdinal &&
		processed == manifest.CommittedOrdinal
	if !full {
		return errors.New("V3 dataset input is not the full clean committed prefix")
	}
	v1report := research.Finalize()
	if len(rows)+unavailable != v1report.PairedEpisodes {
		return fmt.Errorf("paired row reconciliation: %d + %d != %d", len(rows), unavailable, v1report.PairedEpisodes)
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].DecisionOrdinal < rows[b].DecisionOrdinal })
	path := filepath.Join(*out, "rows.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	hRows := sha256.New()
	result := report{SchemaVersion: 1, SourceRunID: *runID, SourceManifestSHA: manifestHash,
		AnalysisRevision: revision(), GoVersion: runtime.Version(), Input: input,
		ProcessedOrdinal: processed, FullCommittedPrefix: full, V0NormalizedSHA: nh,
		V0StateSHA: sh, V1IntentSHA: hex.EncodeToString(hV1.Sum(nil)),
		V2IntentSHA: hex.EncodeToString(hV2.Sum(nil)), V1Research: v1report,
		FeatureSchema: v3.FeatureSchema, TargetVersion: v3.TargetVersion,
		Rows: len(rows), PairedButUnavailable: unavailable, Regimes: map[string]int{}}
	for _, row := range rows {
		b, err := domain.CanonicalJSON(row)
		if err != nil {
			f.Close()
			return err
		}
		b = append(b, '\n')
		if _, err := w.Write(b); err != nil {
			f.Close()
			return err
		}
		_, _ = hRows.Write(b)
		if row.DepthUp {
			result.DepthUp++
		} else {
			result.DepthNonpositive++
		}
		if row.LongNetPositive {
			result.LongNetPositive++
		}
		result.Regimes[row.Regime]++
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	result.DatasetSHA = hex.EncodeToString(hRows.Sum(nil))
	b, err := domain.CanonicalJSON(result)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*out, "report.json"), append(b, '\n'), 0644)
}

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

	"ts3/internal/domain"
	"ts3/internal/processor"
	"ts3/internal/record"
	"ts3/internal/source/replay"
	"ts3/internal/v1"
	"ts3/internal/v2"
	"ts3/internal/v3"
)

type report struct {
	SchemaVersion       int               `json:"schema_version"`
	RunID               string            `json:"run_id"`
	SourceManifestSHA   string            `json:"source_manifest_sha256"`
	SourceCodeRevision  string            `json:"source_code_revision"`
	AnalysisRevision    string            `json:"analysis_revision"`
	GoVersion           string            `json:"go_version"`
	ArtifactSHA         string            `json:"artifact_sha256"`
	Input               record.Report     `json:"input"`
	ProcessedOrdinal    uint64            `json:"processed_ordinal,string"`
	FullCommittedPrefix bool              `json:"full_committed_prefix"`
	V0NormalizedSHA     string            `json:"v0_normalized_sha256"`
	V0StateSHA          string            `json:"v0_state_sha256"`
	V1IntentSHA         string            `json:"v1_intent_sha256"`
	V2IntentSHA         string            `json:"v2_intent_sha256"`
	V3IntentSHA         string            `json:"v3_intent_sha256"`
	Intents             int               `json:"intents"`
	Actions             map[v1.Action]int `json:"actions"`
	Reasons             map[string]int    `json:"reasons"`
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

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}

func canonicalLine(w *bufio.Writer, h hash.Hash, value any) error {
	b, err := domain.CanonicalJSON(value)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if w != nil {
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	_, _ = h.Write(b)
	return nil
}

func run() error {
	dir := flag.String("dir", "data", "raw recording directory")
	runID := flag.String("run", "", "source run ID")
	out := flag.String("out", "", "new replay output directory")
	artifactPath := flag.String("artifact", "", "verified V3 artifact")
	through := flag.Uint64("through", 0, "optional development prefix")
	flag.Parse()
	if *runID == "" || *out == "" || *artifactPath == "" {
		return errors.New("-run, -out and -artifact required")
	}
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		return errors.New("V3 replay output directory must not exist")
	}
	v1Digest, err := v1.DefaultConfig().Digest()
	if err != nil {
		return err
	}
	v2Digest, err := v2.DefaultConfig().Digest()
	if err != nil {
		return err
	}
	artifact, err := v3.LoadArtifact(*artifactPath, v1Digest, v2Digest)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(*dir, *runID+".manifest.json")
	manifestSHA, err := fileHash(manifestPath)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest record.Manifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return err
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
	f, err := os.OpenFile(filepath.Join(*out, "intents.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	h1, h2, h3 := sha256.New(), sha256.New(), sha256.New()
	result := report{SchemaVersion: v3.IntentSchemaVersion, RunID: *runID,
		SourceManifestSHA: manifestSHA, SourceCodeRevision: manifest.Provenance.CodeRevision,
		AnalysisRevision: revision(), GoVersion: runtime.Version(), ArtifactSHA: artifact.SHA256,
		Actions: map[v1.Action]int{}, Reasons: map[string]int{}}
	for {
		raw, err := src.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			return err
		}
		if *through > 0 && raw.Ordinal > *through {
			break
		}
		view, err := p.Apply(raw)
		if err != nil {
			f.Close()
			return err
		}
		if raw.Kind != domain.ClockTick {
			continue
		}
		intent, err := engine.ApplyTick(view, p.Quote())
		if err != nil {
			f.Close()
			return err
		}
		if intent == nil {
			continue
		}
		if err := canonicalLine(nil, h1, intent.Baseline); err != nil {
			f.Close()
			return err
		}
		if err := canonicalLine(nil, h2, intent); err != nil {
			f.Close()
			return err
		}
		v3Intent := v3.EvaluateIntent(*intent, &artifact)
		if err := canonicalLine(w, h3, v3Intent); err != nil {
			f.Close()
			return err
		}
		result.Intents++
		result.Actions[v3Intent.Action]++
		result.Reasons[v3Intent.Reason]++
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
	if err := p.Flush(); err != nil {
		return err
	}
	result.V0NormalizedSHA, result.V0StateSHA = p.Hashes()
	result.ProcessedOrdinal = p.Last()
	if err := p.Close(); err != nil {
		return err
	}
	result.Input = src.Report()
	result.FullCommittedPrefix = *through == 0 && manifest.Clean &&
		!result.Input.Incomplete && result.ProcessedOrdinal == result.Input.CommittedOrdinal &&
		result.ProcessedOrdinal == manifest.CommittedOrdinal
	result.V1IntentSHA = hex.EncodeToString(h1.Sum(nil))
	result.V2IntentSHA = hex.EncodeToString(h2.Sum(nil))
	result.V3IntentSHA = hex.EncodeToString(h3.Sum(nil))
	b, err = domain.CanonicalJSON(result)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "report.json"), append(b, '\n'), 0644); err != nil {
		return err
	}
	if *through == 0 && !result.FullCommittedPrefix {
		return errors.New("V3 replay did not cover clean full committed prefix")
	}
	return nil
}

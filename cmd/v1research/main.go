package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"ts3/internal/domain"
	"ts3/internal/processor"
	"ts3/internal/record"
	"ts3/internal/source/replay"
	"ts3/internal/v1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type outputReport struct {
	SchemaVersion       int           `json:"schema_version"`
	SourceRunID         string        `json:"source_run_id"`
	SourceCodeRevision  string        `json:"source_code_revision"`
	SourceManifestSHA   string        `json:"source_manifest_sha256"`
	AnalysisRevision    string        `json:"analysis_revision"`
	GoVersion           string        `json:"go_version"`
	Input               record.Report `json:"input"`
	ProcessedOrdinal    uint64        `json:"processed_ordinal,string"`
	FullCommittedPrefix bool          `json:"full_committed_prefix"`
	V0NormalizedSHA256  string        `json:"v0_normalized_sha256"`
	V0StateSHA256       string        `json:"v0_state_sha256"`
	IntentSHA256        string        `json:"intent_sha256"`
	Research            v1.Report     `json:"research"`
}

func revision() string {
	info, ok := debug.ReadBuildInfo()
	rev, modified := "unknown", false
	if ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				rev = setting.Value
			}
			if setting.Key == "vcs.modified" {
				modified = setting.Value == "true"
			}
		}
	}
	if rev == "unknown" {
		if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
			rev = strings.TrimSpace(string(out))
		}
		if out, err := exec.Command("git", "status", "--porcelain").Output(); err == nil {
			modified = len(out) > 0
		}
	}
	if modified {
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
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeLine(w *bufio.Writer, h hash.Hash, value any) error {
	b, err := domain.CanonicalJSON(value)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, _ = h.Write(b)
	return nil
}

func run() error {
	dir := flag.String("dir", "data", "raw V0 recording directory")
	runID := flag.String("run", "", "V0 run ID")
	out := flag.String("out", "", "new V1 output directory")
	through := flag.Uint64("through", 0, "optional development prefix; 0 means full committed recording")
	flag.Parse()
	if *runID == "" || *out == "" {
		return fmt.Errorf("-run and -out are required")
	}
	if strings.Contains(*out, "..") {
		return fmt.Errorf("output path cannot contain parent traversal")
	}
	src, err := replay.Open(*dir, *runID, false)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.Mkdir(*out, 0755); err != nil {
		return fmt.Errorf("create unique output directory: %w", err)
	}
	p, err := processor.New(filepath.Join(*out, "v0"), false)
	if err != nil {
		return err
	}
	defer p.Close()
	engine, err := v1.NewEngine(*runID, v1.DefaultConfig())
	if err != nil {
		return err
	}
	research, err := v1.NewResearch(*runID, v1.DefaultConfig())
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(*out, "intents.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	h := sha256.New()
	for {
		raw, err := src.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if *through > 0 && raw.Ordinal > *through {
			break
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
		intent, err := engine.ApplyTick(view, quote)
		if err != nil {
			return err
		}
		if intent != nil {
			if err := writeLine(w, h, *intent); err != nil {
				return err
			}
			if err := research.AddDecision(*intent); err != nil {
				return err
			}
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := p.Flush(); err != nil {
		return err
	}
	nh, sh := p.Hashes()
	manifestPath := filepath.Join(*dir, *runID+".manifest.json")
	manifestHash, err := fileHash(manifestPath)
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
	rawReport := src.Report()
	full := !rawReport.Incomplete && p.Last() == rawReport.CommittedOrdinal && manifest.Clean
	report := outputReport{SchemaVersion: 1, SourceRunID: *runID,
		SourceCodeRevision: manifest.Provenance.CodeRevision, SourceManifestSHA: manifestHash,
		AnalysisRevision: revision(), GoVersion: runtime.Version(), Input: rawReport,
		ProcessedOrdinal: p.Last(), FullCommittedPrefix: full,
		V0NormalizedSHA256: nh, V0StateSHA256: sh,
		IntentSHA256: hex.EncodeToString(h.Sum(nil)), Research: research.Finalize()}
	b, err := domain.CanonicalJSON(report)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(*out, "report.json"), b, 0644); err != nil {
		return err
	}
	if !full && *through == 0 {
		return fmt.Errorf("V1 input is not a clean full committed prefix")
	}
	return nil
}

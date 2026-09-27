package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"ts3/internal/processor"
	"ts3/internal/progress"
	"ts3/internal/record"
	"ts3/internal/source/replay"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type parityReport struct {
	ComparedOrdinal      uint64 `json:"compared_ordinal"`
	Equal                bool   `json:"equal"`
	LiveNormalizedSHA256 string `json:"live_normalized_sha256"`
	LiveStateSHA256      string `json:"live_state_sha256"`
}

func fileSHA(path string) (string, error) {
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
func run() error {
	dir := flag.String("dir", "data", "raw recording directory")
	runID := flag.String("run", "", "run ID")
	out := flag.String("out", "", "output directory")
	paced := flag.Bool("paced", false, "pace by recorded logical time")
	stepped := flag.Bool("step", false, "wait for Enter before each recorded event")
	through := flag.Uint64("through", 0, "apply only through this committed ordinal; 0 applies the full prefix")
	recoverManifest := flag.Bool("recover-manifest", false, "write an unclean manifest when the original is missing")
	flag.Parse()
	if *paced && *stepped {
		return fmt.Errorf("-paced and -step are mutually exclusive")
	}
	if *runID == "" {
		return fmt.Errorf("-run required")
	}
	if *out == "" {
		*out = filepath.Join(*dir, *runID+"-replay")
	}
	src, err := replay.Open(*dir, *runID, *paced)
	if err != nil {
		return err
	}
	defer src.Close()
	p, err := processor.New(*out, false)
	if err != nil {
		return err
	}
	defer p.Close()
	ctx := context.Background()
	input := bufio.NewReader(os.Stdin)
	for {
		r, err := src.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if *through > 0 && r.Ordinal > *through {
			continue
		}
		if *stepped {
			fmt.Fprintf(os.Stderr, "ordinal %d: press Enter to apply\n", r.Ordinal)
			if _, err := input.ReadString('\n'); err != nil {
				return fmt.Errorf("step input: %w", err)
			}
		}
		if _, err = p.Apply(r); err != nil {
			return err
		}
	}
	if err = p.Flush(); err != nil {
		return err
	}
	if *through > 0 && p.Last() != *through {
		return fmt.Errorf("requested ordinal %d exceeds committed prefix %d", *through, p.Last())
	}
	nh, sh := p.Hashes()
	report := struct {
		RunID             string        `json:"run_id"`
		ProcessedOrdinal  uint64        `json:"processed_ordinal"`
		NormalizedSHA256  string        `json:"normalized_sha256"`
		StateSHA256       string        `json:"state_sha256"`
		Raw               any           `json:"raw"`
		LiveProgress      any           `json:"live_progress,omitempty"`
		LiveProgressError string        `json:"live_progress_error,omitempty"`
		RecoveredManifest bool          `json:"recovered_manifest,omitempty"`
		LiveParity        *parityReport `json:"live_parity,omitempty"`
	}{RunID: *runID, ProcessedOrdinal: p.Last(), NormalizedSHA256: nh, StateSHA256: sh, Raw: src.Report()}
	live, liveErr := progress.Recover(filepath.Join(*dir, *runID, "progress.jsonl"))
	if liveErr == nil {
		report.LiveProgress = live
	} else if !os.IsNotExist(liveErr) {
		report.LiveProgress = live
		report.LiveProgressError = liveErr.Error()
	}
	if liveErr == nil && !src.Report().Incomplete && live.Applied == p.Last() && live.Published == p.Last() {
		liveNormalized, err := fileSHA(filepath.Join(*dir, *runID, "normalized.jsonl"))
		if err != nil {
			return err
		}
		liveState, err := fileSHA(filepath.Join(*dir, *runID, "state.jsonl"))
		if err != nil {
			return err
		}
		report.LiveParity = &parityReport{ComparedOrdinal: p.Last(), Equal: liveNormalized == nh && liveState == sh, LiveNormalizedSHA256: liveNormalized, LiveStateSHA256: liveState}
	}
	if *recoverManifest {
		if _, err := record.RebuildManifest(*dir, *runID); err != nil {
			return err
		}
		report.RecoveredManifest = true
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return err
	}
	if report.LiveParity != nil && !report.LiveParity.Equal {
		return fmt.Errorf("live/replay hash mismatch through ordinal %d", p.Last())
	}
	return nil
}

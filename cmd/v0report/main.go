// Command v0report audits a completed V0 capture and its full replay.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
	"ts3/internal/progress"
	"ts3/internal/record"
	"ts3/internal/soakreport"
	"ts3/internal/telemetry"
)

type incident struct {
	Ordinal uint64    `json:"ordinal"`
	Epoch   uint64    `json:"epoch"`
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Detail  string    `json:"detail,omitempty"`
}

type interval struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationNS int64     `json:"duration_ns"`
}

type parity struct {
	Checked          bool   `json:"checked"`
	Equal            bool   `json:"equal"`
	LiveNormalized   string `json:"live_normalized_sha256,omitempty"`
	ReplayNormalized string `json:"replay_normalized_sha256,omitempty"`
	LiveState        string `json:"live_state_sha256,omitempty"`
	ReplayState      string `json:"replay_state_sha256,omitempty"`
}

type report struct {
	RunID                 string                `json:"run_id"`
	StartedAt             time.Time             `json:"started_at"`
	EndedAt               time.Time             `json:"ended_at"`
	RunDurationSeconds    float64               `json:"run_duration_seconds"`
	HealthySpanSeconds    float64               `json:"healthy_span_seconds"`
	RawBytes              int64                 `json:"raw_bytes"`
	RawBytesPerSecond     float64               `json:"raw_bytes_per_second"`
	PayloadBytesPerSecond float64               `json:"payload_bytes_per_second"`
	FramesPerSecond       float64               `json:"frames_per_second"`
	Counts                map[string]uint64     `json:"counts"`
	Bytes                 map[string]uint64     `json:"bytes"`
	QueueMax              int                   `json:"queue_max"`
	HealthDurationNS      map[book.Health]int64 `json:"health_duration_ns"`
	AdmissionLag          telemetry.Quantiles   `json:"admission_lag"`
	CommitLag             telemetry.Quantiles   `json:"commit_lag"`
	ApplyLag              telemetry.Quantiles   `json:"apply_lag"`
	TickLag               telemetry.Quantiles   `json:"tick_lag"`
	DataFsync             telemetry.Quantiles   `json:"data_fsync"`
	MarkerFsync           telemetry.Quantiles   `json:"marker_fsync"`
	Raw                   record.Report         `json:"raw"`
	Progress              progress.Report       `json:"progress"`
	ManifestClean         bool                  `json:"manifest_clean"`
	RunStatus             soakreport.Status     `json:"run_status"`
	ManifestCommitted     uint64                `json:"manifest_committed"`
	StateRows             uint64                `json:"state_rows"`
	Reconnects            uint64                `json:"reconnects"`
	CaptureGaps           uint64                `json:"capture_gaps"`
	ChecksumFailures      uint64                `json:"checksum_failures"`
	ClockAnomalies        uint64                `json:"clock_anomalies"`
	ClockOffsetMeasured   bool                  `json:"clock_offset_measured"`
	QuietBookIntervals    []interval            `json:"quiet_book_intervals"`
	Incidents             []incident            `json:"incidents"`
	Parity                parity                `json:"parity"`
	Checks                map[string]bool       `json:"checks"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", "data", "capture directory")
	runID := flag.String("run", "", "run ID")
	replayDir := flag.String("replay-dir", "", "full replay output directory")
	flag.Parse()
	if *runID == "" {
		return fmt.Errorf("-run required")
	}
	if *replayDir == "" {
		*replayDir = filepath.Join(*dir, *runID+"-replay")
	}
	r, err := audit(*dir, *runID, *replayDir)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(r)
}

func audit(dir, runID, replayDir string) (report, error) {
	var out report
	out.RunID = runID
	out.Checks = map[string]bool{}
	var manifest record.Manifest
	if err := readJSON(filepath.Join(dir, runID+".manifest.json"), &manifest); err != nil {
		return out, err
	}
	var metrics telemetry.Metrics
	if err := readJSON(filepath.Join(dir, runID, "metrics.json"), &metrics); err != nil {
		return out, err
	}
	if manifest.RunID != runID || metrics.RunID != runID {
		return out, fmt.Errorf("run ID mismatch in manifest or metrics")
	}
	runEvidence, err := soakreport.Read(soakreport.Path(filepath.Join(dir, runID)))
	if err == nil {
		if runEvidence.RunID != runID {
			return out, fmt.Errorf("run report ID mismatch")
		}
		out.RunStatus = runEvidence.Status
	} else if !os.IsNotExist(err) {
		return out, err
	}
	marks, err := progress.Recover(filepath.Join(dir, runID, "progress.jsonl"))
	if err != nil {
		return out, err
	}
	out.StartedAt, out.EndedAt = metrics.StartedAt, metrics.EndedAt
	out.RunDurationSeconds = metrics.EndedAt.Sub(metrics.StartedAt).Seconds()
	if out.RunDurationSeconds <= 0 {
		return out, fmt.Errorf("invalid run duration")
	}
	out.ManifestClean = manifest.Clean
	out.ManifestCommitted = manifest.CommittedOrdinal
	out.Progress = marks
	out.Counts, out.Bytes, out.QueueMax = metrics.Counts, metrics.Bytes, metrics.QueueMax
	out.HealthDurationNS = metrics.HealthDurationNS
	out.AdmissionLag, out.CommitLag, out.ApplyLag, out.TickLag = metrics.AdmissionLag, metrics.CommitLag, metrics.ApplyLag, metrics.TickLag
	out.DataFsync, out.MarkerFsync = metrics.DataFsync, metrics.MarkerFsync
	for _, segment := range manifest.Segments {
		info, err := os.Stat(filepath.Join(dir, segment.Name))
		if err != nil {
			return out, err
		}
		out.RawBytes += info.Size()
	}
	out.RawBytesPerSecond = float64(out.RawBytes) / out.RunDurationSeconds
	var payloadBytes, frames uint64
	for name, count := range metrics.Counts {
		if len(name) >= 3 && name[:3] == "ws:" {
			frames += count
		}
	}
	for _, size := range metrics.Bytes {
		payloadBytes += size
	}
	out.PayloadBytesPerSecond = float64(payloadBytes) / out.RunDurationSeconds
	out.FramesPerSecond = float64(frames) / out.RunDurationSeconds
	reader, err := record.NewReader(dir, runID)
	if err != nil {
		return out, err
	}
	defer reader.Close()
	for {
		raw, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		switch raw.Kind {
		case domain.Connected:
			if raw.ConnectionEpoch > 1 {
				out.Reconnects++
			}
		case domain.CaptureGap:
			out.CaptureGaps++
		case domain.ClockAnomaly:
			out.ClockAnomalies++
		}
		switch raw.Kind {
		case domain.Connected, domain.Disconnected, domain.CaptureGap, domain.ClockAnomaly, domain.WatchdogIncident, domain.SourceError:
			detail := string(raw.Payload)
			if len(detail) > 256 {
				detail = detail[:256]
			}
			out.Incidents = append(out.Incidents, incident{Ordinal: raw.Ordinal, Epoch: raw.ConnectionEpoch, Time: raw.UsableFromTime, Kind: string(raw.Kind), Detail: detail})
		}
	}
	out.Raw = reader.Report()
	if err := scanState(filepath.Join(dir, runID, "state.jsonl"), &out); err != nil {
		return out, err
	}
	sort.SliceStable(out.Incidents, func(i, j int) bool { return out.Incidents[i].Ordinal < out.Incidents[j].Ordinal })
	liveDir := filepath.Join(dir, runID)
	if err := compareOutputs(liveDir, replayDir, &out.Parity); err != nil {
		return out, err
	}
	out.Checks["at_least_24h_healthy_span"] = out.HealthySpanSeconds >= 24*3600
	out.Checks["manifest_clean"] = out.ManifestClean
	out.Checks["run_report_complete"] = out.RunStatus == soakreport.Complete
	out.Checks["raw_complete"] = !out.Raw.Incomplete && out.Raw.Error == "" && out.Raw.UncommittedEvents == 0
	out.Checks["high_water_equal"] = out.ManifestCommitted == out.Raw.CommittedOrdinal && out.Raw.CommittedOrdinal == out.Progress.Applied && out.Progress.Applied == out.Progress.Published && out.StateRows == out.Progress.Applied
	out.Checks["lag_samples_equal"] = out.AdmissionLag.Samples+out.AdmissionLag.NegativeSamples == out.Raw.CommittedOrdinal && out.CommitLag.Samples+out.CommitLag.NegativeSamples == out.Raw.CommittedOrdinal && out.ApplyLag.Samples+out.ApplyLag.NegativeSamples == out.Raw.CommittedOrdinal
	out.Checks["full_replay_parity"] = out.Parity.Checked && out.Parity.Equal
	out.Checks["no_clock_anomaly"] = out.ClockAnomalies == 0
	return out, nil
}

func readJSON(path string, target any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}

func scanState(path string, out *report) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 1<<20)
	var previous book.View
	var firstHealthy, lastHealthy time.Time
	var quietStart time.Time
	for s.Scan() {
		var view book.View
		if err := json.Unmarshal(s.Bytes(), &view); err != nil {
			return err
		}
		out.StateRows++
		if view.Ordinal != out.StateRows {
			return fmt.Errorf("state ordinal gap at %d", view.Ordinal)
		}
		if view.Health == book.Healthy {
			if firstHealthy.IsZero() {
				firstHealthy = view.AsOf
			}
			lastHealthy = view.AsOf
		}
		if view.Health == book.Degraded && view.Reason == "BOOK_QUIET" && quietStart.IsZero() {
			quietStart = view.AsOf
		}
		if !quietStart.IsZero() && (view.Health != book.Degraded || view.Reason != "BOOK_QUIET") {
			out.QuietBookIntervals = append(out.QuietBookIntervals, interval{Start: quietStart, End: view.AsOf, DurationNS: view.AsOf.Sub(quietStart).Nanoseconds()})
			quietStart = time.Time{}
		}
		if view.Reason == "BOOK_CHECKSUM" && previous.Reason != "BOOK_CHECKSUM" {
			out.ChecksumFailures++
			out.Incidents = append(out.Incidents, incident{Ordinal: view.Ordinal, Epoch: view.Epoch, Time: view.AsOf, Kind: "BOOK_CHECKSUM"})
		}
		previous = view
	}
	if err := s.Err(); err != nil {
		return err
	}
	if !quietStart.IsZero() {
		out.QuietBookIntervals = append(out.QuietBookIntervals, interval{Start: quietStart, End: out.EndedAt, DurationNS: out.EndedAt.Sub(quietStart).Nanoseconds()})
	}
	if !firstHealthy.IsZero() {
		out.HealthySpanSeconds = lastHealthy.Sub(firstHealthy).Seconds()
	}
	return nil
}

func compareOutputs(liveDir, replayDir string, p *parity) error {
	paths := []string{"normalized.jsonl", "state.jsonl"}
	hashes := make([]string, 0, 4)
	for _, dir := range []string{liveDir, replayDir} {
		for _, name := range paths {
			h, err := fileSHA(filepath.Join(dir, name))
			if os.IsNotExist(err) && dir == replayDir {
				return nil
			}
			if err != nil {
				return err
			}
			hashes = append(hashes, h)
		}
	}
	p.Checked = true
	p.LiveNormalized, p.LiveState, p.ReplayNormalized, p.ReplayState = hashes[0], hashes[1], hashes[2], hashes[3]
	p.Equal = p.LiveNormalized == p.ReplayNormalized && p.LiveState == p.ReplayState
	return nil
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

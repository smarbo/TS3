package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"ts3/internal/book"
	"ts3/internal/processor"
	"ts3/internal/record"
	"ts3/internal/soakreport"
	"ts3/internal/telemetry"
)

// Only the collector owner calls this, after durable raw/derived progress.
func writeRunReport(dir, runID string, base soakreport.Report, metrics *telemetry.Collector, writer *record.Writer, proc *processor.Processor, started time.Time, status soakreport.Status, warnings ...string) error {
	now := time.Now().UTC()
	live := metrics.LiveState()
	m := metrics.Snapshot(now, time.Since(started))
	base.Status = status
	base.UpdatedAt = now
	base.RuntimeSeconds = time.Since(started).Seconds()
	base.CurrentHealth = live.Health
	if base.CurrentHealth == "" {
		base.CurrentHealth = book.Starting
	}
	base.CurrentHealthReason = live.Reason
	if !live.LatestConfirmedHealthyAt.IsZero() {
		confirmed := live.LatestConfirmedHealthyAt
		base.LatestConfirmedHealthyAt = &confirmed
	}
	base.EventCounts, base.PayloadBytes = m.Counts, m.Bytes
	base.Reconnects, base.CaptureGaps = live.Reconnects, live.CaptureGaps
	base.StaleFeedIncidents, base.DataQualityIncidents = live.StaleFeedIncidents, live.DataQualityIncidents
	if live.CaptureGaps > 0 {
		base.Warnings = append(base.Warnings, "Capture gaps are present; inspect raw CAPTURE_GAP records.")
	}
	if live.Reconnects > 0 || m.Counts["SOURCE_ERROR"] > 0 {
		base.Warnings = append(base.Warnings, "Source disconnect or reconnect activity is present; inspect raw source incidents.")
	}
	if m.Counts["CLOCK_ANOMALY"] > 0 || m.Counts["WATCHDOG_INCIDENT"] > 0 {
		base.Warnings = append(base.Warnings, "Clock or watchdog incidents are present; this run needs investigation.")
	}
	if live.StaleFeedIncidents > 0 {
		base.Warnings = append(base.Warnings, "Stale feed incidents are present; inspect state and raw timing.")
	}
	if live.DataQualityIncidents > 0 || m.Counts["ws:malformed"] > 0 {
		base.Warnings = append(base.Warnings, "Parse or data-quality incidents are present; inspect normalized status and raw frames.")
	}
	base.CommittedOrdinal = writer.CommittedOrdinal()
	marks := proc.Progress()
	base.AppliedOrdinal, base.PublishedOrdinal = marks.Applied, marks.Published
	files, err := evidenceFiles(dir, runID)
	if err != nil {
		return err
	}
	base.EvidenceFiles = files
	base.Warnings = append(base.Warnings, warnings...)
	if status != soakreport.Running {
		base.EndedAt = &now
	}
	return soakreport.Write(soakreport.Path(filepath.Join(dir, runID)), base)
}

func evidenceFiles(dir, runID string) (map[string]int64, error) {
	out := map[string]int64{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), runID+"-") || !strings.HasSuffix(entry.Name(), ".raw") {
			continue
		}
		segmentNumber := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), runID+"-"), ".raw")
		if len(segmentNumber) != 6 || strings.IndexFunc(segmentNumber, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out[entry.Name()] = info.Size()
	}
	for _, name := range []string{"normalized.jsonl", "state.jsonl", "progress.jsonl", "metrics.json"} {
		info, err := os.Stat(filepath.Join(dir, runID, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[name] = info.Size()
	}
	if info, err := os.Stat(filepath.Join(dir, runID+".manifest.json")); err == nil {
		out[runID+".manifest.json"] = info.Size()
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return out, nil
}

// This runs before a new capture. It changes only the operational reports of
// prior runs, leaving their raw and derived evidence intact.
func markPriorInterrupted(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := soakreport.MarkInterrupted(soakreport.Path(filepath.Join(dir, entry.Name())), time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

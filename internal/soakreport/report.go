package soakreport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
	"ts3/internal/durable"
)

const Filename = "run-report.json"

type Status string

const (
	Running    Status = "RUNNING"
	Complete   Status = "COMPLETE"
	Incomplete Status = "INCOMPLETE"
)

// Report is operational evidence. It is never consumed by replay as market input.
type Report struct {
	RunID                    string            `json:"run_id"`
	Status                   Status            `json:"status"`
	CodeRevision             string            `json:"code_revision"`
	GoVersion                string            `json:"go_version"`
	ConfigSHA256             string            `json:"config_sha256"`
	SourceEndpoint           string            `json:"source_endpoint"`
	StartedAt                time.Time         `json:"started_at"`
	UpdatedAt                time.Time         `json:"updated_at"`
	EndedAt                  *time.Time        `json:"ended_at,omitempty"`
	RuntimeSeconds           float64           `json:"runtime_seconds"`
	LatestConfirmedHealthyAt *time.Time        `json:"latest_confirmed_healthy_at,omitempty"`
	CurrentHealth            book.Health       `json:"current_health"`
	CurrentHealthReason      string            `json:"current_health_reason,omitempty"`
	EventCounts              map[string]uint64 `json:"event_counts"`
	PayloadBytes             map[string]uint64 `json:"payload_bytes"`
	Reconnects               uint64            `json:"reconnects"`
	CaptureGaps              uint64            `json:"capture_gaps"`
	SequenceGaps             *uint64           `json:"sequence_gaps"` // nil: source has no contiguous sequence IDs
	StaleFeedIncidents       uint64            `json:"stale_feed_incidents"`
	DataQualityIncidents     uint64            `json:"parse_data_quality_incidents"`
	CommittedOrdinal         uint64            `json:"committed_ordinal"`
	AppliedOrdinal           uint64            `json:"applied_ordinal"`
	PublishedOrdinal         uint64            `json:"published_ordinal"`
	EvidenceFiles            map[string]int64  `json:"evidence_files_bytes"`
	Warnings                 []string          `json:"warnings"`
}

func Path(dir string) string { return filepath.Join(dir, Filename) }

// Write replaces a report only after the new copy is durable. A failed rename
// leaves the previous report intact. The parent directory is fsynced on Unix.
func Write(path string, r Report) error {
	b, err := domain.CanonicalJSON(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".run-report-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return durable.SyncDir(dir)
}

func Read(path string) (Report, error) {
	var r Report
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("invalid run report: %w", err)
	}
	return r, nil
}

// MarkInterrupted conservatively disqualifies a prior run left RUNNING by a
// crash. It retains every counter and file; no raw evidence is touched.
func MarkInterrupted(path string, now time.Time) error {
	r, err := Read(path)
	if os.IsNotExist(err) {
		return nil // older V0 runs did not have an incremental report
	}
	if err != nil {
		return err
	}
	if r.Status != Running {
		return nil
	}
	r.Status = Incomplete
	r.UpdatedAt = now.UTC()
	r.Warnings = append(r.Warnings, "Previous collector stopped without completing its final report; evidence is partial.")
	return Write(path, r)
}

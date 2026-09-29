package v1

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveJournalDurableAckAndGateVeto(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	j, err := NewJournal(dir, "r", cfg)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := cfg.Digest()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	i := Intent{RunID: "r", ConfigSHA256: digest, DecisionID: "r:10:v1", AsOfOrdinal: 10, Action: Long}
	if err := j.Append(i, now); err != nil {
		t.Fatal(err)
	}
	if err := j.Flush(false); err != nil {
		t.Fatal(err)
	}
	if err := j.Append(i, now); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	s := j.Snapshot()
	if s.Intents != 1 || s.Acknowledged != 1 || s.GateVetoes != 1 || s.IntentSHA256 == "" {
		t.Fatalf("snapshot: %+v", s)
	}
	f, err := os.Open(filepath.Join(dir, "v1-live-acks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	if !scan.Scan() {
		t.Fatal("missing ack")
	}
	var ack LiveAck
	if err := json.Unmarshal(scan.Bytes(), &ack); err != nil {
		t.Fatal(err)
	}
	if ack.DecisionID != i.DecisionID || ack.GateOpen || ack.VetoReason != "LIVE_GATE_CLOSED" || ack.IntentDurableAt.IsZero() {
		t.Fatalf("ack: %+v", ack)
	}
	path := filepath.Join(dir, "v1-report.json")
	if err := WriteLiveReport(path, LiveReport{Status: "COMPLETE", CodeRevision: "test", UpdatedAt: now, Journal: s}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report LiveReport
	if err := json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if report.Journal.IntentSHA256 != s.IntentSHA256 || report.Status != "COMPLETE" {
		t.Fatalf("report: %+v", report)
	}
}

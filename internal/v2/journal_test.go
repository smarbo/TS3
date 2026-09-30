package v2

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ts3/internal/v1"
)

func TestLiveJournalDurableIntentAndOperationalVeto(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	j, err := NewJournal(dir, "r", cfg)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := cfg.Digest()
	at := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	i := Intent{SchemaVersion: SchemaVersion, RunID: "r", ConfigSHA256: digest,
		DecisionID: "r:10:v2", AsOfOrdinal: 10, Action: v1.Long,
		ReasonCodes: []string{"MULTI_SIGNAL_CONSENSUS"}, Evidence: Evidence{Disagreement: "0"},
		Signals: [3]SignalResult{{ID: "trend.v2.1", Status: Active},
			{ID: "reversion.v2.1", Status: Neutral}, {ID: "book_pressure.v2.1", Status: Active}}}
	j.RecordDecisionLatency(3 * time.Millisecond)
	j.Begin("trend")()
	j.Begin("unbounded_external_label")()
	if err := j.Append(i, at); err != nil {
		t.Fatal(err)
	}
	if err := j.Flush(false); err != nil {
		t.Fatal(err)
	}
	if err := j.Append(i, at); err == nil {
		t.Fatal("duplicate intent accepted")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	s := j.Snapshot()
	if s.Intents != 1 || s.Acknowledged != 1 || s.GateVetoes != 1 ||
		s.Actions[v1.Long] != 1 || s.SignalStatus["book_pressure.v2.1"][Active] != 1 ||
		s.DecisionLatencySamples != 1 || s.DecisionLatencyMaxNS != int64(3*time.Millisecond) ||
		s.StageLatency["trend"].Samples != 1 || len(s.StageLatency) != 1 {
		t.Fatalf("journal snapshot: %+v", s)
	}
	f, err := os.Open(filepath.Join(dir, "v2-live-acks.jsonl"))
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
	path := filepath.Join(dir, "v2-report.json")
	if err := WriteLiveReport(path, LiveReport{Status: "COMPLETE", CodeRevision: "test", UpdatedAt: at, Journal: s}); err != nil {
		t.Fatal(err)
	}
	var report LiveReport
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &report); err != nil || report.Journal.IntentSHA256 != s.IntentSHA256 {
		t.Fatalf("report: %+v %v", report, err)
	}
}

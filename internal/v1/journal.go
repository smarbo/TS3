package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"time"

	"ts3/internal/domain"
	"ts3/internal/durable"
)

type LiveAck struct {
	DecisionID      string    `json:"decision_id"`
	AsOfOrdinal     uint64    `json:"as_of_ordinal,string"`
	Action          Action    `json:"action"`
	DecisionReadyAt time.Time `json:"decision_ready_at"`
	IntentDurableAt time.Time `json:"intent_durable_at"`
	GateOpen        bool      `json:"gate_open"`
	VetoReason      string    `json:"veto_reason,omitempty"`
}

type JournalSnapshot struct {
	SchemaVersion     int    `json:"schema_version"`
	RunID             string `json:"run_id"`
	ConfigSHA256      string `json:"config_sha256"`
	Intents           int    `json:"intents"`
	Acknowledged      int    `json:"acknowledged"`
	GateVetoes        int    `json:"gate_vetoes"`
	LastIntentOrdinal uint64 `json:"last_intent_ordinal,string"`
	IntentSHA256      string `json:"intent_sha256"`
}

type LiveReport struct {
	Status       string          `json:"status"`
	CodeRevision string          `json:"code_revision"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Journal      JournalSnapshot `json:"journal"`
}

func WriteLiveReport(path string, r LiveReport) error {
	b, err := domain.CanonicalJSON(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".v1-report-*")
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

type readyIntent struct {
	intent Intent
	ready  time.Time
}

type Journal struct {
	intents, acks *os.File
	hash          hash.Hash
	pending       []readyIntent
	snapshot      JournalSnapshot
	failed        error
}

func NewJournal(dir, runID string, cfg Config) (*Journal, error) {
	digest, err := cfg.Digest()
	if err != nil {
		return nil, err
	}
	intents, err := os.OpenFile(filepath.Join(dir, "v1-intents.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	acks, err := os.OpenFile(filepath.Join(dir, "v1-live-acks.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		intents.Close()
		return nil, err
	}
	if err := durable.SyncDir(dir); err != nil {
		intents.Close()
		acks.Close()
		return nil, err
	}
	return &Journal{intents: intents, acks: acks, hash: sha256.New(),
		snapshot: JournalSnapshot{SchemaVersion: SchemaVersion, RunID: runID, ConfigSHA256: digest}}, nil
}

func (j *Journal) Append(i Intent, ready time.Time) error {
	if j.failed != nil {
		return j.failed
	}
	if i.RunID != j.snapshot.RunID || i.ConfigSHA256 != j.snapshot.ConfigSHA256 ||
		i.AsOfOrdinal <= j.snapshot.LastIntentOrdinal {
		return j.fail(fmt.Errorf("V1 intent provenance/order mismatch at %d", i.AsOfOrdinal))
	}
	b, err := domain.CanonicalJSON(i)
	if err != nil {
		return j.fail(err)
	}
	b = append(b, '\n')
	if _, err := j.intents.Write(b); err != nil {
		return j.fail(err)
	}
	_, _ = j.hash.Write(b)
	j.snapshot.Intents++
	j.snapshot.LastIntentOrdinal = i.AsOfOrdinal
	j.pending = append(j.pending, readyIntent{intent: i, ready: ready})
	return nil
}

// Flush makes the canonical intent bytes durable before recording a separate
// operational acknowledgment. A missing ack after a crash is conservative:
// the intent remains recoverable but is not claimed as published.
func (j *Journal) Flush(gateOpen bool) error {
	if j.failed != nil {
		return j.failed
	}
	if len(j.pending) == 0 {
		return nil
	}
	if err := j.intents.Sync(); err != nil {
		return j.fail(err)
	}
	durableAt := time.Now().UTC()
	for _, p := range j.pending {
		ack := LiveAck{DecisionID: p.intent.DecisionID, AsOfOrdinal: p.intent.AsOfOrdinal,
			Action: p.intent.Action, DecisionReadyAt: p.ready, IntentDurableAt: durableAt, GateOpen: gateOpen}
		if !gateOpen && p.intent.Action != NoTrade {
			ack.VetoReason = "LIVE_GATE_CLOSED"
			j.snapshot.GateVetoes++
		}
		b, err := domain.CanonicalJSON(ack)
		if err != nil {
			return j.fail(err)
		}
		if _, err := j.acks.Write(append(b, '\n')); err != nil {
			return j.fail(err)
		}
	}
	if err := j.acks.Sync(); err != nil {
		return j.fail(err)
	}
	j.snapshot.Acknowledged += len(j.pending)
	j.pending = j.pending[:0]
	return nil
}

func (j *Journal) Snapshot() JournalSnapshot {
	s := j.snapshot
	s.IntentSHA256 = hex.EncodeToString(j.hash.Sum(nil))
	return s
}

func (j *Journal) fail(err error) error {
	if j.failed == nil {
		j.failed = err
	}
	return j.failed
}

func (j *Journal) Close() error {
	var first error
	for _, f := range []*os.File{j.intents, j.acks} {
		if f == nil {
			continue
		}
		if err := f.Sync(); err != nil && first == nil {
			first = err
		}
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

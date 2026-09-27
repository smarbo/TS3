package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
	"ts3/internal/durable"
)

const bucketWidthNS int64 = int64(100 * time.Microsecond)

type Histogram struct {
	buckets  map[int64]uint64
	count    uint64
	max      int64
	negative uint64
}
type Quantiles struct {
	Samples         uint64 `json:"samples"`
	P50NS           int64  `json:"p50_ns"`
	P99NS           int64  `json:"p99_ns"`
	MaxNS           int64  `json:"max_ns"`
	NegativeSamples uint64 `json:"negative_samples"`
}

func (h *Histogram) Add(ns int64) {
	if ns < 0 {
		h.negative++
		return
	}
	if h.buckets == nil {
		h.buckets = make(map[int64]uint64)
	}
	h.buckets[ns/bucketWidthNS]++
	h.count++
	if ns > h.max {
		h.max = ns
	}
}
func (h Histogram) Summary() Quantiles {
	q := Quantiles{Samples: h.count, MaxNS: h.max, NegativeSamples: h.negative}
	if h.count == 0 {
		return q
	}
	keys := make([]int64, 0, len(h.buckets))
	for k := range h.buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var seen uint64
	set50, set99 := false, false
	p50 := (h.count + 1) / 2
	p99 := (99*h.count + 99) / 100
	for _, k := range keys {
		seen += h.buckets[k]
		if !set50 && seen >= p50 {
			q.P50NS = k * bucketWidthNS
			set50 = true
		}
		if !set99 && seen >= p99 {
			q.P99NS = k * bucketWidthNS
			set99 = true
		}
	}
	return q
}

type Metrics struct {
	RunID            string                `json:"run_id"`
	StartedAt        time.Time             `json:"started_at"`
	EndedAt          time.Time             `json:"ended_at"`
	Counts           map[string]uint64     `json:"counts"`
	Bytes            map[string]uint64     `json:"bytes"`
	QueueMax         int                   `json:"queue_max"`
	HealthDurationNS map[book.Health]int64 `json:"health_duration_ns"`
	AdmissionLag     Quantiles             `json:"admission_lag"`
	CommitLag        Quantiles             `json:"commit_lag"`
	ApplyLag         Quantiles             `json:"apply_lag"`
	TickLag          Quantiles             `json:"tick_lag"`
	DataFsync        Quantiles             `json:"data_fsync"`
	MarkerFsync      Quantiles             `json:"marker_fsync"`
	CommitBatch      Quantiles             `json:"commit_batch"`
}

type Collector struct {
	metrics                                                       Metrics
	admission, commit, apply, tick, dataFsync, markerFsync, batch Histogram
	lastHealth                                                    book.Health
	lastReason                                                    string
	lastMono                                                      int64
	lastConfirmedHealthyAt                                        time.Time
	pendingHealthy                                                bool
	reconnects, captureGaps, staleIncidents, qualityIncidents     uint64
}

type LiveState struct {
	Health                   book.Health
	Reason                   string
	LatestConfirmedHealthyAt time.Time
	Reconnects               uint64
	CaptureGaps              uint64
	StaleFeedIncidents       uint64
	DataQualityIncidents     uint64
}

func (c *Collector) LiveState() LiveState {
	return LiveState{c.lastHealth, c.lastReason, c.lastConfirmedHealthyAt, c.reconnects, c.captureGaps, c.staleIncidents, c.qualityIncidents}
}

// Confirmation happens only after the derived files and progress marks fsync.
func (c *Collector) ConfirmHealthy(at time.Time) {
	if c.pendingHealthy {
		c.lastConfirmedHealthyAt = at.UTC()
		c.pendingHealthy = false
	}
}

func New(runID string, start time.Time) *Collector {
	return &Collector{metrics: Metrics{RunID: runID, StartedAt: start.UTC(), Counts: map[string]uint64{}, Bytes: map[string]uint64{}, HealthDurationNS: map[book.Health]int64{}}}
}
func (c *Collector) QueueDepth(n int) {
	if n > c.metrics.QueueMax {
		c.metrics.QueueMax = n
	}
}
func (c *Collector) Accepted(r domain.RawRecord) {
	if r.Kind == domain.Connected && r.ConnectionEpoch > 1 {
		c.reconnects++
	}
	if r.Kind == domain.CaptureGap {
		c.captureGaps++
	}
	kind := string(r.Kind)
	if r.Kind == domain.WSFrame {
		var h struct {
			Channel string `json:"channel"`
			Type    string `json:"type"`
			Method  string `json:"method"`
		}
		if json.Unmarshal(r.Payload, &h) == nil {
			switch {
			case h.Channel == "book" && (h.Type == "snapshot" || h.Type == "update"):
				kind = "ws:book:" + h.Type
			case h.Channel == "heartbeat":
				kind = "ws:heartbeat"
			case h.Channel == "status":
				kind = "ws:status"
			case h.Method == "subscribe":
				kind = "ws:subscribe"
			default:
				kind = "ws:other"
			}
		} else {
			kind = "ws:malformed"
		}
	}
	c.metrics.Counts[kind]++
	c.metrics.Bytes[kind] += uint64(len(r.Payload))
	c.admission.Add(r.AdmissionTime.Sub(r.ReceiveTime).Nanoseconds())
}
func (c *Collector) Committed(batch []domain.RawRecord, at time.Time, duration, dataSync, markerSync time.Duration) {
	c.batch.Add(duration.Nanoseconds())
	c.dataFsync.Add(dataSync.Nanoseconds())
	c.markerFsync.Add(markerSync.Nanoseconds())
	for _, r := range batch {
		c.commit.Add(at.Sub(r.AdmissionTime).Nanoseconds())
	}
}
func (c *Collector) Applied(batch []domain.RawRecord, at time.Time) {
	for _, r := range batch {
		c.apply.Add(at.Sub(r.AdmissionTime).Nanoseconds())
		if r.Kind == domain.ClockTick {
			c.tick.Add(at.Sub(r.ReceiveTime).Nanoseconds())
		}
	}
}
func (c *Collector) Health(v book.View, mono int64) {
	if c.lastHealth != "" && mono >= c.lastMono {
		c.metrics.HealthDurationNS[c.lastHealth] += mono - c.lastMono
	}
	if v.Health != c.lastHealth || v.Reason != c.lastReason {
		if v.Reason == "HEARTBEAT_STALE" || v.Reason == "BOOK_QUIET" {
			c.staleIncidents++
		}
		if strings.HasPrefix(v.Reason, "INVALID_") || strings.HasPrefix(v.Reason, "MALFORMED_") || v.Reason == "BOOK_CHECKSUM" || v.Reason == "PRODUCT_MISMATCH" || v.Reason == "SUBSCRIPTION_MISMATCH" {
			c.qualityIncidents++
		}
	}
	c.lastHealth = v.Health
	c.lastReason = v.Reason
	if v.Health == book.Healthy {
		c.pendingHealthy = true
	}
	c.lastMono = mono
	if v.Health == book.Unhealthy && v.Reason != "" {
		reason := v.Reason
		if len(reason) > 64 {
			reason = "OTHER"
		} else {
			for _, ch := range reason {
				if (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' {
					reason = "OTHER"
					break
				}
			}
		}
		c.metrics.Counts["unhealthy:"+reason]++
	}
}
func (c *Collector) Snapshot(end time.Time, elapsed time.Duration) Metrics {
	m := c.metrics
	m.Counts = make(map[string]uint64, len(c.metrics.Counts))
	for k, v := range c.metrics.Counts {
		m.Counts[k] = v
	}
	m.Bytes = make(map[string]uint64, len(c.metrics.Bytes))
	for k, v := range c.metrics.Bytes {
		m.Bytes[k] = v
	}
	m.HealthDurationNS = make(map[book.Health]int64, len(c.metrics.HealthDurationNS))
	for k, v := range c.metrics.HealthDurationNS {
		m.HealthDurationNS[k] = v
	}
	m.EndedAt = end.UTC()
	if c.lastHealth != "" && elapsed.Nanoseconds() >= c.lastMono {
		m.HealthDurationNS[c.lastHealth] += elapsed.Nanoseconds() - c.lastMono
	}
	m.AdmissionLag = c.admission.Summary()
	m.CommitLag = c.commit.Summary()
	m.ApplyLag = c.apply.Summary()
	m.TickLag = c.tick.Summary()
	m.DataFsync = c.dataFsync.Summary()
	m.MarkerFsync = c.markerFsync.Summary()
	m.CommitBatch = c.batch.Summary()
	return m
}
func Write(path string, m Metrics) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	b, err := domain.CanonicalJSON(m)
	if err != nil {
		f.Close()
		return err
	}
	b = append(b, '\n')
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
	return durable.SyncDir(filepath.Dir(path))
}

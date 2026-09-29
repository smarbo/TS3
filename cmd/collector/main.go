package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
	"ts3/internal/ingress"
	"ts3/internal/processor"
	"ts3/internal/record"
	"ts3/internal/soakreport"
	"ts3/internal/source/kraken"
	"ts3/internal/source/wsclient"
	"ts3/internal/telemetry"
	"ts3/internal/v1"
	"ts3/internal/watchdog"
)

type queued struct {
	observation domain.Observation
	ack         chan error
}

func main() {
	if err := run(); err != nil {
		slog.Error("collector failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("dir", "data", "recording directory")
	runID := flag.String("run", "", "unique run ID (default UTC timestamp)")
	duration := flag.Duration("duration", 0, "capture duration; 0 runs until interrupted")
	endpoint := flag.String("endpoint", kraken.Endpoint, "public WebSocket endpoint")
	enableV1 := flag.Bool("v1", false, "write V1 shadow analytical intents alongside V0 capture")
	flag.Parse()
	if *runID == "" {
		*runID = time.Now().UTC().Format("20060102T150405Z")
	}
	if *duration < 0 {
		return errors.New("negative duration")
	}
	configBytes, err := domain.CanonicalJSON(struct {
		Endpoint      string   `json:"endpoint"`
		Product       string   `json:"product"`
		Channels      []string `json:"channels"`
		QueueCapacity int      `json:"queue_capacity"`
	}{*endpoint, kraken.Symbol, []string{"book:100", "heartbeat:auto"}, 8192})
	if err != nil {
		return err
	}
	configDigest := sha256.Sum256(configBytes)
	codeRevision := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				codeRevision = setting.Value
			}
		}
	}
	if codeRevision == "unknown" {
		if b, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
			codeRevision = strings.TrimSpace(string(b))
		}
	}
	if b, err := exec.Command("git", "status", "--porcelain").Output(); err == nil && len(b) > 0 {
		codeRevision += "+dirty"
	}
	if err := os.MkdirAll(*dir, 0755); err != nil {
		return err
	}
	if err := markPriorInterrupted(*dir); err != nil {
		return fmt.Errorf("mark prior run incomplete: %w", err)
	}
	gapReason, err := priorGap(*dir)
	if err != nil {
		return err
	}
	if gapReason != "" {
		slog.Warn("previous run incomplete; waiting for stable clock", "reason", gapReason)
		if err := awaitStableClock(context.Background()); err != nil {
			return err
		}
	}
	outputDir := filepath.Join(*dir, *runID)
	writer, err := record.NewWriter(*dir, *runID)
	if err != nil {
		return err
	}
	proc, err := processor.New(outputDir, true)
	if err != nil {
		writer.Close(false)
		return err
	}
	procClosed := false
	defer func() {
		if !procClosed {
			_ = proc.Close()
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	start := time.Now()
	metrics := telemetry.New(*runID, start)
	baseReport := soakreport.Report{RunID: *runID, CodeRevision: codeRevision, GoVersion: runtime.Version(), ConfigSHA256: hex.EncodeToString(configDigest[:]), SourceEndpoint: *endpoint, StartedAt: start.UTC()}
	if err := writeRunReport(*dir, *runID, baseReport, metrics, writer, proc, start, soakreport.Running); err != nil {
		_ = writer.Close(false)
		return fmt.Errorf("persist initial run report: %w", err)
	}
	var analysis *v1.Engine
	var intentJournal *v1.Journal
	v1ReportPath := filepath.Join(outputDir, "v1-report.json")
	journalClosed := false
	if *enableV1 {
		analysis, err = v1.NewEngine(*runID, v1.DefaultConfig())
		if err != nil {
			_ = writer.Close(false)
			return err
		}
		intentJournal, err = v1.NewJournal(outputDir, *runID, v1.DefaultConfig())
		if err != nil {
			_ = writer.Close(false)
			return err
		}
		if err := v1.WriteLiveReport(v1ReportPath, v1.LiveReport{Status: "RUNNING", CodeRevision: codeRevision, UpdatedAt: time.Now().UTC(), Journal: intentJournal.Snapshot()}); err != nil {
			_ = intentJournal.Close()
			_ = writer.Close(false)
			return err
		}
		defer func() {
			if !journalClosed {
				_ = intentJournal.Close()
			}
		}()
	}
	metadataOrdinal := uint64(0)
	provenance := func() record.Provenance {
		marks := proc.Progress()
		return record.Provenance{SourceEndpoint: *endpoint, ProductID: kraken.Symbol, Channels: []string{"book:100", "heartbeat:auto"}, StartedAt: start.UTC(), EndedAt: time.Now().UTC(), CodeRevision: codeRevision, GoVersion: runtime.Version(), ConfigSHA256: hex.EncodeToString(configDigest[:]), MetadataOrdinal: metadataOrdinal, AppliedOrdinal: marks.Applied, PublishedOrdinal: marks.Published}
	}
	coord := ingress.New(*runID, start)
	queue := make(chan queued, 8192)
	reconnect := make(chan uint64, 16)
	fatal := make(chan error, 4)
	var wg sync.WaitGroup
	var gate watchdog.Gate
	var sawHealthy atomic.Bool
	if gapReason != "" {
		now := time.Now().UTC()
		r, err := coord.Accept(domain.Observation{SourceID: "kraken-collector", Kind: domain.CaptureGap, ReceiveTime: now, Payload: []byte(gapReason)}, now, time.Since(start))
		if err != nil {
			_ = writer.Close(false)
			return err
		}
		metrics.Accepted(r)
		began := time.Now()
		if err := writer.AppendBatch([]domain.RawRecord{r}); err != nil {
			_ = writer.Close(false)
			return err
		}
		ds, ms := writer.LastFsyncDurations()
		metrics.Committed([]domain.RawRecord{r}, time.Now().UTC(), time.Since(began), ds, ms)
		v, err := proc.Apply(r)
		if err != nil {
			_ = writer.Close(false)
			return err
		}
		metrics.Health(v, r.MonotonicElapsedNS)
		if err := proc.Flush(); err != nil {
			_ = writer.Close(false)
			return err
		}
		metrics.Applied([]domain.RawRecord{r}, time.Now().UTC())
		metrics.ConfirmHealthy(time.Now().UTC())
		gate.Progress(time.Since(start))
		slog.Warn("capture gap admitted", "ordinal", r.Ordinal, "reason", gapReason)
	}
	emit := func(o domain.Observation, wait bool) error {
		return enqueue(ctx, queue, o, wait, time.Second)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case tm := <-ticker.C:
				if err := emit(domain.Observation{SourceID: "kraken-clock", Kind: domain.ClockTick, ReceiveTime: tm.UTC()}, false); err != nil {
					if ctx.Err() == nil {
						gate.Fail()
						select {
						case fatal <- err:
						default:
						}
						cancel()
					}
					return
				}
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := captureSource(ctx, *endpoint, emit, reconnect); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			gate.Fail()
			select {
			case fatal <- err:
			default:
			}
			cancel()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchGate(ctx, &gate, start, func(tickLag, progressLag time.Duration) {
			slog.Error("operational availability withdrawn", "tick_lag_ns", tickLag.Nanoseconds(), "progress_lag_ns", progressLag.Nanoseconds())
			select {
			case queue <- queued{observation: domain.Observation{SourceID: "kraken-watchdog", Kind: domain.WatchdogIncident, ReceiveTime: time.Now().UTC(), Payload: []byte(`{"reason":"LIVE_PROGRESS_STALE"}`)}}:
			default:
				slog.Error("watchdog incident could not enter full ingress queue")
			}
			cancel()
		})
	}()
	go func() { wg.Wait(); close(queue) }()
	batch := make([]domain.RawRecord, 0, 1024)
	acks := make([]chan error, 0, 1024)
	flush := func() (flushErr error) {
		defer func() {
			if flushErr != nil {
				gate.Fail()
			}
		}()
		if len(batch) == 0 {
			return nil
		}
		var lastView book.View
		appliedTick := false
		v1Pending := make([]struct {
			intent v1.Intent
			ready  time.Time
		}, 0, 2)
		began := time.Now()
		if err := writer.AppendBatch(batch); err != nil {
			return err
		}
		dataSync, markerSync := writer.LastFsyncDurations()
		metrics.Committed(batch, time.Now().UTC(), time.Since(began), dataSync, markerSync)
		if time.Since(began) > time.Second {
			return errors.New("raw commit lag exceeded one second")
		}
		for _, r := range batch {
			v, err := proc.Apply(r)
			if err != nil {
				return err
			}
			if analysis != nil && r.Kind == domain.ClockTick {
				intent, err := analysis.ApplyTick(v, proc.Quote())
				if err != nil {
					return err
				}
				if intent != nil {
					v1Pending = append(v1Pending, struct {
						intent v1.Intent
						ready  time.Time
					}{*intent, time.Now().UTC()})
				}
			}
			lastView = v
			appliedTick = appliedTick || r.Kind == domain.ClockTick
			metrics.Health(v, r.MonotonicElapsedNS)
			if v.Health != book.Healthy {
				gate.Applied(v.Health, false, time.Since(start))
			}
			if v.Health == book.Unhealthy && r.ConnectionEpoch > 0 {
				select {
				case reconnect <- r.ConnectionEpoch:
				default:
				}
			}
		}
		if err := proc.Flush(); err != nil {
			return err
		}
		metrics.Applied(batch, time.Now().UTC())
		metrics.ConfirmHealthy(time.Now().UTC())
		if time.Since(began) > time.Second {
			return errors.New("state/publication lag exceeded one second")
		}
		if lastView.Health == book.Healthy {
			sawHealthy.Store(true)
		}
		if gate.Applied(lastView.Health, appliedTick, time.Since(start)) {
			slog.Info("book healthy", "ordinal", lastView.Ordinal, "epoch", lastView.Epoch)
		}
		gate.Progress(time.Since(start))
		if intentJournal != nil {
			for _, pending := range v1Pending {
				if err := intentJournal.Append(pending.intent, pending.ready); err != nil {
					return err
				}
			}
			if err := intentJournal.Flush(gate.Open()); err != nil {
				return err
			}
			if time.Since(began) > time.Second {
				return errors.New("V1 shadow journal lag exceeded one second")
			}
		}
		for _, ack := range acks {
			if ack != nil {
				ack <- nil
			}
		}
		batch = batch[:0]
		acks = acks[:0]
		return nil
	}
	flushTicker := time.NewTicker(100 * time.Millisecond)
	defer flushTicker.Stop()
	reportTicker := time.NewTicker(5 * time.Second)
	defer reportTicker.Stop()
	clean := true
	var pipelineErr error
	for {
		select {
		case q, ok := <-queue:
			if !ok {
				if pipelineErr != nil {
					for _, ack := range acks {
						if ack != nil {
							ack <- pipelineErr
						}
					}
					_ = telemetry.Write(filepath.Join(outputDir, "metrics.json"), metrics.Snapshot(time.Now().UTC(), time.Since(start)))
					if intentJournal != nil {
						_ = v1.WriteLiveReport(v1ReportPath, v1.LiveReport{Status: "INCOMPLETE", CodeRevision: codeRevision, UpdatedAt: time.Now().UTC(), Journal: intentJournal.Snapshot()})
					}
					writer.SetProvenance(provenance())
					_ = writer.Close(false)
					_ = writeRunReport(*dir, *runID, baseReport, metrics, writer, proc, start, soakreport.Incomplete, pipelineErr.Error())
					return pipelineErr
				}
				if gate.Tripped() {
					clean = false
				}
				if coord.IsActive() {
					r, e := coord.Accept(domain.Observation{SourceID: "kraken", Epoch: coord.ActiveEpoch(), Kind: domain.Disconnected, ReceiveTime: time.Now().UTC(), Payload: []byte("SHUTDOWN")}, time.Now().UTC(), time.Since(start))
					if e == nil {
						batch = append(batch, r)
						metrics.Accepted(r)
					}
				}
				r, e := coord.Accept(domain.Observation{SourceID: "kraken-collector", Kind: domain.Shutdown, ReceiveTime: time.Now().UTC()}, time.Now().UTC(), time.Since(start))
				if e == nil {
					batch = append(batch, r)
					metrics.Accepted(r)
				}
				if err := flush(); err != nil {
					clean = false
					slog.Error("final flush failed", "error", err)
				}
				var fatalErr error
				select {
				case fatalErr = <-fatal:
					clean = false
				default:
				}
				if err := telemetry.Write(filepath.Join(outputDir, "metrics.json"), metrics.Snapshot(time.Now().UTC(), time.Since(start))); err != nil {
					clean = false
					slog.Error("metrics write failed", "error", err)
				}
				if err := proc.Close(); err != nil {
					clean = false
					slog.Error("derived output close failed", "error", err)
				}
				procClosed = true
				if intentJournal != nil {
					if err := intentJournal.Close(); err != nil {
						clean = false
						slog.Error("V1 journal close failed", "error", err)
					}
					journalClosed = true
				}
				writer.SetProvenance(provenance())
				if err := writer.Close(clean); err != nil {
					return err
				}
				reportStatus := soakreport.Complete
				if !clean || !sawHealthy.Load() {
					reportStatus = soakreport.Incomplete
				}
				if err := writeRunReport(*dir, *runID, baseReport, metrics, writer, proc, start, reportStatus); err != nil {
					return fmt.Errorf("final run report: %w", err)
				}
				if intentJournal != nil {
					if err := v1.WriteLiveReport(v1ReportPath, v1.LiveReport{Status: string(reportStatus), CodeRevision: codeRevision, UpdatedAt: time.Now().UTC(), Journal: intentJournal.Snapshot()}); err != nil {
						return fmt.Errorf("final V1 report: %w", err)
					}
				}
				nh, sh := proc.Hashes()
				slog.Info("capture finished", "run", *runID, "committed", writer.CommittedOrdinal(), "normalized_sha256", nh, "state_sha256", sh, "gate_open", gate.Open(), "go", runtime.Version())
				if fatalErr != nil {
					return fatalErr
				}
				if !clean {
					return errors.New("capture incomplete")
				}
				if !sawHealthy.Load() {
					return errors.New("capture ended without a healthy book")
				}
				return nil
			}
			metrics.QueueDepth(len(queue))
			if pipelineErr != nil {
				if q.ack != nil {
					q.ack <- pipelineErr
				}
				continue
			}
			r, e := coord.Accept(q.observation, time.Now().UTC(), time.Since(start))
			if e != nil && r.Ordinal == 0 {
				if q.ack != nil {
					q.ack <- e
				}
				clean = false
				gate.Fail()
				cancel()
				continue
			}
			batch = append(batch, r)
			metrics.Accepted(r)
			if r.Kind == domain.MetadataResponse {
				metadataOrdinal = r.Ordinal
			}
			acks = append(acks, q.ack)
			if e != nil {
				clean = false
				gate.Fail()
				cancel()
			}
			if len(batch) >= 1024 {
				if err := flush(); err != nil {
					pipelineErr = err
					clean = false
					slog.Error("pipeline failed", "error", err)
					cancel()
				}
			}
		case <-flushTicker.C:
			if pipelineErr != nil {
				continue
			}
			if err := flush(); err != nil {
				pipelineErr = err
				clean = false
				slog.Error("pipeline failed", "error", err)
				cancel()
			}
		case <-reportTicker.C:
			if pipelineErr != nil {
				continue
			}
			if err := flush(); err != nil {
				pipelineErr = err
				clean = false
				slog.Error("pipeline failed", "error", err)
				cancel()
				continue
			}
			if err := writeRunReport(*dir, *runID, baseReport, metrics, writer, proc, start, soakreport.Running); err != nil {
				pipelineErr = fmt.Errorf("periodic run report: %w", err)
				clean = false
				gate.Fail()
				slog.Error("run report failed", "error", err)
				cancel()
			}
			if intentJournal != nil && pipelineErr == nil {
				if err := v1.WriteLiveReport(v1ReportPath, v1.LiveReport{Status: "RUNNING", CodeRevision: codeRevision, UpdatedAt: time.Now().UTC(), Journal: intentJournal.Snapshot()}); err != nil {
					pipelineErr = fmt.Errorf("periodic V1 report: %w", err)
					clean = false
					gate.Fail()
					slog.Error("V1 report failed", "error", err)
					cancel()
				}
			}
		}
	}
}

func watchGate(ctx context.Context, gate *watchdog.Gate, start time.Time, onTrip func(time.Duration, time.Duration)) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			trip, tickLag, progressLag := gate.Check(time.Since(start))
			if trip {
				onTrip(tickLag, progressLag)
				return
			}
		}
	}
}

// enqueue bounds both producer handoff and requested commit acknowledgment by
// one elapsed deadline. A caller must fail the run after a timeout because the
// admission outcome of an already queued observation may be uncertain.
func enqueue(ctx context.Context, queue chan<- queued, o domain.Observation, wait bool, timeout time.Duration) error {
	q := queued{observation: o}
	if wait {
		q.ack = make(chan error, 1)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case queue <- q:
	case <-timer.C:
		return errors.New("ingress handoff exceeded one second")
	case <-ctx.Done():
		return ctx.Err()
	}
	if wait {
		select {
		case err := <-q.ack:
			return err
		case <-timer.C:
			return errors.New("ingress/commit acknowledgment exceeded one second")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func captureSource(ctx context.Context, endpoint string, emit func(domain.Observation, bool) error, reconnect <-chan uint64) error {
	return captureSourceWith(ctx, endpoint, emit, reconnect, func(ctx context.Context) ([]byte, time.Time, error) {
		body, _, received, err := kraken.FetchMetadata(ctx, nil)
		return body, received, err
	}, 10*time.Second)
}

func captureSourceWith(ctx context.Context, endpoint string, emit func(domain.Observation, bool) error, reconnect <-chan uint64, fetchMetadata func(context.Context) ([]byte, time.Time, error), initializationTimeout time.Duration) error {
	if err := emit(domain.Observation{SourceID: "kraken-product", Kind: domain.MetadataRequest, ReceiveTime: time.Now().UTC()}, true); err != nil {
		return err
	}
	body, received, err := fetchMetadata(ctx)
	if e := emit(domain.Observation{SourceID: "kraken-product", Kind: domain.MetadataResponse, ReceiveTime: received, Payload: body}, true); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	epoch := uint64(0)
	backoff := time.Second
	for ctx.Err() == nil {
		epoch++
		ws, err := wsclient.Dial(ctx, endpoint)
		if err == nil {
			if err = emit(domain.Observation{SourceID: "kraken", Epoch: epoch, Kind: domain.Connected, ReceiveTime: time.Now().UTC()}, true); err != nil {
				ws.Close()
				return err
			}
			if err = kraken.Subscribe(ws); err == nil {
				_ = ws.SetReadDeadline(time.Now().Add(initializationTimeout))
				acknowledged := false
				terminalRejection := false
				watchDone := make(chan struct{})
				watchExited := make(chan struct{})
				go func(current uint64) {
					defer close(watchExited)
					for {
						select {
						case bad := <-reconnect:
							if bad == current {
								_ = ws.Close()
								return
							}
						case <-watchDone:
							return
						case <-ctx.Done():
							return
						}
					}
				}(epoch)
				for ctx.Err() == nil {
					msg, e := ws.Read()
					if e != nil {
						err = e
						break
					}
					if e = emit(domain.Observation{SourceID: "kraken", Epoch: epoch, Kind: domain.WSFrame, ReceiveTime: time.Now().UTC(), Payload: msg}, false); e != nil {
						err = e
						break
					}
					var header struct {
						Type    string `json:"type"`
						Channel string `json:"channel"`
						Method  string `json:"method"`
						Success *bool  `json:"success"`
						Error   string `json:"error"`
					}
					if json.Unmarshal(msg, &header) == nil {
						if header.Method == "subscribe" && header.Success != nil && !*header.Success {
							err = fmt.Errorf("source rejected subscription: %s", header.Error)
							terminalRejection = true
							break
						}
						if header.Method == "subscribe" && !acknowledged {
							acknowledged = true
							_ = ws.SetReadDeadline(time.Now().Add(initializationTimeout))
						}
						if header.Channel == "book" && header.Type == "snapshot" && acknowledged {
							_ = ws.SetReadDeadline(time.Time{})
							backoff = time.Second
						}
					}
				}
				close(watchDone)
				<-watchExited
				if terminalRejection {
					ws.Close()
					_ = emit(domain.Observation{SourceID: "kraken", Epoch: epoch, Kind: domain.Disconnected, ReceiveTime: time.Now().UTC(), Payload: []byte(err.Error())}, true)
					return err
				}
			}
			ws.Close()
			_ = emit(domain.Observation{SourceID: "kraken", Epoch: epoch, Kind: domain.Disconnected, ReceiveTime: time.Now().UTC(), Payload: []byte(fmt.Sprint(err))}, true)
		} else {
			_ = emit(domain.Observation{SourceID: "kraken", Kind: domain.SourceError, ReceiveTime: time.Now().UTC(), Payload: []byte(err.Error())}, false)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("reconnecting public feed", "epoch", epoch, "error", err)
		jitter := time.Duration(rand.IntN(401)-200) * backoff / 1000
		wait := backoff + jitter
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
	return ctx.Err()
}

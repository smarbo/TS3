# Adversarial architecture review

Review date: 2026-09-27. Scope: `AGENTS.md`, `SPEC.md`, `ARCHITECTURE.md`, `RESEARCH.md`, `PLAN.md`, and `STATUS.md` as design documents. No engine, capture, backtest, or empirical result exists. Findings below are failure modes permitted by incomplete contracts, not observed defects or claims of profitability. References to source protocol use the [Coinbase WebSocket overview](https://docs.cdp.coinbase.com/exchange/websocket-feed/overview), [channels](https://docs.cdp.coinbase.com/exchange/websocket-feed/channels), and [best practices](https://docs.cdp.coinbase.com/exchange/websocket-feed/best-practices).

The most serious replay risk is **backdating**: recording an event's receive time does not mean the live decision engine could act at that time. Raw commitment, queueing, normalization, health checks, feature computation, and publication can all occur later. A replay can reproduce the same state bytes while a historical P&L study still assumes an impossible earlier entry.

## Findings

### F01 — A decision can be priced before it existed

- **Severity:** CRITICAL
- **Subsystem:** Time semantics; V1+ opportunity evaluation.
- **Failure mechanism:** `UsableFromTime` is the frame's read-completion time, but the recorder forwards only after durable commitment (`ARCHITECTURE.md`, capture contract), and the engine and output sink run later. `TradeIntent.as_of_time` has no separate decision-ready, published, or hypothetical-entry time. A replay can label and price an intent at `ReceiveTime` even when a live observer received it after a queue or fsync delay. Byte-identical replay does not test economic causality.
- **Realistic scenario:** At 12:00:00.000 an update is read during a disk stall. It is committed and processed at 12:00:02.000. The quote has moved by then. A backtest uses the 12:00:00 ask for a `LONG` that could only have been emitted at 12:00:02.
- **Likely consequence:** False net profitability, understated adverse selection, and misleading latency sensitivity.
- **Existing safeguards:** Receipt stamping, ordered commitment, processing-lag metrics, and cost/latency language help expose the delay, but do not define the earliest feasible entry time or an eligibility rule for delayed decisions.
- **Smallest robust correction:** Keep recorded receipt time for information availability. Separately record live processing/output time and define a conservative, causally available entry observation after output plus predeclared latency. Historical evaluation must never fill against a quote known only before that point. Mark excessive processing lag as unavailable for actionable intents.
- **Test:** Stall recorder and output sink across a price jump; verify the live intent's timing fields and that the evaluator cannot enter at the pre-stall quote. Replay state hashes may still match.

### F02 — A forward wall-clock jump can poison logical time long after health recovers

- **Severity:** HIGH
- **Subsystem:** Clock and staleness.
- **Failure mechanism:** `UsableFromTime = max(ReceiveTime, prior usable time)` (`ARCHITECTURE.md`, clock contract). If the host clock jumps one hour forward and then is corrected, all later events are clamped to that future time until actual wall time catches up. The specified ten stable ticks can clear time degradation while logical time remains pinned. Equal-time ticks and market events can then collapse an hour of rolling windows and quote ages.
- **Realistic scenario:** NTP misconfiguration advances the clock to 13:00 for one tick at 12:00, then restores it. At 12:00:10 time health clears, but the next hour of raw records has `usable_from_time = 13:00`.
- **Likely consequence:** False freshness, invalid window statistics, wrong expiry and label boundaries, and a replay that deterministically reproduces a bad clock state.
- **Existing safeguards:** Anomaly records and temporary health degradation exist. Recovery is based on drift of later ticks, not on restoration of a trustworthy nondecreasing logical timeline.
- **Smallest robust correction:** Treat a large forward jump as a run or clock-epoch break, or derive post-anomaly logical time from a recorded monotonic anchor. Keep affected analysis unavailable until a monotonic, UTC-anchored timeline is established. Define whether `tick_time` and `usable_from_time` may differ.
- **Test:** Inject a +1-hour jump followed immediately by correction and eleven normal ticks; assert that health cannot become usable with a logical clock still one hour ahead and that windows do not silently collapse.

### F03 — Concurrent capture producers lack a defined ordering boundary

- **Severity:** MEDIUM
- **Subsystem:** Go concurrency; receive-time and control ordering.
- **Failure mechanism:** The socket owner reads frames, a clock producer makes ticks, and a recorder goroutine assigns ordinals (`ARCHITECTURE.md`, canonical model and ownership). The contract does not say at what point a read frame, tick, disconnect, or source error secures its place in the single recorder queue. Channel scheduling can let a later tick or status overtake an already read frame. The ordinal still gives a valid *admission* order under `SPEC.md`; the ambiguity is that `ReceiveTime` and the instruction to preserve raw arrival order can then suggest a different observation order to downstream research or incident analysis.
- **Realistic scenario:** A socket read finishes at 12:00:00.900, but its sender blocks. A 12:00:01 tick reaches the recorder first and declares the heartbeat expired. The earlier frame then appears at a later ordinal with an earlier `ReceiveTime`.
- **Likely consequence:** Misinterpreted health-transition timing and inconsistent use of receipt versus admission order in research. Replay equality alone would not detect that mismatch.
- **Existing safeguards:** A single recorder owner, ordinals, and equal-time tie rules guarantee a reproducible *recorded* order. They do not establish the capture linearization point across producers.
- **Smallest robust correction:** Specify one serialized ingress for frame completions and all control events, with a capture-order ticket assigned when each observation occurs. Preserve per-connection frame order and define deterministic ordering at tick boundaries. Distinguish observed-at, enqueued-at, and committed-at if they differ materially.
- **Test:** Pause the socket-to-recorder handoff after read completion while ticks and disconnect events fire; assert the documented order and resulting health state, not merely replay hash equality.

### F04 — A live backlog can delay the health failure it is supposed to detect

- **Severity:** HIGH
- **Subsystem:** Backpressure; stale-feed handling; continuous operation.
- **Failure mechanism:** Staleness changes are driven by recorded one-second ticks. If raw writing, normalization, or the state goroutine stalls, queued ticks are processed late. An intact but lagging process can keep exposing the last `HEALTHY` state while the heartbeat and quote are already old in wall time. The `CLOCK_TICK` record carries generation time, but no maximum tick-to-state latency is an invariant. Coinbase explicitly warns that backed-up clients can be disconnected as slow consumers ([best practices](https://docs.cdp.coinbase.com/exchange/websocket-feed/best-practices)).
- **Realistic scenario:** A ten-second fsync stall leaves the live dashboard's last health state `HEALTHY`; the queued ticks only mark `UNHEALTHY` after the stall, while the network may already have dropped updates.
- **Likely consequence:** Silent stale decisions or a misleading health display under precisely the load conditions where data quality is poorest.
- **Existing safeguards:** Bounded queues, blocked-time and tick-lag metrics, fail-closed recorder behavior. No hard lag threshold requires withdrawal of live analytical output before delayed ticks are consumed.
- **Smallest robust correction:** Add an independent operational watchdog over monotonic elapsed time since the last processed tick/heartbeat and suppress live outputs when maximum processing lag is exceeded. Record its transitions in the raw stream before resuming analytical output so replay can reproduce the conservative state. Define queue-full and blocked-time deadlines.
- **Test:** Freeze recorder/state for longer than the heartbeat limit while the socket remains connected; confirm the live output is withdrawn promptly, the incident is recorded, and replay produces the same later reason-coded transition.

### F05 — The recoverable raw prefix is not unambiguously committed

- **Severity:** HIGH
- **Subsystem:** Raw storage; crash recovery; deterministic replay.
- **Failure mechanism:** The design calls a batch committed after fsync, but recovery scans a valid prefix using lengths, CRCs, and segment hashes (`ARCHITECTURE.md`, raw storage). After a process crash, a complete record written before its fsync can survive in the filesystem cache and pass all structural checks, though it was never forwarded to the live engine. Conversely, a committed batch may lack the final segment manifest. The design does not encode the last acknowledged commit boundary in a separately durable way.
- **Realistic scenario:** Three records are written, the process crashes before batch fsync, and two complete records remain readable. Recovery accepts them as a valid prefix and replay advances the book through updates live never applied.
- **Likely consequence:** False live/replay divergence, ambiguous audit history, or silent inclusion of observations that were not live-admitted.
- **Existing safeguards:** CRC32C, SHA-256, ordinals, and truncation reporting detect corruption or torn bytes. They cannot prove a complete surviving record crossed the intended durability boundary.
- **Smallest robust correction:** Define and persist an explicit acknowledged commit offset/ordinal after data fsync, then forward only through that marker. Recovery accepts only marked prefixes; it reports any structurally valid uncommitted suffix separately. Specify crash behavior for an absent final manifest.
- **Test:** Kill the process at each step around write, data fsync, commit-marker fsync, and forwarding; compare the recovered *committed* prefix with the acknowledged committed boundary and separately report how far live processing reached.

### F06 — Product metadata can enter replay before its actual receipt

- **Severity:** HIGH
- **Subsystem:** Side-input provenance; product status.
- **Failure mechanism:** V0 puts startup product metadata and its request/response times in the *run manifest*, while runtime must reject an offline/disabled product (`PLAN.md`, V0 scope; `ARCHITECTURE.md`, source contract). The manifest is available in full to replay before the first raw record. There is no ordered metadata event or explicit barrier forbidding the engine from consulting the final manifest early.
- **Realistic scenario:** The WebSocket starts at 12:00:00. A product GET returns at 12:00:03 with status `online`. Replay code loads the manifest at startup and treats the first three seconds as known-online, although live code did not know that status then. The reverse case can retrospectively suppress an earlier observation.
- **Likely consequence:** Future-data leakage in eligibility, universe selection, and later hypothetical feasibility checks.
- **Existing safeguards:** The documents explicitly say metadata must not be inserted retroactively and record receipt time. Storage as run-level metadata does not enforce that rule at the engine boundary.
- **Smallest robust correction:** Record the request and response (or an immutable response reference) as ordered raw control events with a usable-from receipt time. Keep the manifest as an index only; replay must admit metadata through the same one-event-at-a-time path.
- **Test:** Put a delayed metadata response after several market frames; poison its status and verify no prior health/state/decision output changes.

### F07 — Subscription and product identity are not fully validated before book admission

- **Severity:** HIGH
- **Subsystem:** Coinbase adapter; order-book state.
- **Failure mechanism:** The plan requires a `subscriptions` acknowledgment and a snapshot, but does not explicitly require checking that the acknowledgment lists both `level2` and `heartbeat` for exactly `BTC-USD`, or that every heartbeat, snapshot, and update has the configured product ID. Coinbase's acknowledgment lists channel/product memberships ([overview](https://docs.cdp.coinbase.com/exchange/websocket-feed/overview)). A type-only acknowledgment check or parser that assumes the configured instrument could mark a wrong or partial subscription healthy.
- **Realistic scenario:** A server response acknowledges heartbeat but omits level2; a delayed or misrouted snapshot from another product is normalized as BTC-USD. The book is internally uncrossed and receives fresh heartbeats, so existing shape checks pass.
- **Likely consequence:** Incorrect market state and cost estimates with apparently healthy provenance.
- **Existing safeguards:** V0 specifies exact subscribe contents, current-epoch snapshot, and per-product source identifiers. The acknowledgment and every inbound `product_id` validation are implied, not stated as mandatory invariants.
- **Smallest robust correction:** Require exact channel/product membership in the acknowledgment and exact product identity on each market frame; mismatch invalidates the epoch, records a reason code, and triggers resubscription. Disallow snapshot admission before verified acknowledgment.
- **Test:** Fixtures for missing channel, extra/wrong product, duplicate acknowledgment, and wrong-product heartbeat/snapshot/delta must never reach `HEALTHY` or mutate the BTC-USD book.

### F08 — Snapshot replacement is safe only if epoch transitions are serialized with frames

- **Severity:** HIGH
- **Subsystem:** Reconnect; snapshot/delta ordering.
- **Failure mechanism:** The state contract rejects old-epoch deltas and waits for a new snapshot, but the handoff between socket owner, recorder, and state owner is not specified as an atomic epoch boundary. A disconnect/control message and frames already buffered from the old connection can be reordered by producers; a new connection's snapshot may be recorded before an old connection's final update or a late `DISCONNECTED` status. Even if the old update is discarded by epoch, a late status could invalidate the new book, and a mislabeled frame could corrupt it.
- **Realistic scenario:** Connection A reads its final delta; A's error path emits `DISCONNECTED`. Connection B starts and its snapshot is queued. The recorder receives B's snapshot before A's status. State briefly marks B healthy, then A's delayed status marks the single instrument unhealthy.
- **Likely consequence:** Intermittent false state, missed recovery, or contamination across generations; such races may only appear under stress.
- **Existing safeguards:** Epoch field, no carry-over book, and old-epoch delta quarantine address straightforward cases. The status transition's epoch applicability and cross-producer ordering remain undefined.
- **Smallest robust correction:** Make epoch-scoped state transitions conditional on current epoch and serialize connection close/open barriers with the socket's frame stream. Define exactly when an epoch stops admitting frames and when a new snapshot can become current.
- **Test:** Delay old-epoch delta and disconnect delivery while a new acknowledgment/snapshot arrives; verify generation, health, and book hash after every ordinal.

### F09 — Snapshot duplicate and zero-size levels can be silently canonicalized

- **Severity:** MEDIUM
- **Subsystem:** Book reconstruction; decimal normalization.
- **Failure mechanism:** A snapshot is loaded into maps keyed by canonical decimal price. The contract rejects negative sizes and later checks nonempty/uncrossed sides, but does not specify rejection of duplicate canonical levels (`1.0` versus `1.00`), zero-size snapshot levels, or duplicate entries with contradictory sizes. Map insertion may silently choose the last entry and produce a plausible book hash for an invalid source message.
- **Realistic scenario:** A malformed snapshot contains two bid entries for the same canonical price with different sizes. One implementation rejects it; another silently overwrites. Both may yield an uncrossed book, and the latter enters `HEALTHY`.
- **Likely consequence:** Corrupt depth and nondeterministic results across implementations or parser changes.
- **Existing safeguards:** Exact decimal parsing, atomic snapshot replacement, and crossed/empty checks. They do not define duplicate-level semantics or require strictly positive snapshot size.
- **Smallest robust correction:** Prevalidate the entire snapshot before mutation; require unique canonical price per side and positive size, with a documented bound on decimal scale/magnitude and level count. Reject the whole snapshot on violation.
- **Test:** Duplicate canonical price, conflicting duplicate, zero size, excessive scale, and oversized level-count fixtures must fail identically live and replay without replacing the previous valid book.

### F10 — A healthy heartbeat does not bound the age of the book

- **Severity:** HIGH
- **Subsystem:** Data quality propagation; opportunity filter (V1+); V0 observability.
- **Failure mechanism:** The V0 state can remain `HEALTHY` with a current heartbeat and an intact but quiet book until the 30-second activity warning. The documents correctly state heartbeat does not prove Level 2 completeness, but there is no global hard quote-age gate for a future cost estimate or intent; each signal declares its own staleness, while cost and decision paths may accidentally inherit `HEALTHY` as sufficient.
- **Realistic scenario:** Level 2 updates cease while heartbeat continues. At 20 seconds, a later V1 trend signal has a 30-second limit and an opportunity model uses the stale best ask as an immediate entry quote.
- **Likely consequence:** Actionable intents based on stale prices and overstated net opportunity.
- **Existing safeguards:** Separate quiet-book warning, per-signal staleness declarations, and planned stale-book tests. The quote/cost dependency and maximum acceptable quote age are not made a decision-level invariant.
- **Smallest robust correction:** Every feature, cost estimate, and final intent must carry input-specific freshness and generation. Require a predeclared maximum age for the executable quote used by opportunity evaluation; unknown or exceeded age forces `NO_TRADE`. Preserve the distinction between quiet market and proven loss.
- **Test:** Keep heartbeats flowing while suppressing book updates across the signal and cost freshness limits; verify exact reason codes and no directional intent, including after a reconnect.

### F11 — One event-time bar can have several causal versions

- **Severity:** MEDIUM
- **Subsystem:** Late data; rolling windows; feature leakage.
- **Failure mechanism:** The architecture allows a late event-time record to revise a closed bar for later outputs, while lookbacks are described in available-time terms. It does not specify whether rolling statistics count the old bar at its original event-time slot, at revision arrival, or both, or how warm-up/gap status changes. A research export containing only each bar's final revision would expose late information to earlier simulated decisions even if online engine outputs were immutable.
- **Realistic scenario:** A 12:00:59 update arrives at 12:02:10 after the 12:01 bar closed. A historical feature table rebuilt from final bars includes the revision in a 12:01 signal row.
- **Likely consequence:** Look-ahead bias and live/research feature mismatch.
- **Existing safeguards:** Prospective-only admission, immutable past outputs, tick-based bar close, and proposed revision records. Those rules do not define as-of retrieval or window membership for revised bars.
- **Smallest robust correction:** Give each derived bar/feature an availability ordinal and immutable revision ID. Define window membership using available-time cutoffs for prediction; any event-time view must be queried as-of its contemporaneous ordinal and never from a final-value table.
- **Test:** Inject a late price extreme after a bar closes; assert all earlier feature hashes remain fixed and a historical as-of query returns the pre-revision value.

### F12 — Historical outcome labels can select a better quote than the live system could use

- **Severity:** HIGH
- **Subsystem:** Cost model; labels; historical simulation (V1+).
- **Failure mechanism:** V1 proposes fixed-horizon quote labels and a quote-aware hypothetical cost model but does not define entry/exit side, first eligible observation, missing-quote censoring, depth consumption at a reference notional, round-trip fees, or how processing and publication latency shift the horizon. Midpoint-to-midpoint gross return minus a generic spread/slippage estimate can use an optimistic endpoint or count spread inconsistently.
- **Realistic scenario:** A signal is emitted just before a fast upward quote change. A label uses the old midpoint as entry and the next favorable midpoint as exit; an actual hypothetical taker buy would use the later ask, and a round trip would also pay the exit bid and both fees.
- **Likely consequence:** False profitability and misleading comparisons among signal families.
- **Existing safeguards:** The specification insists on realistic spread, fees, slippage, latency, and no fill claims. These are principles, not an exact causal label and cost accounting rule.
- **Smallest robust correction:** Predeclare a reference notional and action-specific executable entry/exit convention, decision-to-entry latency, horizon start, fee basis, depth/slippage treatment, funding/borrow if relevant, and a rule for absent or unhealthy endpoint quotes. Report midpoint forecast skill separately from hypothetical net opportunity.
- **Test:** Hand-calculate a tape with moving bid/ask, thin depth, delayed output, and a missing exit quote; evaluator results must match the predeclared convention and never choose the favorable earlier quote.

### F13 — Censoring on data health can make a strategy appear selective and profitable

- **Severity:** HIGH
- **Subsystem:** Statistical validation; data quality (V1+).
- **Failure mechanism:** Unhealthy periods correctly produce unavailable analysis, but the evaluation protocol does not require reporting what fraction of calendar time, opportunities, and realized volatility was excluded, nor require paired comparison on the same eligible timestamps. If gaps cluster in fast markets, removing them can selectively discard losing or costly observations. A later model's different freshness threshold also changes its sample.
- **Realistic scenario:** The collector falls behind during large BTC moves. The remaining clean hours are quiet and favorable to mean reversion. Comparing only completed trades to an always-long benchmark over the full period makes the model seem superior.
- **Likely consequence:** Selection bias, misleading backtests, and an unsupported claim that abstention is predictive skill.
- **Existing safeguards:** Explicit gaps, `NO_TRADE`, chronological tests, matched action-frequency random baseline, and regime breakdown. The common evaluation population and coverage accounting are not specified.
- **Smallest robust correction:** Freeze an eligibility mask based on data health before model comparison; report calendar coverage, excluded-period returns/volatility, reason counts, and paired baselines on the same feasible timestamps. Treat outage-driven abstention separately from model-driven abstention.
- **Test:** Inject outages only around adverse price moves; the evaluation report must show the changed coverage and must not silently improve net-return claims by dropping those periods.

### F14 — Reusing the final test through milestone iteration defeats the frozen holdout

- **Severity:** HIGH
- **Subsystem:** Multiple testing; overfitting; reproducibility (V1–V3).
- **Failure mechanism:** `SPEC.md` asks for an untouched final test and `PLAN.md` says it is opened once after choices are frozen, but V1 and V2 gates also require out-of-sample and walk-forward comparisons. The documents do not distinguish reusable development validation from the one final holdout, record all tried hypotheses/thresholds, or say what happens after an unfavorable final result. Repeated variants, regime cuts, opportunity thresholds, and cost assumptions can be selected on the same period without overtly “training” a model on it.
- **Realistic scenario:** Several quote-momentum horizons fail on the final period. The team changes the regime veto and cost assumptions, reuses that period, and reports the one surviving version as out-of-sample.
- **Likely consequence:** Invalid p-values and uncertainty bands; false confidence in an edge.
- **Existing safeguards:** Frozen evaluation periods, hypothesis-count record in V3, sensitivity checks, and no retuning the final test. The release/experiment ledger and milestone-specific holdout rules are unspecified.
- **Smallest robust correction:** Register each experiment and selection criterion before evaluation; distinguish development walk-forward folds from a sequestered final period. Open the final period once for a frozen candidate and treat any redesign after seeing it as needing new future data. Report the full search count and dependence-aware uncertainty.
- **Test:** Audit a simulated sequence of failed and revised candidates; the process must label later reuse of an exposed period as development evidence, not fresh out-of-sample evidence.

### F15 — Probability calibration and model disagreement can inherit shared-label leakage

- **Severity:** MEDIUM
- **Subsystem:** Calibration; regime and ensemble inference (V2–V3).
- **Failure mechanism:** The design calls for dependence-aware aggregation and calibration but gives no minimum sample/coverage rule, separation of calibrator training from model fitting, or purge around overlapping horizon labels. Regime thresholds fitted on all data or a calibrator trained on the same predictions used to select families can make `p_net_positive` look calibrated. Disagreement between families trained on the same target is not independent evidence.
- **Realistic scenario:** Two signal families use highly overlapping five-minute quote-return labels. Their outputs agree in-sample, and a calibrator fitted on those same selected examples reports 70% success for a high-score bucket that has few independent episodes.
- **Likely consequence:** Overconfident probabilities, excessive opportunity filtering, and invalid claims of independent confirmation.
- **Existing safeguards:** Probability omission before validation, joint-calibration language, family ablations, and V3 purging/embargo. The concrete split and effective-sample requirements are deferred.
- **Smallest robust correction:** Before enabling probability fields, define chronological out-of-fold prediction generation, label-horizon purge/embargo, effective sample and bucket uncertainty, and a calibration acceptance metric. Keep regime estimation and family selection inside each training fold.
- **Test:** Use synthetic correlated families with overlapping labels and a regime threshold fitted on future data; the pipeline must reject in-sample calibration and show degraded out-of-fold reliability.

### F16 — Cross-asset synchronization remains an aspiration rather than a join contract

- **Severity:** MEDIUM
- **Subsystem:** V2 cross-market data; ownership. Deferred until a second feed is admitted.
- **Failure mechanism:** The design says joins use `UsableFromTime`, freshness, and cutoffs and mentions a future coordinator, but does not define a global ordering key across separate recorder runs, how a late second-feed event affects a pending first-feed decision, or whether the coordinator waits for a watermark. A replay that loads both complete streams could choose the latest value by timestamp even though the second feed had not yet arrived when the first-feed decision was emitted.
- **Realistic scenario:** BTC event is actionable at 12:00:00.100; ETH event has source time 12:00:00.050 but reaches its collector at 12:00:00.400. A timestamp join includes the ETH move in a BTC signal at 12:00:00.100.
- **Likely consequence:** Cross-market look-ahead and live/replay divergence.
- **Existing safeguards:** The spec prohibits later-value interpolation and requires available-time joins. No executable total-order/watermark policy exists yet, appropriately because multi-feed work is deferred.
- **Smallest robust correction:** Before V2, define one global admitted-order stream or a deterministic coordinator with per-feed arrival ordinals, decision cutoffs, bounded wait, and missing/stale outcomes. Never revise an emitted decision.
- **Test:** Delay one feed while preserving earlier exchange timestamps; fast and paced replay must match live decisions and exclude the later-arriving observation.

### F17 — Operational and research reproducibility are conflated

- **Severity:** MEDIUM
- **Subsystem:** Observability; run manifests; replay claims.
- **Failure mechanism:** The V0 acceptance test demands byte-identical normalized/health/book hashes for live and replay, but the design also permits unclean termination, backlog, and replay recovery of committed raw data. A raw tape can reproduce the *analysis implied by the tape* while the live process failed to process or publish its final committed records. A state hash comparison cannot prove user-visible output parity or timely health transitions. Conversely, a valid recovered tape may fail a naive live/replay hash test for an explainable crash boundary.
- **Realistic scenario:** Raw records are committed, then the process crashes before the state goroutine applies them. Replay produces a final book hash and later V1 intents absent from the live journal.
- **Likely consequence:** Overstated parity assurance and incomplete incident accounting.
- **Existing safeguards:** Separate sink, immutable outputs, `SHUTDOWN`, incomplete manifest, and nonzero exit on fatal errors. No applied/output high-water mark or journal reconciliation contract is specified.
- **Smallest robust correction:** Record separate committed, applied, and published ordinal high-water marks. Compare the common applied prefix for live/replay parity and separately report unprocessed committed suffixes. Give future intents idempotent IDs and an append-only publication acknowledgment policy.
- **Test:** Crash after raw commit but before state apply and again after state apply but before output write; recovery report must explain both boundaries without claiming a full live/replay match.

## Blockers before implementation

Resolve **F02–F08 and F17** in the V0 contract before writing the collector: clock recovery, capture/control ordering, lag fail-closed behavior, durable-prefix recovery, ordered metadata admission, subscription/product validation, epoch barriers, and the exact scope of live/replay parity. F01 is also a blocker before any V1 return or cost evaluation; V0 should already record enough timing to make its correction possible. F09's snapshot validation should be fixed in the V0 parser contract before accepting `HEALTHY` book state. F10 needs a decision-level invariant before directional intents exist.

This is a request to amend the design before implementation, not a claim that the existing V0 implementation is defective: none exists.

## Improvements that can wait

F11–F15 should be resolved when their analytical milestones are designed, before a backtest or probability is reported. F16 is a V2 admission gate; no multi-feed coordinator is needed in V0. An independent book check or redundant capture would help bound silent provider-side omissions; V0 should state that a 24-hour soak and a clean replay cannot prove exchange-side completeness. One instrument and no trades limits V0 to a capture experiment; it cannot validate a directional strategy or a cross-regime cost model. The custom binary format, canonical JSONL, recovery protocol, and 24-hour soak make V0 operationally broad; keep only the pieces needed to establish an auditable raw prefix and one faithful book, and avoid V1 package scaffolding.

## Proposed changes to V0 acceptance criteria

1. Add a **capture-order fixture** that races read-completed frames, ticks, disconnects, and reconnects and asserts the specified linearization order and epoch-scoped effects at each ordinal.
2. Add a **clock-epoch fixture** with a large forward jump and correction. Health cannot recover while logical time, tick time, and wall/monotonic anchors disagree; test heartbeat and quote ages across the anomaly.
3. Add a **backlog fixture** that stalls raw persistence and state processing longer than heartbeat expiry. Live output must become unavailable by a bounded monotonic deadline; the incident must be represented in replayable records.
4. Add **crash-point fixtures** around data write, fsync, commit acknowledgment, state apply, and output write. Require a recovery report distinguishing valid bytes, committed prefix, applied prefix, and published prefix.
5. Add **metadata availability and identity fixtures**: a delayed product response cannot affect earlier outputs; acknowledgment must contain the configured channel/product pairs; every market frame must match product and epoch.
6. Expand **book fixtures** to reject duplicate canonical snapshot prices, zero-size snapshot levels, malformed multi-change messages, and over-limit numeric values atomically.
7. Make the **soak report** include high-water marks, time spent unhealthy/degraded, maximum queue and tick lag, prolonged quiet-book intervals, UTC clock offset/adjustments where measured, and the precise prefix compared with replay. A clean 24-hour run demonstrates engineering behavior for that run, not data completeness or strategy value.

## Unresolved architectural questions

- What is the authoritative capture linearization point across socket reads, ticks, connection statuses, and metadata responses? Can `ReceiveTime` precede a lower-ordinal tick, and how should such a record affect as-of state?
- Is `UsableFromTime` meant to describe read availability, durable admission, or engine availability? Which timestamp will a later historical evaluator use as the earliest possible decision and entry time?
- How is a forward clock correction represented without pinning logical time in the future? What exact condition clears clock degradation?
- What durable marker proves the last committed record after a crash, and how are committed, applied, and published prefixes reconciled?
- Does a stale or unknown `subscriptions` response, wrong-product frame, or future unknown message type invalidate the epoch, or can any of them be ignored while retaining `HEALTHY`?
- What is the maximum permitted tick/processing lag before live analytical output is withdrawn? Who records this failure if the recorder itself is blocked?
- At what point is product metadata admitted to state, and is later product-status change tracked without a retrospective manifest lookup?
- For V1, what is the exact executable-quote convention, round-trip fee/slippage model, processing-latency bound, outcome censoring rule, and common eligibility population for comparing baselines?
- Which periods are reusable development validation, which period is the one sealed final test, and what new data are required after a final-test-driven redesign?
- Before admitting a second feed, what global arrival order and watermark/cutoff policy will keep a replay from making a better synchronized join than live could make?

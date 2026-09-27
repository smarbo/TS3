# Project status

Updated: 2026-09-27

## Current phase

V0 implementation and local failure validation are in place. **V0 is not complete and V1 is not safe to begin** until the mandatory 24-hour soak finishes and its replay/incident audit passes. The public source has been changed from Coinbase Exchange to Kraken Spot WebSocket v2 because Coinbase now requires authentication for Level 2. The ordinary tests, race detector in Ubuntu WSL, vet, short public live checks, and synthetic failure fixtures pass. No V1–V4 functionality or trading/account code has been added.

The active development and acceptance branch is `v0`. The prior V0 implementation commit already exists on `main`; that history is preserved, and all further V0 fixes and acceptance documentation belong on `v0`. `main` remains frozen until the V0 acceptance gate passes. The official server soak must use one exact clean `v0` commit whose full hash appears in both the incremental run report and final manifest.

## Implemented

- Typed raw and canonical events, exact decimal normalization, canonical JSON, receipt/admission/usable timestamp separation, ordered ticks, clock-break detection, and epoch barriers.
- Append-only CRC32C-framed raw segments, two-fsync SHA-256 commit markers, manifest segment verification/rebuild, committed-prefix replay, normalized/state JSONL, applied/published progress journal, `-through` prefix comparison, and automatic clean-run live parity verification.
- Public Kraken `XBTUSD` pair metadata GET, public `BTC/USD` depth-100 WebSocket book, automatic heartbeat, exact acknowledgment checks, source-decimal-preserving CRC32 checksum verification, depth truncation, reconnection, and single-owner book/health state.
- Bounded ingress queue, commit batching, atomic operational availability gate and watchdog, fast/paced/stepped replay, bounded-cardinality lag/fsync metrics, a run manifest with source/config/code/Go provenance, `cmd/v0report` for reproducible soak evidence, and a V0 runbook. An atomic `run-report.json` now persists metadata before capture and refreshes every five seconds with health, counts, incident totals, progress marks, and evidence file sizes. On startup, a prior report left `RUNNING` becomes `INCOMPLETE`; the final audit requires `run_report_complete`. After an incomplete prior run, startup waits for ten stable clock observations and admits an ordered `CAPTURE_GAP` before metadata.
- Legacy Coinbase normalization remains solely to replay the recorded failed public probe; no Coinbase live adapter or authentication is active.

## Validation evidence

- `go test -count=1 ./...`, `go vet ./...`, `gofmt -l cmd internal`, and `git diff --check` passed after the latest blocked-recorder and per-ordinal book fixtures. Tests cover overtaken ticks, small wall corrections, clock breaks with source provenance, committed/uncommitted/torn event and commit-marker suffixes, manifest tampering and partial/missing-manifest rebuild, refusal to rebuild a recently written segment, bounded ingress handoff and acknowledgment, Linux `/dev/full` write failure without an advanced commit mark, injected data/marker fsync failures, a blocked data fsync that trips the independent gate in about two seconds, derived-output write failure that freezes the acknowledged prefix, idle-WebSocket cancellation, exact outbound subscription and ordered metadata, acknowledgment/snapshot timeout closure, terminal subscription rejection, malformed-frame invalidation and intra-run reconnect with a fresh checksummed snapshot, subscription identity variants, duplicate acknowledgment, old-epoch isolation, the published checksum example, same-price changes and zero deletion, checksum invalidation, depth truncation, quiet-book degradation and stale-activity invalidation, golden health/reason/generation/BBO/hash at every ordinal, the actual 250 ms watchdog loop's response to a stalled owner, anti-future market/metadata output, and fast/paced replay equivalence.
- `wsl.exe -d Ubuntu -- bash -lc 'cd /mnt/c/Users/eddie/Nextcloud/TS3 && CGO_ENABLED=1 go test -race -count=1 ./...'` passed across every Go package after the latest edits on 2026-09-27 using Ubuntu GCC 13.3 and Go 1.27.0. Earlier Windows attempts failed because Windows has no `gcc`; the WSL command succeeded after WSL access was granted.
- Read-only Coinbase probe: public metadata succeeded, but the server rejected Level 2 with `level2, level3, and full channels now require authentication`. Only heartbeat was acknowledged. [Coinbase's current guide](https://docs.cdp.coinbase.com/exchange/websocket-feed/authentication) confirms the requirement. No credential path was added.
- Read-only Kraken 20-second live smoke: `go run ./cmd/collector -duration 20s -dir data -run kraken-smoke-20260927a` reached `HEALTHY` in epoch 1 and committed/applied/published all 1,839 ordinals. There were 1,830 `HEALTHY` state rows; shutdown closed the book. The raw segment was about 1.19 MB. `go run ./cmd/replay -dir data -run kraken-smoke-20260927a` matched normalized SHA-256 `05628f42da79bb590e140f3d767114c82edc7d42b87a806116d2a716bf053bb6` and state SHA-256 `ae32926f7cd21608d31785ecaca98450019a2fd990d55116b7cc1c852eaedf8f` exactly, with no uncommitted suffix.
- Fast, paced, and stepped replay of that same 1,839-record Kraken tape produced identical normalized and state hashes. The stepped CLI finished with exit code 0 when supplied one newline per record.
- Read-only 60-second Kraken pre-soak: 2,366 committed/applied/published records, clean manifest, and automatic `live_parity.equal=true`. The metrics file reported admission p99 28.4 ms, commit p99 100.2 ms, apply p99 122.1 ms, data-fsync p99 3.8 ms, and maximum observed queue depth 48. These are one-minute engineering measurements, not a 24-hour result.
- Replayed the 60-second pre-soak again after the latest recovery and timestamp fixes: all 2,366 records retained exact live parity, with normalized SHA-256 `6ac3ae5654311fba0d1847f2a03a955f8b496c306b910027f681b82162d96d35` and state SHA-256 `55af685db48a3bbca27b1a0ae6ce3a9e2915310c3e608c627161a9d2882fca4f`.
- An intentionally stopped preliminary background run left no manifest. The next read-only 20-second capture waited ten stable clock observations, admitted `CAPTURE_GAP` at ordinal 1, obtained fresh metadata/snapshot, reached `HEALTHY`, and replayed all 1,204 records identically. That fixture exposed a control-event venue label bug; the final collector now prefixes clock/collector/watchdog source IDs with `kraken`, with a regression test.
- Isolated read-only Ubuntu WSL captures were interrupted by actual `SIGINT` and `SIGTERM` after reaching `HEALTHY`. Both closed cleanly with no uncommitted suffix and exact cross-platform Windows replay parity: SIGINT committed/applied/published 475 ordinals (normalized SHA-256 `3bdd4700448340b0b57163ed65a6157671bc7af168aa3ebfb75353dd03a80791`, state SHA-256 `0d01cb0ac821ab266b94bf1d5cd198b094363255ee3155b46bc3053a9231e5cc`); SIGTERM committed/applied/published 1,859 ordinals (normalized SHA-256 `f2c1c962a0567ab6308f28f64f48fd9184f22a8eafda610bcd181138660f20d2`, state SHA-256 `9ba56a270c472622020e35c8291dfe9e672d6b0493679875b90cf0a1afe424d9`).
- After tightening the operational gate to open only after durable applied/published progress, a separate read-only 20-second Kraken smoke reached `HEALTHY` and replayed with exact parity across 428 committed/applied/published ordinals (`kraken-gatefix-20260927b`). An initial sandboxed metadata request was network-denied and left an explicit incomplete run; the succeeding isolated run correctly began with an ordered `CAPTURE_GAP`.
- `cmd/v0report` was exercised on the 60-second pre-soak and isolated SIGTERM capture. The SIGTERM audit reported exact parity and equal manifest/raw/applied/published/state high-water marks, full lag sample coverage, no checksum/clock incidents, and the expected `at_least_24h_healthy_span=false`. Synthetic tests cover quiet-book intervals, checksum incidents, and two-file parity mismatch. This report command is ready for the final 24-hour run once it ends.
- The incremental soak-report change passed `go test -count=1 ./...`, `go vet ./...`, and `CGO_ENABLED=1 go test -race -count=1 ./...` in Ubuntu WSL. Focused fixtures verify atomic replacement, interrupted-run marking without raw-file loss, and repeated live metrics snapshots without double-counting open health duration. A new 24-hour live run has not yet exercised these changes.
- An eight-second read-only Kraken smoke with the incremental reporter (`data/report-smoke/kraken-report-smoke-20260927`) closed cleanly at 189 committed/applied/published ordinals. Its final run report has code/config/Go provenance, file sizes, last confirmed healthy time, counters and `COMPLETE`; full replay matched both normalized/state hashes. `cmd/v0report` returned `run_report_complete=true`, full parity and equal high-water marks, with the expected `at_least_24h_healthy_span=false`. This is an integration check, not the mandatory soak.
- Two local read-only soak attempts were stopped. `kraken-soak-final-20260927` used an executable that predated later V0 gate/failure fixes. The final-code run `data/v0final/kraken-v0-final-20260927` started 2026-09-27 20:52:51 Europe/London, reached `HEALTHY` at ordinal 8 in epoch 1, and was stopped at the user's request around 20:59 Europe/London so the 24-hour soak can run on an always-on server. PID 23428 has exited and no collector-named process is running. Its raw and derived files remain under ignored `data/`; it has no final manifest and is explicitly incomplete. The earlier preliminary run's raw bytes also remain intact. Neither run satisfies the 24-hour acceptance gate.
- Kraken source facts were checked against its [public WebSocket guide](https://docs.kraken.com/exchange/guides/websockets/introduction), [book protocol](https://docs.kraken.com/exchange/api-reference/spot-websocket-v2/book), [checksum guide](https://docs.kraken.com/exchange/guides/websockets/book-checksum-v2), [heartbeat description](https://docs.kraken.com/exchange/api-reference/spot-websocket-v2/heartbeat), and [AssetPairs endpoint](https://docs.kraken.com/api-reference/market-data/get-tradable-asset-pairs). No strategy or market edge has been measured.

## Open V0 work

- Run and archive a **new 24-hour-plus Kraken capture/replay soak on the always-on server** with a unique run ID. During capture, inspect the five-second `run-report.json` and durable progress journal. After it ends, use `cmd/replay` and `cmd/v0report` to inspect rates, lag distributions, queue maxima, health/quiet intervals, reconnects/gaps/checksum failures, clock anomalies, high-water marks, and exact full-run parity. The stopped local runs, one-minute pre-soak, and synthetic fixtures cannot satisfy this elapsed-time gate.

## Next implementation slice

On the server, run the V0 verification commands and start a new 24h5m public Kraken capture with a unique ID, as specified in [V0_RUNBOOK](V0_RUNBOOK.md). Let it finish cleanly, then run `cmd/replay` and `cmd/v0report` for that run. Inspect and archive the report's checks, incidents, health durations, and exact parity with the manifest and progress journal. **V0 is ready for this acceptance run, but is not yet complete.** Declare V0 complete only if the 24-hour gate and incident audit pass. Do not start V1–V4.

## Architecture review record

The independent review's findings were reconciled in the previous design pass. Their dispositions remain:

- F01 ACCEPT: receipt precedes commit/publication; preserve all boundaries.
- F02 ACCEPT WITH MODIFICATION: large clock jumps terminate the run instead of pinning time.
- F03 ACCEPT WITH MODIFICATION: admission ordinal is the linearization point; receipt remains provenance.
- F04 ACCEPT WITH MODIFICATION: a monotonic live gate closes on stalled progress independently of replay health.
- F05 ACCEPT WITH MODIFICATION: fsynced commit markers, not valid bytes alone, define replay input.
- F06 ACCEPT: metadata enters the ordered stream; the final manifest is not a market side input.
- F07 ACCEPT: verify exact subscription and product identity.
- F08 ACCEPT: epoch barriers isolate old connection events.
- F09 ACCEPT: validate whole snapshots atomically with canonical price uniqueness and bounds.
- F10 ACCEPT: heartbeat activity does not refresh a cost quote.
- F11 ACCEPT WITH MODIFICATION: late bar revisions need as-of ordinals when bars begin in V1.
- F12 ACCEPT: net-return claims need feasible side-specific entry/exit quotes.
- F13 ACCEPT: freeze/report health eligibility and compare on paired timestamps.
- F14 ACCEPT: an opened final test period cannot be reused as a fresh holdout after redesign.
- F15 ACCEPT WITH MODIFICATION: predeclare numeric calibration thresholds when V3 data exist.
- F16 ACCEPT WITH MODIFICATION: add a global join order before a second feed, outside V0.
- F17 ACCEPT: committed, applied, and published high-water marks are distinct.

The Coinbase source assumption was superseded by direct protocol evidence during this implementation pass. Current contracts are in [SPEC](SPEC.md), [ARCHITECTURE](ARCHITECTURE.md), [RESEARCH](RESEARCH.md), and [PLAN](PLAN.md).

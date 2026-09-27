# Project status

Updated: 2026-09-27

## Current phase

Independent architecture review reconciled. **V0 is ready for implementation against the revised contracts**, subject to its tests and 24-hour soak before V0 can be marked complete. No implementation, market capture, or empirical strategy result exists yet.

## Completed work

- Defined scope, time/availability invariant, single live/replay path, health and decision semantics in [SPEC](SPEC.md).
- Chosen the first public feed and instrument, specified V0 event/storage/replay/book contracts and Go ownership model in [ARCHITECTURE](ARCHITECTURE.md).
- Registered source/feature/signal hypotheses and failure tests without claiming evidence in [RESEARCH](RESEARCH.md).
- Defined V0–V4 scopes and evidence gates, including exact V0 fixtures and a 24-hour capture/replay soak, in [PLAN](PLAN.md).
- Verified Coinbase Exchange public WebSocket, Level 2, heartbeat, product metadata, and best-practices claims against current primary documentation linked in the architecture and research register.
- Independently assessed all 17 findings in [ARCHITECTURE_REVIEW](ARCHITECTURE_REVIEW.md), including every CRITICAL and HIGH item; amended the specification, architecture, research protocol, and V0 acceptance tests.

## Major decisions

- `BTC-USD` spot on Coinbase Exchange, `level2` plus `heartbeat`, unauthenticated, is the only V0 market feed.
- V0 omits trade matches and REST book bootstrap. A current-epoch Level 2 snapshot starts each book generation.
- A single ingress coordinator assigns capture ordinals and epoch barriers. Receipt, admission, commit, application, and publication are distinct events/times.
- The replay source is the verified **committed** raw prefix; normalized JSONL is a regenerable derivative. Live/replay parity is asserted on the common acknowledged applied prefix, with committed/unapplied and unpublished suffixes reported separately.
- A large wall-clock jump ends the run. Live backlog closes an independent monotonic availability gate; a later replay reproduces committed analytical transitions without pretending to prove prompt wall-time suppression.
- Product metadata is ordered input admitted before the WebSocket opens. Exact subscription/product identity and atomic snapshot validation are prerequisites for a healthy book.
- Bad required data invalidates dependent analysis; `NO_TRADE` is the intended later safe analytical result.
- No production trading functionality is present or authorized by this specification.

## Review dispositions

- **F01 ACCEPT:** receipt is earlier than commitment/publication; V0 now records those boundaries, and later evaluation must use feasible entry time.
- **F02 ACCEPT WITH MODIFICATION:** forward pinning is real; terminate the run on a large jump rather than attempting same-run clock repair.
- **F03 ACCEPT WITH MODIFICATION:** scheduling can reorder observations; ingress admission is the documented linearization point, while earlier receipt remains provenance only.
- **F04 ACCEPT WITH MODIFICATION:** delayed ticks can leave cached health stale; a monotonic operational gate closes independently, while replay parity is limited to committed state.
- **F05 ACCEPT WITH MODIFICATION:** valid bytes do not prove commit; fsynced prefix commit frames define replay input, with progress marks for later stages.
- **F06 ACCEPT:** manifest metadata is a future side input unless the response enters the ordered event stream.
- **F07 ACCEPT:** subscription membership and every market frame's product ID must be checked, not inferred from the outbound request.
- **F08 ACCEPT:** epoch-scoped barriers and status applicability are necessary to prevent old connection events invalidating a new book.
- **F09 ACCEPT:** duplicate canonical prices and zero-size snapshot levels must be rejected atomically with explicit bounds.
- **F10 ACCEPT:** heartbeat health alone is insufficient for a cost quote; later directional intents require a separate quote-age/generation gate.
- **F11 ACCEPT WITH MODIFICATION:** late bar revisions need as-of ordinals; no bars are implemented in V0, so this is a V1 entry condition.
- **F12 ACCEPT:** a feasible side-specific entry/exit and missing-quote rule must precede any net-return claim.
- **F13 ACCEPT:** health exclusions can select the sample; freeze/report coverage and compare on paired eligible times.
- **F14 ACCEPT:** development folds can be reused with disclosure, but an exposed final period cannot be a fresh holdout after redesign.
- **F15 ACCEPT WITH MODIFICATION:** calibration leakage is valid; numeric sample/metric thresholds must be predeclared when V3 data exist, not guessed in V0.
- **F16 ACCEPT WITH MODIFICATION:** a global join order is required before a second feed, but implementing its coordinator in single-feed V0 would add unused scope.
- **F17 ACCEPT:** raw commit, state apply, and publication are different high-water marks; a replay of a crash suffix is not proof of live output parity.

## Unresolved questions

- Measure actual Level 2 throughput, heartbeat behavior, clock stability, two-fsync batch cost, journal cost, and watchdog responsiveness in the V0 soak. If the bounded pipeline cannot keep up, revise V0 explicitly before accepting it.
- Confirm V1 hypothetical fee, slippage, and latency assumptions before any return claims; no account-specific fee tier can be presumed.
- Evaluate whether quiet-book age is a useful warning threshold and whether signed trades add incremental value after building a gap-safe capture protocol.
- Set V4 forward duration and minimum sample size before opening its evaluation window.

## Next step

**First implementation slice:** create the Go module and `internal/domain`, `internal/ingress`, and `internal/record` for a synthetic, single-producer tape. Implement the raw event/commit frame codec, ordinal/admission-time assignment, commit-marker recovery, and a replay reader that exposes only committed records. Add crash-point and overtaken-tick fixtures **before** connecting to Coinbase or building the book. Then add ordered public metadata, socket/epoch validation, atomic book reconstruction, progress journal, watchdog, and finally the short live capture and 24-hour soak. Do not start V1–V4 work.

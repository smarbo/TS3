# V1 final acceptance audit

Status: IN PROGRESS. The two full accepted-tape replays must finish before a verdict. This document records the adversarial checks and evidence, not a profitability claim.

## Frozen input and code

- Source: accepted V0 run `kraken-v0-server-20260927T203224Z`, clean source revision `d26241151331795cf5005ba09d5f176d1efb6b22`, manifest SHA-256 `8144ec61b4da66aa0e392df5e96b140a25dbaef7bbc6abdd015ca92ebf5bd7dc`.
- V1 research binary: clean revision `eccdcb71be62b6a296210c3be32539e9f256c1ac`, binary SHA-256 `c27b839e00b0237be50fa8e29abb53feebd868c5a0157f8c1239e40b7f992d9e`, Go 1.27.0. Two independent full replays use that exact binary and unique output directories in WSL.
- Frozen policy, costs, horizon, controls, and population are in [V1_DESIGN](V1_DESIGN.md) and [V1_EXPERIMENT_LEDGER](V1_EXPERIMENT_LEDGER.md). The accepted tape is development evidence, not a sealed final test.

## Adversarial checks

1. **Information availability:** `Engine.ApplyTick` receives only the V0 state and detached quote after the current recorded tick has been applied. It has no source of future records or outcome feedback. Its 60/300/900-second boundaries use tick available time, and equal-time ticks do not advance a window. The future-poison, irregular-boundary, warm-up, gap, stale-quote, and generation tests pass.
2. **Live/replay parity:** a clean 21-minute V1 live smoke from revision `2884fc49e7ad9baccfabbecf29fd25e755703bf8` closed with 99,792 committed/applied/published ordinals. Its canonical intent SHA-256 `978a0426ae320ae2a34b68e620fdc83da56975291acb5c79b15539ee926864d4` exactly matches raw replay by the research binary. The V0 normalized/state hashes also match. The engine, cost, journal, and collector files have no diff between those two commits. The live operational acknowledgments are separate from the replayed intent bytes.
3. **Operational timing:** all 22 live intent IDs, ordinals, and actions match their acknowledgments. Measured decision-ready lag was 4–107 ms and intent-durable lag 11–115 ms against intent as-of time, below the research-only two-second entry-delay scenario for this smoke. One closed-gate acknowledgment was the orderly shutdown `NO_TRADE`; there was no directional gate veto. This does not prove future latency or fills.
4. **Executable-side accounting:** the research evaluator is a separate consumer of immutable intents; it starts entry no earlier than the decision plus two seconds and requires the book update itself to be available after the boundary. Long enters through displayed asks and exits through bids; hypothetical short reverses those sides and includes a disclosed borrow scenario. Fees and allowance are each applied once per side. Missing depth, unhealthy path, stale quote, tick gap, generation change, or absent endpoint censors the episode. Hand-calculated moving-BBO, thin-depth, pre-boundary held-quote, and censoring tests pass.
5. **Leakage and statistical limits:** one-minute decisions with five-minute outcomes overlap. The health eligibility mask is fixed before controls; the same paired endpoint population is used for policy, momentum, mean reversion, always-long, random direction at policy action frequency, and no-trade. Four chronological six-hour development folds and deterministic 30-minute-block bootstrap intervals are descriptive. No threshold was fitted to the official tape; no probability or profitability claim is emitted. Excluded-period market returns/volatility are unknown without an independent feed.
6. **Scope and ownership:** V1 adds no account, private API, order placement, wallet, actual position, ML, multi-feed, or V2/V3 feature. The V0 book state remains single-owner; `Quote()` returns copied sorted depth and does not change accepted V0 state serialization. The V1 engine and journal run on the collector's serialized owner path; the live watchdog remains an independent publication veto.

## Pending full-tape verification

- Both replays must finish all 12,316,574 committed ordinals with `full_committed_prefix=true`, the accepted V0 hashes, identical V1 intent and report hashes, and no unexplained divergence.
- Inspect complete decision, eligibility, reason, censoring, action-frequency, comparator, cost-sensitivity, regime, fold, and uncertainty results. Treat low coverage or invalid endpoints as limitations rather than silently dropping them.
- Rerun/record the required test, race, vet, formatting, and whitespace commands after any code change. Record the final milestone verdict in [STATUS](STATUS.md). No V1 merge, completion tag, or shutdown is allowed while this audit remains pending.

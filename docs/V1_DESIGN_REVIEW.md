# Adversarial review of frozen V1 design

Date: 2026-09-29. Reviewed before implementation or V1 outcome inspection.

1. **ACCEPT — Future-complete bars and late revisions.** The smallest V1 uses only recorded tick samples in arrival order, so it needs no bars or mutable final-value table. Every output is stamped at the available ordinal and past windows are never rewritten. Tests must poison later quotes and confirm earlier bytes are unchanged.
2. **ACCEPT — Healthy heartbeat with stale quote.** V0 health can remain `HEALTHY` while book traffic is quiet. The V1 two-second book-age gate and generation reset must override that for costs and intents. Test sustained heartbeats without book updates.
3. **ACCEPT — Midpoint or optimistic endpoint as fill.** The evaluator uses side-specific depth VWAP at the first eligible quote after the delay/exit boundary, fees on both notionals and allowance once per side. It censors missing depth or endpoint. Hand-calculated moving/thin-book tests are mandatory.
4. **ACCEPT — Short infeasibility on spot.** A bearish forecast does not establish borrow access. Default `SHORT` candidate becomes `NO_TRADE`; hypothetical short comparator has a disclosed borrow scenario and is never called executable.
5. **ACCEPT — Arbitrary heuristic thresholds and sample dependence.** The fixed 10-bp thresholds and 300-second horizon are predeclared, not fitted to the accepted tape. They may be useful baselines but cannot establish edge. The report must disclose variants, paired coverage and overlapping-label dependence; one tape is development evidence only.
6. **ACCEPT — Live/replay operational parity confusion.** Canonical V1 features/intents should match on a common applied prefix, but live publication delays and watchdog closures are separate measured facts. Do not infer a live-safe publication time from replay speed.
7. **ACCEPT — Numeric and ordering ambiguity.** Fixed rounding, exact threshold equality, generation resets, and ordinal tie-breaks must be tested. No map iteration may determine an output order.
8. **MODIFICATION — Cost reference without depth.** `book.View` exposes BBO but not level sizes. Add a read-only quote snapshot accessor to the existing book owner and use the shared V0 processor in both live and replay. Do not alter V0 state JSON, accepted hashes, or reconstruct a second independent book.
9. **ACCEPT — Sample count mislabelled as elapsed-time lookback.** A tick can arrive late or two ticks can share a usable time. Fixed 60/300/900 sample counts do not imply 60/300/900 seconds. Use available-time boundaries, skip equal-time duplicates, reset after more than two seconds, and test irregular 1.5-second spacing. This was corrected before full-tape outcomes were inspected.

No second feed, probability calibrator, model selection, training pipeline, or V2/V3 ensemble is justified in V1.

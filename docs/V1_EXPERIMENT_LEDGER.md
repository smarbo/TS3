# V1 experiment ledger

Created before full-tape V1 outcome inspection on 2026-09-29. Every listed number is a **predeclared research assumption**, not a fitted parameter or empirical finding.

| ID | Input and population | Frozen policy and comparators | Cost and label convention | Use of result |
| --- | --- | --- | --- | --- |
| V1-001 | Accepted public Kraken run `kraken-v0-server-20260927T203224Z`; eligible one-second ticks with healthy current-generation book and at most two-second quote age; one decision at first tick of each UTC minute after 900 contiguous available-time seconds | Five-minute horizon; momentum 60-second return threshold ±10 bps; 300-second mean-reversion displacement threshold ±10 bps as comparator; descriptive spread/RMS regimes; default short infeasible; random matched, always-long and NO_TRADE controls | USD 100 decision-mid reference, displayed-depth taker sweep, 20-bp fee and 5-bp adverse allowance per side, 10-bp opportunity margin; two-second research entry delay, first healthy endpoint within two seconds, five-minute hold, path-health censor; 10-bp/day hypothetical short borrow in research comparator | Engineering/exploratory development evidence only. Report all actions, abstentions, censoring, paired controls, regime slices and overlap limits; do not select a profitable variant on this tape. |

The bounded 100,000-ordinal integration prefix was run after the design was written. It is an engineering check, not a parameter search. The full accepted recording is already exposed as V0 engineering evidence and is not a sealed V1 final period. A later untouched chronological period is required before predictive or economic claims. Any subsequent threshold, cost, horizon, population, or signal change must get a new ledger row *before* seeing its outcome and must disclose all earlier attempts.

**Method correction before full-tape outcomes:** nominal rolling lookbacks were changed from sample counts to explicit available-time boundaries after identifying admission jitter. The V1-001 threshold, horizon, cost and comparator set are unchanged. A deterministic 30-minute-block bootstrap was added for descriptive uncertainty. The preliminary 100,000-ordinal prefix remains exposed development evidence, not a tuning period or a sealed test.

**Further evaluator safeguards before full-tape outcomes:** entry and exit require the book update itself to be available after each boundary, and the exploratory report now includes four fixed chronological six-hour development folds. Neither change uses or selects an outcome from the full tape.

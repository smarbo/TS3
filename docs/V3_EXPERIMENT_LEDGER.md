# V3 experiment ledger

Created 2026-10-02 before V3 modelling outcomes. The accepted V0/V1/V2 tape is exposed development data. Every completed experiment, including losing/failed runs, must be appended with commit, dataset hash, schema, target, exact folds/purge/embargo, model, parameters, primary and secondary metrics, fixed costs, result and disposition.

| ID | Predeclared experiment | Status |
| --- | --- | --- |
| V3-001-N | `depth_up_300_v1`, `v3.features.1`, chronological base-rate null model; same V1 paired population and costs | Planned |
| V3-001-L1 | Same target/features/folds; L2 logistic penalty 1 on standardized nonintercept columns | Planned |
| V3-001-L10 | Same, penalty 10 | Planned |
| V3-001-A | Six fixed single-group ablations of the selected development logistic model, reported without claiming a fresh test | Planned |
| V3-001-R | Strongly regularized ridge for long net bps only if folds support it; diagnostic, no probability claim | Conditional |

Hypothesis count at design freeze: one primary target, seven features, one null and two logistic penalties, six disclosed ablations, zero threshold sweep, zero trees. The secondary net-positive label is descriptive only. Any changed feature, label, cost, metric or model after inspecting this tape is a new exposed-data variant, not an untouched confirmation.

**Pre-outcome implementation clarification:** the logistic penalty applies to the sum of fold training losses, not their mean; the intercept is unpenalized. A fixed 50-step full-batch Newton solver, training-only standardization and exact artifact/feature range checks implement that contract. The seven columns, targets, folds, two penalties, costs and selection metric are unchanged. This clarification and all associated code were written while the first full V3 row export was still running; no V3 row labels or model outcomes had been inspected.

**Pre-outcome cost check:** [V3_COST_REVIEW](V3_COST_REVIEW.md) records the current public Kraken Pro fee table. The frozen 20+5 bp per side cost scenario remains primary for identical-population V1/V2 comparison; 10+5 and 80+5 are disclosed sensitivities only. No scenario is chosen from model returns.

**Pre-outcome integration/audit checkpoint:** A deterministic synthetic 24-hour fixture exercised the complete five-fold trainer and artifact writer while the accepted-tape export was still running. The fixture is explicitly synthetic and its easy, constructed classification result is not market evidence. The independent audit now recomputes each fold's training membership, purge/embargo, null score, logistic metrics and aggregates directly from row bytes and reported coefficients. These checks were written before inspecting any V3 accepted-tape row labels or model outcomes.

**Pre-outcome calibration fixture:** A chronological synthetic reversal shows that a calibrator with excellent early in-sample log loss can fail badly on later observations. The effective-count gate rejects both periods together. This tests the refusal to equate a fitted logistic transform with reliable future probabilities; no calibration threshold or model choice changed.

**Pre-outcome uncertainty/cost arithmetic:** The independent audit will report the selected development model's paired out-of-fold log-loss improvement on earliest non-overlapping episodes and a fixed-seed (20261002), 10,000-draw UTC half-hour block bootstrap interval. This interval is descriptive only because all blocks come from one market day. It will also show always-long net outcomes at the preregistered 10+5, frozen 20+5 and 80+5 bps-per-side scenarios on the identical V3 row population. No fee setting is chosen by the outcome.

**Pre-outcome live audit contract:** A public shadow smoke, if run, must have a complete clean raw manifest, exact V0/V2/V3 replay hashes, complete durable V3 acknowledgments, warmed decisions, and no directional V3 action or unsupported calibrated field. The read-only audit script checks these directly. A short smoke is an engineering parity check, never future sealed economic evidence.

**Pre-outcome availability correction:** Adversarial review of the clean V3 trainer found that `training_end` was set to the last **decision** in the final fit, although that row's label only exists at its later exit. The trainer now records the latest actual exit among all fitted rows. Shared inference suppresses scores through that exit. The audit rejects artifacts with an earlier boundary; a regression fixture tests both boundary equality and the preceding minute. The accepted-tape dataset exports were still running, and no V3 outcome had been inspected. The target, fold design, model grid and costs are unchanged; a new clean trainer binary must be built for real training.

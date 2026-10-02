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

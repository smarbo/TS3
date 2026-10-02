# V3 statistical engine design

Status: pre-training V3-001 design, 2026-10-02. This document freezes the small hypothesis space before V3 modelling outcomes are inspected. The accepted tape is development data only. V0/V1/V2 reconstruction, cost, health and intent bytes remain frozen benchmarks.

## Objective, target and population

The primary diagnostic target is `depth_up_300_v1`: `1` when the accepted V1 evaluator's hypothetical **long** side-correct entry-to-exit displayed-depth price component (`Outcome.DepthBps`) is strictly positive, else `0`. Entry is the first healthy, freshly updated quote at or after decision time plus two seconds; exit is the first such quote at or after entry plus 300 seconds. The quote must satisfy V1's two-second age, generation and path-health rules. This price-only target is economically interpretable but excludes fees/allowance, so predictive lift cannot establish tradeability. The secondary descriptive target is `long_net_positive_300_v1`, strictly positive `Outcome.NetBps` under frozen 20-bp taker fee and 5-bp allowance per side; its three positive examples prohibit training a separate classifier on this tape. The numeric long net bps may be explored with a strongly regularized ridge baseline only if a fold has enough rows. No hypothetical short target becomes an executable short intent.

The row population is the frozen V1 eligible five-minute paired population. The V3 exporter attaches a V2 decision snapshot to each downstream V1 paired observation by ordinal and requires identical run/decision time, source generation and quote provenance. A row contains the decision time, actual entry/exit times, ordinals, accepted source manifest hash, V1/V2 config and feature versions, label version, cost version and deterministic canonical hash. Missing future endpoint, unhealthy interval, stale book, generation change, insufficient depth or run end is a censor, never a negative label. No future outcome or dataset handle enters `ApplyTick`.

## Features, schema and transformations

`v3.features.1` has only seven columns, in this order. Values are finite floats derived from already rounded causal V1/V2 intent fields. V1 version is `v1.1`; V2 signal versions are `trend.v2.1`, `reversion.v2.1`, `book_pressure.v2.1`. All feature ordinals must be no later than the decision ordinal and current quote generation must agree.

| Column | Units / source / horizon | Missing semantics |
| --- | --- | --- |
| `return60` | bps; V1 causal midpoint log return over 60 available-time seconds; price mechanism | Missing V1 features excludes row. |
| `displacement300` | bps; V1 midpoint displacement from causal 300-second mean | Same. |
| `rms900` | bps per square-root second; V1 900-second realized volatility | Same. |
| `spread` | bps; current V1 quote BBO at decision tick, at most two seconds old | Missing/stale quote excludes row. |
| `book_pressure30` | signed fraction in [-1,1]; V2 persistent 30-second displayed top-five depth score | `UNAVAILABLE`/`INVALID` excludes row; `NEUTRAL` remains an observed zero-or-small score. |
| `volatile` | 0/1; V1 fixed `VOLATILE` regime threshold, not future-fitted | `UNKNOWN`/`WIDE` excludes row. |
| `disagreement` | -1/0/1; V2 price/book vote relation | `UNDEFINED` maps to -1, explicit agreement to 0 and conflict to 1; missing diversity is not called agreement. |

The last column is a context indicator, not an independent causal mechanism. Trend and reversion categorical direction duplicate the price columns and are excluded. No microprice or external feed is invented. Ablations remove the corresponding column or group: `return60`, `displacement300`, `book_pressure30`, `volatile`, `disagreement`, and `spread`. Standardization uses only each training fold's mean and population standard deviation; zero-variance columns get scale 1 and remain zero after centering. Development validation and future inference use the frozen fold/artifact means and scales. No global-data scaler, class balancer or feature selector is permitted.

## Time splits and small model search

Use chronological expanding development folds with 6, 9, 12, 15 and 18 hours of available training history from the first accepted tick; each subsequent validation window is two hours. Before each validation, purge any training row whose **actual label exit** is at or after the validation start; also leave a fixed six-minute gap between last training decision and validation start. Carry a 30-minute post-validation embargo into any later training set, excluding those embargo rows. Record exact train/purge/validation/embargo timestamps, ordinals and row counts. Never shuffle. At most five validation folds on this one-day tape; a fold with one target class is reported unavailable, not coerced to a fitted probability.

The frozen primary selection metric is mean binary **log loss** on paired validation rows for `depth_up_300_v1`, compared on identical rows with an earlier-training-only base-rate predictor. Secondary metrics: Brier score, class balance, gross and net hypothetical bps, NO_TRADE and fixed-baseline comparators. The search is base rate and L2 logistic regression with just two preregistered penalties (`lambda=1` and `lambda=10`). The optimization objective is the **sum** of binary log losses plus `lambda/2` times the sum of squared standardized nonintercept coefficients; the intercept is unpenalized. Deterministic full-batch Newton steps, fixed row order, Gaussian elimination with fixed pivot tie-breaks and a 50-iteration/1e-10 stopping rule are used. A ridge return model, if attempted, uses one fixed strong penalty and is exploratory, not a competing primary winner. Boosted trees, random forests, neural networks and regime-specific models are postponed for insufficient independent data. The ledger counts all folds, penalties and ablations; a best development score is not a fresh holdout.

For uncertainty, report non-overlapping five-minute episodes and day/30-minute block summaries. Neither an IID row standard error nor a narrow one-day block interval establishes generalization. Compare each model to V1/V2/NO_TRADE, always-long, frozen momentum/reversion and a timestamp-matched deterministic random direction on the **same paired population and costs**. Preserve the accepted V1/V2 benchmark hashes. Report losing variants and all censor counts.

The [cost reality check](V3_COST_REVIEW.md) finds that the frozen 20-bp taker fee corresponds to a published high-volume/assets tier, not an assumed default account rate. Keep it fixed for the primary paired comparison; report predeclared 10+5 and 80+5 fee/allowance sensitivities without selecting a favorable scenario. No public fee table can establish actual fill quality or this project's account tier.

## Calibration, artifact and live inference

Calibration infrastructure consumes only chronological out-of-fold predictions. A Platt fit, if ever used, trains on earlier OOF predictions and is evaluated on later untouched OOF periods with purge/embargo; neither in-sample logits nor the final sealed period fit it. Reliability buckets need at least 200 *effective non-overlapping* observations each and at least 200 observations of each target class across a minimum 30 distinct days. This one-day tape fails: **no calibrated probability is exposed**. Raw logistic output is an uncalibrated score and must not be named confidence. Calibration fields remain omitted/null. Simple distribution and out-of-range feature checks flag OOD; an unavailable/mismatched model or OOD feature fails closed to `NO_TRADE`.

A versioned canonical JSON artifact contains model type/target/schema, ordered columns, coefficients/intercept, training-only means/scales, training start/end and last ordinal, V1/V2/cost versions, dataset hash, training code commit, calibration status, and SHA-256 over canonical artifact bytes excluding the hash field. Go loads and verifies the hash, finite values, schema/version/order and exact expected configuration. Training is offline only. A model change requires a new explicit artifact; there is no live retraining.

The artifact's `training_end` is the **latest actual label exit time** used by its final fit, not its last training decision time. Model scores are unavailable at or before that time. This prevents an offline-fitted artifact from using an outcome whose market endpoint had not yet occurred during a historical replay.

The shared Go live/replay `ApplyTick` path can attach a V3 model ID, artifact hash, uncalibrated score, availability/OOD reason and schema version to a separate V3 intent. Decisions at or before the artifact's training end are explicitly `IN_SAMPLE_PERIOD` and carry no model score; development scores used for comparison come only from chronological validation folds. With no accepted calibration and no validated return magnitude, V3 emits `NO_TRADE`; it never turns an uncalibrated directional score into expected net return. The existing V0/V1/V2 canonical files remain unchanged. The live watchdog is an additional veto and operational times stay outside canonical intent bytes.

## Acceptance boundaries

Engineering completion needs deterministic dataset and artifact bytes from repeat generation; train/validation purge and embargo tests; leakage, correlated-feature, future-fitted-regime and calibration-leakage fixtures; corrupt/mismatched artifact fail-closed tests; shared live/replay inference parity; full Go/race/vet/format/diff checks; complete experiment and walk-forward reports; and a final adversarial audit. Statistical/economic V3 acceptance additionally requires the predeclared future sealed protocol. No development result on the accepted tape can satisfy that gate.

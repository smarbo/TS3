# V3 data sufficiency assessment

Status: pre-training assessment, 2026-10-02. The accepted V0/V1/V2 tape is exposed development data. Counts below come from the accepted V2 report and its frozen V1 paired evaluator, not a new model search.

## Observations and dependence

- Source: public Kraken BTC/USD run `kraken-v0-server-20260927T203224Z`, source manifest SHA-256 `8144ec61b4da66aa0e392df5e96b140a25dbaef7bbc6abdd015ca92ebf5bd7dc`. It contains 12,316,574 committed raw ordinals over 86,700 seconds, seven book epochs and six marked EOF reconnects.
- There are 1,446 minute decisions; 1,339 have the frozen healthy/current/depth eligibility mask, and 1,302 have paired five-minute quote endpoints. Of 37 eligible censors, 31 are unhealthy paths, one has no entry and five end at the run boundary. The separate 30-second pressure study has 1,332 pairs and seven censors.
- Five-minute labels begun one minute apart overlap about fivefold. Even under an optimistic independence assumption outside this overlap, 1,302 paired rows provide at most about 260 non-overlapping five-minute episodes. Serial volatility, common book conditions and same-day shocks may reduce the effective sample further. Only about 49 half-hour time blocks and approximately one distinct market day are available; neither 1,302 nor 260 is a defensible count of independent market regimes.
- The V1 regime counts among all decisions are 1,286 `NORMAL`, 53 `VOLATILE`, 107 `UNKNOWN`, zero `WIDE`. The paired population is 1,254 normal and 48 volatile. At most about ten non-overlapping volatile five-minute episodes exist. A separate volatile model or robust regime-specific calibration is unsupported.
- Frozen V1/V2 policies took zero directional actions. On all 1,302 paired episodes, the frozen hypothetical always-long cost scenario has only **three positive net outcomes** and 1,299 nonpositive outcomes (0.23% positive). The 30-second always-long study has zero positive net outcomes in 1,332 pairs. Five-minute hypothetical momentum (90 actions) and reversion (185 actions) have zero positive net outcomes. A cost-clearing classifier or calibrated cost-clearing probability cannot be estimated from these classes.
- The proposed primary diagnostic label is positive five-minute *displayed-depth executable price change before fees and allowance*, using V1's first valid delayed entry and exit. Its exact positive/negative balance must be measured by the deterministic V3 row export before training. This target is different from positive net return and cannot imply economic value. Rows lacking a valid current feature snapshot or either endpoint are censored, never assigned a class.

## Sufficiency decision

| Question | Assessment |
| --- | --- |
| Engineering feasibility | Enough data and accepted replay machinery to test row provenance, deterministic folds, training-only transforms, artifact validation and shared inference. |
| Exploratory development modelling | A small regularized model and null control can be compared chronologically as a diagnostic. Fold and regime estimates will be noisy and the tape has already influenced project design. |
| Calibration evidence | Insufficient. One day, at most about 260 non-overlapping five-minute episodes, about ten volatile episodes and three net-positive long outcomes cannot support published probability buckets or calibrated confidence. |
| Sealed final validation | Absent. No observation on this tape can be treated as a sealed test. A later frozen candidate and separately collected, unopened period are mandatory. |

The current tape **cannot statistically complete V3**. This does not prevent engineering the pipeline. The predeclared minimum evidence counts and one-time opening protocol are in [V3_SEALED_TEST_PROTOCOL](V3_SEALED_TEST_PROTOCOL.md); they are not lowered to fit this day.

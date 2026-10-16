# V3 evidence map

The `evidence-manifest.json` lists SHA-256 values for every archived sidecar and the WSL directories containing the large raw and replay data. The accepted V0 recording is `kraken-v0-server-20260927T203224Z` under `/home/eddie/TS3-data`; its 43 raw segments remain outside Git. The full V3 dataset exports remain under `/home/eddie/TS3-v3-dataset-{1,2}` and the corrected trainer outputs under `/home/eddie/TS3-v3-train-f6b-{1,2}`. The archive keeps the complete 1,302-row development table and small reports, artifacts, intent journals and audits.

From the repository root in WSL, the independent development audit is:

```sh
python3 tools/v3_audit.py /home/eddie/TS3-v3-dataset-1 /home/eddie/TS3-v3-train-f6b-1 \
  --v2-intents /home/eddie/TS3-v2-accept-1/intents.jsonl
```

Run the same command with `-2` export/training paths to check the independent repeat. The training artifact archived here is from clean corrected commit `f6b182526dee4577632f0ae0d4f1c21685ab22c9`. The optional October 16 public smoke used the earlier clean `68a30de2d4b814a9047e6d1caa289f8fcb60e9d8` artifact, whose coefficients and normalization are identical but whose provenance and artifact hash differ; its exact artifact remains in `/home/eddie/TS3-v3-train-1/artifact.json`. The post-outcome episode-count correction changed neither live inference nor model fit. Smoke sidecars are prefixed `smoke-`; `smoke-replay-{1,2}-*` are independent replays of that exact new raw run. `historical-*` comes from the full accepted development tape and must show `IN_SAMPLE_PERIOD` with no retroactive model score.

The [research report](../../V3_RESEARCH_REPORT.md), [walk-forward report](../../V3_WALK_FORWARD_REPORT.md), [experiment ledger](../../V3_EXPERIMENT_LEDGER.md), and [final audit](../../V3_FINAL_AUDIT.md) explain the statistical and engineering interpretation. The October 16 short public smoke is a parity check only, not the unopened future sealed period.

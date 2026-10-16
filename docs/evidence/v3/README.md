# V3 evidence map

The `evidence-manifest.json` lists SHA-256 values for all 32 archived sidecars and the WSL directories containing the large raw and replay data; `python3 tools/v3_verify_evidence.py docs/evidence/v3 --require-complete` verifies them with zero pending. The accepted V0 recording is `kraken-v0-server-20260927T203224Z` under `/home/eddie/TS3-data`; its 43 raw segments remain outside Git. The full V3 dataset exports remain under `/home/eddie/TS3-v3-dataset-{1,2}` and the corrected trainer outputs under `/home/eddie/TS3-v3-train-f6b-{1,2}`. The archive keeps the complete 1,302-row development table and small reports, artifacts, intent journals and audits.

From the repository root in WSL, the independent development audit is:

```sh
python3 tools/v3_audit.py /home/eddie/TS3-v3-dataset-1 /home/eddie/TS3-v3-train-f6b-1 \
  --v2-intents /home/eddie/TS3-v2-accept-1/intents.jsonl
```

Run the same command with `-2` export/training paths to check the independent repeat. The training artifact archived here is from clean corrected commit `f6b182526dee4577632f0ae0d4f1c21685ab22c9`. The optional October 16 public smoke used the earlier clean `68a30de2d4b814a9047e6d1caa289f8fcb60e9d8` artifact, whose coefficients and normalization are identical but whose provenance and artifact hash differ; its exact artifact remains in `/home/eddie/TS3-v3-train-1/artifact.json`. The post-outcome episode-count correction changed neither live inference nor model fit. Smoke sidecars are prefixed `smoke-`; `smoke-replay-{1,2}-*` are independent replays of that exact new raw run. `historical-*` comes from the complete 12,316,574-ordinal accepted development-tape replay at `/home/eddie/TS3-v3-historical-replay-f6b-2` and must show 1,446 `IN_SAMPLE_PERIOD` intents with no retroactive score. Recheck it with:

```sh
python3 tools/v3_historical_audit.py /home/eddie/TS3-v3-historical-replay-f6b-2 \
  /home/eddie/TS3-v3-train-f6b-1/artifact.json
```

The earlier foreground output at `/home/eddie/TS3-v3-historical-replay-f6b-1` reached the final state ordinal but was interrupted before writing `report.json`; it is preserved and excluded from completion evidence. The completed replay ran detached in tmux, and its independent audit hashes the actual regenerated V0 normalized/state files as well as the V3 intent journal.

The [research report](../../V3_RESEARCH_REPORT.md), [walk-forward report](../../V3_WALK_FORWARD_REPORT.md), [experiment ledger](../../V3_EXPERIMENT_LEDGER.md), and [final audit](../../V3_FINAL_AUDIT.md) explain the statistical and engineering interpretation. The October 16 short public smoke is a parity check only, not the unopened future sealed period.

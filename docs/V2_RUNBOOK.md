# V2 single-feed validation runbook

Use Ubuntu WSL for the accepted tape and CGO race tests. Keep the source directory `/home/eddie/TS3-data` immutable. The official source run ID is `kraken-v0-server-20260927T203224Z`; its manifest SHA-256 is `8144ec61b4da66aa0e392df5e96b140a25dbaef7bbc6abdd015ca92ebf5bd7dc`. The V1 full intent benchmark SHA-256 is `a0dde5ffab48f2e0624ec5b5580765124ea63fb8e2bf8a44d85bf39e9c597f5f`.

## Build and validate a clean V2 commit

From a clean Linux checkout on `v2`, fetch the completed local/pushed commit, record `git rev-parse HEAD`, and run:

```sh
go test -count=1 ./...
CGO_ENABLED=1 go test -race -count=1 ./...
go vet ./...
test -z "$(gofmt -l cmd internal)"
git diff --check
go build -o /home/eddie/ts3-v2-research ./cmd/v2research
go build -o /home/eddie/ts3-v2-collector ./cmd/collector
go version -m /home/eddie/ts3-v2-research
sha256sum /home/eddie/ts3-v2-research /home/eddie/ts3-v2-collector
```

The binaries must say `vcs.revision=<clean V2 commit>` and `vcs.modified=false`. Save the build information and SHA-256 beside the replay outputs. Output directories must be new; the research command refuses to reuse one.

## Two complete historical replays

Run the **same** research binary twice with independent output directories, without `-through`:

```sh
/home/eddie/ts3-v2-research -dir /home/eddie/TS3-data -run kraken-v0-server-20260927T203224Z -out /home/eddie/TS3-v2-eval-1
/home/eddie/ts3-v2-research -dir /home/eddie/TS3-data -run kraken-v0-server-20260927T203224Z -out /home/eddie/TS3-v2-eval-2
sha256sum /home/eddie/TS3-v2-eval-{1,2}/intents.jsonl /home/eddie/TS3-v2-eval-{1,2}/report.json
```

Check each `report.json`: `full_committed_prefix=true`; valid, committed and processed ordinal `12316574`; zero uncommitted; source manifest and revision match V0; V0 normalized/state SHA-256 match accepted V0; V1 intent SHA-256 matches the frozen V1 benchmark; V2 intent and report files match byte for byte across both runs; `research.observer_mismatches=0`. Reconcile calendar, eligible, five-minute and separate pressure 30-second paired/censored episodes, family status, action, reason, cohort, fold and comparator counts. Keep the large regenerated V0 files in WSL; archive both reports and V2 canonical intents in `docs/evidence/v2/` after verification.

## Public live smoke and exact replay

After historical correctness and a clean committed collector build, start a fresh public capture in a dedicated WSL directory with `-v2`. Use approximately one to four hours for the engineering smoke; allow at least 21 minutes for the 900-second V1 feature warm-up and 20-second pressure warm-up, and verify that healthy warmed decisions actually occurred. Use a unique run ID and preserve stdout, binary build info and SHA-256. The collector writes `v2-intents.jsonl`, `v2-live-acks.jsonl`, and an atomic five-second `v2-report.json` beside the V0 run files. Its canonical intents never include live wall-clock timing. The separate acknowledgments record decision-ready/durable times and watchdog vetoes. Stop cleanly at the specified duration; a shutdown `NO_TRADE` remains an ordinary intent.

Replay **that exact raw live run** with the same V2 research binary to a new output directory. Compare the live `v2-intents.jsonl` and replay `intents.jsonl` SHA-256, ordinals and count; compare V0 normalized/state hashes and common applied/committed prefixes. Inspect operational lag and gate events separately; fast replay cannot prove live timeliness. If the live run is interrupted, preserve all raw/journal evidence, mark it incomplete, and repeat the smoke from a new run ID.

## Interpretation

The accepted approximately one-day tape and short forward smoke establish only engineering behavior. The V2 policy may emit no directional intents. Report that as a result, investigate whether the reason is signal absence, conflict, cost, or a defect, and do not tune the frozen thresholds to manufacture actions. Cost-adjusted hypothetical returns are not fills or proof of profitability. No optional second feed is part of this core run.

## Accepted V2-001 evidence, 2026-09-30

The exact clean implementation revision is `5d1647386fe935412a68b333514858f6b2039d0f`, with research binary `/home/eddie/ts3-v2-research-5d16473` (SHA-256 `d733a0cb253ffcaf2a7a2b69fc389b45d381ec643b97650e63b5afaee60c8946`) and collector binary `/home/eddie/ts3-v2-collector-5d16473` (SHA-256 `f91d74127108ea946ca5cdfbfc6fb13f6dabca84c53408ff2e3f2ae9f0e5887f`). Launching the collector from the **clean WSL checkout** `/home/eddie/TS3-v2-validation` matters: the collector records the current directory's Git revision/dirty status in its report and manifest. A clean binary launched from a dirty mounted checkout produced a preliminary `+dirty` report and was excluded.

The completed historical output directories are `/home/eddie/TS3-v2-accept-1` and `/home/eddie/TS3-v2-accept-2`. The qualifying public run is `v2-smoke-5d16473-20260930T191500Z`, with raw root `/home/eddie/TS3-v2-smoke-clean` and raw replay output `/home/eddie/TS3-v2-smoke-replay-5d16473`. The [archived evidence manifest](evidence/v2/evidence-manifest.json) lists every small archived report/intent/journal/log file and its SHA-256. The large raw tape and regenerated V0 normalized/state files remain at those WSL paths. The archive is sufficient to inspect reports and intent bytes; the WSL files are required to independently rehash full raw/derived streams.

From the repository's Windows mount inside WSL, the completed acceptance checks are reproducible with:

```sh
python3 tools/v2_audit.py /home/eddie/TS3-v2-accept-1 /home/eddie/TS3-v2-accept-2
python3 tools/v2_live_audit.py \
  /home/eddie/TS3-v2-smoke-clean/v2-smoke-5d16473-20260930T191500Z \
  /home/eddie/TS3-v2-smoke-replay-5d16473 \
  5d1647386fe935412a68b333514858f6b2039d0f
python3 tools/v2_cohort_summary.py /home/eddie/TS3-v2-accept-1
```

Both full historical replays, the 65-minute smoke and its raw replay exited 0. The full replay audit passed with 12,316,574 ordinals, exact accepted V0/V1 hashes, byte-identical V2 intents and reports, 1,446 decisions, 1,339 eligible, 1,302 five-minute paired and 1,332 pressure 30-second paired episodes. The live audit passed with 422,062 raw ordinals, 67 durably acknowledged V2 intents, exact V0/V2 live/replay parity, no gate veto and a clean ordered shutdown. See [V2_RESEARCH_REPORT](V2_RESEARCH_REPORT.md) for results and [V2_FINAL_AUDIT](V2_FINAL_AUDIT.md) for the adversarial gate. The published archive contains no account keys, private data, orders, or execution code.

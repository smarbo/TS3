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

Check each `report.json`: `full_committed_prefix=true`; valid, committed and processed ordinal `12316574`; zero uncommitted; source manifest and revision match V0; V0 normalized/state SHA-256 match accepted V0; V1 intent SHA-256 matches the frozen V1 benchmark; V2 intent and report files match byte for byte across both runs; `research.observer_mismatches=0`. Reconcile calendar, eligible, paired, censored, family status, action, reason, cohort, fold and comparator counts. Keep the large regenerated V0 files in WSL; archive both reports and V2 canonical intents in `docs/evidence/v2/` after verification.

## Public live smoke and exact replay

After historical correctness and a clean committed collector build, start a fresh public capture in a dedicated WSL directory with `-v2`, for at least 21 minutes so the 900-second V1 feature warm-up and 20-second pressure warm-up both finish. Use a unique run ID and preserve stdout, binary build info and SHA-256. The collector writes `v2-intents.jsonl`, `v2-live-acks.jsonl`, and an atomic five-second `v2-report.json` beside the V0 run files. Its canonical intents never include live wall-clock timing. The separate acknowledgments record decision-ready/durable times and watchdog vetoes. Stop cleanly at the specified duration; a shutdown `NO_TRADE` remains an ordinary intent.

Replay **that exact raw live run** with the same V2 research binary to a new output directory. Compare the live `v2-intents.jsonl` and replay `intents.jsonl` SHA-256, ordinals and count; compare V0 normalized/state hashes and common applied/committed prefixes. Inspect operational lag and gate events separately; fast replay cannot prove live timeliness. If the live run is interrupted, preserve all raw/journal evidence, mark it incomplete, and repeat the smoke from a new run ID.

## Interpretation

The accepted approximately one-day tape and short forward smoke establish only engineering behavior. The V2 policy may emit no directional intents. Report that as a result, investigate whether the reason is signal absence, conflict, cost, or a defect, and do not tune the frozen thresholds to manufacture actions. Cost-adjusted hypothetical returns are not fills or proof of profitability. No optional second feed is part of this core run.

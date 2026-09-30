# V1 baseline validation runbook

V1 is a public-data **shadow research** layer. `collector -v1` writes analytical intents and measured operational acknowledgments; it never sends orders or uses account APIs. Read [V1_DESIGN](V1_DESIGN.md), [PLAN](PLAN.md), and [STATUS](STATUS.md) before interpreting results. Preserve the accepted V0 run and manifest unchanged.

## Build and validate

Run from a clean `v1` checkout with Go 1.27 and a working C compiler for the race detector:

```sh
git status --short
git rev-parse HEAD
go test -count=1 ./...
CGO_ENABLED=1 go test -race -count=1 ./...
go vet ./...
gofmt -l cmd internal
git diff --check
go build -o /tmp/ts3-v1-collector ./cmd/collector
go build -o /tmp/ts3-v1-research ./cmd/v1research
go version -m /tmp/ts3-v1-collector
go version -m /tmp/ts3-v1-research
```

Confirm `vcs.revision` is the clean checkout commit and `vcs.modified=false`. Use a unique output directory for each analysis. Do not use an interrupted raw run as full acceptance evidence.

## Historical development report

```sh
run=kraken-v0-server-20260927T203224Z
/tmp/ts3-v1-research -dir /home/eddie/TS3-data -run "$run" -out /home/eddie/TS3-v1-eval-1
/tmp/ts3-v1-research -dir /home/eddie/TS3-data -run "$run" -out /home/eddie/TS3-v1-eval-2
sha256sum /home/eddie/TS3-v1-eval-{1,2}/intents.jsonl
```

Inspect each `report.json`: the source manifest hash and accepted source revision, clean full committed prefix, processed ordinal, V0 normalized/state hashes, V1 intent hash, decisions, eligible and censored episodes, reason counts, action frequency, paired comparators, chronological folds, regime slices, assumed cost components and sensitivity, and block-bootstrap interval. Compare the two intent hashes exactly. Compare the regenerated V0 hashes to the accepted hashes in [STATUS](STATUS.md). The generated `v0/` subdirectory is derived replay output, not a new raw tape. The accepted one-day tape is exploratory development data, not a sealed final test or proof of profit.

## Public live smoke

```sh
run=v1-smoke-$(date -u +%Y%m%dT%H%M%SZ)
/tmp/ts3-v1-collector -dir /home/eddie/TS3-v1-smoke -run "$run" -duration 21m -v1
cat "/home/eddie/TS3-v1-smoke/$run/v1-report.json"
/tmp/ts3-v1-research -dir /home/eddie/TS3-v1-smoke -run "$run" -out "/home/eddie/TS3-v1-smoke/$run-replay"
```

The live run report is atomically refreshed about every five seconds. `v1-intents.jsonl` contains canonical deterministic shadow intents; `v1-live-acks.jsonl` contains measured decision-ready and intent-durable times plus the live gate state. A directional row with a closed gate has `LIVE_GATE_CLOSED` and is not publishable. `v1-report.json` has the intent SHA-256 and acknowledgment counts. Preserve the V0 raw segments, V0 manifest, V0 `run-report.json`, V1 journals, V1 report, and replay result. After a clean smoke, compare the live `journal.intent_sha256` with the replay `intent_sha256` on the common applied prefix and inspect all gap/reconnect/health incidents. An interrupted report is not a pass.

The 21-minute smoke checks warm-up and continuous plumbing. It does not establish market edge. If any V1 gate remains open, document the exact blocker in `STATUS.md`, keep the machine running, and do not mark V1 complete or shut down.

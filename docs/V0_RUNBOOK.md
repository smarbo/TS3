# V0 capture and replay runbook

V0 is incomplete. The active source is public Kraken Spot `BTC/USD` book depth 100 with an ordered public metadata GET. Coinbase Exchange's Level 2 feed now requires authentication and is no longer used for live capture.

## Commands

From the repository root, with Go 1.27 or newer:

```powershell
$env:GOCACHE = Join-Path (Get-Location) '.gocache'
$env:GOTMPDIR = Join-Path (Get-Location) '.gotmp'
New-Item -ItemType Directory -Force .gocache, .gotmp | Out-Null
go test ./...
go vet ./...
go run ./cmd/collector -dir data -run example-unique-id -duration 20s
go run ./cmd/replay -dir data -run example-unique-id
go run ./cmd/v0report -dir data -run example-unique-id
```

Use a unique run ID for each attempt; the recorder refuses to overwrite existing files. Omit `-duration` for an operator-stopped capture. `-paced` on replay sleeps according to recorded logical time; `-step` waits for Enter before each applied event; default replay is fast. The replay report includes committed raw ordinal, processed ordinal, recovered live applied/published marks, and automatic `live_parity` when a clean run has matching high-water marks. Use `-through <applied_ordinal>` to compare only the common live-applied prefix when a crash left a committed but unapplied suffix. After a capture has stopped, `-recover-manifest` writes an explicitly unclean index if the final manifest is missing or partial; it preserves a partial original as `.manifest.json.invalid.json` and never alters raw bytes. Recovery rejects raw segments written in the preceding five seconds. A successful live smoke should show `book healthy`, finish cleanly, and replay with `live_parity.equal=true`.

## Incident handling

Preserve the `.raw` segments, `.manifest.json` when present, and run directory together. Never append to an existing run or copy a stale book into a new run. Replay reads only through verified commit markers; a missing manifest or uncommitted suffix is flagged. A corrupted committed frame is an error. A new live run needs a new ID, ordered metadata, a new connection epoch, verified subscription, fresh snapshot, heartbeat, and applied tick before its operational gate can open. Do not claim live parity for a committed but unapplied or unpublished crash suffix.

## Incremental evidence and restart

The collector writes `data/<run-id>/run-report.json` before source capture and atomically refreshes it every five seconds while running. It includes start/update time, code revision, Go version, config hash, runtime, last durably confirmed healthy time, counts, current health, incident counters, committed/applied/published ordinals, warnings, and evidence file sizes. Kraken book v2 has no contiguous sequence number, so `sequence_gaps` is `null`; `capture_gaps` counts explicit run gaps. The `.raw` segment is committed and fsynced after each batch (at most 1,024 events or about 100 ms); normalized/state files and `progress.jsonl` are fsynced before progress is acknowledged. `metrics.json` and the final `.manifest.json` are written at shutdown. Only one collector should use a data directory at a time.

For the official soak, check out the exact committed `v0` revision and require `git status --porcelain` to be empty before capture. Save `git rev-parse HEAD` as the expected full hash. Confirm that `code_revision` in the live `run-report.json` and `provenance.code_revision` in the final manifest both equal that hash exactly, with no `+dirty` suffix. If a V0 fix changes code during the run, the new commit needs a new qualifying run.

On the server, inspect a live run with `cat data/<run-id>/run-report.json` and `tail -n 5 data/<run-id>/progress.jsonl`. A clean scheduled end changes the report to `COMPLETE`; this means the collector closed cleanly, **not** that the 24-hour audit passed. If the machine or process stops abruptly, the last atomic report remains `RUNNING` until the next collector startup marks it `INCOMPLETE`. Do not resume the same run ID or count it toward the mandatory 24-hour continuous capture. Preserve its files and start a fresh unique run. After the old raw files are at least five seconds old, recover an absent manifest and inspect the committed prefix with:

```sh
go run ./cmd/replay -dir data -run <old-run-id> -out data/<old-run-id>-recovery-replay -recover-manifest
```

The recovered manifest is explicitly unclean; replay reports committed and uncertain suffixes. Keep that evidence for diagnosis. The final `cmd/v0report` check `run_report_complete` must be true in addition to the clean manifest, 24-hour healthy span, full parity, and other checks.

The race suite passed under Ubuntu WSL using GCC 13.3 and Go 1.27.0:

```powershell
wsl.exe -d Ubuntu -- bash -lc 'cd /mnt/c/Users/eddie/Nextcloud/TS3 && CGO_ENABLED=1 go test -race ./...'
```

The local runs `data/v0final/kraken-v0-final-20260927` and `data/kraken-soak-final-20260927` were stopped early. Their raw files are preserved, but neither is an acceptance soak. `data/` is ignored by Git, so a fresh server checkout needs its own capture. On an always-on Linux server, from the repository root, use a new run ID in each command below (replace `kraken-v0-server-20260928` if it already exists):

```sh
go test -count=1 ./...
CGO_ENABLED=1 go test -race -count=1 ./...
go vet ./...
go run ./cmd/collector -dir data -run kraken-v0-server-20260928 -duration 24h5m
go run ./cmd/replay -dir data -run kraken-v0-server-20260928
go run ./cmd/v0report -dir data -run kraken-v0-server-20260928
```

Run the collector under a persistent server service or terminal multiplexer so an SSH disconnect does not end the process. Keep it running through its scheduled end with public Kraken HTTPS and WebSocket access. Run replay and the final audit only after the collector exits, `run-report.json` says `COMPLETE`, and the final manifest and `metrics.json` exist. The audit `checks` include the 24-hour healthy span, completed run report, exact high-water marks, lag sample coverage, manifest/raw integrity, and full output parity; inspect its incident list and health durations before accepting the soak. The command reports clock anomalies but does not claim an external clock-offset measurement. Archive the raw segments, run report, manifest, progress journal, replay result, and audit JSON. V0 remains incomplete until the 24-hour gate and incident audit pass.

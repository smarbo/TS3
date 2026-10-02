#!/usr/bin/env python3
"""Archive small V2 acceptance artifacts; leave raw and regenerated V0 files in WSL."""

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path


def sha256(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def archive(source, destination):
    if destination.exists():
        if sha256(destination) != sha256(source):
            raise ValueError(f"existing archive differs: {destination}")
        return
    with tempfile.NamedTemporaryFile(dir=destination.parent, prefix=".v2-archive-",
                                     delete=False) as stream:
        tmp = Path(stream.name)
    try:
        shutil.copyfile(source, tmp)
        if sha256(tmp) != sha256(source):
            raise ValueError(f"copy hash mismatch: {source}")
        os.replace(tmp, destination)
    finally:
        tmp.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--historical-first", type=Path, required=True)
    parser.add_argument("--historical-second", type=Path, required=True)
    parser.add_argument("--smoke-root", type=Path, required=True)
    parser.add_argument("--smoke-run", required=True)
    parser.add_argument("--smoke-replay", type=Path, required=True)
    parser.add_argument("--research-binary", type=Path, required=True)
    parser.add_argument("--collector-binary", type=Path, required=True)
    parser.add_argument("--destination", type=Path, required=True)
    args = parser.parse_args()
    args.destination.mkdir(parents=True, exist_ok=True)
    live = args.smoke_root / args.smoke_run
    files = {
        "historical-1-report.json": args.historical_first / "report.json",
        "historical-2-report.json": args.historical_second / "report.json",
        "historical-1-intents.jsonl": args.historical_first / "intents.jsonl",
        "historical-2-intents.jsonl": args.historical_second / "intents.jsonl",
        "smoke-run-report.json": live / "run-report.json",
        "smoke-v2-report.json": live / "v2-report.json",
        "smoke-v2-intents.jsonl": live / "v2-intents.jsonl",
        "smoke-v2-live-acks.jsonl": live / "v2-live-acks.jsonl",
        "smoke-metrics.json": live / "metrics.json",
        "smoke-manifest.json": args.smoke_root / (args.smoke_run + ".manifest.json"),
        "smoke-collector.log": args.smoke_root / "collector.log",
        "smoke-collector-build-info.txt": args.smoke_root / "collector.build-info.txt",
        "smoke-collector-binary.sha256": args.smoke_root / "collector.binary.sha256",
        "smoke-replay-report.json": args.smoke_replay / "report.json",
        "smoke-replay-intents.jsonl": args.smoke_replay / "intents.jsonl",
    }
    for name, source in files.items():
        archive(source, args.destination / name)
    research_build = subprocess.run(
        ["/usr/local/go/bin/go", "version", "-m", str(args.research_binary)],
        check=True, capture_output=True, text=True).stdout
    research_info = args.destination / "historical-research-build-info.txt"
    if research_info.exists() and research_info.read_text() != research_build:
        raise ValueError("existing research build information differs")
    research_info.write_text(research_build)
    manifest = {
        "analysis_binary_sha256": sha256(args.research_binary),
        "collector_binary_sha256": sha256(args.collector_binary),
        "historical_first": str(args.historical_first),
        "historical_second": str(args.historical_second),
        "smoke_root": str(args.smoke_root),
        "smoke_run": args.smoke_run,
        "smoke_replay": str(args.smoke_replay),
        "archived_sha256": {name: sha256(args.destination / name)
                            for name in sorted(files)},
    }
    manifest_path = args.destination / "evidence-manifest.json"
    content = json.dumps(manifest, sort_keys=True, indent=2) + "\n"
    if manifest_path.exists() and manifest_path.read_text() != content:
        raise ValueError("existing evidence manifest differs")
    manifest_path.write_text(content)
    print(f"archived {len(files)} evidence files in {args.destination}")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Create a deterministic 24-hour synthetic V3 dataset for trainer integration checks."""

import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path


def canonical(value):
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()


def stamp(value):
    return value.isoformat(timespec="seconds").replace("+00:00", "Z")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("out", type=Path)
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=False)
    start = dt.datetime(2026, 1, 1, tzinfo=dt.timezone.utc)
    lines = []
    up = net_positive = 0
    for j in range(24 * 60):
        at = start + dt.timedelta(minutes=j)
        x = float((j % 20) - 10)
        depth = x * 2
        label = depth > 0
        net = depth - 50
        row = {
            "schema_version": 1, "feature_schema": "v3.features.1",
            "target_version": "depth_up_300_v1",
            "cost_version": "v1.default.20fee.5allowance.usd100",
            "source_manifest_sha256": "synthetic-manifest", "run_id": "synthetic",
            "decision_ordinal": str(j + 1), "decision_time": stamp(at),
            "entry_time": stamp(at + dt.timedelta(seconds=2)),
            "exit_time": stamp(at + dt.timedelta(seconds=302)),
            "quote_generation": "1", "quote_book_ordinal": str(max(0, j)),
            "v1_config_sha256": "v1-synthetic", "v2_config_sha256": "v2-synthetic",
            "v1_feature_version": "features.v1",
            "signal_versions": ["trend.v2.1", "reversion.v2.1", "book_pressure.v2.1"],
            "regime": "VOLATILE" if j % 20 == 0 else "NORMAL",
            "features": [x, -x / 2, abs(x), 0.1, x / 100, float(j % 20 == 0), -1.0],
            "depth_up": label, "long_depth_bps": depth, "long_gross_mid_bps": depth,
            "long_net_bps": net, "hypothetical_short_net_bps": -depth - 50,
            "long_net_positive": net > 0, "v1_action": "NO_TRADE", "v2_action": "NO_TRADE",
            "momentum_direction": "LONG" if x > 5 else "FLAT",
            "reversion_direction": "SHORT" if x > 5 else "FLAT",
        }
        lines.append(canonical(row))
        up += label
        net_positive += net > 0
    data = b"".join(lines)
    (args.out / "rows.jsonl").write_bytes(data)
    report = {"analysis_revision": "synthetic-fixture", "full_committed_prefix": True,
              "source_run_id": "synthetic", "source_manifest_sha256": "synthetic-manifest",
              "dataset_sha256": hashlib.sha256(data).hexdigest(), "rows": len(lines),
              "v1_intent_sha256": "synthetic-v1", "v2_intent_sha256": "synthetic-v2",
              "v1_research": {"first_tick": stamp(start)}}
    (args.out / "report.json").write_bytes(canonical(report))
    print(f"synthetic rows={len(lines)} depth_up={up} long_net_positive={net_positive}")


if __name__ == "__main__":
    main()

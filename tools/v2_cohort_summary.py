#!/usr/bin/env python3
"""Summarize frozen V2 decisions and matched hypothetical studies without fitting."""

import argparse
import collections
import json
import statistics
from pathlib import Path


def stats(values):
    if not values:
        return None
    return {"min": min(values), "median": statistics.median(values),
            "max": max(values)}


def result(row):
    actions = row["actions"]
    return {"episodes": row["episodes"], "actions": actions,
            "positive_net_actions": row["positive_actions"],
            "gross_bps_per_action": row["sum_gross_mid_bps"] / actions if actions else None,
            "friction_bps_per_action": row["sum_friction_bps"] / actions if actions else None,
            "net_bps_per_action": row["sum_net_bps"] / actions if actions else None}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    directory = args.directory
    report = json.loads((directory / "report.json").read_bytes())["research"]
    votes = collections.Counter()
    consensus = collections.Counter()
    combos = collections.Counter()
    proxy = []
    friction = []
    margin = []
    with (directory / "intents.jsonl").open("rb") as stream:
        for line in stream:
            i = json.loads(line)
            e = i["evidence"]
            votes[(e["price_vote"], e["book_vote"])] += 1
            combo = tuple(s["status"] for s in i["signals"])
            combos[combo] += 1
            if e["reason"] == "CONSENSUS":
                consensus[e["direction"]] += 1
                o = i["opportunity"]
                proxy.append(o["past_move_proxy_micro_bps"] / 1e6)
                friction.append(o["friction_micro_bps"] / 1e6)
                margin.append(o["margin_micro_bps"] / 1e6)
    print(json.dumps({
        "raw_actions": report["raw_actions"],
        "deduplicated_theses": report["deduplicated_theses"],
        "reasons": report["reasons"],
        "signal_status": report["signal_status"],
        "consensus_sides": dict(consensus),
        "consensus_proxy_bps": stats(proxy),
        "consensus_friction_bps": stats(friction),
        "consensus_margin_bps": stats(margin),
        "mechanism_votes": {f"{p}/{b}": n for (p, b), n in sorted(votes.items())},
        "family_status_combinations": {"/".join(k): n for k, n in sorted(combos.items())},
        "paired_300s": report["paired_evaluator"]["paired_episodes"],
        "censored_300s": report["paired_evaluator"]["censored"],
        "paired_30s": report["pressure_30_paired"],
        "censored_30s": report["pressure_30_censored"],
        "family_300s": {name: result(row) for name, row in
                        report["family_hypothetical"].items()},
        "pressure_30s": {name: result(row) for name, row in
                         report["pressure_30_controls"].items()},
        "price_cohorts_300s": {name: result(row) for name, row in
                               report["cohort_price_hypothetical"].items()},
    }, sort_keys=True, indent=2))


if __name__ == "__main__":
    main()

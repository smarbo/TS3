#!/usr/bin/env python3
"""Independently reconcile accepted-source V3 rows and chronological model folds."""

import argparse
import collections
import datetime as dt
import hashlib
import json
import math
from pathlib import Path


SOURCE = "8144ec61b4da66aa0e392df5e96b140a25dbaef7bbc6abdd015ca92ebf5bd7dc"
V0_NORMAL = "06950e8b672a10949d97339c42fb294b72300544eb3eb64aba6f34e647e0e953"
V0_STATE = "200fb2785cdc278dce732d97a75162cb0b7fb2550e24ff45835b06cbc027f132"
V1_INTENT = "a0dde5ffab48f2e0624ec5b5580765124ea63fb8e2bf8a44d85bf39e9c597f5f"
V2_INTENT = "b43a39952c12348b7f340af79c2b36e0596f978a5709519d9c953bd6f55ef505"


def require(condition, message):
    if not condition:
        raise ValueError(message)


def stamp(value):
    return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dataset", type=Path)
    parser.add_argument("training", type=Path)
    args = parser.parse_args()
    dataset = json.loads((args.dataset / "report.json").read_text())
    training = json.loads((args.training / "report.json").read_text())
    artifact = json.loads((args.training / "artifact.json").read_text())
    require(dataset["full_committed_prefix"] and dataset["processed_ordinal"] == "12316574",
            "incomplete accepted source")
    for key, value in (("source_manifest_sha256", SOURCE),
                       ("v0_normalized_sha256", V0_NORMAL),
                       ("v0_state_sha256", V0_STATE),
                       ("v1_intent_sha256", V1_INTENT),
                       ("v2_intent_sha256", V2_INTENT)):
        require(dataset[key] == value, f"accepted {key} mismatch")
    raw = (args.dataset / "rows.jsonl").read_bytes()
    digest = hashlib.sha256(raw).hexdigest()
    require(digest == dataset["dataset_sha256"] == training["dataset_sha256"] ==
            artifact["dataset_sha256"], "dataset digest mismatch")
    rows = [json.loads(line) for line in raw.splitlines()]
    require(len(rows) == dataset["rows"] == training["rows"], "row count mismatch")
    require(dataset["v1_research"]["paired_episodes"] ==
            len(rows) + dataset["paired_but_feature_unavailable"], "paired reconciliation")
    positive = net_positive = 0
    regimes = collections.Counter()
    previous_ordinal = 0
    previous_time = None
    for row in rows:
        ordinal = int(row["decision_ordinal"])
        decision, entry, exit_ = map(stamp, (row["decision_time"], row["entry_time"], row["exit_time"]))
        require(ordinal > previous_ordinal and (previous_time is None or decision > previous_time),
                "row decision order")
        require(int(row["quote_book_ordinal"]) <= ordinal and
                entry >= decision + dt.timedelta(seconds=2) and
                exit_ >= entry + dt.timedelta(seconds=300), "future quote or early endpoint")
        require(row["source_manifest_sha256"] == SOURCE and
                row["feature_schema"] == "v3.features.1" and
                row["target_version"] == "depth_up_300_v1" and
                row["cost_version"] == "v1.default.20fee.5allowance.usd100", "row schema")
        require(len(row["features"]) == 7 and all(math.isfinite(v) for v in row["features"]),
                "invalid feature vector")
        require(row["depth_up"] == (row["long_depth_bps"] > 0) and
                row["long_net_positive"] == (row["long_net_bps"] > 0), "label mismatch")
        require(row["v1_action"] == row["v2_action"] == "NO_TRADE", "baseline action changed")
        positive += row["depth_up"]
        net_positive += row["long_net_positive"]
        regimes[row["regime"]] += 1
        previous_ordinal, previous_time = ordinal, decision
    require(positive == dataset["depth_up"] == training["positive"] and
            len(rows)-positive == dataset["depth_nonpositive"] == training["negative"],
            "target balance mismatch")
    require(net_positive == dataset["long_net_positive"] == training["long_net_positive"],
            "net-positive count mismatch")
    require(dict(regimes) == dataset["regimes"], "regime counts mismatch")
    require(training["v1_intent_sha256"] == V1_INTENT and
            training["v2_intent_sha256"] == V2_INTENT, "training baseline mismatch")
    require(artifact["sha256"] == training["artifact_sha256"] and
            artifact["calibration_status"] == "UNAVAILABLE_INSUFFICIENT_EVIDENCE" and
            not training["calibration_count_gate"], "unsupported calibrated artifact")
    require(len(training["folds"]) == 5, "fold count")
    previous_validation_end = None
    for fold in training["folds"]:
        start, end = stamp(fold["validation_start"]), stamp(fold["validation_end"])
        cutoff = stamp(fold["train_cutoff"])
        require(cutoff <= start-dt.timedelta(minutes=6) and
                stamp(fold["train_max_label_exit"]) < start and
                end-start == dt.timedelta(hours=2) and
                stamp(fold["embargo_end"]) == end+dt.timedelta(minutes=30),
                f"fold {fold['index']} time leakage")
        if previous_validation_end is not None:
            require(start > previous_validation_end, "validation folds overlap")
        previous_validation_end = end
        val = [r for r in rows if start <= stamp(r["decision_time"]) < end]
        require(len(val) == fold["validation"]["rows"] == fold["null"]["rows"] ==
                fold["logistic_lambda_1"]["rows"] == fold["logistic_lambda_10"]["rows"],
                f"fold {fold['index']} population mismatch")
        require(sum(r["depth_up"] for r in val) == fold["validation"]["positive"],
                f"fold {fold['index']} class mismatch")
        require(int(val[0]["decision_ordinal"]) == int(fold["validation_first_ordinal"]) and
                int(val[-1]["decision_ordinal"]) == int(fold["validation_last_ordinal"]),
                f"fold {fold['index']} ordinal range")
    print(json.dumps({"dataset_sha256": digest, "rows": len(rows), "depth_up": positive,
                      "long_net_positive": net_positive, "regimes": dict(regimes),
                      "selected_lambda": training["selected_development_lambda"],
                      "artifact_sha256": artifact["sha256"]}, sort_keys=True, indent=2))
    print("V3 DATASET/WALK-FORWARD AUDIT PASSED")


if __name__ == "__main__":
    main()

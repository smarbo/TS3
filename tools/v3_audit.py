#!/usr/bin/env python3
"""Independently reconcile accepted-source V3 rows and chronological model folds."""

import argparse
import collections
import datetime as dt
import hashlib
import json
import math
import random
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


def sigmoid(value):
    if value >= 0:
        z = math.exp(-value)
        return 1 / (1 + z)
    z = math.exp(value)
    return z / (1 + z)


def metrics(rows, scores):
    losses = [math.log1p(math.exp(-abs(s))) + max(0, -s if r["depth_up"] else s)
              for r, s in zip(rows, scores)]
    briers = [(sigmoid(s) - float(r["depth_up"])) ** 2 for r, s in zip(rows, scores)]
    return sum(losses) / len(rows), sum(briers) / len(rows)


def close(actual, expected, label):
    require(math.isclose(actual, expected, rel_tol=1e-9, abs_tol=1e-10), label)


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
    require(stamp(artifact["training_end"]) >= max(stamp(r["exit_time"]) for r in rows) and
            int(artifact["last_train_ordinal"]) == int(rows[-1]["decision_ordinal"]) and
            artifact["training_run_id"] == dataset["source_run_id"],
            "artifact available before final training label")
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
    prior_embargo = []
    totals = collections.defaultdict(float)
    total_validation = 0
    oof = []
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
        train = [r for r in rows if stamp(r["decision_time"]) < cutoff and
                 stamp(r["exit_time"]) < start and
                 not any(a <= stamp(r["decision_time"]) < b for a, b in prior_embargo)]
        require(len(train) == fold["train_rows"] and
                int(train[-1]["decision_ordinal"]) == int(fold["train_last_ordinal"]) and
                sum(r["depth_up"] for r in train) == fold["train_positive"] and
                max(stamp(r["exit_time"]) for r in train) == stamp(fold["train_max_label_exit"]),
                f"fold {fold['index']} train/purge/embargo mismatch")
        p = (sum(r["depth_up"] for r in train) + 1) / (len(train) + 2)
        base_score = math.log(p / (1 - p))
        null_loss, null_brier = metrics(val, [base_score] * len(val))
        close(null_loss, fold["null"]["log_loss"], "null log loss")
        close(null_brier, fold["null"]["brier"], "null Brier")
        for penalty in (1, 10):
            mean = [sum(r["features"][j] for r in train) / len(train) for j in range(7)]
            scale = [math.sqrt(sum((r["features"][j]-mean[j])**2 for r in train) / len(train))
                     for j in range(7)]
            scale = [v or 1.0 for v in scale]
            beta = fold[f"coefficients_lambda_{penalty}"]
            require(len(beta) == 8 and all(math.isfinite(v) for v in beta),
                    "invalid fold coefficients")
            scores = [beta[0] + sum(beta[j+1]*(r["features"][j]-mean[j])/scale[j]
                                    for j in range(7)) for r in val]
            loss, brier = metrics(val, scores)
            close(loss, fold[f"logistic_lambda_{penalty}"]["log_loss"], "logistic log loss")
            close(brier, fold[f"logistic_lambda_{penalty}"]["brier"], "logistic Brier")
            totals[f"lambda_{penalty}"] += loss * len(val)
            totals[f"lambda_{penalty}_brier"] += brier * len(val)
            if penalty == 1:
                scores1 = scores
            else:
                scores10 = scores
        for row, s1, s10 in zip(val, scores1, scores10):
            oof.append((row, base_score, s1, s10))
        totals["null"] += null_loss * len(val)
        totals["null_brier"] += null_brier * len(val)
        total_validation += len(val)
        prior_embargo.append((end, stamp(fold["embargo_end"])))
    for name in ("null", "lambda_1", "lambda_10"):
        close(totals[name] / total_validation, training["aggregate_log_loss"][name],
              f"aggregate {name} log loss")
        close(totals[f"{name}_brier"] / total_validation, training["aggregate_brier"][name],
              f"aggregate {name} Brier")
    selected = f"lambda_{int(training['selected_development_lambda'])}"
    close(training["aggregate_log_loss"][selected] - training["aggregate_log_loss"]["null"],
          training["selected_minus_null_log_loss"], "selected comparison")
    selected_index = 2 if selected == "lambda_1" else 3
    effective = []
    last_exit = None
    blocks = collections.defaultdict(list)
    for row, null_score, s1, s10 in oof:
        chosen = (s1, s10)[selected_index-2]
        label = row["depth_up"]
        null_loss = math.log1p(math.exp(-abs(null_score))) + max(0, -null_score if label else null_score)
        selected_loss = math.log1p(math.exp(-abs(chosen))) + max(0, -chosen if label else chosen)
        improvement = null_loss - selected_loss
        at = stamp(row["decision_time"])
        key = at.replace(minute=30 if at.minute >= 30 else 0, second=0, microsecond=0).isoformat()
        blocks[key].append(improvement)
        if last_exit is None or stamp(row["entry_time"]) >= last_exit:
            effective.append(improvement)
            last_exit = stamp(row["exit_time"])
    block_values = list(blocks.values())
    rng = random.Random(20261002)
    draws = []
    for _ in range(10000):
        picked = [block_values[rng.randrange(len(block_values))] for _ in block_values]
        draws.append(sum(map(sum, picked)) / sum(map(len, picked)))
    draws.sort()
    cost_sensitivity = {}
    for fee in (10, 20, 80):
        net = [r["long_depth_bps"] -
               (r["long_depth_bps"]-r["long_net_bps"])*(fee+5)/25
               for r in rows]
        cost_sensitivity[f"fee_{fee}_allowance_5"] = {
            "mean_net_bps": sum(net)/len(net), "positive": sum(x > 0 for x in net)}
    print(json.dumps({"dataset_sha256": digest, "rows": len(rows), "depth_up": positive,
                      "long_net_positive": net_positive, "regimes": dict(regimes),
                      "selected_lambda": training["selected_development_lambda"],
                      "artifact_sha256": artifact["sha256"],
                      "oof_rows": len(oof), "oof_nonoverlap": len(effective),
                      "oof_nonoverlap_mean_log_loss_improvement": sum(effective)/len(effective),
                      "half_hour_blocks": len(block_values),
                      "half_hour_bootstrap_p025_p975_descriptive":
                      [draws[250], draws[9749]],
                      "always_long_cost_sensitivity": cost_sensitivity},
                     sort_keys=True, indent=2))
    print("V3 DATASET/WALK-FORWARD AUDIT PASSED")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Read-only V2 acceptance identity audit for two complete replay directories."""

import argparse
import collections
import hashlib
import json
from pathlib import Path

RUN = "kraken-v0-server-20260927T203224Z"
SOURCE_REV = "d26241151331795cf5005ba09d5f176d1efb6b22"
SOURCE_MANIFEST = "8144ec61b4da66aa0e392df5e96b140a25dbaef7bbc6abdd015ca92ebf5bd7dc"
V0_NORMALIZED = "06950e8b672a10949d97339c42fb294b72300544eb3eb64aba6f34e647e0e953"
V0_STATE = "200fb2785cdc278dce732d97a75162cb0b7fb2550e24ff45835b06cbc027f132"
V1_INTENTS = "a0dde5ffab48f2e0624ec5b5580765124ea63fb8e2bf8a44d85bf39e9c597f5f"
ORDINAL = 12_316_574
ANALYSIS_REV = "5d1647386fe935412a68b333514858f6b2039d0f"
FAMILIES = ("trend.v2.1", "reversion.v2.1", "book_pressure.v2.1")
STATUSES = {"ACTIVE", "NEUTRAL", "UNAVAILABLE", "INVALID"}


def check(ok, message):
    if not ok:
        raise AssertionError(message)


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def audit(directory):
    report_path = directory / "report.json"
    intents_path = directory / "intents.jsonl"
    report = json.loads(report_path.read_bytes())
    research = report["research"]
    evaluator = research["paired_evaluator"]
    inp = report["input"]
    check(report["schema_version"] == 2, "report schema")
    check(report["source_run_id"] == RUN and research["run_id"] == RUN, "run ID")
    check(report["source_code_revision"] == SOURCE_REV, "V0 source revision")
    check(report["source_manifest_sha256"] == SOURCE_MANIFEST, "V0 source manifest")
    check(report["v0_normalized_sha256"] == V0_NORMALIZED, "V0 normalized hash")
    check(report["v0_state_sha256"] == V0_STATE, "V0 state hash")
    check(digest(directory / "v0" / "normalized.jsonl") == V0_NORMALIZED,
          "actual V0 normalized file hash")
    check(digest(directory / "v0" / "state.jsonl") == V0_STATE,
          "actual V0 state file hash")
    check(report["v1_intent_sha256"] == V1_INTENTS, "frozen V1 intent hash")
    check(report["full_committed_prefix"] is True, "not a full committed prefix")
    check(inp["valid_ordinal"] == inp["committed_ordinal"] == ORDINAL, "raw ordinals")
    check(inp["uncommitted_events"] == 0 and inp["incomplete"] is False, "raw incomplete")
    check(int(report["processed_ordinal"]) == ORDINAL, "processed ordinal")
    check(report["analysis_revision"] == ANALYSIS_REV, "analysis revision")
    intent_hash = digest(intents_path)
    check(report["intent_sha256"] == intent_hash, "actual intent hash")

    actions = collections.Counter()
    status = {name: collections.Counter() for name in FAMILIES}
    reasons = collections.Counter()
    disagreement = collections.Counter()
    opportunity_reasons = collections.Counter()
    eligible = 0
    last_ordinal = 0
    last_time = ""
    minutes = set()
    with intents_path.open("rb") as stream:
        for number, line in enumerate(stream, 1):
            intent = json.loads(line)
            ordinal = int(intent["as_of_ordinal"])
            when = intent["as_of_time"]
            baseline = intent["v1_baseline"]
            check(ordinal > last_ordinal and when >= last_time, f"intent order {number}")
            check(intent["schema_version"] == 2, f"intent schema {number}")
            check(intent["decision_id"] == f"{RUN}:{ordinal}:v2", f"decision ID {number}")
            check(intent["run_id"] == RUN and baseline["run_id"] == RUN, f"intent run {number}")
            check(int(baseline["as_of_ordinal"]) == ordinal and baseline["as_of_time"] == when,
                  f"V1/V2 as-of {number}")
            check(baseline["decision_id"] == f"{RUN}:{ordinal}:v1", f"V1 ID {number}")
            check(intent["config_sha256"] == research["config_sha256"], f"config {number}")
            check(intent["horizon_seconds"] == 300, f"horizon {number}")
            check(int(baseline["quote_as_of_ordinal"]) <= ordinal, f"future quote {number}")
            check(len(intent["signals"]) == 3, f"family count {number}")
            check(when[:16] not in minutes, f"duplicate minute {number}")
            minutes.add(when[:16])
            actions[intent["action"]] += 1
            eligible += bool(baseline["eligible"])
            for reason in intent["reason_codes"]:
                reasons[reason] += 1
            disagreement[intent["evidence"]["disagreement"]] += 1
            if intent["opportunity"]["reason"]:
                opportunity_reasons[intent["opportunity"]["reason"]] += 1
            observed = collections.Counter()
            for expected, signal in zip(FAMILIES, intent["signals"]):
                check(signal["id"] == expected and signal["status"] in STATUSES,
                      f"family identity/status {number}")
                check(int(signal["as_of_ordinal"]) == ordinal, f"signal as-of {number}")
                check(int(signal["source_book_ordinal"]) == int(baseline["quote_as_of_ordinal"]),
                      f"book provenance {number}")
                check(int(signal["source_book_ordinal"]) <= ordinal, f"future source {number}")
                check(signal["generation"] == baseline["quote_generation"],
                      f"generation provenance {number}")
                check(signal["horizon_seconds"] == (30 if expected == FAMILIES[2] else 300),
                      f"signal horizon {number}")
                if signal["status"] == "ACTIVE":
                    check(signal["direction"] in ("LONG", "SHORT"), f"active direction {number}")
                else:
                    check(signal["direction"] == "FLAT", f"inactive direction {number}")
                observed[signal["status"]] += 1
                status[expected][signal["status"]] += 1
            evidence = intent["evidence"]
            for field, kind in (("active_families", "ACTIVE"), ("neutral_families", "NEUTRAL"),
                                ("unavailable_families", "UNAVAILABLE"), ("invalid_families", "INVALID")):
                check(evidence[field] == observed[kind], f"evidence counts {number}")
            if baseline["regime"] == "WIDE":
                check(observed["UNAVAILABLE"] == 3 and intent["action"] == "NO_TRADE",
                      f"wide regime {number}")
            if intent["action"] in ("LONG", "SHORT"):
                opportunity = intent["opportunity"]
                check(baseline["eligible"] and baseline["health"] == "HEALTHY",
                      f"unhealthy action {number}")
                check(evidence["reason"] == "CONSENSUS" and evidence["direction"] == intent["action"]
                      and evidence["disagreement"] == "0", f"unsupported action {number}")
                check(opportunity["available"] and opportunity["passes_screen"],
                      f"unpriced action {number}")
                check(opportunity["quote_age_ms"] <= 2000, f"stale action {number}")
                check(opportunity["past_move_proxy_micro_bps"] >
                      opportunity["friction_micro_bps"] + opportunity["margin_micro_bps"],
                      f"cost screen {number}")
                check(intent["action"] != "SHORT", f"unsupported spot short {number}")
            last_ordinal, last_time = ordinal, when

    decisions = number
    check(decisions == evaluator["decisions"] == research["raw_actions"].get("LONG", 0) +
          research["raw_actions"].get("SHORT", 0) + research["raw_actions"].get("NO_TRADE", 0),
          "decision counts")
    check(decisions == evaluator["calendar_minutes"] == len(minutes), "calendar minutes")
    check(eligible == evaluator["eligible_decisions"] == research["pressure_30_eligible"],
          "eligibility counts")
    check(dict(actions) == research["raw_actions"], "action counts")
    check(dict(reasons) == research["reasons"], "reason counts")
    check(dict(disagreement) == research["disagreement"], "disagreement counts")
    check(dict(opportunity_reasons) == research["opportunity_reasons"], "opportunity counts")
    for family in FAMILIES:
        check(dict(status[family]) == research["signal_status"][family], f"status counts {family}")
        check(sum(status[family].values()) == decisions, f"missing family {family}")
    paired = evaluator["paired_episodes"]
    check(paired + sum(evaluator["censored"].values()) == eligible, "five-minute coverage")
    pressure_paired = research["pressure_30_paired"]
    check(pressure_paired + sum(research["pressure_30_censored"].values()) == eligible,
          "30-second coverage")
    check(research["observer_mismatches"] == 0, "paired observer mismatch")
    check(research["frozen_v1_policy"]["episodes"] == paired, "V1 paired population")
    frozen = json.loads((Path(__file__).resolve().parents[1] / "docs" / "evidence" /
                         "v1" / "full-eval-1-report.json").read_bytes())["research"]
    check(evaluator["eligible_decisions"] == frozen["eligible_decisions"] and
          evaluator["paired_episodes"] == frozen["paired_episodes"] and
          evaluator["censored"] == frozen["censored"], "frozen V1 population")
    for comparator in ("momentum", "mean_reversion", "always_long", "no_trade"):
        check(evaluator["comparators"][comparator] == frozen["comparators"][comparator],
              f"frozen V1 comparator {comparator}")
    check(research["frozen_v1_policy"] == frozen["comparators"]["policy"],
          "frozen V1 policy result")
    check(research["cohort_policy"]["all_paired"]["episodes"] == paired,
          "V2 paired cohorts")
    for row in research["family_hypothetical"].values():
        check(row["episodes"] == paired, "five-minute family coverage")
    for row in research["pressure_30_controls"].values():
        check(row["episodes"] == pressure_paired, "30-second control coverage")
    check(sum(fold["policy"]["episodes"] for fold in evaluator["fold_comparators"].values()) == paired,
          "chronological folds")
    return {"commit": report["analysis_revision"], "report_sha256": digest(report_path),
            "intent_sha256": intent_hash, "decisions": decisions, "eligible": eligible,
            "paired_300s": paired, "paired_30s": pressure_paired,
            "actions": dict(actions), "signal_status": {key: dict(value) for key, value in status.items()},
            "reasons": dict(reasons), "disagreement": dict(disagreement)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("first", type=Path)
    parser.add_argument("second", type=Path)
    args = parser.parse_args()
    first = audit(args.first)
    second = audit(args.second)
    check(first == second, "independent full replay summary/hash divergence")
    print(json.dumps(first, sort_keys=True, indent=2))
    print("V2 FULL REPLAY AUDIT PASSED")


if __name__ == "__main__":
    main()

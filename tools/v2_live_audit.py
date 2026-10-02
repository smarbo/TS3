#!/usr/bin/env python3
"""Audit one complete public V2 smoke against its exact raw replay."""

import argparse
import hashlib
import itertools
import json
from pathlib import Path


def require(ok, message):
    if not ok:
        raise AssertionError(message)


def sha256(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def lines(path):
    with path.open("rb") as stream:
        for line in stream:
            yield json.loads(line)


def audit(run_dir, replay_dir, revision):
    run_id = run_dir.name
    data_dir = run_dir.parent
    run = json.loads((run_dir / "run-report.json").read_bytes())
    live = json.loads((run_dir / "v2-report.json").read_bytes())
    manifest_path = data_dir / f"{run_id}.manifest.json"
    manifest = json.loads(manifest_path.read_bytes())
    replay = json.loads((replay_dir / "report.json").read_bytes())
    journal = live["journal"]
    raw = replay["input"]

    require(run["run_id"] == live["journal"]["run_id"] == manifest["run_id"] ==
            replay["source_run_id"] == run_id, "run ID")
    require(run["status"] == live["status"] == "COMPLETE", "run/report incomplete")
    require(run["runtime_seconds"] >= 3600 and run["latest_confirmed_healthy_at"],
            "smoke too short or never healthy")
    require(run["code_revision"] == live["code_revision"] ==
            manifest["provenance"]["code_revision"] == replay["source_code_revision"] ==
            replay["analysis_revision"] == revision, "source/analysis revision")
    require(manifest["clean"] is True and replay["full_committed_prefix"] is True,
            "unclean manifest/replay")
    require(replay["source_manifest_sha256"] == sha256(manifest_path), "manifest hash")
    ordinal = run["committed_ordinal"]
    require(ordinal > 0 and ordinal == run["applied_ordinal"] == run["published_ordinal"] ==
            manifest["committed_ordinal"] == raw["valid_ordinal"] ==
            raw["committed_ordinal"] == int(replay["processed_ordinal"]), "high-water marks")
    require(raw["uncommitted_events"] == 0 and raw["incomplete"] is False,
            "raw suffix")
    require(run["capture_gaps"] == run["parse_data_quality_incidents"] ==
            run["stale_feed_incidents"] == 0, "unexplained source incident")
    require(run["event_counts"].get("SHUTDOWN") == 1 and
            run["event_counts"].get("DISCONNECTED") >= 1,
            "missing ordered shutdown markers")
    require(replay["v0_normalized_sha256"] == sha256(run_dir / "normalized.jsonl"),
            "normalized live/replay divergence")
    require(replay["v0_state_sha256"] == sha256(run_dir / "state.jsonl"),
            "state live/replay divergence")
    recent_states = []
    for state in lines(run_dir / "state.jsonl"):
        recent_states.append(state)
        if len(recent_states) > 2:
            recent_states.pop(0)
    require(len(recent_states) == 2 and
            int(recent_states[-1]["ordinal"]) == ordinal and
            recent_states[-1]["health"] == "UNHEALTHY" and
            recent_states[-2]["health"] == "UNHEALTHY",
            "shutdown health/order")

    live_intents = run_dir / "v2-intents.jsonl"
    replay_intents = replay_dir / "intents.jsonl"
    intent_hash = sha256(live_intents)
    require(intent_hash == sha256(replay_intents) == journal["intent_sha256"] ==
            replay["intent_sha256"], "V2 intent bytes diverged")
    require(journal["config_sha256"] == replay["research"]["config_sha256"],
            "config mismatch")

    actions = {}
    last_ordinal = 0
    last_time = ""
    warmed = 0
    count = 0
    for count, intent in enumerate(lines(live_intents), 1):
        at = int(intent["as_of_ordinal"])
        when = intent["as_of_time"]
        require(at > last_ordinal and when >= last_time and at <= ordinal,
                f"intent order {count}")
        require(intent["decision_id"] == f"{run_id}:{at}:v2" and
                intent["config_sha256"] == journal["config_sha256"],
                f"intent provenance {count}")
        if intent["action"] != "NO_TRADE":
            require(intent["v1_baseline"]["health"] == "HEALTHY" and
                    intent["v1_baseline"]["eligible"], f"unhealthy action {count}")
        actions[intent["action"]] = actions.get(intent["action"], 0) + 1
        warmed += bool(intent["v1_baseline"]["eligible"] and
                       intent["v1_baseline"].get("features") is not None and
                       intent["signals"][2]["status"] in ("ACTIVE", "NEUTRAL"))
        last_ordinal, last_time = at, when
    require(count > 16 and count == journal["intents"] == journal["acknowledged"] ==
            replay["research"]["paired_evaluator"]["decisions"], "decision/ack counts")
    require(actions == journal["actions"] == replay["research"]["raw_actions"],
            "action counts")
    require(last_ordinal == int(journal["last_intent_ordinal"]), "last intent")
    require(warmed > 0, "no warmed healthy V2 decision")

    ack_count = 0
    for ack_count, (intent, ack) in enumerate(itertools.zip_longest(
            lines(live_intents), lines(run_dir / "v2-live-acks.jsonl")), 1):
        require(ack_count <= count and intent is not None and ack is not None,
                "missing/extra acknowledgment")
        require(ack["decision_id"] == intent["decision_id"] and
                ack["as_of_ordinal"] == intent["as_of_ordinal"] and
                ack["action"] == intent["action"] and
                ack["decision_ready_at"] <= ack["intent_durable_at"],
                f"ack timing/order {ack_count}")
        require(ack["action"] == "NO_TRADE" or ack["gate_open"] or
                ack.get("veto_reason") == "LIVE_GATE_CLOSED",
                f"unvetoed closed-gate action {ack_count}")
    require(ack_count == count, "missing acknowledgment")
    require(journal["decision_latency_samples"] > 0 and
            journal["decision_latency_max_ns"] > 0, "missing decision timing")
    require(journal["stage_latency"].get("book_pressure", {}).get("samples", 0) > 0,
            "missing book stage timing")
    return {"run_id": run_id, "revision": revision, "raw_ordinals": ordinal,
            "intents": count, "actions": actions, "intent_sha256": intent_hash,
            "normalized_sha256": replay["v0_normalized_sha256"],
            "state_sha256": replay["v0_state_sha256"],
            "reconnects": run["reconnects"], "gate_vetoes": journal["gate_vetoes"],
            "decision_latency_max_ns": journal["decision_latency_max_ns"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("run_dir", type=Path)
    parser.add_argument("replay_dir", type=Path)
    parser.add_argument("revision")
    args = parser.parse_args()
    print(json.dumps(audit(args.run_dir, args.replay_dir, args.revision),
                     sort_keys=True, indent=2))
    print("V2 LIVE/REPLAY AUDIT PASSED")


if __name__ == "__main__":
    main()

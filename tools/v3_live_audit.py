#!/usr/bin/env python3
"""Reconcile one complete public V3 shadow smoke with its exact raw replay."""

import argparse
import hashlib
import itertools
import json
from pathlib import Path


def require(ok, message):
    if not ok:
        raise ValueError(message)


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def lines(path):
    with path.open("rb") as stream:
        for line in stream:
            yield json.loads(line)


def audit(run_dir, replay_dir, artifact_path, revision):
    run_id = run_dir.name
    run = json.loads((run_dir / "run-report.json").read_bytes())
    live = json.loads((run_dir / "v3-report.json").read_bytes())
    v2 = json.loads((run_dir / "v2-report.json").read_bytes())
    manifest_path = run_dir.parent / f"{run_id}.manifest.json"
    manifest = json.loads(manifest_path.read_bytes())
    replay = json.loads((replay_dir / "report.json").read_bytes())
    artifact = json.loads(artifact_path.read_bytes())
    journal = live["journal"]
    raw = replay["input"]

    require(run["run_id"] == journal["run_id"] == manifest["run_id"] ==
            replay["run_id"] == run_id, "run identity")
    require(run["status"] == live["status"] == "COMPLETE", "incomplete smoke")
    require(run["runtime_seconds"] >= 1260 and run["latest_confirmed_healthy_at"],
            "smoke too short or never healthy")
    require(run["code_revision"] == live["code_revision"] ==
            manifest["provenance"]["code_revision"] == replay["source_code_revision"] ==
            replay["analysis_revision"] == revision, "code provenance")
    require(manifest["clean"] and replay["full_committed_prefix"], "unclean source")
    require(replay["source_manifest_sha256"] == sha256(manifest_path), "source hash")
    require(journal["artifact_sha256"] == replay["artifact_sha256"] == artifact["sha256"],
            "artifact identity")
    ordinal = run["committed_ordinal"]
    require(ordinal > 0 and ordinal == run["applied_ordinal"] == run["published_ordinal"] ==
            manifest["committed_ordinal"] == raw["valid_ordinal"] ==
            raw["committed_ordinal"] == int(replay["processed_ordinal"]), "ordinals")
    require(raw["uncommitted_events"] == 0 and not raw["incomplete"], "raw suffix")
    require(run["capture_gaps"] == run["parse_data_quality_incidents"] ==
            run["stale_feed_incidents"] == 0, "source incidents")
    require(replay["v0_normalized_sha256"] == sha256(run_dir / "normalized.jsonl") and
            replay["v0_state_sha256"] == sha256(run_dir / "state.jsonl"),
            "V0 live/replay divergence")

    live_intents = run_dir / "v3-intents.jsonl"
    digest = sha256(live_intents)
    require(digest == sha256(replay_dir / "intents.jsonl") ==
            journal["intent_sha256"] == replay["v3_intent_sha256"],
            "V3 intent divergence")
    require(sha256(run_dir / "v2-intents.jsonl") ==
            v2["journal"]["intent_sha256"] == replay["v2_intent_sha256"],
            "V2 baseline divergence")
    count = warmed = 0
    reasons = {}
    statuses = {}
    previous = 0
    for count, (intent, ack) in enumerate(itertools.zip_longest(
            lines(live_intents), lines(run_dir / "v3-live-acks.jsonl")), 1):
        require(intent is not None and ack is not None, "unpaired intent/ack")
        at = int(intent["as_of_ordinal"])
        require(previous < at <= ordinal and intent["decision_id"] == f"{run_id}:{at}:v3" and
                intent["model_artifact_sha256"] == artifact["sha256"] and
                intent["action"] == "NO_TRADE" and
                int(intent["v2_baseline"]["as_of_ordinal"]) == at,
                f"intent provenance or safety {count}")
        require(ack["decision_id"] == intent["decision_id"] and
                ack["as_of_ordinal"] == intent["as_of_ordinal"] and
                ack["action"] == "NO_TRADE" and
                ack["decision_ready_at"] <= ack["intent_durable_at"],
                f"acknowledgment {count}")
        require("calibrated_probability" not in intent and "expected_net_return" not in intent,
                "unsupported probability or return field")
        status = intent["model_status"]
        reason = intent["reason"]
        if status == "AVAILABLE_UNCALIBRATED":
            require(reason == "UNCALIBRATED_NO_EDGE" and
                    isinstance(intent.get("raw_logit_micro"), int),
                    "available score mislabeled")
        else:
            require("raw_logit_micro" not in intent, "unavailable model carried score")
        base = intent["v2_baseline"]["v1_baseline"]
        warmed += bool(base["eligible"] and base.get("features") is not None)
        reasons[reason] = reasons.get(reason, 0) + 1
        statuses[status] = statuses.get(status, 0) + 1
        previous = at
    require(count >= 20 and warmed > 0 and count == journal["intents"] ==
            journal["acknowledged"] == replay["intents"], "decision/ack count")
    require(previous == int(journal["last_intent_ordinal"]) and
            reasons == journal["reasons"] == replay["reasons"] and
            statuses == journal["model_status"], "journal count reconciliation")
    return {"run_id": run_id, "revision": revision, "raw_ordinals": ordinal,
            "intents": count, "warmed": warmed, "reasons": reasons,
            "model_status": statuses, "v3_intent_sha256": digest,
            "v2_intent_sha256": replay["v2_intent_sha256"],
            "v0_normalized_sha256": replay["v0_normalized_sha256"],
            "v0_state_sha256": replay["v0_state_sha256"],
            "reconnects": run["reconnects"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("run_dir", type=Path)
    parser.add_argument("replay_dir", type=Path)
    parser.add_argument("artifact", type=Path)
    parser.add_argument("revision")
    args = parser.parse_args()
    print(json.dumps(audit(args.run_dir, args.replay_dir, args.artifact, args.revision),
                     sort_keys=True, indent=2))
    print("V3 LIVE/REPLAY AUDIT PASSED")


if __name__ == "__main__":
    main()

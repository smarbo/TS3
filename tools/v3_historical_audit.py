#!/usr/bin/env python3
"""Audit a full V3 replay of the exposed accepted development recording."""

import argparse
import hashlib
import json
from pathlib import Path


RUN = "kraken-v0-server-20260927T203224Z"
SOURCE = "8144ec61b4da66aa0e392df5e96b140a25dbaef7bbc6abdd015ca92ebf5bd7dc"
V0_NORMALIZED = "06950e8b672a10949d97339c42fb294b72300544eb3eb64aba6f34e647e0e953"
V0_STATE = "200fb2785cdc278dce732d97a75162cb0b7fb2550e24ff45835b06cbc027f132"
V1 = "a0dde5ffab48f2e0624ec5b5580765124ea63fb8e2bf8a44d85bf39e9c597f5f"
V2 = "b43a39952c12348b7f340af79c2b36e0596f978a5709519d9c953bd6f55ef505"
ORDINALS = 12_316_574
INTENTS = 1_446
REVISION = "f6b182526dee4577632f0ae0d4f1c21685ab22c9"


def require(ok, message):
    if not ok:
        raise ValueError(message)


def file_digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def audit(replay_dir, artifact_path):
    report = json.loads((replay_dir / "report.json").read_bytes())
    artifact = json.loads(artifact_path.read_bytes())
    intents_path = replay_dir / "intents.jsonl"
    digest = file_digest(intents_path)
    raw = report["input"]
    require(report["run_id"] == artifact["training_run_id"] == RUN, "source run")
    require(report["analysis_revision"] == artifact["training_revision"] == REVISION and
            report["source_code_revision"] ==
            "d26241151331795cf5005ba09d5f176d1efb6b22", "code provenance")
    require(report["source_manifest_sha256"] == SOURCE, "source manifest")
    require(report["full_committed_prefix"] and not raw["incomplete"] and
            raw["uncommitted_events"] == 0, "incomplete replay")
    require(raw["valid_ordinal"] == raw["committed_ordinal"] ==
            int(report["processed_ordinal"]) == ORDINALS, "ordinal mismatch")
    require(report["v0_normalized_sha256"] == V0_NORMALIZED and
            report["v0_state_sha256"] == V0_STATE and
            report["v1_intent_sha256"] == V1 and
            report["v2_intent_sha256"] == V2, "accepted baseline divergence")
    require(file_digest(replay_dir / "v0" / "normalized.jsonl") == V0_NORMALIZED and
            file_digest(replay_dir / "v0" / "state.jsonl") == V0_STATE,
            "actual V0 output file divergence")
    require(report["artifact_sha256"] == artifact["sha256"], "artifact hash")
    require(report["v3_intent_sha256"] == digest, "V3 intent hash")
    require(report["intents"] == INTENTS and
            report["actions"] == {"NO_TRADE": INTENTS} and
            report["reasons"] == {"IN_SAMPLE_PERIOD": INTENTS},
            "historical action/reason counts")
    previous = 0
    count = 0
    with intents_path.open("rb") as stream:
        for line in stream:
            intent = json.loads(line)
            count += 1
            ordinal = int(intent["as_of_ordinal"])
            require(previous < ordinal <= ORDINALS and
                    intent["decision_id"] == f"{RUN}:{ordinal}:v3" and
                    intent["run_id"] == RUN, f"intent identity {count}")
            require(intent["model_artifact_sha256"] == artifact["sha256"] and
                    intent["model_status"] == "UNAVAILABLE" and
                    intent["reason"] == "IN_SAMPLE_PERIOD" and
                    intent["action"] == "NO_TRADE" and
                    "raw_logit_micro" not in intent and
                    "calibrated_probability" not in intent and
                    "expected_net_return" not in intent,
                    f"in-sample information barrier {count}")
            previous = ordinal
    require(count == INTENTS, "intent row count")
    return {"run_id": RUN, "ordinals": ORDINALS, "intents": count,
            "artifact_sha256": artifact["sha256"],
            "v3_intent_sha256": digest, "analysis_revision": report["analysis_revision"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("replay_dir", type=Path)
    parser.add_argument("artifact", type=Path)
    args = parser.parse_args()
    print(json.dumps(audit(args.replay_dir, args.artifact), sort_keys=True, indent=2))
    print("V3 HISTORICAL IN-SAMPLE AUDIT PASSED")


if __name__ == "__main__":
    main()

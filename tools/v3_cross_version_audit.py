#!/usr/bin/env python3
"""Verify an inference-only V3 code change preserves canonical replay output."""

import argparse
import hashlib
import json
from pathlib import Path


FIELDS = ("v0_normalized_sha256", "v0_state_sha256", "v1_intent_sha256",
          "v2_intent_sha256", "v3_intent_sha256", "processed_ordinal",
          "full_committed_prefix", "artifact_sha256")


def audit(before, after, revision):
    left = json.loads((before / "report.json").read_bytes())
    right = json.loads((after / "report.json").read_bytes())
    if right["analysis_revision"] != revision or not right["full_committed_prefix"]:
        raise ValueError("corrected replay provenance or committed prefix")
    for key in FIELDS:
        if left[key] != right[key]:
            raise ValueError(f"cross-version {key} divergence")
    intents_before = (before / "intents.jsonl").read_bytes()
    intents_after = (after / "intents.jsonl").read_bytes()
    if intents_before != intents_after:
        raise ValueError("cross-version V3 intent bytes diverge")
    digest = hashlib.sha256(intents_after).hexdigest()
    if digest != right["v3_intent_sha256"]:
        raise ValueError("cross-version V3 digest mismatch")
    return {"before_revision": left["analysis_revision"],
            "after_revision": right["analysis_revision"],
            "processed_ordinal": right["processed_ordinal"],
            "v3_intent_sha256": digest}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    parser.add_argument("expected_after_revision")
    args = parser.parse_args()
    print(json.dumps(audit(args.before, args.after, args.expected_after_revision),
                     indent=2, sort_keys=True))
    print("V3 CROSS-VERSION INFERENCE AUDIT PASSED")


if __name__ == "__main__":
    main()

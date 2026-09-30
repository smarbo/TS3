#!/usr/bin/env python3
"""Verify that committed V2 evidence retains the original archived bytes."""

import hashlib
import json
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = ROOT / "docs" / "evidence" / "v2"


def main():
    manifest = json.loads((EVIDENCE / "evidence-manifest.json").read_text())
    for name, expected in manifest["archived_sha256"].items():
        relative = f"docs/evidence/v2/{name}"
        working = hashlib.sha256((EVIDENCE / name).read_bytes()).hexdigest()
        staged = subprocess.run(
            ["git", "show", f":{relative}"], cwd=ROOT, check=True,
            capture_output=True,
        )
        committed = hashlib.sha256(staged.stdout).hexdigest()
        if working != expected or committed != expected:
            raise ValueError(
                f"{name}: expected {expected}, worktree {working}, index {committed}"
            )
    print(f"Verified {len(manifest['archived_sha256'])} archived files in worktree and Git index")


if __name__ == "__main__":
    main()

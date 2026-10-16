#!/usr/bin/env python3
"""Verify archived V3 sidecar bytes against their local evidence manifest."""

import argparse
import hashlib
import json
from pathlib import Path


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('archive', type=Path)
    parser.add_argument('--require-complete', action='store_true')
    args = parser.parse_args()
    manifest = json.loads((args.archive / 'evidence-manifest.json').read_bytes())
    pending = manifest.get('pending_evidence_files', [])
    if args.require_complete and pending:
        raise ValueError(f'evidence still pending: {pending}')
    listed = manifest['archived_sha256']
    present = {p.name for p in args.archive.iterdir()
               if p.is_file() and p.name != 'evidence-manifest.json'}
    if present != set(listed):
        raise ValueError(f'unlisted or missing archive files: {present ^ set(listed)}')
    for name, expected in listed.items():
        actual = digest(args.archive / name)
        if actual != expected:
            raise ValueError(f'{name}: SHA-256 mismatch')
    print(f'V3 EVIDENCE MANIFEST PASSED: {len(listed)} files, {len(pending)} pending')


if __name__ == '__main__':
    main()

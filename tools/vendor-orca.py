#!/usr/bin/env python3
"""Import the pinned Orca reference files, or verify their preserved bytes."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
DEST = ROOT / 'third_party/orca'
MANIFEST = DEST / 'manifest.json'
REVISION = '2e40b990b8a70f0d4bb987be2fb6b51a38e477bc'
FILES = [
    'LICENSE',
    'mobile/src/terminal/terminal-gesture-input.ts',
    'mobile/src/terminal/terminal-gesture-input.test.ts',
    'mobile/src/terminal/document/mouse-input-encoding.ts',
    'mobile/src/terminal/document/mouse-report-cell.ts',
    'mobile/src/terminal/terminal-mouse-authority.test.ts',
    'mobile/modules/orca-mobile-web-shell/android/src/main/java/expo/modules/orcamobilewebshell/MobileWebShellOrigin.kt',
    'mobile/modules/orca-mobile-web-shell/android/src/main/java/expo/modules/orcamobilewebshell/MobileWebShellCsp.kt',
    'mobile/modules/orca-mobile-web-shell/android/src/main/java/expo/modules/orcamobilewebshell/MobileWebShellResponseHeaders.kt',
    'mobile/modules/orca-mobile-web-shell/android/src/main/java/expo/modules/orcamobilewebshell/MobileWebShellNavigationPolicy.kt',
]


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--import-from', type=Path, help='explicitly copy files from this local git checkout')
    args = parser.parse_args()
    if args.import_from:
        records = []
        # Read the commit objects, never potentially modified working files.
        contents = [(name, subprocess.check_output(
            ['git', '-C', str(args.import_from), 'show', f'{REVISION}:{name}'])) for name in FILES]
        for name, data in contents:
            destination = DEST / 'upstream' / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(data)
            records.append({'path': name, 'sha256': digest(data)})
        MANIFEST.write_text(json.dumps({
            'repository': 'https://github.com/stablyai/orca',
            'revision': REVISION,
            'source': 'local sibling checkout git objects; origin URL verified during import',
            'files': records,
        }, indent=2) + '\n')
    manifest = json.loads(MANIFEST.read_text())
    if manifest['revision'] != REVISION or [entry['path'] for entry in manifest['files']] != FILES:
        raise SystemExit('Orca manifest differs from the pinned import recipe')
    for entry in manifest['files']:
        if digest((DEST / 'upstream' / entry['path']).read_bytes()) != entry['sha256']:
            raise SystemExit(f"modified upstream snapshot: {entry['path']}")
    print(f"verified {len(FILES)} Orca files at {REVISION}")


if __name__ == '__main__':
    main()

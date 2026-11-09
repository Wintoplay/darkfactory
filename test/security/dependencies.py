#!/usr/bin/env python3
"""Query OSV for pinned Java crypto and npm assets, including browser test tools.

This is a development check. Nothing in the daemon invokes it.
"""
import argparse
import json
from pathlib import Path
import re
import sys
import urllib.request

ROOT = Path(__file__).resolve().parents[2]


def inventory(runtime_manifest=None):
    packages = set()
    # Read the coordinates actually used by the Android build.
    gradle = (ROOT / 'android/app/build.gradle').read_text()
    for group, artifact, version in re.findall(
        r"(?:implementation|api)\s*\(?['\"]([^:'\"]+):([^:'\"]+):([^'\"]+)['\"]", gradle
    ):
        packages.add(('Maven', group + ':' + artifact, version))
    if not any(name.startswith('org.bouncycastle:') for _, name, _ in packages):
        raise ValueError('Android crypto dependency missing from inventory')
    kotlin = re.search(r"org.jetbrains.kotlin:kotlin-gradle-plugin:([^'\"]+)",
                       (ROOT / 'android/build.gradle').read_text())
    if not kotlin:
        raise ValueError('Kotlin compiler version missing from inventory')
    packages.add(('Maven', 'org.jetbrains.kotlin:kotlin-stdlib', kotlin.group(1)))
    # These package versions identify the vendored runtime terminal assets.
    notices = (ROOT / 'third_party/README.md').read_text()
    for name, version in re.findall(r'^## (@xterm/\S+) (\d+\.\d+\.\d+)\s*$', notices, re.M):
        packages.add(('npm', name, version))
    if len([name for _, name, _ in packages if name.startswith('@xterm/')]) != 3:
        raise ValueError('Expected all three vendored xterm packages')
    lock = json.loads((ROOT / 'tools/clientbench/package-lock.json').read_text())
    for path, entry in lock['packages'].items():
        if not path or 'node_modules/' not in path:
            continue
        name = entry.get('name') or path.rsplit('node_modules/', 1)[1]
        packages.add(('npm', name, entry['version']))
    if runtime_manifest:
        for query in json.loads(Path(runtime_manifest).read_text()):
            package = query['package']
            if package['ecosystem'] != 'Maven' or not package['name'] or not query['version']:
                raise ValueError('Invalid Android runtime inventory')
            packages.add(('Maven', package['name'], query['version']))
    return [{'package': {'ecosystem': ecosystem, 'name': name}, 'version': version}
            for ecosystem, name, version in sorted(packages)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inventory', action='store_true', help='print inventory without a network call')
    parser.add_argument('--android-runtime', help='fresh Gradle resolved runtime inventory JSON')
    args = parser.parse_args()
    queries = inventory(args.android_runtime)
    if args.inventory:
        print(json.dumps(queries, indent=2))
        return 0
    request = urllib.request.Request('https://api.osv.dev/v1/querybatch',
                                     json.dumps({'queries': queries}).encode(),
                                     {'Content-Type': 'application/json'}, method='POST')
    with urllib.request.urlopen(request, timeout=60) as response:
        results = json.load(response)['results']
    if len(results) != len(queries):
        raise ValueError('OSV returned an incomplete response')
    found = False
    for query, result in zip(queries, results):
        if not isinstance(result, dict) or set(result) - {'vulns'}:
            raise ValueError('OSV returned an error or unsupported result')
        vulns = result.get('vulns', [])
        if vulns:
            found = True
            print(f"{query['package']['name']} {query['version']}: " +
                  ', '.join(vuln['id'] for vuln in vulns))
    if not found:
        print(f'OSV: no known advisories for {len(queries)} pinned packages')
    return 1 if found else 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception as error:
        print(f'dependency scan failed: {error}', file=sys.stderr)
        sys.exit(2)

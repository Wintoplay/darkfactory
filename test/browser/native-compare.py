#!/usr/bin/env python3
"""Alternating isolated WebKitGTK real-PTY echo trials."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

HERE = Path(__file__).resolve().parent
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--baseline', type=Path, required=True)
p.add_argument('--current', type=Path, required=True)
p.add_argument('--out', type=Path, required=True)
p.add_argument('--trials', type=int, default=5)
p.add_argument('--opencode', action='store_true')
a = p.parse_args()
if a.trials < 1:
    p.error('trials must be positive')
a.out.mkdir(parents=True, exist_ok=False)
binaries = {'baseline': a.baseline.resolve(), 'current': a.current.resolve()}
record = {'trialsPerSide': a.trials, 'fixture': 'opencode' if a.opencode else 'bash',
          'binaries': {name: hashlib.sha256(path.read_bytes()).hexdigest() for name, path in binaries.items()},
          'sources': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in
                      (HERE / 'native-echo.sh', HERE / 'native-echo.py', HERE / 'native-echo.js', HERE / 'fixture-opencode.sh')},
          'runs': []}
for i in range(a.trials):
    for side in (('baseline', 'current') if i % 2 == 0 else ('current', 'baseline')):
        env = os.environ.copy()
        env['DFB_BIN'] = str(binaries[side])
        if a.opencode:
            env['DFB_FIXTURE'] = str(HERE / 'fixture-opencode.sh')
        else:
            env.pop('DFB_FIXTURE', None)
        proc = subprocess.run([str(HERE / 'native-echo.sh')], env=env, text=True, capture_output=True)
        entry = {'trial': i + 1, 'side': side, 'exit': proc.returncode, 'stderr': proc.stderr,
                 'result': None}
        try:
            entry['result'] = json.loads(proc.stdout)
        except ValueError:
            entry['stdout'] = proc.stdout
        record['runs'].append(entry)
        (a.out / 'results.json').write_text(json.dumps(record, indent=2) + '\n')
        print(f'{i+1} {side}: exit {proc.returncode}', flush=True)
        if proc.returncode or not entry['result'] or not entry['result'].get('ok'):
            sys.exit(1)

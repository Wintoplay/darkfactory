#!/usr/bin/env python3
"""Run alternating isolated Wails terminal fixture trials; never connect to a daemon."""
import argparse
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--fixture', type=Path, required=True)
p.add_argument('--ghostty', type=Path, required=True)
p.add_argument('--desktop', type=Path, required=True)
p.add_argument('--out', type=Path, required=True)
p.add_argument('--trials', type=int, default=5)
a = p.parse_args()
if a.trials < 1:
    p.error('trials must be positive')
a.out.mkdir(parents=True, exist_ok=False)
env = os.environ.copy()
for name in ('CONFIG', 'DATA', 'CACHE'):
    path = a.out.resolve() / 'xdg' / name.lower()
    path.mkdir(parents=True)
    env['XDG_' + name + '_HOME'] = str(path)
for i in range(a.trials):
    for side in (('xterm', 'ghostty') if i % 2 == 0 else ('ghostty', 'xterm')):
        url = '{url}' + ('ghostty' if side == 'ghostty' else 'compare')
        cmd = [sys.executable, str(ROOT / 'tools/clientbench/run.py'), '--fixture', str(a.fixture.resolve()),
               '--ghostty', str(a.ghostty.resolve()), '--label', 'wails', '--out', str(a.out / f'{i+1:02d}-{side}'),
               '--timeout', '60', '--hold', '0', '--', str(a.desktop.resolve()), '--autostart=false', '--url', url]
        print(' '.join(cmd), flush=True)
        result = subprocess.run(cmd, env=env, cwd=ROOT)
        if result.returncode:
            sys.exit(result.returncode)

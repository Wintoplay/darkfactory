#!/usr/bin/env python3
"""Repeat native Ghostty and Alacritty probes on shared disposable PTYs."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

here = Path(__file__).resolve().parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out', required=True, type=Path)
parser.add_argument('--daemon', required=True, type=Path)
parser.add_argument('--pairs', type=int, default=5)
args = parser.parse_args()
if args.pairs < 1:
    parser.error('pairs must be positive')
args.out.mkdir(parents=True, exist_ok=False)
files = [here/'rust-pair.sh', here/'ghostty-bridge.py', here/'alacritty-bridge.py',
         here.parent/'browser/lib.sh']
manifest = {'daemonSha256':hashlib.sha256(args.daemon.read_bytes()).hexdigest(),
            'sources':{str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in files},
            'trials':[]}
for i in range(args.pairs):
    order = 'native-first' if i % 2 == 0 else 'alacritty-first'
    out = args.out / f'{i+1:02d}'
    env = os.environ.copy()
    env.update(DFB_BIN=str(args.daemon.resolve()), DFB_OUT=str(out.resolve()), DFB_ORDER=order)
    proc = subprocess.run([str(here/'rust-pair.sh')], env=env, text=True, capture_output=True)
    manifest['trials'].append({'trial':i+1, 'order':order, 'exit':proc.returncode,
                               'stdout':proc.stdout, 'stderr':proc.stderr})
    (args.out/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
    print(f'pair {i+1} {order}: exit {proc.returncode}', flush=True)

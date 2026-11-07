#!/usr/bin/env python3
"""Run one isolated Linux client trial and preserve raw observations."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import shutil
import subprocess
import time
import urllib.request


ROOT = Path(__file__).resolve().parents[2]


def process_memory(root_pid):
    """Sum PSS, not RSS, over this launcher's current process descendants."""
    parents = {}
    for entry in Path('/proc').glob('[0-9]*/status'):
        try:
            fields = dict(line.split(':', 1) for line in entry.read_text().splitlines() if ':' in line)
            parents[int(entry.parent.name)] = int(fields['PPid'])
        except (OSError, ValueError, KeyError):
            continue
    included = {root_pid}
    while True:
        children = {pid for pid, parent in parents.items() if parent in included}
        if children <= included:
            break
        included |= children
    total = 0
    unreadable = []
    for pid in sorted(included):
        try:
            lines = Path(f'/proc/{pid}/smaps_rollup').read_text().splitlines()
            total += next(int(line.split()[1]) for line in lines if line.startswith('Pss:'))
        except (OSError, StopIteration):
            unreadable.append(pid)
    return {'pssKiB': total, 'pids': sorted(included), 'unreadablePids': unreadable}


def stop(process):
    if process is not None:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fixture', required=True, type=Path, help='prebuilt clientbench binary')
    parser.add_argument('--ghostty', type=Path, help='unpacked ghostty-web dist directory')
    parser.add_argument('--label', required=True, choices=['browser', 'wails'])
    parser.add_argument('--out', required=True, type=Path, help='new result directory')
    parser.add_argument('--timeout', type=float, default=45)
    parser.add_argument('--hold', type=float, default=3, help='idle seconds after workload completion')
    parser.add_argument('client', nargs=argparse.REMAINDER, help='command after --, containing {url}')
    args = parser.parse_args()
    command = args.client[1:] if args.client[:1] == ['--'] else args.client
    if not command or not any('{url}' in part for part in command):
        parser.error('client argv must include {url}')
    if args.timeout <= 0 or args.hold < 0:
        parser.error('timeout must be positive and hold must be nonnegative')
    if platform.system() != 'Linux':
        parser.error('this runner measures Linux /proc process trees')
    args.out.mkdir(parents=True, exist_ok=False)
    executable = shutil.which(command[0])
    fixture = client = None
    report = {
        'schema': 1, 'label': args.label, 'verdict': 'incomplete',
        'synthetic': True, 'platform': platform.platform(),
        'commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
        'diffSha256': hashlib.sha256(subprocess.check_output(['git', 'diff', 'HEAD'], cwd=ROOT)).hexdigest(),
        'fixtureSha256': hashlib.sha256(args.fixture.read_bytes()).hexdigest(),
        'clientExecutableSha256': hashlib.sha256(Path(executable).read_bytes()).hexdigest() if executable else None,
        'overrides': {key: os.environ[key] for key in
                      ['DF_BENCH_HEADLESS', 'DF_CHROMIUM', 'DF_PLAYWRIGHT_MODULE', 'DF_DPR'] if key in os.environ},
        'sources': {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest()
                    for base in [ROOT / 'tools/clientbench', ROOT / 'desktop']
                    for path in sorted(base.rglob('*')) if path.is_file()
                    and 'node_modules' not in path.parts and '__pycache__' not in path.parts},
        'commandTemplate': command, 'samples': [], 'errors': [],
        'ghosttyAssets': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(args.ghostty.iterdir()) if p.is_file()} if args.ghostty else None,
        'assets': {name: hashlib.sha256((ROOT / 'web/vendor' / name).read_bytes()).hexdigest()
                   for name in ['xterm.js', 'xterm.css']},
    }
    try:
        with (args.out / 'fixture.log').open('w') as fixture_log, (args.out / 'client.log').open('w') as client_log:
            fixture_args = [str(args.fixture.resolve()), '-assets', str(ROOT / 'web/vendor')]
            if args.ghostty:
                fixture_args += ['-ghostty', str(args.ghostty.resolve())]
            fixture = subprocess.Popen(fixture_args,
                                       stdout=subprocess.PIPE, stderr=fixture_log, text=True, start_new_session=True)
            url = fixture.stdout.readline().strip()
            if not url.startswith('http://127.0.0.1:'):
                raise RuntimeError('fixture did not provide its loopback URL')
            argv = [part.replace('{url}', url) for part in command]
            start = time.monotonic()
            client = subprocess.Popen(argv, stdout=client_log, stderr=client_log, start_new_session=True)
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            result = None
            while time.monotonic() - start < args.timeout:
                if client.poll() is not None:
                    raise RuntimeError(f'client exited before completion: {client.returncode}')
                with opener.open(url + 'result', timeout=2) as response:
                    result = json.load(response)
                report['samples'].append({'elapsedSeconds': time.monotonic() - start,
                                          **process_memory(client.pid)})
                if result is not None:
                    break
                time.sleep(0.1)
            if result is None:
                raise RuntimeError('workload timed out')
            report['launchToResultSeconds'] = time.monotonic() - start
            report['workload'] = result
            if result.get('errors') or result.get('sentinel') != 'DARKFACTORY_BENCH_DONE' or result.get('bytes') != 1049600 or len(result.get('echoWriteMs', [])) != 30:
                raise RuntimeError('workload failed its output or completion checks')
            render_ms = result.get('renderMs')
            if isinstance(render_ms, bool) or not isinstance(render_ms, (int, float)) or render_ms < 0:
                raise RuntimeError('workload did not report a paint interval')
            if not isinstance(result.get('renderer'), str) or not result.get('renderer'):
                raise RuntimeError('workload did not report a renderer')
            deadline = time.monotonic() + args.hold
            while time.monotonic() < deadline:
                if client.poll() is not None:
                    raise RuntimeError('client exited during idle observation')
                report['samples'].append({'elapsedSeconds': time.monotonic() - start,
                                          **process_memory(client.pid)})
                time.sleep(0.1)
            report['verdict'] = 'smoke_passed'
    except Exception as error:
        report['errors'].append(str(error))
    finally:
        stop(client)
        stop(fixture)
        (args.out / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
    print(f"{report['verdict']}: {args.out / 'result.json'}")
    return 0 if report['verdict'] == 'smoke_passed' else 1


if __name__ == '__main__':
    raise SystemExit(main())

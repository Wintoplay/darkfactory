#!/usr/bin/env python3
"""Exercise pairing confirmation in a fresh, disposable Android emulator.

Requires a rebuilt bin/darkfactory and debug APK. Refuses a device that already
has DarkFactory installed; never resets an existing app or operator daemon.
"""
import argparse
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import tempfile
import threading
import time
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = 'dev.darkfactory.mobile'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', required=True)
    parser.add_argument('--adb', default=str(Path.home() / 'Android/Sdk/platform-tools/adb'))
    args = parser.parse_args()
    if not re.fullmatch(r'emulator-\d+', args.serial):
        raise SystemExit('Only a disposable emulator is accepted')

    def adb(*command):
        try:
            return subprocess.check_output([args.adb, '-s', args.serial, *command], text=True,
                                           stderr=subprocess.STDOUT)
        except subprocess.CalledProcessError:
            # am's URI and UI dumps can contain the one-time code.
            raise RuntimeError('An ADB test action failed') from None

    if PACKAGE in adb('shell', 'pm', 'list', 'packages', PACKAGE):
        raise SystemExit('Refusing an emulator with an existing DarkFactory install')
    apk = ROOT / 'android/app/build/outputs/apk/debug/app-debug.apk'
    binary = ROOT / 'bin/darkfactory'
    with tempfile.TemporaryDirectory(prefix='df-android-security-') as temporary:
        work = Path(temporary)
        env = os.environ.copy()
        for name in ['CONFIG', 'STATE', 'DATA', 'CACHE']:
            env[f'XDG_{name}_HOME'] = str(work / name.lower())
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        base = f'http://127.0.0.1:{port}'
        token = subprocess.check_output([str(binary), 'token'], env=env, text=True).strip()

        def api(path, body=None):
            headers = {'Authorization': 'Bearer ' + token, 'X-DF-Request': '1'}
            data = None
            if body is not None:
                headers['Content-Type'] = 'application/json'
                data = json.dumps(body).encode()
            request = urllib.request.Request(base + path, data, headers)
            with urllib.request.urlopen(request, timeout=5) as response:
                return json.load(response)

        def wait(predicate, message, seconds=45):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                try:
                    result = predicate()
                    if result:
                        return result
                except Exception:
                    pass
                time.sleep(0.3)
            raise AssertionError(message)

        def nodes():
            adb('shell', 'uiautomator', 'dump', '/sdcard/df-security.xml')
            return ET.fromstring(adb('shell', 'cat', '/sdcard/df-security.xml')).iter('node')

        def button(label):
            for node in nodes():
                if node.attrib.get('text', '').casefold() == label.casefold():
                    return node.attrib
            return None

        def click(label):
            node = wait(lambda: button(label), 'Missing expected UI action: ' + label)
            bounds = [int(value) for value in re.findall(r'\d+', node['bounds'])]
            adb('shell', 'input', 'tap', str((bounds[0] + bounds[2]) // 2),
                str((bounds[1] + bounds[3]) // 2))

        with (work / 'serve.log').open('w') as log:
            daemon = subprocess.Popen([str(binary), 'serve', '--listen', f'127.0.0.1:{port}',
                                       '--e2ee-min-suite=2'], env=env, stdout=log, stderr=log)
            installed = False
            try:
                wait(lambda: api('/api/v1/e2ee/host-key'), 'Disposable daemon did not start')
                grant = api('/api/v1/pair/code', {'scope': 'full', 'label': 'security-emulator'})
                # The device list may be an object or array; compare the whole
                # value before confirmation, then count just the paired rows.
                before = api('/api/v1/devices')
                adb('install', str(apk))
                installed = True
                adb('shell', 'pm', 'grant', PACKAGE, 'android.permission.POST_NOTIFICATIONS')
                adb('reverse', f'tcp:{port}', f'tcp:{port}')
                # A hostile transport strips every hybrid advertisement but
                # keeps the genuine classical identity. The app must refuse
                # before any pairing POST, even though that legacy pin matches.
                post_seen = []
                metadata_seen = []
                class StrippedHost(BaseHTTPRequestHandler):
                    def log_message(self, *args):
                        pass

                    def do_GET(self):
                        metadata_seen.append(self.path)
                        metadata = api('/api/v1/e2ee/host-key')
                        for key in ['maxVersion', 'suite', 'bundle', 'fingerprintV2']:
                            metadata.pop(key, None)
                        encoded = json.dumps(metadata).encode()
                        self.send_response(200)
                        self.send_header('Content-Type', 'application/json')
                        self.send_header('Content-Length', str(len(encoded)))
                        self.end_headers()
                        self.wfile.write(encoded)

                    def do_POST(self):
                        post_seen.append(True)
                        self.send_response(403)
                        self.send_header('Content-Length', '0')
                        self.end_headers()

                proxy = HTTPServer(('127.0.0.1', 0), StrippedHost)
                proxy_port = proxy.server_address[1]
                thread = threading.Thread(target=proxy.serve_forever, daemon=True)
                thread.start()
                try:
                    adb('reverse', f'tcp:{proxy_port}', f'tcp:{proxy_port}')
                    stripped_link = 'darkfactory://pair?' + urllib.parse.urlencode({
                        'base': f'http://127.0.0.1:{proxy_port}', 'code': grant['code']})
                    adb('shell', 'am', 'start', '-a', 'android.intent.action.VIEW', '-d',
                        "'" + stripped_link + "'", PACKAGE)
                    click('Pair and connect')
                    wait(lambda: (button('Pair and connect') or {}).get('enabled') == 'true',
                         'Stripped hybrid metadata was not rejected')
                    assert button('Fingerprint matches') is None, 'Legacy pairing was offered'
                    assert metadata_seen == ['/api/v1/e2ee/host-key'], 'The app did not inspect the stripped host metadata'
                    assert not post_seen, 'A stripped advertisement caused a pairing POST'
                    assert api('/api/v1/devices') == before, 'Downgrade attempt created a device'
                finally:
                    proxy.shutdown()
                    proxy.server_close()
                    thread.join(timeout=5)
                    adb('reverse', '--remove', f'tcp:{proxy_port}')
                adb('shell', 'am', 'force-stop', PACKAGE)
                link = 'darkfactory://pair?' + urllib.parse.urlencode({'base': base, 'code': grant['code']})
                # am's URI reaches the emulator shell, so quote '&' as data.
                adb('shell', 'am', 'start', '-a', 'android.intent.action.VIEW', '-d', "'" + link + "'", PACKAGE)
                click('Pair and connect')
                wait(lambda: button('Fingerprint matches'), 'Fingerprint confirmation not shown')
                assert api('/api/v1/devices') == before, 'Pairing mutated devices before confirmation'
                click('Cancel')
                assert api('/api/v1/devices') == before, 'Cancel created a device'
                # Reuse the same one-time code. Cancel must not consume it.
                adb('shell', 'am', 'force-stop', PACKAGE)
                adb('shell', 'am', 'start', '-a', 'android.intent.action.VIEW', '-d', "'" + link + "'", PACKAGE)
                click('Pair and connect')
                click('Fingerprint matches')
                wait(lambda: api('/api/v1/devices') != before, 'Confirmed pairing did not create a device')
                wait(lambda: any(node.attrib.get('class') == 'android.webkit.WebView' for node in nodes()),
                     'Confirmed pairing did not open its WebView', seconds=60)
                # A subsequent app launch must restore the saved hybrid host.
                adb('shell', 'am', 'force-stop', PACKAGE)
                adb('shell', 'am', 'start', '-n', PACKAGE + '/.MainActivity')
                wait(lambda: any(node.attrib.get('class') == 'android.webkit.WebView' for node in nodes()),
                     'Saved hybrid host did not open its WebView', seconds=60)
                print('PASS: stripped metadata refused without POST; no device before confirmation; cancel preserves code; confirmation pairs; saved host reconnects')
            finally:
                if installed:
                    adb('uninstall', PACKAGE)
                try:
                    adb('reverse', '--remove', f'tcp:{port}')
                finally:
                    daemon.terminate()
                    daemon.wait(timeout=10)
                    subprocess.run([str(binary), 'session-host', '--stop'], env=env,
                                   check=True, capture_output=True, text=True)


if __name__ == '__main__':
    main()

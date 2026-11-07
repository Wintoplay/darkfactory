#!/usr/bin/env python3
"""Drive the native attach client through a real local PTY, no GUI automation."""
import fcntl
import json
import os
import select
import signal
import struct
import sys
import termios
import time

master, slave = os.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
credential_read, credential_write = os.pipe()
os.write(credential_write, os.environ['DFB_TOKEN'].encode())
os.close(credential_write)
os.set_inheritable(credential_read, True)
pid = os.fork()
if pid == 0:
    os.setsid()
    fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
    for fd in (0, 1, 2):
        os.dup2(slave, fd)
    os.close(master)
    os.close(slave)
    env = os.environ.copy()
    env.pop('DFB_TOKEN', None)
    os.execve(env['DFB_BIN'], [env['DFB_BIN'], 'native-attach', '--listen',
              env['DFB_BASE'].removeprefix('http://'), '--credential-fd',
              str(credential_read), env['DFB_SID']], env)
os.close(slave)
os.close(credential_read)
seen = bytearray()

def wait_for(expected, timeout=12):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if expected in seen:
            return
        ready, _, _ = select.select([master], [], [], deadline - time.monotonic())
        if ready:
            chunk = os.read(master, 65536)
            if not chunk:
                break
            seen.extend(chunk)
    raise RuntimeError('missing expected terminal output')

result = {'ok': False, 'input': False, 'resize': False}
try:
    if os.environ.get('DFB_TEST_HOSTILE') == '1':
        wait_for(b'hostile-fixture done')
        blocked = (b'\x1b]0;', b'\x1b]1;', b'\x1b]2;',
                   b'\x1b]8;', b'\x1b]1337;', b'\x1b_G', b'\x1bPq',
                   b'\x1b]52;c;?', b'\x1b]11;rgb:', b'\x1b]4;1;rgb:', b'\x1b]10;rgb:',
                   b'\x1b]12;rgb:', b'\x1b]104', b'\x1b]110', b'\x1b]111', b'\x1b]112')
        allowed = (b'\x1b]10;?\x07', b'\x1b]11;?\x07', b'\x1b]4;1;?\x07', b'\x1bP$q"m\x1b\\',
                   # A clipboard write is forwarded so Ghostty can ask the user to
                   # authorize it; the read request above stays inert.
                   b'\x1b]52;c;')
        if any(value in seen for value in blocked) or not all(value in seen for value in allowed):
            raise RuntimeError('native terminal output filter failed')
        result['filter'] = True
    else:
        if os.environ.get('DFB_TEST_OPENCODE') == '1':
            # The standalone OpenCode UI starts asynchronously. Do not submit a prompt.
            time.sleep(5)
            seen.clear()
            os.write(master, b'qzxvjkmbprwytfgh')
            wait_for(b'qzxvjkmbprwytfgh')
            result['input'] = True
            result['opencode'] = True
        else:
            wait_for(b'$ ')
            os.write(master, b'\x15printf "DF_""NATIVE""_OK\\n"\r')
            wait_for(b'DF_NATIVE_OK\r\n')
            result['input'] = True
        seen.clear()
        fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack('HHHH', 31, 91, 0, 0))
        if os.environ.get('DFB_TEST_OPENCODE') == '1':
            import urllib.request
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                req = urllib.request.Request(os.environ['DFB_BASE'] + '/api/v1/state',
                      headers={'Authorization': 'Bearer ' + os.environ['DFB_TOKEN']})
                with urllib.request.urlopen(req) as response:
                    state = json.load(response)
                session = next(x for x in state['sessions'] if x['id'] == os.environ['DFB_SID'])
                if session.get('rows') == 31 and session.get('cols') == 91:
                    break
                time.sleep(.1)
            else:
                raise RuntimeError('OpenCode session resize was not applied')
        else:
            time.sleep(.5)
            os.write(master, b'\x15stty size\r')
            wait_for(b'31 91\r\n')
        result['resize'] = True
    result['ok'] = True
except Exception as exc:
    result['error'] = str(exc)
finally:
    os.close(master)
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        waited, status = os.waitpid(pid, os.WNOHANG)
        if waited:
            break
        time.sleep(.05)
    else:
        os.kill(pid, signal.SIGTERM)
        os.waitpid(pid, 0)
print(json.dumps(result))
sys.exit(0 if result['ok'] else 1)

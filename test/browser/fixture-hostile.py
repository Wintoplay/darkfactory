#!/usr/bin/env python3
"""A hostile-output fixture: every escape a repository could print, on purpose.

The canaries are emitted on the first SIGWINCH, which is what a client attach
produces. A terminal that attaches late therefore cannot miss them: the client
fits the grid, the daemon resizes the PTY, and only then does the fixture write.

Each canary gets its own row so a probe can find it. Three sequences are split
across separate writes to prove the client's filter survives a chunk boundary.
Replies from the terminal are appended to $DF_MOUSE_LOG, so a probe can assert
which queries were answered.

Nothing here touches the daemon. The bytes travel the same path as any other
PTY output.
"""

import base64
import os
import select
import signal
import sys
import time
import tty

LOG = os.environ.get("DF_MOUSE_LOG")
if not LOG:
    sys.exit("DF_MOUSE_LOG is not set")

tty.setraw(0)
out = sys.stdout.buffer
log = open(LOG, "ab", buffering=0)

ESC = b"\x1b"
BEL = b"\x07"
ST = ESC + b"\\"
emitted = False


def w(data: bytes) -> None:
    out.write(data)
    out.flush()


def row(n: int, text: str) -> None:
    # Cursor addressing is what the daemon's looksLikeTUI heuristic keys on, and
    # it lets the probe locate a canary without depending on wrapping.
    w(ESC + b"[%d;1H" % n + text.encode() + ESC + b"[K")


def emit() -> None:
    global emitted
    if emitted:
        return
    emitted = True

    w(ESC + b"[?25l" + ESC + b"[2J")
    row(1, "hostile-fixture ready")
    row(2, "canary-clip")
    row(3, "canary-title")
    row(4, "canary-link ")

    # OSC 8 hyperlink: LINKTEXT is a clickable run if the client ever honours it.
    w(b"\x1b]8;;https://evil.invalid/phish" + ST + b"LINKTEXT" + b"\x1b]8;;" + ST)
    row(5, "canary-image")
    row(6, "canary-palette")

    # Clipboard write and read. Neither is reachable: no OSC 52 handler and no
    # clipboard addon. A probe asserts the count stays zero.
    w(b"\x1b]52;c;" + base64.b64encode(b"stolen-clipboard") + BEL)
    w(b"\x1b]52;c;?" + BEL)

    # Title and icon. A PTY title is never identity.
    w(b"\x1b]0;PWNED-TITLE" + BEL)
    w(b"\x1b]2;PWNED-TITLE" + BEL)
    w(b"\x1b]1;PWNED-ICON" + BEL)

    # Inline images: iTerm2 OSC 1337, kitty APC, Sixel DCS. All must be
    # discarded rather than decoded.
    w(b"\x1b]1337;File=inline=1;width=10;height=10:" + base64.b64encode(b"GIF89a") + BEL)
    w(b"\x1b_Ga=T,f=32,s=1,v=1;AAAA" + ST)
    w(b"\x1bPq#0;2;0;0;0#0~~@@vv@@~~@@~~$\x1b\\")

    # Palette, foreground, background and cursor sets, plus the reset forms.
    # These are the sequences the client drops: repainting them hides text or
    # impersonates another agent. Distinct colours so the probe can look.
    w(b"\x1b]11;rgb:ff/00/00" + BEL)
    w(b"\x1b]4;1;rgb:ff/00/00" + BEL)
    w(b"\x1b]10;rgb:ff/00/00" + BEL)
    w(b"\x1b]12;rgb:ff/00/00" + BEL)
    w(b"\x1b]104" + BEL)
    w(b"\x1b]110" + BEL)
    w(b"\x1b]111" + BEL)
    w(b"\x1b]112" + BEL)

    # Split payload: part of the sequence arrives, then the rest after a pause,
    # so they land in different stream chunks.
    w(b"\x1b]11;rgb:10/00")
    time.sleep(0.4)
    w(b"/00" + BEL)

    # Split introducer: ESC at the end of one chunk and "]11;..." at the start
    # of the next must still be recognised as one sequence.
    w(ESC)
    time.sleep(0.4)
    w(b"]11;rgb:00/10/00" + BEL)

    # Split palette entry, so the same is proven for OSC 4.
    w(b"\x1b]4;2;rgb:00/00")
    time.sleep(0.4)
    w(b"/10" + BEL)

    row(7, "canary-query")
    # Queries are answered, not dropped, so a TUI can still detect the palette.
    w(b"\x1b]10;?" + BEL)
    w(b"\x1b]11;?" + BEL)
    w(b"\x1b]4;1;?" + BEL)
    # Device attributes and DECRQSS also answer, and stay on the allowlist.
    w(ESC + b"[c")
    w(b"\x1bP$q\"m" + ST)

    # A plain line after all of it, to prove ordinary output still renders.
    row(8, "hostile-fixture done")
    w(ESC + b"[?25h")


def on_winch(*_args) -> None:
    emit()


row(1, "hostile-fixture waiting")
signal.signal(signal.SIGWINCH, on_winch)

while True:
    ready, _, _ = select.select([0], [], [], 1.0)
    if not ready:
        continue
    data = os.read(0, 4096)
    if not data:
        break
    log.write(data + b"\n")

#!/usr/bin/env python3
"""A deterministic stand-in for a full-screen TUI.

It enables the input modes a real TUI enables once at startup -- mouse
tracking, SGR mouse encoding, bracketed paste, application cursor keys -- then
paints a screen and repaints it on SIGWINCH *without* repeating those enables.
Every byte it reads from the PTY is appended to $DF_MOUSE_LOG, so a probe can
prove that a click or a keystroke actually reached the application.

This is what makes the C1 case reproducible without OpenCode: a client that
suppresses the raw ring tail for a TUI also loses the startup modes unless it
restores them.
"""

import os
import select
import signal
import sys
import tty

LOG = os.environ.get("DF_MOUSE_LOG")
if not LOG:
    sys.exit("DF_MOUSE_LOG is not set")

tty.setraw(0)
out = sys.stdout.buffer
log = open(LOG, "ab", buffering=0)


def paint(label: str) -> None:
    # Cursor addressing plus hide/show is what the daemon's looksLikeTUI
    # heuristic keys on; it must not rely on the alternate screen alone.
    frame = bytearray(b"\x1b[?25l\x1b[H")
    frame += b"\x1b[1;1Hmouse-tui " + label.encode() + b"\x1b[K"
    for row in range(2, 12):
        frame += b"\x1b[%d;1Hrow %d \x1b[K" % (row, row)
    frame += b"\x1b[?25h"
    out.write(bytes(frame))
    out.flush()


out.write(b"\x1b[?1000h\x1b[?1006h\x1b[?2004h\x1b[?1h")
paint("ready")


def on_winch(*_args) -> None:
    paint("repaint")


signal.signal(signal.SIGWINCH, on_winch)

while True:
    ready, _, _ = select.select([0], [], [], 1.0)
    if not ready:
        continue
    data = os.read(0, 4096)
    if not data:
        break
    log.write(data + b"\n")

#!/usr/bin/env python3
"""Report the screen offset between the page's viewport and `adb` pixels.

The browser client logs a control's box in device pixels from
`getBoundingClientRect()`, but that rectangle is relative to the layout
viewport, which starts *below* Firefox's toolbar. `adb shell input tap` speaks
screen pixels, so a page-reported y lands short by the toolbar height (312 px on
the Pixel 5 AVD with the toolbar shown). The x axis has no such offset.

The toolbar is light chrome and the app paints a dark background, so the first
row of app content is the offset. Only the middle column band is sampled, which
avoids a status-bar icon or a browser menu carrying dark pixels near the top.

    page-offset.py shot.png          # prints the offset in device pixels

Stdlib only, same PNG reader as check-blank.py.
"""

from __future__ import annotations

import struct
import sys
import zlib

# Sum of the three channels below which a pixel is the app's dark background
# (#090c0f sums to 30). Toolbar text and icons are much lighter.
DARK = 120


def read_png(path: str) -> tuple[int, int, int, list[bytes]]:
    data = open(path, "rb").read()
    if data[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError(f"{path}: not a PNG")
    pos = 8
    idat = b""
    width = height = depth = color = interlace = None
    while pos < len(data):
        (length,) = struct.unpack(">I", data[pos : pos + 4])
        ctype = data[pos + 4 : pos + 8]
        chunk = data[pos + 8 : pos + 8 + length]
        pos += 12 + length
        if ctype == b"IHDR":
            width, height, depth, color, _, _, interlace = struct.unpack(
                ">IIBBBBB", chunk
            )
        elif ctype == b"IDAT":
            idat += chunk
        elif ctype == b"IEND":
            break
    if depth != 8 or interlace != 0 or color not in (2, 6):
        raise ValueError(
            f"{path}: unsupported PNG (depth={depth} color={color} interlace={interlace}); "
            "adb screencap is 8-bit RGBA"
        )
    channels = 3 if color == 2 else 4
    raw = zlib.decompress(idat)
    stride = width * channels
    rows: list[bytes] = []
    prev = bytearray(stride)
    pos = 0
    for _ in range(height):
        filt = raw[pos]
        pos += 1
        line = bytearray(raw[pos : pos + stride])
        pos += stride
        if filt == 1:
            for i in range(channels, stride):
                line[i] = (line[i] + line[i - channels]) & 0xFF
        elif filt == 2:
            for i in range(stride):
                line[i] = (line[i] + prev[i]) & 0xFF
        elif filt == 3:
            for i in range(stride):
                left = line[i - channels] if i >= channels else 0
                line[i] = (line[i] + ((left + prev[i]) >> 1)) & 0xFF
        elif filt == 4:
            for i in range(stride):
                a = line[i - channels] if i >= channels else 0
                b = prev[i]
                c = prev[i - channels] if i >= channels else 0
                pa = abs(b - c)
                pb = abs(a - c)
                pc = abs(a + b - 2 * c)
                pred = a if (pa <= pb and pa <= pc) else (b if pb <= pc else c)
                line[i] = (line[i] + pred) & 0xFF
        elif filt != 0:
            raise ValueError(f"{path}: unknown PNG filter {filt}")
        rows.append(bytes(line))
        prev = line
    return width, height, channels, rows


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: page-offset.py shot.png", file=sys.stderr)
        return 2
    width, height, channels, rows = read_png(sys.argv[1])
    lo, hi = int(width * 0.25), int(width * 0.75)
    step = max(1, (hi - lo) // 40)
    for y in range(height):
        line = rows[y]
        dark = 0
        sampled = 0
        for x in range(lo, hi, step):
            i = x * channels
            sampled += 1
            if line[i] + line[i + 1] + line[i + 2] < DARK:
                dark += 1
        if sampled and dark >= sampled * 3 // 4:
            print(y)
            return 0
    print(0)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, zlib.error) as error:
        print(f"page-offset: {error}", file=sys.stderr)
        sys.exit(2)

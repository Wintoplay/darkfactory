#!/usr/bin/env python3
"""Report whether the phone terminal pane is painted or blank.

An unpainted pane is a single flat colour, which is what "the terminal went
blank" looks like on a screenshot. This measures the share of pixels carrying
terminal ink (text, borders, a TUI frame), so a probe can fail on a black pane
without a human staring at the image.

    check-blank.py shot.png                 # Pixel 5 default pane region
    check-blank.py shot.png --min 0.01      # stricter
    check-blank.py shot.png --rect 0,700,1080,2000

Prints `ink=<share>` and exits 0 when painted, 3 when blank, 2 on a bad input.
Stdlib only: PNG decode is a few lines of zlib, so running this needs nothing
that `python3` does not already ship.
"""

from __future__ import annotations

import argparse
import struct
import sys
import zlib

# Ink is a channel at or above this. The theme background (#090c0f) and a TUI's
# own dark fill stay far below it; glyphs, borders and the cursor stay above.
INK = 96


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
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("shot")
    ap.add_argument(
        "--rect",
        help="pane in device pixels as x0,y0,x1,y1; default is the pane region "
        "of the Pixel 5 AVD (1080x2340)",
    )
    ap.add_argument("--min", type=float, default=0.001, help="ink share floor")
    args = ap.parse_args()

    width, height, channels, rows = read_png(args.shot)
    if args.rect:
        x0, y0, x1, y1 = (int(v) for v in args.rect.split(","))
    else:
        x0, x1 = 0, width
        y0, y1 = int(height * 0.28), int(height * 0.87)
    x0 = max(0, min(x0, width))
    x1 = max(x0, min(x1, width))
    y0 = max(0, min(y0, height))
    y1 = max(y0, min(y1, height))

    ink = 0
    total = 0
    for y in range(y0, y1):
        line = rows[y]
        for x in range(x0, x1):
            i = x * channels
            if line[i] >= INK or line[i + 1] >= INK or line[i + 2] >= INK:
                ink += 1
            total += 1

    share = ink / total if total else 0.0
    verdict = "painted" if share >= args.min else "blank"
    print(f"ink={share:.4f} verdict={verdict} rect={x0},{y0},{x1},{y1}")
    return 0 if verdict == "painted" else 3


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, zlib.error) as error:
        print(f"check-blank: {error}", file=sys.stderr)
        sys.exit(2)

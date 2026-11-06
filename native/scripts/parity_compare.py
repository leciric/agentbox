#!/usr/bin/env python3
"""Compare two screenshots of the same scenario: the Electron app's and the native app's.

The metric is a per-pixel similarity with a small antialiasing tolerance (not SSIM):

  * a pixel matches when some pixel of the other image within one pixel of it
    (its 3x3 neighbourhood) has every channel within TOLERANCE (of 255) of it,
    and the same holds the other way round;
  * the score is the fraction of matching pixels, outside the masked regions.

The neighbourhood absorbs glyph edges that two rasterisers (Skia in Chromium,
GPUI's own on Linux) put half a pixel apart; the channel tolerance absorbs
antialiasing and gamma differences. A missing border, a 2px padding change, a
different colour or a wrong font size still fail.

  parity_compare.py electron.png native.png out-prefix [--mask x,y,w,h]...

Writes out-prefix.png (Electron | native | diff, mismatches in red) and prints
the score.
"""
import sys

import numpy as np
from PIL import Image

TOLERANCE = 24
RADIUS = 1


def load(path):
    return np.asarray(Image.open(path).convert("RGB"), dtype=np.int16)


def near(a, b):
    """near[y,x] is True when some pixel of b within RADIUS of (y,x) is within TOLERANCE of a[y,x]."""
    h, w, _ = a.shape
    pad = np.pad(b, ((RADIUS, RADIUS), (RADIUS, RADIUS), (0, 0)), mode="edge")
    ok = np.zeros((h, w), dtype=bool)
    for dy in range(-RADIUS, RADIUS + 1):
        for dx in range(-RADIUS, RADIUS + 1):
            s = pad[RADIUS + dy : RADIUS + dy + h, RADIUS + dx : RADIUS + dx + w]
            ok |= np.abs(a - s).max(axis=2) <= TOLERANCE
    return ok


def compare(a, b, masks):
    match = near(a, b) & near(b, a)
    keep = np.ones(match.shape, dtype=bool)
    for x, y, w, h in masks:
        keep[y : y + h, x : x + w] = False
    total = int(keep.sum())
    score = float((match & keep).sum()) / total if total else 1.0
    return score, match, keep


def main(argv):
    if len(argv) < 4:
        print(__doc__, file=sys.stderr)
        return 2
    ea, na, out = argv[1], argv[2], argv[3]
    masks = []
    rest = argv[4:]
    while rest:
        if rest[0] == "--mask":
            masks.append(tuple(int(v) for v in rest[1].split(",")))
            rest = rest[2:]
        else:
            print(f"unknown argument {rest[0]}", file=sys.stderr)
            return 2
    a, b = load(ea), load(na)
    if a.shape != b.shape:
        # A different window size is a failure in itself; compare what overlaps.
        h, w = min(a.shape[0], b.shape[0]), min(a.shape[1], b.shape[1])
        print(f"size differs: electron {a.shape[1]}x{a.shape[0]}, native {b.shape[1]}x{b.shape[0]}", file=sys.stderr)
        a, b = a[:h, :w], b[:h, :w]
    score, match, keep = compare(a, b, masks)
    diff = (a * 0.35 + 255 * 0.65 * 0.15).astype(np.uint8)
    diff[~match & keep] = (255, 40, 40)
    diff[~keep] = (diff[~keep] * 0.4 + np.array([40, 90, 255]) * 0.6).astype(np.uint8)
    side = np.concatenate([a.astype(np.uint8), b.astype(np.uint8), diff], axis=1)
    Image.fromarray(side).save(out + ".png", optimize=False, compress_level=3)
    print(f"{score:.4f}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

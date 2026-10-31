// How the review compares a before/after pair (components/ReviewCompare.tsx):
// side by side, a slider over the two, or flipping between them, and whether
// it marks the pixels that changed. The last mode chosen is how this app
// compares on this computer, so it lives in localStorage, as
// pageStackMode.ts's choice does.
import { useSyncExternalStore } from 'react';

export type CompareMode = 'side' | 'slider' | 'flip';
export const compareModes: CompareMode[] = ['side', 'slider', 'flip'];

export interface ComparePrefs {
  mode: CompareMode;
  highlight: boolean;
}

const storageKey = 'agentbox.review.compare';
const listeners = new Set<() => void>();
let current: ComparePrefs | undefined;

export function parseComparePrefs(v: string | null): ComparePrefs {
  try {
    const p = JSON.parse(v ?? '') as Partial<ComparePrefs>;
    return { mode: compareModes.includes(p.mode as CompareMode) ? (p.mode as CompareMode) : 'side', highlight: p.highlight === true };
  } catch {
    return { mode: 'side', highlight: false };
  }
}

export function comparePrefs(): ComparePrefs {
  if (current === undefined) {
    try {
      current = parseComparePrefs(localStorage.getItem(storageKey));
    } catch {
      current = parseComparePrefs(null);
    }
  }
  return current;
}

export function setComparePrefs(change: Partial<ComparePrefs>) {
  current = { ...comparePrefs(), ...change };
  try {
    localStorage.setItem(storageKey, JSON.stringify(current));
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const listener of listeners) listener();
}

export function useComparePrefs(): ComparePrefs {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    comparePrefs,
  );
}

// nextMode is the mode after mode, V's cycle.
export const nextMode = (mode: CompareMode): CompareMode => compareModes[(compareModes.indexOf(mode) + 1) % compareModes.length];

// A zoom is how far a pair is zoomed in, and where to: both halves share
// one, so side by side they move together.
export interface Zoom {
  scale: number;
  x: number;
  y: number;
}

export const noZoom: Zoom = { scale: 1, x: 0, y: 0 };

// zoomAt is z zoomed by factor around the point (px, py) of a box w × h,
// which stays where it is under the pointer, kept between 1× and 16× and
// within the box.
export function zoomAt(z: Zoom, factor: number, px: number, py: number, w: number, h: number): Zoom {
  const scale = Math.min(16, Math.max(1, z.scale * factor));
  const k = scale / z.scale;
  return clampZoom({ scale, x: px - (px - z.x) * k, y: py - (py - z.y) * k }, w, h);
}

// clampZoom keeps a zoomed picture covering its box: no panning past its edges.
export function clampZoom(z: Zoom, w: number, h: number): Zoom {
  const clamp = (v: number, size: number) => Math.min(0, Math.max(size - size * z.scale, v));
  return { scale: z.scale, x: clamp(z.x, w), y: clamp(z.y, h) };
}

// A difference is what highlighting found between two pictures of the same
// size: an overlay of the same size (RGBA), the pixels that changed in solid
// magenta and the 16px cells around them tinted, so a change of a pixel or
// two still shows; and how many pixels changed.
export interface Difference {
  overlay: Uint8ClampedArray<ArrayBuffer>;
  changed: number;
  total: number;
}

const cell = 16;

// pixelDiff compares two RGBA pictures of width × height: a pixel changed
// when any channel moved by more than threshold, which leaves alone the
// noise of compression and anti-aliasing.
export function pixelDiff(a: Uint8ClampedArray, b: Uint8ClampedArray, width: number, height: number, threshold = 24): Difference {
  const total = width * height;
  const overlay = new Uint8ClampedArray(total * 4);
  const cols = Math.ceil(width / cell);
  const cells = new Uint8Array(cols * Math.ceil(height / cell));
  const hit = new Uint8Array(total);
  let changed = 0;
  for (let i = 0, p = 0; p < total; p++, i += 4) {
    const d = Math.max(Math.abs(a[i] - b[i]), Math.abs(a[i + 1] - b[i + 1]), Math.abs(a[i + 2] - b[i + 2]), Math.abs(a[i + 3] - b[i + 3]));
    if (d <= threshold) continue;
    hit[p] = 1;
    changed++;
    cells[Math.floor(p / width / cell) * cols + Math.floor((p % width) / cell)] = 1;
  }
  if (!changed) return { overlay, changed, total };
  for (let p = 0, i = 0; p < total; p++, i += 4) {
    const solid = hit[p] === 1;
    if (!solid && !cells[Math.floor(p / width / cell) * cols + Math.floor((p % width) / cell)]) continue;
    overlay[i] = 255;
    overlay[i + 1] = 0;
    overlay[i + 2] = 170;
    overlay[i + 3] = solid ? 230 : 60;
  }
  return { overlay, changed, total };
}

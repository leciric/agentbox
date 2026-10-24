// The tray icon's pixels (tray.ts), drawn here rather than shipped as images:
// its badges carry numbers, at whatever size the platform's tray wants. It is
// the app icon (build/icon.svg) cut down to what reads at 16–32 pixels, a
// rounded square with the box glyph, and up to two badges: how many things
// wait for the user (orange, top right) and how many agents are working
// (green, bottom right). A Mac's menu bar takes a template instead, the glyph
// alone in black that macOS tints, whose numbers go in the tray's title.
//
// Each pixel is sampled 4×4 times and the shapes painted over each other per
// sample, so the edges are smoothed without any coverage arithmetic.

export type TrayTone = 'idle' | 'working' | 'attention' | 'offline';

export interface TrayIconSpec {
  size: number; // pixels on a side
  tone: TrayTone;
  waiting?: number; // the orange badge's number, when there's one
  working?: number; // the green badge's
  template?: boolean; // a Mac menu bar template: glyph only, black on clear
}

type RGBA = readonly [number, number, number, number];

const white: RGBA = [255, 255, 255, 255];
const black: RGBA = [0, 0, 0, 255];
const clear: RGBA = [0, 0, 0, 0];
const orange: RGBA = [249, 115, 22, 255];
const green: RGBA = [34, 197, 94, 255];
// The app icon's gradient, light violet at the top left to indigo; and grey
// for an AgentBox the app can't reach.
const violet: RGBA = [167, 139, 250, 255];
const indigo: RGBA = [79, 70, 229, 255];
const greyLight: RGBA = [156, 163, 175, 255];
const greyDark: RGBA = [75, 85, 99, 255];

// The box glyph (lucide's "package"), in its 24-unit grid: the hexagon, the
// lid's two edges and the edge down the middle.
const glyph: [number, number][][] = [
  [[12, 2], [21, 7], [21, 17], [12, 22], [3, 17], [3, 7], [12, 2]],
  [[3.3, 7], [12, 12], [20.7, 7]],
  [[12, 22], [12, 12]],
];

// A 3×5 font for the badges' numbers.
const font: Record<string, string> = {
  '0': '111101101101111',
  '1': '010110010010111',
  '2': '111001111100111',
  '3': '111001111001111',
  '4': '101101111001001',
  '5': '111100111001111',
  '6': '111100111101111',
  '7': '111001001001001',
  '8': '111101111101111',
  '9': '111101111001111',
  '+': '000010111010000',
};

// badgeText is what a badge says: the number, or 9+ past nine.
export function badgeText(n: number): string {
  return n > 9 ? '9+' : String(n);
}

interface Badge {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
  colour: RGBA;
  text: string;
  unit: number; // a font pixel's side
  textX: number;
  textY: number;
}

function badge(size: number, n: number, colour: RGBA, top: boolean): Badge {
  const text = badgeText(n);
  const h = size * 0.5;
  const unit = h / 7;
  const textW = (text.length * 4 - 1) * unit;
  const w = Math.max(h, textW + unit * 2.5);
  const x1 = size;
  const x0 = x1 - w;
  const y0 = top ? 0 : size - h;
  return { x0, y0, x1, y1: y0 + h, colour, text, unit, textX: x0 + (w - textW) / 2, textY: y0 + unit };
}

function inPill(b: Pick<Badge, 'x0' | 'y0' | 'x1' | 'y1'>, x: number, y: number, grow = 0): boolean {
  const r = (b.y1 - b.y0) / 2 + grow;
  const cy = (b.y0 + b.y1) / 2;
  const cx = Math.min(Math.max(x, b.x0 + r - grow), b.x1 - r + grow);
  return (x - cx) ** 2 + (y - cy) ** 2 <= r * r;
}

function inText(b: Badge, x: number, y: number): boolean {
  const col = Math.floor((x - b.textX) / b.unit);
  const row = Math.floor((y - b.textY) / b.unit);
  if (row < 0 || row > 4 || col < 0) return false;
  const char = b.text[Math.floor(col / 4)];
  const c = col % 4;
  return char !== undefined && c < 3 && font[char]?.[row * 3 + c] === '1';
}

function inRoundedSquare(size: number, x: number, y: number): boolean {
  const inset = size / 32;
  const r = size * 0.24;
  const lo = inset + r;
  const hi = size - inset - r;
  const cx = Math.min(Math.max(x, lo), hi);
  const cy = Math.min(Math.max(y, lo), hi);
  return (x - cx) ** 2 + (y - cy) ** 2 <= r * r;
}

function distToSegment(px: number, py: number, [ax, ay]: [number, number], [bx, by]: [number, number]): number {
  const dx = bx - ax;
  const dy = by - ay;
  const t = Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy)));
  return Math.hypot(px - ax - t * dx, py - ay - t * dy);
}

// onGlyph is whether a point, in the glyph's 24-unit grid, is on its strokes.
function onGlyph(gx: number, gy: number, halfWidth: number): boolean {
  for (const line of glyph) {
    for (let i = 1; i < line.length; i++) if (distToSegment(gx, gy, line[i - 1], line[i]) <= halfWidth) return true;
  }
  return false;
}

const mix = (a: RGBA, b: RGBA, t: number): RGBA => [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t, 255];

// trayIcon draws one icon, as BGRA pixels, Electron's nativeImage.createFromBitmap's order.
export function trayIcon(spec: TrayIconSpec): Buffer {
  const { size, tone, template = false } = spec;
  const badges: Badge[] = [];
  if (!template) {
    if (spec.waiting) badges.push(badge(size, spec.waiting, orange, true));
    if (spec.working) badges.push(badge(size, spec.working, green, false));
  }
  // A template is the glyph on its own, as big as the menu bar allows; on
  // the square the glyph has a margin, like the app icon's.
  const glyphSpan = template ? size * 0.92 : size * 0.66;
  const glyphOrigin = (size - glyphSpan) / 2;
  const glyphHalf = template ? 1.1 : 1.15; // in grid units
  const [from, to] = tone === 'offline' ? [greyLight, greyDark] : [violet, indigo];
  const gap = size / 16; // the clear ring between a badge and the square under it
  const samples = 4;
  const out = Buffer.alloc(size * size * 4);
  for (let py = 0; py < size; py++) {
    for (let px = 0; px < size; px++) {
      let r = 0;
      let g = 0;
      let b = 0;
      let a = 0;
      for (let sy = 0; sy < samples; sy++) {
        for (let sx = 0; sx < samples; sx++) {
          const x = px + (sx + 0.5) / samples;
          const y = py + (sy + 0.5) / samples;
          let c: RGBA = clear;
          const gx = ((x - glyphOrigin) / glyphSpan) * 24;
          const gy = ((y - glyphOrigin) / glyphSpan) * 24;
          if (template) {
            if (onGlyph(gx, gy, glyphHalf)) c = black;
            // The Mac's "something needs you": a dot cut out of the glyph's
            // top right corner, which macOS tints like the rest.
            if (tone === 'attention') {
              const dot = { x0: size * 0.62, y0: 0, x1: size, y1: size * 0.38 };
              if (inPill(dot, x, y, gap * 1.5)) c = clear;
              if (inPill(dot, x, y)) c = black;
            }
          } else {
            if (inRoundedSquare(size, x, y)) c = mix(from, to, Math.min(1, (x + y) / (2 * size)));
            if (c !== clear && onGlyph(gx, gy, glyphHalf)) c = white;
            for (const bd of badges) {
              if (inPill(bd, x, y, gap)) c = clear;
              if (inPill(bd, x, y)) c = inText(bd, x, y) ? white : bd.colour;
            }
          }
          // Premultiplied, so a clear sample adds nothing to the colour.
          r += c[0] * c[3];
          g += c[1] * c[3];
          b += c[2] * c[3];
          a += c[3];
        }
      }
      const i = (py * size + px) * 4;
      const n = samples * samples;
      out[i] = a ? Math.round(b / a) : 0;
      out[i + 1] = a ? Math.round(g / a) : 0;
      out[i + 2] = a ? Math.round(r / a) : 0;
      out[i + 3] = Math.round(a / n);
    }
  }
  return out;
}

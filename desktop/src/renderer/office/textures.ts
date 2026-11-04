// What the Office paints on canvases: the monitors, the wall board, the name
// tags, the floor's planks and the sky in the windows. The words come from the
// React view, translated; this only lays them out.
import * as THREE from 'three';
import type { DeskTone } from './model.ts';

const SANS = '"Inter Variable", Inter, "Noto Sans", system-ui, sans-serif';
const MONO = '"JetBrains Mono Variable", "JetBrains Mono", ui-monospace, monospace';

// Each tone's colour: the beacon, the monitor's bar, the board's dot.
export const toneColor: Record<DeskTone, string> = {
  needs: '#fbbf24',
  error: '#f87171',
  finished: '#34d399',
  working: '#60a5fa',
  idle: '#a5b4c8',
  stopped: '#6b7280',
};

export type LineTone = 'muted' | 'good' | 'bad' | 'warn' | 'code' | 'plain';
export type Screen = {
  tone: DeskTone;
  // The bar across the top: the state in words.
  head: string;
  title: string;
  lines: { text: string; tone?: LineTone }[];
};

// A column lists what fits; the rest is "+n", the board's list has them all.
export type BoardColumn = { head: string; empty: string; items: { title: string; sub: string; tone: DeskTone }[] };
export type BoardScreen = { title: string; needs: BoardColumn; review: BoardColumn };

const lineColor: Record<LineTone, string> = {
  muted: '#94a3b8',
  good: '#6ee7b7',
  bad: '#fca5a5',
  warn: '#fcd34d',
  code: '#c4b5fd',
  plain: '#e2e8f0',
};

export function canvasTexture(w: number, h: number): { tex: THREE.CanvasTexture; ctx: CanvasRenderingContext2D } {
  const canvas = document.createElement('canvas');
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext('2d')!;
  const tex = new THREE.CanvasTexture(canvas);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.anisotropy = 4;
  return { tex, ctx };
}

// wrap breaks text into at most max lines that fit width, the last one cut
// with an ellipsis.
export function wrap(ctx: CanvasRenderingContext2D, text: string, width: number, max: number): string[] {
  const words = text.replace(/\s+/g, ' ').trim().split(' ');
  const lines: string[] = [];
  let cur = '';
  for (const w of words) {
    const next = cur ? `${cur} ${w}` : w;
    if (ctx.measureText(next).width <= width || !cur) cur = next;
    else {
      lines.push(cur);
      cur = w;
      if (lines.length === max) break;
    }
  }
  if (lines.length < max && cur) lines.push(cur);
  if (lines.length === max && words.join(' ').length > lines.join(' ').length) lines[max - 1] = fit(ctx, `${lines[max - 1]}…`, width);
  return lines.map((l) => fit(ctx, l, width));
}

function fit(ctx: CanvasRenderingContext2D, text: string, width: number): string {
  if (ctx.measureText(text).width <= width) return text;
  let s = text;
  while (s.length > 1 && ctx.measureText(`${s}…`).width > width) s = s.slice(0, -1);
  return `${s.trimEnd()}…`;
}

// drawScreen paints a monitor: the state's bar, the task's title, and its lines
// (branch, pull request, what it's doing). A stopped agent's screen is off.
export function drawScreen(ctx: CanvasRenderingContext2D, s: Screen, t = 0) {
  const { width: W, height: H } = ctx.canvas;
  const off = s.tone === 'stopped';
  const g = ctx.createLinearGradient(0, 0, 0, H);
  g.addColorStop(0, off ? '#0b0d12' : '#111827');
  g.addColorStop(1, off ? '#07080b' : '#0b1220');
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, W, H);
  if (off) {
    ctx.fillStyle = '#1f2937';
    ctx.font = `600 34px ${SANS}`;
    ctx.textAlign = 'center';
    ctx.fillText(s.head, W / 2, H / 2 + 6);
    ctx.font = `500 26px ${SANS}`;
    ctx.fillStyle = '#273244';
    ctx.fillText(fit(ctx, s.title, W - 80), W / 2, H / 2 + 46);
    ctx.textAlign = 'left';
    return;
  }
  const col = toneColor[s.tone];
  ctx.fillStyle = col;
  ctx.fillRect(0, 0, W, 54);
  ctx.fillStyle = '#0b1220';
  ctx.font = `700 30px ${SANS}`;
  ctx.fillText(fit(ctx, s.head, W - 40), 22, 38);
  // A working agent's cursor blinks in the bar.
  if (s.tone === 'working' && Math.floor(t * 2) % 2 === 0) ctx.fillRect(W - 40, 16, 16, 24);
  ctx.fillStyle = '#f8fafc';
  ctx.font = `700 38px ${SANS}`;
  const title = wrap(ctx, s.title, W - 40, 2);
  title.forEach((l, i) => ctx.fillText(l, 20, 102 + i * 44));
  let y = 102 + title.length * 44 + 12;
  for (const line of s.lines) {
    if (y > H - 16) break;
    const tone = line.tone ?? 'plain';
    ctx.fillStyle = lineColor[tone];
    ctx.font = tone === 'code' ? `500 26px ${MONO}` : `600 29px ${SANS}`;
    for (const l of wrap(ctx, line.text, W - 40, tone === 'plain' || tone === 'warn' ? 3 : 1)) {
      if (y > H - 12) break;
      ctx.fillText(l, 20, y);
      y += 35;
    }
    y += 4;
  }
}

// drawBoard paints the wall board: what needs you on the left, what waits for
// your review on the right, as sticky notes on cork.
export function drawBoard(ctx: CanvasRenderingContext2D, b: BoardScreen) {
  const { width: W, height: H } = ctx.canvas;
  ctx.fillStyle = '#1a2233';
  ctx.fillRect(0, 0, W, H);
  ctx.fillStyle = '#f8fafc';
  ctx.font = `700 40px ${SANS}`;
  ctx.textAlign = 'center';
  ctx.fillText(b.title, W / 2, 52);
  ctx.textAlign = 'left';
  const colW = (W - 90) / 2;
  const column = (c: BoardColumn, x: number, accent: string) => {
    ctx.fillStyle = accent;
    ctx.beginPath();
    ctx.arc(x + 14, 96, 11, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = '#e2e8f0';
    ctx.font = `700 30px ${SANS}`;
    ctx.fillText(fit(ctx, c.head, colW - 40), x + 36, 106);
    let y = 126;
    if (c.items.length === 0) {
      ctx.fillStyle = '#64748b';
      ctx.font = `500 28px ${SANS}`;
      for (const l of wrap(ctx, c.empty, colW - 20, 2)) {
        ctx.fillText(l, x + 4, y + 40);
        y += 36;
      }
      return;
    }
    const rowH = 66;
    const fits = Math.max(1, Math.floor((H - y - 10) / (rowH + 8)));
    const shown = c.items.length > fits ? fits - 1 : fits;
    for (const it of c.items.slice(0, shown)) {
      ctx.fillStyle = '#0f172a';
      roundRect(ctx, x, y, colW, rowH, 14);
      ctx.fill();
      ctx.fillStyle = toneColor[it.tone];
      roundRect(ctx, x, y, 10, rowH, 5);
      ctx.fill();
      ctx.fillStyle = '#f1f5f9';
      ctx.font = `600 26px ${SANS}`;
      ctx.fillText(fit(ctx, it.title, colW - 40), x + 24, y + 29);
      ctx.fillStyle = '#94a3b8';
      ctx.font = `500 20px ${SANS}`;
      ctx.fillText(fit(ctx, it.sub, colW - 40), x + 24, y + 55);
      y += rowH + 8;
    }
    if (c.items.length > shown) {
      ctx.fillStyle = '#cbd5e1';
      ctx.font = `700 28px ${SANS}`;
      ctx.fillText(`+${c.items.length - shown}`, x + 8, y + 30);
    }
  };
  column(b.needs, 30, toneColor.needs);
  column(b.review, 60 + colW, toneColor.finished);
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath();
  ctx.roundRect(x, y, w, h, r);
}

// drawTag paints a name tag: the text on a dark pill with a coloured dot.
export function drawTag(ctx: CanvasRenderingContext2D, text: string, dot: string | undefined, big = false) {
  const { width: W, height: H } = ctx.canvas;
  ctx.clearRect(0, 0, W, H);
  ctx.font = `${big ? 700 : 600} ${big ? 50 : 38}px ${SANS}`;
  const label = fit(ctx, text, W - (dot ? 110 : 70));
  const tw = ctx.measureText(label).width;
  const pw = tw + (dot ? 92 : 56);
  const x = (W - pw) / 2;
  ctx.fillStyle = 'rgba(10, 14, 24, 0.82)';
  roundRect(ctx, x, 6, pw, H - 12, (H - 12) / 2);
  ctx.fill();
  let tx = x + 28;
  if (dot) {
    ctx.fillStyle = dot;
    ctx.beginPath();
    ctx.arc(x + 36, H / 2, big ? 13 : 11, 0, Math.PI * 2);
    ctx.fill();
    tx = x + 60;
  }
  ctx.fillStyle = '#f8fafc';
  ctx.fillText(label, tx, H / 2 + (big ? 18 : 13));
}

// drawSign paints an area's sign: its project's name in its colour.
export function drawSign(ctx: CanvasRenderingContext2D, text: string, hue: number) {
  const { width: W, height: H } = ctx.canvas;
  ctx.clearRect(0, 0, W, H);
  ctx.fillStyle = `hsl(${hue} 55% 22%)`;
  roundRect(ctx, 4, 4, W - 8, H - 8, 26);
  ctx.fill();
  ctx.strokeStyle = `hsl(${hue} 70% 62%)`;
  ctx.lineWidth = 6;
  roundRect(ctx, 7, 7, W - 14, H - 14, 23);
  ctx.stroke();
  ctx.fillStyle = `hsl(${hue} 90% 88%)`;
  ctx.font = `800 64px ${SANS}`;
  ctx.textAlign = 'center';
  ctx.fillText(fit(ctx, text, W - 60), W / 2, H / 2 + 22);
  ctx.textAlign = 'left';
}

// drawBubble paints a speech bubble's sign: ! for a question, × an error, ✓
// finished, z asleep.
export function drawBubble(ctx: CanvasRenderingContext2D, glyph: string, fill: string) {
  const { width: W, height: H } = ctx.canvas;
  ctx.clearRect(0, 0, W, H);
  ctx.fillStyle = fill;
  ctx.beginPath();
  ctx.arc(W / 2, H / 2 - 8, W / 2 - 12, 0, Math.PI * 2);
  ctx.fill();
  ctx.beginPath();
  ctx.moveTo(W / 2 - 16, H - 40);
  ctx.lineTo(W / 2, H - 6);
  ctx.lineTo(W / 2 + 16, H - 40);
  ctx.fill();
  ctx.fillStyle = '#111827';
  ctx.font = `900 ${W * 0.55}px ${SANS}`;
  ctx.textAlign = 'center';
  ctx.fillText(glyph, W / 2, H / 2 + W * 0.18);
  ctx.textAlign = 'left';
}

// planks is the floor: warm wooden boards, a little uneven.
export function planks(): THREE.CanvasTexture {
  const { tex, ctx } = canvasTexture(512, 512);
  const rows = 8;
  const h = 512 / rows;
  let seed = 7;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  for (let r = 0; r < rows; r++) {
    let x = -rnd() * 200;
    while (x < 512) {
      const w = 160 + rnd() * 180;
      const l = 40 + rnd() * 10;
      ctx.fillStyle = `hsl(${28 + rnd() * 6} ${38 + rnd() * 10}% ${l}%)`;
      ctx.fillRect(x, r * h, w, h);
      // Grain.
      ctx.strokeStyle = 'rgba(60, 30, 10, 0.12)';
      ctx.lineWidth = 1;
      for (let k = 0; k < 4; k++) {
        const y = r * h + 8 + rnd() * (h - 16);
        ctx.beginPath();
        ctx.moveTo(x, y);
        ctx.bezierCurveTo(x + w / 3, y + rnd() * 6 - 3, x + (2 * w) / 3, y + rnd() * 6 - 3, x + w, y);
        ctx.stroke();
      }
      ctx.fillStyle = 'rgba(40, 20, 8, 0.45)';
      ctx.fillRect(x, r * h, 2, h);
      x += w;
    }
    ctx.fillStyle = 'rgba(40, 20, 8, 0.5)';
    ctx.fillRect(0, r * h, 512, 2);
  }
  tex.wrapS = tex.wrapT = THREE.RepeatWrapping;
  return tex;
}

// sky paints a window's view at an hour of the day: blue with clouds by day,
// orange at dusk, dark blue with stars at night.
export function paintSky(ctx: CanvasRenderingContext2D, hour: number) {
  const { width: W, height: H } = ctx.canvas;
  const night = hour < 6 || hour >= 20;
  const dusk = !night && (hour < 8 || hour >= 17.5);
  const g = ctx.createLinearGradient(0, 0, 0, H);
  if (night) {
    g.addColorStop(0, '#0b1230');
    g.addColorStop(1, '#1e2a55');
  } else if (dusk) {
    g.addColorStop(0, '#4a5fa8');
    g.addColorStop(0.6, '#f59e6b');
    g.addColorStop(1, '#fcd29f');
  } else {
    g.addColorStop(0, '#5aa7ef');
    g.addColorStop(1, '#bfe3ff');
  }
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, W, H);
  let seed = 3;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  if (night) {
    ctx.fillStyle = '#fff';
    for (let i = 0; i < 60; i++) ctx.fillRect(rnd() * W, rnd() * H * 0.7, 1.5, 1.5);
    ctx.fillStyle = '#f8f3d4';
    ctx.beginPath();
    ctx.arc(W * 0.75, H * 0.25, 14, 0, Math.PI * 2);
    ctx.fill();
  } else {
    ctx.fillStyle = 'rgba(255,255,255,0.85)';
    for (let i = 0; i < 5; i++) {
      const cx = rnd() * W;
      const cy = 20 + rnd() * H * 0.4;
      for (let k = 0; k < 4; k++) {
        ctx.beginPath();
        ctx.ellipse(cx + k * 18, cy + (k % 2) * 6, 22, 12, 0, 0, Math.PI * 2);
        ctx.fill();
      }
    }
  }
  // A city skyline along the bottom.
  ctx.fillStyle = night ? '#121a36' : dusk ? '#5b4a6e' : '#8fb2cf';
  let x = 0;
  while (x < W) {
    const w = 18 + rnd() * 30;
    const h = 30 + rnd() * 70;
    ctx.fillRect(x, H - h, w, h);
    if (night) {
      ctx.fillStyle = '#fde68a';
      for (let k = 0; k < 4; k++) if (rnd() > 0.5) ctx.fillRect(x + 4 + rnd() * (w - 8), H - h + 6 + rnd() * (h - 12), 3, 4);
      ctx.fillStyle = '#121a36';
    }
    x += w + 2;
  }
}

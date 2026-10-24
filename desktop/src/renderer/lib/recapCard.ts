// The weekly recap's share cards: the week drawn on a canvas, 1200×630 for a
// post's link-card shape or 1080×1080 square, saved or copied as a PNG. The
// page shows the PNG itself, so what the user sees is what they post. Project
// names, and the titles of agents' tasks, which can name a project, are left
// off unless the user ticks them in.
//
// Drawn with the canvas, not an SVG turned into an image: an SVG drawn as an
// image can't reach the app's fonts, and Inter is half the look.

import type * as T from '../../shared/api';
import { t } from '../../shared/i18n/index.ts';
import { awayShare, cardSize, compact, dayName, duration, modelLabel, weekRange, type CardFormat } from './recap.ts';

const font = '"Inter Variable", Inter, system-ui, sans-serif';

// The card's colors: AgentBox's violet (build/icon.svg) on near-black.
const ink = { text: '#f5f3ff', muted: '#a8a3c7', subtle: '#6f6a92', accent: '#c4b5fd' };
const away = '#8b5cf6';
const withYou = '#c4b5fd';
const panel = 'rgba(255,255,255,0.045)';
const edge = 'rgba(255,255,255,0.09)';

const aiNames: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', opencode: 'OpenCode', cursor: 'Cursor' };

export type CardOptions = { format: CardFormat; names: boolean };

type Ctx = CanvasRenderingContext2D;
type Box = { x: number; y: number; w: number; h: number };

// drawCard draws recap onto a new canvas.
export async function drawCard(recap: T.Recap, { format, names }: CardOptions): Promise<HTMLCanvasElement> {
  await Promise.all(['800 120px', '700 30px', '600 20px', '500 16px'].map((f) => document.fonts.load(`${f} ${font}`).catch(() => [])));
  const { width, height } = cardSize[format];
  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  const ctx = canvas.getContext('2d')!;
  background(ctx, width, height);
  if (format === 'landscape') {
    header(ctx, { x: 60, y: 52, w: width - 120, h: 44 }, recap);
    hero(ctx, { x: 60, y: 134, w: 560, h: 200 }, recap, 112);
    tiles(ctx, { x: 60, y: 352, w: 560, h: 218 }, recap, names);
    const right = { x: 652, y: 134, w: 488, h: 436 };
    rounded(ctx, right, 24, panel, edge);
    days(ctx, { x: right.x + 28, y: right.y + 26, w: right.w - 56, h: 200 }, recap);
    models(ctx, { x: right.x + 28, y: right.y + 252, w: right.w - 56, h: 160 }, recap);
  } else {
    header(ctx, { x: 72, y: 64, w: width - 144, h: 48 }, recap);
    hero(ctx, { x: 72, y: 168, w: width - 144, h: 230 }, recap, 140);
    tiles(ctx, { x: 72, y: 418, w: width - 144, h: 246 }, recap, names);
    const bottom = { x: 72, y: 690, w: width - 144, h: 318 };
    rounded(ctx, bottom, 28, panel, edge);
    const half = (bottom.w - 32 * 3) / 2;
    days(ctx, { x: bottom.x + 32, y: bottom.y + 30, w: half, h: bottom.h - 60 }, recap);
    models(ctx, { x: bottom.x + 64 + half, y: bottom.y + 30, w: half, h: bottom.h - 60 }, recap);
  }
  return canvas;
}

// cardPNG is the canvas as PNG bytes.
export function cardPNG(canvas: HTMLCanvasElement): Promise<Uint8Array> {
  return new Promise((resolve, reject) =>
    canvas.toBlob((blob) => (blob ? blob.arrayBuffer().then((b) => resolve(new Uint8Array(b)), reject) : reject(new Error('no image'))), 'image/png'),
  );
}

function background(ctx: Ctx, w: number, h: number) {
  const base = ctx.createLinearGradient(0, 0, w, h);
  base.addColorStop(0, '#0d0b1f');
  base.addColorStop(1, '#130d2c');
  ctx.fillStyle = base;
  ctx.fillRect(0, 0, w, h);
  glow(ctx, w * 0.92, h * 0.02, Math.max(w, h) * 0.62, 'rgba(124,58,237,0.42)');
  glow(ctx, w * 0.02, h * 1.0, Math.max(w, h) * 0.55, 'rgba(79,70,229,0.30)');
  glow(ctx, w * 0.48, h * 0.42, Math.max(w, h) * 0.35, 'rgba(167,139,250,0.07)');
  // A faint dot grid, the texture of a machine room.
  ctx.fillStyle = 'rgba(255,255,255,0.045)';
  for (let y = 18; y < h; y += 28) for (let x = 18; x < w; x += 28) ctx.fillRect(x, y, 1.5, 1.5);
}

function glow(ctx: Ctx, x: number, y: number, r: number, color: string) {
  const g = ctx.createRadialGradient(x, y, 0, x, y, r);
  g.addColorStop(0, color);
  g.addColorStop(1, 'rgba(0,0,0,0)');
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, ctx.canvas.width, ctx.canvas.height);
}

function rounded(ctx: Ctx, b: Box, r: number, fill: string | CanvasGradient, stroke?: string) {
  ctx.beginPath();
  ctx.roundRect(b.x, b.y, b.w, b.h, r);
  ctx.fillStyle = fill;
  ctx.fill();
  if (stroke) {
    ctx.strokeStyle = stroke;
    ctx.lineWidth = 1;
    ctx.stroke();
  }
}

function text(ctx: Ctx, s: string, x: number, y: number, size: number, weight: number, color: string | CanvasGradient, align: CanvasTextAlign = 'left', spacing = 0) {
  ctx.font = `${weight} ${size}px ${font}`;
  ctx.letterSpacing = `${spacing}px`;
  ctx.fillStyle = color;
  ctx.textAlign = align;
  ctx.textBaseline = 'alphabetic';
  ctx.fillText(s, x, y);
  ctx.letterSpacing = '0px';
}

// fit is s cut to width w at the font set, with an ellipsis.
function fit(ctx: Ctx, s: string, w: number, size: number, weight: number): string {
  ctx.font = `${weight} ${size}px ${font}`;
  if (ctx.measureText(s).width <= w) return s;
  let lo = 0;
  let hi = s.length;
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2);
    if (ctx.measureText(`${s.slice(0, mid)}…`).width <= w) lo = mid;
    else hi = mid - 1;
  }
  return `${s.slice(0, lo).trimEnd()}…`;
}

// logo is AgentBox's mark (build/icon.svg) at x, y, size across.
function logo(ctx: Ctx, x: number, y: number, size: number) {
  const g = ctx.createLinearGradient(x, y, x + size, y + size);
  g.addColorStop(0, '#a78bfa');
  g.addColorStop(0.55, '#7c3aed');
  g.addColorStop(1, '#4f46e5');
  rounded(ctx, { x, y, w: size, h: size }, size * 0.24, g);
  ctx.save();
  ctx.translate(x + size * 0.19, y + size * 0.19);
  ctx.scale((size * 0.62) / 24, (size * 0.62) / 24);
  ctx.strokeStyle = '#fff';
  ctx.lineWidth = 2;
  ctx.lineCap = 'round';
  ctx.lineJoin = 'round';
  for (const d of ['M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z', 'm3.3 7 8.7 5 8.7-5', 'M12 22V12']) ctx.stroke(new Path2D(d));
  ctx.restore();
}

function header(ctx: Ctx, b: Box, recap: T.Recap) {
  logo(ctx, b.x, b.y, b.h);
  text(ctx, 'AgentBox', b.x + b.h + 14, b.y + b.h * 0.68, b.h * 0.5, 650, ink.text, 'left', -0.3);
  text(ctx, weekRange(recap), b.x + b.w, b.y + b.h * 0.66, b.h * 0.42, 500, ink.muted, 'right');
}

function hero(ctx: Ctx, b: Box, recap: T.Recap, size: number) {
  text(ctx, t('recap.card.eyebrow').toUpperCase(), b.x, b.y + 16, size * 0.135, 700, ink.accent, 'left', 2.4);
  const big = duration(recap.workSeconds);
  const top = b.y + 30 + size * 0.86;
  const g = ctx.createLinearGradient(b.x, top - size, b.x, top);
  g.addColorStop(0, '#ffffff');
  g.addColorStop(1, '#c4b5fd');
  text(ctx, big, b.x - size * 0.04, top, size, 800, g, 'left', -size * 0.035);
  ctx.font = `800 ${size}px ${font}`;
  ctx.letterSpacing = `${-size * 0.035}px`;
  const after = b.x + ctx.measureText(big).width + size * 0.12;
  ctx.letterSpacing = '0px';
  const label = fit(ctx, t('recap.card.agentWork'), b.x + b.w - after, size * 0.26, 600);
  text(ctx, label, after, top - size * 0.06, size * 0.26, 600, ink.text);
  const line = recap.workSeconds ? t('recap.card.away', { hours: duration(recap.awaySeconds), share: Math.round(awayShare(recap) * 100) }) : t('recap.card.quiet');
  moon(ctx, b.x + size * 0.11, top + size * 0.365, size * 0.1);
  text(ctx, fit(ctx, line, b.w - size * 0.34, size * 0.2, 500), b.x + size * 0.3, top + size * 0.43, size * 0.2, 500, ink.accent);
}

// moon is a crescent: the work done while the user slept.
function moon(ctx: Ctx, cx: number, cy: number, r: number) {
  ctx.save();
  ctx.beginPath();
  ctx.arc(cx, cy, r, 0, Math.PI * 2);
  ctx.arc(cx + r * 0.55, cy - r * 0.35, r * 0.85, 0, Math.PI * 2, true);
  ctx.fillStyle = '#fde68a';
  ctx.fill('evenodd');
  ctx.restore();
}

type Tile = { label: string; value: string; sub: string };

function tileList(recap: T.Recap, names: boolean): Tile[] {
  const longest = recap.longest;
  const busiest = recap.busiest;
  return [
    { label: t('recap.agents'), value: compact(recap.agents), sub: t('recap.projects', { count: recap.projects }) },
    { label: t('recap.prs'), value: compact(recap.prsOpened), sub: t('recap.merged', { count: recap.prsMerged }) },
    { label: t('recap.lines'), value: compact(recap.insertions + recap.deletions), sub: `+${compact(recap.insertions)}  −${compact(recap.deletions)}` },
    { label: t('recap.streak'), value: t('recap.days', { count: recap.streak }), sub: t('recap.best', { count: recap.bestStreak }) },
    {
      label: t('recap.card.longest'),
      value: longest ? duration(longest.workSeconds) : '—',
      sub: !longest ? '' : names ? `${longest.project} · ${longest.title || longest.agent}` : (aiNames[longest.ai ?? ''] ?? t('recap.oneAgent')),
    },
    names && busiest
      ? { label: t('recap.busiest'), value: busiest.project, sub: duration(busiest.workSeconds) }
      : {
          label: t('recap.busiest'),
          value: busiest && recap.workSeconds ? `${Math.round((busiest.workSeconds / recap.workSeconds) * 100)}%` : '—',
          sub: busiest ? t('recap.ofTheWork') : '',
        },
  ];
}

function tiles(ctx: Ctx, b: Box, recap: T.Recap, names: boolean) {
  const gap = 14;
  const w = (b.w - gap * 2) / 3;
  const h = (b.h - gap) / 2;
  const s = h / 100;
  tileList(recap, names).forEach((tile, i) => {
    const x = b.x + (i % 3) * (w + gap);
    const y = b.y + Math.floor(i / 3) * (h + gap);
    rounded(ctx, { x, y, w, h }, 18, panel, edge);
    const inner = w - 36;
    text(ctx, fit(ctx, tile.label, inner, 14 * s, 600), x + 18, y + 28 * s, 14 * s, 600, ink.muted);
    text(ctx, fit(ctx, tile.value, inner, 32 * s, 750), x + 18, y + 66 * s, 32 * s, 750, ink.text, 'left', -0.5);
    if (tile.sub) text(ctx, fit(ctx, tile.sub, inner, 14 * s, 500), x + 18, y + 88 * s, 14 * s, 500, ink.subtle);
  });
}

// days is each day's work as a bar: the part done while the user was away
// at its foot, the rest above it, a 2px gap between.
function days(ctx: Ctx, b: Box, recap: T.Recap) {
  text(ctx, t('recap.eachDay'), b.x, b.y + 16, 15, 650, ink.text);
  legend(ctx, b.x + b.w, b.y + 16);
  const top = b.y + 44;
  const foot = b.y + b.h - 30;
  const most = Math.max(...recap.days.map((d) => d.workSeconds), 1);
  const slot = b.w / recap.days.length;
  const bar = Math.min(slot * 0.58, 44);
  recap.days.forEach((d, i) => {
    const x = b.x + i * slot + (slot - bar) / 2;
    const full = ((foot - top) * d.workSeconds) / most;
    if (d.workSeconds === 0) {
      rounded(ctx, { x, y: foot - 4, w: bar, h: 4 }, 2, 'rgba(255,255,255,0.10)');
    } else {
      const low = (full * d.awaySeconds) / d.workSeconds;
      const high = full - low;
      ctx.save();
      ctx.beginPath();
      ctx.roundRect(x, foot - full, bar, full, [6, 6, 2, 2]);
      ctx.clip();
      ctx.fillStyle = away;
      ctx.fillRect(x, foot - low, bar, low);
      ctx.fillStyle = withYou;
      ctx.fillRect(x, foot - full, bar, Math.max(0, high - (low > 0 && high > 0 ? 2 : 0)));
      ctx.restore();
    }
    text(ctx, dayName(d.date), x + bar / 2, b.y + b.h - 6, 13, 500, i === recap.days.length - 1 ? ink.text : ink.subtle, 'center');
  });
}

function legend(ctx: Ctx, right: number, y: number) {
  ctx.font = `500 12px ${font}`;
  const items = [
    { label: t('recap.withYou'), color: withYou },
    { label: t('recap.whileAway'), color: away },
  ];
  let x = right;
  for (const item of items) {
    const w = ctx.measureText(item.label).width;
    text(ctx, item.label, x, y, 12, 500, ink.muted, 'right');
    x -= w + 8;
    rounded(ctx, { x: x - 9, y: y - 9, w: 9, h: 9 }, 2, item.color);
    x -= 9 + 16;
  }
}

// models is the week's tokens by model, the top three and the rest.
function models(ctx: Ctx, b: Box, recap: T.Recap) {
  text(ctx, t('recap.models'), b.x, b.y + 16, 15, 650, ink.text);
  text(ctx, t('recap.tokens', { tokens: compact(recap.tokens) }), b.x + b.w, b.y + 16, 13, 500, ink.muted, 'right');
  const shown = recap.models.slice(0, 3);
  const rest = recap.models.slice(3).reduce((n, m) => n + m.tokens, 0);
  const rows = rest ? [...shown.map((m) => ({ name: m.model ? modelLabel(m.model) : t('vm.tokens.unnamedModel'), tokens: m.tokens })), { name: t('recap.otherModels'), tokens: rest }] : shown.map((m) => ({ name: m.model ? modelLabel(m.model) : t('vm.tokens.unnamedModel'), tokens: m.tokens }));
  if (rows.length === 0) {
    text(ctx, t('recap.noTokens'), b.x, b.y + 56, 14, 500, ink.subtle);
    return;
  }
  const most = rows[0].tokens || 1;
  const step = Math.min(40, (b.h - 44) / rows.length);
  rows.forEach((row, i) => {
    const y = b.y + 48 + i * step;
    text(ctx, fit(ctx, row.name, b.w * 0.62, 14, 600), b.x, y, 14, 600, ink.text);
    text(ctx, compact(row.tokens), b.x + b.w, y, 14, 600, ink.muted, 'right');
    rounded(ctx, { x: b.x, y: y + 7, w: b.w, h: 5 }, 2.5, 'rgba(255,255,255,0.07)');
    const g = ctx.createLinearGradient(b.x, 0, b.x + b.w, 0);
    g.addColorStop(0, '#7c3aed');
    g.addColorStop(1, '#a78bfa');
    rounded(ctx, { x: b.x, y: y + 7, w: Math.max(5, (b.w * row.tokens) / most), h: 5 }, 2.5, g);
  });
}

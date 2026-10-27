// Rich blocks: what a chat draws instead of showing a fenced block's code.
// An AI tool writes them as it writes any code block, so they cost no tool
// and no prompt beyond one line of the brief:
//
//   ```mermaid          a diagram, or a chart (pie, xychart-beta), drawn by mermaid
//   ```svg              a picture
//   ```html preview     a page, run in a sandboxed frame with no network
//
// A plain ```html block stays code, with a button to preview it: HTML shown
// as an example of code isn't a page to run.

export type RichKind = 'mermaid' | 'svg' | 'html';

// richKind is what a fenced block draws, from its language and the rest of
// its info string (```html preview), or null for code.
export function richKind(language: string | undefined, meta?: string | null): { kind: RichKind; drawn: boolean } | null {
  switch (language?.toLowerCase()) {
    case 'mermaid':
      return { kind: 'mermaid', drawn: true };
    case 'svg':
      return { kind: 'svg', drawn: true };
    case 'html': {
      const words = (meta ?? '').toLowerCase().split(/\s+/);
      return { kind: 'html', drawn: words.includes('preview') || words.includes('render') };
    }
    default:
      return null;
  }
}

// openFence is a renderable block the text ends inside of: its opening fence
// has arrived and its closing one hasn't. `before` is the text up to the
// fence, `code` what of the block has arrived. A plain ```html block, which
// stays code, isn't one, nor is any other language.
export function openFence(text: string): { kind: RichKind; drawn: boolean; before: string; code: string; lines: number; bytes: number } | null {
  let fence: { char: string; size: number; at: number; info: string } | null = null;
  let at = 0;
  for (const line of text.split('\n')) {
    const m = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (m && !fence) fence = { char: m[1][0], size: m[1].length, at, info: m[2].trim() };
    else if (m && fence && m[1][0] === fence.char && m[1].length >= fence.size && !m[2].trim()) fence = null;
    at += line.length + 1;
  }
  if (!fence) return null;
  const [language, ...meta] = fence.info.split(/\s+/);
  const rich = richKind(language, meta.join(' '));
  if (!rich?.drawn) return null;
  const rest = text.slice(text.indexOf('\n', fence.at) + 1 || text.length);
  const code = text.includes('\n', fence.at) ? rest : '';
  return { ...rich, before: text.slice(0, fence.at), code, lines: code ? code.split('\n').length : 0, bytes: new TextEncoder().encode(code).length };
}

// svgDocument is an SVG made fit to stand alone, as an <img> or a file
// needs it: with its namespace, which an inline SVG can do without.
export function svgDocument(svg: string): string {
  const s = svg.trim();
  return /^(<\?xml[^>]*>\s*)?<svg\b[^>]*\bxmlns\s*=/i.test(s) ? s : s.replace(/<svg\b/i, '<svg xmlns="http://www.w3.org/2000/svg"');
}

// svgUrl is an SVG as a data: URL for an <img>, where it is a picture and
// nothing more: no script runs, nothing is fetched.
export function svgUrl(svg: string): string {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svgDocument(svg))}`;
}

// svgSize is an SVG's size from its width and height, or else its viewBox,
// in pixels; null when it says neither.
export function svgSize(svg: string): { width: number; height: number } | null {
  const root = /<svg\b[^>]*>/i.exec(svg)?.[0];
  if (!root) return null;
  const attr = (name: string) => new RegExp(`\\s${name}\\s*=\\s*["']([^"']*)["']`, 'i').exec(root)?.[1];
  const px = (v?: string) => (v && /^\s*[\d.]+\s*(px)?\s*$/.test(v) ? parseFloat(v) : NaN);
  const [w, h] = [px(attr('width')), px(attr('height'))];
  if (w > 0 && h > 0) return { width: w, height: h };
  const box = attr('viewBox')?.trim().split(/[\s,]+/).map(Number);
  if (box?.length === 4 && box[2] > 0 && box[3] > 0) return { width: box[2], height: box[3] };
  return null;
}

// The height a page's frame takes in the chat, at least and at most (the most is
// FRAME_VIEWPORT of the window, FRAME_MAX where the window is unknown): past
// that, the page scrolls inside it, and Open large shows it whole.
export const FRAME_MIN = 60;
export const FRAME_MAX = 480;
// A page's frame is as tall as the page, up to this share of the window.
export const FRAME_VIEWPORT = 0.7;
// A picture's: a diagram is drawn to fit the column, and opened large to read.
export const PICTURE_MAX = 360;

export function frameHeight(reported: unknown, max = FRAME_MAX): number | null {
  if (typeof reported !== 'number' || !Number.isFinite(reported)) return null;
  return Math.min(max, Math.max(FRAME_MIN, Math.ceil(reported)));
}

// The messages a framed page and the chat send each other, as the one key of
// an object: the page's height, a link it was clicked on, the first error its
// scripts threw, and what it tried to load with no network; and the theme and
// the zoom (of a page opened large) the chat sends it. The chat takes them only from its own frame's window.
export const HEIGHT_MESSAGE = 'agentboxRenderHeight';
export const LINK_MESSAGE = 'agentboxOpenLink';
export const ERROR_MESSAGE = 'agentboxError';
export const BLOCKED_MESSAGE = 'agentboxBlocked';
export const THEME_MESSAGE = 'agentboxTheme';
export const ZOOM_MESSAGE = 'agentboxZoom';

// PageTheme is what a framed page is told of the app's look: light or dark,
// and the custom properties its CSS can use (PAGE_VARIABLES), so a chart
// drawn in var(--chart-1) is in the app's colours and follows a switch
// between light and dark without the page loading again.
export interface PageTheme {
  mode: 'dark' | 'light';
  vars: Record<string, string>;
}

// The properties a page gets: shadcn's names, which a model already writes,
// each taken from one of the app's (styles.css) or, for one the app doesn't
// set, from these defaults.
const PAGE_VARIABLES: { name: string; from?: string; dark: string; light: string }[] = [
  { name: '--background', from: '--ab-chat', dark: '#09090d', light: '#ffffff' },
  { name: '--foreground', from: '--ab-secondary', dark: '#e4e4e7', light: '#27272a' },
  { name: '--muted', from: '--ab-surface-raised', dark: 'rgb(255 255 255 / 0.065)', light: 'rgb(9 9 11 / 0.07)' },
  { name: '--muted-foreground', from: '--ab-muted', dark: '#a1a1aa', light: '#52525b' },
  { name: '--card', from: '--ab-surface', dark: 'rgb(255 255 255 / 0.05)', light: 'rgb(9 9 11 / 0.05)' },
  { name: '--border', from: '--ab-line-strong', dark: 'rgb(255 255 255 / 0.1)', light: 'rgb(9 9 11 / 0.16)' },
  { name: '--primary', from: '--ab-brand-500', dark: '#8b5cf6', light: '#7c3aed' },
  { name: '--primary-foreground', dark: '#ffffff', light: '#ffffff' },
  { name: '--success', dark: '#34d399', light: '#059669' },
  { name: '--warning', dark: '#fbbf24', light: '#d97706' },
  { name: '--destructive', dark: '#fb7185', light: '#e11d48' },
  { name: '--chart-1', from: '--ab-brand-500', dark: '#8b5cf6', light: '#7c3aed' },
  { name: '--chart-2', dark: '#f59e0b', light: '#d97706' },
  { name: '--chart-3', dark: '#10b981', light: '#059669' },
  { name: '--chart-4', dark: '#ec4899', light: '#db2777' },
  { name: '--chart-5', dark: '#06b6d4', light: '#0891b2' },
  { name: '--chart-6', dark: '#a3e635', light: '#65a30d' },
  { name: '--font-sans', dark: 'ui-sans-serif, system-ui, sans-serif', light: 'ui-sans-serif, system-ui, sans-serif' },
  { name: '--font-mono', dark: 'ui-monospace, SFMono-Regular, Menlo, monospace', light: 'ui-monospace, SFMono-Regular, Menlo, monospace' },
  { name: '--radius', dark: '8px', light: '8px' },
];

// pageTheme is the theme for mode, reading the app's properties through read
// (the window's computed style), each falling back to its default.
export function pageTheme(mode: 'dark' | 'light', read: (property: string) => string = () => ''): PageTheme {
  const vars: Record<string, string> = {};
  for (const v of PAGE_VARIABLES) vars[v.name] = (v.from && read(v.from).trim()) || v[mode];
  return { mode, vars };
}

// themeCss is a theme as the rule the page's theme style holds. A property
// whose name or value could reach out of the rule is left out.
export function themeCss(theme: PageTheme): string {
  let css = `:root{color-scheme:${theme.mode === 'light' ? 'light' : 'dark'};`;
  for (const [k, v] of Object.entries(theme.vars)) if (/^--[a-z0-9-]+$/.test(k) && !/[;{}<>\\]/.test(v)) css += `${k}:${v};`;
  return `${css}}`;
}

// themeFragment is the theme a frame starts in, as its URL's fragment: the
// page's own script reads it before the page is first painted, so the page
// opens in the right colours and its document stays the same in both modes.
export const themeFragment = (theme: PageTheme) => `#agentbox-theme=${encodeURIComponent(JSON.stringify(theme))}`;

// openableLink is a link a framed page asked to open, when it is one the
// browser may: http or https, as written.
export function openableLink(url: unknown): string | null {
  if (typeof url !== 'string' || url.length > 4096) return null;
  try {
    const u = new URL(url);
    return u.protocol === 'http:' || u.protocol === 'https:' ? u.href : null;
  } catch {
    return null;
  }
}

// blockedHost is what a page tried to load with no network, for the chat to
// name: a host, or nothing for what isn't a URL (an inline script the policy refused).
export function blockedHost(uri: unknown): string | null {
  if (typeof uri !== 'string') return null;
  try {
    const u = new URL(uri);
    return u.protocol === 'http:' || u.protocol === 'https:' || u.protocol === 'wss:' || u.protocol === 'ws:' ? u.host : null;
  } catch {
    return null;
  }
}

// The page's own script, first in its head. It applies the theme in the
// fragment and those the chat sends, and its zoom; tells the chat its height as it changes;
// hands a clicked http(s) link to the chat to open, as a frame with no
// popups and nowhere to navigate can't; and reports the first error its
// scripts throw and the first thing it was refused for want of network.
const bootstrap =
  `(()=>{const P=parent,R=document.documentElement,S=document.getElementById('agentbox-theme');` +
  `const apply=t=>{if(!t||typeof t!=='object')return;const m=t.mode==='light'?'light':'dark';let c=':root{color-scheme:'+m+';';` +
  `for(const[k,v]of Object.entries(t.vars||{}))if(/^--[a-z0-9-]+$/.test(k)&&typeof v==='string'&&!/[;{}<>\\\\]/.test(v))c+=k+':'+v+';';S.textContent=c+'}';R.dataset.theme=m};` +
  `try{const f=/[#&]agentbox-theme=([^&]*)/.exec(location.hash);if(f)apply(JSON.parse(decodeURIComponent(f[1])))}catch{}` +
  `addEventListener('message',e=>{if(e.source!==P||!e.data)return;const d=e.data;if(d.${THEME_MESSAGE})apply(d.${THEME_MESSAGE});` +
  `const z=d.${ZOOM_MESSAGE};if(typeof z==='number'&&z>=0.25&&z<=4)R.style.zoom=String(z)});` +
  `const post=()=>{const b=document.body;P.postMessage({${HEIGHT_MESSAGE}:Math.max(R.getBoundingClientRect().height,b?b.getBoundingClientRect().bottom:0)},'*')};` +
  `const ro=new ResizeObserver(post);ro.observe(R);addEventListener('DOMContentLoaded',()=>{document.body&&ro.observe(document.body);post()});addEventListener('load',post);` +
  `addEventListener('click',e=>{if(!e.isTrusted||e.defaultPrevented||e.button!==0)return;const a=e.target&&e.target.closest&&e.target.closest('a[href]');if(!a)return;` +
  `let u;try{u=new URL(a.href)}catch{return}if(u.protocol!=='http:'&&u.protocol!=='https:')return;e.preventDefault();P.postMessage({${LINK_MESSAGE}:u.href},'*')});` +
  `let err=0,blk=0;const fail=m=>{if(!err++)P.postMessage({${ERROR_MESSAGE}:String(m).slice(0,300)},'*')};` +
  `addEventListener('error',e=>fail(e.message||'error'));addEventListener('unhandledrejection',e=>fail(e.reason&&e.reason.message||e.reason));` +
  `addEventListener('securitypolicyviolation',e=>{if(/^(https?|wss?):/.test(e.blockedURI)&&!blk++)P.postMessage({${BLOCKED_MESSAGE}:e.blockedURI},'*')})})()`;

// framedPage is a ```html block's page as the chat frames it: in the theme's
// colours (PageTheme) unless it sets its own, telling the chat its height as
// it changes, its links opened by the chat, and its failures reported. With
// no theme, the page is the same in both modes and takes its theme from the
// fragment and the chat's messages; with one, it starts in that one, as a
// frame where no script runs (the phone's srcdoc) needs. What it may load is
// the frame's policy (main/renders.ts).
export function framedPage(html: string, theme?: PageTheme): string {
  const head =
    '<meta charset="utf-8"><meta name="color-scheme" content="dark light">' +
    `<style id="agentbox-theme">${theme ? themeCss(theme) : ''}</style>` +
    '<style>body{margin:0;padding:12px;font:13px/1.5 var(--font-sans,ui-sans-serif,system-ui,sans-serif);color:var(--foreground);background:transparent}a{color:var(--primary)}</style>' +
    `<script>${bootstrap}</script>`;
  return injectHead(html, head);
}

// injectHead puts markup at the start of a page's head, wherever the page
// starts it, so a doctype stays first and the page stays out of quirks mode.
export function injectHead(html: string, markup: string): string {
  for (const re of [/<head\b[^>]*>/i, /<html\b[^>]*>/i, /<!doctype[^>]*>/i]) {
    const m = re.exec(html);
    if (m) return html.slice(0, m.index + m[0].length) + markup + html.slice(m.index + m[0].length);
  }
  return `<!doctype html>${markup}${html}`;
}

// titleOf is a block's title for Hatch: a page's <title> or first heading,
// a diagram's title, or what the block is.
export function titleOf(kind: RichKind, source: string, fallback: string): string {
  const text = (s?: string) =>
    s
      ?.replace(/<[^>]+>/g, '')
      .replace(/\s+/g, ' ')
      .trim()
      .slice(0, 200);
  let found: string | undefined;
  if (kind === 'mermaid') found = /^\s*title\s*[:]?\s*(.+)$/im.exec(source)?.[1]?.replace(/^["']|["']$/g, '');
  else found = /<title[^>]*>([\s\S]*?)<\/title>/i.exec(source)?.[1] ?? /<h[1-3][^>]*>([\s\S]*?)<\/h[1-3]>/i.exec(source)?.[1];
  return text(found) || fallback;
}

// pageOf is a block as one self-contained page, to publish on Hatch: a page
// is itself, with the light theme's properties it may use, a picture or a
// diagram (as drawn, in svg) a page holding it.
export function pageOf(kind: RichKind, source: string, title: string, svg?: string): string {
  if (kind === 'html') return injectHead(source, `<style>${themeCss(pageTheme('light'))}</style>`);
  const esc = title.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]!);
  return (
    `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${esc}</title>` +
    '<style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#fff}svg{max-width:100%;height:auto}</style>' +
    `</head><body>${svgDocument(svg ?? source)}</body></html>`
  );
}

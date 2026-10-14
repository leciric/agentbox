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

// The height a page's frame takes in the chat, at least and at most: past
// that, the page scrolls inside it, and Open large shows it whole.
export const FRAME_MIN = 60;
export const FRAME_MAX = 480;
// A picture's: a diagram is drawn to fit the column, and opened large to read.
export const PICTURE_MAX = 360;

export function frameHeight(reported: unknown, max = FRAME_MAX): number | null {
  if (typeof reported !== 'number' || !Number.isFinite(reported)) return null;
  return Math.min(max, Math.max(FRAME_MIN, Math.ceil(reported)));
}

// The message a framed page sends its height in.
export const HEIGHT_MESSAGE = 'agentboxRenderHeight';

// framedPage is a ```html block's page as the chat frames it: in the app's
// colours unless it sets its own, and telling the chat its height as it
// changes. What it may load is the frame's policy (main/renders.ts).
export function framedPage(html: string, mode: 'dark' | 'light'): string {
  const ink = mode === 'dark' ? '#e4e4e7' : '#27272a';
  const head =
    `<meta charset="utf-8"><meta name="color-scheme" content="${mode}">` +
    `<style>html{color-scheme:${mode}}body{margin:0;padding:12px;font:13px/1.5 ui-sans-serif,system-ui,sans-serif;color:${ink};background:transparent}</style>` +
    `<script>(()=>{const post=()=>parent.postMessage({${HEIGHT_MESSAGE}:document.documentElement.scrollHeight},'*');` +
    `new ResizeObserver(post).observe(document.documentElement);addEventListener('load',post)})()</script>`;
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
// is itself, a picture or a diagram (as drawn, in svg) a page holding it.
export function pageOf(kind: RichKind, source: string, title: string, svg?: string): string {
  if (kind === 'html') return source;
  const esc = title.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]!);
  return (
    `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${esc}</title>` +
    '<style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#fff}svg{max-width:100%;height:auto}</style>' +
    `</head><body>${svgDocument(svg ?? source)}</body></html>`
  );
}

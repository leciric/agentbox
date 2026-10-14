import { createHash } from 'node:crypto';

// Renders are the HTML pages a chat draws from a ```html preview block
// (renderer/components/chat/RichBlock.tsx), served at
// agentbox-media://render/<key> to a frame sandboxed without
// allow-same-origin. They can't be the frame's srcdoc: a srcdoc document takes
// the app's own Content-Security-Policy, which runs no inline script. Served
// from here, the page gets a policy of its own: its inline scripts and styles,
// data: pictures and fonts, and no network unless the user allowed it on that
// block. The app's frame-src lets the frame navigate only to agentbox-media:,
// so a page can't leave by navigating either.

// The most pages kept, and the most HTML: the oldest go first. A page the
// chat shows again after that is registered again.
const MAX_RENDERS = 64;
const MAX_BYTES = 32 << 20;

const renders = new Map<string, { html: string; network: boolean }>();
let bytes = 0;

// renderPolicy is a page's Content-Security-Policy, with or without network.
export function renderPolicy(network: boolean): string {
  const net = network ? ' https:' : '';
  return [
    "default-src 'none'",
    `script-src 'unsafe-inline' 'unsafe-eval'${net}`,
    `style-src 'unsafe-inline'${net}`,
    `img-src data: blob:${net}`,
    `font-src data:${net}`,
    `media-src data: blob:${net}`,
    `connect-src ${network ? 'https:' : "'none'"}`,
    "form-action 'none'",
    "base-uri 'none'",
    "object-src 'none'",
    'sandbox allow-scripts',
  ].join('; ');
}

// registerRender keeps a page and answers with where to frame it from: the
// same page, with the same network, has the same address.
export function registerRender(html: string, network: boolean): string {
  const key = createHash('sha256')
    .update(network ? 'net\0' : 'none\0')
    .update(html)
    .digest('hex')
    .slice(0, 32);
  const had = renders.get(key);
  if (had) renders.delete(key);
  else bytes += html.length;
  renders.set(key, { html, network });
  for (const [k, r] of renders) {
    if (renders.size <= MAX_RENDERS && bytes <= MAX_BYTES) break;
    if (k === key) continue;
    renders.delete(k);
    bytes -= r.html.length;
  }
  return `agentbox-media://render/${key}`;
}

// renderResponse answers agentbox-media://render/<key>.
export function renderResponse(key: string): Response {
  const r = renders.get(key);
  if (!r) return new Response('not found', { status: 404 });
  return new Response(r.html, {
    headers: {
      'Content-Type': 'text/html; charset=utf-8',
      'Content-Security-Policy': renderPolicy(r.network),
      'X-Content-Type-Options': 'nosniff',
      'Referrer-Policy': 'no-referrer',
      'Cache-Control': 'no-store',
    },
  });
}

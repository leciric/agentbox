// Rich renders in a chat's markdown: which fenced blocks are drawn rather
// than shown as code, and the page each is drawn in (Markdown.tsx).

// richKind says which rich render a fenced block's info string asks for.
export function richKind(lang: string | undefined): 'svg' | 'html' | 'mermaid' | undefined {
  const words = (lang ?? '').trim().toLowerCase().split(/\s+/);
  if (words[0] === 'svg') return 'svg';
  if (words[0] === 'html' && words.includes('preview')) return 'html';
  if (words[0] === 'mermaid') return 'mermaid';
  return undefined;
}

// The frame's page: what the block holds, fitted to the phone's width, and a
// policy that lets nothing load or run.
export function framePage(kind: 'svg' | 'html', body: string): string {
  const csp = "default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:";
  const fit = kind === 'svg' ? 'svg{max-width:100%;height:auto;display:block;margin:auto}' : '';
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${csp}"><meta name="viewport" content="width=device-width,initial-scale=1"><style>html,body{margin:0;background:#fff;font-family:system-ui,sans-serif}${fit}</style></head><body>${body}</body></html>`;
}

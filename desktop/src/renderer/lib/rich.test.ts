// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { blockedHost, FRAME_MAX, FRAME_MIN, framedPage, frameHeight, injectHead, openableLink, openFence, pageOf, pageTheme, richKind, svgDocument, svgSize, svgUrl, themeCss, themeFragment, titleOf } from './rich.ts';

test('mermaid and svg blocks are drawn; html only when its fence asks for a preview', () => {
  assert.deepEqual(richKind('mermaid'), { kind: 'mermaid', drawn: true });
  assert.deepEqual(richKind('SVG'), { kind: 'svg', drawn: true });
  assert.deepEqual(richKind('html'), { kind: 'html', drawn: false });
  assert.deepEqual(richKind('html', 'preview'), { kind: 'html', drawn: true });
  assert.deepEqual(richKind('html', 'title="x" render'), { kind: 'html', drawn: true });
  assert.equal(richKind('ts'), null);
  assert.equal(richKind(undefined), null);
});

test('an svg gets its namespace, which an <img> needs, once', () => {
  assert.equal(svgDocument('<svg viewBox="0 0 1 1"></svg>'), '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"></svg>');
  const ok = '<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>';
  assert.equal(svgDocument(ok), ok);
  assert.ok(svgUrl('<svg/>').startsWith('data:image/svg+xml;charset=utf-8,%3Csvg%20xmlns'));
});

test('an svg is as big as it says, or as its viewBox', () => {
  assert.deepEqual(svgSize('<svg width="120" height="40px">'), { width: 120, height: 40 });
  assert.deepEqual(svgSize('<svg width="100%" viewBox="0 0 640 480" style="max-width: 640px">'), { width: 640, height: 480 });
  assert.equal(svgSize('<svg>'), null);
  assert.equal(svgSize('no svg'), null);
});

test('a frame takes the height its page reports, within bounds', () => {
  assert.equal(frameHeight(10), FRAME_MIN);
  assert.equal(frameHeight(200.2), 201);
  assert.equal(frameHeight(5000), FRAME_MAX);
  assert.equal(frameHeight('300'), null);
  assert.equal(frameHeight(Number.NaN), null);
});

test('the chat’s head goes inside the page’s head, after its doctype', () => {
  assert.equal(injectHead('<!DOCTYPE html><html><head><title>x</title></head></html>', 'X'), '<!DOCTYPE html><html><head>X<title>x</title></head></html>');
  assert.equal(injectHead('<!doctype html><html lang="en"><body>b</body></html>', 'X'), '<!doctype html><html lang="en">X<body>b</body></html>');
  assert.equal(injectHead('<!doctype html><p>a</p>', 'X'), '<!doctype html>X<p>a</p>');
  assert.equal(injectHead('<p>a</p>', 'X'), '<!doctype html>X<p>a</p>');
  const page = framedPage('<p>hi</p>');
  assert.ok(page.startsWith('<!doctype html><meta charset="utf-8"><meta name="color-scheme" content="dark light"><style id="agentbox-theme"></style>'));
  assert.match(page, /agentboxRenderHeight/);
  assert.ok(page.endsWith('<p>hi</p>'));
  // The page is the same in both modes; a frame whose scripts don't run gets its theme written in.
  assert.match(framedPage('<p>hi</p>', pageTheme('light')), /<style id="agentbox-theme">:root\{color-scheme:light;--background:#ffffff;/);
});

test('a block’s title is its page’s title or heading, its diagram’s title, or what it is', () => {
  assert.equal(titleOf('html', '<title> Q3  report </title><h1>No</h1>', 'Page'), 'Q3 report');
  assert.equal(titleOf('html', '<h2>Sales <em>by</em> month</h2>', 'Page'), 'Sales by month');
  assert.equal(titleOf('mermaid', 'pie\n  title "Pets adopted"\n  "Dogs": 3', 'Diagram'), 'Pets adopted');
  assert.equal(titleOf('mermaid', 'graph TD\nA-->B', 'Diagram'), 'Diagram');
  assert.equal(titleOf('svg', '<svg/>', 'Picture'), 'Picture');
});

test('a picture published is a page holding it; a page is itself', () => {
  assert.equal(pageOf('html', '<!doctype html><p>x</p>', 'T'), `<!doctype html><style>${themeCss(pageTheme('light'))}</style><p>x</p>`);
  const page = pageOf('mermaid', 'graph TD', 'A <b> & "c"', '<svg viewBox="0 0 1 1"></svg>');
  assert.match(page, /<title>A &lt;b&gt; &amp; &quot;c&quot;<\/title>/);
  assert.match(page, /<body><svg xmlns="http:\/\/www.w3.org\/2000\/svg" viewBox="0 0 1 1"><\/svg><\/body>/);
  assert.match(pageOf('svg', '<svg><circle/></svg>', 'T'), /<circle\/>/);
});

test('openFence finds a renderable block still arriving', () => {
  const o = openFence('Here:\n\n```html preview\n<h1>Hi</h1>\n<p>x');
  assert.equal(o?.kind, 'html');
  assert.equal(o?.before, 'Here:\n\n');
  assert.equal(o?.code, '<h1>Hi</h1>\n<p>x');
  assert.equal(o?.lines, 2);
  assert.equal(o?.bytes, 16);
  assert.equal(openFence('```mermaid')?.kind, 'mermaid');
  assert.equal(openFence('```mermaid')?.lines, 0);
  assert.equal(openFence('~~~svg\n<svg>')?.kind, 'svg');
});

test('openFence ignores closed, plain and other blocks', () => {
  assert.equal(openFence('```mermaid\ngraph TD\n```\n'), null);
  assert.equal(openFence('```html\n<p>x'), null);
  assert.equal(openFence('```go\nfunc'), null);
  assert.equal(openFence('no fence at all'), null);
  assert.equal(openFence('```mermaid\na\n```\n\n```svg\n<svg'  )?.kind, 'svg');
  // A longer fence is closed only by one as long; a shorter one is content.
  assert.equal(openFence('````svg\n```\n<svg>')?.kind, 'svg');
  assert.equal(openFence('````svg\n<svg>\n````'), null);
});

test('a page’s theme is the app’s colours under shadcn’s names, with defaults for the rest', () => {
  const app: Record<string, string> = { '--ab-chat': ' #101010 ', '--ab-brand-500': '#ff0066' };
  const dark = pageTheme('dark', (p) => app[p] ?? '');
  assert.equal(dark.mode, 'dark');
  assert.equal(dark.vars['--background'], '#101010');
  assert.equal(dark.vars['--primary'], '#ff0066');
  assert.equal(dark.vars['--chart-1'], '#ff0066');
  assert.equal(dark.vars['--foreground'], '#e4e4e7');
  assert.equal(pageTheme('light').vars['--foreground'], '#27272a');
  for (const n of [1, 2, 3, 4, 5, 6]) assert.ok(dark.vars[`--chart-${n}`]);
});

test('a theme’s rule leaves out what could reach outside it', () => {
  const css = themeCss({ mode: 'light', vars: { '--ok': 'red', '--bad': 'red;}body{display:none', '--x</style>': 'red', '--esc': 'a\\b', Upper: 'red' } });
  assert.equal(css, ':root{color-scheme:light;--ok:red;}');
  assert.equal(themeCss({ mode: 'evil' as 'dark', vars: {} }), ':root{color-scheme:dark;}');
});

test('a frame opens only http(s) links, and names the host a page reached for', () => {
  assert.equal(openableLink('https://example.com/a?b#c'), 'https://example.com/a?b#c');
  assert.equal(openableLink('http://localhost:3000'), 'http://localhost:3000/');
  for (const bad of ['javascript:alert(1)', 'file:///etc/passwd', 'agentbox-media://render/x', 'not a url', 42, null, `https://x.com/${'a'.repeat(5000)}`]) assert.equal(openableLink(bad), null);
  assert.equal(blockedHost('https://cdn.jsdelivr.net/npm/chart.js'), 'cdn.jsdelivr.net');
  assert.equal(blockedHost('wss://live.example:8443/s'), 'live.example:8443');
  assert.equal(blockedHost('inline'), null);
  assert.equal(blockedHost('data:image/png;base64,x'), null);
});

// runPage runs a framed page's own script against a fake window: what it
// posts to the chat, and the handlers it set.
function runPage(hash = '') {
  const script = /<script>([\s\S]*?)<\/script>/.exec(framedPage('<p>x</p>'))![1];
  const posted: Record<string, unknown>[] = [];
  const handlers: Record<string, (e: unknown) => void> = {};
  const parent = { postMessage: (m: Record<string, unknown>) => posted.push(m) };
  const style = { textContent: '' };
  const root = { dataset: {} as Record<string, string>, style: {} as Record<string, string>, getBoundingClientRect: () => ({ height: 321.5 }) };
  const document = { documentElement: root, body: null, getElementById: (id: string) => (id === 'agentbox-theme' ? style : null) };
  class ResizeObserver {
    observe() {}
  }
  new Function('parent', 'document', 'location', 'addEventListener', 'ResizeObserver', script)(parent, document, { hash }, (type: string, fn: (e: unknown) => void) => (handlers[type] = fn), ResizeObserver);
  return { posted, handlers, style, root, parent };
}

test('a framed page takes its theme from its fragment and the chat, and only the chat', () => {
  const theme = pageTheme('light');
  const page = runPage(themeFragment(theme));
  assert.equal(page.style.textContent, themeCss(theme));
  assert.equal(page.root.dataset.theme, 'light');
  const dark = { mode: 'dark', vars: { '--foreground': '#fff', '--bad': 'x;}' } };
  page.handlers.message({ source: {}, data: { agentboxTheme: dark } });
  assert.equal(page.root.dataset.theme, 'light');
  page.handlers.message({ source: page.parent, data: { agentboxTheme: dark } });
  assert.equal(page.style.textContent, ':root{color-scheme:dark;--foreground:#fff;}');
  page.handlers.message({ source: page.parent, data: { agentboxZoom: 1.5 } });
  assert.equal(page.root.style.zoom, '1.5');
  page.handlers.message({ source: page.parent, data: { agentboxZoom: 40 } });
  assert.equal(page.root.style.zoom, '1.5');
  // A broken fragment leaves the defaults.
  assert.equal(runPage('#agentbox-theme=%7Bnope').style.textContent, '');
});

test('a framed page tells its height, hands the chat its links, and reports its first failures', () => {
  const page = runPage();
  page.handlers.load({});
  assert.deepEqual(page.posted.shift(), { agentboxRenderHeight: 321.5 });
  const click = (href: string, more: Record<string, unknown> = {}) => {
    let prevented = false;
    const a = { href };
    page.handlers.click({ isTrusted: true, defaultPrevented: false, button: 0, target: { closest: () => a }, preventDefault: () => (prevented = true), ...more });
    return prevented;
  };
  assert.equal(click('https://example.com/'), true);
  assert.deepEqual(page.posted.shift(), { agentboxOpenLink: 'https://example.com/' });
  // In-page anchors, other schemes, scripted clicks and handled ones stay the page's.
  assert.equal(click('agentbox-media://render/k#top'), false);
  assert.equal(click('javascript:void 0'), false);
  assert.equal(click('https://example.com/', { isTrusted: false }), false);
  assert.equal(click('https://example.com/', { defaultPrevented: true }), false);
  assert.equal(page.posted.length, 0);
  page.handlers.error({ message: 'x is not defined' });
  page.handlers.error({ message: 'second' });
  page.handlers.unhandledrejection({ reason: new Error('third') });
  assert.deepEqual(page.posted, [{ agentboxError: 'x is not defined' }]);
  page.posted.length = 0;
  page.handlers.securitypolicyviolation({ blockedURI: 'inline' });
  page.handlers.securitypolicyviolation({ blockedURI: 'https://cdn.example/chart.js' });
  page.handlers.securitypolicyviolation({ blockedURI: 'https://other.example/x' });
  assert.deepEqual(page.posted, [{ agentboxBlocked: 'https://cdn.example/chart.js' }]);
});

// Thumbnails of the pages a project's chats published on Hatch: the page's
// HTML, read from the daemon's /page route as the preview frames it, drawn
// in a window nobody sees and snapshotted. Each is kept on disk by the page's
// id and version, which Hatch never changes, so a page is drawn once.
//
// The window is as shut in as the preview's frame, and more: its own
// in-memory session (no cookies, no storage, none of the app's protocols),
// no Node, a sandboxed renderer, the daemon's sandboxing
// Content-Security-Policy on the page, no popups, no navigation, no sound.
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { app, BrowserWindow, session, type Session } from 'electron';
import { requestOptions } from './connection';

export const thumbScheme = 'agentbox-pagethumb';

// What a page is drawn at, and what its thumbnail keeps: a laptop's window,
// scaled to a width the Pages tab and the Media grids show sharply.
const pageSize = { width: 1280, height: 800 };
const thumbWidth = 480;
// How long a page gets to settle after it loads (fonts, a CDN's styles, an
// entrance animation), and to load at all.
const settleMs = 900;
const loadTimeoutMs = 12_000;
const maxPageBytes = 25 << 20;
// Two at a time: a list of hundreds asks for the ones on screen, and each is
// a renderer process for a moment.
const concurrency = 2;

const pagePath = /^\/v1\/projects\/[^/]+\/artifacts\/[A-Za-z0-9_-]+\/page$/;
const keyPattern = /^[A-Za-z0-9_-]+@\d+$/;

let thumbSession: Session | undefined;
// The page each drawing window is showing, by its token in the URL.
const pending = new Map<string, { html: string; csp: string }>();

function ownSession(): Session {
  if (thumbSession) return thumbSession;
  thumbSession = session.fromPartition('agentbox-pagethumbs', { cache: false });
  thumbSession.setPermissionRequestHandler((_wc, _permission, callback) => callback(false));
  thumbSession.protocol.handle(thumbScheme, (request) => {
    const page = pending.get(new URL(request.url).hostname);
    if (!page) return new Response('not found', { status: 404 });
    return new Response(page.html, {
      headers: { 'Content-Type': 'text/html; charset=utf-8', 'Content-Security-Policy': page.csp, 'X-Content-Type-Options': 'nosniff' },
    });
  });
  return thumbSession;
}

// fetchPage reads a page's HTML and the policy it's framed with from the
// daemon, or null when the daemon can't hand it over (Hatch doesn't give
// AgentBox its pages' HTML yet, the page expired, the daemon is away).
function fetchPage(path: string): Promise<{ html: string; csp: string } | null> {
  const { module, options } = requestOptions(path);
  return new Promise((resolve) => {
    const req = module.request({ ...options, method: 'GET' }, (res) => {
      if (res.statusCode !== 200) {
        res.resume();
        return resolve(null);
      }
      const chunks: Buffer[] = [];
      let size = 0;
      res.on('data', (chunk: Buffer) => {
        size += chunk.length;
        if (size > maxPageBytes) {
          req.destroy();
          return resolve(null);
        }
        chunks.push(chunk);
      });
      res.on('end', () => {
        const csp = res.headers['content-security-policy'];
        resolve({ html: Buffer.concat(chunks).toString('utf8'), csp: typeof csp === 'string' ? csp : "default-src 'none'; style-src 'unsafe-inline'; sandbox" });
      });
      res.on('error', () => resolve(null));
    });
    req.setTimeout(loadTimeoutMs, () => req.destroy());
    req.on('error', () => resolve(null));
    req.end();
  });
}

// snapshot draws html offscreen and gives back a JPEG of its first screen.
export async function snapshot(html: string, csp: string): Promise<Buffer | null> {
  const token = createHash('sha256').update(`${Date.now()}:${Math.random()}`).digest('hex').slice(0, 24);
  pending.set(token, { html, csp });
  const win = new BrowserWindow({
    show: false,
    ...pageSize,
    paintWhenInitiallyHidden: true,
    webPreferences: {
      offscreen: true,
      session: ownSession(),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
      webSecurity: true,
      spellcheck: false,
      backgroundThrottling: false,
    },
  });
  try {
    const wc = win.webContents;
    wc.setAudioMuted(true);
    wc.setWindowOpenHandler(() => ({ action: 'deny' }));
    wc.on('will-navigate', (event) => event.preventDefault());
    wc.on('will-redirect', (event) => event.preventDefault());
    const loaded = new Promise<void>((resolve, reject) => {
      wc.once('did-finish-load', () => resolve());
      wc.once('did-fail-load', (_e, code, desc) => reject(new Error(`${code} ${desc}`)));
      setTimeout(() => resolve(), loadTimeoutMs);
    });
    await win.loadURL(`${thumbScheme}://${token}/`).catch(() => {});
    await loaded;
    await new Promise((r) => setTimeout(r, settleMs));
    if (win.isDestroyed()) return null;
    const image = await wc.capturePage({ x: 0, y: 0, ...pageSize });
    if (image.isEmpty()) return null;
    return image.resize({ width: thumbWidth, quality: 'good' }).toJPEG(82);
  } catch {
    return null;
  } finally {
    pending.delete(token);
    if (!win.isDestroyed()) win.destroy();
  }
}

// The queue: requests for the same thumbnail share one drawing.
const inFlight = new Map<string, Promise<string | null>>();
const queue: (() => void)[] = [];
let running = 0;

function limited<V>(fn: () => Promise<V>): Promise<V> {
  return new Promise<V>((resolve, reject) => {
    const run = () => {
      running++;
      fn()
        .then(resolve, reject)
        .finally(() => {
          running--;
          queue.shift()?.();
        });
    };
    if (running < concurrency) run();
    else queue.push(run);
  });
}

const cacheDir = () => join(app.getPath('userData'), 'page-thumbs');
const dataUrl = (jpeg: Buffer) => `data:image/jpeg;base64,${jpeg.toString('base64')}`;

// pageThumb is a page's thumbnail as a data: URL, from the disk when it was
// drawn before, or null when the page's HTML can't be had.
export function pageThumb(path: string, key: string): Promise<string | null> {
  if (!pagePath.test(path) || !keyPattern.test(key)) return Promise.resolve(null);
  const known = inFlight.get(key);
  if (known) return known;
  const file = join(cacheDir(), `${createHash('sha256').update(key).digest('hex').slice(0, 32)}.jpg`);
  const work = (async () => {
    const cached = await readFile(file).catch(() => null);
    if (cached) return dataUrl(cached);
    const jpeg = await limited(async () => {
      const page = await fetchPage(path);
      return page && snapshot(page.html, page.csp);
    });
    if (!jpeg) return null;
    await mkdir(cacheDir(), { recursive: true }).catch(() => {});
    await writeFile(file, jpeg).catch(() => {});
    return dataUrl(jpeg);
  })().finally(() => inFlight.delete(key));
  inFlight.set(key, work);
  return work;
}

// Serves media to the renderer as agentbox-media://media/<id>[/<path in a directory>],
// streamed from the daemon with range requests, so videos can seek and HTML
// reports can load their own files; and the pictures sent in a chat, as
// agentbox-media://api/v1/.../chat/images/<id>; and the pictures in a pull
// request's description, as agentbox-media://api/v1/projects/<name>/pulls/image?src=<url>.
import { Readable } from 'node:stream';
import { protocol } from 'electron';
import { requestOptions } from './connection';
import { apiPath } from './mediapaths';
import { thumbScheme } from './pagethumbs';
import { renderResponse } from './renders';

export const mediaScheme = 'agentbox-media';

// registerMediaScheme must run before the app is ready.
export function registerMediaScheme(): void {
  protocol.registerSchemesAsPrivileged([
    { scheme: mediaScheme, privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, stream: true } },
    // A Hatch page drawn for its thumbnail, in its own session (main/pagethumbs.ts).
    { scheme: thumbScheme, privileges: { standard: true, secure: true } },
  ]);
}

// The page comes from file://, so reading a file's text with fetch() is a
// cross-origin request: without these headers it fails, while images and
// videos, which don't need them, still load.
const cors = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Headers': 'Range',
  'Access-Control-Expose-Headers': 'Content-Length, Content-Range, Content-Type',
};

export function handleMedia(): void {
  protocol.handle(mediaScheme, (request) => {
    if (request.method === 'OPTIONS') return new Response(null, { status: 204, headers: { ...cors, 'Access-Control-Allow-Methods': 'GET' } });
    const url = new URL(request.url);
    // A page a chat rendered (main/renders.ts), which is the app's own, not the daemon's.
    if (url.host === 'render') return renderResponse(url.pathname.replace(/^\//, ''));
    let path: string;
    if (url.host === 'api') {
      // A picture sent in a chat or in a pull request, or a Hatch page, and nothing else of the daemon's API.
      const allowed = apiPath(url);
      if (allowed === null) return new Response('not found', { status: 404, headers: cors });
      path = allowed;
    } else {
      const [id = '', ...rest] = url.pathname.replace(/^\//, '').split('/');
      const sub = rest.map(decodeURIComponent).join('/');
      path = `/v1/media/${encodeURIComponent(decodeURIComponent(id))}/file${sub ? `?path=${encodeURIComponent(sub)}` : ''}`;
    }
    const headers: Record<string, string> = {};
    const range = request.headers.get('range');
    if (range) headers.Range = range;

    const { module, options } = requestOptions(path);
    return new Promise<Response>((resolve) => {
      const req = module.request({ ...options, method: 'GET', headers: { ...(options.headers as Record<string, string>), ...headers } }, (res) => {
        const out = new Headers(cors);
        for (const [name, value] of Object.entries(res.headers)) {
          if (typeof value === 'string') out.set(name, value);
          else if (Array.isArray(value)) out.set(name, value.join(', '));
        }
        const body = res.statusCode === 204 ? null : (Readable.toWeb(res) as ReadableStream);
        resolve(new Response(body, { status: res.statusCode ?? 502, headers: out }));
      });
      req.on('error', (err) => resolve(new Response(err.message, { status: 502, headers: cors })));
      req.end();
    });
  });
}

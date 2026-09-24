// Serves media to the renderer as agentbox-media://media/<id>[/<path in a directory>],
// streamed from the daemon with range requests, so videos can seek and HTML
// reports can load their own files; and the pictures sent in a chat, as
// agentbox-media://api/v1/.../chat/images/<id>.
import { Readable } from 'node:stream';
import { protocol } from 'electron';
import { requestOptions } from './connection';

export const mediaScheme = 'agentbox-media';

// registerMediaScheme must run before the app is ready.
export function registerMediaScheme(): void {
  protocol.registerSchemesAsPrivileged([
    { scheme: mediaScheme, privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, stream: true } },
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

// chatImage is the one daemon path agentbox-media://api/... reaches: a picture
// sent in an agent's chat, or in a project's.
const chatImage = /^\/v1\/(projects\/[^/]+|agents\/[^/]+\/[^/]+)\/chat\/images\/[0-9a-f]{16}$/;

export function handleMedia(): void {
  protocol.handle(mediaScheme, (request) => {
    if (request.method === 'OPTIONS') return new Response(null, { status: 204, headers: { ...cors, 'Access-Control-Allow-Methods': 'GET' } });
    const url = new URL(request.url);
    let path: string;
    if (url.host === 'api') {
      // A picture sent in a chat, and nothing else of the daemon's API.
      if (!chatImage.test(url.pathname)) return new Response('not found', { status: 404, headers: cors });
      path = url.pathname;
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

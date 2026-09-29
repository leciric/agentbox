// Fetching what a voice needs the first time it's used, and keeping it: the
// browser's Cache Storage, which transformers.js keeps Kokoro's weights in
// too, so none of it is fetched twice.

export type Progress = (p: { file: string; loaded: number; total: number }) => void;

const cacheName = 'agentbox-voice';

// cachedFetch returns the file at url, from the cache when it's there, and
// only if its SHA-256 is the one given.
export async function cachedFetch(file: { url: string; sha256: string }, progress: Progress): Promise<ArrayBuffer> {
  const name = file.url.slice(file.url.lastIndexOf('/') + 1);
  const cache = await caches.open(cacheName).catch(() => undefined);
  const hit = await cache?.match(file.url);
  if (hit) {
    const body = await hit.arrayBuffer();
    if ((await sha256(body)) === file.sha256) {
      progress({ file: name, loaded: body.byteLength, total: body.byteLength });
      return body;
    }
    await cache?.delete(file.url);
  }

  const response = await fetch(file.url);
  if (!response.ok || !response.body) throw new Error(`Could not download ${name}: ${response.status} ${response.statusText}`);
  const total = Number(response.headers.get('content-length')) || 0;
  const chunks: Uint8Array[] = [];
  let loaded = 0;
  const reader = response.body.getReader();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
    loaded += value.byteLength;
    progress({ file: name, loaded, total: Math.max(total, loaded) });
  }
  const body = new Uint8Array(loaded);
  let at = 0;
  for (const chunk of chunks) {
    body.set(chunk, at);
    at += chunk.byteLength;
  }
  if ((await sha256(body.buffer)) !== file.sha256) throw new Error(`${name} is not the file AgentBox expects: not using it`);
  await cache?.put(file.url, new Response(body.slice())).catch(() => {});
  return body.buffer;
}

async function sha256(body: ArrayBuffer): Promise<string> {
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', body));
  return Array.from(digest, (b) => b.toString(16).padStart(2, '0')).join('');
}

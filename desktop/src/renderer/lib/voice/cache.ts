// What of each speech-to-text model is downloaded, from transformers.js's own
// Cache Storage, where it keeps every file it fetched under its URL.
import { voiceModels } from './models';

const cacheName = 'transformers-cache';

// modelOf is which model a cached URL belongs to, by its repository's path.
function modelOf(url: string): string | undefined {
  return voiceModels.find((m) => url.includes(`/${m.repo}/`))?.id;
}

// cachedSizes is the bytes each model has in the cache.
export async function cachedSizes(): Promise<Record<string, number>> {
  const sizes: Record<string, number> = {};
  if (typeof caches === 'undefined') return sizes;
  const cache = await caches.open(cacheName);
  for (const request of await cache.keys()) {
    const id = modelOf(request.url);
    if (!id) continue;
    const response = await cache.match(request);
    const length = Number(response?.headers.get('content-length'));
    // A response saved without its length is measured, which reads it.
    const size = Number.isFinite(length) && length > 0 ? length : ((await response?.blob())?.size ?? 0);
    sizes[id] = (sizes[id] ?? 0) + size;
  }
  return sizes;
}

export async function clearCached(id: string): Promise<void> {
  if (typeof caches === 'undefined') return;
  const cache = await caches.open(cacheName);
  for (const request of await cache.keys()) if (modelOf(request.url) === id) await cache.delete(request);
}

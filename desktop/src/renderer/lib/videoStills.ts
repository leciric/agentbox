// A recording's tile in a media grid shows a still of its frame at 0.5 s. It
// used to be a <video preload="metadata"> per tile: one media player and one
// open stream each, all at once. With a few hundred recordings over the VM's
// link, every stream stalled half-read and the renderer with them, so tiles
// stayed on their first frame or blank and no recording would play. Now a
// tile asks for its still only once it's on screen, a few at a time; the
// frame is drawn into a JPEG and the player let go.

// limiter runs at most `limit` tasks at a time, in the order they were asked
// for. A task whose signal is aborted while it waits is dropped without
// running, so a tile scrolled past doesn't hold up the ones in view.
export function limiter(limit: number) {
  let running = 0;
  const waiting: { start: () => void; signal?: AbortSignal }[] = [];
  const next = () => {
    while (running < limit && waiting.length > 0) {
      const task = waiting.shift()!;
      if (!task.signal?.aborted) task.start();
    }
  };
  return function run<T>(fn: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const start = () => {
        running++;
        fn()
          .then(resolve, reject)
          .finally(() => {
            running--;
            next();
          });
      };
      signal?.addEventListener('abort', () => reject(signal.reason), { once: true });
      waiting.push({ start, signal });
      next();
    });
  };
}

// Unplayable is a recording Chromium can't decode: cut short with no moov
// box, or damaged. Anything else (a stream that timed out) may load next time.
export class Unplayable extends Error {}

// unplayable says whether a <video>'s error is the file's, not the stream's.
export function unplayable(error: MediaError | null): boolean {
  return error?.code === MediaError.MEDIA_ERR_DECODE || error?.code === MediaError.MEDIA_ERR_SRC_NOT_SUPPORTED;
}

const stills = new Map<string, string>();
const queue = limiter(3);

// cachedStill is the still already made for a media item, if any.
export function cachedStill(id: string): string | undefined {
  return stills.get(id);
}

// videoStill is a JPEG of the recording's frame at 0.5 s,
// made once per item: a media item's file never changes.
export function videoStill(id: string, url: string, signal: AbortSignal): Promise<string> {
  const have = stills.get(id);
  if (have) return Promise.resolve(have);
  // The dev preview's fixtures have no file for a recording.
  if (!url) return Promise.reject(new Error('no file'));
  return queue(async () => {
    const still = await grabFrame(url, signal);
    stills.set(id, still);
    return still;
  }, signal);
}

async function grabFrame(url: string, signal: AbortSignal): Promise<string> {
  const video = document.createElement('video');
  video.muted = true;
  video.preload = 'auto';
  // The file comes from agentbox-media://, another origin than the page:
  // without CORS the canvas would be tainted and give no picture.
  video.crossOrigin = 'anonymous';
  try {
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('timed out')), 30_000);
      const done = (fn: () => void) => () => {
        clearTimeout(timer);
        fn();
      };
      video.addEventListener('loadedmetadata', () => (video.currentTime = Math.min(0.5, video.duration / 2 || 0)), { once: true });
      video.addEventListener('seeked', done(resolve), { once: true });
      video.addEventListener('error', done(() => reject(unplayable(video.error) ? new Unplayable(video.error?.message) : new Error(video.error?.message))), { once: true });
      signal.addEventListener('abort', done(() => reject(signal.reason)), { once: true });
      video.src = url;
    });
    const width = Math.min(480, video.videoWidth);
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = Math.round((video.videoHeight * width) / video.videoWidth);
    canvas.getContext('2d')!.drawImage(video, 0, 0, canvas.width, canvas.height);
    // A data: URL, which the page's CSP lets an <img> show (blob: it doesn't).
    return canvas.toDataURL('image/jpeg', 0.8);
  } finally {
    // Ends the stream and frees the player now, not when it's collected.
    video.removeAttribute('src');
    video.load();
  }
}

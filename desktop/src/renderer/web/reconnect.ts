// reconnector decides when the web app follows its event stream again from
// scratch (web/bridge.ts), where EventSource's own retries fall short:
//
//   - an answer that isn't the stream, like a tunnel's 502 while the daemon
//     is away, ends EventSource's retries for good (failed): try again a
//     little later each time, up to every 15 seconds;
//   - a phone that slept, or a tunnel that dropped without closing, leaves a
//     stream that looks open and carries nothing: a stream that pings and
//     went silent for longer than silentAfter is replaced (heard keeps it);
//   - coming back to the page after a while, the network coming back, or the
//     browser restoring the page from its cache: the stream may not have
//     survived, so it is followed again at once rather than at the next try.
//
// It never follows on its own before the first follow: that is up to the
// page (a phone follows only once it is paired).
export interface ReconnectorOptions {
  follow: () => void;
  connected: () => boolean;
  silentAfter: () => number | undefined;
  // For tests: the clock, the timers, and the page.
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
  every?: (fn: () => void, ms: number) => void;
  page?: Pick<Document, 'visibilityState' | 'addEventListener'>;
  window?: Pick<Window, 'addEventListener'>;
}

export interface Reconnector {
  // started says the page follows the stream: from now on, follow it again when needed.
  started: () => void;
  // opened says the stream is up: the next failure starts the backoff over.
  opened: () => void;
  // heard says the stream carried something, an event or a ping.
  heard: () => void;
  // failed says EventSource gave up: follow again after the backoff.
  failed: () => void;
  // now follows again at once.
  now: () => void;
}

// A page hidden for longer than this may have lost its stream to the
// phone's sleep without hearing about it.
const awayFor = 15_000;
const maxBackoff = 15_000;

export function reconnector(opts: ReconnectorOptions): Reconnector {
  const now = opts.now ?? Date.now;
  const setTimer = opts.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
  const clearTimer = opts.clearTimer ?? ((timer) => clearTimeout(timer as ReturnType<typeof setTimeout>));
  const every = opts.every ?? ((fn, ms) => void setInterval(fn, ms));
  const page = opts.page ?? (typeof document === 'undefined' ? undefined : document);
  const win = opts.window ?? (typeof window === 'undefined' ? undefined : window);

  let following = false;
  let failures = 0;
  let timer: unknown;
  let lastHeard = now();
  let hiddenAt: number | undefined;

  const follow = () => {
    if (!following) return;
    if (timer !== undefined) clearTimer(timer);
    timer = undefined;
    lastHeard = now();
    opts.follow();
  };
  const silent = () => {
    const after = opts.silentAfter();
    return after !== undefined && now() - lastHeard > after;
  };

  every(() => {
    if (following && page?.visibilityState !== 'hidden' && opts.connected() && silent()) follow();
  }, 5_000);
  page?.addEventListener('visibilitychange', () => {
    if (page.visibilityState === 'hidden') {
      hiddenAt = now();
      return;
    }
    const away = hiddenAt !== undefined && now() - hiddenAt > awayFor;
    hiddenAt = undefined;
    if (away || !opts.connected() || silent()) {
      failures = 0;
      follow();
    }
  });
  win?.addEventListener('online', () => {
    if (!opts.connected()) {
      failures = 0;
      follow();
    }
  });
  win?.addEventListener('pageshow', (event) => {
    if ((event as PageTransitionEvent).persisted) follow();
  });

  return {
    started: () => {
      following = true;
    },
    opened: () => {
      failures = 0;
      lastHeard = now();
    },
    heard: () => {
      lastHeard = now();
    },
    failed: () => {
      if (!following) return;
      const delay = Math.min(1000 * 2 ** failures, maxBackoff);
      failures++;
      if (timer !== undefined) clearTimer(timer);
      timer = setTimer(() => {
        timer = undefined;
        follow();
      }, delay);
    },
    now: () => {
      failures = 0;
      follow();
    },
  };
}

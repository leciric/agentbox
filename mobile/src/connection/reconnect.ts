// reconnector decides when the phone app follows the daemon's event stream
// again from scratch (events.ts), as the web phone app's
// desktop/src/renderer/web/reconnect.ts does, plus what a phone's sleep does
// to a socket the page never sees:
//
//   - the stream failed: try again a little later each time, up to every
//     15 seconds, and not while the app is in the background, where the OS
//     may not let it through anyway; coming back tries at once;
//   - an attempt that never opens nor fails: React Native's HTTP client has
//     no read timeout, and after a sleep it can send the request down a
//     pooled socket the OS has already cut, where it waits forever. After
//     openTimeout it is dropped and counts as a failure;
//   - a stream that looks open and carries nothing, as one the OS killed
//     while the app was away without a close leaves it: the daemon pings
//     every 15 s, so one silent for longer than silentAfter is replaced, and
//     after coming back from a short absence one that hasn't pinged within
//     wakeGrace is too;
//   - coming back to the foreground after a while, or when it isn't
//     connected, and the network coming back or changing (Wi-Fi to mobile
//     data): the stream may not have survived, so it is followed again at
//     once, with the backoff started over.
//
// It never follows on its own before started(), nor after stopped().
export type AppStateStatus = 'active' | 'background' | 'inactive' | 'unknown' | 'extension';

export interface Network {
  online: boolean;
  // Which network: a change means a new socket is needed.
  kind?: string;
}

export interface ReconnectorOptions {
  // follow drops the stream, if any, and opens it again.
  follow: () => void;
  // drop abandons an attempt that never opened, before the next one.
  drop: () => void;
  connected: () => boolean;
  // ended says the stream finished without an error (the daemon restarted).
  ended?: () => boolean;
  // React Native's AppState, and expo-network's listener.
  appState: { currentState: string; addEventListener: (type: 'change', fn: (next: AppStateStatus) => void) => unknown };
  network?: (fn: (state: Network) => void) => void;
  // For tests: the clock and the timers.
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
  every?: (fn: () => void, ms: number) => void;
}

export interface Reconnector {
  // started says the app follows the stream: from now on, follow it again when needed.
  started: () => void;
  // stopped says it no longer does (unpaired).
  stopped: () => void;
  // opened says the stream is up: the next failure starts the backoff over.
  opened: () => void;
  // heard says the stream carried something, an event or a ping.
  heard: () => void;
  // failed says the stream errored: follow again after the backoff.
  failed: () => void;
  // now follows again at once (the bar's "Retry now").
  now: () => void;
}

// The daemon pings every 15 s (?ping=1).
export const silentAfter = 45_000;
export const wakeGrace = 20_000;
// An app away for longer than this may have lost its stream to the phone's sleep.
export const awayFor = 15_000;
export const openTimeout = 10_000;
export const maxBackoff = 15_000;

export function reconnector(opts: ReconnectorOptions): Reconnector {
  const now = opts.now ?? Date.now;
  const setTimer = opts.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
  const clearTimer = opts.clearTimer ?? ((timer) => clearTimeout(timer as ReturnType<typeof setTimeout>));
  const every = opts.every ?? ((fn, ms) => void setInterval(fn, ms));

  let following = false;
  let failures = 0;
  let retry: unknown;
  let opening: unknown;
  let lastHeard = now();
  let hiddenAt: number | undefined;
  // When it came back from a short absence with the stream still looking up.
  let wokeAt: number | undefined;
  let network: Network | undefined;
  const active = () => opts.appState.currentState === 'active';

  const clear = () => {
    if (retry !== undefined) clearTimer(retry);
    if (opening !== undefined) clearTimer(opening);
    retry = opening = undefined;
  };
  const follow = () => {
    if (!following) return;
    clear();
    lastHeard = now();
    wokeAt = undefined;
    opts.follow();
    opening = setTimer(() => {
      opening = undefined;
      opts.drop();
      failed();
    }, openTimeout);
  };
  const failed = () => {
    if (!following) return;
    clear();
    if (!active()) return;
    const delay = Math.min(1000 * 2 ** failures, maxBackoff);
    failures++;
    retry = setTimer(() => {
      retry = undefined;
      // Gone to the background meanwhile: coming back follows.
      if (active()) follow();
    }, delay);
  };
  const followNow = () => {
    failures = 0;
    follow();
  };
  const silent = () => now() - lastHeard > silentAfter || (wokeAt !== undefined && lastHeard < wokeAt && now() - wokeAt > wakeGrace);

  every(() => {
    if (following && active() && opts.connected() && (silent() || opts.ended?.() === true)) follow();
  }, 5_000);
  opts.appState.addEventListener('change', (next) => {
    if (next !== 'active') {
      hiddenAt ??= now();
      return;
    }
    if (hiddenAt === undefined) return;
    const away = now() - hiddenAt > awayFor;
    hiddenAt = undefined;
    if (!following) return;
    if (away || !opts.connected() || silent()) followNow();
    else wokeAt = now();
  });
  opts.network?.((next) => {
    const was = network;
    network = next;
    if (!next.online || !following || !active()) return;
    // The first state heard is only where things stand.
    if ((was && (!was.online || was.kind !== next.kind)) || !opts.connected()) followNow();
  });

  return {
    started: () => {
      following = true;
    },
    stopped: () => {
      following = false;
      clear();
    },
    opened: () => {
      if (opening !== undefined) clearTimer(opening);
      opening = undefined;
      failures = 0;
      lastHeard = now();
    },
    heard: () => {
      lastHeard = now();
    },
    failed,
    now: followNow,
  };
}

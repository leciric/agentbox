import assert from 'node:assert/strict';
import { test } from 'node:test';
import { reconnector, type AppStateStatus, type Network, type ReconnectorOptions } from './reconnect.ts';

// harness drives a reconnector with a fake clock, timers, app state and
// network, and a stream that stays connecting until it is opened.
function harness(over: Partial<ReconnectorOptions> = {}) {
  let clock = 0;
  let stream: 'connecting' | 'connected' | 'disconnected' = 'disconnected';
  let follows = 0;
  let drops = 0;
  const timers: { at: number; fn: () => void }[] = [];
  let tick: () => void = () => {};
  let onAppState: (next: AppStateStatus) => void = () => {};
  let onNetwork: (state: Network) => void = () => {};
  const appState = { currentState: 'active' as string, addEventListener: (_: 'change', fn: (next: AppStateStatus) => void) => void (onAppState = fn) };
  const r = reconnector({
    follow: () => {
      follows++;
      stream = 'connecting';
    },
    drop: () => {
      drops++;
      stream = 'disconnected';
    },
    connected: () => stream === 'connected',
    appState,
    network: (fn) => {
      onNetwork = fn;
    },
    now: () => clock,
    setTimer: (fn, ms) => {
      const timer = { at: clock + ms, fn };
      timers.push(timer);
      return timer;
    },
    clearTimer: (timer) => {
      const i = timers.indexOf(timer as (typeof timers)[number]);
      if (i >= 0) timers.splice(i, 1);
    },
    every: (fn) => {
      tick = fn;
    },
    ...over,
  });
  const h = {
    r,
    follows: () => follows,
    drops: () => drops,
    stream: () => stream,
    // advance moves the clock a second at a time, firing timers and the 5 s check.
    advance: (ms: number) => {
      for (let left = ms; left > 0; left -= 1_000) {
        clock += Math.min(left, 1_000);
        for (const timer of timers.filter((x) => x.at <= clock)) {
          timers.splice(timers.indexOf(timer), 1);
          timer.fn();
        }
        if (clock % 5_000 === 0) tick();
      }
    },
    open: () => {
      stream = 'connected';
      r.opened();
    },
    fail: () => {
      stream = 'disconnected';
      r.failed();
    },
    // The phone locked: the app goes inactive, then to the background.
    lock: () => {
      for (const next of ['inactive', 'background'] as const) {
        appState.currentState = next;
        onAppState(next);
      }
    },
    unlock: () => {
      appState.currentState = 'active';
      onAppState('active');
    },
    network: (state: Network) => onNetwork(state),
    pending: () => timers.length,
  };
  return h;
}

// connected starts a reconnector and opens its stream.
function connected() {
  const h = harness();
  h.r.started();
  h.r.now();
  h.open();
  return h;
}

test('it does nothing before the app follows the stream, nor after it stops', () => {
  const h = harness();
  h.lock();
  h.advance(60_000);
  h.unlock();
  h.network({ online: false });
  h.network({ online: true });
  h.r.failed();
  h.advance(60_000);
  assert.equal(h.follows(), 0);

  h.r.started();
  h.r.now();
  h.r.stopped();
  h.advance(60_000);
  h.lock();
  h.advance(60_000);
  h.unlock();
  assert.equal(h.follows(), 1);
  assert.equal(h.pending(), 0);
});

test('a failed stream is followed again a little later each time, up to 15 s', () => {
  const h = connected();
  const delays: number[] = [];
  for (let i = 0; i < 6; i++) {
    h.fail();
    const before = h.follows();
    let waited = 0;
    while (h.follows() === before) {
      h.advance(1_000);
      waited += 1_000;
    }
    delays.push(waited);
  }
  assert.deepEqual(delays, [1_000, 2_000, 4_000, 8_000, 15_000, 15_000]);

  // Once it opens, the next failure starts over.
  h.open();
  h.fail();
  const before = h.follows();
  h.advance(1_000);
  assert.equal(h.follows(), before + 1);
});

test('an attempt that never opens is dropped and tried again', () => {
  // After a sleep the request can go down a socket the OS already cut, and
  // wait there forever: the app stayed "Reconnecting" until it was closed.
  const h = harness();
  h.r.started();
  h.r.now();
  assert.equal(h.stream(), 'connecting');
  h.advance(9_000);
  assert.equal(h.drops(), 0);
  h.advance(1_000);
  assert.equal(h.drops(), 1);
  assert.equal(h.stream(), 'disconnected');
  h.advance(1_000);
  assert.equal(h.follows(), 2, 'tried again after the backoff');

  // One that opens in time is kept.
  h.open();
  h.advance(30_000);
  assert.equal(h.drops(), 1);
  assert.equal(h.follows(), 2);
});

test('unlocking after a while follows the stream again at once', () => {
  const h = connected();
  h.lock();
  h.advance(60_000);
  h.unlock();
  assert.equal(h.follows(), 2);
  assert.equal(h.stream(), 'connecting');
});

test('unlocking while not connected follows at once and starts the backoff over', () => {
  const h = connected();
  for (let i = 0; i < 4; i++) {
    h.fail();
    h.advance(15_000);
  }
  h.fail();
  h.lock();
  // In the background it doesn't keep trying.
  const before = h.follows();
  h.advance(5 * 60_000);
  assert.equal(h.follows(), before);
  h.unlock();
  assert.equal(h.follows(), before + 1);
  h.fail();
  h.advance(1_000);
  assert.equal(h.follows(), before + 2, 'the backoff started over');
});

test('a stream killed without a close while the app was away is replaced', () => {
  // A short absence: it still looks connected, but the OS cut it.
  const h = connected();
  h.lock();
  h.advance(5_000);
  h.unlock();
  assert.equal(h.follows(), 1, 'not at once: it may well be alive');
  h.advance(20_000);
  assert.equal(h.follows(), 1);
  h.advance(5_000);
  assert.equal(h.follows(), 2, 'no ping since coming back: replaced');
});

test('a stream that pings after coming back is kept', () => {
  const h = connected();
  h.lock();
  h.advance(5_000);
  h.unlock();
  h.advance(8_000);
  h.r.heard();
  h.advance(40_000);
  assert.equal(h.follows(), 1);
});

test('a stream that goes silent is replaced; one that pings is kept', () => {
  const h = connected();
  for (let i = 0; i < 10; i++) {
    h.advance(15_000);
    h.r.heard();
  }
  assert.equal(h.follows(), 1);
  h.advance(50_000);
  assert.equal(h.follows(), 2);
});

test('the network coming back or changing follows at once', () => {
  const h = connected();
  h.network({ online: true, kind: 'WIFI' });
  assert.equal(h.follows(), 1, 'the first state heard is only where things stand');

  h.network({ online: false, kind: 'NONE' });
  assert.equal(h.follows(), 1);
  h.network({ online: true, kind: 'WIFI' });
  assert.equal(h.follows(), 2);

  h.open();
  h.network({ online: true, kind: 'CELLULAR' });
  assert.equal(h.follows(), 3, 'Wi-Fi to mobile data needs a new socket');

  h.open();
  h.network({ online: true, kind: 'CELLULAR' });
  assert.equal(h.follows(), 3, 'nothing changed');
});

test('the network coming back while not connected follows at once', () => {
  const h = connected();
  h.fail();
  h.network({ online: true, kind: 'WIFI' });
  assert.equal(h.follows(), 2);
});

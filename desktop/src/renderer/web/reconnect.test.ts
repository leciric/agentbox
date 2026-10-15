import assert from 'node:assert/strict';
import { test } from 'node:test';
import { reconnector, type ReconnectorOptions } from './reconnect.ts';

// harness drives a reconnector with a fake clock, timers and page.
function harness(over: Partial<ReconnectorOptions> = {}) {
  let clock = 0;
  let connected = true;
  let follows = 0;
  const timers: { at: number; fn: () => void }[] = [];
  let tick: () => void = () => {};
  const listeners = new Map<string, (event: unknown) => void>();
  const page = { visibilityState: 'visible' as DocumentVisibilityState, addEventListener: (type: string, fn: (event: unknown) => void) => void listeners.set(type, fn) };
  const r = reconnector({
    follow: () => follows++,
    connected: () => connected,
    silentAfter: () => 45_000,
    now: () => clock,
    setTimer: (fn, ms) => {
      const timer = { at: clock + ms, fn };
      timers.push(timer);
      return timer;
    },
    clearTimer: (timer) => timers.splice(timers.indexOf(timer as (typeof timers)[number]) >>> 0, 1),
    every: (fn) => {
      tick = fn;
    },
    page: page as unknown as Document,
    window: { addEventListener: (type: string, fn: (event: unknown) => void) => void listeners.set(type, fn) } as unknown as Window,
    ...over,
  });
  return {
    r,
    follows: () => follows,
    advance: (ms: number) => {
      clock += ms;
      for (const timer of timers.filter((x) => x.at <= clock)) {
        timers.splice(timers.indexOf(timer), 1);
        timer.fn();
      }
      tick();
    },
    setConnected: (c: boolean) => {
      connected = c;
    },
    hide: () => {
      page.visibilityState = 'hidden';
      listeners.get('visibilitychange')?.({});
    },
    show: () => {
      page.visibilityState = 'visible';
      listeners.get('visibilitychange')?.({});
    },
    fire: (type: string, event: unknown = {}) => listeners.get(type)?.(event),
  };
}

test('it does nothing before the page follows the stream', () => {
  const h = harness();
  h.setConnected(false);
  h.r.failed();
  h.fire('online');
  h.advance(60_000);
  assert.equal(h.follows(), 0);
});

test('a stream EventSource gave up on is followed again, later each time, until one opens', () => {
  const h = harness();
  h.r.started();
  h.setConnected(false);
  h.r.failed();
  h.advance(999);
  assert.equal(h.follows(), 0);
  h.advance(1);
  assert.equal(h.follows(), 1);
  h.r.failed();
  h.advance(1_999);
  assert.equal(h.follows(), 1);
  h.advance(1);
  assert.equal(h.follows(), 2);
  for (let i = 0; i < 6; i++) h.r.failed();
  h.advance(15_000);
  assert.equal(h.follows(), 3, 'the backoff stops at 15s, and a later failure replaces the timer');
  h.r.opened();
  h.r.failed();
  h.advance(1_000);
  assert.equal(h.follows(), 4, 'once open, the next failure starts over at 1s');
});

test('a stream that pings and went silent is replaced, and one that keeps pinging isn\'t', () => {
  const h = harness();
  h.r.started();
  h.r.opened();
  for (let i = 0; i < 12; i++) {
    h.advance(5_000);
    h.r.heard();
  }
  assert.equal(h.follows(), 0);
  h.advance(46_000);
  assert.equal(h.follows(), 1);
  h.advance(5_000);
  assert.equal(h.follows(), 1, 'following again counts as hearing from it');
});

test('a stream that doesn\'t ping is never taken for silent', () => {
  const h = harness({ silentAfter: () => undefined });
  h.r.started();
  h.advance(10 * 60_000);
  assert.equal(h.follows(), 0);
});

test('coming back after the phone slept follows again at once; a glance away doesn\'t', () => {
  const h = harness();
  h.r.started();
  h.hide();
  h.advance(3_000);
  h.r.heard();
  h.show();
  assert.equal(h.follows(), 0);
  h.hide();
  h.advance(20_000);
  h.show();
  assert.equal(h.follows(), 1);
});

test('the network coming back, or the page restored from the cache, follows again', () => {
  const h = harness();
  h.r.started();
  h.fire('online');
  assert.equal(h.follows(), 0, 'connected already: nothing to do');
  h.setConnected(false);
  h.fire('online');
  assert.equal(h.follows(), 1);
  h.fire('pageshow', { persisted: false });
  assert.equal(h.follows(), 1);
  h.fire('pageshow', { persisted: true });
  assert.equal(h.follows(), 2);
  h.r.now();
  assert.equal(h.follows(), 3);
});

// Run with `npm test`: the keep-awake switch against a fake blocker and window.
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { test } from 'node:test';
import { keepAwake } from './keepawake.ts';

function fakes() {
  const running = new Set<number>();
  let next = 1;
  const blocker = {
    start: () => (running.add(next), next++),
    stop: (id: number) => running.delete(id),
  };
  const window = Object.assign(new EventEmitter(), { id: 7 });
  return { running, blocker, window, set: keepAwake(blocker) };
}

const listeners = (w: EventEmitter) =>
  ['render-process-gone', 'did-start-navigation', 'destroyed'].map((e) => w.listenerCount(e));

test('busy and idle many times registers the window listeners once', () => {
  const { running, window, set } = fakes();
  for (let i = 0; i < 5; i++) {
    set(window as never, true);
    assert.equal(running.size, 1);
    set(window as never, false);
    assert.equal(running.size, 0);
  }
  assert.deepEqual(listeners(window), [1, 1, 1]);
});

test('a crash releases the hold, and a reloaded window can hold and release again', () => {
  const { running, window, set } = fakes();
  set(window as never, true);
  window.emit('render-process-gone');
  assert.equal(running.size, 0);
  set(window as never, true);
  assert.equal(running.size, 1);
  window.emit('render-process-gone');
  assert.equal(running.size, 0);
  assert.deepEqual(listeners(window), [1, 1, 1]);
});

test('in-page navigation keeps the hold, a new page drops it', () => {
  const { running, window, set } = fakes();
  set(window as never, true);
  window.emit('did-start-navigation', { isMainFrame: true, isSameDocument: true });
  window.emit('did-start-navigation', { isMainFrame: false, isSameDocument: false });
  assert.equal(running.size, 1);
  window.emit('did-start-navigation', { isMainFrame: true, isSameDocument: false });
  assert.equal(running.size, 0);
});

test('closing the window releases it and forgets its listeners', () => {
  const { running, window, set } = fakes();
  set(window as never, true);
  window.emit('destroyed');
  assert.equal(running.size, 0);
  assert.deepEqual(listeners(window), [1, 1, 0]);
});

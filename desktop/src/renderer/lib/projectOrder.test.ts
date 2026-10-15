import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { movePin, orderProjects, settleAfterMs, settled, togglePin, type OrderInput } from './projectOrder.ts';

const day = 24 * 60 * 60 * 1000;
const now = Date.parse('2026-10-15T12:00:00Z');
const ago = (ms: number) => new Date(now - ms).toISOString();
const project = (name: string, over: Partial<T.Project> = {}) => ({ name, displayName: name, createdAt: ago(30 * day), ...over }) as T.Project;

test('a project settles after three days', () => {
  assert.equal(settleAfterMs, 3 * day);
});

test('settled counts the user: their message or opening it', () => {
  const base = { pinned: false, live: false, createdAt: ago(30 * day) };
  assert.equal(settled({ ...base, lastMessage: ago(3 * day - 1) }, now), false);
  assert.equal(settled({ ...base, lastMessage: ago(3 * day) }, now), true);
  // Opening it brings it back.
  assert.equal(settled({ ...base, lastMessage: ago(10 * day), opened: ago(1 * day) }, now), false);
  // A project added lately hasn't had its three days yet.
  assert.equal(settled({ ...base, createdAt: ago(1 * day) }, now), false);
  // Never pinned, never while something is going on.
  assert.equal(settled({ ...base, pinned: true, lastMessage: ago(10 * day) }, now), false);
  assert.equal(settled({ ...base, live: true, lastMessage: ago(10 * day) }, now), false);
});

test("activity that isn't the user's keeps nothing up, but brings a settled project back", () => {
  const base = { pinned: false, live: false, createdAt: ago(30 * day), lastMessage: ago(5 * day) };
  // The lead answered a day after the user wrote: it still settles.
  assert.equal(settled({ ...base, lastActivity: ago(4 * day) }, now), true);
  // An agent reported after it had settled: it's back.
  assert.equal(settled({ ...base, lastActivity: ago(1 * day) }, now), false);
});

const input = (over: Partial<OrderInput> = {}): OrderInput => ({
  projects: [project('alpha'), project('beta'), project('gamma'), project('delta')],
  pins: [],
  sort: 'recent',
  activity: new Map([
    ['alpha', { project: 'alpha', lastActivity: ago(2 * day), lastMessage: ago(2 * day) }],
    ['beta', { project: 'beta', lastActivity: ago(1 * day), lastMessage: ago(1 * day) }],
    ['gamma', { project: 'gamma', lastActivity: ago(1000), lastMessage: ago(1000) }],
    ['delta', { project: 'delta', lastActivity: ago(20 * day), lastMessage: ago(20 * day) }],
  ]),
  working: new Map([['alpha', 3], ['beta', 1]]),
  live: new Set(),
  opened: {},
  label: (p) => p.displayName,
  now,
  ...over,
});

const names = (ps: T.Project[]) => ps.map((p) => p.name);

test('orderProjects sorts the rest by recent activity, name or agents working', () => {
  let out = orderProjects(input());
  assert.deepEqual(names(out.rest), ['gamma', 'beta', 'alpha']);
  assert.deepEqual(names(out.settled), ['delta']);
  out = orderProjects(input({ sort: 'name' }));
  assert.deepEqual(names(out.rest), ['alpha', 'beta', 'gamma']);
  out = orderProjects(input({ sort: 'working' }));
  assert.deepEqual(names(out.rest), ['alpha', 'beta', 'gamma']);
});

test('pins come first in their order, override the sort and never settle', () => {
  const out = orderProjects(input({ pins: ['delta', 'gone', 'alpha'], sort: 'name' }));
  assert.deepEqual(names(out.pinned), ['delta', 'alpha']);
  assert.deepEqual(names(out.rest), ['beta', 'gamma']);
  assert.deepEqual(out.settled, []);
});

test('a live or opened project stays off the shelf', () => {
  assert.deepEqual(orderProjects(input({ live: new Set(['delta']) })).settled, []);
  assert.deepEqual(orderProjects(input({ opened: { delta: ago(1000) } })).settled, []);
});

test('pins move by drag and toggle', () => {
  assert.deepEqual(movePin(['a', 'b', 'c'], 'c', 'a', 'before'), ['c', 'a', 'b']);
  assert.deepEqual(movePin(['a', 'b', 'c'], 'a', 'c', 'after'), ['b', 'c', 'a']);
  assert.deepEqual(movePin(['a', 'b'], 'a', 'x', 'after'), ['a', 'b']);
  assert.deepEqual(togglePin(['a'], 'b'), ['a', 'b']);
  assert.deepEqual(togglePin(['a', 'b'], 'a'), ['b']);
});

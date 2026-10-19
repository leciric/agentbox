import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { movePin, orderProjects, togglePin, type OrderInput } from './projectOrder.ts';

const day = 24 * 60 * 60 * 1000;
const now = Date.parse('2026-10-15T12:00:00Z');
const ago = (ms: number) => new Date(now - ms).toISOString();
const project = (name: string, over: Partial<T.Project> = {}) => ({ name, displayName: name, createdAt: ago(30 * day), ...over }) as T.Project;

const input = (over: Partial<OrderInput> = {}): OrderInput => ({
  projects: [project('alpha'), project('beta'), project('gamma'), project('delta')],
  pins: [],
  activity: new Map([
    ['alpha', { project: 'alpha', lastActivity: ago(2 * day), lastMessage: ago(2 * day) }],
    ['beta', { project: 'beta', lastActivity: ago(1 * day), lastMessage: ago(1 * day) }],
    ['gamma', { project: 'gamma', lastActivity: ago(1000), lastMessage: ago(1000) }],
    ['delta', { project: 'delta', lastActivity: ago(20 * day), lastMessage: ago(20 * day) }],
  ]),
  active: new Set(['alpha', 'beta', 'gamma']),
  label: (p) => p.displayName,
  groupIdle: true,
  ...over,
});

const names = (ps: T.Project[]) => ps.map((p) => p.name);

test('orderProjects sorts the rest by recent activity', () => {
  const out = orderProjects(input());
  assert.deepEqual(names(out.rest), ['gamma', 'beta', 'alpha']);
  assert.deepEqual(names(out.idle), ['delta']);
});

test('pins come first in their order, override the sort and stay up idle or not', () => {
  const out = orderProjects(input({ pins: ['delta', 'gone', 'alpha'] }));
  assert.deepEqual(names(out.pinned), ['delta', 'alpha']);
  assert.deepEqual(names(out.rest), ['gamma', 'beta']);
  assert.deepEqual(out.idle, []);
});

test('the projects with nothing going on are idle, sorted like the rest', () => {
  let out = orderProjects(input({ active: new Set(['beta']) }));
  assert.deepEqual(names(out.rest), ['beta']);
  assert.deepEqual(names(out.idle), ['gamma', 'alpha', 'delta']);
  out = orderProjects(input({ active: new Set() }));
  assert.deepEqual(out.rest, []);
  assert.deepEqual(names(out.idle), ['gamma', 'beta', 'alpha', 'delta']);
});

test('pins move by drag and toggle', () => {
  assert.deepEqual(movePin(['a', 'b', 'c'], 'c', 'a', 'before'), ['c', 'a', 'b']);
  assert.deepEqual(movePin(['a', 'b', 'c'], 'a', 'c', 'after'), ['b', 'c', 'a']);
  assert.deepEqual(movePin(['a', 'b'], 'a', 'x', 'after'), ['a', 'b']);
  assert.deepEqual(togglePin(['a'], 'b'), ['a', 'b']);
  assert.deepEqual(togglePin(['a', 'b'], 'a'), ['b']);
});

test('orderProjects without grouping keeps one stable list that activity never reorders', () => {
  const out = orderProjects(input({ groupIdle: false, pins: ['delta'] }));
  assert.deepEqual(names(out.pinned), ['delta']);
  assert.deepEqual(names(out.rest), ['alpha', 'beta', 'gamma']);
  assert.deepEqual(out.idle, []);
  const later = orderProjects(input({ groupIdle: false, active: new Set(['gamma']) }));
  assert.deepEqual(names(later.rest), ['alpha', 'beta', 'delta', 'gamma']);
});

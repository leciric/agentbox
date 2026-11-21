// Run with `npm test`.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { span, splitTrees, upFor, withoutTree } from './running.ts';

const tree = (pid: number, memory: number, more: Partial<T.ProcessTree> = {}): T.ProcessTree => ({
  pid,
  label: `t${pid}`,
  command: `t${pid}`,
  kind: 'agent',
  protected: false,
  processes: 1,
  memory,
  rss: memory,
  pss: 0,
  cpu: 0,
  cpuSeconds: 0,
  started: '2026-11-20T10:00:00Z',
  idleSeconds: 0,
  heavy: false,
  idle: false,
  members: [],
  ...more,
});

const MiB = 1 << 20;

test('small trees fold away unless flagged, and the system comes last', () => {
  const { shown, small } = splitTrees([
    tree(1, 2048 * MiB, { heavy: true }),
    tree(2, 600 * MiB, { kind: 'tool', protected: true }),
    tree(3, 300 * MiB, { kind: 'system', protected: true }),
    tree(4, 100 * MiB),
    tree(5, 4 * MiB),
    tree(6, 3 * MiB, { idle: true }),
  ]);
  assert.deepEqual(
    shown.map((t) => t.pid),
    [1, 2, 4, 6, 3],
  );
  assert.deepEqual(
    small.map((t) => t.pid),
    [5],
  );
});

test('spans read the way the view says them', () => {
  assert.equal(span(42), '42s');
  assert.equal(span(12 * 60 + 5), '12m');
  assert.equal(span(2 * 3600 + 14 * 60), '2h 14m');
  assert.equal(span(5 * 3600), '5h');
  assert.equal(span(3 * 86400 + 5), '3d');
  assert.equal(upFor('2026-11-20T10:00:00Z', Date.parse('2026-11-20T12:14:30Z')), '2h 14m');
  assert.equal(upFor('0001-01-01T00:00:00Z'), undefined);
});

test('a killed tree leaves the list, and its agent’s totals drop by it', () => {
  const list: T.ProcessList = {
    at: '2026-11-20T12:00:00Z',
    agents: [
      {
        ref: 'p/agent-01',
        project: 'p',
        name: 'agent-01',
        title: '',
        running: true,
        trees: [tree(1, 2048 * MiB, { processes: 3, cpu: 4 }), tree(2, 600 * MiB, { kind: 'tool', protected: true })],
        memory: 2648 * MiB,
        killable: 2048 * MiB,
        cpu: 5,
        processes: 4,
      },
    ],
  };
  const got = withoutTree(list, 'p/agent-01', 1).agents[0];
  assert.deepEqual(
    got.trees.map((t) => t.pid),
    [2],
  );
  assert.equal(got.memory, 600 * MiB);
  assert.equal(got.killable, 0);
  assert.equal(got.processes, 1);
  assert.equal(got.cpu, 1);
  assert.equal(withoutTree(list, 'p/agent-01', 99).agents[0], list.agents[0]);
});

// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { taskActions, taskFromText, taskLane, taskLanes, taskText, type TasksApi } from './tasks.ts';

const task = (id: string, extra: Partial<T.Task> = {}): T.Task => ({
  id,
  project: 'p',
  status: 'open',
  goal: `Goal ${id}`,
  createdAt: `2026-10-01T00:00:0${id.slice(-1)}Z`,
  updatedAt: '2026-10-01T00:00:00Z',
  ...extra,
});

// recorder is the daemon, as the list of calls the tab made.
function recorder() {
  const calls: unknown[][] = [];
  const record =
    (name: string) =>
    async (...args: unknown[]) => {
      calls.push([name, ...args]);
    };
  const api: TasksApi = {
    addTask: record('addTask'),
    updateTask: record('updateTask'),
    deleteTask: record('deleteTask'),
    createAgent: record('createAgent'),
    removeQueued: record('removeQueued'),
    moveQueued: record('moveQueued'),
  };
  return { calls, actions: taskActions(api, 'p') };
}

test('a task with no agent is in the backlog, which is where a new one goes', () => {
  assert.equal(taskLane(task('t1'), undefined), 'backlog');
});

test("a task's lane follows its agent: queued, running, or gone", () => {
  assert.equal(taskLane(task('t1', { agent: 'a1' }), { state: 'queued' }), 'queue');
  assert.equal(taskLane(task('t1', { agent: 'a1' }), { state: 'running' }), 'running');
  assert.equal(taskLane(task('t1', { agent: 'a1' }), undefined), 'backlog');
  assert.equal(taskLane(task('t1', { status: 'done', agent: 'a1' }), { state: 'running' }), 'done');
});

test('the queue is in the order it starts, the backlog newest first', () => {
  const agents = new Map([
    ['a1', { state: 'queued', queuePosition: 2 }],
    ['a2', { state: 'queued', queuePosition: 1 }],
    ['a3', { state: 'running' }],
  ]);
  const lanes = taskLanes(
    [task('t1', { agent: 'a1' }), task('t2', { agent: 'a2' }), task('t3', { agent: 'a3' }), task('t4'), task('t5'), task('t6', { status: 'abandoned' })],
    agents,
  );
  assert.deepEqual(
    lanes.queue.map((t) => t.id),
    ['t2', 't1'],
  );
  assert.deepEqual(
    lanes.running.map((t) => t.id),
    ['t3'],
  );
  assert.deepEqual(
    lanes.backlog.map((t) => t.id),
    ['t5', 't4'],
  );
  assert.deepEqual(
    lanes.done.map((t) => t.id),
    ['t6'],
  );
});

test("a task's text is its goal on the first line and its brief after", () => {
  assert.deepEqual(taskFromText('  Fix login  \nThe redirect loops.\n\nOnly on Safari.'), { goal: 'Fix login', detail: 'The redirect loops.\n\nOnly on Safari.' });
  assert.equal(taskText({ goal: 'Fix login', detail: 'The redirect loops.' }), 'Fix login\nThe redirect loops.');
  assert.equal(taskText({ goal: 'Fix login' }), 'Fix login');
});

test('creating, editing, finishing and deleting a task are one call each', async () => {
  const { calls, actions } = recorder();
  await actions.add('Fix login\nThe redirect loops.');
  await actions.edit(task('t1'), 'Fix login on Safari');
  await actions.markDone(task('t1'));
  await actions.remove(task('t1', { agent: 'a1' }));
  assert.deepEqual(calls, [
    ['addTask', 'p', { goal: 'Fix login', detail: 'The redirect loops.' }],
    ['updateTask', 'p', 't1', { goal: 'Fix login on Safari', detail: '' }],
    ['updateTask', 'p', 't1', { status: 'done' }],
    ['deleteTask', 'p', 't1'],
  ]);
});

test('queueing a backlog task makes a queued agent for it, and the backlog takes it back out', async () => {
  const { calls, actions } = recorder();
  await actions.setLane(task('t1'), undefined, 'queue');
  await actions.setLane(task('t2', { agent: 'a2' }), { name: 'a2', state: 'queued' }, 'backlog');
  assert.deepEqual(calls, [
    ['createAgent', { project: 'p', taskId: 't1', queue: true, ai: 'claude' }],
    ['removeQueued', 'p', 'a2'],
  ]);
});

test('choosing the lane a task is already in does nothing', async () => {
  const { calls, actions } = recorder();
  await actions.setLane(task('t1'), undefined, 'backlog');
  await actions.setLane(task('t2', { agent: 'a2' }), { name: 'a2', state: 'queued' }, 'queue');
  assert.deepEqual(calls, []);
});

test('a running task has no lane to choose', async () => {
  const { actions } = recorder();
  await assert.rejects(actions.setLane(task('t1', { agent: 'a1' }), { name: 'a1', state: 'running' }, 'backlog'));
});

test('a task whose agent is gone is let go before it is queued again', async () => {
  const { calls, actions } = recorder();
  await actions.setLane(task('t1', { agent: 'gone' }), undefined, 'queue');
  assert.deepEqual(calls, [
    ['updateTask', 'p', 't1', { agent: '' }],
    ['createAgent', { project: 'p', taskId: 't1', queue: true, ai: 'claude' }],
  ]);
});

test('reordering moves the queued agent, never above first', async () => {
  const { calls, actions } = recorder();
  await actions.move('a2', 1);
  await actions.move('a1', 0);
  assert.deepEqual(calls, [
    ['moveQueued', 'p', 'a2', 1],
    ['moveQueued', 'p', 'a1', 1],
  ]);
});

test('with the queue off, starting a task makes its agent now', async () => {
  const { calls, actions } = recorder();
  await actions.start(task('t1'), undefined);
  assert.deepEqual(calls, [['createAgent', { project: 'p', taskId: 't1', ai: 'claude' }]]);
});

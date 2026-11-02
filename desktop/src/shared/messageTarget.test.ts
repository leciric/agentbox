import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from './api.ts';
import { atTarget, cycleTarget, Lead, liveTarget, otherSession, targetOrder, targetsOf } from './messageTarget.ts';

const agent = (name: string, leadSession?: string, project = 'web'): T.Agent => ({ name, project, leadSession, ref: `${project}/${name}` }) as T.Agent;

const agents = [agent('agent-10'), agent('agent-2', 'prod'), agent('agent-3', 'main'), agent('agent-1', 'main'), agent('agent-9', 'main', 'other'), agent('lead.prod')];

test('a session writes to its own agents first, then the project’s others', () => {
  const targets = targetsOf(agents, 'web', 'main');
  assert.deepEqual(
    targets.own.map((a) => a.name),
    ['agent-1', 'agent-3'],
  );
  assert.deepEqual(
    targets.others.map((a) => a.name),
    ['agent-2', 'agent-10'],
  );
  assert.deepEqual(targetOrder(targets), [Lead, 'agent-1', 'agent-3', 'agent-2', 'agent-10']);
});

test('the keys cycle round the targets, both ways', () => {
  const order = targetOrder(targetsOf(agents, 'web', 'main'));
  assert.equal(cycleTarget(order, Lead, 1), 'agent-1');
  assert.equal(cycleTarget(order, 'agent-10', 1), Lead);
  assert.equal(cycleTarget(order, Lead, -1), 'agent-10');
  assert.equal(cycleTarget(order, 'retired', 1), 'agent-1');
  assert.equal(cycleTarget([], 'agent-1', 1), Lead);
});

test('a retired agent falls back to the lead', () => {
  const targets = targetsOf(agents, 'web', 'main');
  assert.equal(liveTarget('agent-2', targets), 'agent-2');
  assert.equal(liveTarget('agent-77', targets), Lead);
  assert.equal(liveTarget(undefined, targets), Lead);
});

test('"@agent-31 " at the start targets that agent for the message', () => {
  const names = ['agent-31', 'agent-4'];
  assert.deepEqual(atTarget('@agent-31 use the new table', names), { to: 'agent-31', text: 'use the new table' });
  assert.deepEqual(atTarget('  @agent-4 \nline one\nline two', names), { to: 'agent-4', text: 'line one\nline two' });
  assert.equal(atTarget('ask @agent-31 about it', names), undefined, 'a mention mid-message is the lead’s');
  assert.equal(atTarget('@agent-31', names), undefined, 'still being typed');
  assert.equal(atTarget('@src/main.ts is broken', names), undefined, 'a file');
});

test('another session’s agent asks first; its own and nobody’s don’t', () => {
  const sessions = [{ id: 'main', topic: 'Chat' }, { id: 'prod', topic: 'Prod incident' }];
  assert.equal(otherSession(agent('agent-2', 'prod'), 'main', sessions)?.topic, 'Prod incident');
  assert.equal(otherSession(agent('agent-1', 'main'), 'main', sessions), undefined);
  assert.equal(otherSession(agent('agent-10'), 'main', sessions), undefined);
  assert.equal(otherSession(agent('agent-5', 'gone'), 'main', sessions), undefined);
});

// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { clampSpot, docked, latestBy, mentionHref, mentionOf, mentionsIn, remarkAgentMentions, reported } from './pip.ts';

const now = Date.parse('2026-10-17T15:00:00Z');
const at = (min: number) => new Date(now - min * 60_000).toISOString();
const agent = (name: string, more: Partial<T.Agent> = {}) => ({ project: 'p', name, ref: `p/${name}`, state: 'running', chat: 'running', createdAt: at(60), ...more }) as T.Agent;
const item = (agentName: string, finished: number) => ({ project: 'p', agent: agentName, ref: `p/${agentName}`, finishedAt: at(finished) }) as T.ReviewItem;
const media = (id: string, kind: string, min: number) => ({ id, kind, createdAt: at(min) }) as T.MediaItem;

test('agents at work get a tile, in the order they were made', () => {
  const list = [agent('agent-3', { createdAt: at(10) }), agent('agent-1', { createdAt: at(30) }), agent('agent-2', { chat: 'waiting', createdAt: at(20) })];
  assert.deepEqual(docked('p', list, [], now).map((d) => d.agent.name), ['agent-1', 'agent-2', 'agent-3']);
});

test('stopped, idle and other projects’ agents get none', () => {
  const list = [agent('a', { state: 'stopped' }), agent('b', { chat: 'ready' }), agent('c', { project: 'q' })];
  assert.deepEqual(docked('p', list, [], now), []);
});

test('an agent that just finished comes first, until it has waited a while', () => {
  const list = [agent('agent-1'), agent('agent-2', { chat: 'ready' }), agent('agent-3', { chat: 'ready' })];
  const out = docked('p', list, [item('agent-2', 3), item('agent-3', 40)], now);
  assert.deepEqual(out.map((d) => d.agent.name), ['agent-2', 'agent-1']);
  assert.equal(out[0].finishedAt, at(3));
});

test('a message reports the latest recording before it, over a newer screenshot', () => {
  const items = [media('s2', 'screenshot', 2), media('r1', 'recording', 8), media('s1', 'screenshot', 12)];
  assert.equal(reported(items, at(1))?.id, 'r1');
});

test('with no recording, the latest screenshot; nothing from long before', () => {
  assert.equal(reported([media('s2', 'screenshot', 2), media('n', 'note', 1)], at(1))?.id, 's2');
  assert.equal(reported([media('old', 'screenshot', 120)], at(1)), undefined);
});

test('a clicked mention opens on the latest capture as of its message, else the latest', () => {
  const items = [media('s3', 'screenshot', 1), media('s2', 'screenshot', 30), media('l', 'log', 40)];
  assert.equal(latestBy(items, at(20))?.id, 's2');
  assert.equal(latestBy(items, at(90))?.id, 's3');
});

test('mentions are whole names, each once', () => {
  const names = new Set(['agent-12', 'agent-1']);
  assert.deepEqual(mentionsIn('agent-12 and agent-1 (agent-12 again). agent-123, agentbox/agent-12-x, agent-1.png', names), ['agent-12', 'agent-1']);
  assert.deepEqual(mentionsIn('agent-12.', names), ['agent-12']);
});

test('a mention href goes there and back', () => {
  assert.equal(mentionOf(mentionHref('agent-7')), 'agent-7');
  assert.equal(mentionOf('https://example.com'), undefined);
});

test('the remark plugin links names in text, not in code or links', () => {
  const tree = {
    type: 'root',
    children: [
      {
        type: 'paragraph',
        children: [
          { type: 'text', value: 'agent-12 finished; ask agent-9' },
          { type: 'inlineCode', value: 'agent-12' },
          { type: 'link', url: 'x', children: [{ type: 'text', value: 'agent-12' }] },
        ],
      },
    ],
  };
  remarkAgentMentions(new Set(['agent-12', 'agent-9']))()(tree);
  const p = tree.children[0].children as { type: string; value?: string; url?: string }[];
  assert.deepEqual(
    p.map((n) => n.type + (n.url ? `:${n.url}` : '') + (n.value !== undefined ? `=${n.value}` : '')),
    [`link:${mentionHref('agent-12')}`, 'text= finished; ask ', `link:${mentionHref('agent-9')}`, 'inlineCode=agent-12', 'link:x'],
  );
});

test('a floating tile stays inside its area', () => {
  assert.deepEqual(clampSpot({ x: -40, y: 900 }, 200, 140, 1000, 800), { x: 8, y: 652 });
});

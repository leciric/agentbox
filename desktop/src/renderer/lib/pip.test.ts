// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { docked, latestBy, mentionHref, mentionOf, mentionsIn, remarkAgentMentions, reported } from './pip.ts';

const now = Date.parse('2026-10-17T15:00:00Z');
const at = (min: number) => new Date(now - min * 60_000).toISOString();
const agent = (name: string, more: Partial<T.Agent> = {}) => ({ project: 'p', name, ref: `p/${name}`, state: 'running', chat: 'running', createdAt: at(60), ...more }) as T.Agent;
const use = (name: string, min: number): T.DesktopUse => ({ ref: `p/${name}`, usedAt: at(min) });
const media = (id: string, kind: string, min: number) => ({ id, kind, createdAt: at(min) }) as T.MediaItem;

test('agents using their desktops get a tile, in the order they were made', () => {
  const list = [agent('agent-3', { createdAt: at(10) }), agent('agent-1', { createdAt: at(30) }), agent('agent-2', { createdAt: at(20) })];
  const uses = [use('agent-3', 0), use('agent-1', 1), use('agent-2', 0.5)];
  assert.deepEqual(docked('p', list, uses, now).map((a) => a.name), ['agent-1', 'agent-2', 'agent-3']);
});

test('an idle desktop, a stopped agent or another project’s gets none', () => {
  const list = [agent('idle'), agent('never'), agent('stopped', { state: 'stopped' }), agent('other', { project: 'q' })];
  const uses = [use('idle', 3), use('stopped', 0), { ref: 'q/other', usedAt: at(0) }];
  assert.deepEqual(docked('p', list, uses, now), []);
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

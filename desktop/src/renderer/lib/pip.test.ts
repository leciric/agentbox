// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { docked, laneBox, laneFit, laneStrip, laneWidth, mentionHref, mentionOf, remarkAgentMentions } from './pip.ts';

const now = Date.parse('2026-10-17T15:00:00Z');
const at = (min: number) => new Date(now - min * 60_000).toISOString();
const agent = (name: string, more: Partial<T.Agent> = {}) => ({ project: 'p', name, ref: `p/${name}`, state: 'running', chat: 'running', createdAt: at(60), ...more }) as T.Agent;
const use = (name: string, min: number): T.DesktopUse => ({ ref: `p/${name}`, usedAt: at(min) });

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

test('the lane narrows, folds to a strip, then goes, before the chat gets under its least', () => {
  const chatMin = 440;
  assert.equal(laneFit(1200, chatMin, undefined), 'tiles');
  // Dragged wide, it takes only what the chat can spare.
  assert.equal(laneBox(900, 1200, chatMin), 1200 - chatMin);
  assert.equal(laneBox(undefined, 1200, chatMin), laneWidth);
  // Narrower than foldBelow, a strip unless the user opened it, and opened, tiles while they fit.
  assert.equal(laneFit(700, chatMin, undefined), 'strip');
  assert.equal(laneFit(700, chatMin, false), 'tiles');
  assert.equal(laneFit(chatMin + laneWidth - 1, chatMin, false), 'strip');
  assert.equal(laneFit(chatMin + laneStrip, chatMin, false), 'strip');
  assert.equal(laneFit(chatMin + laneStrip - 1, chatMin, false), 'none');
  // Folded by the user, a strip wherever it fits.
  assert.equal(laneFit(1600, chatMin, true), 'strip');
});

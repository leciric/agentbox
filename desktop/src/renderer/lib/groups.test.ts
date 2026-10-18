import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { latestGroupNotes, defaultPicks, duration, lowest, memberOf, memberSt, projectGroups, ready, toVariant, tookMs, variants } from './groups.ts';

const member = (m: Partial<T.GroupMember>): T.GroupMember => ({ label: 'A', ai: 'claude', ...m });
const group = (g: Partial<T.Group>): T.Group => ({ id: 'grp_1', project: 'shop', title: 'Login', task: 'Fix it', createdAt: '2026-10-18T10:00:00Z', finished: 0, members: [], ...g });

test('memberSt: made, working, finished, then kept or retired', () => {
  const open = group({});
  assert.equal(memberSt(open, member({})), 'creating');
  assert.equal(memberSt(open, member({ error: 'no login' })), 'failed');
  assert.equal(memberSt(open, member({ agent: 'agent-01' })), 'working');
  assert.equal(memberSt(open, member({ agent: 'agent-01', finishedAt: '2026-10-18T10:10:00Z' })), 'finished');
  // Told something after it finished, it works again.
  assert.equal(memberSt(open, member({ agent: 'agent-01', finishedAt: '2026-10-18T10:10:00Z', busy: true })), 'working');
  const kept = group({ kept: 'agent-01', settledAt: '2026-10-18T11:00:00Z' });
  assert.equal(memberSt(kept, member({ agent: 'agent-01' })), 'kept');
  assert.equal(memberSt(kept, member({ agent: 'agent-02' })), 'retired');
});

test('projectGroups and ready: open groups of a project, ready once all finished', () => {
  const a = group({ id: 'a', createdAt: '2026-10-18T10:00:00Z', members: [member({}), member({})], finished: 2 });
  const b = group({ id: 'b', createdAt: '2026-10-18T09:00:00Z', members: [member({}), member({})], finished: 1 });
  const done = group({ id: 'c', settledAt: '2026-10-18T11:00:00Z' });
  const other = group({ id: 'd', project: 'blog' });
  assert.deepEqual(projectGroups([a, b, done, other], 'shop').map((g) => g.id), ['b', 'a']);
  assert.equal(ready(a), true);
  assert.equal(ready(b), false);
  assert.equal(ready({ ...a, settledAt: '2026-10-18T11:00:00Z' }), false);
  assert.equal(memberOf([a, b], 'shop/agent-09'), undefined);
  const g = group({ members: [member({ ref: 'shop/agent-02', label: 'B' })] });
  assert.equal(memberOf([g], 'shop/agent-02')?.member.label, 'B');
});

test('tookMs and duration: start to first finish, in the fewest words', () => {
  const m = member({ startedAt: '2026-10-18T10:00:00Z', finishedAt: '2026-10-18T10:12:20Z' });
  assert.equal(duration(tookMs(m, 0)!), '12m');
  assert.equal(tookMs(member({}), 0), undefined);
  assert.equal(duration(45_000), '45s');
  assert.equal(duration(64 * 60_000), '1h 4m');
});

test('lowest: the best of a column, only when it stands out', () => {
  assert.equal(lowest([3, 1, 2]), 1);
  assert.equal(lowest([2, 2]), undefined);
  assert.equal(lowest([5, undefined]), undefined);
  assert.equal(lowest([0, 4, 6]), 4);
});

test('variants: each signed-in tool, on each of its models', () => {
  const settings = {
    claudeModelChoices: [
      { value: 'default', name: 'Default (recommended)' },
      { value: 'opus', name: 'Opus' },
      { value: 'sonnet', name: 'Sonnet' },
    ],
    openCodeModelChoices: [],
    cursorModelChoices: [{ value: 'composer-2', name: 'Composer 2' }],
  } as unknown as T.Settings;
  const auth = { claude: true, codex: true, opencode: false, cursor: true } as T.AuthStatus;
  const got = variants(settings, auth, 'default');
  assert.deepEqual(
    got.map((v) => v.label),
    ['Claude Code · Default', 'Claude Code · Opus', 'Claude Code · Sonnet', 'Codex', 'Cursor · Composer 2'],
  );
  assert.deepEqual(defaultPicks(got), ['claude|opus', 'claude|sonnet']);
  assert.deepEqual(toVariant('codex|'), { ai: 'codex', model: undefined });
  assert.deepEqual(toVariant('claude|sonnet'), { ai: 'claude', model: 'sonnet' });
  // Before any model is known, Claude Code still offers its default, twice.
  const bare = variants(undefined, undefined, 'default');
  assert.deepEqual(bare.map((v) => v.value), ['claude|']);
  assert.deepEqual(defaultPicks(bare), ['claude|', 'claude|']);
});

test('latestGroupNotes: one card per group, at its latest notice', () => {
  const rows = [{ item: { id: '1', group: 'g' } }, { item: { id: '2' } }, { item: { id: '3', group: 'g' } }, { item: { id: '4', group: 'h' } }, {}];
  assert.deepEqual(latestGroupNotes(rows).map((r) => r.item?.id ?? '-'), ['2', '3', '4', '-']);
});

test('defaultPicks: Opus and Sonnet before anything else, never Fable', () => {
  const opts = ['default', 'sonnet', 'fable', 'opus', 'haiku'].map((m) => ({ value: `claude|${m}`, ai: 'claude', model: m, label: m }));
  assert.deepEqual(defaultPicks(opts), ['claude|opus', 'claude|sonnet']);
  assert.deepEqual(defaultPicks(opts.filter((o) => o.model !== 'opus')), ['claude|sonnet', 'claude|haiku']);
});

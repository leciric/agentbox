import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as T from '../shared/api.ts';
import { agentStatus, isWaiting, leadStatus, liveStatus, rank } from '../features/threads/activity.ts';
import { advance } from '../features/threads/thread.ts';
import { timelineRows } from '../features/threads/timeline.ts';
import * as P from './parse.ts';

// The daemon's answers as Go writes them: a nil slice is null and an
// omitempty field is absent (json.Marshal of the internal/api types), which
// shared/api.ts's types don't say.
const at = '2026-10-21T09:12:03.123456789Z';
const daemon = {
  projects: `[
    {"name":"home","displayName":"","envFiles":null,"claudeAccounts":null,"openCodeAccounts":null,"createdAt":"${at}"},
    {"name":"web","displayName":"Web app","envFiles":null,"claudeAccounts":null,"openCodeAccounts":null,"createdAt":"${at}"},
    {"name":"empty","displayName":"","envFiles":null,"claudeAccounts":null,"openCodeAccounts":null,"createdAt":"${at}"}
  ]`,
  agents: `[
    {"ref":"web/agent-1","project":"web","name":"agent-1","title":"Fix login","state":"running","chat":"running","createdAt":"${at}"},
    {"ref":"web/agent-2","project":"web","name":"agent-2","title":"","state":"running","outcome":"limited","outcomeUntil":"${at}","createdAt":"${at}"},
    {"ref":"web/agent-3","project":"web","name":"agent-3","title":"","state":"stopped","createdAt":"${at}"}
  ]`,
  lead: `{"project":"web","ref":"web/lead","started":true,"chat":"ready"}`,
  leadOff: `{"project":"empty","ref":"empty/lead","started":false,"chat":"off"}`,
  thread: `{"agent":"web/agent-1","seq":4,"session":{"state":"running","tool":"claude","options":null,"commands":null,"turnStartedAt":"${at}"},"items":[
    {"id":"u1","turn":"u1","kind":"user","text":"go","createdAt":"${at}","updatedAt":"${at}"},
    {"id":"t2","turn":"u1","kind":"tool","tool":{"callId":"c1","title":"ls","kind":"execute","status":"in_progress"},"createdAt":"${at}","updatedAt":"${at}"},
    {"id":"p3","turn":"u1","kind":"permission","permission":{"callId":"c2","title":"Write file","options":null},"createdAt":"${at}","updatedAt":"${at}"}
  ]}`,
  emptyThread: `{"agent":"web/lead","seq":0,"session":{"state":"","tool":"","options":null,"commands":null},"items":null}`,
  questions: `null`,
  review: `{"items":null,"total":0}`,
  pulls: `{"project":"web","pullRequests":null}`,
  media: `[{"id":"m1","agent":"web/agent-1","kind":"recording","name":"login-after","size":10,"source":"desktop","meta":{},"createdAt":"${at}"},{"id":"m2","kind":"gpu-report"}]`,
};
const read = (name: keyof typeof daemon) => JSON.parse(daemon[name]) as unknown;

test("the daemon's answers, nulls and all, give the screens lists they can read", () => {
  const projects = P.projects(read('projects'));
  assert.deepEqual(
    projects.map((p) => p.name),
    ['home', 'web', 'empty'],
  );
  const thread = P.chatThread(read('emptyThread'));
  assert.deepEqual(thread.items, []);
  assert.deepEqual(thread.session.options, []);
  assert.deepEqual(P.questions(read('questions')), []);
  assert.deepEqual(P.reviewPage(read('review')), { items: [], total: 0 });
  assert.deepEqual(P.pulls(read('pulls')).pullRequests, []);
  assert.deepEqual(
    P.media(read('media')).map((m) => [m.id, m.kind, m.name]),
    [
      ['m1', 'recording', 'login-after'],
      ['m2', 'gpu-report', ''],
    ],
  );
  const live = P.chatThread(read('thread'));
  assert.deepEqual(live.items.find((it) => it.kind === 'permission')?.permission?.options, []);
});

// What the home screen does with them, for every project, an agentless one
// included: none of it may throw.
test('the home screen sums up every project and agent the daemon sends', () => {
  const projects = P.projects(read('projects')).filter((p) => p.name !== T.HomeProject);
  const agents = P.agents(read('agents'));
  const thread = P.chatThread(read('thread'));
  for (const p of projects) {
    const lead = P.projectChat(read(p.name === 'web' ? 'lead' : 'leadOff'));
    const status = leadStatus(lead.chat, lead.started ? P.chatThread(read('emptyThread')) : undefined);
    assert.ok(status.text);
    const own = agents.filter((a) => a.project === p.name).map((a) => ({ a, status: agentStatus(a, a.ref === thread.agent ? thread : undefined) }));
    own.sort((x, y) => rank(x.a) - rank(y.a) || x.a.name.localeCompare(y.a.name));
    for (const { status } of own) assert.ok(status.text);
    assert.equal(P.questions(read('questions')).filter(isWaiting).length, 0);
  }
  // A permission with no options is still a permission to give.
  assert.equal(liveStatus(thread)?.tone, 'urgent');
  assert.ok(timelineRows(thread).length > 0);
});

test('items that make no sense are dropped, and wrong types become zero values', () => {
  const agents = P.agents([null, 'web/x', { ref: 'web/a' }, { ref: 'web/b', project: 'web', name: 'b', state: 7, title: null, background: ['x', 3] }]);
  assert.equal(agents.length, 1);
  assert.equal(agents[0].state, '');
  assert.equal(agents[0].title, '');
  assert.deepEqual(agents[0].background, ['x']);
  assert.doesNotThrow(() => agentStatus(agents[0], undefined));
  assert.deepEqual(P.agents({ error: 'nope' }), []);
  assert.deepEqual(P.projects(null), []);
  // A lead that says it started but names no chat isn't read.
  assert.equal(P.projectChat({ started: true }).started, false);
  const thread = P.chatThread({ items: [{ id: 'a', kind: 'tool', tool: { kind: 'read', status: 'in_progress', paths: null } }, { kind: 'user' }], session: null });
  assert.equal(thread.items.length, 1);
  assert.equal(thread.session.state, '');
  assert.doesNotThrow(() => timelineRows(thread));
  const page = P.reviewPage({ items: [{ ref: 'web/a', project: 'web', pr: { title: 'no number' }, changes: { files: '2' } }] });
  assert.equal(page.total, 1);
  assert.equal(page.items[0].pr, undefined);
  assert.equal(page.items[0].changes?.files, 0);
});

test('chat and agent events are checked before they reach the queries', () => {
  assert.equal(P.chatEvent({ seq: 1 }), undefined);
  assert.equal(P.chatEvent({ agent: 'web/lead' }), undefined);
  const ev = P.chatEvent({ agent: 'web/lead', seq: 1, item: { id: 'a1', kind: 'assistant', text: 'hi', images: null } });
  assert.ok(ev);
  const next = advance(P.chatThread(read('emptyThread')), [ev]);
  assert.notEqual(next, 'gap');
  if (next !== 'gap') assert.equal(next.items[0].text, 'hi');
  // An item without an id is no item: the event changes nothing but its seq.
  assert.equal(P.chatEvent({ agent: 'web/lead', seq: 2, item: { kind: 'user' } })?.item, undefined);
  assert.equal(P.agentChange({ state: 'running' }), undefined);
  assert.deepEqual(P.agentChange({ ref: 'web/a', state: null }), { ref: 'web/a', state: '', removed: false });
});

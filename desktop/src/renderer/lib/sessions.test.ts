import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { afterClose, awake, byActivity, isSnoozed, snoozeChoices, snoozedOf, isUnread, isWorking, needsYouCount, neighbour, openSession, pickSession, SESSION_COLORS, sessionAlerts, sessionColor, sessionStatus, topicFrom, withSession } from './sessions.ts';

const session = (id: string, activeAt: string, over: Partial<T.LeadSession> = {}): T.LeadSession => ({
  project: 'web',
  id,
  ref: id === 'main' ? 'web/lead' : `web/lead.${id}`,
  topic: id,
  color: '',
  chat: 'off',
  agents: 0,
  createdAt: '2026-10-01T00:00:00Z',
  activeAt,
  ...over,
});

const main = session('main', '2026-10-28T10:00:00Z');
const prod = session('prod', '2026-10-29T09:00:00Z');
const docs = session('docs', '2026-10-27T09:00:00Z');
const all = [main, prod, docs];

test('the chat shows the session chosen last, else the most recently active', () => {
  assert.equal(pickSession(all, 'docs')?.id, 'docs');
  assert.equal(pickSession(all, undefined)?.id, 'prod');
  assert.equal(pickSession(all, 'gone')?.id, 'prod', 'a deleted choice falls back');
  assert.equal(pickSession([], 'main'), undefined);
  assert.deepEqual(byActivity(all).map((s) => s.id), ['prod', 'main', 'docs']);
});

const agent = (name: string, leadSession?: string) => ({ ref: `web/${name}`, leadSession }) as T.Agent;
const question = (name: string, createdAt: string, status = 'pending') => ({ id: `${name}-${createdAt}`, project: 'web', agent: name, ref: `web/${name}`, question: '?', status, createdAt }) as T.Question;
const agents = [agent('agent-1', 'docs'), agent('agent-2', 'main'), agent('agent-3'), agent('agent-4', 'deleted')];

test('clicking a project opens the session its agents wait in', () => {
  assert.equal(openSession(all, 'main', [], agents)?.id, 'main', 'none waiting: the one chosen last');
  assert.equal(openSession(all, undefined, [], agents)?.id, 'prod');
  assert.equal(openSession(all, 'main', [question('agent-1', '2026-10-29T10:00:00Z')], agents)?.id, 'docs');
  assert.equal(openSession(all, 'docs', [question('agent-2', '2026-10-29T10:00:00Z', 'escalated')], agents)?.id, 'main', 'one escalated to you waits too');
  const several = [question('agent-2', '2026-10-29T11:00:00Z'), question('agent-1', '2026-10-29T10:00:00Z'), question('agent-2', '2026-10-29T12:00:00Z')];
  assert.equal(openSession(all, 'prod', several, agents)?.id, 'docs', 'the oldest one');
});

test('a question no session owns waits in the one most recently active', () => {
  assert.equal(openSession(all, 'docs', [question('agent-3', '2026-10-29T10:00:00Z')], agents)?.id, 'prod', "an agent nobody's");
  assert.equal(openSession(all, 'docs', [question('agent-4', '2026-10-29T10:00:00Z')], agents)?.id, 'prod', 'its session closed or deleted');
});

test('answered questions, and gone agents', () => {
  const done = [question('agent-1', '2026-10-29T10:00:00Z', 'answered'), question('agent-1', '2026-10-29T10:00:00Z', 'cancelled'), question('agent-9', '2026-10-29T09:00:00Z')];
  assert.equal(openSession(all, 'main', done, agents)?.id, 'main');
  assert.equal(openSession(all, 'main', [question('agent-9', '2026-10-29T09:00:00Z'), question('agent-1', '2026-10-29T10:00:00Z')], agents)?.id, 'docs', 'a gone agent is skipped, not the oldest');
  assert.equal(openSession([], 'main', [question('agent-1', '2026-10-29T10:00:00Z')], agents), undefined);
});

test('next and previous go round the tabs', () => {
  assert.equal(neighbour(all, 'main', 1)?.id, 'prod');
  assert.equal(neighbour(all, 'docs', 1)?.id, 'main');
  assert.equal(neighbour(all, 'main', -1)?.id, 'docs');
  assert.equal(neighbour(all, 'gone', 1)?.id, 'main');
  assert.equal(neighbour([], 'main', 1), undefined);
});

test('closing a session shows the one beside it', () => {
  assert.equal(afterClose(all, 'prod')?.id, 'docs');
  assert.equal(afterClose(all, 'docs')?.id, 'prod');
  assert.equal(afterClose([main], 'main'), undefined);
});

test('a new session never shows a closed one: its id is its own', () => {
  // The daemon hands out an id no session ever had (state.AddLeadSession), so
  // the session Ctrl+T opens after closing "session" is "session-2", and the
  // tabs show it, not the closed one the choice may still name.
  const closed = session('session', '2026-10-29T10:00:00Z');
  const fresh = session('session-2', '2026-10-30T09:00:00Z');
  const open = withSession(all.filter((s) => s.id !== closed.id), fresh);
  assert.equal(pickSession(open, 'session-2')?.ref, 'web/lead.session-2');
  assert.equal(pickSession(open, 'session')?.id, 'session-2', 'a closed choice falls back to the most recently active');
});

test('a session just made opens before the list is read again', () => {
  const fresh = session('fresh', '2026-10-30T09:00:00Z');
  assert.deepEqual(withSession(all, fresh).map((s) => s.id), ['main', 'prod', 'docs', 'fresh']);
  assert.equal(pickSession(withSession(all, fresh), 'fresh')?.id, 'fresh');
  const renamed = { ...prod, topic: 'production' };
  assert.deepEqual(withSession(all, renamed).map((s) => s.topic), ['main', 'production', 'docs'], 'one already listed is replaced, not added twice');
});

test('a session is unread when it said something after it was last seen', () => {
  const said = session('prod', '2026-10-29T09:00:00Z', { last: { kind: 'assistant', text: 'done', at: '2026-10-29T09:00:00Z' } });
  assert.ok(isUnread(said, '2026-10-29T08:00:00Z'));
  assert.ok(!isUnread(said, '2026-10-29T09:30:00Z'));
  assert.ok(isUnread(said, undefined), 'never seen counts from when it was opened');
  assert.ok(!isUnread(prod, undefined), 'nothing said, nothing unread');
  const seenOnThePhone = { ...said, seenAt: '2026-10-29T09:30:00Z' };
  assert.ok(!isUnread(seenOnThePhone, undefined), 'seen anywhere is read here');
  assert.ok(!isUnread({ ...said, seenAt: '2026-10-29T08:00:00Z' }, '2026-10-29T09:30:00Z'), 'seen here since the daemon last said');
  assert.ok(isWorking(session('x', '', { chat: 'running' })));
  assert.ok(!isWorking(session('x', '', { chat: 'ready' })));
});

test('a session started from a message is named after its first line', () => {
  assert.equal(topicFrom('  Fix the checkout\nand then the cart'), 'Fix the checkout');
  assert.equal(topicFrom('Look into why the login page returns 500s for users without a session cookie'), 'Look into why the login page returns 500s for users without…');
  assert.equal(topicFrom('   '), undefined);
});

test('sessions show their colours only once the chat has more than one', () => {
  const main = session('main', '2026-01-01T00:00:00Z', { color: 'lilac' });
  const prod = session('prod', '2026-01-01T00:00:00Z', { color: 'peach' });
  const old = session('old', '2026-01-01T00:00:00Z');
  assert.equal(sessionColor([main], main), undefined);
  assert.equal(sessionColor([main, prod], prod), 'peach');
  assert.equal(sessionColor([main, prod, old], old), undefined, 'a session with no colour shows none');
  assert.equal(sessionColor([main, prod], undefined), undefined, 'an agent no session owns stays neutral');
});

test('every session colour has its look in styles.css', () => {
  const css = readFileSync(new URL('../styles.css', import.meta.url), 'utf8');
  for (const c of SESSION_COLORS) {
    assert.match(css, new RegExp(`\\[data-session-color="${c}"\\] \\{ --session: var\\(--ab-session-${c}\\); \\}`), c);
    assert.equal(css.match(new RegExp(`--ab-session-${c}: #`, 'g'))?.length, 2, `${c} in the dark and light themes`);
  }
});

test('a snoozed session leaves the tabs until its time', () => {
  const now = new Date('2026-11-04T10:00:00Z').getTime();
  const later = session('later', '2026-10-29T09:00:00Z', { snoozedUntil: '2026-11-05T09:00:00Z' });
  const soon = session('soon', '2026-10-29T09:00:00Z', { snoozedUntil: '2026-11-04T11:00:00Z' });
  const past = session('past', '2026-10-29T09:00:00Z', { snoozedUntil: '2026-11-04T09:00:00Z' });
  const list = [main, later, soon, past];
  assert.deepEqual(
    awake(list, now).map((s) => s.id),
    ['main', 'past'],
  );
  assert.deepEqual(
    snoozedOf(list, now).map((s) => s.id),
    ['soon', 'later'],
  );
  assert.equal(isSnoozed(past, now), false);
});

test('the snooze menu offers an hour, this evening, tomorrow and next week', () => {
  // A Wednesday morning, local time.
  const morning = new Date(2026, 10, 4, 10, 0);
  const choices = snoozeChoices(morning);
  assert.deepEqual(
    choices.map((c) => c.key),
    ['hour', 'evening', 'tomorrow', 'week'],
  );
  assert.equal(choices[1].until.getHours(), 18);
  assert.equal(choices[2].until.getDate(), 5);
  assert.equal(choices[3].until.getDay(), 1);
  assert.equal(choices[3].until.getDate(), 9);
  // Late in the day there is no evening left; on a Monday, next week is a
  // week away.
  assert.deepEqual(
    snoozeChoices(new Date(2026, 10, 4, 17, 30)).map((c) => c.key),
    ['hour', 'tomorrow', 'week'],
  );
  assert.equal(snoozeChoices(new Date(2026, 10, 9, 10, 0)).at(-1)!.until.getDate(), 16);
});

test('a session stands where the daemon says, or where its chat is for an older one', () => {
  assert.equal(sessionStatus(session('x', '', { status: 'needsYou', needsYou: 2, chat: 'running' })), 'needsYou');
  assert.equal(sessionStatus(session('x', '', { status: 'idle', chat: 'running' })), 'idle', "the daemon's word wins");
  assert.equal(sessionStatus(session('x', '', { chat: 'waiting' })), 'needsYou');
  assert.equal(sessionStatus(session('x', '', { chat: 'error' })), 'failed');
  assert.equal(sessionStatus(session('x', '', { chat: 'running' })), 'working');
  assert.equal(sessionStatus(session('x', '', { chat: 'ready' })), 'idle');
  assert.equal(needsYouCount(session('x', '', { status: 'needsYou', needsYou: 3 })), 3);
  assert.equal(needsYouCount(session('x', '', { chat: 'waiting' })), 1, 'an old daemon says only that it waits');
  assert.equal(needsYouCount(session('x', '', { status: 'working', needsYou: 0 })), 0);
});

test('a session tells you when a permission waits in it, it stops, or its turn ends with its agents done', () => {
  const at = (over: Partial<T.LeadSession>) => session('prod', '', over);
  const kinds = (prev: T.LeadSession[] | undefined, next: T.LeadSession[]) => sessionAlerts(prev, next).map((a) => `${a.session.id}:${a.kind}`);
  assert.deepEqual(kinds(undefined, [at({ status: 'needsYou', needsYou: 1 })]), [], 'the first list read is no change');
  assert.deepEqual(kinds([at({ status: 'working' })], [at({ status: 'needsYou', needsYou: 1 })]), ['prod:needsYou']);
  assert.deepEqual(kinds([at({ status: 'needsYou', needsYou: 1 })], [at({ status: 'needsYou', needsYou: 2 })]), ['prod:needsYou'], 'another one waits');
  assert.deepEqual(kinds([at({ status: 'needsYou', needsYou: 2 })], [at({ status: 'needsYou', needsYou: 1 })]), [], 'one was answered');
  assert.deepEqual(
    kinds([at({ status: 'working' })], [at({ status: 'needsYou', needsYou: 1, questions: 1 })]),
    [],
    'a question notifies on its own',
  );
  assert.deepEqual(kinds([at({ status: 'working' })], [at({ status: 'failed' })]), ['prod:failed']);
  assert.deepEqual(kinds([at({ status: 'failed' })], [at({ status: 'failed' })]), []);
  assert.deepEqual(kinds([at({ status: 'working' })], [at({ status: 'idle' })]), ['prod:done']);
  assert.deepEqual(kinds([at({ status: 'working' })], [at({ status: 'idle', agentsWorking: 1 })]), [], 'its agents still work');
  assert.deepEqual(kinds([at({ status: 'idle' })], [at({ status: 'idle', unread: true })]), []);
  assert.deepEqual(kinds([main], [main, at({ status: 'needsYou', needsYou: 1 })]), [], 'a session just opened');
});

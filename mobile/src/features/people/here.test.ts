import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { initials, personColor, PERSON_COLORS } from '../../shared/people.ts';
import { setLanguage, t } from '../../shared/i18n.ts';
import { applyHere, inChat, onAgent, others, peopleIn, seedHere, typingLine, whereLine } from './here.ts';

setLanguage('en-US');

const since = '2026-11-25T09:00:00Z';
const person = (id: string, name: string, at: Partial<T.PersonHere> = {}): T.PersonHere => ({ id, name, project: 'shared', since, ...at });
const own = person('', 'Leo');
const ana = person('peer-ana', 'Ana', { agent: 'agent-12', tab: 'desktop' });
const bruno = person('peer-bruno', 'Bruno', { session: 'main', typing: true });

test("this AgentBox's own user is never shown, wherever they are", () => {
  assert.deepEqual(others([own, ana]), [ana]);
  assert.deepEqual(others(undefined), []);
  // Nobody else in a project: it is not held at all.
  assert.deepEqual(seedHere([{ project: 'shared', people: [own] }, { project: 'web', people: [ana] }]), [{ project: 'web', people: [ana] }]);
});

test("an event replaces one project's people and leaves the others", () => {
  const held = seedHere([
    { project: 'shared', people: [ana] },
    { project: 'web', people: [bruno] },
  ]);
  const moved = applyHere(held, { project: 'shared', people: [own, bruno] });
  assert.deepEqual(peopleIn(moved, 'shared'), [bruno]);
  assert.deepEqual(peopleIn(moved, 'web'), [bruno]);
  // Everybody left: the project goes, and an event can arrive before anything is read.
  assert.deepEqual(peopleIn(applyHere(moved, { project: 'shared', people: [] }), 'shared'), []);
  assert.deepEqual(peopleIn(applyHere(undefined, { project: 'shared', people: [ana] }), 'shared'), [ana]);
  // The held list is not changed in place (react-query compares by identity).
  assert.deepEqual(peopleIn(held, 'shared'), [ana]);
  assert.deepEqual(peopleIn(held, undefined), []);
});

test('on an agent is the people whose agent that is, however long they stay', () => {
  assert.deepEqual(onAgent([ana, bruno], 'agent-12'), [ana]);
  assert.deepEqual(onAgent([ana, bruno], 'agent-13'), []);
});

test("a chat's people are those in it, as the desktop's ChatPeople says", () => {
  const onAgentChat = person('p1', 'Cy', { agent: 'agent-12' });
  const inOtherSession = person('p2', 'Di', { session: 'lead.prod' });
  const inProject = person('p3', 'Ed');
  const all = [ana, bruno, onAgentChat, inOtherSession, inProject];
  // The project's main session: those in it, and those in the project with nothing open.
  assert.deepEqual(inChat(all, { session: 'main' }), [bruno, inProject]);
  assert.deepEqual(inChat(all, { session: 'lead.prod' }), [inOtherSession]);
  // An agent's own chat: its chat tab, not its desktop.
  assert.deepEqual(inChat(all, { agent: 'agent-12' }), [onAgentChat]);
});

test('where a person is, in words', () => {
  assert.equal(whereLine(t, ana), "Ana is on agent-12's desktop");
  assert.equal(whereLine(t, person('x', 'Bo', { agent: 'agent-3', tab: 'terminal' })), "Bo is in agent-3's terminal");
  assert.equal(whereLine(t, person('x', 'Bo', { agent: 'agent-3', tab: 'chat' })), 'Bo is on agent-3');
  assert.equal(whereLine(t, bruno), 'Bruno is in the chat');
  assert.equal(whereLine(t, person('x', 'Bo')), 'Bo is in this project');
});

test('who is typing, in words', () => {
  assert.equal(typingLine(t, [ana]), '');
  assert.equal(typingLine(t, [ana, bruno]), 'Bruno is typing…');
  assert.equal(typingLine(t, [bruno, person('x', 'Cy', { typing: true })]), 'Bruno, Cy are typing…');
});

test('a name has one hue and the same letters on the phone as on the desktop', () => {
  assert.equal(personColor({ name: 'Ana' }), personColor({ name: ' ANA ' }));
  assert.ok((PERSON_COLORS as readonly string[]).includes(personColor({ name: 'Bruno' })));
  assert.equal(initials("Alice's AgentBox"), 'AA');
  assert.equal(initials(''), '?');
});

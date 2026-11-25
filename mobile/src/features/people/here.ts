// The people of a shared project, as the phone holds them: who else is in each
// project now and what they have open (api.ProjectHere), read once from
// GET /v1/here and kept current by the daemon's EventHere. After the desktop's
// renderer/lib/here.ts and components/People.tsx, without what those say of
// their own window: a phone says nothing of what it has open (PUT /v1/here is
// refused to phones, lanRefused in internal/daemon/lan.go), it only shows who
// else is there.
//
// A person with id "" is this AgentBox's own user, on whichever machine: they
// are left out of what is shown.
import type * as T from '../../shared/api.ts';
import type { t as translate } from '../../shared/i18n.ts';

// hereKey is the query that holds it: a list of the projects somebody else is in.
export const hereKey = ['here'];

const none: T.PersonHere[] = [];

// others is the people of a list besides this AgentBox's own user.
export function others(people: readonly T.PersonHere[] | undefined): T.PersonHere[] {
  return (people ?? none).filter((p) => p.id !== '');
}

// seedHere is GET /v1/here's answer as held: only the projects somebody else is in.
export function seedHere(all: readonly T.ProjectHere[]): T.ProjectHere[] {
  return all.flatMap((ph) => {
    const people = others(ph.people);
    return people.length > 0 ? [{ project: ph.project, people }] : [];
  });
}

// applyHere takes EventHere's data, who is in one project now, into what is held.
export function applyHere(held: readonly T.ProjectHere[] | undefined, ph: T.ProjectHere): T.ProjectHere[] {
  const rest = (held ?? []).filter((h) => h.project !== ph.project);
  return [...rest, ...seedHere([ph])];
}

// peopleIn is who else is in a project now.
export function peopleIn(held: readonly T.ProjectHere[] | undefined, project: string | undefined): T.PersonHere[] {
  return (project ? held?.find((h) => h.project === project)?.people : undefined) ?? none;
}

// onAgent is the people on one agent (its name).
export function onAgent(people: readonly T.PersonHere[], agent: string): T.PersonHere[] {
  return people.filter((p) => p.agent === agent);
}

// inChat is who is in the chat on screen: with a session, a session of the
// project's chat (the agent a person has open beside it doesn't move them
// out), else an agent's own chat, which is its chat tab rather than its
// desktop or terminal.
export function inChat(people: readonly T.PersonHere[], on: { session?: string; agent?: string }): T.PersonHere[] {
  const { session, agent } = on;
  if (session) return people.filter((p) => (p.session ? p.session === session : !p.agent && session === 'main'));
  return people.filter((p) => !p.session && p.agent === agent && (p.tab || 'chat') === 'chat');
}

// whereLine says where a person is: on an agent's desktop or terminal, on an
// agent, in the chat, or in the project.
export function whereLine(t: typeof translate, p: T.PersonHere): string {
  if (p.agent && p.tab === 'desktop') return t('people.onDesktop', { name: p.name, agent: p.agent });
  if (p.agent && p.tab === 'terminal') return t('people.inTerminal', { name: p.name, agent: p.agent });
  if (p.agent) return t('people.onAgent', { name: p.name, agent: p.agent });
  if (p.session) return t('people.inChat', { name: p.name });
  return t('people.inProject', { name: p.name });
}

// typingLine says who is writing a message, or '' when nobody is.
export function typingLine(t: typeof translate, people: readonly T.PersonHere[]): string {
  const typing = people.filter((p) => p.typing);
  if (typing.length === 0) return '';
  return typing.length === 1 ? t('people.typingOne', { name: typing[0].name }) : t('people.typingMany', { names: typing.map((p) => p.name).join(', ') });
}

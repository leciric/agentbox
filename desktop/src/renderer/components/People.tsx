import type * as T from '../../shared/api';
import { sessionOfRef } from '../lib/api';
import { onAgent, usePeopleIn } from '../lib/here';
import { useT } from '../lib/i18n';
import { initials, personColor } from '../lib/people';
import { cn } from '../lib/utils';
import { Tip } from './ui/tooltip';

// The people of a shared project (lib/here.ts): who else is in it, where,
// and who is typing. Desktops and terminals stay shared: being there is only
// shown, nothing is locked.

// PersonAvatar is a person's initials on their colour.
export function PersonAvatar({ person, size = 'sm', className }: { person: T.Person; size?: 'xs' | 'sm'; className?: string }) {
  return (
    <span
      className={cn('person-avatar inline-flex shrink-0 select-none items-center justify-center rounded-full font-semibold', size === 'xs' ? 'size-4' : 'size-5', className)}
      data-size={size}
      data-person-color={personColor(person)}
      data-person={person.id}
      title={person.name}
      aria-label={person.name}
    >
      {initials(person.name)}
    </span>
  );
}

// Avatars is a row of people, overlapping, the first few and how many more.
export function Avatars({ people, max = 3, size = 'sm', className }: { people: T.PersonHere[]; max?: number; size?: 'xs' | 'sm'; className?: string }) {
  const t = useT();
  if (people.length === 0) return null;
  const shown = people.slice(0, max);
  const more = people.length - shown.length;
  return (
    <span className={cn('inline-flex shrink-0 items-center', className)} data-people={people.length} title={people.map((p) => p.name).join(', ')}>
      {shown.map((p, i) => (
        <PersonAvatar key={p.id} person={p} size={size} className={cn(i > 0 && '-ml-1.5')} />
      ))}
      {more > 0 && <span className="ml-1 text-caption text-subtle">{t('people.more', { count: more })}</span>}
    </span>
  );
}

// whereLine says where a person is: on an agent's desktop or terminal, on an
// agent, in the chat, or in the project.
export function useWhere(): (p: T.PersonHere) => string {
  const t = useT();
  return (p) => {
    if (p.agent && p.tab === 'desktop') return t('people.onDesktop', { name: p.name, agent: p.agent });
    if (p.agent && p.tab === 'terminal') return t('people.inTerminal', { name: p.name, agent: p.agent });
    if (p.agent) return t('people.onAgent', { name: p.name, agent: p.agent });
    if (p.session) return t('people.inChat', { name: p.name });
    return t('people.inProject', { name: p.name });
  };
}

// PresenceLine is everyone else in a project, each with where they are.
export function PresenceLine({ people, className }: { people: T.PersonHere[]; className?: string }) {
  const where = useWhere();
  if (people.length === 0) return null;
  return (
    <div className={cn('flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1', className)} data-presence-line>
      {people.map((p) => (
        <span key={p.id} className="inline-flex min-w-0 items-center gap-1.5 text-caption text-subtle" data-presence={p.id}>
          <PersonAvatar person={p} size="xs" />
          <span className="min-w-0 truncate">{where(p)}</span>
        </span>
      ))}
    </div>
  );
}

// TypingLine says who else is writing a message in what is on screen.
export function TypingLine({ people, className }: { people: T.PersonHere[]; className?: string }) {
  const t = useT();
  const typing = people.filter((p) => p.typing);
  if (typing.length === 0) return null;
  const text = typing.length === 1 ? t('people.typingOne', { name: typing[0].name }) : t('people.typingMany', { names: typing.map((p) => p.name).join(', ') });
  return (
    <div className={cn('flex min-w-0 items-center gap-1.5 text-caption text-subtle', className)} role="status" data-typing={typing.map((p) => p.id).join(' ')}>
      <Avatars people={typing} size="xs" />
      <span className="chat-shine min-w-0 truncate">{text}</span>
    </div>
  );
}

// HerePeople is who else is in a project, or on one of its agents, as
// avatars with where each one is in a tooltip, and the project's access when
// another AgentBox shares it and lets this one only look or chat.
export function HerePeople({ project, agent }: { project: T.Project; agent?: string }) {
  const t = useT();
  const all = usePeopleIn(project.name);
  const people = agent ? onAgent(all, agent) : all;
  const where = useWhere();
  const access = project.remote?.access;
  return (
    <span className="flex shrink-0 items-center gap-2" data-here-people={people.length}>
      {people.length > 0 && (
        <Tip
          label={
            <span className="grid gap-0.5">
              {people.map((p) => (
                <span key={p.id}>{where(p)}</span>
              ))}
            </span>
          }
        >
          <span className="inline-flex">
            <Avatars people={people} />
          </span>
        </Tip>
      )}
      {(access === 'view' || access === 'chat') && (
        <Tip label={access === 'view' ? t('people.viewOnlyNote', { owner: project.remote!.owner }) : t('peers.access.chat')}>
          <span className="rounded-full border border-line px-2 py-px text-micro text-subtle" data-remote-access={access}>
            {access === 'view' ? t('people.viewOnly') : t('people.chatOnly')}
          </span>
        </Tip>
      )}
    </span>
  );
}

// ChatPeople, above a chat's composer, is where everyone else in its project
// is, and who is typing in this chat: a session of the project's chat, or an
// agent's own.
export function ChatPeople({ chatRef }: { chatRef: string }) {
  const [project, name] = chatRef.split('/');
  const all = usePeopleIn(project);
  if (all.length === 0) return null;
  const session = sessionOfRef(chatRef);
  // A project's page says its session (with the agent its panel shows, if
  // any); an agent's page only the agent.
  const here = session ? all.filter((p) => (p.session ? p.session === session : !p.agent && session === 'main')) : all.filter((p) => !p.session && p.agent === name && (p.tab || 'chat') === 'chat');
  return (
    <div className="mb-1.5 grid min-w-0 gap-1 px-2" data-chat-people>
      <TypingLine people={here} />
      <PresenceLine people={all.filter((p) => !p.typing || !here.includes(p))} />
    </div>
  );
}

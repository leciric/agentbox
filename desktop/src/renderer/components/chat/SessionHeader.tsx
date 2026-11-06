import { useQuery } from '@tanstack/react-query';
import { AlarmClock, Archive, Bot, FileDiff, GitBranch, LoaderCircle } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../../shared/api';
import { language } from '../../../shared/i18n/index.ts';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { snoozeChoices } from '../../lib/sessions';
import { cn } from '../../lib/utils';
import { PrChip } from '../PrChip';
import { AgentDiff } from '../ReadyToReview';
import { Button } from '../ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../ui/dialog';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import { openAgent } from './AgentPermissionCard';

// useSessionOutput is what a session made (daemon/sessionsettle.go): its
// agents, live then retired, each with its pull request as last read. The
// sessions' and pull requests' events read it again (lib/events.ts).
export function useSessionOutput(project: string, session: string | undefined) {
  return useQuery({
    queryKey: ['sessionOutput', project, session],
    queryFn: () => api.sessionOutput(project, session!),
    enabled: !!session,
    refetchInterval: 60_000,
  });
}

// wakeTime is when a snoozed session comes back, short: "Thu 09:00".
export function wakeTime(iso: string): string {
  return new Intl.DateTimeFormat(language(), { weekday: 'short', hour: 'numeric', minute: '2-digit' }).format(new Date(iso));
}

// SessionHeader is the strip above a session's chat that ties it to what it
// made: its agents, each with a spinner while it works and its pull request's
// state and checks; Changes, every agent's diff in one view (SessionChanges);
// and Snooze. Clicking an agent opens it. A session that made nothing yet
// says so, and can still be snoozed.
export function SessionHeader({ project, session, onSnooze }: { project: string; session: T.LeadSession; onSnooze: (until: Date) => void }) {
  const t = useT();
  const output = useSessionOutput(project, session.id);
  const agents = output.data?.agents ?? [];
  const [changes, setChanges] = useState(false);
  return (
    <div className="flex min-w-0 shrink-0 items-center gap-2 border-b border-line-faint px-4 py-1.5" data-session-header={session.id}>
      <Bot className="size-3.5 shrink-0 text-faint" aria-hidden />
      {/* It scrolls sideways with no scrollbar: its end fades, so a chip cut
          off reads as "more this way" rather than a broken word. */}
      <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto pr-6 [mask-image:linear-gradient(to_right,black_calc(100%-1.5rem),transparent)] [scrollbar-width:none]" data-session-agents>
        {agents.length === 0 ? (
          <span className="truncate text-caption text-faint">{output.data ? t('sessions.header.none') : t('common.loading')}</span>
        ) : (
          agents.map((a) => <AgentPill key={a.ref} agent={a} />)
        )}
      </div>
      {agents.length > 0 && (
        <Tip label={t('sessions.header.changesHint')}>
          <Button variant="ghost" size="sm" className="h-6 shrink-0 gap-1 px-1.5 text-caption" onClick={() => setChanges(true)} data-session-changes>
            <FileDiff className="size-3.5" />
            {t('sessions.header.changes')}
          </Button>
        </Tip>
      )}
      <SnoozeMenu onSnooze={onSnooze} />
      {changes && <SessionChanges topic={session.topic} agents={agents} onClose={() => setChanges(false)} />}
    </div>
  );
}

function AgentPill({ agent }: { agent: T.SessionAgent }) {
  const t = useT();
  const name = agent.title || agent.name;
  const body = (
    <>
      {agent.working ? (
        <LoaderCircle className="size-3 shrink-0 animate-spin text-sky-400" aria-label={t('sessions.working')} />
      ) : agent.archived ? (
        <Archive className="size-3 shrink-0 text-faint" aria-label={t('sessions.header.retired')} />
      ) : null}
      <span className="max-w-40 truncate">{name}</span>
      {agent.pr && <PrChip pr={agent.pr} dataAttr="data-session-pr" />}
    </>
  );
  const cls = 'flex h-6 shrink-0 items-center gap-1.5 rounded-full border border-line-faint bg-surface-faint pl-2 pr-1 text-caption text-secondary';
  if (agent.archived) {
    return (
      <span className={cn(cls, 'text-subtle', !agent.pr && 'pr-2')} title={t('sessions.header.retiredNamed', { name })} data-session-agent={agent.ref} data-session-agent-archived>
        {body}
      </span>
    );
  }
  // The pill is the agent's button stretched over it (its ::before), with the
  // PR chip, a link of its own, above it: not a link inside a button, which a
  // screen reader takes as one control.
  return (
    <span
      className={cn(cls, 'relative has-[>button:hover]:border-line has-[>button:hover]:bg-surface-raised has-[>button:hover]:text-primary', !agent.pr && 'pr-2')}
      data-session-agent={agent.ref}
      data-session-agent-working={agent.working ? '' : undefined}
    >
      <button
        type="button"
        className="flex min-w-0 items-center gap-1.5 rounded-full before:absolute before:inset-0 before:rounded-full focus-visible:outline-none focus-visible:before:ring-2 focus-visible:before:ring-brand-400"
        onClick={() => openAgent(agent.ref)}
        title={name}
      >
        {agent.working && <LoaderCircle className="size-3 shrink-0 animate-spin text-sky-400" aria-label={t('sessions.working')} />}
        <span className="max-w-40 truncate">{name}</span>
      </button>
      {agent.pr && (
        <span className="relative">
          <PrChip pr={agent.pr} dataAttr="data-session-pr" />
        </span>
      )}
    </span>
  );
}

// SnoozeMenu offers the times a session can be snoozed until.
export function SnoozeMenu({ onSnooze }: { onSnooze: (until: Date) => void }) {
  const t = useT();
  const [now, setNow] = useState(() => new Date());
  return (
    <Menu onOpenChange={(open) => open && setNow(new Date())}>
      <Tip label={t('sessions.snooze.hint')}>
        <MenuTrigger asChild>
          <Button variant="ghost" size="sm" className="h-6 shrink-0 gap-1 px-1.5 text-caption" data-session-snooze>
            <AlarmClock className="size-3.5" />
            {t('sessions.snooze.label')}
          </Button>
        </MenuTrigger>
      </Tip>
      <MenuContent>
        <MenuLabel>{t('sessions.snooze.until')}</MenuLabel>
        {snoozeChoices(now).map((c) => (
          <MenuItem key={c.key} onSelect={() => onSnooze(c.until)} hint={wakeTime(c.until.toISOString())} data-session-snooze-choice={c.key}>
            {t(`sessions.snooze.${c.key}`)}
          </MenuItem>
        ))}
      </MenuContent>
    </Menu>
  );
}

// SessionChanges is every one of a session's agents' diffs in one view, one
// after the other, each as the review shows it (AgentDiff): a live agent's
// against its base, a retired one's from its kept branch.
function SessionChanges({ topic, agents, onClose }: { topic: string; agents: T.SessionAgent[]; onClose: () => void }) {
  const t = useT();
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[88vh] max-w-5xl flex-col gap-0 p-0" data-session-changes-view>
        <DialogHeader className="shrink-0 border-b border-line px-6 py-4">
          <DialogTitle>{t('sessions.header.changesTitle', { topic })}</DialogTitle>
          <DialogDescription>{t('sessions.header.changesDescription', { count: agents.length })}</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {agents.map((a) => (
            <section key={a.ref} className="border-b border-line" data-session-changes-agent={a.ref}>
              <div className="sticky top-0 z-10 flex min-w-0 items-center gap-2 border-b border-line-faint bg-modal px-5 py-2.5">
                <span className="min-w-0 truncate text-ui font-medium text-primary">{a.title || a.name}</span>
                {a.branch && (
                  <span className="flex min-w-0 items-center gap-1 font-mono text-caption text-subtle">
                    <GitBranch className="size-3 shrink-0 text-faint" />
                    <span className="truncate">{a.branch}</span>
                  </span>
                )}
                {a.pr && <PrChip pr={a.pr} />}
                {a.archived && <span className="shrink-0 text-caption text-faint">{t('sessions.header.retired')}</span>}
              </div>
              <AgentDiff agent={a.ref} past={a.archived} />
            </section>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}

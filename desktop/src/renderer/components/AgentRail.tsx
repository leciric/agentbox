import { useQuery } from '@tanstack/react-query';
import { ChevronRight, GitBranch, MessagesSquare, PanelRight, Plus } from 'lucide-react';
import { useEffect, useState } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { chatLabel, rank } from '../lib/agentStatus';
import { useCpuHistory } from '../lib/useCpuHistory';
import { cn, humanBytes, timeAgo } from '../lib/utils';
import { AgentThread, eventsByAgent, latestLine, markSeen, unreadCount, useSeen } from './AgentThread';
import { Sparkline } from './Sparkline';
import { AgentAvatar } from './state';
import { Tip } from './ui/tooltip';

// AgentRail is a project's agents, kept visible beside whatever you're looking
// at: the lead's chat, an agent's own view, or the Agents tab behind it.
//
// One row per agent, and that row is two things at once. Clicking it opens the
// agent, the way it always did — status, how busy it is, its pull request.
// Opening the chevron unfolds its thread: what it has reported, a question with
// the box to answer it in, and a box to write back. They are one list on
// purpose: an agent and what it last said are the same thing to look at, and
// two rails of the same agents side by side was one rail too many.
export function AgentRail({ view, onSelect, onNewAgent }: { view: View; onSelect: (view: View) => void; onNewAgent: (project: string) => void }) {
  const project = view.kind === 'agent' ? view.ref.split('/')[0] : view.kind === 'project' ? view.project : null;
  const enabled = project !== null;
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, enabled });
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage, enabled });
  // Pull requests come from the fleet, which already carries one per agent,
  // matched against the repository list the daemon keeps (D54). ['agents'] is
  // every project's agents and would have to read GitHub per project to say
  // the same thing. Sharing the Agents tab's key means one request when both
  // are open, and this slower poll when only the rail is: what moves in
  // between arrives as an event.
  const fleet = useQuery({
    queryKey: ['fleet', project],
    queryFn: () => api.fleet(project as string),
    enabled,
    refetchInterval: 30_000,
  });
  // What the agents reported, and the questions behind the ones that asked.
  // The questions are read live rather than copied into the events: a question
  // is escalated and then answered after the event recording it was written.
  const events = useQuery({ queryKey: ['agentEvents', project], queryFn: () => api.agentEvents(project as string), enabled });
  const questions = useQuery({ queryKey: ['questions', project], queryFn: () => api.questions(project as string), enabled });
  const history = useCpuHistory(usage.data);
  const seen = useSeen();
  const [folded, setFolded] = useFolded();
  const [openRef, setOpenRef] = useState<string | null>(null);

  const byAgent = eventsByAgent(events.data ?? []);
  const byId = new Map((questions.data ?? []).map((q) => [q.id, q]));
  // An open thread counts as read, and stays read while new events land in it.
  const openEvents = openRef ? byAgent.get(openRef) : undefined;
  const openLatest = openEvents?.at(-1)?.at;
  useEffect(() => {
    if (openRef && openLatest) markSeen(openRef, openLatest);
  }, [openRef, openLatest]);

  if (!project) return null;
  const mine = (agents.data ?? []).filter((a) => a.project === project).toSorted((a, b) => rank(a) - rank(b));
  const prs = new Map((fleet.data?.agents ?? []).map((a) => [a.ref, a.pr]));
  const onLead = view.kind === 'project';
  const open = (ref: string) => {
    setFolded(false);
    setOpenRef(ref);
  };

  if (folded) {
    return (
      <aside className="flex w-[56px] shrink-0 flex-col items-center gap-1 border-l border-line bg-rail py-2 backdrop-blur-xl" data-agent-rail="folded" aria-label={`${project}'s agents`}>
        <Tip label="Show the agents">
          <button aria-label="Show the agents" className="rounded-lg p-2 text-subtle transition hover:bg-surface-strong hover:text-primary" onClick={() => setFolded(false)}>
            <PanelRight className="size-4" />
          </button>
        </Tip>
        <Tip label="Project chat">
          <button
            aria-label="Project chat"
            className={cn('flex size-9 items-center justify-center rounded-xl border border-line-strong bg-gradient-to-br from-brand-500/25 via-indigo-500/10 to-transparent text-brand-200 transition', onLead && 'ring-1 ring-brand-400')}
            onClick={() => onSelect({ kind: 'project', project })}
          >
            <MessagesSquare className="size-4" />
          </button>
        </Tip>
        {mine.map((agent) => {
          const unread = unreadCount(byAgent.get(agent.ref) ?? [], seen[agent.ref]);
          return (
            <Tip key={agent.ref} label={`${agent.title || agent.name}${unread > 0 ? ` — ${unread} new` : ''}`}>
              <button aria-label={agent.title || agent.name} data-rail-icon={agent.ref} className="relative rounded-xl p-0.5 transition hover:bg-surface-strong" onClick={() => open(agent.ref)}>
                <AgentAvatar ai={agent.ai} state={agent.state} className="size-9" />
                {unread > 0 && <span className="absolute right-0 top-0 size-2 rounded-full bg-brand-400 ring-2 ring-ink" />}
              </button>
            </Tip>
          );
        })}
      </aside>
    );
  }

  return (
    <aside className="flex w-[268px] shrink-0 flex-col border-l border-line bg-rail backdrop-blur-xl xl:w-[312px]" data-agent-rail="open" aria-label={`${project}'s agents`}>
      <div className="flex h-12 shrink-0 items-center gap-2 px-3">
        <span className="text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle">Agents</span>
        {mine.length > 0 && <span className="rounded-full bg-surface-raised px-1.5 text-[10.5px] tabular-nums text-subtle">{mine.length}</span>}
        <Tip label={`New agent in ${project}`}>
          <button
            aria-label={`New agent in ${project}`}
            className="ml-auto rounded-md p-1.5 text-subtle transition hover:bg-surface-strong hover:text-primary"
            onClick={() => onNewAgent(project)}
          >
            <Plus className="size-3.5" />
          </button>
        </Tip>
        <Tip label="Hide the agents">
          <button
            aria-label="Hide the agents"
            data-rail-fold
            className="rounded-md p-1.5 text-subtle transition hover:bg-surface-strong hover:text-primary"
            onClick={() => {
              setFolded(true);
              setOpenRef(null);
            }}
          >
            <PanelRight className="size-3.5" />
          </button>
        </Tip>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-2 pb-3">
        <button
          onClick={() => onSelect({ kind: 'project', project })}
          className={cn('group relative flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors xl:py-2.5', onLead ? 'bg-surface-strong' : 'hover:bg-surface')}
        >
          {onLead && <span className="absolute inset-y-1.5 left-0 w-[3px] rounded-full bg-brand-400" />}
          <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-line-strong bg-gradient-to-br from-brand-500/25 via-indigo-500/10 to-transparent text-brand-200">
            <MessagesSquare className="size-4" />
          </span>
          <span className="min-w-0 flex-1">
            <span className={cn('block truncate text-[13px] font-medium', onLead ? 'text-title' : 'text-secondary')}>Project chat</span>
            <span className="block truncate text-[11px] text-subtle">The lead's conversation</span>
          </span>
        </button>

        {mine.length > 0 && <div className="my-1.5 border-t border-line-faint" />}

        {mine.map((agent) => (
          <AgentRow
            key={agent.ref}
            agent={agent}
            active={view.kind === 'agent' && view.ref === agent.ref}
            open={openRef === agent.ref}
            sample={usage.data?.agents.find((u) => u.ref === agent.ref)}
            cpuHistory={history.get(agent.ref) ?? []}
            pr={prs.get(agent.ref)}
            events={byAgent.get(agent.ref) ?? []}
            unread={unreadCount(byAgent.get(agent.ref) ?? [], seen[agent.ref])}
            questions={byId}
            onSelect={() => onSelect({ kind: 'agent', ref: agent.ref })}
            onToggle={() => (openRef === agent.ref ? setOpenRef(null) : open(agent.ref))}
          />
        ))}

        {mine.length === 0 && (
          <div className="mt-2 grid justify-items-center gap-2 px-2 py-8 text-center">
            <p className="text-[12.5px] leading-relaxed text-subtle">No agents yet in {project}.</p>
            <button className="flex items-center gap-1.5 rounded-lg border border-line-strong px-2.5 py-1.5 text-[12.5px] text-tertiary transition hover:bg-surface-raised" onClick={() => onNewAgent(project)}>
              <Plus className="size-3.5" />
              New agent
            </button>
          </div>
        )}
      </div>
    </aside>
  );
}

function AgentRow({
  agent,
  active,
  open,
  sample,
  cpuHistory,
  pr,
  events,
  unread,
  questions,
  onSelect,
  onToggle,
}: {
  agent: T.Agent;
  active: boolean;
  open: boolean;
  sample?: T.AgentUsage;
  cpuHistory: number[];
  pr?: T.PullRequest;
  events: T.AgentEvent[];
  unread: number;
  questions: Map<string, T.Question>;
  onSelect: () => void;
  onToggle: () => void;
}) {
  const status = chatLabel(agent);
  const latest = latestLine(events, questions);
  const at = events.at(-1)?.at;
  return (
    <div className={cn('rounded-xl', open && 'bg-surface-faint ring-1 ring-inset ring-line')}>
      {/* The row is a button that opens the agent, with the chevron and the pull
          request badge over it: a button and a link inside a button are neither
          valid HTML nor clickable on their own. */}
      <div className="relative">
        <button
          data-agent={agent.ref}
          onClick={onSelect}
          className={cn('group relative flex w-full items-center gap-2.5 rounded-xl py-2 pl-2.5 pr-7 text-left transition-colors xl:py-2.5', active ? 'bg-surface-strong' : 'hover:bg-surface')}
        >
          {active && <span className="absolute inset-y-1.5 left-0 w-[3px] rounded-full bg-brand-400" />}
          <AgentAvatar ai={agent.ai} state={agent.state} className="size-8" />
          <span className={cn('min-w-0 flex-1', pr && 'pr-11')}>
            <span className={cn('block truncate text-[13px] font-medium', active ? 'text-title' : 'text-secondary')}>{agent.title || agent.name}</span>
            <span className="mt-0.5 flex items-center gap-1.5 text-[11px]">
              {status.tone === 'urgent' && <span className="size-1.5 shrink-0 animate-pulse rounded-full bg-amber-400" />}
              {status.tone === 'error' && <span className="size-1.5 shrink-0 rounded-full bg-rose-400" />}
              <span
                className={cn(
                  'shrink-0',
                  status.tone === 'urgent' && 'font-medium text-amber-300',
                  status.tone === 'error' && 'font-medium text-rose-300',
                  status.tone === 'live' && 'chat-shine font-medium',
                  status.tone === 'muted' && 'text-subtle',
                )}
              >
                {status.text}
              </span>
              {at && <span className="ml-auto shrink-0 tabular-nums text-faint">{timeAgo(at)}</span>}
            </span>
            {/* What it last reported, which is what the thread below opens on.
                An agent that has reported nothing shows its branch instead —
                there is nothing to say about it yet, and the branch is what you
                would otherwise go looking for. */}
            {latest ? (
              <span className="mt-1 flex items-center gap-1.5">
                <span className={cn('min-w-0 flex-1 truncate text-[11px]', latest.urgent ? 'font-medium text-amber-300' : 'text-subtle')} data-rail-latest>
                  {latest.text}
                </span>
                {unread > 0 && !open && (
                  <span className="shrink-0 rounded-full bg-brand-500/25 px-1.5 text-[10px] font-medium tabular-nums text-brand-200" data-rail-unread>
                    {unread}
                  </span>
                )}
              </span>
            ) : (
              <span className="mt-1 hidden items-center gap-1 font-mono text-[10.5px] text-faint xl:flex">
                <GitBranch className="size-3 shrink-0" />
                <span className="truncate">{agent.branch}</span>
              </span>
            )}
            {sample && agent.state === 'running' && (
              <span className="mt-1.5 hidden items-center gap-2 xl:flex">
                <Sparkline values={cpuHistory} className={status.tone === 'live' ? 'text-sky-300/80' : 'text-subtle'} />
                <span className="font-mono text-[10px] tabular-nums text-subtle">
                  {sample.cpu.toFixed(0)}% · {humanBytes(sample.memory)}
                </span>
              </span>
            )}
          </span>
        </button>
        {pr && <PullRequestBadge pr={pr} />}
        {events.length > 0 && (
          <Tip label={open ? 'Close the thread' : `What ${agent.name} reported`}>
            <button
              data-rail-thread={agent.ref}
              aria-label={open ? `Close ${agent.name}'s thread` : `Open ${agent.name}'s thread`}
              aria-expanded={open}
              className="absolute bottom-0 right-0 top-0 flex w-7 items-center justify-center rounded-r-xl text-faint transition hover:bg-surface-raised hover:text-secondary"
              onClick={onToggle}
            >
              <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} />
            </button>
          </Tip>
        )}
      </div>
      {open && <AgentThread agent={agent.ref} name={agent.name} running={agent.state === 'running'} events={events} questions={questions} />}
    </div>
  );
}

// Whether the rail is folded down to icons, kept in this browser: it is a
// choice about this window, and the rail is the same rail on every view.
const foldedKey = 'agentbox.rail.folded';

function useFolded(): [boolean, (folded: boolean) => void] {
  const [folded, set] = useState(() => localStorage.getItem(foldedKey) === '1');
  return [
    folded,
    (next: boolean) => {
      localStorage.setItem(foldedKey, next ? '1' : '0');
      set(next);
    },
  ];
}

const checkMark: Record<string, string> = { passing: '✓', failing: '✕', pending: '•' };
const checkTone: Record<string, string> = { passing: 'text-emerald-300', failing: 'text-rose-300', pending: 'text-amber-300' };

// PullRequestBadge is the agent's pull request at a glance — its number, and
// whether its checks pass. Clicking it opens the pull request in the browser,
// the way the Pull requests tab does. An agent without one shows nothing.
function PullRequestBadge({ pr }: { pr: T.PullRequest }) {
  const state = pr.draft ? 'draft' : pr.state;
  return (
    <Tip label={`#${pr.number} ${state}${pr.checks ? `, checks ${pr.checks}` : ''} — ${pr.title}`}>
      <a
        href={pr.url}
        data-rail-pr={pr.number}
        aria-label={`Pull request #${pr.number}, ${state}`}
        onClick={(event) => {
          event.preventDefault();
          void window.agentbox.openExternal(pr.url);
        }}
        className={cn(
          'absolute right-7 top-2 flex items-center gap-1 rounded-full px-1.5 py-0.5 font-mono text-[10.5px] ring-1 ring-inset transition',
          pr.state === 'merged'
            ? 'bg-violet-400/10 text-violet-300 ring-violet-400/25 hover:bg-violet-400/20'
            : 'bg-surface-raised text-muted ring-line-strong hover:bg-surface-vivid hover:text-primary',
        )}
      >
        #{pr.number}
        {pr.checks && <span className={checkTone[pr.checks]}>{checkMark[pr.checks]}</span>}
      </a>
    </Tip>
  );
}

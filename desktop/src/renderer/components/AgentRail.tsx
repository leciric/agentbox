import { useQuery } from '@tanstack/react-query';
import { ChevronRight, GitBranch, PanelRight, Plus } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { avatarMood, chatLabel, isAsking, rank, settled, type Mood } from '../lib/agentStatus';
import { useCpuHistory } from '../lib/useCpuHistory';
import { cn, humanBytes, timeAgo } from '../lib/utils';
import { AgentContextMenu } from './AgentContextMenu';
import { AgentInfoCard } from './AgentInfoCard';
import { Sparkline } from './Sparkline';
import { AgentAvatar } from './state';
import { Tip } from './ui/tooltip';

// AgentRail is a project's agents, kept visible beside whatever you're looking
// at: the lead's chat, an agent's own view, or the Agents tab behind it.
//
// One row per agent: its name, its status and when it last reported, its pull
// request, its branch and how busy its machine is. Clicking it opens the agent.
// What the agents report and ask isn't read here: a question the lead passes
// on comes back in the lead's reply, a credential request is a card in the
// lead's chat and in the agent's own, and the avatar waves while either waits.
//
// The agents still at something — working, waiting on you, starting, or with
// a machine that needs a look — are on top. The rest, idle, paused or stopped,
// are folded under "Finished" at the bottom, closed unless you open it: a
// project that has run for a while has more agents that are done than ones
// that aren't, and those are not what you open the rail to find. The section
// opens by itself while the agent you're on is in it.
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
  // When each agent last reported, for the time on its row, and whether it
  // has a question waiting on you, for its avatar.
  const events = useQuery({ queryKey: ['agentEvents', project], queryFn: () => api.agentEvents(project as string), enabled });
  const questions = useQuery({ queryKey: ['questions', project], queryFn: () => api.questions(project as string), enabled });
  // The lead's own state, for its avatar: whether it's mid-turn or asking.
  const lead = useQuery({ queryKey: ['projectChat', project], queryFn: () => api.projectChat(project as string), enabled });
  const history = useCpuHistory(usage.data);
  const [folded, setFolded] = useFolded();
  const [showFinished, setShowFinished] = useShowFinished();

  // The events arrive newest first, so the first one seen is each agent's last.
  const lastReport = new Map<string, string>();
  for (const ev of events.data ?? []) if (!lastReport.has(ev.ref)) lastReport.set(ev.ref, ev.at);

  if (!project) return null;
  const mine = (agents.data ?? []).filter((a) => a.project === project).toSorted((a, b) => rank(a) - rank(b));
  const asking = (agent: T.Agent) => isAsking(questions.data, agent.ref);
  // An agent with a question waiting on you stays on top, whatever its chat is doing.
  const moving = mine.filter((a) => !settled(a) || asking(a));
  const finished = mine.filter((a) => settled(a) && !asking(a));
  const finishedOpen = showFinished || finished.some((a) => view.kind === 'agent' && view.ref === a.ref);
  const prs = new Map((fleet.data?.agents ?? []).map((a) => [a.ref, a.pr]));
  const onLead = view.kind === 'project';
  const leadMood = avatarMood({ state: 'running', chat: lead.data?.chat });
  const mood = (agent: T.Agent) => avatarMood(agent, asking(agent));

  const icon = (agent: T.Agent) => (
    <Tip key={agent.ref} side="right" className="max-w-none p-3" label={<AgentInfoCard agent={agent} pr={prs.get(agent.ref)} />}>
      <button
        aria-label={agent.title || agent.name}
        data-rail-icon={agent.ref}
        className="relative rounded-xl p-0.5 transition hover:bg-surface-strong"
        onClick={() => onSelect({ kind: 'agent', ref: agent.ref })}
      >
        <AgentAvatar ai={agent.ai} mood={mood(agent)} state={agent.state} seed={agent.ref} />
      </button>
    </Tip>
  );
  const row = (agent: T.Agent) => (
    <AgentRow
      key={agent.ref}
      agent={agent}
      active={view.kind === 'agent' && view.ref === agent.ref}
      sample={usage.data?.agents.find((u) => u.ref === agent.ref)}
      cpuHistory={history.get(agent.ref) ?? []}
      pr={prs.get(agent.ref)}
      at={lastReport.get(agent.ref)}
      asking={asking(agent)}
      mood={mood(agent)}
      onSelect={() => onSelect({ kind: 'agent', ref: agent.ref })}
      onOpen={onSelect}
    />
  );

  if (folded) {
    return (
      <aside className="flex w-[56px] shrink-0 flex-col items-center gap-1 border-l border-line bg-rail py-2 backdrop-blur-xl" data-agent-rail="folded" aria-label={`${project}'s agents`}>
        <Tip label="Show the agents">
          <button aria-label="Show the agents" className="rounded-lg p-2 text-subtle transition hover:bg-surface-strong hover:text-primary" onClick={() => setFolded(false)}>
            <PanelRight className="size-4" />
          </button>
        </Tip>
        <Tip label="Project chat">
          <button aria-label="Project chat" className="rounded-xl p-0.5 transition hover:bg-surface-strong" onClick={() => onSelect({ kind: 'project', project })}>
            <AgentAvatar ai="claude" mood={leadMood} seed={`${project}/lead`} className={cn(onLead && 'ring-1 ring-brand-400')} />
          </button>
        </Tip>
        {moving.map(icon)}
        {finished.length > 0 && (
          <>
            <div className="my-1 w-7 border-t border-line-faint" />
            <Tip label={finishedOpen ? 'Hide the finished agents' : `Show ${finished.length} finished`}>
              <button
                aria-label={finishedOpen ? 'Hide the finished agents' : `Show ${finished.length} finished`}
                aria-expanded={finishedOpen}
                data-rail-finished={finishedOpen ? 'open' : 'closed'}
                className="flex h-6 items-center gap-0.5 rounded-md px-1 text-[10.5px] tabular-nums text-subtle transition hover:bg-surface-strong hover:text-primary"
                onClick={() => setShowFinished(!finishedOpen)}
              >
                <ChevronRight className={cn('size-3 transition-transform', finishedOpen && 'rotate-90')} />
                {finished.length}
              </button>
            </Tip>
            {finishedOpen && finished.map(icon)}
          </>
        )}
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
            onClick={() => setFolded(true)}
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
          <AgentAvatar ai="claude" mood={leadMood} seed={`${project}/lead`} />
          <span className="min-w-0 flex-1">
            <span className={cn('block truncate text-[13px] font-medium', onLead ? 'text-title' : 'text-secondary')}>Project chat</span>
            <span className="block truncate text-[11px] text-subtle">The lead's conversation</span>
          </span>
        </button>

        {moving.length > 0 && <div className="my-1.5 border-t border-line-faint" />}

        {moving.map(row)}

        {finished.length > 0 && (
          <section className="mt-1.5 border-t border-line-faint pt-1.5" data-rail-finished={finishedOpen ? 'open' : 'closed'}>
            <button
              aria-expanded={finishedOpen}
              className="flex w-full items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-left text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle transition hover:bg-surface hover:text-primary"
              onClick={() => setShowFinished(!finishedOpen)}
            >
              <ChevronRight className={cn('size-3.5 shrink-0 transition-transform', finishedOpen && 'rotate-90')} />
              Finished
              <span className="rounded-full bg-surface-raised px-1.5 text-[10.5px] font-normal normal-case tracking-normal tabular-nums">{finished.length}</span>
            </button>
            {finishedOpen && finished.map(row)}
          </section>
        )}

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
  sample,
  cpuHistory,
  pr,
  at,
  asking,
  mood,
  onSelect,
  onOpen,
}: {
  agent: T.Agent;
  active: boolean;
  sample?: T.AgentUsage;
  cpuHistory: number[];
  pr?: T.PullRequest;
  at?: string;
  asking: boolean;
  mood: Mood;
  onSelect: () => void;
  onOpen: (view: View) => void;
}) {
  // A question or a credential request waiting on you is said in words too,
  // not only by the avatar: the agent is usually mid-turn, blocked on it, so
  // its chat alone would call it working. Opening the agent shows a credential
  // request's card; a question comes back in the lead's reply.
  const status: ReturnType<typeof chatLabel> = asking && agent.chat !== 'waiting' ? { text: 'Asks you something', tone: 'urgent' } : chatLabel(agent);
  // The row is a button that opens the agent, with the pull request badge over
  // it: a link inside a button is neither valid HTML nor clickable on its own.
  return (
    <AgentContextMenu agent={agent} pr={pr} active={active} onSelect={onOpen}>
      <div className="relative">
        <button
          data-agent={agent.ref}
          onClick={onSelect}
          className={cn('group relative flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors xl:py-2.5', active ? 'bg-surface-strong' : 'hover:bg-surface')}
        >
          {active && <span className="absolute inset-y-1.5 left-0 w-[3px] rounded-full bg-brand-400" />}
          <AgentAvatar ai={agent.ai} mood={mood} state={agent.state} seed={agent.ref} />
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
            <span className="mt-1 flex items-center gap-1 font-mono text-[10.5px] text-faint" data-rail-branch>
              <GitBranch className="size-3 shrink-0" />
              <span className="min-w-0 truncate">{agent.branch}</span>
            </span>
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
      </div>
    </AgentContextMenu>
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

// Whether the Finished section is open, kept in this browser like the fold.
// Closed until somebody opens it.
const finishedKey = 'agentbox.rail.finished';

function useShowFinished(): [boolean, (open: boolean) => void] {
  const [open, set] = useState(() => localStorage.getItem(finishedKey) === '1');
  return [
    open,
    (next: boolean) => {
      localStorage.setItem(finishedKey, next ? '1' : '0');
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
          'absolute right-2 top-2 flex items-center gap-1 rounded-full px-1.5 py-0.5 font-mono text-[10.5px] ring-1 ring-inset transition',
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

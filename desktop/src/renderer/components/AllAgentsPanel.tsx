import { useQuery } from '@tanstack/react-query';
import { ChevronRight, FolderGit2, Plus } from 'lucide-react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { chatLabel, rank, type StatusTone } from '../lib/agentStatus';
import { useCpuHistory } from '../lib/useCpuHistory';
import { cn, humanBytes } from '../lib/utils';
import { AgentContextMenu } from './AgentContextMenu';
import { Sparkline } from './Sparkline';
import { LiveAgentAvatar } from './state';
import { Button } from './ui/button';
import { Panel } from './ui/card';

// The tones in the order they should catch your eye: agents that need you
// first, then whatever's failed, then whatever's working, then the rest.
const toneOrder: StatusTone[] = ['urgent', 'error', 'live', 'muted'];

function compareAgents(a: T.Agent, b: T.Agent): number {
  const byRank = rank(a) - rank(b);
  if (byRank !== 0) return byRank;
  const byTone = toneOrder.indexOf(chatLabel(a).tone) - toneOrder.indexOf(chatLabel(b).tone);
  if (byTone !== 0) return byTone;
  return (a.title || a.name).localeCompare(b.title || b.name);
}

// AllAgentsPanel is every agent, from every project, as one dense,
// urgency-ordered list: the whole fleet at a glance, and a click straight into
// whichever one needs you. It reads its vocabulary from lib/agentStatus, the
// same place the rail and the sidebar do, so a state means the same thing here
// as it does everywhere else.
//
// Every agent is listed, not just the running ones - hiding a stopped agent
// would leave you wondering where it went. Agents with no machine up (stopped,
// paused) are just drawn quieter, so they don't compete with the ones doing
// something or waiting on you.
export function AllAgentsPanel({ onSelect, onNewAgent }: { onSelect: (view: View) => void; onNewAgent: () => void }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const history = useCpuHistory(usage.data);

  const all = (agents.data ?? []).toSorted(compareAgents);
  const emptyProjects = (projects.data ?? []).filter((p) => !all.some((a) => a.project === p.name));

  if (agents.data && all.length === 0) {
    return (
      <div className="panel grid justify-items-center gap-3 rounded-2xl px-4 py-10 text-center">
        <p className="text-[13.5px] text-muted">No agents yet. Create one to get started.</p>
        <Button size="sm" onClick={onNewAgent}>
          <Plus />
          New agent
        </Button>
      </div>
    );
  }

  return (
    <div>
      <div className="hidden items-center gap-3 px-4 pb-1.5 text-[11px] font-medium uppercase tracking-[0.06em] text-faint sm:flex">
        <span className="w-8 shrink-0" />
        <span className="w-24 shrink-0 md:w-32 lg:w-40">Project</span>
        <span className="min-w-0 flex-1">Agent</span>
        <span className="w-[124px] shrink-0">Status</span>
        <span className="hidden w-[150px] shrink-0 md:block">Activity</span>
        <span className="w-4 shrink-0" />
      </div>
      <Panel className="divide-y divide-line-faint overflow-hidden rounded-2xl" aria-label="All agents">
        {all.map((agent) => (
          <AgentFleetRow
            key={agent.ref}
            agent={agent}
            sample={usage.data?.agents.find((u) => u.ref === agent.ref)}
            cpuHistory={history.get(agent.ref) ?? []}
            onSelect={() => onSelect({ kind: 'agent', ref: agent.ref })}
            onOpen={onSelect}
          />
        ))}
      </Panel>
      {emptyProjects.length > 0 && (
        <p className="mt-3 px-1 text-[12.5px] leading-relaxed text-subtle">
          No agents yet in{' '}
          {emptyProjects.map((project, i) => (
            <span key={project.name}>
              {i > 0 && (i === emptyProjects.length - 1 ? ' and ' : ', ')}
              <button
                type="button"
                data-project={project.name}
                className="text-muted underline decoration-line-heavy underline-offset-2 transition hover:text-secondary"
                onClick={() => onSelect({ kind: 'project', project: project.name })}
              >
                {project.name}
              </button>
            </span>
          ))}
          .
        </p>
      )}
    </div>
  );
}

function AgentFleetRow({
  agent,
  sample,
  cpuHistory,
  onSelect,
  onOpen,
}: {
  agent: T.Agent;
  sample?: T.AgentUsage;
  cpuHistory: number[];
  onSelect: () => void;
  onOpen: (view: View) => void;
}) {
  const status = chatLabel(agent);
  // A machine that's stopped or paused holds nothing live: draw it quieter so
  // it doesn't compete with the agents actually doing something.
  const quiet = agent.state === 'stopped' || agent.state === 'paused';

  return (
    <AgentContextMenu agent={agent} infoSide="bottom" onSelect={onOpen}>
    <button
      type="button"
      data-agent={agent.ref}
      onClick={onSelect}
      className={cn(
        'group relative flex w-full items-center gap-3 py-2 pl-4 pr-3 text-left transition-colors',
        status.tone === 'urgent' && 'bg-amber-400/[0.035] hover:bg-amber-400/[0.06]',
        status.tone === 'error' && 'bg-rose-400/[0.035] hover:bg-rose-400/[0.06]',
        status.tone !== 'urgent' && status.tone !== 'error' && 'hover:bg-surface',
        quiet && 'opacity-55 hover:opacity-90',
      )}
    >
      <span
        aria-hidden
        className={cn(
          'absolute inset-y-1.5 left-0 w-[3px] rounded-full',
          status.tone === 'urgent' && 'bg-amber-400',
          status.tone === 'error' && 'bg-rose-400',
          status.tone === 'live' && 'bg-sky-400',
          status.tone === 'muted' && 'bg-transparent',
        )}
      />
      <LiveAgentAvatar agent={agent} />
      <span className="hidden w-24 shrink-0 items-center gap-1.5 truncate text-[12px] text-subtle sm:flex md:w-32 lg:w-40" title={agent.project}>
        <FolderGit2 className="size-3.5 shrink-0 text-faint" />
        <span className="truncate">{agent.project}</span>
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate text-[13px] font-medium text-primary">{agent.title || agent.name}</span>
          {agent.title && <span className="hidden shrink-0 font-mono text-[11px] text-faint lg:inline">{agent.name}</span>}
        </span>
        <span className="flex items-center gap-1.5 truncate text-[11px] text-faint sm:hidden">
          <FolderGit2 className="size-3 shrink-0" />
          {agent.project}
        </span>
      </span>
      <span className="flex w-[124px] shrink-0 items-center gap-1.5 text-[11.5px]">
        {status.tone === 'urgent' && <span className="size-1.5 shrink-0 animate-pulse rounded-full bg-amber-400" />}
        {status.tone === 'error' && <span className="size-1.5 shrink-0 rounded-full bg-rose-400" />}
        <span
          className={cn(
            'truncate',
            status.tone === 'urgent' && 'font-medium text-amber-300',
            status.tone === 'error' && 'font-medium text-rose-300',
            status.tone === 'live' && 'chat-shine font-medium',
            status.tone === 'muted' && 'text-subtle',
          )}
        >
          {status.text}
        </span>
      </span>
      <span className="hidden w-[150px] shrink-0 items-center gap-2 md:flex">
        {sample && agent.state === 'running' ? (
          <>
            <Sparkline values={cpuHistory} className={status.tone === 'live' ? 'text-sky-300/80' : 'text-subtle'} />
            <span className="font-mono text-[10.5px] tabular-nums text-subtle">
              {sample.cpu.toFixed(0)}% · {humanBytes(sample.memory)}
            </span>
          </>
        ) : (
          <span className="text-[11px] text-ghost">—</span>
        )}
      </span>
      <ChevronRight className="hidden size-4 shrink-0 text-ghost transition group-hover:translate-x-0.5 group-hover:text-muted sm:block" />
    </button>
    </AgentContextMenu>
  );
}

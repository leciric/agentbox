import { useQuery } from '@tanstack/react-query';
import { ChevronRight, FolderGit2, Plus } from 'lucide-react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { Fragment } from 'react';
import { useT } from '../lib/i18n';
import { useProjectName } from '../lib/useProjectName';
import { projectLabel } from '../lib/projectName';
import { chatLabel, memoryHold, rank, type StatusTone } from '../lib/agentStatus';
import { useCpuHistory } from '../lib/useCpuHistory';
import { cn, humanBytes, humanRate, shortRate } from '../lib/utils';
import { useUiV2 } from '../lib/uiV2';
import { AgentContextMenu } from './AgentContextMenu';
import { ProjectIcon } from './ProjectIcon';
import { Sparkline } from './Sparkline';
import { LiveAgentAvatar } from './state';
import { Button } from './ui/button';
import { Panel } from './ui/card';
import { Skeleton, skeletonWidths } from './ui/skeleton';

// The tones in the order they should catch your eye: agents that need you
// first, then whatever's failed, then whatever's working, then the rest.
const toneOrder: StatusTone[] = ['urgent', 'error', 'warning', 'live', 'muted'];

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
// project, when set, keeps one project's agents (v2's Overview filter).
export function AllAgentsPanel({ project, onSelect, onNewAgent }: { project?: string; onSelect: (view: View) => void; onNewAgent: () => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const history = useCpuHistory(usage.data);

  const all = (agents.data ?? []).filter((a) => !project || a.project === project).toSorted(compareAgents);
  // A project can only be said to have no agents once they're known.
  const emptyProjects = agents.data ? (projects.data ?? []).filter((p) => (!project || p.name === project) && !all.some((a) => a.project === p.name)) : [];
  // A column that says the same thing on every row says nothing: Project
  // only when the rows are from more than one, Activity only when some
  // agent has any.
  const columns = { project: all.some((a) => a.project !== all[0].project), activity: all.some((a) => activityOf(a, usage.data)) };

  if (agents.data && all.length === 0) {
    return (
      <div className="panel grid justify-items-center gap-3 rounded-2xl px-4 py-10 text-center">
        <p className="text-ui text-muted">{t('project.all.empty')}</p>
        <Button size="sm" onClick={onNewAgent}>
          <Plus />
          {t('project.view.newAgent')}
        </Button>
      </div>
    );
  }

  return (
    <div>
      <div className="hidden items-center gap-3 pb-1.5 pl-4 pr-3 text-xs font-medium text-subtle sm:flex">
        <span className="w-10 shrink-0" />
        {columns.project && <span className="w-24 shrink-0 md:w-32 lg:w-40">{t('project.all.project')}</span>}
        <span className="min-w-0 flex-1">{t('project.all.agent')}</span>
        <span className="w-[124px] shrink-0">{t('project.all.status')}</span>
        {columns.activity && <span className="hidden w-[200px] shrink-0 md:block">{t('project.all.activity')}</span>}
        <span className="w-4 shrink-0" />
      </div>
      <Panel className="divide-y divide-line-faint overflow-hidden rounded-2xl" aria-label={t('project.all.label')} aria-busy={!agents.data}>
        {/* Until the first list arrives there is nothing to say about the
            agents, not even that there are none. */}
        {!agents.data && skeletonWidths.map((width) => <AgentFleetRowSkeleton key={width} width={width} />)}
        {all.map((agent) => (
          <AgentFleetRow
            key={agent.ref}
            agent={agent}
            sample={activityOf(agent, usage.data)}
            columns={columns}
            cpuHistory={history.get(agent.ref) ?? []}
            onSelect={() => onSelect({ kind: 'agent', ref: agent.ref })}
            onOpen={onSelect}
          />
        ))}
      </Panel>
      {emptyProjects.length > 0 && (
        <p className="mt-3 px-1 text-xs leading-relaxed text-subtle">
          {t.rich('project.all.emptyProjects', {
            list: (
              <>
                {emptyProjects.map((project, i) => (
                  <Fragment key={project.name}>
                    {i > 0 && (i === emptyProjects.length - 1 ? t('project.all.and') : ', ')}
                    <button
                      type="button"
                      data-project={project.name}
                      className="text-muted underline decoration-line-heavy underline-offset-2 transition hover:text-secondary"
                      onClick={() => onSelect({ kind: 'project', project: project.name })}
                    >
                      {projectLabel(project)}
                    </button>
                  </Fragment>
                ))}
              </>
            ),
          })}
        </p>
      )}
    </div>
  );
}

// activityOf is an agent's latest load sample, while its machine runs: what
// the Activity column draws.
function activityOf(agent: T.Agent, usage?: T.Usage): T.AgentUsage | undefined {
  return agent.state === 'running' ? usage?.agents?.find((u) => u.ref === agent.ref) : undefined;
}

// AgentFleetRowSkeleton holds an agent's row, the same height and columns,
// while the list loads.
function AgentFleetRowSkeleton({ width }: { width: string }) {
  return (
    <div className="flex items-center gap-3 py-2 pl-4 pr-3" data-agent-skeleton>
      <Skeleton className="size-10 shrink-0 rounded-xl" />
      <span className="hidden w-24 shrink-0 sm:block md:w-32 lg:w-40">
        <Skeleton className="h-3 w-3/4" />
      </span>
      <span className="min-w-0 flex-1">
        <Skeleton className={cn('h-3.5', width)} />
      </span>
      <span className="w-[124px] shrink-0">
        <Skeleton className="h-3 w-20" />
      </span>
      <span className="hidden w-[200px] shrink-0 md:block">
        <Skeleton className="h-3 w-32" />
      </span>
      <span className="hidden w-4 shrink-0 sm:block" />
    </div>
  );
}

function AgentFleetRow({
  agent,
  sample,
  columns,
  cpuHistory,
  onSelect,
  onOpen,
}: {
  agent: T.Agent;
  sample?: T.AgentUsage;
  columns: { project: boolean; activity: boolean };
  cpuHistory: number[];
  onSelect: () => void;
  onOpen: (view: View) => void;
}) {
  const t = useT();
  const status = chatLabel(agent);
  const projectName = useProjectName(agent.project);
  // v2 draws the project's icon where v1 draws a folder.
  const v2 = useUiV2();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: v2 });
  const project = v2 ? projects.data?.find((p) => p.name === agent.project) : undefined;
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
        'group flex w-full items-center gap-3 py-2 pl-4 pr-3 text-left transition-colors hover:bg-surface',
        quiet && 'opacity-55 hover:opacity-90',
      )}
    >
      {/* The row itself stays neutral, whatever the status: its colour is the
          status column's alone. */}
      <LiveAgentAvatar agent={agent} />
      {columns.project && (
        <span className="hidden w-24 shrink-0 items-center gap-1.5 truncate text-xs text-subtle sm:flex md:w-32 lg:w-40" title={projectName}>
          {project ? <ProjectIcon project={project} className="size-4 rounded text-caption" /> : <FolderGit2 className="size-3.5 shrink-0 text-faint" />}
          <span className="truncate">{projectName}</span>
        </span>
      )}
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate text-ui font-medium text-primary">{agent.title || agent.name}</span>
          {agent.title && <span className="hidden shrink-0 font-mono text-xs text-faint lg:inline">{agent.name}</span>}
        </span>
        {columns.project && (
          <span className="flex items-center gap-1.5 truncate text-xs text-faint sm:hidden">
            <FolderGit2 className="size-3 shrink-0" />
            {projectName}
          </span>
        )}
      </span>
      <span className="flex w-[124px] shrink-0 items-center gap-1.5 text-xs">
        {status.tone === 'urgent' && <span className="size-1.5 shrink-0 rounded-full bg-amber-400" />}
        {status.tone === 'error' && <span className="size-1.5 shrink-0 rounded-full bg-rose-400" />}
        <span
          title={memoryHold(agent)?.tip}
          className={cn(
            'truncate',
            status.tone === 'urgent' && 'font-medium text-amber-300',
            status.tone === 'error' && 'font-medium text-rose-300',
            status.tone === 'warning' && 'font-medium text-amber-300/80',
            status.tone === 'live' && 'chat-shine font-medium',
            status.tone === 'muted' && 'text-subtle',
          )}
        >
          {status.text}
        </span>
      </span>
      {columns.activity && (
      <span className="hidden w-[200px] shrink-0 items-center gap-2 md:flex">
        {sample ? (
          <>
            <Sparkline values={cpuHistory} className={status.tone === 'live' ? 'text-sky-300/80' : 'text-subtle'} />
            <span className="font-mono text-xs tabular-nums text-subtle" title={t('project.all.usage', {
                cpu: sample.cpu.toFixed(0),
                memory: humanBytes(sample.memory),
                read: humanRate(sample.diskRead),
                write: humanRate(sample.diskWrite),
              })}>
              {sample.cpu.toFixed(0)}% · {humanBytes(sample.memory)} · {shortRate(sample.diskRead + sample.diskWrite)}
            </span>
          </>
        ) : (
          <span className="text-xs text-ghost">—</span>
        )}
      </span>
      )}
      <ChevronRight className="hidden size-4 shrink-0 text-ghost transition group-hover:translate-x-0.5 group-hover:text-muted sm:block" />
    </button>
    </AgentContextMenu>
  );
}

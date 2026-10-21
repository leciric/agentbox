import { useQuery } from '@tanstack/react-query';
import { ChevronRight, GitBranch, PanelRight, Plus } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { countByAgent, livePages } from '../lib/pages';
import { useT } from '../lib/i18n';
import { useProjectName } from '../lib/useProjectName';
import { avatarMood, chatLabel, isAsking, rank, settled, statusTip, type Mood } from '../lib/agentStatus';
import { cn, timeAgo } from '../lib/utils';
import { AgentContextMenu } from './AgentContextMenu';
import { AgentInfoCard } from './AgentInfoCard';
import { ErrorBoundary } from './ErrorBoundary';
import { PageIcon } from './pages/PageThumb';
import { PagesPanel } from './pages/PagesPanel';
import { useProjectPages } from './pages/usePages';
import { AgentAvatar } from './state';
import { Skeleton, skeletonWidths } from './ui/skeleton';
import { PrChip } from './PrChip';
import { Tip } from './ui/tooltip';

// AgentRail is a project's agents, kept visible beside whatever you're looking
// at: the lead's chat, an agent's own view, or the project's page behind it.
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
//
// In a narrow window the rail is a drawer over the page (App), given onClose:
// it is always open there, and its fold button closes the drawer instead.
// App keeps whether the docked rail is folded (useRailFolded), since the
// sidebar docks beside it only when there's room for both.
export function AgentRail({ view, onSelect, onNewAgent, onClose, folded: foldedHere = false, onFold = () => {} }: { view: View; onSelect: (view: View) => void; onNewAgent: (project: string) => void; onClose?: () => void; folded?: boolean; onFold?: (folded: boolean) => void }) {
  const t = useT();
  const project = view.kind === 'agent' ? view.ref.split('/')[0] : view.kind === 'project' ? view.project : null;
  const projectName = useProjectName(project ?? '');
  const rail = useRailAgents(project);
  const folded = foldedHere && !onClose;
  // The Pages tab, beside Agents while Hatch is connected and the project has
  // a page Hatch still has; an agent's page icon opens it on that agent.
  const pagesQuery = useProjectPages(project);
  const [railTab, setRailTab] = useState<'agents' | 'pages'>('agents');
  const [pagesAgent, setPagesAgent] = useState('');

  if (!project) return null;
  const pages = livePages(pagesQuery.data);
  const pageCounts = countByAgent(pages);
  const tab = pages.length > 0 ? railTab : 'agents';
  const leadRef = `${project}/lead`;
  const leadPages = pageCounts.get(leadRef) ?? 0;
  const openPages = (ref: string) => {
    setPagesAgent(ref);
    setRailTab('pages');
  };
  const { agents, mine, moving, finished, prs, lastReport, asking, mood, lead } = rail;
  const finishedOpen = rail.showFinished || finished.some((a) => view.kind === 'agent' && view.ref === a.ref);
  const setShowFinished = rail.setShowFinished;
  const onLead = view.kind === 'project';
  const leadMood = avatarMood({ state: 'running', chat: lead.data?.chat });

  const icon = (agent: T.Agent) => (
    <Tip key={agent.ref} side="right" className="max-w-none p-3" label={
        <ErrorBoundary>
          <AgentInfoCard agent={agent} pr={prs.get(agent.ref)} />
        </ErrorBoundary>
      }>
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
      pr={prs.get(agent.ref)}
      pages={pageCounts.get(agent.ref) ?? 0}
      onPages={() => openPages(agent.ref)}
      at={lastReport.get(agent.ref)}
      asking={asking(agent)}
      mood={mood(agent)}
      onSelect={() => onSelect({ kind: 'agent', ref: agent.ref })}
      onOpen={onSelect}
    />
  );

  if (folded) {
    return (
      <aside className="flex w-[56px] shrink-0 flex-col items-center gap-1 border-l border-line bg-rail py-2 backdrop-blur-xl" data-agent-rail="folded" aria-label={t('agent.rail.agentsOf', { project })}>
        <Tip label={t('agent.rail.show')}>
          <button aria-label={t('agent.rail.show')} className="rounded-lg p-2 text-subtle transition hover:bg-surface-strong hover:text-primary" onClick={() => onFold(false)}>
            <PanelRight className="size-4" />
          </button>
        </Tip>
        <Tip label={t('agent.rail.projectChat')}>
          <button aria-label={t('agent.rail.projectChat')} className="rounded-xl p-0.5 transition hover:bg-surface-strong" onClick={() => onSelect({ kind: 'project', project })}>
            <AgentAvatar ai="claude" mood={leadMood} seed={`${project}/lead`} className={cn(onLead && 'ring-1 ring-brand-400')} />
          </button>
        </Tip>
        {!agents.data && skeletonWidths.slice(0, 3).map((width) => <Skeleton key={width} className="m-0.5 size-10 rounded-xl" />)}
        {moving.map(icon)}
        {finished.length > 0 && (
          <>
            <div className="my-1 w-7 border-t border-line-faint" />
            <Tip label={finishedOpen ? t('agent.rail.hideFinished') : t('agent.rail.showFinished', { count: finished.length })}>
              <button
                aria-label={finishedOpen ? t('agent.rail.hideFinished') : t('agent.rail.showFinished', { count: finished.length })}
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
    <aside className="flex w-[268px] shrink-0 flex-col border-l border-line bg-rail backdrop-blur-xl xl:w-[312px]" data-agent-rail="open" aria-label={t('agent.rail.agentsOf', { project })}>
      <div className="flex h-12 shrink-0 items-center gap-2 px-3">
        {pages.length > 0 ? (
          <div className="flex items-center gap-0.5" role="tablist" aria-label={t('agent.rail.agentsOf', { project })}>
            <RailTab active={tab === 'agents'} count={mine.length} onClick={() => setRailTab('agents')} data-rail-tab="agents">
              {t('agent.rail.agents')}
            </RailTab>
            <RailTab
              active={tab === 'pages'}
              count={pages.length}
              onClick={() => {
                setPagesAgent('');
                setRailTab('pages');
              }}
              data-rail-tab="pages"
            >
              {t('pages.rail.tab')}
            </RailTab>
          </div>
        ) : (
          <>
            <span className="text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle">{t('agent.rail.agents')}</span>
            {mine.length > 0 && <span className="rounded-full bg-surface-raised px-1.5 text-[10.5px] tabular-nums text-subtle">{mine.length}</span>}
          </>
        )}
        <Tip label={t('agent.rail.newAgentIn', { project: projectName })}>
          <button
            aria-label={t('agent.rail.newAgentIn', { project: projectName })}
            className="ml-auto rounded-md p-1.5 text-subtle transition hover:bg-surface-strong hover:text-primary"
            onClick={() => onNewAgent(project)}
          >
            <Plus className="size-3.5" />
          </button>
        </Tip>
        <Tip label={t('agent.rail.hide')}>
          <button
            aria-label={t('agent.rail.hide')}
            data-rail-fold
            className="rounded-md p-1.5 text-subtle transition hover:bg-surface-strong hover:text-primary"
            onClick={() => (onClose ? onClose() : onFold(true))}
          >
            <PanelRight className="size-3.5" />
          </button>
        </Tip>
      </div>

      {tab === 'pages' ? (
        <PagesPanel project={project} pages={pages} agents={mine} agent={pagesAgent} onAgent={setPagesAgent} />
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-2 pb-3">
          <div className="relative">
            <button
              onClick={() => onSelect({ kind: 'project', project })}
              className={cn('group relative flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors xl:py-2.5', onLead ? 'bg-surface-strong' : 'hover:bg-surface')}
            >
              {onLead && <span className="absolute inset-y-1.5 left-0 w-[3px] rounded-full bg-brand-400" />}
              <AgentAvatar ai="claude" mood={leadMood} seed={`${project}/lead`} />
              <span className="min-w-0 flex-1">
                <span className={cn('block truncate text-[13px] font-medium', onLead ? 'text-title' : 'text-secondary')}>{t('agent.rail.projectChat')}</span>
                <span className="block truncate text-[11px] text-subtle">{t('agent.rail.leadConversation')}</span>
              </span>
            </button>
            {leadPages > 0 && <PagesBadge count={leadPages} onClick={() => openPages(leadRef)} />}
          </div>

          {(moving.length > 0 || !agents.data) && <div className="my-1.5 border-t border-line-faint" />}

          {/* Until the first list arrives, rows where the agents will be: "No
              agents yet" would be a guess. */}
          {!agents.data && <RailSkeleton />}

          {moving.map(row)}

          {finished.length > 0 && (
            <FinishedSection count={finished.length} open={finishedOpen} onToggle={setShowFinished}>
              {finished.map(row)}
            </FinishedSection>
          )}

          {agents.data && mine.length === 0 && <RailEmpty project={projectName} onNewAgent={() => onNewAgent(project)} />}
        </div>
      )}
    </aside>
  );
}

// useRailAgents is what the rail reads of a project's agents, and v2's
// panel list (v2/AgentList.tsx) with it: its agents in the rail's order,
// those still at something and the finished ones, each one's pull request,
// when it last reported and what, and whether it asks you something; for v2,
// each one's fleet entry (its changes, last activity) and its question.
export function useRailAgents(project: string | null) {
  const enabled = project !== null;
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, enabled });
  // Pull requests come from the fleet, which already carries one per agent,
  // matched against the repository list the daemon keeps (D54). ['agents'] is
  // every project's agents and would have to read GitHub per project to say
  // the same thing. It polls slowly: what moves in between arrives as an
  // event.
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
  const [showFinished, setShowFinished] = useShowFinished();

  // The events arrive newest first, so the first one seen is each agent's last.
  // Freeing its Docker space on stop is the daemon's doing, not a report.
  const lastReport = new Map<string, string>();
  for (const ev of events.data ?? []) if (ev.kind !== 'docker_pruned' && !lastReport.has(ev.ref)) lastReport.set(ev.ref, ev.at);
  // And what the latest one with words said, for v2's list.
  const lastSummary = new Map<string, string>();
  for (const ev of events.data ?? []) if (ev.summary && !lastSummary.has(ev.ref)) lastSummary.set(ev.ref, ev.summary);
  const mine = (agents.data ?? []).filter((a) => a.project === project).toSorted((a, b) => rank(a) - rank(b));
  const asking = (agent: T.Agent) => isAsking(questions.data, agent.ref);
  // An agent with a question waiting on you stays on top, whatever its chat is doing.
  const moving = mine.filter((a) => !settled(a) || asking(a));
  const finished = mine.filter((a) => settled(a) && !asking(a));
  const prs = new Map((fleet.data?.agents ?? []).map((a) => [a.ref, a.pr]));
  const fleetOf = new Map((fleet.data?.agents ?? []).map((a) => [a.ref, a]));
  const mood = (agent: T.Agent) => avatarMood(agent, asking(agent));
  // The question it waits on you for, in its words.
  const question = (ref: string) => (questions.data ?? []).find((q) => q.ref === ref && isAsking([q], ref))?.question;
  return { agents, loaded: !!agents.data, lead, mine, moving, finished, prs, fleetOf, lastReport, lastSummary, asking, question, mood, showFinished, setShowFinished };
}

// RailSkeleton is rows where the agents will be until the first list
// arrives: "No agents yet" would be a guess.
export function RailSkeleton() {
  return (
    <div aria-busy data-rail-loading>
      {skeletonWidths.slice(0, 3).map((width) => (
        <div key={width} className="flex items-center gap-2.5 px-2.5 py-2 xl:py-2.5">
          <Skeleton className="size-10 shrink-0 rounded-xl" />
          <span className="grid min-w-0 flex-1 gap-1.5">
            <Skeleton className={cn('h-3.5', width)} />
            <Skeleton className="h-2.5 w-1/3" />
          </span>
        </div>
      ))}
    </div>
  );
}

// FinishedSection is the agents that are done, folded under their header.
export function FinishedSection({ count, open, onToggle, children }: { count: number; open: boolean; onToggle: (open: boolean) => void; children: React.ReactNode }) {
  const t = useT();
  return (
    <section className="mt-1.5 border-t border-line-faint pt-1.5" data-rail-finished={open ? 'open' : 'closed'}>
      <button
        aria-expanded={open}
        className="flex w-full items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-left text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle transition hover:bg-surface hover:text-primary"
        onClick={() => onToggle(!open)}
      >
        <ChevronRight className={cn('size-3.5 shrink-0 transition-transform', open && 'rotate-90')} />
        {t('agent.rail.finished')}
        <span className="rounded-full bg-surface-raised px-1.5 text-[10.5px] font-normal normal-case tracking-normal tabular-nums">{count}</span>
      </button>
      {open && children}
    </section>
  );
}

// RailEmpty is a project with no agents, and the button to start one.
export function RailEmpty({ project, onNewAgent }: { project: string; onNewAgent: () => void }) {
  const t = useT();
  return (
    <div className="mt-2 grid justify-items-center gap-2 px-2 py-8 text-center">
      <p className="text-[12.5px] leading-relaxed text-subtle">{t('agent.rail.empty', { project })}</p>
      <button className="flex items-center gap-1.5 rounded-lg border border-line-strong px-2.5 py-1.5 text-[12.5px] text-tertiary transition hover:bg-surface-raised" onClick={onNewAgent}>
        <Plus className="size-3.5" />
        {t('agent.rail.newAgent')}
      </button>
    </div>
  );
}

// RailTab is Agents or Pages at the top of the column.
function RailTab({ active, count, onClick, children, ...rest }: { active: boolean; count: number; onClick: () => void; children: React.ReactNode; 'data-rail-tab': string }) {
  return (
    <button
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        'flex items-center gap-1.5 rounded-md px-1.5 py-1 text-[11px] font-semibold uppercase tracking-[0.08em] transition-colors',
        active ? 'bg-surface-strong text-secondary' : 'text-subtle hover:bg-surface hover:text-primary',
      )}
      {...rest}
    >
      {children}
      {count > 0 && <span className="rounded-full bg-surface-raised px-1.5 text-[10.5px] font-normal normal-case tracking-normal tabular-nums text-subtle">{count}</span>}
    </button>
  );
}

function AgentRow({
  agent,
  active,
  pr,
  pages,
  onPages,
  at,
  asking,
  mood,
  onSelect,
  onOpen,
}: {
  agent: T.Agent;
  active: boolean;
  pr?: T.PullRequest;
  pages: number;
  onPages: () => void;
  at?: string;
  asking: boolean;
  mood: Mood;
  onSelect: () => void;
  onOpen: (view: View) => void;
}) {
  const t = useT();
  // A question or a credential request waiting on you is said in words too,
  // not only by the avatar: the agent is usually mid-turn, blocked on it, so
  // its chat alone would call it working. Opening the agent shows a credential
  // request's card; a question comes back in the lead's reply.
  const status: ReturnType<typeof chatLabel> = asking && agent.chat !== 'waiting' ? { text: t('agent.rail.asks'), tone: 'urgent' } : chatLabel(agent);
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
          <span className={cn('min-w-0 flex-1', pr && (pr.conflict ? 'pr-14' : 'pr-11'))}>
            {/* The title, or the agent's name (agent-125) when it has none:
                the name, its IP and what its machine uses are in the card its
                hover opens (AgentInfoCard). */}
            <span className={cn('block min-w-0 truncate text-[13px] font-medium', active ? 'text-title' : 'text-secondary')}>{agent.title || agent.name}</span>
            <span className="mt-0.5 flex items-center gap-1.5 text-[11px]">
              {status.tone === 'urgent' && <span className="size-1.5 shrink-0 animate-pulse rounded-full bg-amber-400" />}
              {status.tone === 'error' && <span className="size-1.5 shrink-0 rounded-full bg-rose-400" />}
              <span
                title={statusTip(agent)}
                className={cn(
                  'shrink-0',
                  status.tone === 'urgent' && 'font-medium text-amber-300',
                  status.tone === 'error' && 'font-medium text-rose-300',
                  status.tone === 'warning' && 'font-medium text-amber-300/80',
                  status.tone === 'live' && 'chat-shine font-medium',
                  status.tone === 'muted' && 'text-subtle',
                )}
              >
                {status.text}
              </span>
              {at && <span className="ml-auto shrink-0 tabular-nums text-faint">{timeAgo(at)}</span>}
            </span>
            {/* Its branch, or its pull request in the badge over the row. */}
            {!pr && (
              <span className={cn('mt-1 flex items-center gap-1 font-mono text-[10.5px] text-faint', pages > 0 && 'pr-9')} data-rail-branch>
                <GitBranch className="size-3 shrink-0" />
                <span className="min-w-0 truncate">{agent.branch}</span>
              </span>
            )}
          </span>
        </button>
        {pr && <PullRequestBadge pr={pr} />}
        {pages > 0 && <PagesBadge count={pages} onClick={onPages} />}
      </div>
    </AgentContextMenu>
  );
}

// Whether the rail is folded down to icons, kept in this browser: it is a
// choice about this window, and the rail is the same rail on every view.
const foldedKey = 'agentbox.rail.folded';

export function useRailFolded(): [boolean, (folded: boolean) => void] {
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

// PagesBadge is a tiny page on an agent's row when it published pages Hatch
// still has, with how many when there's more than one. Clicking it opens the
// Pages tab on that agent.
function PagesBadge({ count, onClick }: { count: number; onClick: () => void }) {
  const t = useT();
  return (
    <Tip label={t('pages.rail.agentPages', { count })}>
      <button
        data-rail-pages-badge={count}
        aria-label={t('pages.rail.agentPages', { count })}
        onClick={onClick}
        className="absolute bottom-2 right-2 flex items-center gap-0.5 rounded-md px-1 py-0.5 text-[10.5px] tabular-nums text-subtle transition hover:bg-surface-vivid hover:text-primary xl:bottom-2.5"
      >
        <PageIcon className="size-3.5" />
        {count > 1 && count}
      </button>
    </Tip>
  );
}

// PullRequestBadge is the agent's pull request at a glance (PrChip), in the
// row's corner. An agent without one shows nothing.
function PullRequestBadge({ pr }: { pr: T.PullRequest }) {
  return <PrChip pr={pr} dataAttr="data-rail-pr" className="absolute right-2 top-2" />;
}

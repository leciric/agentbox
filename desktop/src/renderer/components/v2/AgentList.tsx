// UI v2's right-hand panel opens on its project's agents, beside the lead's
// chat, as v1's rail had them, in one narrow column with the sidebar's quiet
// rows (AgentLine): what it is, what it's doing now, and what it runs on and
// has made. An agent
// using its desktop has it in its row, small and live, where the lane of
// desktops beside the chat used to show it. The
// repository, the lead's checkout, heads the list; the finished agents fold
// under the rest, and Past agents is at its foot.
//
// A row opens the agent's chat, with its Desktop in the panel; the panel's
// back arrow there comes back to the lead and this list. Folded (AgentStrip),
// the list is a strip of avatars.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Archive, ChevronRight, ChevronsLeft } from 'lucide-react';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { chatLabel, statusTip } from '../../lib/agentStatus';
import { chatKey, peekThread } from '../../lib/chat';
import { choiceName } from '../../lib/modelChoices';
import { humanTokens } from '../../lib/tokens';
import { useT } from '../../lib/i18n';
import { agentSt, type AgentSt } from '../../lib/leadFirst';
import { pastAgentsKey } from '../../lib/pastAgents';
import { projectLabel } from '../../lib/projectName';
import { leadOf, pickTarget } from '../../lib/rightPanel';
import { cn, timeAgo } from '../../lib/utils';
import { AgentContextMenu } from '../AgentContextMenu';
import { FinishedSection, RailEmpty, RailSkeleton, useRailAgents } from '../AgentRail';
import { PrChip } from '../PrChip';
import { LiveDesktop } from '../pip/LiveDesktop';
import { useInUse } from '../pip/inUse';
import { ProjectIcon } from '../ProjectIcon';
import { AgentAvatar, ToolMark } from '../state';
import { Tip } from '../ui/tooltip';
import { useOpenReview, useProjectReview } from './ReviewV2';
import { StMark } from './SidebarV2';

// useListAgents is the rail's reading of the project's agents, with each
// one's state as the sidebar marks it, and the ones using their desktops.
function useListAgents(project: string) {
  const rail = useRailAgents(project);
  const live = new Set(useInUse(project).map((a) => a.ref));
  const ready = new Set((useProjectReview(project, !!useOpenReview()).data?.items ?? []).map((i) => i.ref));
  const asking = new Set(rail.mine.filter(rail.asking).map((a) => a.ref));
  const st = (agent: T.Agent) => agentSt(agent, { review: ready, asking });
  // What each running machine uses, as the rail showed it, read every few
  // seconds while the list is on screen.
  const cpu = useQuery({ queryKey: ['cpuUsage'], queryFn: api.cpuUsage, refetchInterval: 5_000 });
  const memory = useQuery({ queryKey: ['memoryUsage'], queryFn: api.memoryUsage, refetchInterval: 5_000 });
  const usage = new Map<string, { cpu?: number; memory?: number }>();
  for (const a of cpu.data?.agents ?? []) usage.set(a.ref, { cpu: a.cpu });
  for (const a of memory.data?.agents ?? []) usage.set(a.ref, { ...usage.get(a.ref), memory: a.memory });
  return { rail, live, ready, st, usage };
}

export function AgentList({ project, chat, onSelect, onNewAgent }: { project: string; chat: string; onSelect: (view: View) => void; onNewAgent: (project: string) => void }) {
  const t = useT();
  const { rail, live, ready, st, usage } = useListAgents(project);
  const past = useQuery({ queryKey: [...pastAgentsKey, 'panel', project], queryFn: () => api.archivedAgents({ project, limit: 50 }), staleTime: 30_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  const name = projectLabel(project, projects.data);

  const row = (agent: T.Agent) => (
    <AgentLine
      key={agent.ref}
      agent={agent}
      st={st(agent)}
      active={chat === agent.ref}
      live={live.has(agent.ref)}
      ready={ready.has(agent.ref)}
      pr={rail.prs.get(agent.ref)}
      fleet={rail.fleetOf.get(agent.ref)}
      at={rail.lastReport.get(agent.ref)}
      report={rail.lastSummary.get(agent.ref)}
      question={rail.question(agent.ref)}
      usage={agent.state === 'running' ? usage.get(agent.ref) : undefined}
      onOpen={onSelect}
    />
  );

  return (
    <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-1.5 pb-3 pt-1.5" data-panel-list={project}>
      <button type="button" onClick={() => pickTarget(chat, leadOf(project))} className="group flex w-full min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-surface" data-list-repository>
        {p ? <ProjectIcon project={p} className="size-5 rounded-md text-[10px]" /> : <span className="size-5 shrink-0 rounded-md bg-surface-strong" />}
        <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium text-primary">{t('panel.repository')}</span>
        <ChevronRight className="size-3.5 shrink-0 text-faint opacity-0 transition group-hover:opacity-100" />
      </button>

      {(rail.moving.length > 0 || !rail.loaded) && <div className="mx-2 my-1 border-t border-line-faint" />}
      {!rail.loaded && <RailSkeleton />}
      <div className="flex flex-col gap-px">{rail.moving.map(row)}</div>
      {rail.finished.length > 0 && (
        <FinishedSection count={rail.finished.length} open={rail.showFinished || rail.finished.some((a) => a.ref === chat)} onToggle={rail.setShowFinished}>
          <div className="flex flex-col gap-px">{rail.finished.map(row)}</div>
        </FinishedSection>
      )}
      {rail.loaded && rail.mine.length === 0 && <RailEmpty project={name} onNewAgent={() => onNewAgent(project)} />}

      {(past.data?.length ?? 0) > 0 && (
        <button
          type="button"
          onClick={() => onSelect({ kind: 'pastAgents', project })}
          className="mt-1.5 flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-left text-[11.5px] text-subtle transition hover:bg-surface hover:text-primary"
          data-list-past
        >
          <Archive className="size-3.5 shrink-0" />
          {t('pastAgents.title')}
          <span className="tabular-nums text-faint">· {past.data!.length >= 50 ? '50+' : past.data!.length}</span>
        </button>
      )}
    </div>
  );
}

// shortAgo is timeAgo in a few characters, for a narrow row: "4m", "2h".
function shortAgo(t: ReturnType<typeof useT>, iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 45) return t('panel.list.agoNow');
  if (s < 3600) return t('panel.list.agoMinutes', { n: Math.max(1, Math.round(s / 60)) });
  if (s < 86400) return t('panel.list.agoHours', { n: Math.round(s / 3600) });
  return t('panel.list.agoDays', { n: Math.round(s / 86400) });
}

// memShort is memory in a few characters: "1.4G", "820M".
const memShort = (n: number) => (n >= 1024 ** 3 ? `${(n / 1024 ** 3).toFixed(1)}G` : `${Math.round(n / 1024 ** 2)}M`);

// doingNow is the row's second line: the question it waits on you for, its
// permission request, its tool call or what it's writing while its turn
// runs, else what it last reported. tone colours what needs you.
function doingNow(t: ReturnType<typeof useT>, agent: T.Agent, st: AgentSt, last: T.ChatItem | undefined, question: string | undefined, report: string | undefined): { text?: string; tone?: 'needs' | 'error' } {
  if (question) return { text: question, tone: 'needs' };
  if (agent.chat === 'waiting') return { text: t('v2.tag.approval'), tone: 'needs' };
  if (agent.chat === 'running' && last) {
    if (last.tool) return { text: last.tool.command ? t('panel.list.running', { command: last.tool.command }) : last.tool.title };
    if (last.kind === 'assistant' && last.text) return { text: last.text.split('\n').find((l) => l.trim())?.trim() };
  }
  if (st === 'error') return { text: chatLabel(agent).text, tone: 'error' };
  return { text: report };
}

// AgentLine is one agent in up to three lines, each only when it has
// something: its state mark, task, time since it last did anything and its
// machine's CPU and memory; what it's doing now (doingNow); and, small, its
// AI tool, its pull request (or that it's ready to review), its changes, how
// much of its context it used and its model, the first to go when the row is
// too narrow. Using its desktop, its mark gives
// way to the desktop, small and live. A click opens its chat (with its
// Desktop in the panel); a right-click, its menu (AgentContextMenu). Its
// chat's latest item and session are read as the phone's list reads them
// (peekThread), kept current by the chat's events.
function AgentLine({ agent, st, active, live, ready, pr, fleet, at, report, question, usage, onOpen }: { agent: T.Agent; st: AgentSt; active: boolean; live: boolean; ready: boolean; pr?: T.PullRequest; fleet?: T.FleetAgent; at?: string; report?: string; question?: string; usage?: { cpu?: number; memory?: number }; onOpen: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => peekThread(queryClient, agent.ref), staleTime: 30_000 });
  const session = thread.data?.session;
  const title = agent.title || agent.name;
  const now = doingNow(t, agent, st, thread.data?.items.at(-1), question, report);
  const load = usage && [usage.cpu !== undefined && `${Math.round(usage.cpu)}%`, usage.memory && memShort(usage.memory)].filter(Boolean).join(' ');
  const modelOption = session?.options.find((o) => o.category === 'model' && o.type === 'select');
  const modelChoice = modelOption?.choices.find((c) => c.value === modelOption.value);
  const model = modelOption && (modelChoice ? choiceName(modelChoice) : modelOption.value);
  const context = session?.contextSize ? t('panel.list.context', { percent: Math.round(((session.contextUsed ?? 0) / session.contextSize) * 100), size: humanTokens(session.contextSize).replace(/\.0(?=[KMB])/, '') }) : undefined;
  const changes = fleet?.changes && fleet.changes.files > 0 ? fleet.changes : undefined;
  const when = fleet?.lastActive ?? at;
  const open = () => onOpen({ kind: 'agent', ref: agent.ref });
  return (
    <AgentContextMenu agent={agent} pr={pr} active={active} onSelect={onOpen}>
      <button
        type="button"
        onClick={(e) => {
          if (e.button !== 0 || e.ctrlKey) return;
          open();
        }}
        title={statusTip(agent)}
        aria-current={active ? 'true' : undefined}
        className={cn('group flex w-full min-w-0 flex-col rounded-lg px-2 py-1.5 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400', active ? 'bg-surface-raised' : 'hover:bg-surface')}
        data-list-agent={agent.ref}
        data-list-st={st}
        data-list-live={live || undefined}
      >
        <span className="flex min-w-0 items-start gap-2">
          {live ? (
            <Tip label={t('panel.openChat', { name: title })} side="left">
              <span className="relative mt-0.5 block w-12 shrink-0 overflow-hidden rounded border border-line-strong transition group-hover:border-brand-500/60" data-list-desktop>
                <LiveDesktop agent={agent} small />
                <span className="absolute right-0.5 top-0.5 size-1.5 animate-pulse rounded-full bg-emerald-400" />
              </span>
            </Tip>
          ) : (
            <span className="relative mt-0.5 flex aspect-[16/10] w-12 shrink-0 items-center justify-center rounded border border-line bg-surface" data-list-tool>
              <ToolMark ai={agent.ai} className="size-5" />
              <span className="absolute right-0.5 top-0.5 flex size-2 items-center justify-center">
                <StMark st={st} />
              </span>
            </span>
          )}
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="flex min-w-0 items-center gap-1.5">
              <span title={title} className={cn('min-w-0 flex-1 truncate text-[12.5px] font-medium leading-[18px]', active ? 'text-title' : st === 'done' ? 'text-subtle' : 'text-primary', !agent.title && 'font-mono text-[11.5px]')}>{title}</span>
              {when && (
                <span className="shrink-0 text-[10.5px] tabular-nums leading-4 text-faint" title={timeAgo(when)}>
                  {shortAgo(t, when)}
                </span>
              )}
              {load && (
                <span className="shrink-0 font-mono text-[10px] tabular-nums leading-4 text-faint" title={t('panel.list.usage')} data-list-usage>
                  {load}
                </span>
              )}
            </span>
            {now.text && (
              <span className={cn('truncate text-[11.5px] leading-4', now.tone === 'needs' ? 'font-medium text-amber-300' : now.tone === 'error' ? 'font-medium text-rose-300' : 'text-secondary')} data-list-now>
                {now.text}
              </span>
            )}
          </span>
        </span>
        <span className="mt-0.5 flex min-w-0 items-center gap-1.5 overflow-hidden whitespace-nowrap text-[10.5px] leading-4 text-faint" data-list-meta>
          {live && <ToolMark ai={agent.ai} className="size-3 shrink-0 opacity-70" />}
          {pr && <PrChip pr={pr} dataAttr="data-list-pr" />}
          {ready && (
            <span className="shrink-0 rounded bg-surface-strong px-1 font-medium text-secondary" data-list-ready>
              {t('v2.st.review')}
            </span>
          )}
          {changes && (
            <span className="shrink-0 tabular-nums" data-list-changes>
              <span className="text-emerald-300/80">+{changes.insertions}</span> <span className="text-rose-300/80">−{changes.deletions}</span> {t('panel.list.files', { count: changes.files })}
            </span>
          )}
          {context && <span className="min-w-0 truncate tabular-nums" data-list-context>{context}</span>}
          {model && <span className="min-w-0 truncate">{model}</span>}
        </span>
      </button>
    </AgentContextMenu>
  );
}

// AgentStrip is the list folded: a strip of the agents' avatars, each with
// its state mark, the ones at something first, that keeps the chat wide. A
// click opens the agent's chat; the arrow on top unfolds the list.
export function AgentStrip({ project, chat, onSelect, onUnfold }: { project: string; chat: string; onSelect: (view: View) => void; onUnfold: () => void }) {
  const t = useT();
  const { rail, live, st } = useListAgents(project);
  return (
    <div className="flex min-h-0 w-full flex-col items-center gap-1 overflow-y-auto overflow-x-hidden py-1.5" data-panel-strip={project}>
      <Tip label={t('panel.list.unfold')} side="left">
        <button type="button" aria-label={t('panel.list.unfold')} onClick={onUnfold} className="mb-1 rounded-md p-1.5 text-subtle transition hover:bg-surface-strong hover:text-primary" data-panel-unfold>
          <ChevronsLeft className="size-4" />
        </button>
      </Tip>
      {[...rail.moving, ...rail.finished].map((agent) => (
        <Tip key={agent.ref} label={agent.title || agent.name} side="left">
          <button type="button" aria-label={agent.title || agent.name} onClick={() => onSelect({ kind: 'agent', ref: agent.ref })} className={cn('relative rounded-lg p-0.5 transition hover:bg-surface-strong', chat === agent.ref && 'bg-surface-raised')} data-strip-agent={agent.ref}>
            <AgentAvatar ai={agent.ai} mood={rail.mood(agent)} state={agent.state} seed={agent.ref} className="size-8 rounded-lg" />
            <span className="absolute -bottom-px -right-px flex size-2.5 items-center justify-center rounded-full bg-sunken">
              {live.has(agent.ref) ? <span className="size-1.5 animate-pulse rounded-full bg-emerald-400" /> : <StMark st={st(agent)} />}
            </span>
          </button>
        </Tip>
      ))}
    </div>
  );
}

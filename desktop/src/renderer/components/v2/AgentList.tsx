// UI v2's right-hand panel opens on its project's agents, beside the lead's
// chat, as v1's rail had them, in one narrow column of rows (AgentLine): its
// name, then why it's where it is, in its state's colour, then what it runs
// on and has made. Each row leads with a tiny monitor (AgentMonitor) acting
// out its state, its AI tool's mark on the screen; an agent using its desktop
// has it there, small and live. The rows are grouped by state, read where every count is
// (lib/useAgentCounts.ts): needs you, failed, ready to review, working, and
// idle, which folds; Past agents is at its foot. The repository, the lead's
// checkout, is the panel's other tab, Files (RightPanel's LeadSwitch).
//
// A row opens the agent's chat, with its Desktop in the panel; the panel's
// back arrow there comes back to the lead and this list. Folded (AgentStrip),
// the list is a strip of tiny monitors with each agent's initials and state.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import * as TooltipPrimitive from '@radix-ui/react-tooltip';
import { type ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
import { Archive, ChevronRight, ChevronsLeft, HardDrive, ShieldAlert } from 'lucide-react';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { chatLabel, statusTip } from '../../lib/agentStatus';
import { groupRows, tileLabels, type PanelGroup } from '../../lib/agentCounts';
import { chatKey, peekThread } from '../../lib/chat';
import { choiceName } from '../../lib/modelChoices';
import { heavy } from '../../lib/terminalTail';
import { humanTokens } from '../../lib/tokens';
import { useT, type MessageKey } from '../../lib/i18n';
import { awaitsApproval, type AgentSt } from '../../lib/leadFirst';
import { pastAgentsKey } from '../../lib/pastAgents';
import { projectLabel } from '../../lib/projectName';
import { useAgentCounts } from '../../lib/useAgentCounts';
import { useAgentSession } from '../chat/sessionColor';
import { cn, humanBytes, timeAgo } from '../../lib/utils';
import { AgentContextMenu } from '../AgentContextMenu';
import { RailEmpty, RailSkeleton, useRailAgents } from '../AgentRail';
import { PrChip } from '../PrChip';
import { LiveDesktop } from '../pip/LiveDesktop';
import { useInUse } from '../pip/inUse';
import { Tip } from '../ui/tooltip';
import { FrozenBadge } from '../FrozenBadge';
import { monitorScreen, typingPace } from '../../../shared/monitor';
import { AgentMonitor } from './AgentMonitor';

const groupLabel: Record<PanelGroup, MessageKey> = {
  needs: 'panel.group.needs',
  error: 'panel.group.error',
  review: 'panel.group.review',
  working: 'panel.group.working',
  idle: 'panel.group.idle',
};

// stTone is a state's colour on a row's second line and its tile: colour
// only for what needs you, as StMark has it.
export const stTone = (st: AgentSt) => (st === 'needs' ? 'font-medium text-amber-300' : st === 'error' ? 'font-medium text-rose-300' : st === 'done' ? 'text-subtle' : 'text-secondary');

// useListAgents is the rail's reading of the project's agents, with each
// one's state from the counts everyone reads, and the ones using their desktops.
function useListAgents(project: string) {
  const rail = useRailAgents(project);
  const counts = useAgentCounts();
  const live = new Set(useInUse(project).map((a) => a.ref));
  const st = (agent: T.Agent): AgentSt => counts.st(agent.ref) ?? 'done';
  // What each running machine uses, as the rail showed it, read every few
  // seconds while the list is on screen.
  const cpu = useQuery({ queryKey: ['cpuUsage'], queryFn: api.cpuUsage, refetchInterval: 5_000 });
  const memory = useQuery({ queryKey: ['memoryUsage'], queryFn: api.memoryUsage, refetchInterval: 5_000 });
  const usage = new Map<string, { cpu?: number; memory?: number }>();
  for (const a of cpu.data?.agents ?? []) usage.set(a.ref, { cpu: a.cpu });
  for (const a of memory.data?.agents ?? []) usage.set(a.ref, { ...usage.get(a.ref), memory: a.memory });
  // Every agent of the project, in the rail's order, grouped by state.
  const groups = groupRows(rail.mine.map((agent) => ({ agent, st: st(agent) })));
  return { rail, live, st, usage, groups, hostMemory: memory.data?.hostTotal };
}

// useSeenRows is which of the list's rows (or the strip's tiles) are on
// screen: watch is a row's ref callback, and seen the refs of the rows an
// IntersectionObserver last saw. A monitor off screen doesn't animate.
function useSeenRows() {
  const [seen, setSeen] = useState<ReadonlySet<string>>(() => new Set());
  const observer = useMemo(
    () =>
      typeof IntersectionObserver === 'undefined'
        ? undefined
        : new IntersectionObserver((entries) =>
            setSeen((prev) => {
              const next = new Set(prev);
              for (const e of entries) {
                const { listAgent, stripAgent } = (e.target as HTMLElement).dataset;
                const ref = listAgent ?? stripAgent;
                if (ref && e.isIntersecting) next.add(ref);
                else if (ref) next.delete(ref);
              }
              return next;
            }),
          ),
    [],
  );
  useEffect(() => () => observer?.disconnect(), [observer]);
  const watch = useCallback(
    (el: HTMLElement | null) => {
      if (!el || !observer) return;
      observer.observe(el);
      return () => observer.unobserve(el);
    },
    [observer],
  );
  return { seen, watch };
}

export function AgentList({ project, chat, onSelect, onNewAgent }: { project: string; chat: string; onSelect: (view: View) => void; onNewAgent: (project: string) => void }) {
  const t = useT();
  const { rail, live, usage, groups, hostMemory } = useListAgents(project);
  const { seen, watch } = useSeenRows();
  const past = useQuery({ queryKey: [...pastAgentsKey, 'panel', project], queryFn: () => api.archivedAgents({ project, limit: 50 }), staleTime: 30_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const name = projectLabel(project, projects.data);

  const row = ({ agent, st }: { agent: T.Agent; st: AgentSt }) => (
    <AgentLine
      key={agent.ref}
      agent={agent}
      st={st}
      active={chat === agent.ref}
      live={live.has(agent.ref)}
      pr={rail.prs.get(agent.ref)}
      fleet={rail.fleetOf.get(agent.ref)}
      at={rail.lastReport.get(agent.ref)}
      report={rail.lastSummary.get(agent.ref)}
      question={rail.question(agent.ref)}
      usage={agent.state === 'running' ? usage.get(agent.ref) : undefined}
      hostMemory={hostMemory}
      disk={rail.disk.get(agent.ref)}
      seen={seen.has(agent.ref)}
      watch={watch}
      onOpen={onSelect}
    />
  );

  return (
    <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-1.5 pb-3 pt-1" data-panel-list={project}>
      {!rail.loaded && <RailSkeleton />}
      {groups.map(({ group, rows }) => {
        // Idle folds, as Finished did, unless the chat beside it is one of its.
        const folds = group === 'idle';
        const open = !folds || rail.showFinished || rows.some((r) => r.agent.ref === chat);
        const disk = folds ? rows.reduce((n, r) => n + (rail.disk.get(r.agent.ref) ?? 0), 0) : 0;
        const head = (
          <>
            {folds && <ChevronRight className={cn('size-3.5 shrink-0 transition-transform', open && 'rotate-90')} />}
            <span className="min-w-0 truncate">{t(groupLabel[group])}</span>
            <span className="tabular-nums text-faint">{rows.length}</span>
            {disk > 0 && (
              <span className="ml-auto inline-flex shrink-0 items-center gap-1 tabular-nums text-faint" title={t('agent.rail.stoppedTip')} data-list-disk>
                <HardDrive className="size-3" />
                {humanBytes(disk)}
              </span>
            )}
          </>
        );
        return (
          <section key={group} className="pt-1.5" data-list-group={group}>
            {folds ? (
              <button type="button" aria-expanded={open} onClick={() => rail.setShowFinished(!open)} className="flex h-7 w-full min-w-0 items-center gap-1.5 rounded-md px-2 text-left text-[12px] text-muted hover:bg-surface hover:text-primary" data-list-group-toggle>
                {head}
              </button>
            ) : (
              <h3 className="flex h-7 min-w-0 items-center gap-1.5 px-2 text-[12px] font-normal text-muted">{head}</h3>
            )}
            {open && <div className="flex flex-col gap-px">{rows.map(row)}</div>}
          </section>
        );
      })}
      {rail.loaded && rail.mine.length === 0 && <RailEmpty project={name} onNewAgent={() => onNewAgent(project)} />}

      {(past.data?.length ?? 0) > 0 && (
        <button
          type="button"
          onClick={() => onSelect({ kind: 'work', project })}
          className="mt-1.5 flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-left text-[12px] text-subtle transition hover:bg-surface hover:text-primary"
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

// SlotPreview is the live desktop on a row's monitor shown large beside the
// list after a short hover. While the pointer is on the monitor, it is the
// only tip: the row's own (its title=) is on the rest of the row.
function SlotPreview({ children, preview }: { children: ReactNode; preview: ReactNode }) {
  return (
    <TooltipPrimitive.Root delayDuration={350}>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content side="left" align="start" sideOffset={14} collisionPadding={8} className="z-[60] w-[480px] max-w-[calc(100vw-16px)] animate-fade-in overflow-hidden rounded-lg border border-line-strong bg-terminal shadow-2xl" data-list-preview>
          {preview}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  );
}

// AgentLine is one agent: its monitor (AgentMonitor), then its lines. The
// monitor acts out its state (monitorScreen), typing faster the more tool
// calls its chat made lately (typingPace), and holds its desktop, small and
// live, while it uses it; hovered, that shows large (SlotPreview). Beside it: its name, its number and the time since it last
// did anything; why it's where it is (doingNow, else its state) in its
// state's colour; and, small, its pull request, its machine's CPU and memory
// (amber when heavy), its disk, its changes and its model, the last to go
// when the row is too narrow. The rest (its status, how much of its context
// it used) is in the row's tooltip. A click opens its chat (with its Desktop
// in the panel); a right-click, its menu (AgentContextMenu). Its chat's
// latest items and session are read as the phone's list reads them
// (peekThread), kept current by the chat's events.
function AgentLine({ agent, st, active, live, pr, fleet, at, report, question, usage, hostMemory, disk, seen, watch, onOpen }: { agent: T.Agent; st: AgentSt; active: boolean; live: boolean; pr?: T.PullRequest; fleet?: T.FleetAgent; at?: string; report?: string; question?: string; usage?: { cpu?: number; memory?: number }; hostMemory?: number; disk?: number; seen: boolean; watch: (el: HTMLElement | null) => void; onOpen: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => peekThread(queryClient, agent.ref), staleTime: 30_000 });
  const session = thread.data?.session;
  const title = agent.title || agent.name;
  const number = /^agent-(\d+)$/.exec(agent.name)?.[1];
  const now = doingNow(t, agent, st, thread.data?.items.at(-1), question, report);
  // With nothing more to say, the row says its state.
  const reason = now.text || (st === 'review' ? t('panel.group.review') : chatLabel(agent).text);
  const load = usage && (usage.cpu !== undefined || usage.memory !== undefined) ? usage : undefined;
  const loadTip = load && t('panel.list.usage', { cpu: (load.cpu ?? 0).toFixed(1), memory: load.memory ? memShort(load.memory) : '–' });
  const modelOption = session?.options.find((o) => o.category === 'model' && o.type === 'select');
  const modelChoice = modelOption?.choices.find((c) => c.value === modelOption.value);
  const model = modelOption && (modelChoice ? choiceName(modelChoice) : modelOption.value);
  const context = session?.contextSize ? t('panel.list.contextTip', { percent: Math.round(((session.contextUsed ?? 0) / session.contextSize) * 100), size: humanTokens(session.contextSize).replace(/\.0(?=[KMB])/, '') }) : undefined;
  const changes = fleet?.changes && fleet.changes.files > 0 ? fleet.changes : undefined;
  const when = fleet?.lastActive ?? at;
  const owner = useAgentSession(agent);
  const tip = [title, owner && t('sessions.owner', { topic: owner.session.topic }), statusTip(agent), pr && `#${pr.number} ${pr.title}`, live && t('panel.list.desktopLive'), loadTip, model, context, changes && `+${changes.insertions} −${changes.deletions} · ${t('panel.list.files', { count: changes.files })}`, disk !== undefined && `${t('agent.rail.machineDisk')}: ${humanBytes(disk)}`].filter(Boolean).join('\n');
  const screen = monitorScreen(st, { live, running: agent.state === 'running' });
  const pace = typingPace((thread.data?.items ?? []).filter((it) => it.tool).map((it) => it.createdAt));
  const monitor = (
    <span className="block shrink-0 pt-0.5" data-list-slot data-list-screen={screen}>
      <AgentMonitor ai={agent.ai} screen={screen} pace={pace} paused={!seen} desktop={<LiveDesktop agent={agent} small />} />
    </span>
  );
  return (
    <AgentContextMenu agent={agent} pr={pr} active={active} onSelect={onOpen}>
      <button
        ref={watch}
        type="button"
        onClick={(e) => {
          if (e.button !== 0 || e.ctrlKey) return;
          onOpen({ kind: 'agent', ref: agent.ref });
        }}
        aria-current={active ? 'true' : undefined}
        className={cn('group flex w-full min-w-0 items-start gap-2.5 rounded-lg px-2 py-1.5 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400', active ? 'bg-surface-raised' : 'hover:bg-surface', owner && 'session-tint')}
        data-session-color={owner?.color}
        data-list-agent={agent.ref}
        data-list-st={st}
        data-list-live={live || undefined}
      >
        {screen === 'desktop' ? <SlotPreview preview={<LiveDesktop agent={agent} />}>{monitor}</SlotPreview> : monitor}
        <span className="flex min-w-0 flex-1 flex-col self-stretch" title={tip}>
          <span className="flex min-w-0 items-center gap-1.5 leading-[18px]">
            <span className={cn('min-w-0 flex-1 truncate text-[13px] font-medium', active ? 'text-title' : st === 'done' ? 'text-subtle' : 'text-primary', !agent.title && 'font-mono text-[12px]')}>{title}</span>
            <FrozenBadge agent={agent} />
            {number && (
              <span className="shrink-0 text-[12px] tabular-nums text-faint" data-list-number>
                #{number}
              </span>
            )}
            {when && (
              <span className="shrink-0 text-[12px] tabular-nums text-faint" title={timeAgo(when)}>
                {shortAgo(t, when)}
              </span>
            )}
          </span>
          <span className={cn('flex min-w-0 items-center gap-1 text-[12px] leading-4', now.tone === 'needs' ? stTone('needs') : now.tone === 'error' ? stTone('error') : stTone(st))} data-list-now>
            {st === 'needs' && awaitsApproval(agent) && <ShieldAlert className="size-3 shrink-0" />}
            <span className="min-w-0 truncate">{reason}</span>
          </span>
          <span className="mt-1 flex min-w-0 items-center gap-1.5 overflow-x-clip whitespace-nowrap text-[11px] leading-4 text-faint empty:hidden" data-list-meta>
            {pr && <PrChip pr={pr} dataAttr="data-list-pr" />}
            {load && (
              <span className={cn('shrink-0 rounded font-mono tabular-nums', heavy(load, hostMemory) ? 'bg-amber-400/15 px-1 font-medium text-amber-300' : 'text-secondary')} data-list-usage data-list-heavy={heavy(load, hostMemory) || undefined}>
                {[load.cpu !== undefined && `${Math.round(load.cpu)}%`, load.memory && memShort(load.memory)].filter(Boolean).join(' · ')}
              </span>
            )}
            {disk !== undefined && (
              <span className="shrink-0 font-mono tabular-nums text-secondary" data-list-disk>
                {humanBytes(disk)}
              </span>
            )}
            {changes && (
              <span className="min-w-0 truncate tabular-nums" data-list-changes>
                <span className="text-emerald-300/80">+{changes.insertions}</span> <span className="text-rose-300/80">−{changes.deletions}</span>
              </span>
            )}
            {model && <span className="shrink-0">{model}</span>}
          </span>
        </span>
      </button>
    </AgentContextMenu>
  );
}

// stName is each state's name, for a tile's tooltip.
const stName: Record<AgentSt, MessageKey> = { needs: 'v2.st.needs', error: 'v2.st.error', review: 'v2.st.review', working: 'v2.st.working', done: 'v2.st.done' };

// AgentStrip is the list folded, as it is beside a narrow chat: a strip of
// tiny monitors (AgentMonitor tiny) in the list's order, each acting out the
// agent's state round its initials, or its number where two share them
// (lib/agentCounts.ts tileLabels), so agents on the same AI tool aren't a
// column of the same icon. A click opens the agent's chat; the arrow on top
// unfolds the list.
export function AgentStrip({ project, chat, onSelect, onUnfold }: { project: string; chat: string; onSelect: (view: View) => void; onUnfold: () => void }) {
  const t = useT();
  const { live, groups } = useListAgents(project);
  const { seen, watch } = useSeenRows();
  const rows = groups.flatMap((g) => g.rows);
  const labels = tileLabels(rows.map((r) => r.agent));
  return (
    <div className="flex min-h-0 w-full flex-col items-center gap-1 overflow-y-auto overflow-x-hidden py-1.5" data-panel-strip={project}>
      <Tip label={t('panel.list.unfold')} side="left">
        <button type="button" aria-label={t('panel.list.unfold')} onClick={onUnfold} className="mb-1 rounded-md p-1.5 text-subtle transition hover:bg-surface-strong hover:text-primary" data-panel-unfold>
          <ChevronsLeft className="size-4" />
        </button>
      </Tip>
      {rows.map(({ agent, st }) => {
        const label = `${agent.title || agent.name} · ${t(stName[st])}`;
        const screen = monitorScreen(st, { live: live.has(agent.ref), running: agent.state === 'running' });
        return (
          <Tip key={agent.ref} label={label} side="left">
            <button
              ref={watch}
              type="button"
              aria-label={label}
              onClick={() => onSelect({ kind: 'agent', ref: agent.ref })}
              className={cn('group relative rounded-lg px-0.5 pb-0.5 pt-1.5 transition hover:bg-surface-strong', chat === agent.ref && 'bg-surface-raised ring-2 ring-brand-400/60')}
              data-strip-agent={agent.ref}
              data-strip-st={st}
            >
              <AgentMonitor ai={agent.ai} screen={screen} tiny label={labels.get(agent.ref)} paused={!seen.has(agent.ref)} />
            </button>
          </Tip>
        );
      })}
    </div>
  );
}

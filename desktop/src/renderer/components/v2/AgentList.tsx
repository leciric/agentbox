// UI v2's right-hand panel opens on its project's agents, beside the lead's
// chat, as v1's rail had them, in one narrow column with the sidebar's quiet
// rows (AgentLine): what it is, what it's doing now, and what it runs on and
// has made. Each row leads with one slot of a fixed size: an agent using its
// desktop has it there, small and live, where the lane of desktops beside the
// chat used to show it, and any other agent the end of its terminal. The finished agents fold under
// the rest, and Past agents is at its foot; the repository, the lead's
// checkout, is the panel's other tab, Files (RightPanel's LeadSwitch).
//
// A row opens the agent's chat, with its Desktop in the panel; the panel's
// back arrow there comes back to the lead and this list. Folded (AgentStrip),
// the list is a strip of avatars.
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import * as TooltipPrimitive from '@radix-ui/react-tooltip';
import { type ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
import { Archive, ChevronsLeft } from 'lucide-react';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { chatLabel, statusTip, type Mood } from '../../lib/agentStatus';
import { chatKey, peekThread } from '../../lib/chat';
import { choiceName } from '../../lib/modelChoices';
import { heavy, terminalLines } from '../../lib/terminalTail';
import { humanTokens } from '../../lib/tokens';
import { useT } from '../../lib/i18n';
import { agentSt, type AgentSt } from '../../lib/leadFirst';
import { pastAgentsKey } from '../../lib/pastAgents';
import { projectLabel } from '../../lib/projectName';
import { cn, humanBytes, timeAgo } from '../../lib/utils';
import { AgentContextMenu } from '../AgentContextMenu';
import { FinishedSection, RailEmpty, RailSkeleton, StoppedSection, useRailAgents } from '../AgentRail';
import { PrChip } from '../PrChip';
import { LiveDesktop } from '../pip/LiveDesktop';
import { useInUse } from '../pip/inUse';
import { AgentAvatar } from '../state';
import { Tip } from '../ui/tooltip';
import { FrozenBadge } from '../FrozenBadge';
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
  return { rail, live, ready, st, usage, hostMemory: memory.data?.hostTotal };
}

// useSeenRows is which of the list's rows are on screen: watch is a row's ref
// callback, and seen the refs of the rows an IntersectionObserver last saw.
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
                const ref = (e.target as HTMLElement).dataset.listAgent;
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

// useTerminalTails is the end of the terminals of the running agents whose
// rows are on screen and show their terminal, read every two seconds (the
// daemon keeps each for a moment, so other windows asking share the read).
function useTerminalTails(project: string, refs: string[]) {
  const names = refs.map((ref) => ref.slice(ref.indexOf('/') + 1)).sort();
  const tails = useQuery({
    queryKey: ['terminalTails', project, names],
    queryFn: () => api.terminalTails(project, names),
    enabled: names.length > 0,
    refetchInterval: 2_000,
    placeholderData: keepPreviousData,
  });
  return new Map((tails.data?.agents ?? []).map((t) => [t.ref, t]));
}

export function AgentList({ project, chat, onSelect, onNewAgent }: { project: string; chat: string; onSelect: (view: View) => void; onNewAgent: (project: string) => void }) {
  const t = useT();
  const { rail, live, ready, st, usage, hostMemory } = useListAgents(project);
  const { seen, watch } = useSeenRows();
  const tails = useTerminalTails(
    project,
    rail.mine.filter((a) => a.state === 'running' && seen.has(a.ref) && !live.has(a.ref)).map((a) => a.ref),
  );
  const past = useQuery({ queryKey: [...pastAgentsKey, 'panel', project], queryFn: () => api.archivedAgents({ project, limit: 50 }), staleTime: 30_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const name = projectLabel(project, projects.data);

  const row = (agent: T.Agent) => (
    <AgentLine
      key={agent.ref}
      agent={agent}
      st={st(agent)}
      mood={rail.mood(agent)}
      active={chat === agent.ref}
      live={live.has(agent.ref)}
      ready={ready.has(agent.ref)}
      pr={rail.prs.get(agent.ref)}
      fleet={rail.fleetOf.get(agent.ref)}
      at={rail.lastReport.get(agent.ref)}
      report={rail.lastSummary.get(agent.ref)}
      question={rail.question(agent.ref)}
      usage={agent.state === 'running' ? usage.get(agent.ref) : undefined}
      hostMemory={hostMemory}
      disk={rail.disk.get(agent.ref)}
      tail={tails.get(agent.ref)}
      watch={watch}
      onOpen={onSelect}
    />
  );

  return (
    <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-1.5 pb-3 pt-1.5" data-panel-list={project}>
      {!rail.loaded && <RailSkeleton />}
      <div className="flex flex-col gap-px">{rail.moving.map(row)}</div>
      {rail.stopped.length > 0 && (
        <StoppedSection agents={rail.stopped} disk={rail.disk}>
          <div className="flex flex-col gap-px">{rail.stopped.map(row)}</div>
        </StoppedSection>
      )}
      {rail.done.length > 0 && (
        <FinishedSection count={rail.done.length} open={rail.showFinished || rail.done.some((a) => a.ref === chat)} onToggle={rail.setShowFinished}>
          <div className="flex flex-col gap-px">{rail.done.map(row)}</div>
        </FinishedSection>
      )}
      {rail.loaded && rail.mine.length === 0 && <RailEmpty project={name} onNewAgent={() => onNewAgent(project)} />}

      {(past.data?.length ?? 0) > 0 && (
        <button
          type="button"
          onClick={() => onSelect({ kind: 'work', project })}
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

// Terminal is the end of an agent's terminal on a dark ground, the newest
// line at the bottom, with a cursor that blinks while the agent works: tiny
// in the row's slot, legible in its hover preview (big).
function Terminal({ lines, working, big }: { lines: string[]; working: boolean; big?: boolean }) {
  const cursor = <span className={cn('term-cursor inline-block shrink-0 translate-y-px bg-slate-300', big ? 'ml-0.5 h-[11px] w-[6px]' : 'ml-px h-[7px] w-[3.5px]')} />;
  return (
    <span aria-hidden className={cn('flex size-full flex-col justify-end overflow-hidden bg-[#0b0d12] font-mono text-slate-300', big ? 'px-2.5 py-2 text-[11px] leading-[14px]' : 'row-swap-in px-1 py-1 text-[7px] leading-[8px]')} data-list-terminal={big ? 'preview' : 'thumb'}>
      {lines.map((line, i) => (
        <span key={i} className={cn('flex', line.startsWith('$ ') && 'text-emerald-300/90')}>
          <span className="min-w-0 overflow-hidden whitespace-pre">{line}</span>
          {i === lines.length - 1 && working && cursor}
        </span>
      ))}
      {lines.length === 0 && (
        <span className="block text-emerald-300/90">
          ${working && cursor}
        </span>
      )}
    </span>
  );
}

// previewLines is how many lines of its terminal the hover preview shows.
const previewLines = 20;

// TerminalPreview is the hover preview's terminal: the last previewLines
// lines, read every two seconds while it shows (the daemon serves them from
// the same read as the thumbnail's).
function TerminalPreview({ agent, items, fallback, working }: { agent: T.Agent; items?: T.ChatItem[]; fallback?: string; working: boolean }) {
  const tail = useQuery({
    queryKey: ['terminalTails', agent.project, [agent.name], previewLines],
    queryFn: () => api.terminalTails(agent.project, [agent.name], previewLines),
    enabled: agent.state === 'running',
    refetchInterval: 2_000,
  });
  return (
    <span className="block aspect-[16/10] w-full">
      <Terminal lines={terminalLines(tail.data?.agents[0], items, fallback, previewLines)} working={working} big />
    </span>
  );
}

// SlotPreview is the row's slot shown large beside the list after a short
// hover: the live desktop, or the terminal with more lines. While the pointer
// is on the slot, it is the only tip: the row's own (its title=) is on the
// rest of the row.
function SlotPreview({ children, preview }: { children: ReactNode; preview: ReactNode }) {
  return (
    <TooltipPrimitive.Root delayDuration={350}>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content side="left" align="start" sideOffset={14} collisionPadding={8} className="z-[60] w-[480px] max-w-[calc(100vw-16px)] animate-fade-in overflow-hidden rounded-lg border border-line-strong bg-[#0b0d12] shadow-2xl" data-list-preview>
          {preview}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  );
}

// AgentLine is one agent: a slot of a fixed size, then its lines. The slot
// holds its desktop, small and live, while it uses it, and the end of its
// terminal otherwise (terminalLines), with its avatar in a corner (its mood:
// working, asleep, asking); hovered, it shows large (SlotPreview). Beside it,
// each only when it has something: its task and the time since it last did
// anything; what it's doing now (doingNow); and, small, its pull request (or
// that it's ready to review), its machine's CPU and memory (amber when
// heavy), its changes and its model, the last to go when the row is too
// narrow. How much of its context it used is in the row's tooltip. A click opens its chat (with its Desktop in the panel); a
// right-click, its menu (AgentContextMenu). Its chat's latest items and
// session are read as the phone's list reads them (peekThread), kept current
// by the chat's events.
function AgentLine({ agent, st, mood, active, live, ready, pr, fleet, at, report, question, usage, hostMemory, disk, tail, watch, onOpen }: { agent: T.Agent; st: AgentSt; mood: Mood; active: boolean; live: boolean; ready: boolean; pr?: T.PullRequest; fleet?: T.FleetAgent; at?: string; report?: string; question?: string; usage?: { cpu?: number; memory?: number }; hostMemory?: number; disk?: number; tail?: T.TerminalTail; watch: (el: HTMLElement | null) => void; onOpen: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => peekThread(queryClient, agent.ref), staleTime: 30_000 });
  const session = thread.data?.session;
  const title = agent.title || agent.name;
  const number = /^agent-(\d+)$/.exec(agent.name)?.[1];
  const now = doingNow(t, agent, st, thread.data?.items.at(-1), question, report);
  const load = usage && (usage.cpu !== undefined || usage.memory !== undefined) ? usage : undefined;
  const loadTip = load && t('panel.list.usage', { cpu: (load.cpu ?? 0).toFixed(1), memory: load.memory ? `${(load.memory / 1024 ** 2).toFixed(0)} MiB` : '–' });
  const modelOption = session?.options.find((o) => o.category === 'model' && o.type === 'select');
  const modelChoice = modelOption?.choices.find((c) => c.value === modelOption.value);
  const model = modelOption && (modelChoice ? choiceName(modelChoice) : modelOption.value);
  const context = session?.contextSize ? t('panel.list.contextTip', { percent: Math.round(((session.contextUsed ?? 0) / session.contextSize) * 100), size: humanTokens(session.contextSize).replace(/\.0(?=[KMB])/, '') }) : undefined;
  const changes = fleet?.changes && fleet.changes.files > 0 ? fleet.changes : undefined;
  const when = fleet?.lastActive ?? at;
  const open = () => onOpen({ kind: 'agent', ref: agent.ref });
  const lines = terminalLines(tail, thread.data?.items, now.text);
  const slot = (
    <span className="relative mt-0.5 block aspect-[16/10] w-24 shrink-0 overflow-hidden rounded border border-line-strong bg-[#0b0d12] transition group-hover:border-brand-500/60" data-list-slot>
      {live ? (
        <span className="row-swap-in block size-full" data-list-desktop>
          <LiveDesktop agent={agent} small />
        </span>
      ) : (
        <Terminal lines={lines} working={st === 'working'} />
      )}
      <AgentAvatar ai={agent.ai} mood={mood} seed={agent.ref} className="absolute right-0.5 top-0.5 size-5 rounded-md border-white/15 bg-black/70" />
      <span className="absolute left-1 top-1 flex size-2 items-center justify-center">
        <StMark st={st} />
      </span>
    </span>
  );
  return (
    <AgentContextMenu agent={agent} pr={pr} active={active} onSelect={onOpen}>
      <button
        ref={watch}
        type="button"
        onClick={(e) => {
          if (e.button !== 0 || e.ctrlKey) return;
          open();
        }}
        aria-current={active ? 'true' : undefined}
        className={cn('group flex w-full min-w-0 items-start gap-2 rounded-lg px-2 py-1.5 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400', active ? 'bg-surface-raised' : 'hover:bg-surface')}
        data-list-agent={agent.ref}
        data-list-st={st}
        data-list-live={live || undefined}
      >
        <SlotPreview preview={live ? <LiveDesktop agent={agent} /> : <TerminalPreview agent={agent} items={thread.data?.items} fallback={now.text} working={st === 'working'} />}>
          {slot}
        </SlotPreview>
        <span className="flex min-w-0 flex-1 flex-col self-stretch" title={[statusTip(agent), loadTip, context].filter(Boolean).join('\n')}>
          <span className="flex min-w-0 items-center gap-1.5">
            <span title={title} className={cn('min-w-0 flex-1 truncate text-[12.5px] font-medium leading-[18px]', active ? 'text-title' : st === 'done' ? 'text-subtle' : 'text-primary', !agent.title && 'font-mono text-[11.5px]')}>{title}</span>
            {number && (
              <span className="shrink-0 text-[10.5px] tabular-nums leading-4 text-faint/70" data-list-number>
                #{number}
              </span>
            )}
            {when && (
              <span className="shrink-0 text-[10.5px] tabular-nums leading-4 text-faint" title={timeAgo(when)}>
                {shortAgo(t, when)}
              </span>
            )}
          </span>
          {now.text && (
            <span className={cn('truncate text-[11.5px] leading-4', now.tone === 'needs' ? 'font-medium text-amber-300' : now.tone === 'error' ? 'font-medium text-rose-300' : 'text-secondary')} data-list-now>
              {now.text}
            </span>
          )}
          <span className="mt-0.5 flex min-w-0 items-center gap-1.5 overflow-x-clip whitespace-nowrap text-[10.5px] leading-4 text-faint empty:hidden" data-list-meta>
            <FrozenBadge agent={agent} />
            {pr && <PrChip pr={pr} dataAttr="data-list-pr" />}
            {ready && (
              <span className="shrink-0 rounded bg-surface-strong px-1 font-medium text-secondary" data-list-ready>
                {t('v2.st.review')}
              </span>
            )}
            {load && (
              <span className={cn('shrink-0 rounded font-mono text-[10px] tabular-nums', heavy(load, hostMemory) ? 'bg-amber-400/15 px-1 font-medium text-amber-300' : 'text-secondary')} data-list-usage data-list-heavy={heavy(load, hostMemory) || undefined}>
                {[load.cpu !== undefined && `${Math.round(load.cpu)}%`, load.memory && memShort(load.memory)].filter(Boolean).join(' · ')}
              </span>
            )}
            {disk !== undefined && (
              <span className="shrink-0 font-mono text-[10px] tabular-nums text-secondary" title={t('agent.rail.machineDisk')} data-list-disk>
                {humanBytes(disk)}
              </span>
            )}
            {changes && (
              <span className="min-w-0 truncate tabular-nums" title={t('panel.list.files', { count: changes.files })} data-list-changes>
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

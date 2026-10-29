// UI v2's right-hand panel opens on its project's agents, beside the lead's
// chat, as v1's rail had them, in one narrow column of quiet two-line rows
// (AgentLine): its name, then why it's where it is, in its state's colour.
// The rows are grouped by that state, read where every count is
// (lib/useAgentCounts.ts): needs you, failed, ready to review, working, and
// idle, which folds; Past agents is at its foot. The repository, the lead's
// checkout, is the panel's other tab, Files (RightPanel's LeadSwitch). The
// rows used to lead with a thumbnail of each agent's terminal: at 7px it
// could not be read, it was black in light mode, and twenty of them made the
// list a wall of near-identical boxes.
//
// A row opens the agent's chat, with its Desktop in the panel; the panel's
// back arrow there comes back to the lead and this list. Folded (AgentStrip),
// the list is a strip of tiles with each agent's initials and state.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Archive, ChevronRight, ChevronsLeft, HardDrive, ShieldAlert } from 'lucide-react';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { chatLabel, statusTip, type Mood } from '../../lib/agentStatus';
import { groupRows, tileLabels, type PanelGroup } from '../../lib/agentCounts';
import { chatKey, peekThread } from '../../lib/chat';
import { choiceName } from '../../lib/modelChoices';
import { humanTokens } from '../../lib/tokens';
import { useT, type MessageKey } from '../../lib/i18n';
import { awaitsApproval, type AgentSt } from '../../lib/leadFirst';
import { pastAgentsKey } from '../../lib/pastAgents';
import { projectLabel } from '../../lib/projectName';
import { useAgentCounts } from '../../lib/useAgentCounts';
import { SessionChip } from '../chat/SessionChip';
import { cn, humanBytes, timeAgo } from '../../lib/utils';
import { AgentContextMenu } from '../AgentContextMenu';
import { RailEmpty, RailSkeleton, useRailAgents } from '../AgentRail';
import { useInUse } from '../pip/inUse';
import { AgentAvatar } from '../state';
import { Tip } from '../ui/tooltip';
import { FrozenBadge } from '../FrozenBadge';
import { StMark } from './SidebarV2';

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
  // What each running machine uses, for a row's tooltip, read every few
  // seconds while the list is on screen.
  const cpu = useQuery({ queryKey: ['cpuUsage'], queryFn: api.cpuUsage, refetchInterval: 5_000 });
  const memory = useQuery({ queryKey: ['memoryUsage'], queryFn: api.memoryUsage, refetchInterval: 5_000 });
  const usage = new Map<string, { cpu?: number; memory?: number }>();
  for (const a of cpu.data?.agents ?? []) usage.set(a.ref, { cpu: a.cpu });
  for (const a of memory.data?.agents ?? []) usage.set(a.ref, { ...usage.get(a.ref), memory: a.memory });
  // Every agent of the project, in the rail's order, grouped by state.
  const groups = groupRows(rail.mine.map((agent) => ({ agent, st: st(agent) })));
  return { rail, live, st, usage, groups };
}

export function AgentList({ project, chat, onSelect, onNewAgent }: { project: string; chat: string; onSelect: (view: View) => void; onNewAgent: (project: string) => void }) {
  const t = useT();
  const { rail, live, usage, groups } = useListAgents(project);
  const past = useQuery({ queryKey: [...pastAgentsKey, 'panel', project], queryFn: () => api.archivedAgents({ project, limit: 50 }), staleTime: 30_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const name = projectLabel(project, projects.data);

  const row = ({ agent, st }: { agent: T.Agent; st: AgentSt }) => (
    <AgentLine
      key={agent.ref}
      agent={agent}
      st={st}
      mood={rail.mood(agent)}
      active={chat === agent.ref}
      live={live.has(agent.ref)}
      pr={rail.prs.get(agent.ref)}
      fleet={rail.fleetOf.get(agent.ref)}
      at={rail.lastReport.get(agent.ref)}
      report={rail.lastSummary.get(agent.ref)}
      question={rail.question(agent.ref)}
      usage={agent.state === 'running' ? usage.get(agent.ref) : undefined}
      disk={rail.disk.get(agent.ref)}
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

// AgentLine is one agent in two lines, 44px tall: its name, its pull
// request's number and the time since it last did anything; then why it's
// where it is (doingNow, else its state) in its state's colour. Beside them
// its avatar (its AI tool and mood), with its state's mark, green while it
// uses its desktop. The rest (its status, its machine's CPU and memory, its
// model, how much of its context it used, its changes, its disk) is in the
// row's tooltip. A click opens its chat (with its Desktop in the panel); a
// right-click, its menu (AgentContextMenu). Its chat's latest items and
// session are read as the phone's list reads them (peekThread), kept current
// by the chat's events.
function AgentLine({ agent, st, mood, active, live, pr, fleet, at, report, question, usage, disk, onOpen }: { agent: T.Agent; st: AgentSt; mood: Mood; active: boolean; live: boolean; pr?: T.PullRequest; fleet?: T.FleetAgent; at?: string; report?: string; question?: string; usage?: { cpu?: number; memory?: number }; disk?: number; onOpen: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => peekThread(queryClient, agent.ref), staleTime: 30_000 });
  const session = thread.data?.session;
  const title = agent.title || agent.name;
  const now = doingNow(t, agent, st, thread.data?.items.at(-1), question, report);
  // With nothing more to say, the row says its state.
  const reason = now.text || (st === 'review' ? t('panel.group.review') : chatLabel(agent).text);
  const load = usage && (usage.cpu !== undefined || usage.memory !== undefined) ? usage : undefined;
  const loadTip = load && t('panel.list.usage', { cpu: (load.cpu ?? 0).toFixed(1), memory: load.memory ? memShort(load.memory) : '–' });
  const modelOption = session?.options.find((o) => o.category === 'model' && o.type === 'select');
  const modelChoice = modelOption?.choices.find((c) => c.value === modelOption.value);
  const model = modelOption && (modelChoice ? choiceName(modelChoice) : modelOption.value);
  const context = session?.contextSize ? t('panel.list.contextTip', { percent: Math.round(((session.contextUsed ?? 0) / session.contextSize) * 100), size: humanTokens(session.contextSize).replace(/\.0(?=[KMB])/, '') }) : undefined;
  const changes = fleet?.changes && fleet.changes.files > 0 ? `+${fleet.changes.insertions} −${fleet.changes.deletions} · ${t('panel.list.files', { count: fleet.changes.files })}` : undefined;
  const when = fleet?.lastActive ?? at;
  const tip = [title, statusTip(agent), pr && `#${pr.number} ${pr.title}`, live && t('panel.list.desktopLive'), loadTip, model, context, changes, disk !== undefined && `${t('agent.rail.machineDisk')}: ${humanBytes(disk)}`].filter(Boolean).join('\n');
  return (
    <AgentContextMenu agent={agent} pr={pr} active={active} onSelect={onOpen}>
      <button
        type="button"
        onClick={(e) => {
          if (e.button !== 0 || e.ctrlKey) return;
          onOpen({ kind: 'agent', ref: agent.ref });
        }}
        aria-current={active ? 'true' : undefined}
        title={tip}
        className={cn('group flex h-11 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400', active ? 'bg-surface-raised' : 'hover:bg-surface')}
        data-list-agent={agent.ref}
        data-list-st={st}
        data-list-live={live || undefined}
      >
        <span className="relative shrink-0">
          <AgentAvatar ai={agent.ai} mood={mood} state={agent.state} seed={agent.ref} className={cn('size-7 rounded-lg', st === 'done' && 'opacity-60')} />
          <span className="absolute -bottom-0.5 -right-0.5 flex size-2.5 items-center justify-center rounded-full bg-sunken">
            {live ? <span className="size-1.5 rounded-full bg-emerald-400" data-list-desktop /> : <StMark st={st} />}
          </span>
        </span>
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="flex min-w-0 items-center gap-1.5 leading-[18px]">
            <span className={cn('min-w-0 flex-1 truncate text-[13px] font-medium', active ? 'text-title' : st === 'done' ? 'text-subtle' : 'text-primary', !agent.title && 'font-mono text-[12px]')}>{title}</span>
            <SessionChip agent={agent} />
            <FrozenBadge agent={agent} />
            {pr && (
              <span className={cn('shrink-0 text-[12px] tabular-nums', pr.state === 'merged' ? 'text-violet-300' : 'text-faint')} data-list-pr={pr.number}>
                #{pr.number}
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
        </span>
      </button>
    </AgentContextMenu>
  );
}

// stName is each state's name, for a tile's tooltip.
const stName: Record<AgentSt, MessageKey> = { needs: 'v2.st.needs', error: 'v2.st.error', review: 'v2.st.review', working: 'v2.st.working', done: 'v2.st.done' };

// AgentStrip is the list folded, as it is beside a narrow chat: a strip of
// tiles in the list's order, each with the agent's initials, or its number
// where two share them (lib/agentCounts.ts tileLabels), so agents on the same AI tool aren't a column of the same icon,
// edged in its state's colour where it needs you, with its state's mark. A
// click opens the agent's chat; the arrow on top unfolds the list.
export function AgentStrip({ project, chat, onSelect, onUnfold }: { project: string; chat: string; onSelect: (view: View) => void; onUnfold: () => void }) {
  const t = useT();
  const { live, groups } = useListAgents(project);
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
        return (
          <Tip key={agent.ref} label={label} side="left">
            <button type="button" aria-label={label} onClick={() => onSelect({ kind: 'agent', ref: agent.ref })} className={cn('relative rounded-lg p-0.5 transition hover:bg-surface-strong', chat === agent.ref && 'bg-surface-raised')} data-strip-agent={agent.ref} data-strip-st={st}>
              <span
                className={cn(
                  'flex size-8 items-center justify-center rounded-lg border bg-surface-strong text-[12px] font-semibold tabular-nums',
                  st === 'needs' ? 'border-amber-400/70 text-amber-300' : st === 'error' ? 'border-rose-400/70 text-rose-300' : st === 'done' ? 'border-line text-faint' : 'border-line-strong text-secondary',
                  chat === agent.ref && 'ring-2 ring-brand-400/60',
                )}
                data-strip-initials
              >
                {labels.get(agent.ref)}
              </span>
              <span className="absolute -bottom-px -right-px flex size-2.5 items-center justify-center rounded-full bg-sunken">
                {live.has(agent.ref) ? <span className="size-1.5 rounded-full bg-emerald-400" /> : <StMark st={st} />}
              </span>
            </button>
          </Tip>
        );
      })}
    </div>
  );
}

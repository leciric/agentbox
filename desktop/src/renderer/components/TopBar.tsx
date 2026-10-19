import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, ChevronRight, Menu as MenuIcon, Search as SearchIcon, TriangleAlert, Users } from 'lucide-react';
import type { View } from '../App';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import * as T from '../../shared/api';
import { limitTone, windowLeft, windowNow } from '../lib/tokens';
import { noLimits, pickMeter } from '../lib/usageMeter';
import { useNow } from '../lib/useNow';
import { useUiV2 } from '../lib/uiV2';
import { ProjectCrumb, TopEndSlot, TopSlot } from './v2/ProjectHeader';
import { cn, onMac, shortcut, timeAgo, timeUntil } from '../lib/utils';
import { ToolMark } from './state';
import { Tip } from './ui/tooltip';
import { DiskGuardPill } from './DiskGuardPill';
import { openSettingsSection } from './SettingsPage';
import { ImageBuildChip } from './ImageBuildChip';
import { MachineStatus } from './MachineStatus';
import { Inbox } from './Inbox';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

const backToLeadKeys = onMac ? '⌥←' : 'Alt+←';

export function TopBar({
  view,
  onSelect,
  onOpenNav,
  onOpenRail,
  onOpenNotice,
  onSearch,
}: {
  view: View;
  onSelect: (view: View) => void;
  // onOpenNav opens the sidebar's drawer, and onOpenRail the agent rail's,
  // in a window too narrow for them to sit beside the page (App).
  onOpenNav: () => void;
  onOpenRail?: () => void;
  // onOpenNotice goes where a notification in the inbox points.
  onOpenNotice?: (notice: T.Notification) => void;
  // onSearch opens the search palette; without it there's no search button.
  onSearch?: () => void;
}) {
  const t = useT();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });

  const v2 = useUiV2();
  // v2 names the project once, by its icon, in the top bar's breadcrumb.
  const crumbProject = v2 && (view.kind === 'project' || view.kind === 'agent') ? projects.data?.find((p) => p.name === (view.kind === 'project' ? view.project : view.ref.split('/')[0])) : undefined;
  const crumbThen = view.kind === 'project' ? T.LeadName : view.kind === 'agent' ? agents.data?.find((a) => a.ref === view.ref)?.title || view.ref.split('/')[1] : '';

  const crumbs: { label: string; mono?: boolean; view?: View }[] = [];
  switch (view.kind) {
    // v2's Home is one place, the Home chat and its Status tab: one name.
    case 'home':
      crumbs.push({ label: t('shell.nav.home') });
      break;
    case 'homeChat':
      crumbs.push({ label: t(v2 ? 'shell.nav.home' : 'shell.nav.mainChat') });
      break;
    case 'jobs':
      crumbs.push({ label: t('shell.nav.jobs') });
      break;
    case 'media':
      crumbs.push({ label: t('shell.nav.media') });
      break;
    case 'settings':
      crumbs.push({ label: t('common.settings') });
      break;
    case 'project':
      crumbs.push({ label: projectLabel(view.project, projects.data) });
      break;
    case 'group':
      crumbs.push({ label: t('group.crumb') });
      break;
    case 'pastAgents':
      crumbs.push({ label: t('pastAgents.title'), view: view.ref ? { kind: 'pastAgents' } : undefined });
      if (view.ref) crumbs.push({ label: view.ref.split('/')[1], mono: true });
      break;
    case 'agent': {
      const [project, name] = view.ref.split('/');
      const agent = agents.data?.find((a) => a.ref === view.ref);
      crumbs.push({ label: projectLabel(project, projects.data), view: { kind: 'project', project } }, { label: agent?.title || name, mono: !agent?.title });
    }
  }

  return (
    // In a narrow window the controls wrap onto a second row rather than
    // run off the edge: the breadcrumb takes what's left of the first, and
    // gives way to them only below 8rem.
    <header className="flex min-h-14 shrink-0 flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-line px-3 py-2 md:gap-x-4 md:px-5" data-top-bar>
      <button
        aria-label={t('shell.top.openMenu')}
        className="-ml-1 rounded-lg p-2 text-muted transition hover:bg-surface-raised hover:text-primary lg:hidden"
        onClick={onOpenNav}
        data-open-nav
      >
        <MenuIcon className="size-5" />
      </button>
      <nav className="flex min-w-32 grow basis-0 items-center gap-1.5 text-[13px]" aria-label={t('shell.top.breadcrumb')}>
        {/* In an agent's chat, the way back to its lead's: ←, Alt+← (App), or the project's name. */}
        {crumbProject && view.kind === 'agent' && (
          <Tip label={`${t('v2.header.backToLead')} (${backToLeadKeys})`}>
            <button
              type="button"
              aria-label={t('v2.header.backToLead')}
              onClick={() => onSelect({ kind: 'project', project: crumbProject.name })}
              className="-ml-1 shrink-0 rounded-md p-1 text-muted transition hover:bg-surface-raised hover:text-primary"
              data-back-to-lead
            >
              <ArrowLeft className="size-4" />
            </button>
          </Tip>
        )}
        {crumbProject ? (
          <ProjectCrumb project={crumbProject} then={crumbThen} onProject={view.kind === 'agent' ? () => onSelect({ kind: 'project', project: crumbProject.name }) : undefined} />
        ) : crumbs.map((crumb, i) => (
          <span key={i} className="flex min-w-0 items-center gap-1.5">
            {i > 0 && <ChevronRight className="size-3.5 shrink-0 text-faint" />}
            {crumb.view ? (
              <button className="-mx-1 truncate rounded-md px-1 text-muted transition hover:bg-surface-raised hover:text-primary" onClick={() => onSelect(crumb.view!)}>
                {crumb.label}
              </button>
            ) : (
              <span className={cn('truncate font-medium text-primary', crumb.mono && 'font-mono text-[12.5px]')}>{crumb.label}</span>
            )}
          </span>
        ))}
      </nav>

      {onOpenRail && (view.kind === 'project' || view.kind === 'agent') && <RailButton view={view} onClick={onOpenRail} />}

      <div className="ml-auto flex min-w-0 items-center gap-2">
        {v2 && <TopSlot />}
        {onSearch && <SearchButton onClick={onSearch} />}
        {setup.data && !setup.data.ready && view.kind !== 'settings' && (
          <button
            className="flex items-center gap-1.5 rounded-full bg-amber-400/10 px-2.5 py-1 text-xs font-medium text-amber-200 ring-1 ring-inset ring-amber-400/25 transition hover:bg-amber-400/15"
            // Setup's next step, not whichever section Settings was left at.
            onClick={() => openSettingsSection('setup')}
          >
            <TriangleAlert className="size-3.5" />
            <span className="hidden sm:inline">{t('shell.home.finishSetup')}</span>
          </button>
        )}
        <ImageBuildChip onSelect={onSelect} />
        <DiskGuardPill onSelect={onSelect} />
        <ClaudeUsage view={view} agents={agents.data ?? []} />
        <MachineStatus agents={agents.data ?? []} onSelect={onSelect} />
        {/* v2 has no inbox: what needs you is where it's answered, the lead
            chat's cards and the sidebar's rows, and review is the sidebar's. */}
        {!v2 && <Inbox onOpen={onOpenNotice ?? (() => {})} onSelect={onSelect} />}
        {/* In its place, the page's ⋯ and Panel, at the far right. */}
        {v2 && <TopEndSlot />}
      </div>
    </header>
  );
}

// ClaudeUsage is how much of a Claude account's limits is used (D85): its
// five-hour window and its week, the limits every agent on the account
// shares, in Claude's own colour so it isn't taken for one of the machine's
// meters. The account is the one what's open spends — the agent's, the
// project's, or on Home the machine's default (pickMeter) — and the popover
// names it above every account's readings. A reading is what the last chat
// on that account was told, so the popover says when that was, and a window
// that has reset since shows no number rather than one that describes a
// window that is over. Its countdowns tick every minute: the query's refetch
// leaves them alone while the reading is unchanged.
// The search palette's way in for the pointer, with its shortcut on it.
function SearchButton({ onClick }: { onClick: () => void }) {
  const t = useT();
  return (
    <button
      type="button"
      className="flex items-center gap-2 rounded-full py-1 pl-2.5 pr-2.5 text-muted ring-1 ring-inset ring-line transition hover:bg-surface-raised hover:text-primary md:pr-1.5"
      aria-label={t('search.open')}
      title={t('search.open')}
      data-search-button
      onClick={onClick}
    >
      <SearchIcon className="size-3.5" />
      <span className="hidden text-[12px] md:inline">{t('common.search')}</span>
      <kbd className="hidden whitespace-nowrap rounded-md bg-surface-raised px-1.5 font-mono text-[10.5px] text-subtle md:inline">{shortcut('K')}</kbd>
    </button>
  );
}

// RailButton opens the agent rail's drawer, below xl where the rail doesn't
// sit beside the page: the project's agents, with how many there are.
function RailButton({ view, onClick }: { view: View; onClick: () => void }) {
  const t = useT();
  const project = view.kind === 'agent' ? view.ref.split('/')[0] : view.kind === 'project' ? view.project : '';
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const mine = agents.data?.filter((agent) => agent.project === project);
  return (
    <button
      type="button"
      className="flex shrink-0 items-center gap-1.5 rounded-full border border-line bg-surface-faint px-2.5 py-1 text-xs text-muted transition hover:bg-surface-raised hover:text-primary xl:hidden"
      aria-label={t('agent.rail.show')}
      title={t('agent.rail.show')}
      data-open-rail
      onClick={onClick}
    >
      <Users className="size-3.5" />
      {/* No count before the agents are in: 0 would say there are none. */}
      {mine && <span className="tabular-nums">{mine.length}</span>}
    </button>
  );
}

function ClaudeUsage({ view, agents }: { view: View; agents: T.Agent[] }) {
  const t = useT();
  const clock = useNow(60_000);
  const limits = useQuery({ queryKey: ['claudeLimits'], queryFn: api.claudeLimits, refetchInterval: 30_000 });
  const codexLimits = useQuery({ queryKey: ['codexLimits'], queryFn: api.codexLimits, refetchInterval: 30_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agent = view.kind === 'agent' ? agents.find((a) => a.ref === view.ref) : undefined;
  const projectName = view.kind === 'project' ? view.project : view.kind === 'agent' ? view.ref.split('/')[0] : undefined;
  const project = projectName ? projects.data?.find((p) => p.name === projectName) : undefined;
  // On a project's page, or Home, its chat's own tool and account: what the
  // composer chose spends there (RunnerPicker).
  const chatOf = view.kind === 'project' ? project?.name : view.kind === 'home' || view.kind === 'homeChat' ? '_home' : undefined;
  const runners = useQuery({ queryKey: ['chatRunners', chatOf], queryFn: () => api.chatRunners(chatOf!), enabled: !!chatOf, refetchInterval: 30_000 });
  const pick = pickMeter({ limits: limits.data ?? [], codexLimits: codexLimits.data ?? [], project, agent, lead: chatOf ? runners.data : undefined });
  const account = pick.reading;
  const five = account?.windows.find((w) => w.name === 'five_hour') ?? account?.windows[0];
  const week = account?.windows.find((w) => w.name === 'seven_day');
  const codex = pick.tool === 'codex';
  // A tool with no usage to show says so, quietly, so a missing meter isn't
  // taken for a broken one. Claude with no reading stays hidden.
  if ((!account || !five) && (noLimits(pick.tool) || codex)) {
    return (
      <span
        className="hidden items-center gap-1.5 rounded-full py-1 pl-2 pr-2.5 text-[11.5px] text-faint ring-1 ring-inset ring-line sm:flex"
        title={t('shell.top.noLimitsHint', { tool: toolName(pick.tool) })}
        data-usage-quiet={pick.tool}
      >
        <ToolMark ai={pick.tool} className="!size-3.5" />
        {codex ? t('shell.top.noReading', { tool: toolName(pick.tool) }) : t('shell.top.noLimits')}
      </span>
    );
  }
  if (!account || !five) return null;
  const fiveNow = windowNow(five, clock);
  const weekNow = week ? windowNow(week, clock) : null;
  const fiveLeft = fiveNow === null ? null : windowLeft(five.resetsAt, clock);
  const tone = limitTone(Math.max(fiveNow ?? 0, weekNow ?? 0));
  const percent = (now: number | null) => (now === null ? '—' : `${Math.round(Math.max(0, Math.min(1, now)) * 100)}%`);
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            'hidden items-center gap-1.5 rounded-full py-1 pl-2 pr-2.5 ring-1 ring-inset transition sm:flex',
            tone === 'high' ? 'bg-rose-400/10 ring-rose-400/40 hover:bg-rose-400/15' : codex ? 'bg-surface-raised ring-line hover:bg-surface-strong' : 'bg-claude/10 ring-claude/30 hover:bg-claude/15',
          )}
          aria-label={t(codex ? 'shell.top.codexLabel' : 'shell.top.claudeLabel', {
            account: account.account,
            five: percent(fiveNow),
            left: fiveLeft ?? timeUntil(five.resetsAt, clock),
            week: week ? percent(weekNow) : 'none',
          })}
          data-claude-meter={fiveNow === null ? 'reset' : Math.round(fiveNow * 100)}
          data-claude-account={account.account}
        >
          {codex ? <ToolMark ai="codex" className="!size-3.5" /> : <ClaudeMark />}
          <span className="text-[12px] font-medium text-primary">{toolName(pick.tool)}</span>
          <span className="text-[11px] text-muted">{fiveLeft ?? t('shell.top.fiveHourShort')}</span>
          <span className={cn('font-mono text-[11px] font-semibold tabular-nums', limitText(fiveNow))}>{percent(fiveNow)}</span>
          {week && (
            <span className="hidden items-center gap-1.5 md:flex">
              <span className="text-[11px] text-muted">{t('shell.top.weekShort')}</span>
              <span className={cn('font-mono text-[11px] tabular-nums', limitText(weekNow))}>{percent(weekNow)}</span>
            </span>
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80">
        <div className="grid gap-3" data-claude-popover>
          <div className="grid gap-0.5">
            <span className="flex items-center gap-2 text-[13px] font-medium text-primary">
              {codex ? <ToolMark ai="codex" className="!size-4" /> : <ClaudeMark className="size-4" />}
              {t(codex ? 'shell.top.codexTitle' : 'shell.top.claudeTitle', { account: account.account })}
            </span>
            <span className="text-[11.5px] text-muted">{t(codex ? 'shell.top.codexWhose' : 'shell.top.claudeWhose', { whose: pick.whose })}</span>
          </div>
          {account.windows.map((w) => (
            <LimitRow key={w.name} window={w} clock={clock} bar={codex ? 'bg-brand-500' : undefined} />
          ))}
          {fiveNow !== null && fiveNow > 0.85 && (
            <span className="rounded-lg bg-rose-400/10 px-2.5 py-2 text-[11.5px] leading-relaxed text-rose-200 ring-1 ring-inset ring-rose-400/25">
              {t(codex ? 'shell.top.codexAtLimit' : 'shell.top.claudeAtLimit', { left: fiveLeft ?? timeUntil(five.resetsAt, clock) })}
            </span>
          )}
          <OtherAccounts limits={(codex ? codexLimits.data : limits.data) ?? []} shown={account.account} clock={clock} />
          <span className="text-[11px] text-faint">{t('shell.top.asOf', { ago: timeAgo(account.at) })}</span>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function toolName(tool: string): string {
  return tool === 'codex' ? 'Codex' : tool === 'opencode' ? 'OpenCode' : tool === 'cursor' ? 'Cursor' : 'Claude';
}

function limitText(now: number | null): string {
  const tone = limitTone(now);
  return tone === 'high' ? 'text-rose-300' : tone === 'warn' ? 'text-amber-300' : 'text-secondary';
}

// LimitRow is one of an account's limits: how much is used, and when it
// starts again. The composer's AI tool picker shows them too.
export function LimitRow({ window: w, clock, bar = 'bg-claude' }: { window: T.ClaudeLimitWindow; clock: number; bar?: string }) {
  const t = useT();
  const now = windowNow(w, clock);
  const percent = now === null ? 0 : Math.max(0, Math.min(1, now)) * 100;
  const tone = limitTone(now);
  return (
    <div className="grid gap-1" data-limit={w.name}>
      <div className="flex items-baseline justify-between gap-3 text-[12px]">
        <span className="text-secondary">{w.label}</span>
        <span className="font-mono text-[11px] tabular-nums">
          {now === null ? (
            <span className="text-faint">{t('shell.top.resetAgo', { ago: timeAgo(w.resetsAt) })}</span>
          ) : (
            <>
              <span className={cn('font-semibold', limitText(now))}>{t('shell.top.percentUsed', { percent: Math.round(percent) })}</span>
              <span className="text-faint"> · {t('shell.top.resetsIn', { left: (w.name === 'five_hour' && windowLeft(w.resetsAt, clock)) || timeUntil(w.resetsAt, clock) })}</span>
            </>
          )}
        </span>
      </div>
      <span className="block h-1.5 overflow-hidden rounded-full bg-surface-strong">
        {now !== null && (
          <span
            className={cn('block h-full rounded-full', tone === 'high' ? 'bg-rose-400' : tone === 'warn' ? 'bg-amber-400' : bar)}
            style={{ width: `${Math.max(percent, 2)}%` }}
          />
        )}
      </span>
    </div>
  );
}

// OtherAccounts is every other account's latest reading, one line each.
function OtherAccounts({ limits, shown, clock }: { limits: T.ClaudeLimit[]; shown: string; clock: number }) {
  const t = useT();
  const others = limits.filter((l) => l.account !== shown);
  if (others.length === 0) return null;
  return (
    <div className="grid gap-1 border-t border-line pt-2.5 text-[11.5px]">
      <span className="font-medium text-secondary">{t('shell.top.otherAccounts')}</span>
      {others.map((l) => (
        <span key={l.account} className="flex min-w-0 items-center justify-between gap-3 text-muted">
          <span className="min-w-0 truncate">
            {l.account}
            {l.default ? ` (${t('common.default').toLowerCase()})` : ''}
          </span>
          <span className="shrink-0 font-mono tabular-nums text-faint">
            {l.windows
              .filter((w) => w.name === 'five_hour' || w.name === 'seven_day')
              .map((w) => {
                const now = windowNow(w, clock);
                return `${w.name === 'five_hour' ? t('shell.top.fiveHourShort') : t('shell.top.weekShort')} ${now === null ? '—' : `${Math.round(now * 100)}%`}`;
              })
              .join(' · ')}
          </span>
        </span>
      ))}
    </div>
  );
}

// ClaudeMark is a small starburst in Claude's colour, so the Claude item is
// told apart from the machine's by colour and shape before it's read.
function ClaudeMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={cn('size-3.5 shrink-0 text-claude', className)} aria-hidden>
      {Array.from({ length: 8 }, (_, i) => (
        <rect key={i} x="11" y="2" width="2" height="9" rx="1" fill="currentColor" transform={`rotate(${i * 45} 12 12)`} />
      ))}
    </svg>
  );
}

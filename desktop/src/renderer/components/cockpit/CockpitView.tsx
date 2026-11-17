// Cockpit mode (option B, "river"), experimental behind Settings.cockpitMode:
// a view for very wide screens (2560×1440 up to 5120×1440) in two levels.
//
//   All projects  every project's lead in one feed, each message tagged by
//                 its project, with a project filter (any number, down to
//                 one) and a composer that answers any of them in place;
//                 beside it a live wall of every agent, and what needs you
//                 flagged in both.
//   Focus         one project: its lead's chat, one agent on a big stage with
//                 its live visual (its display, Android emulator or iOS
//                 Simulator) or, with nothing visual, its terminal and chat,
//                 and the project's other agents in a strip under it.
//
// Everything here is the app's own: the Office's model of projects and agents
// (office/useOffice.ts), the chat's Composer, ChatTab and ProjectChatPanel,
// and the agent's BrowserTab, AndroidTab, IOSTab, TerminalTab and
// LiveDesktop. What it decides is in lib/cockpit.ts.
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { AppWindow, ArrowUpRight, Check, ChevronDown, CircleDot, Focus, Hand, Layers, LoaderCircle, MessageSquare, Monitor, Smartphone, SquareTerminal, TabletSmartphone, TriangleAlert } from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { chatKey, fetchThread } from '../../lib/chat';
import { feedOf, fitWall, loadHidden, onlyShown, saveHidden, shownProjects, stagedOf, toggleShown, visualOf, type FeedRow, type Level, type Visual } from '../../lib/cockpit';
import { useT } from '../../lib/i18n';
import { terminalLines } from '../../lib/terminalTail';
import { cn } from '../../lib/utils';
import type { DeskModel, DeskTone } from '../../office/model';
import { useOffice } from '../../office/useOffice';
import { AndroidTab } from '../AndroidTab';
import { BrowserTab } from '../BrowserTab';
import { ChatTab } from '../chat/ChatTab';
import { Composer } from '../chat/Composer';
import { Markdown } from '../chat/Markdown';
import { IOSTab } from '../IOSTab';
import { leadAgentFrom, ProjectChatPanel } from '../ProjectChatPanel';
import { ProjectIcon } from '../ProjectIcon';
import { ToolMark } from '../state';
import { TerminalTab } from '../TerminalTab';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../ui/menu';
import { LiveDesktop } from '../pip/LiveDesktop';
import { useDeskUp } from '../v2/DesktopPeek';
import { useWake } from '../v2/RightPanel';

// isCockpitEscape is whether Escape leaves the cockpit (App): not while a
// dialog or menu is open, which takes it, nor in a terminal or a desktop
// being driven, whose keys are the agent's. In a text field the first Escape
// only leaves the field.
export function isCockpitEscape(e: KeyboardEvent): boolean {
  if (e.key !== 'Escape' || e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return false;
  if (document.querySelector('[role="dialog"], [role="menu"], [role="listbox"]')) return false;
  const el = e.target instanceof HTMLElement ? e.target : null;
  if (el?.closest('.xterm, canvas, [data-cockpit-keys]')) return false;
  if (el && (el.isContentEditable || el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT')) {
    el.blur();
    return false;
  }
  return true;
}

// The level shown, kept while the window is open: going back into the
// cockpit lands where it was left.
let lastLevel: Level = { kind: 'all' };
const lastStaged = new Map<string, string>();

const needy = (tone: DeskTone) => tone === 'needs' || tone === 'error';

export function CockpitView({ onSelect }: { onSelect: (view: View) => void }) {
  const t = useT();
  const office = useOffice();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const all = useMemo(() => (projects.data ?? []).map((p) => p.name), [projects.data]);
  const [hidden, setHidden] = useState(loadHidden);
  const shown = useMemo(() => shownProjects(all, hidden), [all, hidden]);
  const changeHidden = (next: Set<string>) => {
    setHidden(next);
    saveHidden(next);
  };
  const [level, setLevelState] = useState<Level>(lastLevel);
  const setLevel = (next: Level) => {
    lastLevel = next;
    setLevelState(next);
  };
  const [staged, setStagedState] = useState<Record<string, string>>(() => Object.fromEntries(lastStaged));
  const stage = (project: string, ref: string) => {
    lastStaged.set(project, ref);
    setStagedState((s) => ({ ...s, [project]: ref }));
  };
  // A project gone since it was focused: back to every project.
  const focusProject = level.kind === 'focus' ? projects.data?.find((p) => p.name === level.project) : undefined;
  useEffect(() => {
    if (level.kind === 'focus' && projects.data && !focusProject) setLevel({ kind: 'all' });
  }, [level, projects.data, focusProject]);

  const needs = office.model.board.needs;
  const needsOf = (project: string) => needs.filter((n) => n.project === project).length;
  const focus = (project: string, ref?: string) => {
    if (ref && !ref.endsWith('/lead')) stage(project, ref);
    setLevel({ kind: 'focus', project });
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-ink" data-cockpit={level.kind}>
      <div className="flex h-12 shrink-0 items-center gap-4 border-b border-line-faint bg-rail px-4">
        <LevelSwitch level={level} projects={projects.data ?? []} onAll={() => setLevel({ kind: 'all' })} onFocus={(p) => focus(p)} needsOf={needsOf} />
        {focusProject && (
          <span className="flex min-w-0 items-center gap-1.5 text-caption text-muted">
            <span className="truncate">{t('cockpit.focus.other')}</span>
            {(projects.data ?? [])
              .filter((p) => p.name !== focusProject.name && needsOf(p.name) > 0)
              .map((p) => (
                <button key={p.name} type="button" onClick={() => focus(p.name)} className="flex h-6 shrink-0 items-center gap-1.5 rounded-full border border-line-faint px-2 text-caption text-secondary transition hover:bg-surface-raised">
                  <ProjectIcon project={p} className="size-4 text-micro" />
                  {p.displayName || p.name}
                  <Count n={needsOf(p.name)} />
                </button>
              ))}
          </span>
        )}
        <span className="flex-1" />
        {needs.length > 0 && (
          <span className="flex h-7 shrink-0 items-center gap-1.5 rounded-md bg-amber-400/15 px-2.5 text-ui font-medium text-amber-200" data-cockpit-needs-total>
            <Hand className="size-3.5" />
            {t('cockpit.needs.total', { count: needs.length })}
          </span>
        )}
        <span className="hidden text-caption text-faint xl:inline">{t('cockpit.escHint')}</span>
      </div>
      {level.kind === 'all' || !focusProject ? (
        <AllProjects
          projects={projects.data ?? []}
          shown={shown}
          hidden={hidden}
          all={all}
          onHidden={changeHidden}
          office={office}
          onFocus={focus}
        />
      ) : (
        <FocusProject key={focusProject.name} project={focusProject} office={office} staged={staged[focusProject.name]} onStage={(ref) => stage(focusProject.name, ref)} onSelect={onSelect} />
      )}
    </div>
  );
}

type Office = ReturnType<typeof useOffice>;

function Count({ n, className }: { n: number; className?: string }) {
  if (!n) return null;
  return <span className={cn('flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-amber-400 px-1 text-micro font-semibold text-black', className)}>{n}</span>;
}

// LevelSwitch is the toggle between every project and one: Focus opens a
// menu of the projects to focus on.
function LevelSwitch({ level, projects, onAll, onFocus, needsOf }: { level: Level; projects: readonly T.Project[]; onAll: () => void; onFocus: (project: string) => void; needsOf: (project: string) => number }) {
  const t = useT();
  const focused = level.kind === 'focus' ? projects.find((p) => p.name === level.project) : undefined;
  return (
    <span className="flex h-8 shrink-0 items-center rounded-lg border border-line-strong bg-surface-faint p-0.5 text-ui" role="group" aria-label={t('cockpit.level')}>
      <button type="button" aria-pressed={level.kind === 'all'} onClick={onAll} className={cn('flex h-full items-center gap-1.5 rounded-md px-2.5 transition', level.kind === 'all' ? 'bg-surface-strong text-title' : 'text-muted hover:text-primary')} data-cockpit-all>
        <Layers className="size-3.5" />
        {t('cockpit.level.all')}
      </button>
      <Menu>
        <MenuTrigger asChild>
          <button type="button" aria-pressed={level.kind === 'focus'} className={cn('flex h-full items-center gap-1.5 whitespace-nowrap rounded-md px-2.5 transition', level.kind === 'focus' ? 'bg-surface-strong text-title' : 'text-muted hover:text-primary')} data-cockpit-focus>
            <Focus className="size-3.5" />
            {focused ? (
              <>
                <ProjectIcon project={focused} className="size-4 text-micro" />
                {focused.displayName || focused.name}
              </>
            ) : (
              t('cockpit.level.focus')
            )}
            <ChevronDown className="size-3 text-faint" />
          </button>
        </MenuTrigger>
        <MenuContent align="start">
          {projects.map((p) => (
            <MenuItem key={p.name} onSelect={() => onFocus(p.name)} hint={needsOf(p.name) ? <Count n={needsOf(p.name)} /> : undefined} data-cockpit-focus-project={p.name}>
              <span className="flex items-center gap-2">
                <ProjectIcon project={p} className="size-4 text-micro" />
                {p.displayName || p.name}
              </span>
            </MenuItem>
          ))}
        </MenuContent>
      </Menu>
    </span>
  );
}

// ——— All projects: the filter and what needs you, the river, the wall ———

function AllProjects({ projects, shown, hidden, all, onHidden, office, onFocus }: { projects: readonly T.Project[]; shown: string[]; hidden: Set<string>; all: string[]; onHidden: (next: Set<string>) => void; office: Office; onFocus: (project: string, ref?: string) => void }) {
  const t = useT();
  const shownSet = new Set(shown);
  const desks = office.model.areas.filter((a) => shownSet.has(a.project)).flatMap((a) => a.desks);
  const needs = office.model.board.needs.filter((n) => shownSet.has(n.project));
  return (
    <div className="flex min-h-0 flex-1">
      <aside className="flex w-72 shrink-0 flex-col gap-6 overflow-y-auto border-r border-line-faint bg-rail p-3" data-cockpit-rail>
        <div className="flex flex-col gap-0.5">
          <span className="px-2 pb-1 text-micro font-semibold uppercase tracking-wide text-faint">{t('cockpit.filter.title')}</span>
          {projects.map((p) => {
            const on = shownSet.has(p.name);
            const area = office.model.areas.find((a) => a.project === p.name);
            const n = office.model.board.needs.filter((x) => x.project === p.name).length;
            return (
              <div key={p.name} className={cn('group flex h-12 items-center gap-2.5 rounded-md px-2 transition hover:bg-surface-raised', on ? 'text-primary' : 'text-faint')} data-cockpit-filter={p.name}>
                <button type="button" role="checkbox" aria-checked={on} aria-label={p.displayName || p.name} onClick={() => onHidden(toggleShown(all, hidden, p.name))} className="flex min-w-0 flex-1 items-center gap-2.5 text-left">
                  <span className={cn('flex size-4 shrink-0 items-center justify-center rounded border', on ? 'border-brand-400 bg-brand-500' : 'border-line-strong')}>{on && <Check className="size-3 text-on-brand" />}</span>
                  <ProjectIcon project={p} className={cn('size-5 text-micro', !on && 'opacity-50')} />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-ui font-medium">{p.displayName || p.name}</span>
                    <span className={cn('truncate text-caption', area?.lead.tone === 'needs' ? 'text-amber-300' : 'text-faint')}>
                      {t(area?.lead.tone === 'needs' ? 'cockpit.lead.waits' : area?.lead.tone === 'working' ? 'cockpit.lead.working' : 'cockpit.lead.quiet')} · {t('cockpit.agents', { count: area?.desks.length ?? 0 })}
                    </span>
                  </span>
                </button>
                <button type="button" onClick={() => onHidden(onlyShown(all, hidden, p.name))} className="hidden shrink-0 rounded border border-line px-1.5 text-caption text-muted hover:text-primary group-hover:block" data-cockpit-only={p.name}>
                  {t('cockpit.filter.only')}
                </button>
                <Count n={n} />
              </div>
            );
          })}
        </div>
        <div className="flex flex-col gap-1.5" data-cockpit-needs>
          <span className="px-2 pb-1 text-micro font-semibold uppercase tracking-wide text-faint">{t('cockpit.needs.title')}</span>
          {needs.length === 0 && <span className="px-2 text-caption text-faint">{t('cockpit.needs.none')}</span>}
          {needs.map((n) => {
            const p = projects.find((x) => x.name === n.project);
            const lead = n.ref.endsWith('/lead');
            return (
              <button
                key={n.ref}
                type="button"
                onClick={() => onFocus(n.project, n.ref)}
                className={cn('flex flex-col gap-1 rounded-md border px-2.5 py-2 text-left transition', n.tone === 'error' ? 'border-rose-400/30 bg-rose-400/5 hover:bg-rose-400/10' : 'border-amber-400/30 bg-amber-400/5 hover:bg-amber-400/10')}
              >
                <span className="flex min-w-0 items-center gap-1.5 text-caption">
                  {p && <ProjectIcon project={p} className="size-4 text-micro" />}
                  <span className="shrink-0 font-medium text-primary">{p?.displayName || n.project}</span>
                  <span className="truncate text-muted">{lead ? t('cockpit.lead') : n.title}</span>
                </span>
                <span className="line-clamp-2 text-caption text-secondary">{n.text || t(lead ? 'cockpit.lead.waits' : n.tone === 'error' ? 'cockpit.agent.failed' : 'cockpit.agent.asks')}</span>
              </button>
            );
          })}
        </div>
      </aside>
      <section className="flex w-[32vw] min-w-[34rem] max-w-[88rem] shrink-0 flex-col border-r border-line-faint bg-chat" data-cockpit-river>
        <River projects={projects} shown={shown} office={office} onFocus={onFocus} />
      </section>
      <Wall desks={desks} office={office} projects={projects} onOpen={(d) => onFocus(d.project, d.id)} />
    </div>
  );
}

const clock = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

// River is every shown project's lead in one feed, oldest at the top, and a
// composer under it that answers whichever lead is chosen as its target: the
// chat's own Composer, so a lead waiting on a permission is answered there too.
function River({ projects, shown, office, onFocus }: { projects: readonly T.Project[]; shown: string[]; office: Office; onFocus: (project: string) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const chats = useQueries({ queries: shown.map((name) => ({ queryKey: ['projectChat', name], queryFn: () => api.projectChat(name), refetchInterval: 60_000 })) });
  const refs = shown.map((name, i) => ({ project: name, info: chats[i]?.data }));
  const threads = useQueries({
    queries: refs.map(({ info }) => ({ queryKey: chatKey(info?.ref ?? '-'), queryFn: () => fetchThread(queryClient, info!.ref), enabled: !!info?.ref })),
  });
  const threadKey = threads.map((q) => q.dataUpdatedAt).join(',');
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const rows = useMemo(() => feedOf(refs.map(({ project, info }, i) => ({ project, ref: info?.ref ?? '', items: threads[i]?.data?.items }))), [threadKey, shown.join('\n')]);
  const tone = (project: string) => office.model.areas.find((a) => a.project === project)?.lead.tone ?? 'idle';

  // Whom the composer answers: the one chosen, else a lead waiting on you,
  // else whoever spoke last.
  const [chosen, setChosen] = useState<string>();
  const target = (chosen && shown.includes(chosen) ? chosen : undefined) ?? shown.find((p) => tone(p) === 'needs') ?? rows.at(-1)?.project ?? shown[0];
  const targetProject = projects.find((p) => p.name === target);
  const targetInfo = refs.find((r) => r.project === target)?.info;
  const targetThread = threads[shown.indexOf(target ?? '')]?.data;

  // The feed stays at its newest message, unless scrolled up to read.
  const scroller = useRef<HTMLDivElement>(null);
  const atEnd = useRef(true);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atEnd.current) el.scrollTop = el.scrollHeight;
  }, [rows]);

  const last = new Map<string, FeedRow>();
  for (const r of rows) last.set(r.project, r);
  const projectOf = (name: string) => projects.find((p) => p.name === name);

  return (
    <>
      <div
        ref={scroller}
        onScroll={(e) => {
          const el = e.currentTarget;
          atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
        className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto px-4 py-3"
      >
        <span className="flex-1" />
        {rows.length === 0 && <span className="py-8 text-center text-ui text-faint">{threads.some((q) => q.isPending && q.fetchStatus !== 'idle') ? t('common.loading') : t('cockpit.river.empty')}</span>}
        {rows.map((r) => (
          <div key={r.item.id} className="flex flex-col gap-1.5">
            <RiverRow row={r} project={projectOf(r.project)} target={r.project === target} onReply={() => setChosen(r.project)} onFocus={() => onFocus(r.project)} />
            {last.get(r.project) === r && tone(r.project) === 'working' && (
              <span className="flex items-center gap-1.5 pl-[5.75rem] text-caption text-muted">
                <LoaderCircle className="size-3 animate-spin" />
                {t('cockpit.river.writing', { project: projectOf(r.project)?.displayName || r.project })}
              </span>
            )}
            {last.get(r.project) === r && tone(r.project) === 'needs' && (
              <button type="button" onClick={() => setChosen(r.project)} className="ml-[5.75rem] flex items-center gap-2 self-start rounded-md border border-amber-400/50 bg-amber-400/10 px-2.5 py-1.5 text-caption text-amber-200 transition hover:bg-amber-400/15" data-cockpit-lead-waits={r.project}>
                <Hand className="size-3.5" />
                {t('cockpit.river.waits', { project: projectOf(r.project)?.displayName || r.project })}
              </button>
            )}
          </div>
        ))}
      </div>
      <div className="flex flex-col gap-1.5 border-t border-line-faint px-3 pb-2 pt-2" data-cockpit-composer>
        <Menu>
          <MenuTrigger asChild>
            <button type="button" className="flex h-7 items-center gap-1.5 self-start rounded-md bg-surface px-2 text-caption text-primary transition hover:bg-surface-raised" data-cockpit-target={target}>
              {t('cockpit.river.to')}
              {targetProject && <ProjectIcon project={targetProject} className="size-4 text-micro" />}
              {targetProject?.displayName || target}
              {target && tone(target) === 'needs' && <Hand className="size-3 text-amber-300" />}
              <ChevronDown className="size-3 text-faint" />
            </button>
          </MenuTrigger>
          <MenuContent align="start">
            {shown.map((name) => {
              const p = projectOf(name);
              return (
                <MenuItem key={name} onSelect={() => setChosen(name)} hint={tone(name) === 'needs' ? <Hand className="size-3 text-amber-300" /> : undefined}>
                  <span className="flex items-center gap-2">
                    {p && <ProjectIcon project={p} className="size-4 text-micro" />}
                    {p?.displayName || name}
                  </span>
                </MenuItem>
              );
            })}
          </MenuContent>
        </Menu>
        {targetProject && targetInfo ? <Composer key={targetInfo.ref} agent={leadAgentFrom(targetProject, targetInfo)} thread={targetThread} disabled={false} onSent={() => (atEnd.current = true)} /> : null}
      </div>
    </>
  );
}

// RiverRow is one message, tagged with its project; long ones fold.
function RiverRow({ row, project, target, onReply, onFocus }: { row: FeedRow; project: T.Project | undefined; target: boolean; onReply: () => void; onFocus: () => void }) {
  const t = useT();
  const me = row.item.kind === 'user';
  const [open, setOpen] = useState(false);
  const long = (row.item.text?.length ?? 0) > 600;
  return (
    <div className={cn('group relative flex gap-3 rounded-md border-l-2 py-1.5 pl-3 pr-2', target ? 'border-brand-400/70 bg-surface-faint' : 'border-line-faint')} data-cockpit-row={row.project}>
      <span className="w-16 shrink-0 whitespace-nowrap pt-0.5 text-right text-caption tabular-nums text-faint">{clock(row.item.createdAt)}</span>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex items-center gap-1.5 text-caption">
          <button type="button" onClick={onFocus} className="flex items-center gap-1.5 font-semibold text-title hover:underline">
            {project && <ProjectIcon project={project} className="size-4 text-micro" />}
            {project?.displayName || row.project}
          </button>
          <span className="text-faint">·</span>
          <span className="text-muted">{me ? t('cockpit.you') : t('cockpit.lead')}</span>
        </span>
        <div className={cn('min-w-0 text-ui leading-relaxed [overflow-wrap:anywhere]', me ? 'text-primary' : 'text-secondary', long && !open && 'max-h-40 overflow-hidden [mask-image:linear-gradient(to_bottom,black_70%,transparent)]')}>
          <Markdown text={row.item.text ?? ''} streaming={row.item.streaming} />
        </div>
        {long && (
          <button type="button" onClick={() => setOpen(!open)} className="self-start text-caption text-muted hover:text-primary">
            {open ? t('cockpit.river.less') : t('cockpit.river.more')}
          </button>
        )}
      </div>
      <button type="button" onClick={onReply} className="absolute -top-2 right-2 hidden h-7 items-center gap-1 rounded-md border border-line-strong bg-overlay px-2 text-caption text-primary shadow-pop group-hover:flex" data-cockpit-reply={row.project}>
        <MessageSquare className="size-3.5" />
        {t('cockpit.river.reply', { project: project?.displayName || row.project })}
      </button>
    </div>
  );
}

// ——— The wall: every agent, live ———

// useTails is the end of each desk's terminal, for the tiles with no desktop
// up: one request a project, every few seconds.
function useTails(desks: readonly DeskModel[]) {
  const projects = [...new Set(desks.map((d) => d.project))];
  const tails = useQueries({
    queries: projects.map((project) => {
      const names = desks.filter((d) => d.project === project && d.tone !== 'stopped').map((d) => d.name);
      return { queryKey: ['terminalTails', project, names.join(',')], queryFn: () => api.terminalTails(project, names, 8), enabled: names.length > 0, refetchInterval: 5_000 };
    }),
  });
  const out = new Map<string, T.TerminalTail>();
  for (const q of tails) for (const tail of q.data?.agents ?? []) out.set(tail.ref, tail);
  return out;
}

function Wall({ desks, office, projects, onOpen }: { desks: DeskModel[]; office: Office; projects: readonly T.Project[]; onOpen: (d: DeskModel) => void }) {
  const t = useT();
  const box = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const cols = fitWall(desks.length, size.w, size.h);
  const tails = useTails(desks);
  const working = desks.filter((d) => d.tone === 'working').length;
  const waiting = desks.filter((d) => needy(d.tone)).length;
  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 p-4" data-cockpit-wall>
      <div className="flex items-center gap-2 text-ui">
        <Monitor className="size-4 text-muted" />
        <span className="font-semibold text-title">{t('cockpit.wall.title')}</span>
        <span className="text-caption text-faint">{t('cockpit.wall.counts', { agents: desks.length, working, waiting })}</span>
      </div>
      <div ref={box} className="min-h-0 flex-1 overflow-y-auto">
        {desks.length === 0 ? (
          <span className="flex h-full items-center justify-center text-ui text-faint">{t('cockpit.wall.empty')}</span>
        ) : (
          <div className="grid content-start gap-2" style={{ gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))` }}>
            {desks.map((d) => {
              const agent = office.agents.get(d.id);
              return agent ? <Tile key={d.id} desk={d} agent={agent} project={projects.find((p) => p.name === d.project)} tail={tails.get(d.id)} onOpen={() => onOpen(d)} /> : null;
            })}
          </div>
        )}
      </div>
    </section>
  );
}

function ToneMark({ tone, className }: { tone: DeskTone; className?: string }) {
  const c = cn('size-3.5 shrink-0', className);
  if (tone === 'working') return <LoaderCircle className={cn(c, 'animate-spin text-brand-300')} />;
  if (tone === 'needs') return <Hand className={cn(c, 'text-amber-300')} />;
  if (tone === 'error') return <TriangleAlert className={cn(c, 'text-rose-300')} />;
  return <CircleDot className={cn(c, tone === 'finished' ? 'text-sky-300' : 'text-faint')} />;
}

// Tile is an agent on the wall or in the focus strip: its desktop live while
// it's up, else the end of its terminal; flagged when it needs you.
function Tile({ desk, agent, project, tail, onOpen, compact, active }: { desk: DeskModel; agent: T.Agent; project?: T.Project; tail?: T.TerminalTail; onOpen: () => void; compact?: boolean; active?: boolean }) {
  const up = useDeskUp(agent);
  const lines = terminalLines(tail, undefined, desk.question, 8);
  return (
    <button
      type="button"
      onClick={onOpen}
      className={cn(
        'flex w-full min-w-0 flex-col gap-1 rounded-md p-1 text-left transition hover:bg-surface-raised',
        desk.tone === 'needs' ? 'bg-amber-400/10 ring-2 ring-amber-400/70' : desk.tone === 'error' ? 'bg-rose-400/10 ring-2 ring-rose-400/60' : active ? 'ring-2 ring-brand-400/60' : '',
      )}
      data-cockpit-tile={desk.id}
      data-tone={desk.tone}
    >
      <div className="relative overflow-hidden rounded border border-line-faint bg-stage">
        {up ? (
          <LiveDesktop agent={agent} small />
        ) : (
          <div className="flex aspect-[16/10] w-full flex-col justify-end overflow-hidden p-2 font-mono text-micro leading-snug text-secondary">
            {lines.length > 0 ? lines.map((l, i) => <span key={i} className="truncate">{l}</span>) : <SquareTerminal className="m-auto size-5 text-faint" />}
          </div>
        )}
        {needy(desk.tone) && desk.question && (
          <span className={cn('absolute inset-x-0 bottom-0 line-clamp-2 px-2 py-1 text-caption', desk.tone === 'needs' ? 'bg-overlay text-amber-200' : 'bg-overlay text-rose-200')}>{desk.question}</span>
        )}
      </div>
      <span className="flex min-w-0 shrink-0 items-center gap-1.5 px-0.5 text-caption">
        {!compact && project && <ProjectIcon project={project} className="size-4 text-micro" />}
        {!compact && <span className="shrink-0 text-muted">{project?.displayName || desk.project}</span>}
        <ToneMark tone={desk.tone} className="size-3" />
        <ToolMark ai={desk.ai} className="size-3.5" />
        <span className="truncate font-medium text-primary">{desk.title}</span>
      </span>
    </button>
  );
}

// ——— Focus: one project ———

const visualIcon: Record<Visual, typeof Monitor> = { display: AppWindow, android: Smartphone, ios: TabletSmartphone, none: SquareTerminal };
const visualLabel = { display: 'cockpit.visual.display', android: 'cockpit.visual.android', ios: 'cockpit.visual.ios', none: 'cockpit.visual.none' } as const;

function FocusProject({ project, office, staged, onStage, onSelect }: { project: T.Project; office: Office; staged: string | undefined; onStage: (ref: string) => void; onSelect: (view: View) => void }) {
  const t = useT();
  const desks = office.model.areas.find((a) => a.project === project.name)?.desks ?? [];
  const desk = stagedOf(desks, staged);
  const agent = desk ? office.agents.get(desk.id) : undefined;
  const rest = desks.filter((d) => d !== desk);
  const tails = useTails(rest);
  return (
    <div className="flex min-h-0 flex-1">
      <section className="flex w-[24vw] min-w-[30rem] max-w-[56rem] shrink-0 flex-col border-r border-line-faint bg-chat" data-cockpit-lead={project.name}>
        <ProjectChatPanel project={project} />
      </section>
      <section className="flex min-w-0 flex-1 flex-col gap-3 p-3">
        {desk && agent ? (
          <Stage key={agent.ref} desk={desk} agent={agent} onSelect={onSelect} />
        ) : (
          <div className="flex min-h-0 flex-1 items-center justify-center rounded-lg border border-line bg-chat text-ui text-faint">{t('cockpit.focus.noAgents', { project: project.displayName || project.name })}</div>
        )}
        {rest.length > 0 && (
          <div className="flex h-60 shrink-0 gap-3 overflow-x-auto" data-cockpit-strip>
            {rest.map((d) => {
              const a = office.agents.get(d.id);
              return a ? (
                <div key={d.id} className="w-80 shrink-0">
                  <Tile desk={d} agent={a} project={project} tail={tails.get(d.id)} compact onOpen={() => onStage(d.id)} />
                </div>
              ) : null;
            })}
          </div>
        )}
      </section>
    </div>
  );
}

// useVisual is what an agent shows on the stage.
function useVisual(agent: T.Agent): Visual {
  const running = agent.state === 'running';
  const info = useQuery({ queryKey: ['app-info'], queryFn: () => window.agentbox.info(), staleTime: Infinity });
  const android = useQuery({ queryKey: ['android', agent.ref], queryFn: () => api.android(agent.ref), enabled: running, refetchInterval: 15_000 });
  const ios = useQuery({ queryKey: ['ios', agent.ref], queryFn: () => api.ios(agent.ref), enabled: running && info.data?.platform === 'darwin', refetchInterval: 30_000 });
  const display = useDeskUp(agent);
  return visualOf({ android: android.data, ios: ios.data, display });
}

// Stage is the agent in focus, big: its live visual, with its chat beside it
// on a screen wide enough; with nothing visual, its terminal and its chat.
function Stage({ desk, agent, onSelect }: { desk: DeskModel; agent: T.Agent; onSelect: (view: View) => void }) {
  const t = useT();
  const visual = useVisual(agent);
  const wake = useWake(agent);
  const V = visualIcon[visual];
  const media = () => onSelect({ kind: 'agent', ref: agent.ref, tab: 'media' });
  const chat = <ChatTab key={agent.ref} agent={agent} starting={wake.isPending} onStart={() => wake.mutate()} onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })} />;
  let body: ReactNode;
  if (visual === 'none') {
    body = (
      <div className="flex min-h-0 flex-1">
        <div className="flex min-h-0 min-w-0 flex-[3] flex-col border-r border-line-faint" data-cockpit-keys>
          <TerminalTab agent={agent} starting={wake.isPending} onStart={() => wake.mutate()} />
        </div>
        <div className="flex min-h-0 min-w-0 flex-[2] flex-col bg-chat">{chat}</div>
      </div>
    );
  } else {
    const screen = visual === 'android' ? <AndroidTab agent={agent} onOpenMedia={media} onSetup={() => onSelect({ kind: 'settings' })} /> : visual === 'ios' ? <IOSTab agent={agent} onOpenMedia={media} /> : <BrowserTab agent={agent} onOpenMedia={media} />;
    body = (
      <div className="flex min-h-0 flex-1">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto" data-cockpit-keys>
          {screen}
        </div>
        {/* Beside the screen only where there's room for both. */}
        <div className="hidden min-h-0 w-[34rem] shrink-0 flex-col border-l border-line-faint bg-chat min-[3000px]:flex">{chat}</div>
      </div>
    );
  }
  return (
    <div className={cn('flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border bg-chat', needy(desk.tone) ? 'border-amber-400/70' : 'border-line')} data-cockpit-stage={agent.ref} data-visual={visual}>
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-line-faint px-3 text-ui">
        <ToneMark tone={desk.tone} />
        <ToolMark ai={agent.ai} className="size-4" />
        <span className="shrink-0 font-medium text-title">{desk.title}</span>
        {desk.question && <span className={cn('truncate', desk.tone === 'error' ? 'text-rose-300' : 'text-amber-300')}>{desk.question}</span>}
        <span className="flex-1" />
        <span className="flex shrink-0 items-center gap-1 text-caption text-faint">
          <V className="size-3.5" />
          {t(visualLabel[visual])}
        </span>
        <button type="button" onClick={() => onSelect({ kind: 'agent', ref: agent.ref })} className="flex h-7 shrink-0 items-center gap-1 rounded-md px-2 text-caption text-muted transition hover:bg-surface-raised hover:text-primary" data-cockpit-open-agent>
          {t('cockpit.stage.open')}
          <ArrowUpRight className="size-3.5" />
        </button>
      </div>
      {body}
    </div>
  );
}

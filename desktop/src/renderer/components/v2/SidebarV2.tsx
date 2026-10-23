// UI v2's sidebar, lead-first: most of the time is spent talking to each
// project's lead, so the list is Home and one row per lead, with its icon,
// its last line and its live agents' counts (working, to review, asks,
// failed) and what you haven't seen. Its agents aren't here: they're in the
// right-hand panel, beside its chat (v2/AgentList.tsx), and finished ones
// in Past agents. Under Home, search and a
// filter popover (T3Code's: state, project, removable chips) narrow it. The
// projects the user pinned come first, in the order they dragged them, then
// the ones with something going on, most recent first, and docked at the
// bottom, above the foot, the Idle list of the rest, folded unless nothing is
// going on, and under it the projects the user hid, folded. The
// project on screen never folds into Idle: the chat you're in stays in view.
// The user's sections, Classic's (lib/sidebar.ts, the same data), go between
// the pins and the rest, each with its projects in the order the user put
// them: a section is a place the user chose, so neither activity nor Idle
// moves a project out of one. A pin is still with the pins and a hidden
// project with the hidden, and back in its section when unpinned or shown.
// That grouping is a setting, off by default (lib/groupIdle.ts): off, every
// project is listed in a stable order and activity never moves one.
// A project's options are on a right-click of its row, its Settings on a
// gear its row shows on hover. The reading is
// lib/leadFirst.ts's, lib/projectOrder.ts's and lib/sidebarHidden.ts's.
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { Briefcase, Check, ChevronRight, CircleArrowUp, Eye, EyeOff, Folder, FolderInput, FolderMinus, FolderPlus, FolderTree, House, Images, ListChecks, ListFilter, LoaderCircle, MoonStar, RefreshCw, Search, Settings, ShieldAlert, SquarePen, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type ComponentProps, type DragEvent, type ReactNode } from 'react';
import * as A from '../../../shared/api';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT, type MessageKey } from '../../lib/i18n';
import { pageItems, useQuestions, useReviewHead } from '../../lib/inboxPages';
import { activeProjects, agentSt, countSt, filterCount, leadLine, leadBusy, liveOrder, projectRows, toggle, type AgentSt, type Filters, type ProjectRows, type Row } from '../../lib/leadFirst';
import { useGroupIdle } from '../../lib/groupIdle';
import { movePin, orderProjects, togglePin } from '../../lib/projectOrder';
import { HelpMenu } from './Tour';
import { setFilterChoice, setHiddenOpen, setPins, useV2Projects } from '../../lib/v2Projects';
import { arrange, hide, pinsAfterHide, sections, unhide, type Slot } from '../../lib/sidebarHidden';
import { drop as dropOn, listEnd, sectionsAside, stepTarget, type DropTarget, type SidebarList } from '../../lib/sidebar';
import { edge, useSectionDrag, useSections } from '../../lib/useSections';
import { useSidebarLayout } from '../../lib/useSidebarLayout';
import { useGrace } from '../../lib/grace';
import { projectLabel } from '../../lib/projectName';
import { useReviewing } from '../../lib/reviewAlert';
import { useNow } from '../../lib/useNow';
import { updateHint } from '../../lib/appUpdate';
import { isNightly, isUpgrade } from '../../lib/nightly';
import { useAppUpdate } from '../../lib/useAppUpdate';
import { cn, errorMessage, shortcut, timeAgo } from '../../lib/utils';
import { openNewTask, requestComposerFocus } from '../../lib/newTask';
import { openPanelOn, setPanel } from '../../lib/rightPanel';
import { describeDelete, dropLine, Logo, moveKeys, NameSection, RenameSection, SectionHeader } from '../Sidebar';
import { ConfirmDialog } from '../ConfirmDialog';
import { ProjectIcon, ProjectIconDialog } from '../ProjectIcon';
import { ReviewCount, useOpenWork } from './ReviewV2';
import { projectActions, type ProjectAction } from './ProjectHeader';
import { TalkToProject } from './TalkToProject';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuMore, ContextMenuSeparator, ContextMenuTrigger } from '../ui/context-menu';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';
import { Tip } from '../ui/tooltip';
import { GroupChips } from '../groups/GroupChip';

const stLabel: Record<AgentSt, MessageKey> = {
  needs: 'v2.st.needs',
  error: 'v2.st.error',
  review: 'v2.st.review',
  working: 'v2.st.working',
  done: 'v2.st.done',
};

// The counts on a folded lead, in the order they read.
const countKeys: { st: AgentSt; key: MessageKey }[] = [
  { st: 'working', key: 'v2.count.working' },
  { st: 'review', key: 'v2.count.review' },
  { st: 'needs', key: 'v2.count.needs' },
  { st: 'error', key: 'v2.count.error' },
];

export function SidebarV2({ view, onSelect, onAddProject, onNewAgent }: { view: View; onSelect: (view: View) => void; onAddProject: () => void; onNewAgent: (project: string) => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const activity = useQuery({ queryKey: ['projectActivity'], queryFn: api.projectActivity, refetchInterval: 60_000 });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });
  // The same test as Sidebar's: a nightly is one by its version, or by the update check's word.
  const update = useQuery({ queryKey: ['update'], queryFn: api.update, staleTime: Infinity });
  const info = useQuery({ queryKey: ['app-info'], queryFn: () => window.agentbox.info(), staleTime: Infinity });
  const nightly = isNightly(info.data?.version) || !!update.data?.nightly;
  const reviewing = useReviewing();
  const head = useReviewHead(reviewing);
  const questions = useQuestions();
  const kept = useV2Projects();
  const [q, setQ] = useState('');
  const filters: Filters = { st: kept.st, projects: kept.projects, q };
  const onFilters = (f: Filters) => {
    if (f.st !== kept.st || f.projects !== kept.projects) setFilterChoice(f.st, f.projects);
    setQ(f.q);
  };
  // No project opens its agents here any more: they're in the panel.
  const expanded: ReadonlyMap<string, boolean> = useMemo(() => new Map(), []);
  const [dragging, setDragging] = useState<string | null>(null);
  const [drop, setDrop] = useState<{ name: string; edge: 'before' | 'after' } | null>(null);

  // Each agent's pull request number, from the fleet the panel's list reads
  // (the same query), so a search can find an agent by it.
  const fleets = useQueries({
    queries: (projects.data ?? []).map((p) => ({ queryKey: ['fleet', p.name], queryFn: () => api.fleet(p.name), staleTime: 30_000 })),
  });
  const prKey = fleets.flatMap((f) => (f.data?.agents ?? []).flatMap((a) => (a.pr?.number ? [`${a.ref}\t${a.pr.number}`] : []))).join('\n');
  const prs = useMemo(() => new Map(prKey ? prKey.split('\n').map((l) => { const [ref, n] = l.split('\t'); return [ref, Number(n)] as const; }) : []), [prKey]);
  const rows: Row[] = useMemo(() => {
    const ctx = {
      review: new Set((reviewing ? (head.data?.items ?? []) : []).map((i) => i.ref)),
      asking: new Set(pageItems(questions.data).map((n) => n.ref)),
    };
    return (agents.data ?? []).map((agent) => ({ agent, st: agentSt(agent, ctx), pr: prs.get(agent.ref) }));
  }, [agents.data, head.data, questions.data, reviewing, prs]);
  const label = (p: T.Project) => projectLabel(p);
  const currentAgentProject = view.kind === 'agent' ? view.ref.split('/')[0] : undefined;

  // Each lead's chat, the same queries its row reads: a lead mid-turn counts
  // as working, for the filter, the counts and the sort, and with one waiting
  // on the user's permission keeps its project out of the Idle list.
  const leadChats = useQueries({
    queries: (projects.data ?? []).map((p) => ({ queryKey: ['projectChat', p.name], queryFn: () => api.projectChat(p.name), refetchInterval: 60_000 })),
  });
  const leadKey = (projects.data ?? []).map((p, i) => `${p.name}\t${leadChats[i]?.data?.chat ?? ''}`).join('\n');
  const leads = useMemo(() => new Map(leadKey ? leadKey.split('\n').map((l) => l.split('\t') as [string, string]) : []), [leadKey]);
  const busyNow = useMemo(() => new Set([...leads].filter(([, chat]) => leadBusy(chat)).map(([name]) => name)), [leads]);
  const activeNow = useMemo(() => activeProjects(rows, leads, pageItems(questions.data).map((n) => n.ref)), [rows, leads, questions.data]);
  // Both with a grace (lib/grace.ts): a busy project goes through moments
  // with nothing to show for it, between a lead's turns or as an agent
  // finishes, which would drop it to Idle and bring it back.
  const busy = useGrace(busyNow);
  const activeGrace = useGrace(activeNow);
  // The project on screen, its lead's or one of its agents', counts as
  // active: folding it away would lose the chat you're reading.
  const onScreen = view.kind === 'project' ? view.project : currentAgentProject;
  const active = useMemo(() => (onScreen && !activeGrace.has(onScreen) ? new Set([...activeGrace, onScreen]) : activeGrace), [activeGrace, onScreen]);

  const grouping = useGroupIdle();
  const order = useMemo(() => {
    return orderProjects({
      groupIdle: grouping,
      projects: projects.data ?? [],
      pins: kept.pins,
      activity: new Map((activity.data ?? []).map((a) => [a.project, a])),
      active,
      label,
    });
    // label is projectLabel's, the same each render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projects.data, activity.data, active, kept.pins, grouping]);
  const pinned = new Set(kept.pins);
  const ctx = { busy, keep: pinned };
  const { layout, save } = useSidebarLayout();
  const hiddenSet = new Set(layout.hidden);
  // A hidden project comes back while it has something going on, which the
  // project on screen alone doesn't count as: opened from Hidden, it stays there.
  const arranged = arrange(order, layout, activeGrace);
  // The user's sections, each with the projects in it that aren't with the
  // pins or the hidden; the rest of the lists are the projects in none.
  const sec = useSections();
  const dnd = useSectionDrag(sec.lists, sec.apply);
  const { sections: userSections, shown: inSection } = sectionsAside(sec.lists, new Set([...arranged.pinned, ...arranged.hidden].map((p) => p.name)));
  const loose = (ps: T.Project[]) => ps.filter((p) => !inSection.has(p.name));
  const lists = {
    pinned: projectRows(arranged.pinned, rows, filters, expanded, label, ctx),
    rest: projectRows(loose(arranged.rest), rows, filters, expanded, label, ctx),
    idle: projectRows(loose(arranged.idle), rows, filters, expanded, label, ctx),
    hidden: projectRows(arranged.hidden, rows, filters, expanded, label, ctx),
  };
  const searching = q.trim() !== '';
  // Under a search or a filter, a section shows what matches in it, open,
  // and one with nothing that matches doesn't show; otherwise every section
  // does, an empty one too, to drop projects into.
  const filtering = searching || filterCount(filters) > 0;
  const sectionLists = userSections.flatMap((list) => {
    const shown = projectRows(list.projects, rows, filters, expanded, label, ctx);
    return filtering && shown.length === 0 ? [] : [{ section: list.section!, list, shown }];
  });
  const [naming, setNaming] = useState<{ id: string; move?: string } | null>(null); // a section being named: its id, or '' for a new one
  const [deleting, setDeleting] = useState<T.Section | null>(null);
  // Until the agents are in, every project would read idle.
  const loaded = !!agents.data;
  // With nothing above it, the Idle list is the list: open, and not a fold.
  const idleOnly = lists.pinned.length + lists.rest.length + sectionLists.length === 0;
  // Otherwise it is folded, and a click opens it only until something starts:
  // an agent starting never leaves it open under the project it started in.
  const [idleOpened, setIdleOpened] = useState(false);
  useEffect(() => setIdleOpened(false), [idleOnly]);
  // Docked at the bottom, it opens below the fold: a click scrolls it into view.
  const idleRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (idleOpened) idleRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }, [idleOpened]);
  // A search looks in it too, and opens it on a match.
  const idleOpen = idleOnly || idleOpened || (searching && lists.idle.length > 0);
  const hiddenOpen = kept.hiddenOpen || (searching && lists.hidden.length > 0) || (!!onScreen && lists.hidden.some((p) => p.project.name === onScreen));
  const nothing = projects.data && projects.data.length > 0 && lists.pinned.length + lists.rest.length + lists.idle.length + lists.hidden.length + sectionLists.reduce((n, s) => n + s.shown.length, 0) === 0;

  // Review all goes after the first list's label, whichever list is first.
  const openWork = useOpenWork();
  const toReview = reviewing ? (head.data?.total ?? 0) : 0;
  let reviewPlaced = false;
  const reviewAll = () => {
    if (reviewPlaced || !openWork || toReview === 0) return null;
    reviewPlaced = true;
    return (
      <Tip label={t('v2.review.allHint')}>
        <button
          type="button"
          onClick={() => openWork({ filter: 'review' })}
          className="ml-1.5 shrink-0 rounded-full border border-line-strong bg-surface px-1.5 py-px text-[10.5px] font-medium normal-case tracking-normal tabular-nums text-secondary transition hover:border-line-heavy hover:bg-surface-strong hover:text-title"
          data-v2-review-all={toReview}
        >
          {t('v2.review.all', { count: toReview })}
        </button>
      </Tip>
    );
  };

  // Moving a project with the keyboard (Alt+↑/↓): a pin among the pins, and
  // any other among the sections, as Classic does (stepTarget). The row is
  // drawn again where it lands, often in another list, so it takes the focus
  // back.
  const refocus = (name: string) => requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-v2-lead="${CSS.escape(name)}"]`)?.focus());
  const stepPin = (name: string) => (delta: -1 | 1) => {
    const i = kept.pins.indexOf(name);
    const next = kept.pins[i + delta];
    if (i < 0 || !next) return;
    setPins(movePin(kept.pins, name, next, delta === -1 ? 'before' : 'after'));
    refocus(name);
  };
  const shownLists: SidebarList[] = [...userSections, { section: null, projects: [...lists.rest, ...lists.idle].map((p) => p.project) }];
  const moveTo = (name: string, at: DropTarget | null) => {
    if (!at) return;
    sec.apply(dropOn(sec.lists, { kind: 'project', name }, at), name);
    refocus(name);
  };
  const stepProject = (name: string) => (delta: -1 | 1) => moveTo(name, stepTarget(shownLists, name, delta));

  // Dragging a project into a section, out of one or within one. Dropped on
  // a project in a section it goes beside it; on one in no section, out of
  // its section (the projects in none have no order of their own here).
  const rowTarget = (name: string, section: string | null, at: 'before' | 'after'): DropTarget | null => {
    const what = dnd.draggingRef.current;
    if (what?.kind !== 'project') return null;
    if (section !== null) return { kind: 'project', name, edge: at };
    return inSection.has(what.name) ? { kind: 'list', section: null } : null;
  };
  const anySection = userSections.length > 0;
  const sectionDrag = (name: string, section: string | null): Drag | undefined =>
    anySection
      ? {
          over: section === null ? undefined : (['before', 'after'] as const).find((at) => dnd.isTarget({ kind: 'project', name, edge: at })),
          dragged: dnd.dragging?.kind === 'project' && dnd.dragging.name === name,
          onStart: dnd.startDrag({ kind: 'project', name }),
          onEnd: dnd.finishDrag,
          onOver: (at) => {
            const where = rowTarget(name, section, at);
            if (where) dnd.aim(where);
            return !!where;
          },
          onDrop: (at) => {
            const where = rowTarget(name, section, at);
            if (where) dnd.commitDrop(where);
            return !!where;
          },
        }
      : undefined;

  const lead = (p: ProjectRows, drag?: Drag, onMove?: (delta: -1 | 1) => void) => (
    <ProjectLead
      key={p.project.name}
      project={p.project}
      all={p.all}
      approvals={p.approvals}
      pinned={pinned.has(p.project.name)}
      hidden={hiddenSet.has(p.project.name)}
      view={view}
      onPin={() => {
        const pin = !pinned.has(p.project.name);
        setPins(togglePin(kept.pins, p.project.name));
        // Pinning a hidden project brings it back.
        if (pin && hiddenSet.has(p.project.name)) save(unhide(layout, p.project.name));
      }}
      onHide={() => {
        if (hiddenSet.has(p.project.name)) return save(unhide(layout, p.project.name));
        // A pin is never hidden: hiding one unpins it, so it moves to Hidden now.
        if (pinned.has(p.project.name)) setPins(pinsAfterHide(kept.pins, p.project.name));
        save(hide(layout, p.project.name));
      }}
      onSelect={onSelect}
      onNewAgent={() => onNewAgent(p.project.name)}
      drag={drag}
      onMove={onMove}
      // Where a pinned or hidden project shows is the pins' or Hidden's, so
      // only the others move between sections from the menu.
      sectionMenu={
        pinned.has(p.project.name) || hiddenSet.has(p.project.name)
          ? undefined
          : {
              sections: sec.lists.flatMap((l) => (l.section ? [l.section] : [])),
              current: sec.lists.find((l) => l.projects.some((x) => x.name === p.project.name))?.section?.id ?? '',
              onMove: (id) => moveTo(p.project.name, { kind: 'list', section: id }),
              onNew: () => setNaming({ id: '', move: p.project.name }),
            }
      }
    />
  );
  const pinDrag = (name: string): Drag => ({
    over: drop?.name === name ? drop.edge : undefined,
    dragged: dragging === name,
    onStart: () => setDragging(name),
    onEnd: () => {
      setDragging(null);
      setDrop(null);
    },
    onOver: (at) => {
      if (!dragging || dragging === name) return false;
      setDrop({ name, edge: at });
      return true;
    },
    onDrop: () => {
      if (!dragging) return false;
      if (drop) setPins(movePin(kept.pins, dragging, drop.name, drop.edge));
      setDragging(null);
      setDrop(null);
      return true;
    },
  });

  const newSection = (
    <Tip label={t('shell.sidebar.newSection')}>
      <button
        type="button"
        aria-label={t('shell.sidebar.newSection')}
        onClick={() => setNaming({ id: '' })}
        className="ml-auto rounded-md p-1 text-subtle transition hover:bg-surface-strong hover:text-primary"
        data-v2-new-section
      >
        <FolderTree className="size-3.5" />
      </button>
    </Tip>
  );
  // A new section's name is typed in where it will appear: under the
  // sections, or where they will be.
  const nameSection = naming?.id === '' && (
    <div className="px-1 pt-1" data-v2-name-section>
      <NameSection
        onCancel={() => setNaming(null)}
        onSave={(name) => {
          const move = naming.move;
          setNaming(null);
          if (move) void sec.addSectionWith(name, move);
          else sec.addSection.mutate(name);
        }}
      />
    </div>
  );

  const { top, dock } = sections({ ...lists, sections: sectionLists });
  const draggingOut = dnd.dragging?.kind === 'project' && inSection.has(dnd.dragging.name);
  const dropBox = (targeted: boolean) =>
    cn('mx-2 mb-1 rounded-lg border border-dashed border-line-strong px-2.5 py-1.5 text-[11.5px] text-faint transition-colors', targeted && 'border-brand-400/60 bg-brand-400/10 text-brand-300');
  const section = (s: Slot) => {
    if (s === 'pinned')
      return (
        <div key={s} data-v2-pinned>
          <ListHead extra={reviewAll()}>{t('v2.sidebar.pinned')}</ListHead>
          {lists.pinned.map((p) => lead(p, pinDrag(p.project.name), stepPin(p.project.name)))}
        </div>
      );
    if (s === 'sections')
      return (
        <div key={s} className="pt-1.5" data-v2-sections>
          {sectionLists.map(({ section: it, list, shown }) => {
            // A search or a filter opens a folded section on what matches.
            const open = !it.collapsed || filtering;
            return (
              // A section dragged over another, its projects included, goes
              // before or after it by which half; a project over its padding
              // goes to its end.
              <div
                key={it.id}
                className="relative pb-1"
                data-v2-section={it.id}
                {...dnd.dropZone((e) => {
                  const what = dnd.draggingRef.current;
                  if (what?.kind === 'section') return { kind: 'sectionOrder', id: it.id, edge: edge(e) };
                  return what ? listEnd(list) : null;
                })}
              >
                {dnd.isTarget({ kind: 'sectionOrder', id: it.id, edge: 'before' }) && dropLine('before', true)}
                <SectionHeader
                  section={open === !it.collapsed ? it : { ...it, collapsed: false }}
                  count={shown.length}
                  dragging={dnd.dragging}
                  targeted={dnd.isTarget({ kind: 'section', id: it.id })}
                  onToggle={() => sec.patchSection.mutate({ id: it.id, req: { collapsed: !it.collapsed } })}
                  onMove={(delta) => sec.moveSectionBy(it, delta)}
                  onRename={() => setNaming({ id: it.id })}
                  onDelete={() => setDeleting(it)}
                  onDragStart={dnd.startDrag({ kind: 'section', id: it.id })}
                  onDragEnd={dnd.finishDrag}
                  dropZone={dnd.dropZone(() => (dnd.draggingRef.current?.kind === 'project' ? { kind: 'section', id: it.id } : null))}
                  moveKeys={moveKeys}
                />
                {open && shown.map((p) => lead(p, sectionDrag(p.project.name, it.id), stepProject(p.project.name)))}
                {open && shown.length === 0 && (
                  <div className={dropBox(dnd.isTarget({ kind: 'list', section: it.id }))} {...dnd.dropZone(() => (dnd.draggingRef.current?.kind === 'project' ? { kind: 'list', section: it.id } : null))}>
                    {t('shell.sidebar.dropHere')}
                  </div>
                )}
                {dnd.isTarget({ kind: 'sectionOrder', id: it.id, edge: 'after' }) && dropLine('after', true)}
              </div>
            );
          })}
          {/* While a project in a section is dragged, somewhere to drop it
              out of any, whether or not the list of those shows. */}
          {draggingOut && (
            <div
              className={dropBox(dnd.isTarget({ kind: 'list', section: null }))}
              {...dnd.dropZone(() => (dnd.draggingRef.current?.kind === 'project' ? { kind: 'list', section: null } : null))}
              data-v2-drop-no-section
            >
              {t('v2.sidebar.dropNoSection')}
            </div>
          )}
          {nameSection}
        </div>
      );
    if (s === 'rest')
      return (
        <div key={s} data-v2-rest>
          <ListHead
            extra={
              <>
                {reviewAll()}
                {newSection}
              </>
            }
          >
            {t('shell.sidebar.projects')}
          </ListHead>
          {!top.includes('sections') && nameSection}
          {lists.rest.map((p) => lead(p, sectionDrag(p.project.name, null), stepProject(p.project.name)))}
        </div>
      );
    if (s === 'idle')
      return (
        <div key={s} ref={idleRef} data-v2-idle={idleOnly ? 'only' : idleOpen ? 'open' : 'closed'}>
          {idleOnly ? (
            <ListHead
              extra={
                <>
                  {reviewAll()}
                  {newSection}
                </>
              }
            >
              {t('v2.sidebar.idle')}
              <span className="ml-1.5 font-normal tabular-nums normal-case tracking-normal text-subtle">{lists.idle.length}</span>
            </ListHead>
          ) : (
            <button
              type="button"
              aria-expanded={idleOpen}
              onClick={() => setIdleOpened(!idleOpened)}
              className="flex w-full items-center gap-1.5 rounded-md border border-dashed border-line px-2.5 py-1.5 text-left text-[12px] text-secondary hover:border-line-strong hover:bg-surface hover:text-title"
              data-v2-idle-toggle
            >
              <ChevronRight className={cn('size-3.5 shrink-0 transition-transform', idleOpen && 'rotate-90')} />
              <span className="font-medium">{t('v2.sidebar.idle')}</span>
              <span className="tabular-nums text-subtle">· {lists.idle.length}</span>
              <span className="min-w-0 truncate text-subtle">· {t('v2.sidebar.idleHint')}</span>
            </button>
          )}
          {idleOnly && nameSection}
          {idleOpen && <div className={cn(!idleOnly && 'opacity-80')}>{lists.idle.map((p) => lead(p, sectionDrag(p.project.name, null), stepProject(p.project.name)))}</div>}
        </div>
      );
    return (
      <div key={s} className="mt-2 opacity-70 first:mt-0" data-v2-hidden={hiddenOpen ? 'open' : 'closed'}>
        <button
          type="button"
          aria-expanded={hiddenOpen}
          onClick={() => setHiddenOpen(!kept.hiddenOpen)}
          className="flex w-full items-center gap-1.5 rounded-md px-2.5 py-1 text-left text-[11.5px] text-subtle hover:bg-surface hover:text-secondary"
          data-v2-hidden-toggle
        >
          <ChevronRight className={cn('size-3 shrink-0 transition-transform', hiddenOpen && 'rotate-90')} />
          <EyeOff className="size-3 shrink-0" />
          <span>{t('v2.sidebar.hidden')}</span>
          <span className="tabular-nums">· {lists.hidden.length}</span>
        </button>
        {hiddenOpen && lists.hidden.map((p) => lead(p))}
      </div>
    );
  };

  return (
    <aside className="flex w-[288px] shrink-0 flex-col border-r border-line bg-rail backdrop-blur-xl" data-sidebar-v2>
      <div className={cn('flex h-14 shrink-0 items-center gap-2 px-4', nightly && 'nightly-sky')} data-nightly={nightly || undefined}>
        <Logo className="size-7" />
        <span className="text-[14px] font-semibold tracking-tight text-title">AgentBox</span>
        {nightly && (
          <Tip label={t('shell.sidebar.nightlyTip', { version: info.data?.version ? `, ${info.data.version}` : '' })}>
            <span className="nightly-badge inline-flex shrink-0 items-center rounded-full p-1" aria-label={t('shell.sidebar.nightlyTip', { version: info.data?.version ? `, ${info.data.version}` : '' })}>
              <MoonStar className="size-3" aria-hidden />
            </span>
          </Tip>
        )}
        <span className="ml-auto" />
        <HelpMenu />
        <Tip label={t('shell.home.addProject')}>
          <button type="button" className="flex shrink-0 items-center rounded-md p-1.5 text-subtle transition hover:bg-surface-strong hover:text-primary" onClick={onAddProject} aria-label={t('shell.home.addProject')} data-v2-add-project>
            <FolderPlus className="size-4" />
          </button>
        </Tip>
      </div>

      {/* Never sideways: what's too long in it truncates (each row's flex children are min-w-0). */}
      {/* Over no row or section that takes it, a drag lands nowhere, and the indicator says so. */}
      <nav
        className="flex min-h-0 flex-1 flex-col overflow-y-auto overflow-x-hidden px-2 pb-3 pt-1 [&>*]:shrink-0"
        aria-label={t('v2.sidebar.chats')}
        onDragOver={(e: DragEvent) => !e.isDefaultPrevented() && dnd.draggingRef.current && dnd.aim(null)}
        onDragLeave={(e: DragEvent) => !e.currentTarget.contains(e.relatedTarget as Node | null) && dnd.aim(null)}
      >
        <NewTaskRow />
        <HomeRow active={view.kind === 'homeChat' || view.kind === 'home'} onClick={() => onSelect({ kind: 'homeChat' })} />
        <FilterBar filters={filters} onFilters={onFilters} projects={projects.data ?? []} rows={rows} />
        {projects.data?.length === 0 && (
          <button className="mx-2 mt-1 flex w-[calc(100%-1rem)] items-center gap-2 rounded-lg border border-dashed border-line-strong px-3 py-3 text-left text-[13px] text-subtle hover:border-line-heavy hover:text-tertiary" onClick={onAddProject}>
            <FolderPlus className="size-4" />
            {t('shell.sidebar.addFirst')}
          </button>
        )}
        {loaded && top.map(section)}
        {loaded && nothing && <div className="px-2.5 py-1.5 text-[11.5px] text-subtle">{t('v2.sidebar.nothingMatches')}</div>}
        {/* Idle and Hidden dock at the bottom, just above the foot, the free space above them. */}
        {loaded && dock.length > 0 && (
          <div className="mt-auto pt-3" data-v2-dock>
            {dock.map(section)}
          </div>
        )}
        <p className="sr-only" aria-live="polite">
          {sec.announced}
        </p>
      </nav>
      {naming && naming.id !== '' && (
        <RenameSection
          section={sec.sections.data?.find((it) => it.id === naming.id)}
          onClose={() => setNaming(null)}
          onSave={(name) => {
            setNaming(null);
            sec.patchSection.mutate({ id: naming.id, req: { name } });
          }}
        />
      )}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={deleting ? t('shell.sidebar.deleteTitle', { name: deleting.name }) : t('shell.sidebar.deleteTitleNone')}
        description={describeDelete(sec.lists, deleting?.id)}
        confirmLabel={t('shell.sidebar.deleteSection')}
        destructive
        onConfirm={() => sec.removeSection(deleting!.id)}
      />

      {/* The foot is one row of icons, each named by its tooltip: Settings,
          which opens straight away, Past agents, Media and Jobs, the places
          across every project, the one open highlighted, and the update check.
          Home, the chat and its Status tab, heads the list above; which
          AgentBox this window drives is under Settings → General. */}
      <div className="flex items-center gap-1 border-t border-line p-2" data-v2-foot>
        {(
          [
            { kind: 'settings', icon: Settings, label: t('common.settings'), alert: setup.data && !setup.data.ready },
            { kind: 'work', icon: Briefcase, label: t('work.title'), alert: false },
            { kind: 'media', icon: Images, label: t('shell.nav.media'), alert: false },
            { kind: 'jobs', icon: ListChecks, label: t('shell.nav.jobs'), alert: false },
          ] as const
        ).map(({ kind, icon: Icon, label, alert }) => {
          const active = view.kind === kind;
          return (
            <Tip key={kind} label={label}>
              <button
                type="button"
                aria-label={label}
                aria-current={active ? 'page' : undefined}
                onClick={() => onSelect({ kind })}
                className={cn(
                  'relative flex size-8 shrink-0 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50',
                  active && 'bg-surface-raised text-title',
                )}
                data-v2-foot-item={kind}
              >
                <Icon className={cn('size-4', active ? 'text-brand-300' : 'text-subtle')} />
                {alert && <span className="absolute right-1 top-1 size-1.5 rounded-full bg-amber-400" title={t('shell.sidebar.needsAttention')} aria-label={t('shell.sidebar.needsAttention').toLowerCase()} />}
              </button>
            </Tip>
          );
        })}
        <UpdateCheck />
      </div>
    </aside>
  );
}

function ListHead({ children, extra }: { children: ReactNode; extra?: ReactNode }) {
  return (
    <div className="flex h-7 items-center px-2.5 pt-2">
      <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{children}</span>
      {extra}
    </div>
  );
}

// FilterBar is the search box and the filter button beside it, and under
// them a chip for each filter that's on, each removable, with Reset.
function FilterBar({ filters, onFilters, projects, rows }: { filters: Filters; onFilters: (f: Filters) => void; projects: T.Project[]; rows: Row[] }) {
  const t = useT();
  const on = filterCount(filters);
  const counts = countSt(rows.map((r) => r.st));
  const chips: { key: string; label: string; st?: AgentSt; remove: () => void }[] = [
    ...filters.st.map((st) => ({ key: `st:${st}`, label: t(stLabel[st]), st, remove: () => onFilters({ ...filters, st: filters.st.filter((x) => x !== st) }) })),
    ...filters.projects.map((name) => ({
      key: `p:${name}`,
      label: projectLabel(name, projects),
      remove: () => onFilters({ ...filters, projects: filters.projects.filter((x) => x !== name) }),
    })),
  ];
  return (
    <div className="shrink-0 px-1 pb-0.5 pt-2" data-v2-filters>
      <div className="flex items-center gap-1.5">
        <label className="flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-lg border border-line bg-surface-faint px-2 focus-within:border-line-heavy">
          <Search className="size-3.5 shrink-0 text-faint" />
          <input
            value={filters.q}
            onChange={(e) => onFilters({ ...filters, q: e.target.value })}
            onKeyDown={(e) => e.key === 'Escape' && filters.q && (e.stopPropagation(), onFilters({ ...filters, q: '' }))}
            placeholder={t('v2.filters.search')}
            aria-label={t('v2.filters.search')}
            className="min-w-0 flex-1 bg-transparent text-[12.5px] text-primary outline-none placeholder:text-faint"
            data-v2-search
          />
          {filters.q && (
            <button type="button" aria-label={t('v2.filters.clearSearch')} onClick={() => onFilters({ ...filters, q: '' })} className="text-faint hover:text-title">
              <X className="size-3.5" />
            </button>
          )}
        </label>
        <Popover>
          <Tip label={t('v2.filters.button')}>
          <PopoverTrigger asChild>
            <button
              type="button"
              aria-label={t('v2.filters.button')}
              className={cn(
                'relative flex size-8 shrink-0 items-center justify-center rounded-lg border transition',
                on ? 'border-line-heavy bg-surface-strong text-title' : 'border-line bg-surface-faint text-subtle hover:text-title',
              )}
              data-v2-filter-button
            >
              <ListFilter className="size-4" />
              {on > 0 && <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-surface-raised px-1 text-[9.5px] font-semibold text-title ring-1 ring-line-strong">{on}</span>}
            </button>
          </PopoverTrigger>
          </Tip>
          <PopoverContent align="start" className="grid w-[300px] gap-3 p-3" data-v2-filter-popover>
            <FilterGroup title={t('v2.filters.state')}>
              {liveOrder.map((st) => (
                <Chip key={st} on={filters.st.includes(st)} onClick={() => onFilters({ ...filters, st: toggle(filters.st, st) })} data={`st:${st}`}>
                  <StMark st={st} />
                  {t(stLabel[st])}
                  <span className="tabular-nums text-faint">{counts[st]}</span>
                </Chip>
              ))}
            </FilterGroup>
            {projects.length > 1 && (
              <FilterGroup title={t('v2.filters.project')}>
                {projects.map((p) => (
                  <Chip key={p.name} on={filters.projects.includes(p.name)} onClick={() => onFilters({ ...filters, projects: toggle(filters.projects, p.name) })} data={`p:${p.name}`}>
                    <span className="max-w-[180px] truncate">{projectLabel(p)}</span>
                  </Chip>
                ))}
              </FilterGroup>
            )}
          </PopoverContent>
        </Popover>
      </div>
      {chips.length > 0 && (
        <div className="mt-1.5 flex flex-wrap items-center gap-1">
          {chips.map((c) => (
            <span key={c.key} className="inline-flex max-w-full items-center gap-1 rounded-full border border-line bg-surface px-2 py-0.5 text-[11px] text-secondary" data-v2-chip={c.key}>
              {c.st && <StMark st={c.st} />}
              <span className="min-w-0 truncate">{c.label}</span>
              <button type="button" aria-label={t('v2.filters.remove', { name: c.label })} onClick={c.remove} className="-mr-0.5 text-faint hover:text-title">
                <X className="size-3" />
              </button>
            </span>
          ))}
          <button type="button" onClick={() => onFilters({ ...filters, st: [], projects: [] })} className="px-1 text-[11px] text-subtle hover:text-title" data-v2-reset>
            {t('v2.filters.reset')}
          </button>
        </div>
      )}
    </div>
  );
}

function FilterGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <div className="pb-1.5 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{title}</div>
      <div className="flex flex-wrap gap-1">{children}</div>
    </div>
  );
}

function Chip({ on, onClick, data, children }: { on: boolean; onClick: () => void; data: string; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={cn('inline-flex min-w-0 items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11.5px]', on ? 'border-line-heavy bg-surface-strong text-title' : 'border-line text-secondary hover:bg-surface')}
      data-v2-chip-option={data}
    >
      {on && <Check className="size-3 text-secondary" />}
      {children}
    </button>
  );
}

// StMark is an agent's one state mark: neutral for work and review, colour
// only for what needs you.
export function StMark({ st, className }: { st: AgentSt; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn(
        'size-1.5 shrink-0 rounded-full',
        st === 'needs' && 'bg-amber-400',
        st === 'error' && 'bg-rose-400',
        st === 'review' && 'ring-1 ring-inset ring-secondary',
        st === 'working' && 'animate-pulse bg-secondary',
        st === 'done' && 'bg-faint',
        className,
      )}
      data-st={st}
    />
  );
}

// Lead is a chat's row. A click opens it; a right-click (or Ctrl+click on a
// Mac) is for the row's menu only, where it has one, and opens nothing. The
// rest of props is ContextMenuTrigger's, as its asChild child.
// With onMove, Alt+↑/↓ moves it, Classic's keys (Sidebar.tsx).
function Lead({ active, onClick, onMove, children, data, ...props }: { active: boolean; onClick: () => void; onMove?: (delta: -1 | 1) => void; children: ReactNode; data?: Record<string, string> } & Omit<ComponentProps<'div'>, 'onClick'>) {
  return (
    <div
      {...props}
      role="button"
      tabIndex={0}
      aria-current={active ? 'page' : undefined}
      onClick={(e) => {
        if (e.button !== 0 || e.ctrlKey) return;
        onClick();
      }}
      onKeyDown={(e) => {
        if (onMove) moveKeys(onMove)(e);
        if (e.key !== 'Enter' && e.key !== ' ') return;
        e.preventDefault();
        onClick();
      }}
      aria-keyshortcuts={onMove ? 'Alt+ArrowUp Alt+ArrowDown' : undefined}
      className={cn('group relative flex min-w-0 cursor-pointer items-start gap-2 rounded-lg px-2 py-2 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400', active ? 'bg-surface-raised' : 'hover:bg-surface')}
      {...data}
    >
      {children}
    </div>
  );
}

// NewTaskRow opens New task (NewTaskDialog): pick a project, or none for the
// Home chat, and its chat opens to write the task in.
function NewTaskRow() {
  const t = useT();
  return (
    <button
      type="button"
      onClick={openNewTask}
      className="group mb-1 flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[13px] font-medium text-secondary transition-colors hover:bg-surface hover:text-title"
      data-v2-new-task
    >
      <span className="flex size-6 shrink-0 items-center justify-center">
        <SquarePen className="size-4" />
      </span>
      <span className="flex-1">{t('v2.sidebar.newTask')}</span>
      <kbd className="rounded-md bg-surface-raised px-1.5 font-mono text-[10.5px] text-subtle">{shortcut('N')}</kbd>
    </button>
  );
}

// HomeRow is the Home chat, across every project, with its last line.
function HomeRow({ active, onClick }: { active: boolean; onClick: () => void }) {
  const t = useT();
  const chat = useQuery({ queryKey: ['projectChat', A.HomeProject], queryFn: () => api.projectChat(A.HomeProject), refetchInterval: 60_000 });
  const line = leadLine(chat.data?.last);
  const now = useNow(60_000);
  return (
    <Lead active={active} onClick={onClick} data={{ 'data-v2-home': '' }}>
      <span className="flex size-6 shrink-0 items-center justify-center rounded-md bg-surface-strong text-secondary">
        <House className="size-3.5" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-[13px] font-medium text-title">{t('shell.nav.home')}</span>
          {chat.data?.chat === 'running' && <LoaderCircle className="size-3 shrink-0 animate-spin text-subtle" />}
          {chat.data?.last && <span className="ml-auto shrink-0 text-[11px] text-faint">{timeAgo(chat.data.last.at, now)}</span>}
        </span>
        <span className="block truncate text-[11.5px] text-subtle">{line ? (line.mine ? t('v2.sidebar.you', { text: line.text }) : line.text) : t('v2.sidebar.homeHint')}</span>
      </span>
    </Lead>
  );
}

// A row that drags: among the pins, or among the sections. onOver and
// onDrop say whether the row takes the drag at that edge; one it doesn't
// goes on to what's around it (a section's, or nowhere).
type Drag = {
  over?: 'before' | 'after';
  dragged?: boolean;
  onStart: (e: DragEvent) => void;
  onEnd: (e: DragEvent) => void;
  onOver: (edge: 'before' | 'after') => boolean;
  onDrop: (edge: 'before' | 'after') => boolean;
};

// ProjectLead is a project's lead, the main chat, with its icon, its last
// line, its live agents' counts and how many things you haven't seen. A
// gear, on hover and on the project open, opens its Settings; a right-click
// opens its menu, the project header's ⋯ (projectActions). It is lit while
// its chat or one of its agents' is on screen.
// A pinned one drags among the pins, any other among the sections.
function ProjectLead({
  project,
  all,
  approvals,
  pinned,
  hidden,
  view,
  onPin,
  onHide,
  onSelect,
  onNewAgent,
  drag,
  onMove,
  sectionMenu,
}: {
  project: T.Project;
  all: Row[];
  approvals: number;
  pinned: boolean;
  hidden: boolean;
  view: View;
  onPin: () => void;
  onHide: () => void;
  onSelect: (view: View) => void;
  onNewAgent: () => void;
  drag?: Drag;
  onMove?: (delta: -1 | 1) => void;
  // Move to section, in its menu: the sections, the one it's in ('' for
  // none), and what picking one, or New section, does.
  sectionMenu?: { sections: T.Section[]; current: string; onMove: (id: string | null) => void; onNew: () => void };
}) {
  const t = useT();
  const chat = useQuery({ queryKey: ['projectChat', project.name], queryFn: () => api.projectChat(project.name), refetchInterval: 60_000 });
  const now = useNow(60_000);
  const [iconOpen, setIconOpen] = useState(false);
  const [talk, setTalk] = useState(false);
  // Talk to another project, picked: the menu, closing, mustn't take the
  // focus back to the row, outside the picker opening under it, which closed
  // the picker as it opened (ProjectMenu's guard, for its ⋯). New section's
  // name box is the same: the focus going back would blur it closed.
  const talking = useRef(false);
  const leadRef = `${project.name}/${A.LeadName}`;
  // Talk to another project needs another project.
  const others = (useQuery({ queryKey: ['projects'], queryFn: api.projects }).data?.length ?? 0) > 1;
  const line = leadLine(chat.data?.last);
  const counts = countSt(all.map((r) => r.st));
  // The lead at work counts as one working.
  if (leadBusy(chat.data?.chat)) counts.working++;
  const counted = counts.working + counts.review + counts.needs + counts.error > 0;
  const name = projectLabel(project);
  const active = (view.kind === 'project' && view.project === project.name) || (view.kind === 'agent' && view.ref.startsWith(`${project.name}/`));
  const open_ = () => onSelect({ kind: 'project', project: project.name });
  const openSettings = () => {
    open_();
    setPanel({ section: 'general' });
    openPanelOn(leadRef, 'settings');
  };
  const { main, more } = projectActions(t, {
    project,
    others,
    pinned,
    onNewTask: () => {
      open_();
      requestComposerFocus(leadRef);
    },
    onTalk: () => {
      talking.current = true;
      setTimeout(() => setTalk(true), 0);
    },
    onWork: () => onSelect({ kind: 'work', project: project.name }),
    onPin,
    onSettings: openSettings,
    onChangeIcon: () => setIconOpen(true),
    onNewAgent,
  });
  const item = (a: ProjectAction) => [
    a.separatorBefore && <ContextMenuSeparator key={`${a.key}-sep`} />,
    <ContextMenuItem key={a.key} icon={a.icon} onSelect={a.onSelect} data-v2-project-action={a.key}>
      {a.label}
    </ContextMenuItem>,
  ];
  return (
    <div
      className={cn('group/lead relative mb-0.5 rounded-xl transition-colors', drag?.dragged && 'opacity-40', drag?.over === 'before' && 'before:absolute before:inset-x-2 before:-top-px before:h-0.5 before:rounded-full before:bg-brand-400', drag?.over === 'after' && 'after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-brand-400')}
      data-v2-project={project.name}
      draggable={!!drag}
      onDragStart={
        drag &&
        ((e) => {
          e.dataTransfer.effectAllowed = 'move';
          e.dataTransfer.setData('text/plain', project.name);
          drag.onStart(e);
        })
      }
      onDragEnd={drag?.onEnd}
      onDragOver={drag && ((e) => !e.isDefaultPrevented() && drag.onOver(edge(e)) && e.preventDefault())}
      onDrop={drag && ((e) => !e.isDefaultPrevented() && drag.onDrop(edge(e)) && e.preventDefault())}
    >
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <Lead
            active={active}
            onMove={onMove}
            onClick={() => {
              onSelect({ kind: 'project', project: project.name });
              requestComposerFocus(leadRef);
            }}
            data={{ 'data-v2-lead': project.name }}
          >
            <ProjectIcon project={project} className="mt-px" />
            <span className="min-w-0 flex-1">
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate text-[13px] font-medium text-title">{name}</span>
                {chat.data?.chat === 'running' && <LoaderCircle className="size-3 shrink-0 animate-spin text-subtle" />}
                {chat.data?.chat === 'waiting' && <span className="size-1.5 shrink-0 rounded-full bg-amber-400" aria-label={t('shell.sidebar.needsYou')} />}
                {chat.data?.last && <span className={cn('ml-auto shrink-0 text-[11px] text-faint group-hover/lead:hidden', active && 'hidden')}>{timeAgo(chat.data.last.at, now)}</span>}
                <Tip label={t('v2.sidebar.projectSettings')}>
                  <button
                    type="button"
                    aria-label={t('v2.sidebar.projectSettingsOf', { project: name })}
                    onClick={(e) => {
                      e.stopPropagation();
                      openSettings();
                    }}
                    className={cn('-my-1 ml-auto hidden size-5 shrink-0 items-center justify-center rounded text-subtle hover:bg-surface-strong hover:text-title focus-visible:flex group-hover/lead:flex', active && 'flex')}
                    data-v2-project-settings={project.name}
                  >
                    <Settings className="size-3.5" />
                  </button>
                </Tip>
              </span>
              {/* "N to review" ends this line, not the name's: there it left the name a few letters. */}
              <span className="flex min-w-0 items-center gap-2">
                <span className="min-w-0 flex-1 truncate text-[11.5px] text-subtle" data-v2-lead-line>
                  {line ? (line.mine ? t('v2.sidebar.you', { text: line.text }) : line.text) : t('v2.sidebar.leadHint')}
                </span>
                <ReviewCount project={project.name} variant="button" />
              </span>
              {/* Its agents' permission requests wait in this chat too, as cards. */}
              {approvals > 0 && (
                <span className="mt-1 flex items-center gap-1 text-[10.5px] font-medium tabular-nums text-amber-300" data-v2-approvals={approvals}>
                  <ShieldAlert className="size-3 shrink-0" />
                  {t('v2.sidebar.approvals', { count: approvals })}
                </span>
              )}
              {counted && (
                <span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10.5px] tabular-nums" data-v2-counts>
                  {countKeys.map(({ st, key }) =>
                    counts[st] ? (
                      <span key={st} className={cn('inline-flex items-center gap-1', st === 'needs' ? 'text-amber-300' : st === 'error' ? 'text-rose-300' : 'text-subtle')} data-v2-count={st}>
                        <StMark st={st} />
                        {t(key, { count: counts[st] })}
                      </span>
                    ) : null,
                  )}
                </span>
              )}
            </span>
          </Lead>
        </ContextMenuTrigger>
        <ContextMenuContent
          data-v2-project-menu={project.name}
          onCloseAutoFocus={(e) => {
            if (!talking.current) return;
            talking.current = false;
            e.preventDefault();
          }}
        >
          {[
            ...main.flatMap(item),
            <ContextMenuItem key="hide" icon={hidden ? Eye : EyeOff} onSelect={onHide} data-v2-project-action={hidden ? 'unhide' : 'hide'}>
              {t(hidden ? 'v2.sidebar.unhide' : 'v2.sidebar.hide')}
            </ContextMenuItem>,
            sectionMenu && (
              <ContextMenuMore key="section" icon={FolderInput} label={t('v2.sidebar.moveToSection')}>
                {sectionMenu.sections
                  .filter((it) => it.id !== sectionMenu.current)
                  .map((it) => (
                    <ContextMenuItem key={it.id} icon={Folder} onSelect={() => sectionMenu.onMove(it.id)} data-v2-move-to={it.id}>
                      <span className="min-w-0 truncate">{it.name}</span>
                    </ContextMenuItem>
                  ))}
                {sectionMenu.current && (
                  <ContextMenuItem icon={FolderMinus} onSelect={() => sectionMenu.onMove(null)} data-v2-move-to="">
                    {t('v2.sidebar.noSection')}
                  </ContextMenuItem>
                )}
                {(sectionMenu.sections.length > 1 || (sectionMenu.sections.length === 1 && !sectionMenu.current)) && <ContextMenuSeparator />}
                <ContextMenuItem
                  icon={FolderPlus}
                  onSelect={() => {
                    talking.current = true;
                    setTimeout(sectionMenu.onNew, 0);
                  }}
                  data-v2-move-to-new
                >
                  {t('v2.sidebar.newSectionFor')}
                </ContextMenuItem>
              </ContextMenuMore>
            ),
            <ContextMenuMore key="more" label={t('common.more')}>{more.flatMap(item)}</ContextMenuMore>,
          ]}
        </ContextMenuContent>
      </ContextMenu>
      {/* Talk to another project's picker hangs under the row, from a point out
          of the flow: TalkToProject wraps its anchor in an inline-flex span,
          which here, in the row's block, would add a line under the preview. */}
      <span aria-hidden className="pointer-events-none absolute right-2 top-11 size-0">
        <TalkToProject project={project.name} leadRef={leadRef} open={talk} onOpenChange={setTalk} onPicked={open_} anchor={<span className="block size-0" />} />
      </span>
      <ProjectIconDialog project={project} open={iconOpen} onOpenChange={setIconOpen} />
      {/* A group of agents given one task shows as a chip under its lead
          until it is decided. */}
      <div className="ml-[15px] mr-1 flex flex-col gap-px pl-[7px] empty:hidden" data-v2-groups={project.name}>
        <GroupChips project={project.name} activeId={view.kind === 'group' ? view.id : undefined} />
      </div>
    </div>
  );
}

// UpdateCheck is the foot's update button, an icon as T3Code has it: Check
// for updates asks the daemon for the latest release now (GET /v1/update/release,
// which also makes a newer one the offer); once there is one, it updates the
// app in place, or opens the release page where this install can't.
function UpdateCheck() {
  const t = useT();
  const queryClient = useQueryClient();
  const update = useQuery({ queryKey: ['update'], queryFn: api.update, staleTime: Infinity });
  const app = useAppUpdate();
  const [checking, setChecking] = useState(false);
  const current = update.data?.current ?? '';
  const offer = update.data?.available && isUpgrade(update.data.available.version, current) ? update.data.available : undefined;
  const check = async () => {
    setChecking(true);
    try {
      const latest = await api.latestRelease();
      if (isUpgrade(latest.version, current)) await queryClient.invalidateQueries({ queryKey: ['update'] });
      else toast(t('v2.update.latest', { version: current }));
    } catch (err) {
      toast.error(t('v2.update.failed'), { description: errorMessage(err) });
    } finally {
      setChecking(false);
    }
  };
  if (offer) {
    const label = app.label ?? t('v2.update.to', { version: offer.version });
    return (
      <Tip label={app.updating ? label : `${label} · ${updateHint(app.support, offer.version)}`}>
        <button
          type="button"
          aria-label={label}
          disabled={app.updating}
          onClick={() => void app.start(offer.url)}
          className="ml-auto flex size-8 shrink-0 items-center justify-center rounded-lg text-emerald-300 transition hover:bg-emerald-500/15 disabled:cursor-progress"
          data-v2-update={offer.version}
        >
          {app.updating ? <LoaderCircle className="size-4 animate-spin" /> : <CircleArrowUp className="size-4" />}
        </button>
      </Tip>
    );
  }
  const label = checking ? t('v2.update.checking') : t('v2.update.check');
  return (
    <Tip label={label}>
      <button
        type="button"
        aria-label={label}
        disabled={checking}
        onClick={() => void check()}
        className="ml-auto flex size-8 shrink-0 items-center justify-center rounded-lg text-muted transition hover:bg-surface hover:text-primary disabled:cursor-progress"
        data-v2-update-check
      >
        <RefreshCw className={cn('size-4', checking && 'animate-spin')} />
      </button>
    </Tip>
  );
}

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Box, ChevronRight, CircleArrowUp, FolderPlus, FolderTree, GripVertical, House, Images, ListChecks, MessagesSquare, MoonStar, MoreHorizontal, Pencil, Plus, Settings, Trash2 } from 'lucide-react';
import type { ComponentType, DragEvent, KeyboardEvent, ReactNode } from 'react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { t as tNow, useT, type MessageKey } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { projectTone, type StatusTone } from '../lib/agentStatus';
import { isNightly, isUpgrade } from '../lib/nightly';
import { updateHint } from '../lib/appUpdate';
import { arrange, drop, flatten, moveProject, moveSection, place, targetKey, toLayout, type Dragging, type DropTarget, type SidebarList } from '../lib/sidebar';
import { useAppUpdate } from '../lib/useAppUpdate';
import { cn, errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { EnvironmentSwitcher } from './EnvironmentSwitcher';
import { Button } from './ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';
import { Skeleton, skeletonWidths } from './ui/skeleton';
import { Tip } from './ui/tooltip';

// A quieter version of the rail's vocabulary: what to call a project's most
// urgent agent tone, and how to draw it as a single dot.
const toneLabel: Record<StatusTone, MessageKey | null> = { urgent: 'shell.sidebar.needsYou', error: 'shell.sidebar.needsAttention', live: 'shell.sidebar.working', muted: null };
const toneDot: Record<StatusTone, string> = {
  urgent: 'bg-amber-400 animate-pulse',
  error: 'bg-rose-400',
  live: 'bg-sky-400 animate-pulse',
  muted: '',
};

export function Logo({ className }: { className?: string }) {
  return (
    <span className={cn('brand-gradient relative flex size-8 items-center justify-center rounded-[10px] shadow-[0_8px_24px_-8px_rgb(139_92_246/0.8)]', className)}>
      <span className="absolute inset-px rounded-[9px] bg-gradient-to-b from-gloss to-transparent" />
      <Box className="relative size-[18px] text-white" strokeWidth={2.2} />
    </span>
  );
}

export function Sidebar({
  view,
  onSelect,
  onAddProject,
  onNewAgent,
}: {
  view: View;
  onSelect: (view: View) => void;
  onAddProject: () => void;
  onNewAgent: (project: string) => void;
}) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const sections = useQuery({ queryKey: ['sections'], queryFn: api.sections });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: api.jobs });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });
  // Pushed by the daemon whenever it changes (EventUpdate); read once here.
  const update = useQuery({ queryKey: ['update'], queryFn: api.update, staleTime: Infinity });
  const appUpdate = useAppUpdate();
  // The app's own version says whether it is a nightly; the daemon's is the
  // fallback, for the web app, which has no build of its own to ask.
  const info = useQuery({ queryKey: ['app-info'], queryFn: () => window.agentbox.info(), staleTime: Infinity });
  const nightly = isNightly(info.data?.version) || !!update.data?.nightly;
  const running = jobs.data?.filter((j) => j.status === 'running').length ?? 0;
  const queryClient = useQueryClient();

  // A move on its way to the daemon: the layout it sent, shown until the
  // last move in flight has its answer. Until then a refetch (the daemon
  // announces every reorder, and a move can be dropped before the one before
  // it lands) would answer with an older order, and the rows would jump back.
  const [pending, setPending] = useState<T.ProjectLayout | null>(null);
  const inFlight = useRef(0);

  // The sidebar's order, as the daemon last said it, or as the move on its
  // way asked: the sections in theirs, then the projects in none. Every move
  // below is a function of this. No sections before the projects are in
  // either: each would show as empty.
  const lists = useMemo(() => (projects.data ? arrange(projects.data, sections.data ?? [], pending) : []), [projects.data, sections.data, pending]);

  // What is being dragged lives in a ref, which every drag event reads, and
  // in state a moment later, for what it draws: changing the page inside
  // dragstart puts the change in the drag image, and can cancel the drag.
  const draggingRef = useRef<Dragging | null>(null);
  const [dragging, setDragging] = useState<Dragging | null>(null);
  const [target, setTarget] = useState<DropTarget | null>(null);
  const aimed = useRef<DropTarget | null>(null); // the target, as of the last dragover
  const [naming, setNaming] = useState<string | null>(null); // a section being named: its id, or '' for a new one
  const [deleting, setDeleting] = useState<T.Section | null>(null);
  // What a keyboard move did, for a screen reader: the sidebar rearranging
  // itself is the whole of the feedback otherwise.
  const [announced, setAnnounced] = useState('');

  // A reorder sends the whole layout, and shows it before the daemon answers.
  // Reorders go one at a time (the scope), so the daemon stores the last one
  // last, and only the last answer is written: an earlier one is an order
  // already moved on from.
  const reorder = useMutation({
    scope: { id: 'project-layout' },
    mutationFn: (layout: T.ProjectLayout) => api.setProjectLayout(layout),
    onError: (err) => toast.error(errorMessage(err)),
    onSettled: (ordered, err, layout) => {
      if (--inFlight.current > 0) return;
      // A refetch started before the daemon stored this would answer with
      // the order before it.
      void queryClient.cancelQueries({ queryKey: ['projects'] });
      void queryClient.cancelQueries({ queryKey: ['sections'] });
      if (err || !ordered) {
        void queryClient.invalidateQueries({ queryKey: ['projects'] });
        void queryClient.invalidateQueries({ queryKey: ['sections'] });
      } else {
        queryClient.setQueryData<T.Project[]>(['projects'], ordered);
        queryClient.setQueryData<T.Section[]>(['sections'], (current) => current && flatten(arrange([], current, layout)).sections);
      }
      setPending(null);
    },
  });

  // save sends a move, and shows it at once: the cache is written with
  // exactly what the daemon will write, for every other view that lists the
  // projects, and the sidebar keeps it on screen until the answer.
  const save = (next: SidebarList[], then?: () => void) => {
    const layout = toLayout(next);
    inFlight.current++;
    setPending(layout);
    void queryClient.cancelQueries({ queryKey: ['projects'] });
    void queryClient.cancelQueries({ queryKey: ['sections'] });
    const { projects: ordered, sections: order } = flatten(next);
    queryClient.setQueryData<T.Project[]>(['projects'], ordered);
    queryClient.setQueryData<T.Section[]>(['sections'], order);
    reorder.mutate(layout, { onSuccess: then });
  };

  const patchSection = useMutation({
    mutationFn: ({ id, req }: { id: string; req: T.UpdateSectionRequest }) => api.updateSection(id, req),
    onMutate: ({ id, req }) =>
      queryClient.setQueryData<T.Section[]>(['sections'], (current) => current?.map((s) => (s.id === id ? { ...s, ...req } : s))),
    onError: async (err) => {
      toast.error(errorMessage(err));
      await queryClient.invalidateQueries({ queryKey: ['sections'] });
    },
  });

  const addSection = useMutation({
    mutationFn: (name: string) => api.addSection(name),
    onSuccess: async () => queryClient.invalidateQueries({ queryKey: ['sections'] }),
    onError: (err) => toast.error(errorMessage(err)),
  });

  // Applying a move: say what happened, for anyone not watching, and unfold
  // the section it landed in, so nothing a move does is invisible. The unfold
  // waits for the reorder rather than going with it: they are two writes to
  // the same database, and sending them together is asking one to lose.
  const apply = (next: SidebarList[] | null, moved?: string) => {
    if (!next) return;
    const landed = moved ? next.find((l) => l.projects.some((p) => p.name === moved)) : undefined;
    if (moved) setAnnounced(place(next, moved));
    save(next, () => {
      if (landed?.section?.collapsed) patchSection.mutate({ id: landed.section.id, req: { collapsed: false } });
    });
  };

  // Moving a section among the sections, which is the other half of what a
  // move can be and says where it landed the same way.
  const moveSectionBy = (section: T.Section, delta: -1 | 1) => {
    const next = moveSection(lists, section.id, delta);
    if (!next) return;
    const sections = next.filter((l) => l.section);
    setAnnounced(tNow('shell.sidebar.sectionPlace', { name: section.name, n: sections.findIndex((l) => l.section!.id === section.id) + 1, total: sections.length }));
    save(next);
  };

  const startDrag = (what: Dragging) => (e: DragEvent) => {
    draggingRef.current = what;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', what.kind === 'project' ? what.name : what.id);
    setTimeout(() => draggingRef.current === what && setDragging(what));
  };

  const endDrag = useCallback(() => {
    draggingRef.current = null;
    aimed.current = null;
    setDragging(null);
    setTarget(null);
  }, []);

  // A drag whose row went away mid-drag (its section folded, the list
  // refetched without it) never hears its dragend; the first mouse move with
  // no button down says the drag is over all the same.
  useEffect(() => {
    if (!dragging) return;
    const over = (e: MouseEvent) => e.buttons === 0 && endDrag();
    window.addEventListener('mousemove', over);
    return () => window.removeEventListener('mousemove', over);
  }, [dragging, endDrag]);

  // The indicator: where a drop would land, or nothing where it would change
  // nothing (a project over itself), so what it shows is what a drop does.
  const aim = (where: DropTarget | null) => {
    const what = draggingRef.current;
    const useful = what && where && drop(lists, what, where) ? where : null;
    aimed.current = useful;
    setTarget((current) => (current && useful && targetKey(current) === targetKey(useful) ? current : useful));
  };

  // A drop goes where the drop event says, not where the last dragover did:
  // dragover comes at most every few dozen milliseconds, so a quick drag
  // lets go somewhere it hasn't reported yet.
  const commitDrop = (where: DropTarget | null) => {
    const what = draggingRef.current;
    endDrag();
    if (what && where) apply(drop(lists, what, where), what.kind === 'project' ? what.name : undefined);
  };

  // A drag that ends saying it moved something, with no drop event, was
  // dropped where the indicator last was: Chromium on X11 can let go of a
  // quick drag before telling the page, which would otherwise see the row
  // jump back. Dropped elsewhere (outside the window, or Escape), it says
  // none; one that did drop has ended already, so there is nothing left.
  const finishDrag = (e: DragEvent) => {
    if (e.dataTransfer.dropEffect !== 'none' && draggingRef.current && aimed.current) commitDrop(aimed.current);
    else endDrag();
  };

  // dropZone makes an element a drop target. where says what a drop at the
  // pointer would be, or null for a drag this element isn't a target for,
  // which then goes on to the element around it: a section dragged over a
  // project is a drag over the section that project is in. The innermost
  // target that answers decides.
  const dropZone = (where: (e: DragEvent) => DropTarget | null) => ({
    onDragOver: (e: DragEvent) => {
      if (e.isDefaultPrevented()) return;
      const at = where(e);
      if (!at) return;
      e.preventDefault();
      aim(at);
    },
    onDrop: (e: DragEvent) => {
      if (e.isDefaultPrevented()) return;
      const at = where(e);
      if (!at) return;
      e.preventDefault();
      commitDrop(at);
    },
  });
  // A project over a list but not over any of its rows (the space between
  // two sections) goes to that list's end, or onto the section's header when
  // there are no rows to go after.
  const listEnd = (list: SidebarList): DropTarget => {
    const last = list.section?.collapsed ? undefined : list.projects.at(-1);
    if (last) return { kind: 'project', name: last.name, edge: 'after' };
    return list.section ? { kind: 'section', id: list.section.id } : { kind: 'list', section: null };
  };
  const edge = (e: DragEvent): 'before' | 'after' => {
    const box = e.currentTarget.getBoundingClientRect();
    return e.clientY < box.top + box.height / 2 ? 'before' : 'after';
  };

  // Below every list, the space left in the sidebar is the end of it: a
  // project lands at the bottom of the projects in no section, a section
  // after the last section. Anywhere else no target took (the gap between
  // two sections), there is nothing to drop on, and the indicator says so.
  const listsEnd = useRef<HTMLDivElement>(null);
  const belowLists = (e: DragEvent): DropTarget | null => {
    const what = draggingRef.current;
    const end = listsEnd.current?.getBoundingClientRect().bottom ?? Infinity;
    if (!what || e.clientY < end) return null;
    if (what.kind === 'project') return listEnd(lists.at(-1)!);
    const last = lists.filter((l) => l.section).at(-1)?.section;
    return last ? { kind: 'sectionOrder', id: last.id, edge: 'after' } : null;
  };

  // Alt with an arrow moves the focused row, which is the keyboard's whole
  // vocabulary here: down past the end of a list is the top of the next one,
  // so the same two keys reorder a list and move a project between sections.
  const moveKeys = (move: (delta: -1 | 1) => void) => (e: KeyboardEvent) => {
    if (!e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown')) return;
    e.preventDefault();
    move(e.key === 'ArrowUp' ? -1 : 1);
  };

  // Drawn over the rows rather than between them: a line that took room would
  // push the row under the pointer away, and the next dragover would aim
  // somewhere else.
  const dropLine = (edge: 'before' | 'after', gap = false) => (
    <div className={cn('pointer-events-none absolute inset-x-2 z-10 h-0.5 rounded-full bg-brand-400', edge === 'before' ? (gap ? '-top-[3px]' : '-top-px') : gap ? 'bottom-px' : '-bottom-px')} />
  );
  const isTarget = (where: DropTarget) => target !== null && targetKey(target) === targetKey(where);

  const projectRow = (project: T.Project) => {
    const mine = agents.data?.filter((agent) => agent.project === project.name) ?? [];
    const tone = projectTone(mine);
    const dragged = dragging?.kind === 'project' && dragging.name === project.name;
    return (
      <div key={project.name} className="relative" {...dropZone((e) => (draggingRef.current?.kind === 'project' ? { kind: 'project', name: project.name, edge: edge(e) } : null))}>
        {isTarget({ kind: 'project', name: project.name, edge: 'before' }) && dropLine('before')}
        <div
          className={cn(
            'group flex items-center rounded-lg pr-1 transition-colors hover:bg-surface-faint',
            view.kind === 'project' && view.project === project.name && 'bg-surface',
            dragged && 'opacity-40',
          )}
          draggable
          onDragStart={startDrag({ kind: 'project', name: project.name })}
          onDragEnd={finishDrag}
        >
          <button
            data-project={project.name}
            className="flex min-w-0 flex-1 items-center gap-2 py-1.5 pl-2.5 text-left text-[13px] font-medium text-tertiary hover:text-title"
            onClick={() => onSelect({ kind: 'project', project: project.name })}
            onKeyDown={moveKeys((delta) => apply(moveProject(lists, project.name, delta), project.name))}
            aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
          >
            <span className="truncate">{projectLabel(project)}</span>
            {tone && toneLabel[tone] && (
              <Tip label={t(toneLabel[tone])}>
                <span className={cn('size-1.5 shrink-0 rounded-full', toneDot[tone])} aria-label={t(toneLabel[tone])} />
              </Tip>
            )}
            {agents.data ? (
              <span className="ml-auto rounded-full bg-surface-raised px-1.5 text-[10.5px] tabular-nums text-subtle">{mine.length}</span>
            ) : (
              <Skeleton className="ml-auto h-3.5 w-5 rounded-full" />
            )}
          </button>
          <Tip label={t('shell.sidebar.newAgentIn', { project: projectLabel(project) })}>
            <button
              aria-label={t('shell.sidebar.newAgentIn', { project: projectLabel(project) })}
              className="ml-1 rounded-md p-1 text-subtle opacity-0 transition hover:bg-surface-strong hover:text-primary focus-visible:opacity-100 group-hover:opacity-100"
              onClick={() => onNewAgent(project.name)}
            >
              <Plus className="size-3.5" />
            </button>
          </Tip>
        </div>
        {isTarget({ kind: 'project', name: project.name, edge: 'after' }) && dropLine('after')}
      </div>
    );
  };

  // What an empty list offers instead of nothing at all. A list with
  // projects needs none: below its last one is after its last one. An empty
  // section says what it is for; the projects in no section are a list with
  // no heading, so an empty one has nothing to say and shows nothing until
  // something is dragged, at the bottom, where it moves nothing else.
  const listTail = (list: SidebarList) => {
    const id = list.section?.id ?? null;
    if (list.projects.length > 0 || (!list.section && dragging?.kind !== 'project')) return null;
    return (
      <div
        className={cn(
          'mx-2 rounded-lg border border-dashed border-line-strong px-2.5 py-1.5 text-[11.5px] text-faint transition-colors',
          isTarget({ kind: 'list', section: id }) && 'border-brand-400/60 bg-brand-400/10 text-brand-300',
        )}
        {...dropZone(() => (draggingRef.current?.kind === 'project' ? { kind: 'list', section: id } : null))}
      >
        {list.section && t('shell.sidebar.dropHere')}
      </div>
    );
  };

  return (
    <aside className="flex w-[272px] shrink-0 flex-col border-r border-line bg-rail backdrop-blur-xl">
      <div className={cn('flex h-14 shrink-0 items-center px-4', nightly && 'nightly-sky')} data-nightly={nightly || undefined}>
        <button className="flex items-center gap-2.5 rounded-lg focus-visible:outline-none" aria-label={t('shell.sidebar.home')} onClick={() => onSelect({ kind: 'home' })}>
          <Logo />
          <span className="text-[15px] font-semibold tracking-tight text-title">AgentBox</span>
        </button>
        {nightly && (
          <Tip label={t('shell.sidebar.nightlyTip', { version: info.data?.version ? `, ${info.data.version}` : '' })}>
            <span className="nightly-badge ml-2 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10.5px] font-medium">
              <MoonStar className="size-3" aria-hidden />
              {t('shell.sidebar.nightly')}
            </span>
          </Tip>
        )}
      </div>

      <div className="px-3 pb-2">
        <EnvironmentSwitcher />
      </div>

      <div className="grid gap-0.5 px-2 pb-1">
        <NavItem icon={House} active={view.kind === 'home'} onClick={() => onSelect({ kind: 'home' })}>
          {t('shell.nav.home')}
        </NavItem>
        <HomeChatItem active={view.kind === 'homeChat'} onClick={() => onSelect({ kind: 'homeChat' })} />
        <MediaNavItem active={view.kind === 'media'} onClick={() => onSelect({ kind: 'media' })} />
      </div>

      <nav
        className="min-h-0 flex-1 overflow-y-auto px-2 pb-3 pt-1"
        aria-label={t('shell.sidebar.projects')}
        onDragOver={(e: DragEvent) => {
          if (e.isDefaultPrevented() || !draggingRef.current) return; // a row took it
          const at = belowLists(e);
          if (at) e.preventDefault();
          aim(at);
        }}
        onDrop={(e: DragEvent) => {
          if (e.isDefaultPrevented()) return;
          e.preventDefault();
          commitDrop(belowLists(e));
        }}
        onDragLeave={(e: DragEvent) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node | null)) aim(null);
        }}
      >
        <div className="flex items-center gap-1 px-2.5 pb-1.5">
          <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{t('shell.sidebar.projects')}</span>
          <Tip label={t('shell.sidebar.newProject')}>
            <button
              aria-label={t('shell.sidebar.newProject')}
              className="ml-auto rounded-md p-1 text-subtle transition hover:bg-surface-strong hover:text-primary"
              onClick={onAddProject}
            >
              <Plus className="size-3.5" />
            </button>
          </Tip>
          <Tip label={t('shell.sidebar.newSection')}>
            <button
              aria-label={t('shell.sidebar.newSection')}
              className="rounded-md p-1 text-subtle transition hover:bg-surface-strong hover:text-primary"
              onClick={() => setNaming('')}
            >
              <FolderTree className="size-3.5" />
            </button>
          </Tip>
        </div>
        {/* Until the first list arrives, rows where the projects will be:
            an empty list, or "Add your first project", would say there are
            none. */}
        {!projects.data && (
          <div className="grid gap-0.5" aria-busy data-projects-loading>
            {skeletonWidths.slice(0, 3).map((width) => (
              <div key={width} className="flex items-center gap-2 py-2 pl-2.5 pr-2">
                <Skeleton className={cn('h-3.5', width)} />
                <Skeleton className="ml-auto h-3.5 w-5 rounded-full" />
              </div>
            ))}
          </div>
        )}
        {projects.data?.length === 0 && (
          <button className="mx-2 mt-1 flex w-[calc(100%-1rem)] items-center gap-2 rounded-lg border border-dashed border-line-strong px-3 py-3 text-left text-[13px] text-subtle hover:border-line-heavy hover:text-tertiary" onClick={onAddProject}>
            <FolderPlus className="size-4" />
            {t('shell.sidebar.addFirst')}
          </button>
        )}

        <div ref={listsEnd}>
          {lists.map((list) =>
            list.section ? (
              // A section dragged over another one, anywhere on it, its
              // projects included, goes before or after it by which half. The
              // space under it is its own (padding, not a margin), so a drop
              // between two sections has somewhere to go.
              <div
                key={list.section.id}
                className="relative pb-1"
                {...dropZone((e) => {
                  const what = draggingRef.current;
                  if (what?.kind === 'section') return { kind: 'sectionOrder', id: list.section!.id, edge: edge(e) };
                  return what ? listEnd(list) : null;
                })}
              >
                {isTarget({ kind: 'sectionOrder', id: list.section.id, edge: 'before' }) && dropLine('before', true)}
                <SectionHeader
                  section={list.section}
                  count={list.projects.length}
                  dragging={dragging}
                  targeted={isTarget({ kind: 'section', id: list.section.id })}
                  onToggle={() => patchSection.mutate({ id: list.section!.id, req: { collapsed: !list.section!.collapsed } })}
                  onMove={(delta) => moveSectionBy(list.section!, delta)}
                  onRename={() => setNaming(list.section!.id)}
                  onDelete={() => setDeleting(list.section)}
                  onDragStart={startDrag({ kind: 'section', id: list.section.id })}
                  onDragEnd={finishDrag}
                  dropZone={dropZone(() => (draggingRef.current?.kind === 'project' ? { kind: 'section', id: list.section!.id } : null))}
                  moveKeys={moveKeys}
                />
                {!list.section.collapsed && (
                  <>
                    {list.projects.map(projectRow)}
                    {listTail(list)}
                  </>
                )}
                {isTarget({ kind: 'sectionOrder', id: list.section.id, edge: 'after' }) && dropLine('after', true)}
              </div>
            ) : (
              <div
                key="loose"
                className={cn(lists.length > 1 && 'mt-1.5 border-t border-line-faint pt-1.5')}
                {...dropZone(() => {
                  if (draggingRef.current?.kind !== 'project') return null;
                  const first = list.projects[0];
                  return first ? { kind: 'project', name: first.name, edge: 'before' } : { kind: 'list', section: null };
                })}
              >
                {naming === '' && (
                  <NameSection
                    onCancel={() => setNaming(null)}
                    onSave={(name) => {
                      setNaming(null);
                      addSection.mutate(name);
                    }}
                  />
                )}
                {list.projects.map(projectRow)}
                {listTail(list)}
              </div>
            ),
          )}
        </div>
        <p className="sr-only" aria-live="polite">
          {announced}
        </p>
      </nav>

      <div className="grid gap-0.5 border-t border-line p-2">
        {update.data?.available && (
          // What the daemon's daily check found (see the README's "Update
          // check"). Clicking it updates the app in place, where this install
          // can (lib/appUpdate.ts), and opens the release page where it can't.
          <NavItem
            icon={CircleArrowUp}
            title={appUpdate.updating ? undefined : updateHint(appUpdate.support, update.data.available.version)}
            disabled={appUpdate.updating}
            onClick={() => void appUpdate.start(update.data!.available!.url)}
          >
            <span className="min-w-0 truncate text-emerald-300" data-update-available={update.data.available.version} aria-live="polite">
              {appUpdate.label ??
                (isUpgrade(update.data.available.version, update.data.current) ? t('shell.sidebar.updateAvailable') : t('shell.sidebar.latestStable'))}
            </span>
          </NavItem>
        )}
        <NavItem icon={ListChecks} active={view.kind === 'jobs'} onClick={() => onSelect({ kind: 'jobs' })}>
          {t('shell.nav.jobs')}
          {running > 0 && <span className="ml-auto rounded-full bg-sky-400/15 px-1.5 text-[10.5px] text-sky-300">{t('shell.sidebar.running', { count: running })}</span>}
        </NavItem>
        <NavItem icon={FolderPlus} onClick={onAddProject}>
          {t('shell.home.addProject')}
        </NavItem>
        <NavItem icon={Settings} active={view.kind === 'settings'} onClick={() => onSelect({ kind: 'settings' })}>
          {t('common.settings')}
          {setup.data && !setup.data.ready && <span className="ml-auto size-2 rounded-full bg-amber-400" aria-label={t('shell.sidebar.needsAttention').toLowerCase()} />}
        </NavItem>
      </div>

      {naming !== null && naming !== '' && (
        <RenameSection
          section={sections.data?.find((s) => s.id === naming)}
          onClose={() => setNaming(null)}
          onSave={(name) => {
            setNaming(null);
            patchSection.mutate({ id: naming, req: { name } });
          }}
        />
      )}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={deleting ? t('shell.sidebar.deleteTitle', { name: deleting.name }) : t('shell.sidebar.deleteTitleNone')}
        description={describeDelete(lists, deleting?.id)}
        confirmLabel={t('shell.sidebar.deleteSection')}
        destructive
        onConfirm={async () => {
          await api.removeSection(deleting!.id);
          await queryClient.invalidateQueries({ queryKey: ['sections'] });
          await queryClient.invalidateQueries({ queryKey: ['projects'] });
        }}
      />
    </aside>
  );
}

// describeDelete says what deleting a section does, which is the one thing
// worth being clear about: it takes the section and keeps every project.
function describeDelete(lists: SidebarList[], id: string | undefined): string {
  const n = lists.find((l) => l.section?.id === id)?.projects.length ?? 0;
  return tNow('shell.sidebar.deleteBody', { count: n });
}

function SectionHeader({
  section,
  count,
  dragging,
  targeted,
  onToggle,
  onMove,
  onRename,
  onDelete,
  onDragStart,
  onDragEnd,
  dropZone,
  moveKeys,
}: {
  section: T.Section;
  count: number;
  dragging: Dragging | null;
  targeted: boolean;
  onToggle: () => void;
  onMove: (delta: -1 | 1) => void;
  onRename: () => void;
  onDelete: () => void;
  onDragStart: (e: DragEvent) => void;
  onDragEnd: (e: DragEvent) => void;
  // A project dropped on the header joins the section, at its top.
  dropZone: { onDragOver: (e: DragEvent) => void; onDrop: (e: DragEvent) => void };
  moveKeys: (move: (delta: -1 | 1) => void) => (e: KeyboardEvent) => void;
}) {
  const t = useT();
  return (
    <div
      className={cn(
        'group/section flex items-center gap-1 rounded-lg pr-1 transition-colors hover:bg-surface-faint',
        targeted && 'bg-brand-400/10 ring-1 ring-brand-400/50',
        dragging?.kind === 'section' && dragging.id === section.id && 'opacity-40',
      )}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      {...dropZone}
    >
      <GripVertical className="size-3 shrink-0 text-ghost opacity-0 transition group-hover/section:opacity-100" />
      <button
        className="flex min-w-0 flex-1 items-center gap-1 py-1 text-left text-[11px] font-semibold uppercase tracking-[0.06em] text-muted hover:text-primary"
        aria-expanded={!section.collapsed}
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.key === 'ArrowLeft' && !section.collapsed) return onToggle();
          if (e.key === 'ArrowRight' && section.collapsed) return onToggle();
          moveKeys(onMove)(e);
        }}
        aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
      >
        <ChevronRight className={cn('size-3 shrink-0 text-faint transition-transform', !section.collapsed && 'rotate-90')} />
        <span className="truncate">{section.name}</span>
        <span className="ml-auto rounded-full px-1 text-[10px] tabular-nums text-faint">{count}</span>
      </button>
      <Menu>
        <MenuTrigger asChild>
          <button
            aria-label={t('shell.sidebar.sectionMenu', { name: section.name })}
            className="rounded-md p-1 text-subtle opacity-0 transition hover:bg-surface-strong hover:text-primary focus-visible:opacity-100 group-hover/section:opacity-100"
          >
            <MoreHorizontal className="size-3.5" />
          </button>
        </MenuTrigger>
        <MenuContent>
          <MenuItem icon={Pencil} onSelect={onRename}>
            {t('common.rename')}
          </MenuItem>
          <MenuItem icon={ChevronRight} onSelect={onToggle}>
            {section.collapsed ? t('shell.sidebar.expand') : t('shell.sidebar.collapse')}
          </MenuItem>
          <MenuItem icon={Trash2} destructive onSelect={onDelete}>
            {t('shell.sidebar.deleteSection')}
          </MenuItem>
        </MenuContent>
      </Menu>
    </div>
  );
}

// NameSection is the row a new section is typed into, where it will appear:
// the section is made when you press Enter, so nothing empty is left behind by
// changing your mind.
function NameSection({ onSave, onCancel }: { onSave: (name: string) => void; onCancel: () => void }) {
  const t = useT();
  return (
    <Input
      autoFocus
      maxLength={40}
      aria-label={t('shell.sidebar.newSectionName')}
      placeholder={t('shell.sidebar.sectionName')}
      className="mb-1 h-8 text-[12px]"
      onBlur={onCancel}
      onKeyDown={(e) => {
        if (e.key === 'Escape') onCancel();
        if (e.key !== 'Enter') return;
        const name = e.currentTarget.value.trim();
        if (name) onSave(name);
        else onCancel();
      }}
    />
  );
}

// RenameSection is the section's name in a dialog, which is where the app puts
// anything it asks for a word.
function RenameSection({ section, onSave, onClose }: { section: T.Section | undefined; onSave: (name: string) => void; onClose: () => void }) {
  const t = useT();
  const [name, setName] = useState(section?.name ?? '');
  if (!section) return null;
  const save = () => {
    const trimmed = name.trim();
    if (trimmed && trimmed !== section.name) onSave(trimmed);
    else onClose();
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{t('shell.sidebar.renameTitle', { name: section.name })}</DialogTitle>
        </DialogHeader>
        <Field label={t('shell.sidebar.sectionName')} htmlFor="section-name">
          <Input
            id="section-name"
            autoFocus
            maxLength={40}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && save()}
          />
        </Field>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={save}>{t('common.rename')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// HomeChatItem opens the Home chat, the user's main chat across projects, and
// shows a dot while it works or waits for an answer, like a project's.
function HomeChatItem({ active, onClick }: { active: boolean; onClick: () => void }) {
  const t = useT();
  const chat = useQuery({ queryKey: ['projectChat', A.HomeProject], queryFn: () => api.projectChat(A.HomeProject) });
  const state = chat.data?.chat;
  const tone: StatusTone = state === 'waiting' ? 'urgent' : state === 'running' ? 'live' : 'muted';
  return (
    <NavItem icon={MessagesSquare} active={active} onClick={onClick}>
      {t('shell.nav.mainChat')}
      {tone !== 'muted' && (
        <Tip label={t(toneLabel[tone]!)}>
          <span className={cn('ml-auto size-1.5 rounded-full', toneDot[tone])} data-home-chat={state} />
        </Tip>
      )}
    </NavItem>
  );
}

// MediaNavItem opens the all-projects Media view, with how many screenshots
// and recordings you were told about and haven't opened.
function MediaNavItem({ active, onClick }: { active: boolean; onClick: () => void }) {
  const t = useT();
  const notices = useQuery({ queryKey: ['notifications'], queryFn: api.notifications });
  const unseen = (notices.data ?? []).filter((n) => n.media && !n.media.removed && !n.seen).length;
  return (
    <NavItem icon={Images} active={active} onClick={onClick}>
      {t('shell.nav.media')}
      {unseen > 0 && (
        <span className="ml-auto rounded-full bg-brand-500/15 px-1.5 text-[11px] tabular-nums text-brand-300 [:root[data-appearance=light]_&]:text-brand-600" data-media-unseen={unseen}>
          {unseen}
        </span>
      )}
    </NavItem>
  );
}

function NavItem({
  icon: Icon,
  active,
  title,
  disabled,
  onClick,
  children,
}: {
  icon: ComponentType<{ className?: string }>;
  active?: boolean;
  title?: string;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      title={title}
      disabled={disabled}
      className={cn(
        'flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-muted transition-colors hover:bg-surface hover:text-primary disabled:cursor-progress',
        active && 'bg-surface-raised text-title',
      )}
    >
      <Icon className="size-4" />
      {children}
    </button>
  );
}

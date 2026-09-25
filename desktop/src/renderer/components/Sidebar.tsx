import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Box, ChevronRight, CircleArrowUp, FolderPlus, FolderTree, GripVertical, House, ListChecks, MoreHorizontal, Pencil, Plus, Settings, Trash2 } from 'lucide-react';
import type { ComponentType, DragEvent, KeyboardEvent, ReactNode } from 'react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { projectTone, type StatusTone } from '../lib/agentStatus';
import { buildLists, drop, flatten, moveProject, moveSection, place, targetKey, toLayout, type Dragging, type DropTarget, type SidebarList } from '../lib/sidebar';
import { cn, errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { EnvironmentSwitcher } from './EnvironmentSwitcher';
import { Button } from './ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';
import { Tip } from './ui/tooltip';

// A quieter version of the rail's vocabulary: what to call a project's most
// urgent agent tone, and how to draw it as a single dot.
const toneLabel: Record<StatusTone, string> = { urgent: 'Needs you', error: 'Needs attention', live: 'Working', muted: '' };
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
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const sections = useQuery({ queryKey: ['sections'], queryFn: api.sections });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: api.jobs });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });
  // Pushed by the daemon whenever it changes (EventUpdate); read once here.
  const update = useQuery({ queryKey: ['update'], queryFn: api.update, staleTime: Infinity });
  const running = jobs.data?.filter((j) => j.status === 'running').length ?? 0;
  const queryClient = useQueryClient();

  // The sidebar's order, as the daemon last said it: the sections in theirs,
  // then the projects in none. Every move below is a function of this.
  const lists = useMemo(() => buildLists(projects.data ?? [], sections.data ?? []), [projects.data, sections.data]);

  const [dragging, setDragging] = useState<Dragging | null>(null);
  const [target, setTarget] = useState<DropTarget | null>(null);
  const [naming, setNaming] = useState<string | null>(null); // a section being named: its id, or '' for a new one
  const [deleting, setDeleting] = useState<T.Section | null>(null);
  // What a keyboard move did, for a screen reader: the sidebar rearranging
  // itself is the whole of the feedback otherwise.
  const [announced, setAnnounced] = useState('');

  // A reorder sends the whole layout, and shows it before the daemon answers:
  // the cache is written with exactly what the daemon will write, so a
  // successful round trip changes nothing on screen.
  const reorder = useMutation({
    mutationFn: (next: SidebarList[]) => api.setProjectLayout(toLayout(next)),
    onMutate: (next: SidebarList[]) => {
      const { projects: ordered, sections: order } = flatten(next);
      queryClient.setQueryData<T.Project[]>(['projects'], ordered);
      queryClient.setQueryData<T.Section[]>(['sections'], order);
    },
    onSuccess: (ordered) => queryClient.setQueryData<T.Project[]>(['projects'], ordered),
    onError: async (err) => {
      toast.error(errorMessage(err));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      await queryClient.invalidateQueries({ queryKey: ['sections'] });
    },
  });

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
    reorder.mutate(next, {
      onSuccess: () => {
        if (landed?.section?.collapsed) patchSection.mutate({ id: landed.section.id, req: { collapsed: false } });
      },
    });
  };

  // Moving a section among the sections, which is the other half of what a
  // move can be and says where it landed the same way.
  const moveSectionBy = (section: T.Section, delta: -1 | 1) => {
    const next = moveSection(lists, section.id, delta);
    if (!next) return;
    const sections = next.filter((l) => l.section);
    setAnnounced(`${section.name} is now section ${sections.findIndex((l) => l.section!.id === section.id) + 1} of ${sections.length}`);
    reorder.mutate(next);
  };

  const endDrag = () => {
    setDragging(null);
    setTarget(null);
  };

  // A drop applies whatever the last dragover decided, which is the same
  // thing the indicator is drawn from: the row under the pointer set both.
  const commitDrop = () => {
    if (dragging && target) apply(drop(lists, dragging, target), dragging.kind === 'project' ? dragging.name : undefined);
    endDrag();
  };

  // Alt with an arrow moves the focused row, which is the keyboard's whole
  // vocabulary here: down past the end of a list is the top of the next one,
  // so the same two keys reorder a list and move a project between sections.
  const moveKeys = (move: (delta: -1 | 1) => void) => (e: KeyboardEvent) => {
    if (!e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown')) return;
    e.preventDefault();
    move(e.key === 'ArrowUp' ? -1 : 1);
  };

  const dropLine = <div className="mx-2 my-px h-0.5 rounded-full bg-brand-400" />;
  const isTarget = (where: DropTarget) => target !== null && targetKey(target) === targetKey(where);

  const projectRow = (project: T.Project) => {
    const mine = agents.data?.filter((agent) => agent.project === project.name) ?? [];
    const tone = projectTone(mine);
    const dragged = dragging?.kind === 'project' && dragging.name === project.name;
    return (
      <div key={project.name}>
        {isTarget({ kind: 'project', name: project.name, edge: 'before' }) && dropLine}
        <div
          className={cn(
            'group flex items-center rounded-lg pr-1 transition-colors hover:bg-surface-faint',
            view.kind === 'project' && view.project === project.name && 'bg-surface',
            dragged && 'opacity-40',
          )}
          draggable
          onDragStart={(e: DragEvent) => {
            setDragging({ kind: 'project', name: project.name });
            e.dataTransfer.effectAllowed = 'move';
            e.dataTransfer.setData('text/plain', project.name);
          }}
          onDragEnd={endDrag}
          onDragOver={(e: DragEvent) => {
            // A section dragged over a project isn't a drop: no indicator,
            // and no preventDefault, so the cursor says so.
            if (dragging?.kind !== 'project') return setTarget(null);
            e.preventDefault();
            const box = e.currentTarget.getBoundingClientRect();
            setTarget({ kind: 'project', name: project.name, edge: e.clientY < box.top + box.height / 2 ? 'before' : 'after' });
          }}
          onDrop={(e: DragEvent) => {
            e.preventDefault();
            commitDrop();
          }}
        >
          <button
            data-project={project.name}
            className="flex min-w-0 flex-1 items-center gap-2 py-1.5 pl-2.5 text-left text-[13px] font-medium text-tertiary hover:text-title"
            onClick={() => onSelect({ kind: 'project', project: project.name })}
            onKeyDown={moveKeys((delta) => apply(moveProject(lists, project.name, delta), project.name))}
            aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
          >
            <span className="truncate">{project.name}</span>
            {tone && (
              <Tip label={toneLabel[tone]}>
                <span className={cn('size-1.5 shrink-0 rounded-full', toneDot[tone])} aria-label={toneLabel[tone]} />
              </Tip>
            )}
            <span className="ml-auto rounded-full bg-surface-raised px-1.5 text-[10.5px] tabular-nums text-subtle">{mine.length}</span>
          </button>
          <Tip label={`New agent in ${project.name}`}>
            <button
              aria-label={`New agent in ${project.name}`}
              className="ml-1 rounded-md p-1 text-subtle opacity-0 transition hover:bg-surface-strong hover:text-primary focus-visible:opacity-100 group-hover:opacity-100"
              onClick={() => onNewAgent(project.name)}
            >
              <Plus className="size-3.5" />
            </button>
          </Tip>
        </div>
        {isTarget({ kind: 'project', name: project.name, edge: 'after' }) && dropLine}
      </div>
    );
  };

  // The tail of a list: what a project dropped below everything lands on, and
  // what an empty section offers instead of nothing at all.
  const listTail = (list: SidebarList) => {
    const id = list.section?.id ?? null;
    const empty = list.projects.length === 0;
    // An empty section says what it is for; the projects in no section are a
    // list with no heading, so an empty one has nothing to say and shows
    // nothing until something is dragged over it.
    if ((!empty || !list.section) && dragging?.kind !== 'project') return null;
    return (
      <div
        className={cn(
          'mx-2 rounded-lg text-[11.5px] text-faint transition-colors',
          empty ? 'border border-dashed border-line-strong px-2.5 py-1.5' : 'h-2',
          isTarget({ kind: 'list', section: id }) && 'border-brand-400/60 bg-brand-400/10 text-brand-300',
        )}
        onDragOver={(e: DragEvent) => {
          if (dragging?.kind !== 'project') return;
          e.preventDefault();
          setTarget({ kind: 'list', section: id });
        }}
        onDrop={(e: DragEvent) => {
          e.preventDefault();
          commitDrop();
        }}
      >
        {empty && list.section && 'Drop a project here'}
      </div>
    );
  };

  return (
    <aside className="flex w-[272px] shrink-0 flex-col border-r border-line bg-rail backdrop-blur-xl">
      <div className="flex h-14 shrink-0 items-center px-4">
        <button className="flex items-center gap-2.5 rounded-lg focus-visible:outline-none" aria-label="AgentBox home" onClick={() => onSelect({ kind: 'home' })}>
          <Logo />
          <span className="text-[15px] font-semibold tracking-tight text-title">AgentBox</span>
        </button>
      </div>

      <div className="px-3 pb-2">
        <EnvironmentSwitcher />
      </div>

      <div className="grid gap-0.5 px-2 pb-1">
        <NavItem icon={House} active={view.kind === 'home'} onClick={() => onSelect({ kind: 'home' })}>
          Home
        </NavItem>
      </div>

      <nav className="min-h-0 flex-1 overflow-y-auto px-2 pb-3 pt-1" aria-label="Projects">
        <div className="flex items-center gap-1 px-2.5 pb-1.5">
          <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">Projects</span>
          <Tip label="New project">
            <button
              aria-label="New project"
              className="ml-auto rounded-md p-1 text-subtle transition hover:bg-surface-strong hover:text-primary"
              onClick={onAddProject}
            >
              <Plus className="size-3.5" />
            </button>
          </Tip>
          <Tip label="New section">
            <button
              aria-label="New section"
              className="rounded-md p-1 text-subtle transition hover:bg-surface-strong hover:text-primary"
              onClick={() => setNaming('')}
            >
              <FolderTree className="size-3.5" />
            </button>
          </Tip>
        </div>
        {projects.data?.length === 0 && (
          <button className="mx-2 mt-1 flex w-[calc(100%-1rem)] items-center gap-2 rounded-lg border border-dashed border-line-strong px-3 py-3 text-left text-[13px] text-subtle hover:border-line-heavy hover:text-tertiary" onClick={onAddProject}>
            <FolderPlus className="size-4" />
            Add your first project
          </button>
        )}

        {lists.map((list) =>
          list.section ? (
            <div key={list.section.id} className="mb-1">
              {isTarget({ kind: 'sectionOrder', id: list.section.id, edge: 'before' }) && dropLine}
              <SectionHeader
                section={list.section}
                count={list.projects.length}
                dragging={dragging}
                targeted={isTarget({ kind: 'section', id: list.section.id })}
                onToggle={() => patchSection.mutate({ id: list.section!.id, req: { collapsed: !list.section!.collapsed } })}
                onMove={(delta) => moveSectionBy(list.section!, delta)}
                onRename={() => setNaming(list.section!.id)}
                onDelete={() => setDeleting(list.section)}
                onDragStart={() => setDragging({ kind: 'section', id: list.section!.id })}
                onDragEnd={endDrag}
                onDragOver={setTarget}
                onDrop={commitDrop}
                moveKeys={moveKeys}
              />
              {!list.section.collapsed && (
                <>
                  {list.projects.map(projectRow)}
                  {listTail(list)}
                </>
              )}
              {isTarget({ kind: 'sectionOrder', id: list.section.id, edge: 'after' }) && dropLine}
            </div>
          ) : (
            <div key="loose" className={cn(lists.length > 1 && 'mt-1.5 border-t border-line-faint pt-1.5')}>
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
        <p className="sr-only" aria-live="polite">
          {announced}
        </p>
      </nav>

      <div className="grid gap-0.5 border-t border-line p-2">
        {update.data?.available && (
          // What the daemon's daily check found (see the README's "Update
          // check"). It links to the release rather than updating anything:
          // how AgentBox was installed decides how it's updated.
          <NavItem icon={CircleArrowUp} onClick={() => void window.agentbox.openExternal(update.data!.available!.url)}>
            <span className="text-emerald-300" data-update-available={update.data.available.version}>
              Update available
            </span>
            <span className="ml-auto rounded-full bg-emerald-400/15 px-1.5 text-[10.5px] text-emerald-300">{update.data.available.version}</span>
          </NavItem>
        )}
        <NavItem icon={ListChecks} active={view.kind === 'jobs'} onClick={() => onSelect({ kind: 'jobs' })}>
          Jobs
          {running > 0 && <span className="ml-auto rounded-full bg-sky-400/15 px-1.5 text-[10.5px] text-sky-300">{running} running</span>}
        </NavItem>
        <NavItem icon={FolderPlus} onClick={onAddProject}>
          Add project
        </NavItem>
        <NavItem icon={Settings} active={view.kind === 'settings'} onClick={() => onSelect({ kind: 'settings' })}>
          Settings
          {setup.data && !setup.data.ready && <span className="ml-auto size-2 rounded-full bg-amber-400" aria-label="needs attention" />}
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
        title={`Delete ${deleting?.name ?? 'the section'}?`}
        description={describeDelete(lists, deleting?.id)}
        confirmLabel="Delete section"
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
  if (n === 0) return 'The section goes. It is empty, so no project is affected.';
  const projects = n === 1 ? 'the project in it stays' : `all ${n} projects in it stay`;
  return `The section goes; ${projects}, back in the list of projects in no section.`;
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
  onDragOver,
  onDrop,
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
  onDragStart: () => void;
  onDragEnd: () => void;
  onDragOver: (where: DropTarget) => void;
  onDrop: () => void;
  moveKeys: (move: (delta: -1 | 1) => void) => (e: KeyboardEvent) => void;
}) {
  // A section takes a project dropped on its header, and swaps places with
  // another section dropped on it. Which of the two is being dragged decides
  // what the header is a target for.
  const over = (e: DragEvent) => {
    if (!dragging) return;
    e.preventDefault();
    if (dragging.kind === 'project') return onDragOver({ kind: 'section', id: section.id });
    if (dragging.id === section.id) return;
    const box = e.currentTarget.getBoundingClientRect();
    onDragOver({ kind: 'sectionOrder', id: section.id, edge: e.clientY < box.top + box.height / 2 ? 'before' : 'after' });
  };

  return (
    <div
      className={cn(
        'group/section flex items-center gap-1 rounded-lg pr-1 transition-colors hover:bg-surface-faint',
        targeted && 'bg-brand-400/10 ring-1 ring-brand-400/50',
        dragging?.kind === 'section' && dragging.id === section.id && 'opacity-40',
      )}
      draggable
      onDragStart={(e: DragEvent) => {
        onDragStart();
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', section.name);
      }}
      onDragEnd={onDragEnd}
      onDragOver={over}
      onDrop={(e: DragEvent) => {
        e.preventDefault();
        onDrop();
      }}
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
            aria-label={`Section ${section.name}`}
            className="rounded-md p-1 text-subtle opacity-0 transition hover:bg-surface-strong hover:text-primary focus-visible:opacity-100 group-hover/section:opacity-100"
          >
            <MoreHorizontal className="size-3.5" />
          </button>
        </MenuTrigger>
        <MenuContent>
          <MenuItem icon={Pencil} onSelect={onRename}>
            Rename
          </MenuItem>
          <MenuItem icon={ChevronRight} onSelect={onToggle}>
            {section.collapsed ? 'Expand' : 'Collapse'}
          </MenuItem>
          <MenuItem icon={Trash2} destructive onSelect={onDelete}>
            Delete section
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
  return (
    <Input
      autoFocus
      maxLength={40}
      aria-label="New section name"
      placeholder="Section name"
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
          <DialogTitle>Rename {section.name}</DialogTitle>
        </DialogHeader>
        <Field label="Section name" htmlFor="section-name">
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
            Cancel
          </Button>
          <Button onClick={save}>Rename</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function NavItem({ icon: Icon, active, onClick, children }: { icon: ComponentType<{ className?: string }>; active?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-muted transition-colors hover:bg-surface hover:text-primary',
        active && 'bg-surface-raised text-title',
      )}
    >
      <Icon className="size-4" />
      {children}
    </button>
  );
}

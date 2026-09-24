import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Brain,
  CircleCheck,
  Coins,
  Ellipsis,
  FileText,
  FlaskConical,
  Gauge,
  GitCompare,
  GitMerge,
  History,
  Link2,
  ListPlus,
  ListTree,
  Paperclip,
  Plus,
  Search as SearchIcon,
  Sparkles,
  TriangleAlert,
  Users,
  X,
} from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { formatTokens } from '../lib/chat';
import { api } from '../lib/api';
import { cn, errorMessage, humanBytes, timeAgo } from '../lib/utils';
import { Markdown } from './chat/Markdown';
import { FilterChip } from './MediaTab';
import { Sparkline } from './Sparkline';
import { Badge, type BadgeVariant } from './ui/badge';
import { Button } from './ui/button';
import { Card, EmptyState, Notice, Panel } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from './ui/menu';
import { Select, SelectOption } from './ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';

// ProjectMemoryPanel is the Memory tab: what a project knows, kept by AgentBox
// rather than by any one agent's conversation (docs/implementation/project-memory.md,
// D72). The project's chat already reads and writes all of this through its
// own MCP tools; this is the same store, for a human looking at it directly.
//
// Five things live here, and they stay five sections rather than one feed,
// because they answer different questions: working memory is what the
// project is doing *right now*, memories are judgements worth keeping,
// events are the raw history nothing decided to keep, search reaches all
// three at once, and reports are what an agent said as it finished.
export function ProjectMemoryPanel({ project, onOpenMedia }: { project: string; onOpenMedia: () => void }) {
  const [section, setSection] = useState<'tasks' | 'memories' | 'search' | 'events' | 'reports' | 'context'>('memories');

  return (
    <div className="mx-auto grid max-w-4xl gap-5 px-4 py-6 md:px-8 md:py-7">
      <WorkingMemoryCard project={project} />

      <Tabs value={section} onValueChange={(value) => setSection(value as typeof section)}>
        <TabsList>
          <TabsTrigger value="tasks">
            <ListTree />
            Tasks
          </TabsTrigger>
          <TabsTrigger value="memories">
            <Sparkles />
            Memories
          </TabsTrigger>
          <TabsTrigger value="search">
            <SearchIcon />
            Search
          </TabsTrigger>
          <TabsTrigger value="events">
            <History />
            Events
          </TabsTrigger>
          <TabsTrigger value="reports">
            <FileText />
            Reports
          </TabsTrigger>
          <TabsTrigger value="context">
            <Gauge />
            Context
          </TabsTrigger>
        </TabsList>

        <TabsContent value="tasks" className="mt-4">
          <TasksSection project={project} />
        </TabsContent>
        <TabsContent value="memories" className="mt-4">
          <MemoriesSection project={project} />
        </TabsContent>
        <TabsContent value="search" className="mt-4">
          <MemorySearchSection project={project} />
        </TabsContent>
        <TabsContent value="events" className="mt-4">
          <MemoryEventsSection project={project} />
        </TabsContent>
        <TabsContent value="reports" className="mt-4">
          <MemoryReportsSection project={project} onOpenMedia={onOpenMedia} />
        </TabsContent>
        <TabsContent value="context" className="mt-4">
          <ContextSection project={project} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

// --- Working memory --------------------------------------------------------

// WorkingMemoryCard is the one document here that is replaced rather than
// added to, so it is edited the way ProjectNotes edits the project's notes:
// a draft kept until you save it, sent as a merge patch. activeAgents isn't a
// field in the form — the daemon keeps it in step with an agent being made
// and retired (memoryevents.go), and a hand edit here would just be
// overwritten by the next one, so it is shown as a fact rather than offered
// as something to type into.
function WorkingMemoryCard({ project }: { project: string }) {
  const queryClient = useQueryClient();
  const working = useQuery({ queryKey: ['memoryWorking', project], queryFn: () => api.memoryWorking(project) });
  const saved = working.data;
  const [draft, setDraft] = useState<{ goal: string; currentTask: string; blockers: string; notes: string } | null>(null);

  const fields = draft ?? {
    goal: saved?.goal ?? '',
    currentTask: saved?.currentTask ?? '',
    blockers: (saved?.blockers ?? []).join('\n'),
    notes: saved?.notes ?? '',
  };
  const dirty =
    draft !== null &&
    (draft.goal !== (saved?.goal ?? '') ||
      draft.currentTask !== (saved?.currentTask ?? '') ||
      draft.blockers !== (saved?.blockers ?? []).join('\n') ||
      draft.notes !== (saved?.notes ?? ''));

  const save = useMutation({
    mutationFn: () =>
      api.setMemoryWorking(project, {
        goal: fields.goal,
        currentTask: fields.currentTask,
        blockers: fields.blockers
          .split('\n')
          .map((line) => line.trim())
          .filter(Boolean),
        notes: fields.notes,
      } satisfies T.WorkingMemoryPatch),
    onSuccess: (updated) => {
      setDraft(null);
      queryClient.setQueryData(['memoryWorking', project], updated);
      toast('Working memory saved');
    },
  });

  return (
    <Card
      title="Working memory"
      icon={Brain}
      description="What the project is doing right now. The only thing here worth correcting by hand — a memory is written down instead of edited."
      action={
        <>
          <Button variant="ghost" size="sm" disabled={!dirty || save.isPending} onClick={() => setDraft(null)}>
            Revert
          </Button>
          <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? 'Saving…' : 'Save'}
          </Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Goal" htmlFor="memory-goal">
          <Input
            id="memory-goal"
            placeholder="What this project is for right now"
            value={fields.goal}
            onChange={(event) => setDraft({ ...fields, goal: event.target.value })}
          />
        </Field>
        <Field label="Current task" htmlFor="memory-task">
          <Input
            id="memory-task"
            placeholder="What's in flight"
            value={fields.currentTask}
            onChange={(event) => setDraft({ ...fields, currentTask: event.target.value })}
          />
        </Field>
      </div>
      <Field label="Blockers" htmlFor="memory-blockers" hint="One per line." >
        <Textarea
          id="memory-blockers"
          className="mt-1.5 min-h-16 font-mono text-[12.5px]"
          placeholder={'Nothing is blocked'}
          value={fields.blockers}
          onChange={(event) => setDraft({ ...fields, blockers: event.target.value })}
        />
      </Field>
      <Field label="Notes" htmlFor="memory-notes">
        <Textarea
          id="memory-notes"
          className="mt-1.5 min-h-16"
          placeholder="Anything else worth keeping in view"
          value={fields.notes}
          onChange={(event) => setDraft({ ...fields, notes: event.target.value })}
        />
      </Field>

      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-line-faint pt-3">
        <span className="flex items-center gap-1.5 text-[11.5px] text-subtle">
          <Users className="size-3.5" />
          Active agents
        </span>
        {(saved?.activeAgents ?? []).length === 0 ? (
          <span className="text-[11.5px] text-faint">none</span>
        ) : (
          (saved?.activeAgents ?? []).map((name) => (
            <Badge key={name} variant="brand">
              {name}
            </Badge>
          ))
        )}
        <span className="ml-auto text-[11px] text-faint">
          {dirty ? 'Unsaved changes' : working.isPending ? 'Loading…' : saved?.updatedAt ? `Saved ${timeAgo(saved.updatedAt)}` : 'Never set'}
        </span>
      </div>
      {save.error && <Notice className="mt-3">{errorMessage(save.error)}</Notice>}
      {working.error && <Notice className="mt-3">{errorMessage(working.error)}</Notice>}
    </Card>
  );
}

// --- Tasks: the plan --------------------------------------------------------

// TasksSection is the plan (tasks.go, D77): a tree by parent_task_id beside a
// few blocking edges, not a general graph — which is why this reads it with
// indentation and a "blocked on" list rather than a node-and-edge diagram.
// GET …/memory/tasks is the whole graph in one call, edges both ways, so one
// query is all a tree needs. Creating a task, changing a status, and linking
// or unlinking a dependency are exactly what the routes let the user do —
// this doesn't invent anything the API can't back, like editing a task that
// belongs to an agent, which is the plan-curation split the daemon enforces
// in taskPatch (internal/daemon/memory.go).
const taskStatuses = [A.TaskBlocked, A.TaskActive, A.TaskOpen, A.TaskDone, A.TaskAbandoned];
const taskStatusLabel: Record<string, string> = {
  [A.TaskOpen]: 'Open',
  [A.TaskActive]: 'Active',
  [A.TaskBlocked]: 'Blocked',
  [A.TaskDone]: 'Done',
  [A.TaskAbandoned]: 'Abandoned',
};
const taskStatusVariant: Record<string, BadgeVariant> = {
  [A.TaskOpen]: 'default',
  [A.TaskActive]: 'info',
  [A.TaskBlocked]: 'warning',
  [A.TaskDone]: 'success',
  [A.TaskAbandoned]: 'danger',
};
const taskOpenStatuses = new Set([A.TaskOpen, A.TaskActive, A.TaskBlocked]);

// taskTree indexes a project's tasks by parent, in the order the API already
// returned them (blocked, active, open, done, abandoned, then newest first) —
// grouping by parent preserves that order, so nothing here re-sorts.
function taskTree(tasks: T.Task[]) {
  const byId = new Map(tasks.map((t) => [t.id, t] as const));
  const childrenOf = new Map<string, T.Task[]>();
  const roots: T.Task[] = [];
  for (const t of tasks) {
    if (t.parentId && byId.has(t.parentId)) {
      childrenOf.set(t.parentId, [...(childrenOf.get(t.parentId) ?? []), t]);
    } else {
      roots.push(t);
    }
  }
  return { byId, childrenOf, roots };
}

// visibleTasks is which tasks show when closed work is hidden: a task stays
// visible if it's still open, or if anything under it is — collapsing a
// finished branch whole rather than leaving its open children orphaned with
// no parent row to indent under.
function visibleTasks(tasks: T.Task[], childrenOf: Map<string, T.Task[]>, showClosed: boolean): Set<string> {
  if (showClosed) return new Set(tasks.map((t) => t.id));
  const visible = new Set<string>();
  const check = (t: T.Task): boolean => {
    const isVisible = taskOpenStatuses.has(t.status) || (childrenOf.get(t.id) ?? []).some(check);
    if (isVisible) visible.add(t.id);
    return isVisible;
  };
  for (const t of tasks) check(t);
  return visible;
}

function TasksSection({ project }: { project: string }) {
  const queryClient = useQueryClient();
  const tasksQuery = useQuery({ queryKey: ['memoryTasks', project], queryFn: () => api.memoryTasks(project) });
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const [adding, setAdding] = useState<{ parentId?: string } | null>(null);
  const [linking, setLinking] = useState<T.Task | null>(null);
  const [showClosed, setShowClosed] = useState(false);

  const tasks = tasksQuery.data ?? [];
  const agents = (agentsQuery.data ?? []).filter((a) => a.project === project).map((a) => a.name);
  const { byId, childrenOf, roots } = taskTree(tasks);
  const visible = visibleTasks(tasks, childrenOf, showClosed);
  const closedCount = tasks.length - visibleTasks(tasks, childrenOf, false).size;
  const visibleRoots = roots.filter((t) => visible.has(t.id));

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['memoryTasks', project] });

  const updateStatus = useMutation({
    mutationFn: (vars: { id: string; status: string }) => api.updateTask(project, vars.id, { status: vars.status } satisfies T.UpdateTaskRequest),
    onSuccess: refresh,
    onError: (err) => toast.error(errorMessage(err)),
  });
  const unlink = useMutation({
    mutationFn: (vars: { taskId: string; dependsOnId: string }) => api.unlinkTasks(project, vars satisfies T.LinkTasksRequest),
    onSuccess: refresh,
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11.5px] text-subtle">
          {visibleRoots.length === 0 && tasks.length === 0
            ? 'Nothing planned yet'
            : `${tasks.length} task${tasks.length === 1 ? '' : 's'}`}
        </span>
        <div className="ml-auto flex items-center gap-2">
          {closedCount > 0 && (
            <button
              type="button"
              className="text-[12px] text-subtle underline-offset-2 hover:text-tertiary hover:underline"
              onClick={() => setShowClosed((v) => !v)}
            >
              {showClosed ? 'Hide done & abandoned' : `Show done & abandoned (${closedCount})`}
            </button>
          )}
          <Button size="sm" onClick={() => setAdding({})}>
            <Plus />
            Add task
          </Button>
        </div>
      </div>

      {tasksQuery.error && <Notice>{errorMessage(tasksQuery.error)}</Notice>}
      {tasksQuery.isPending && <p className="px-1 text-[13px] text-subtle">Loading…</p>}
      {!tasksQuery.isPending && tasks.length === 0 && (
        <Panel className="rounded-2xl">
          <EmptyState icon={ListTree} title="No plan yet">
            A task is work the project has, who's on it and what it's waiting on. Add one, or let the project's chat write them as it hands work to
            agents.
          </EmptyState>
        </Panel>
      )}
      {tasks.length > 0 && visibleRoots.length === 0 && <p className="px-1 text-[13px] text-subtle">Everything is done or abandoned.</p>}

      <div className="grid gap-2">
        {visibleRoots.map((task) => (
          <TaskRow
            key={task.id}
            task={task}
            byId={byId}
            childrenOf={childrenOf}
            visible={visible}
            onAddSubtask={(parentId) => setAdding({ parentId })}
            onChangeStatus={(id, status) => updateStatus.mutate({ id, status })}
            onLink={(id) => setLinking(byId.get(id) ?? null)}
            onUnlink={(taskId, dependsOnId) => unlink.mutate({ taskId, dependsOnId })}
          />
        ))}
      </div>

      <AddTaskDialog
        project={project}
        open={adding !== null}
        onOpenChange={(open) => !open && setAdding(null)}
        tasks={tasks}
        agents={agents}
        defaultParentId={adding?.parentId}
        onAdded={async () => {
          await refresh();
        }}
      />
      <LinkTaskDialog
        project={project}
        open={linking !== null}
        onOpenChange={(open) => !open && setLinking(null)}
        task={linking}
        tasks={tasks}
        onLinked={async () => {
          await refresh();
        }}
      />
    </div>
  );
}

function TaskRow({
  task,
  byId,
  childrenOf,
  visible,
  onAddSubtask,
  onChangeStatus,
  onLink,
  onUnlink,
}: {
  task: T.Task;
  byId: Map<string, T.Task>;
  childrenOf: Map<string, T.Task[]>;
  visible: Set<string>;
  onAddSubtask: (parentId: string) => void;
  onChangeStatus: (id: string, status: string) => void;
  onLink: (id: string) => void;
  onUnlink: (taskId: string, dependsOnId: string) => void;
}) {
  const children = (childrenOf.get(task.id) ?? []).filter((t) => visible.has(t.id));

  return (
    <div className="grid gap-2">
      <div className="panel rounded-2xl px-4 py-3" data-task={task.id} data-task-status={task.status}>
        <div className="flex flex-wrap items-start gap-2">
          <Badge variant={taskStatusVariant[task.status] ?? 'default'}>{taskStatusLabel[task.status] ?? task.status}</Badge>
          <h4 className="min-w-0 flex-1 text-[13.5px] font-medium text-primary">{task.goal}</h4>
          {task.agent && (
            <Badge variant="brand">
              <Users />
              {task.agent}
            </Badge>
          )}
          <Menu>
            <MenuTrigger asChild>
              <Button size="icon-sm" variant="ghost" aria-label="Task actions">
                <Ellipsis />
              </Button>
            </MenuTrigger>
            <MenuContent>
              {taskStatuses
                .filter((status) => status !== task.status)
                .map((status) => (
                  <MenuItem key={status} onSelect={() => onChangeStatus(task.id, status)}>
                    Mark {taskStatusLabel[status].toLowerCase()}
                  </MenuItem>
                ))}
              <MenuSeparator />
              <MenuItem icon={ListPlus} onSelect={() => onAddSubtask(task.id)}>
                Add subtask…
              </MenuItem>
              <MenuItem icon={Link2} onSelect={() => onLink(task.id)}>
                Link a blocker…
              </MenuItem>
            </MenuContent>
          </Menu>
        </div>
        {task.detail && <Markdown text={task.detail} className="mt-1.5 text-[12.5px] leading-relaxed text-muted" />}
        <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-subtle">
          <span>{timeAgo(task.createdAt)}</span>
          {task.updatedAt !== task.createdAt && <span>updated {timeAgo(task.updatedAt)}</span>}
        </div>
        {task.dependsOn && task.dependsOn.length > 0 && (
          <div className="mt-2">
            <p className="flex items-center gap-1.5 text-[10.5px] uppercase tracking-[0.07em] text-faint">
              <TriangleAlert className="size-3 text-amber-400/80" />
              Blocked on
            </p>
            <ul className="mt-1 grid gap-1">
              {task.dependsOn.map((id) => {
                const dep = byId.get(id);
                return (
                  <li key={id} className="flex items-center gap-2 rounded-lg bg-amber-400/[0.04] px-2 py-1 text-[12px] text-amber-200/80">
                    <Badge variant={dep ? (taskStatusVariant[dep.status] ?? 'default') : 'default'}>
                      {dep ? (taskStatusLabel[dep.status] ?? dep.status) : '?'}
                    </Badge>
                    <span className="min-w-0 flex-1 truncate">{dep?.goal ?? id}</span>
                    <button
                      type="button"
                      className="shrink-0 text-amber-300/60 hover:text-amber-100"
                      aria-label="Remove blocker"
                      onClick={() => onUnlink(task.id, id)}
                    >
                      <X className="size-3" />
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        )}
      </div>
      {children.length > 0 && (
        <div className="ml-5 grid gap-2 border-l border-line pl-4">
          {children.map((child) => (
            <TaskRow
              key={child.id}
              task={child}
              byId={byId}
              childrenOf={childrenOf}
              visible={visible}
              onAddSubtask={onAddSubtask}
              onChangeStatus={onChangeStatus}
              onLink={onLink}
              onUnlink={onUnlink}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function AddTaskDialog({
  project,
  open,
  onOpenChange,
  tasks,
  agents,
  defaultParentId,
  onAdded,
}: {
  project: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tasks: T.Task[];
  agents: string[];
  defaultParentId?: string;
  onAdded: () => Promise<unknown>;
}) {
  const [goal, setGoal] = useState('');
  const [detail, setDetail] = useState('');
  const [status, setStatus] = useState(A.TaskOpen);
  const [agent, setAgent] = useState('');
  const [parentId, setParentId] = useState(defaultParentId ?? '');

  const add = useMutation({
    mutationFn: () =>
      api.addTask(project, {
        goal: goal.trim(),
        detail: detail.trim() || undefined,
        status,
        agent: agent || undefined,
        parentId: parentId || undefined,
      } satisfies T.AddTaskRequest),
    onSuccess: async () => {
      setGoal('');
      setDetail('');
      setStatus(A.TaskOpen);
      setAgent('');
      setParentId('');
      onOpenChange(false);
      toast('Task added');
      await onAdded();
    },
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) add.reset();
        if (next) setParentId(defaultParentId ?? '');
        onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add a task</DialogTitle>
          <DialogDescription>What is to be done, in a line somebody would recognise it by. Who's on it and what it waits on can follow.</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (goal.trim()) add.mutate();
          }}
        >
          <Field label="Goal" htmlFor="task-goal">
            <Input id="task-goal" autoFocus value={goal} onChange={(event) => setGoal(event.target.value)} placeholder="What is to be done" />
          </Field>
          <Field label="Detail" htmlFor="task-detail">
            <Textarea id="task-detail" className="min-h-24" value={detail} onChange={(event) => setDetail(event.target.value)} placeholder="The brief behind the goal" />
          </Field>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Status" htmlFor="task-status">
              <Select id="task-status" value={status} onChange={setStatus}>
                {taskStatuses.map((s) => (
                  <SelectOption key={s} value={s}>
                    {taskStatusLabel[s]}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            <Field label="Agent" htmlFor="task-agent">
              <Select id="task-agent" value={agent} onChange={setAgent}>
                <SelectOption value="">Unassigned</SelectOption>
                {agents.map((name) => (
                  <SelectOption key={name} value={name}>
                    {name}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            <Field label="Parent" htmlFor="task-parent">
              <Select id="task-parent" value={parentId} onChange={setParentId}>
                <SelectOption value="">No parent</SelectOption>
                {tasks.map((t) => (
                  <SelectOption key={t.id} value={t.id}>
                    {t.goal}
                  </SelectOption>
                ))}
              </Select>
            </Field>
          </div>
          {add.error && <Notice>{errorMessage(add.error)}</Notice>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!goal.trim() || add.isPending}>
              {add.isPending ? 'Saving…' : 'Add task'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// LinkTaskDialog draws one blocking edge (LinkTasks, tasks.go): task can't
// unblock until dependsOn does. A cycle is refused server-side, with an error
// naming the path that closed it, so the dialog just surfaces that message
// rather than trying to rule it out itself.
function LinkTaskDialog({
  project,
  open,
  onOpenChange,
  task,
  tasks,
  onLinked,
}: {
  project: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  task: T.Task | null;
  tasks: T.Task[];
  onLinked: () => Promise<unknown>;
}) {
  const [dependsOnId, setDependsOnId] = useState('');
  const options = tasks.filter((t) => t.id !== task?.id && !(task?.dependsOn ?? []).includes(t.id));

  const link = useMutation({
    mutationFn: () => api.linkTasks(project, { taskId: task!.id, dependsOnId } satisfies T.LinkTasksRequest),
    onSuccess: async () => {
      setDependsOnId('');
      onOpenChange(false);
      await onLinked();
    },
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          link.reset();
          setDependsOnId('');
        }
        onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{task && `Block "${task.goal}" on…`}</DialogTitle>
          <DialogDescription>{task?.goal} can't be finished until the task you pick here is.</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (dependsOnId) link.mutate();
          }}
        >
          <Field label="Waiting on" htmlFor="task-depends-on">
            <Select id="task-depends-on" value={dependsOnId} onChange={setDependsOnId} placeholder="Choose a task">
              {options.map((t) => (
                <SelectOption key={t.id} value={t.id}>
                  {t.goal}
                </SelectOption>
              ))}
            </Select>
          </Field>
          {link.error && <Notice>{errorMessage(link.error)}</Notice>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!dependsOnId || link.isPending}>
              {link.isPending ? 'Linking…' : 'Link'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// --- Memories ---------------------------------------------------------------

const allKinds = [A.MemoryKindProject, A.MemoryKindDecision, A.MemoryKindDiscovery, A.MemoryKindIssue, A.MemoryKindEpisodic];
const kindLabel: Record<string, string> = {
  [A.MemoryKindProject]: 'Project',
  [A.MemoryKindDecision]: 'Decision',
  [A.MemoryKindDiscovery]: 'Discovery',
  [A.MemoryKindIssue]: 'Issue',
  [A.MemoryKindEpisodic]: 'Episodic',
};
const kindVariant: Record<string, BadgeVariant> = {
  [A.MemoryKindProject]: 'info',
  [A.MemoryKindDecision]: 'brand',
  [A.MemoryKindDiscovery]: 'success',
  [A.MemoryKindIssue]: 'warning',
  [A.MemoryKindEpisodic]: 'default',
};

function ImportanceDots({ value }: { value: number }) {
  return (
    <span className="flex items-center gap-0.5" title={`Importance ${value} of 5`}>
      {[1, 2, 3, 4, 5].map((n) => (
        <span key={n} className={cn('size-1.5 rounded-full', n <= value ? 'bg-brand-400' : 'bg-surface-strong')} />
      ))}
    </span>
  );
}

// Superseded is what SupersedeMemory ([memory.go]) is: the newest live memory
// keeps supersedesId, the row it replaced stops coming back from Memories or
// Search, and nothing is deleted. But there's no route that reads the old row
// back — Memories and Search both leave it out on purpose, and there's no
// GET-by-id either — so this tab can't reach back further than its own
// session. Superseding a memory here keeps the row you replaced in view,
// behind the toggle, pointing at what replaced it; a memory the project's
// chat superseded before you opened this tab is just gone from the list,
// same as it would be without a toggle at all.
interface SupersededEntry {
  old: T.Memory;
  by: T.Memory;
}

function MemoriesSection({ project }: { project: string }) {
  const queryClient = useQueryClient();
  const [kind, setKind] = useState('');
  const [showSuperseded, setShowSuperseded] = useState(false);
  const [showResolved, setShowResolved] = useState(false);
  const [adding, setAdding] = useState(false);
  const [superseding, setSuperseding] = useState<T.Memory | null>(null);
  const [superseded, setSuperseded] = useState<SupersededEntry[]>([]);
  const [resolving, setResolving] = useState<T.Memory | null>(null);
  const [resolved, setResolved] = useState<T.Memory[]>([]);
  const memories = useQuery({
    queryKey: ['memories', project, kind],
    queryFn: () => api.memories(project, kind ? [kind] : []),
  });

  const items = memories.data ?? [];
  const visibleSuperseded = showSuperseded ? superseded.filter((entry) => !kind || entry.old.kind === kind) : [];
  const visibleResolved = showResolved ? resolved.filter((memory) => !kind || memory.kind === kind) : [];
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['memories', project] });

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-1">
        <FilterChip active={!kind} count={items.length} onClick={() => setKind('')}>
          All kinds
        </FilterChip>
        {allKinds.map((k) => (
          <FilterChip key={k} active={kind === k} count={items.filter((m) => m.kind === k).length} onClick={() => setKind(kind === k ? '' : k)}>
            {kindLabel[k]}
          </FilterChip>
        ))}
        <div className="ml-auto flex items-center gap-2">
          {resolved.length > 0 && (
            <button
              type="button"
              className="text-[12px] text-subtle underline-offset-2 hover:text-tertiary hover:underline"
              onClick={() => setShowResolved((v) => !v)}
            >
              {showResolved ? 'Hide resolved' : `Show resolved (${resolved.length})`}
            </button>
          )}
          {superseded.length > 0 && (
            <button
              type="button"
              className="text-[12px] text-subtle underline-offset-2 hover:text-tertiary hover:underline"
              onClick={() => setShowSuperseded((v) => !v)}
            >
              {showSuperseded ? 'Hide superseded' : `Show superseded (${superseded.length})`}
            </button>
          )}
          <Button size="sm" onClick={() => setAdding(true)}>
            <Plus />
            Add memory
          </Button>
        </div>
      </div>

      {memories.error && <Notice>{errorMessage(memories.error)}</Notice>}
      {memories.isPending && <p className="px-1 text-[13px] text-subtle">Loading…</p>}
      {!memories.isPending && items.length === 0 && visibleSuperseded.length === 0 && visibleResolved.length === 0 && (
        <Panel className="rounded-2xl">
          <EmptyState icon={Sparkles} title="Nothing remembered yet">
            A memory is a judgement worth keeping — how the project works, why something was chosen, a thing that will bite you. Add one, or let the
            project's chat write them as it goes.
          </EmptyState>
        </Panel>
      )}

      <div className="grid gap-2">
        {items.map((memory) => (
          <MemoryRow
            key={memory.id}
            memory={memory}
            onSupersede={() => setSuperseding(memory)}
            onResolve={memory.kind === A.MemoryKindIssue ? () => setResolving(memory) : undefined}
          />
        ))}
        {visibleSuperseded.map((entry) => (
          <MemoryRow key={`superseded-${entry.old.id}`} memory={entry.old} supersededBy={entry.by} />
        ))}
        {visibleResolved.map((memory) => (
          <MemoryRow key={`resolved-${memory.id}`} memory={memory} />
        ))}
      </div>

      <AddMemoryDialog
        project={project}
        open={adding}
        onOpenChange={setAdding}
        onAdded={async () => {
          await refresh();
        }}
      />
      <AddMemoryDialog
        project={project}
        open={superseding !== null}
        onOpenChange={(open) => !open && setSuperseding(null)}
        supersedes={superseding ?? undefined}
        onAdded={async (created) => {
          if (superseding) setSuperseded((prev) => [...prev, { old: superseding, by: created }]);
          await refresh();
        }}
      />
      {resolving && (
        <ResolveIssueDialog
          project={project}
          issue={resolving}
          onOpenChange={(open) => !open && setResolving(null)}
          onResolved={async (updated) => {
            setResolved((prev) => [...prev, updated]);
            await refresh();
          }}
        />
      )}
    </div>
  );
}

function MemoryRow({
  memory,
  supersededBy,
  onSupersede,
  onResolve,
}: {
  memory: T.Memory;
  supersededBy?: T.Memory;
  onSupersede?: () => void;
  onResolve?: () => void;
}) {
  const resolved = Boolean(memory.resolvedAt);
  const dimmed = Boolean(supersededBy) || resolved;
  return (
    <div
      className={cn('panel rounded-2xl px-4 py-3', dimmed && 'opacity-60')}
      data-memory={memory.id}
      data-memory-superseded={supersededBy ? true : undefined}
      data-memory-resolved={resolved ? true : undefined}
    >
      <div className="flex flex-wrap items-start gap-2">
        <Badge variant={kindVariant[memory.kind] ?? 'default'}>{kindLabel[memory.kind] ?? memory.kind}</Badge>
        <h4 className="min-w-0 flex-1 text-[13.5px] font-medium text-primary">{memory.title}</h4>
        {resolved && (
          <Badge variant="success">
            <CircleCheck />
            Resolved
          </Badge>
        )}
        <ImportanceDots value={memory.importance} />
        {(onSupersede || onResolve) && (
          <Menu>
            <MenuTrigger asChild>
              <Button size="icon-sm" variant="ghost" aria-label="Memory actions">
                <Ellipsis />
              </Button>
            </MenuTrigger>
            <MenuContent>
              {onResolve && <MenuItem onSelect={onResolve}>Resolve…</MenuItem>}
              {onSupersede && <MenuItem onSelect={onSupersede}>Supersede…</MenuItem>}
            </MenuContent>
          </Menu>
        )}
      </div>
      {memory.content && <Markdown text={memory.content} className="mt-1.5 text-[12.5px] leading-relaxed text-muted" />}
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-subtle">
        <span>{timeAgo(memory.createdAt)}</span>
        {memory.updatedAt !== memory.createdAt && <span>updated {timeAgo(memory.updatedAt)}</span>}
        {memory.supersedesId && <span className="font-mono text-faint">supersedes {memory.supersedesId}</span>}
        {supersededBy && <span className="text-amber-300/80">superseded by “{supersededBy.title}”</span>}
        {resolved && <span className="text-emerald-300/80">resolved: {memory.resolvedBy}</span>}
      </div>
    </div>
  );
}

// ResolveIssueDialog is resolve_memory's click (ResolveMemory, D76): an issue
// that is simply over, with nothing to put in its place, so unlike Supersede
// there's no new memory to write — only the line that says what happened.
// GET …/memory/memories leaves a resolved row out exactly as it does a
// superseded one, so the row this closes is tracked locally afterwards
// (`resolved` in MemoriesSection) the same way a superseded row already is —
// this tab can't reach back past its own session either way.
function ResolveIssueDialog({
  project,
  issue,
  onOpenChange,
  onResolved,
}: {
  project: string;
  issue: T.Memory;
  onOpenChange: (open: boolean) => void;
  onResolved: (resolved: T.Memory) => Promise<unknown>;
}) {
  const [why, setWhy] = useState('');
  const resolve = useMutation({
    mutationFn: () => api.resolveMemory(project, { id: issue.id, why: why.trim() } satisfies T.ResolveMemoryRequest),
    onSuccess: async (updated) => {
      onOpenChange(false);
      toast(`“${updated.title}” resolved`);
      await onResolved(updated);
    },
  });

  return (
    <Dialog open onOpenChange={(next) => { if (!next) resolve.reset(); onOpenChange(next); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Resolve “{issue.title}”</DialogTitle>
          <DialogDescription>
            Closes the issue with nothing to replace it — the bug was fixed, the flaky test was deleted, it stopped mattering. It stops coming back
            from a list or a search, but stays readable by id.
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (why.trim()) resolve.mutate();
          }}
        >
          <Field label="How it was resolved" htmlFor="resolve-why" hint="One line: what happened, not what to do next.">
            <Textarea
              id="resolve-why"
              autoFocus
              className="min-h-20"
              value={why}
              onChange={(event) => setWhy(event.target.value)}
              placeholder="What happened"
            />
          </Field>
          {resolve.error && <Notice>{errorMessage(resolve.error)}</Notice>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!why.trim() || resolve.isPending}>
              {resolve.isPending ? 'Resolving…' : 'Resolve'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function AddMemoryDialog({
  project,
  open,
  onOpenChange,
  supersedes,
  prefillFrom,
  onAdded,
}: {
  project: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  supersedes?: T.Memory;
  // prefillFrom starts the form from another memory's fields rather than
  // blank — the duplicate pairs card uses it to start "supersede B" from A's
  // title and content, since the two are judged near-duplicates already.
  prefillFrom?: T.Memory;
  onAdded: (created: T.Memory) => Promise<unknown>;
}) {
  const [kind, setKind] = useState(prefillFrom?.kind ?? supersedes?.kind ?? A.MemoryKindProject);
  const [title, setTitle] = useState(prefillFrom?.title ?? '');
  const [content, setContent] = useState(prefillFrom?.content ?? '');
  const [importance, setImportance] = useState(String(prefillFrom?.importance ?? supersedes?.importance ?? 3));
  const add = useMutation({
    mutationFn: () =>
      api.addMemory(project, {
        kind,
        title: title.trim(),
        content: content.trim() || undefined,
        importance: Number(importance),
        supersedesId: supersedes?.id,
      } satisfies T.AddMemoryRequest),
    onSuccess: async (created) => {
      setTitle('');
      setContent('');
      setImportance('3');
      onOpenChange(false);
      toast(supersedes ? `“${created.title}” supersedes “${supersedes.title}”` : `“${created.title}” remembered`);
      await onAdded(created);
    },
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) add.reset();
        onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{supersedes ? `Supersede “${supersedes.title}”` : 'Add a memory'}</DialogTitle>
          <DialogDescription>
            {supersedes
              ? 'The old memory stays, but stops coming back from a list or a search — this is what replaces it.'
              : "A judgement worth keeping: how the project works, why something was chosen, a thing that will bite you. Raw history belongs in an event, not here."}
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (title.trim()) add.mutate();
          }}
        >
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_auto]">
            <Field label="Kind" htmlFor="memory-kind">
              <Select id="memory-kind" value={kind} onChange={setKind}>
                {allKinds.map((k) => (
                  <SelectOption key={k} value={k}>
                    {kindLabel[k]}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            <Field label="Importance" htmlFor="memory-importance">
              <Select id="memory-importance" className="w-32" value={importance} onChange={setImportance}>
                {[1, 2, 3, 4, 5].map((n) => (
                  <SelectOption key={n} value={String(n)}>
                    {n} {n === 1 ? '(worth knowing)' : n === 5 ? '(essential)' : ''}
                  </SelectOption>
                ))}
              </Select>
            </Field>
          </div>
          <Field label="Title" htmlFor="memory-title" hint="One line somebody can recognise it by in a list.">
            <Input id="memory-title" autoFocus value={title} onChange={(event) => setTitle(event.target.value)} placeholder="What this is" />
          </Field>
          <Field label="Content" htmlFor="memory-content">
            <Textarea id="memory-content" className="min-h-28" value={content} onChange={(event) => setContent(event.target.value)} placeholder="The detail behind the title" />
          </Field>
          {add.error && <Notice>{errorMessage(add.error)}</Notice>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!title.trim() || add.isPending}>
              {add.isPending ? 'Saving…' : supersedes ? 'Supersede' : 'Remember'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// --- Search -----------------------------------------------------------------

// MemorySearchSection is one query against all three FTS5 indexes at once
// (search.go). The three answers are shown as three groups rather than one
// merged list because bm25 only ranks within one index — a memory's score
// and an event's score aren't the same currency.
function MemorySearchSection({ project }: { project: string }) {
  const [query, setQuery] = useState('');
  const search = useMutation({
    mutationFn: (q: string) => api.searchMemory(project, { query: q } satisfies T.MemorySearchRequest),
  });

  return (
    <div className="grid gap-4">
      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (query.trim()) search.mutate(query.trim());
        }}
      >
        <Input
          aria-label="Search memory"
          className="flex-1"
          placeholder="What do we know about…"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <Button type="submit" disabled={!query.trim() || search.isPending}>
          {search.isPending ? 'Searching…' : 'Search'}
          <SearchIcon />
        </Button>
      </form>

      {search.error && <Notice>{errorMessage(search.error)}</Notice>}

      {search.data && (
        <div className="grid gap-5">
          <SearchGroup title="Memories" count={search.data.memories?.length ?? 0}>
            {(search.data.memories ?? []).map((memory) => (
              <MemoryRow key={memory.id} memory={memory} />
            ))}
          </SearchGroup>
          <SearchGroup title="Events" count={search.data.events?.length ?? 0}>
            {(search.data.events ?? []).map((event) => (
              <EventRow key={event.id} event={event} />
            ))}
          </SearchGroup>
          <SearchGroup title="Reports" count={search.data.reports?.length ?? 0}>
            {(search.data.reports ?? []).map((report) => (
              <ReportRow key={report.id} report={report} />
            ))}
          </SearchGroup>
        </div>
      )}
    </div>
  );
}

function SearchGroup({ title, count, children }: { title: string; count: number; children: ReactNode }) {
  if (count === 0) return null;
  return (
    <div className="grid gap-2">
      <h3 className="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle">
        {title} <span className="tabular-nums text-faint">{count}</span>
      </h3>
      <div className="grid gap-2">{children}</div>
    </div>
  );
}

// --- Events ------------------------------------------------------------------

const limitOptions = [50, 100, 200];

// MemoryEventsSection is the raw timeline (events.go): what the daemon
// captured on its own from an agent's lifecycle, a question, a merge, new
// media, and the lead's conversation being folded in — append-only, and
// newest first. It's the debugging view, so nothing here is summarised.
function MemoryEventsSection({ project }: { project: string }) {
  const [limit, setLimit] = useState(100);
  const [type, setType] = useState('');
  const [agent, setAgent] = useState('');
  const events = useQuery({ queryKey: ['memoryEvents', project, limit], queryFn: () => api.memoryEvents(project, { limit }) });

  const items = events.data ?? [];
  const types = new Map<string, number>();
  const agents = new Map<string, number>();
  for (const event of items) {
    types.set(event.type, (types.get(event.type) ?? 0) + 1);
    const a = event.agent ?? '';
    agents.set(a, (agents.get(a) ?? 0) + 1);
  }
  const visible = items.filter((event) => (!type || event.type === type) && (!agent || (event.agent ?? '') === agent));

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-1">
        <FilterChip active={!type} count={items.length} onClick={() => setType('')}>
          All types
        </FilterChip>
        {[...types].map(([name, count]) => (
          <FilterChip key={name} active={type === name} count={count} onClick={() => setType(type === name ? '' : name)}>
            {name}
          </FilterChip>
        ))}
      </div>
      {agents.size > 1 && (
        <div className="flex flex-wrap items-center gap-1">
          <FilterChip active={!agent} count={items.length} onClick={() => setAgent('')}>
            All agents
          </FilterChip>
          {[...agents].map(([name, count]) => (
            <FilterChip key={name || '(project)'} active={agent === name} count={count} onClick={() => setAgent(agent === name ? '' : name)}>
              {name || '(project)'}
            </FilterChip>
          ))}
        </div>
      )}

      <div className="flex items-center justify-between px-1">
        <span className="text-[11.5px] text-subtle">
          Newest {items.length} event{items.length === 1 ? '' : 's'}
          {items.length === limit && ` (of at most ${limit} — there may be more)`}
        </span>
        <label className="flex items-center gap-2 text-[12px] text-subtle">
          Show
          <Select className="h-7 w-24" value={String(limit)} onChange={(v) => setLimit(Number(v))}>
            {limitOptions.map((n) => (
              <SelectOption key={n} value={String(n)}>
                {n}
              </SelectOption>
            ))}
          </Select>
        </label>
      </div>

      {events.error && <Notice>{errorMessage(events.error)}</Notice>}
      {events.isPending && <p className="px-1 text-[13px] text-subtle">Loading…</p>}
      {!events.isPending && items.length === 0 && (
        <Panel className="rounded-2xl">
          <EmptyState icon={History} title="Nothing captured yet">
            An agent being made or retired, a question, a merge, new media, and the lead's own conversation land here on their own — nothing to do to start it.
          </EmptyState>
        </Panel>
      )}
      {items.length > 0 && visible.length === 0 && <p className="px-1 text-[13px] text-subtle">Nothing matches that filter.</p>}

      <div className="grid gap-2">
        {visible.map((event) => (
          <EventRow key={event.id} event={event} />
        ))}
      </div>
    </div>
  );
}

function EventRow({ event }: { event: T.MemoryEvent }) {
  const [expanded, setExpanded] = useState(false);
  const payload = payloadText(event.payload);
  return (
    <div className="panel rounded-2xl px-4 py-3" data-event={event.id} data-event-type={event.type}>
      <div className="flex flex-wrap items-center gap-2">
        <Badge>{event.type}</Badge>
        {event.agent && <span className="text-[12px] text-muted">{event.agent}</span>}
        <span className="ml-auto text-[11.5px] text-subtle">{timeAgo(event.at)}</span>
      </div>
      {payload && (
        <button type="button" className="mt-1.5 text-[11px] text-subtle hover:text-tertiary" onClick={() => setExpanded((v) => !v)}>
          {expanded ? 'Hide payload' : 'Show payload'}
        </button>
      )}
      {expanded && payload && (
        <pre className="mt-1.5 max-h-48 overflow-auto rounded-lg border border-line-faint bg-sunken p-2.5 font-mono text-[11px] leading-relaxed text-muted">
          {payload}
        </pre>
      )}
    </div>
  );
}

function payloadText(payload: unknown): string {
  if (payload === undefined || payload === null) return '';
  try {
    const text = JSON.stringify(payload, null, 2);
    return text === '{}' || text === 'null' ? '' : text;
  } catch {
    return '';
  }
}

// --- Reports & artifacts ------------------------------------------------------

const statusVariant: Record<string, BadgeVariant> = {
  [A.ReportDone]: 'success',
  [A.ReportPartial]: 'warning',
  blocked: 'warning',
  failed: 'danger',
};

// MemoryReportsSection is what an agent said as it finished (report, in
// internal/cli/memory.go) and, below it, the artifacts agents recorded —
// references to something produced, never a copy. An artifact's path may
// point at something that no longer exists, so it's shown as a reference:
// the "media" ones open in the Media tab, which already knows how to show
// one, rather than this tab pretending it can open anything itself.
function MemoryReportsSection({ project, onOpenMedia }: { project: string; onOpenMedia: () => void }) {
  const reports = useQuery({ queryKey: ['memoryReports', project], queryFn: () => api.memoryReports(project) });
  const artifacts = useQuery({ queryKey: ['memoryArtifacts', project], queryFn: () => api.memoryArtifacts(project) });

  return (
    <div className="grid gap-6">
      <div className="grid gap-2">
        <h3 className="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle">Reports</h3>
        {reports.error && <Notice>{errorMessage(reports.error)}</Notice>}
        {reports.isPending && <p className="px-1 text-[13px] text-subtle">Loading…</p>}
        {!reports.isPending && (reports.data?.length ?? 0) === 0 && (
          <Panel className="rounded-2xl">
            <EmptyState icon={FileText} title="No reports yet">
              What an agent says as it finishes — a summary, its status, what it found and decided, and what's still open.
            </EmptyState>
          </Panel>
        )}
        <div className="grid gap-2">
          {(reports.data ?? []).map((report) => (
            <ReportRow key={report.id} report={report} />
          ))}
        </div>
      </div>

      <div className="grid gap-2">
        <h3 className="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle">Artifacts</h3>
        {artifacts.error && <Notice>{errorMessage(artifacts.error)}</Notice>}
        {!artifacts.isPending && (artifacts.data?.length ?? 0) === 0 && <p className="px-1 text-[13px] text-subtle">Nothing recorded yet.</p>}
        <div className="grid gap-2">
          {(artifacts.data ?? []).map((artifact) => (
            <ArtifactRow key={artifact.id} artifact={artifact} onOpenMedia={onOpenMedia} />
          ))}
        </div>
      </div>
    </div>
  );
}

function ReportRow({ report }: { report: T.AgentReport }) {
  return (
    <div className="panel rounded-2xl px-4 py-3" data-report={report.id}>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={statusVariant[report.status] ?? 'default'}>{report.status}</Badge>
        <span className="text-[13px] font-medium text-primary">{report.agent}</span>
        {report.task && <span className="min-w-0 truncate text-[12px] text-subtle">— {report.task}</span>}
        <span className="ml-auto shrink-0 text-[11.5px] text-subtle">{timeAgo(report.createdAt)}</span>
      </div>
      <Markdown text={report.summary} className="mt-1.5 text-[12.5px] leading-relaxed text-tertiary" />
      <ReportList label="Discoveries" items={report.discoveries} />
      <ReportList label="Decisions" items={report.decisions} />
      <ReportList label="Remaining issues" items={report.remainingIssues} tone="amber" />
    </div>
  );
}

function ReportList({ label, items, tone }: { label: string; items?: string[]; tone?: 'amber' }) {
  if (!items || items.length === 0) return null;
  return (
    <div className="mt-2">
      <p className="flex items-center gap-1.5 text-[10.5px] uppercase tracking-[0.07em] text-faint">
        {tone === 'amber' && <TriangleAlert className="size-3 text-amber-400/80" />}
        {label}
      </p>
      <ul className={cn('mt-1 list-disc space-y-0.5 pl-4 text-[12px] leading-relaxed', tone === 'amber' ? 'text-amber-200/80' : 'text-muted')}>
        {items.map((item, i) => (
          <li key={i}>{item}</li>
        ))}
      </ul>
    </div>
  );
}

function ArtifactRow({ artifact, onOpenMedia }: { artifact: T.MemoryArtifact; onOpenMedia: () => void }) {
  const isURL = artifact.type === 'url' || /^https?:\/\//.test(artifact.path);
  const isMedia = artifact.type === 'media';
  return (
    <div className="flex flex-wrap items-center gap-2.5 rounded-xl border border-line-faint bg-surface-faint px-3 py-2.5" data-artifact={artifact.id}>
      <Paperclip className="size-3.5 shrink-0 text-subtle" />
      <Badge>{artifact.type}</Badge>
      <span className="min-w-0 flex-1 truncate font-mono text-[12px] text-muted" title={artifact.path}>
        {artifact.path}
      </span>
      {artifact.agent && <span className="shrink-0 text-[11.5px] text-subtle">{artifact.agent}</span>}
      <span className="shrink-0 text-[11px] text-faint">{timeAgo(artifact.createdAt)}</span>
      {isMedia && (
        <Button size="sm" variant="ghost" onClick={onOpenMedia}>
          Open in Media
        </Button>
      )}
      {isURL && !isMedia && (
        <Button size="sm" variant="ghost" onClick={() => void window.agentbox.openExternal(artifact.path)}>
          Open
        </Button>
      )}
    </div>
  );
}

// --- Context: what memory costs, the budget, consolidation, a preview -------

// ContextSection is the "spending fewer tokens, not storing more" half of
// this tab (docs/implementation/project-memory.md, "The context builder" and
// "Consolidation"). Everything here reads what the builder already records —
// nothing is computed twice — and the budget control writes through the same
// PATCH `agentbox context-budget` uses.
function ContextSection({ project }: { project: string }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const current = projects.data?.find((p) => p.name === project);

  return (
    <div className="grid gap-4">
      <ContextCostCard project={project} current={current} />
      <ConsolidationPassesCard project={project} />
      <DuplicatePairsCard project={project} />
      <ContextPreviewCard project={project} budget={current?.contextBudget} />
    </div>
  );
}

const contextForLabel: Record<string, string> = {
  [A.ContextForLead]: 'the project chat',
  [A.ContextForAgent]: 'a worker brief',
  [A.ContextForTool]: 'a direct request',
};

// describeRatio turns Stats.Ratio into a sentence rather than a bare number.
// Below 1 the context is a fraction of the corpus it was drawn from, which is
// the point of having a budget; at or above 1 a project that remembers almost
// nothing costs more in headings and labels than the handful of rows it draws
// from — the honest answer the builder gives rather than a bug.
function describeRatio(ratio: number): string {
  if (ratio >= 1) return `${ratio.toFixed(1)}× the size of what it drew from`;
  const factor = Math.round(1 / Math.max(ratio, 0.001));
  return factor >= 2 ? `${factor}× smaller than what it drew from` : `${Math.round(ratio * 100)}% of what it drew from`;
}

function ContextCostCard({ project, current }: { project: string; current?: T.Project }) {
  const queryClient = useQueryClient();
  const stats = useQuery({ queryKey: ['memoryContextStats', project], queryFn: () => api.memoryContextStats(project) });
  const account = stats.data;
  const latest = account?.recent?.[0];
  const trend = [...(account?.recent ?? [])].reverse().map((s) => s.tokens);

  const savedBudget = current?.contextBudget ?? 0;
  const [draft, setDraft] = useState<string | null>(null);
  const budgetValue = draft ?? (savedBudget ? String(savedBudget) : '');
  const dirty = draft !== null && draft !== (savedBudget ? String(savedBudget) : '');
  const save = useMutation({
    mutationFn: (contextBudget: number) => api.updateProject(project, { contextBudget }),
    onSuccess: async (updated) => {
      setDraft(null);
      toast(`${updated.name}'s context budget is now ${formatTokens(updated.contextBudget)} tokens`);
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <Card
      title="What memory costs"
      icon={Coins}
      description="The compression ratio a build records: how large the project's memory is against how many tokens a built context actually spends. Estimated at about four bytes to a token — it undercounts code, file paths and identifiers, and non-Latin text more again, so read it as an order of magnitude, not a number a provider would agree with."
    >
      {stats.error && <Notice>{errorMessage(stats.error)}</Notice>}
      {stats.isPending && <p className="text-[13px] text-subtle">Loading…</p>}

      {!stats.isPending && !latest && (
        <p className="text-[13px] leading-relaxed text-subtle">
          Nothing has been built from this project's memory yet. The lead's recap, a worker's brief and the preview below all go through the same
          builder, and this fills in the first time one of them runs.
        </p>
      )}

      {latest && (
        <div className="grid gap-3">
          <div className="flex flex-wrap items-end justify-between gap-3">
            <div>
              <div className="text-2xl font-semibold tracking-tight text-title">{describeRatio(latest.ratio)}</div>
              <p className="mt-0.5 text-[12px] text-subtle">
                Last build, for {contextForLabel[latest.for] ?? latest.for}: {formatTokens(latest.tokens)} of {formatTokens(latest.corpusTokens)} tokens
                remembered · {timeAgo(latest.at)}
              </p>
            </div>
            {trend.length >= 2 && <Sparkline values={trend} className="mb-1 shrink-0 text-brand-300" />}
          </div>

          <TokenMeter tokens={latest.tokens} budget={latest.budget} />

          {account && (
            <p className="text-[11.5px] text-faint">
              {account.builds} build{account.builds === 1 ? '' : 's'} since the daemon started · {formatTokens(account.tokens)} tokens spent in total ·{' '}
              {account.droppedRows} row{account.droppedRows === 1 ? '' : 's'} dropped to fit
            </p>
          )}
        </div>
      )}

      <div className="mt-4 flex flex-wrap items-end gap-3 border-t border-line-faint pt-3.5">
        <Field label="Context budget" htmlFor="context-budget" hint="500 to 32,000 tokens — a build clamps rather than refuses. The same as agentbox context-budget.">
          <Input
            id="context-budget"
            type="number"
            min={500}
            max={32000}
            step={100}
            className="w-40"
            value={budgetValue}
            onChange={(event) => setDraft(event.target.value)}
          />
        </Field>
        <Button size="sm" disabled={!dirty || !Number(budgetValue) || save.isPending} onClick={() => save.mutate(Number(budgetValue))}>
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
        {dirty && (
          <Button variant="ghost" size="sm" disabled={save.isPending} onClick={() => setDraft(null)}>
            Revert
          </Button>
        )}
        {save.error && <Notice className="w-full">{errorMessage(save.error)}</Notice>}
      </div>
    </Card>
  );
}

// TokenMeter is the budget as a bar: how much of it the last build spent.
// Amber past 85%, the threshold the chat's own context ring uses (ContextMeter
// in chat/Composer.tsx); a build over budget is clamped by the builder rather
// than refused, so this can still read past 100%.
function TokenMeter({ tokens, budget }: { tokens: number; budget: number }) {
  if (!budget) return null;
  const fraction = tokens / budget;
  const over = fraction > 1;
  return (
    <div className="grid gap-1">
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-strong">
        <div
          className={cn('h-full rounded-full', over ? 'bg-rose-400' : fraction > 0.85 ? 'bg-amber-400' : 'bg-brand-400')}
          style={{ width: `${Math.round(Math.min(1, fraction) * 100)}%` }}
        />
      </div>
      <p className="text-[11px] text-subtle">
        {formatTokens(tokens)} of a {formatTokens(budget)}-token budget{over ? ' — over, and clamped' : ''}
      </p>
    </div>
  );
}

// --- Consolidation ------------------------------------------------------------

const passKindVariant: Record<string, BadgeVariant> = {
  [A.ConsolidationMechanical]: 'default',
  [A.ConsolidationDistill]: 'brand',
};

// ConsolidationPassesCard is what turns events into memories (D76): a free
// mechanical pass — string comparison, no model — beside a distillation that
// spends the project's chat. The table is what a person can use to tell the
// two apart and see that either is actually running; GET /consolidation
// already bounds it to the last ten passes, so there's nothing here to page.
function ConsolidationPassesCard({ project }: { project: string }) {
  const consolidation = useQuery({ queryKey: ['memoryConsolidation', project], queryFn: () => api.memoryConsolidation(project) });
  const data = consolidation.data;
  const passes = data?.recent ?? [];

  return (
    <Card
      title="Consolidation"
      icon={GitMerge}
      description="What turns raw events into judgement: a free mechanical pass that merges exact duplicates and ages what nothing has referenced, and a distillation that spends the project's chat on the events since the last one."
    >
      {consolidation.error && <Notice>{errorMessage(consolidation.error)}</Notice>}
      {consolidation.isPending && <p className="text-[13px] text-subtle">Loading…</p>}

      {data && (
        <div className="grid gap-3">
          <p className="text-[12.5px] leading-relaxed text-muted">
            {data.events} event{data.events === 1 ? '' : 's'} recorded, {data.memories} live memor{data.memories === 1 ? 'y' : 'ies'}
            {data.pending > 0 ? `, ${data.pending} pending since the watermark` : ''} ·{' '}
            {data.setting > 0 ? `distills every ${data.setting} new events` : 'distillation is switched off for this project'}
          </p>

          {passes.length === 0 ? (
            <p className="text-[13px] text-subtle">No pass has run yet.</p>
          ) : (
            <div className="overflow-x-auto rounded-xl border border-line-faint">
              <table className="w-full text-[12px]">
                <thead>
                  <tr className="border-b border-line text-left text-[10.5px] uppercase tracking-[0.06em] text-subtle">
                    <th className="py-1.5 pl-3 pr-2 font-medium">Pass</th>
                    <th className="px-2 py-1.5 font-medium">When</th>
                    <th className="px-2 py-1.5 text-right font-medium">Read</th>
                    <th className="px-2 py-1.5 text-right font-medium">Written</th>
                    <th className="px-2 py-1.5 text-right font-medium">Superseded</th>
                    <th className="px-2 py-1.5 text-right font-medium">Resolved</th>
                    <th className="px-2 py-1.5 text-right font-medium">Decayed</th>
                    <th className="px-2 py-1.5 text-right font-medium">Duplicates</th>
                    <th className="py-1.5 pl-2 pr-3 text-right font-medium">Cost</th>
                  </tr>
                </thead>
                <tbody>
                  {passes.map((pass) => (
                    <PassRow key={pass.id} pass={pass} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </Card>
  );
}

function PassRow({ pass }: { pass: T.ConsolidationPass }) {
  return (
    <tr className="border-t border-line-faint first:border-t-0" data-pass={pass.id} data-pass-kind={pass.kind}>
      <td className="py-1.5 pl-3 pr-2 align-top">
        <Badge variant={passKindVariant[pass.kind] ?? 'default'}>{pass.kind}</Badge>
        {pass.error && <div className="mt-1 max-w-[13rem] text-[10.5px] leading-snug text-rose-300">{pass.error}</div>}
      </td>
      <td className="px-2 py-1.5 align-top whitespace-nowrap text-muted">{timeAgo(pass.at)}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.eventsRead}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.memoriesWritten}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.memoriesSuperseded}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.memoriesResolved}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.memoriesDecayed}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.duplicatesFound}</td>
      <td className="py-1.5 pl-2 pr-3 text-right align-top tabular-nums whitespace-nowrap text-muted">
        {pass.kind === A.ConsolidationMechanical ? (
          <span className="text-faint">free</span>
        ) : (
          <>
            {humanBytes(pass.inputBytes)} → {humanBytes(pass.outputBytes)}
            <div className="text-[10.5px] text-faint">
              {pass.durationMs >= 1000 ? `${(pass.durationMs / 1000).toFixed(1)}s` : `${pass.durationMs}ms`}
              {/* Which model actually read the events (D78): a pass that isn't on the project's
                  consolidation model is one that fell back to the chat's own session. */}
              {pass.model ? ` · ${pass.model}` : ' · the chat\u2019s model'}
            </div>
          </>
        )}
      </td>
    </tr>
  );
}

// --- Duplicate pairs -----------------------------------------------------------

// DuplicatePairsCard is the mechanical pass's other half (D76): near-duplicate
// candidates it finds and deliberately does not merge on its own, because two
// titles that rhyme aren't always the same fact — that judgement is a
// person's. GET …/memory/duplicates already reads both memories back and caps
// at MaxLimit, so there's nothing to page here that the route doesn't already
// bound.
//
// There is no route to say "these two aren't duplicates" without also
// superseding one of them — memory_duplicates has no flag for that, and the
// list is only ever rewritten whole by the next mechanical pass. So the only
// action here is supersede; a pair that genuinely isn't a duplicate stays
// listed until a later pass's own comparison drops it or the score changes.
function DuplicatePairsCard({ project }: { project: string }) {
  const queryClient = useQueryClient();
  const duplicates = useQuery({ queryKey: ['memoryDuplicates', project], queryFn: () => api.memoryDuplicates(project) });
  const pairs = duplicates.data ?? [];
  const [choice, setChoice] = useState<{ keep: T.Memory; replace: T.Memory } | null>(null);

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['memoryDuplicates', project] });
    await queryClient.invalidateQueries({ queryKey: ['memories', project] });
  };

  return (
    <Card
      title="Duplicate pairs"
      icon={GitCompare}
      description="What the free mechanical pass finds close enough in title to be the same memory, and leaves for a person to decide — merging two memories is a judgement, not a score."
    >
      {duplicates.error && <Notice>{errorMessage(duplicates.error)}</Notice>}
      {duplicates.isPending && <p className="text-[13px] text-subtle">Loading…</p>}
      {!duplicates.isPending && pairs.length === 0 && <p className="text-[13px] text-subtle">No candidates from the last mechanical pass.</p>}

      <div className="grid gap-3">
        {pairs.map((pair) => (
          <DuplicatePairRow key={`${pair.memory.id}-${pair.of.id}`} pair={pair} onChoose={(keep, replace) => setChoice({ keep, replace })} />
        ))}
      </div>

      {choice && (
        <AddMemoryDialog
          project={project}
          open
          onOpenChange={(open) => !open && setChoice(null)}
          supersedes={choice.replace}
          prefillFrom={choice.keep}
          onAdded={async () => {
            setChoice(null);
            await refresh();
          }}
        />
      )}
    </Card>
  );
}

function DuplicatePairRow({ pair, onChoose }: { pair: T.MemoryDuplicate; onChoose: (keep: T.Memory, replace: T.Memory) => void }) {
  return (
    <div className="panel rounded-2xl px-4 py-3" data-duplicate={`${pair.memory.id}-${pair.of.id}`}>
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-subtle">
        <Badge variant={kindVariant[pair.memory.kind] ?? 'default'}>{kindLabel[pair.memory.kind] ?? pair.memory.kind}</Badge>
        <span>{Math.round(pair.similarity * 100)}% alike in title</span>
        <span className="ml-auto">found {timeAgo(pair.foundAt)}</span>
      </div>
      <div className="mt-2 grid gap-2 sm:grid-cols-2">
        <DuplicateSide memory={pair.memory} onKeep={() => onChoose(pair.memory, pair.of)} />
        <DuplicateSide memory={pair.of} onKeep={() => onChoose(pair.of, pair.memory)} />
      </div>
    </div>
  );
}

function DuplicateSide({ memory, onKeep }: { memory: T.Memory; onKeep: () => void }) {
  return (
    <div className="grid gap-2 rounded-xl border border-line-faint bg-surface-faint p-3">
      <h4 className="text-[13px] font-medium text-primary">{memory.title}</h4>
      {memory.content && <Markdown text={memory.content} className="text-[12px] leading-relaxed text-muted" />}
      <div className="flex items-center justify-between gap-2 text-[11px] text-subtle">
        <span>{timeAgo(memory.createdAt)}</span>
        <Button size="sm" variant="ghost" onClick={onKeep}>
          Keep this, supersede the other
        </Button>
      </div>
    </div>
  );
}

// --- Preview -------------------------------------------------------------------

// ContextPreviewCard is the debugging view: what POST /context would actually
// hand an agent for a query, against the project's real budget, without
// starting one. It's the same call a worker's brief and the lead's recap make
// (For: "tool"), so what it shows is real — including that it adds itself to
// the build accounting above.
function ContextPreviewCard({ project, budget }: { project: string; budget?: number }) {
  const [query, setQuery] = useState('');
  const preview = useMutation({
    mutationFn: (q: string) => api.memoryContext(project, { query: q, for: A.ContextForTool } satisfies T.ContextRequest),
  });
  const sections = preview.data?.sections ?? [];
  const droppedSections = preview.data?.stats.droppedSections ?? [];

  return (
    <Card
      title="Preview a context"
      icon={FlaskConical}
      description="What an agent would actually be told for a query, built the same way the lead's recap and every worker's brief are — without starting one."
    >
      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          preview.mutate(query.trim());
        }}
      >
        <Input
          aria-label="Preview query"
          className="flex-1"
          placeholder="A task, a file, an error — empty asks for what the project says it's doing right now"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <Button type="submit" disabled={preview.isPending}>
          {preview.isPending ? 'Building…' : 'Preview'}
          <FlaskConical />
        </Button>
      </form>
      {budget !== undefined && <p className="mt-2 text-[11.5px] text-subtle">Built against the current budget, {formatTokens(budget)} tokens.</p>}

      {preview.error && <Notice className="mt-3">{errorMessage(preview.error)}</Notice>}

      {preview.data && (
        <div className="mt-4 grid gap-3">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px] text-muted">
            <span>
              <span className="text-primary">{formatTokens(preview.data.stats.tokens)}</span> of {formatTokens(preview.data.stats.budget)} tokens
            </span>
            <span>
              {preview.data.stats.rows} of {preview.data.stats.consideredRows} rows kept ({preview.data.stats.droppedRows} dropped)
            </span>
            <span>ratio {preview.data.stats.ratio.toFixed(2)}</span>
          </div>
          {preview.data.stats.truncated && (
            <Notice tone="warning">Even what a build never drops overflowed the budget, so the text below is cut on a word boundary.</Notice>
          )}
          {droppedSections.length > 0 && <p className="text-[11.5px] text-subtle">Given up to fit: {droppedSections.join(', ')}</p>}
          {sections.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {sections.map((section) => (
                <Badge key={section.kind}>
                  {section.title} · {section.rows} row{section.rows === 1 ? '' : 's'} · {formatTokens(section.tokens)}
                </Badge>
              ))}
            </div>
          )}
          <Markdown
            text={preview.data.text}
            className="max-h-96 overflow-auto rounded-xl border border-line-faint bg-rail p-3.5 text-[12.5px] leading-relaxed text-tertiary"
          />
        </div>
      )}
    </Card>
  );
}

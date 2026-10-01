import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Check, ListTodo, LoaderCircle, MessagesSquare, Pencil, Play, Plus, Trash2 } from 'lucide-react';
import { type ReactNode, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { leadOwner, taskActions, taskLane, taskLanes, taskTarget, taskText, type TaskTarget } from '../lib/tasks';
import { cn, errorMessage, humanBytes, timeAgo } from '../lib/utils';
import { Button } from './ui/button';
import { EmptyState, Notice, Panel } from './ui/card';
import { Textarea } from './ui/input';
import { Select, SelectOption } from './ui/select';

// ProjectTasksPanel is the project's task list, which only the user writes:
// nothing in AgentBox adds a task on its own, and neither the project's chat
// nor an agent can change one. Each task sits in the Backlog, where a new one
// goes, or in the Queue, which hands it to the agent queue as a queued agent
// (lib/tasks.ts); queued tasks can be moved up and down or sent back. With the
// agent queue off there is no Queue: a backlog task's Start makes its agent
// at once. A task goes to a new agent or to the project's lead, as Settings'
// "Tasks go to" says unless the task chose for itself; one the lead has shows
// the lead as its owner, a link to the chat.
export function ProjectTasksPanel({ project, onSelect, onOpenChat }: { project: string; onSelect: (view: View) => void; onOpenChat: () => void }) {
  const queryClient = useQueryClient();
  const tasksQuery = useQuery({ queryKey: ['memoryTasks', project], queryFn: () => api.memoryTasks(project) });
  // Refetching every few seconds keeps the slot count and each task's queue
  // position live even when nothing here triggers an event; the agent events
  // stream (queue invalidation in lib/events.ts) is what makes it feel instant.
  const queueQuery = useQuery({ queryKey: ['queue', project], queryFn: () => api.queue(project), refetchInterval: 5000 });
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const [showDone, setShowDone] = useState(false);
  const [prompt, setPrompt] = useState('');

  // The installation's own switch (Settings → Agents → Agent queue).
  const queueOn = settings.data?.agentQueue ?? false;
  const target = settings.data?.taskTarget;
  const slots = queueQuery.data?.projects.find((p) => p.project === project);
  const agentsByName = new Map((agentsQuery.data ?? []).filter((a) => a.project === project).map((a) => [a.name, a] as const));
  const lanes = taskLanes(tasksQuery.data ?? [], agentsByName);
  const total = tasksQuery.data?.length ?? 0;
  const actions = taskActions(api, project);

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['memoryTasks', project] });
    await queryClient.invalidateQueries({ queryKey: ['queue', project] });
    await queryClient.invalidateQueries({ queryKey: ['agents'] });
  };
  // Every action here is one call and a refresh, and fails the same way.
  const act = useMutation({
    mutationFn: (run: () => Promise<unknown>) => run(),
    onSettled: refresh,
    onError: (err) => toast.error(errorMessage(err)),
  });
  const addTask = useMutation({
    mutationFn: () => actions.add(prompt),
    onSuccess: async () => {
      setPrompt('');
      await refresh();
    },
  });

  // Only a task waiting for its agent moves in the queue: one waiting for the
  // lead keeps its place behind the agents queued before it.
  const agentQueue = lanes.queue.filter((t) => !t.leadQueuedAt);
  const row = (task: T.Task, index?: number) => {
    const agent = task.agent ? agentsByName.get(task.agent) : undefined;
    const moveIndex = agentQueue.indexOf(task);
    return (
      <TaskRow
        key={task.id}
        task={task}
        agent={agent}
        queueOn={queueOn}
        target={taskTarget(task, target)}
        defaultTarget={taskTarget({}, target)}
        busy={act.isPending}
        place={index}
        queueIndex={index === undefined || moveIndex < 0 ? undefined : moveIndex}
        queueLength={agentQueue.length}
        onRoute={(route) => act.mutate(() => actions.setRoute(task, route))}
        onOpenChat={onOpenChat}
        onLane={(lane) => act.mutate(() => actions.setLane(task, agent, lane))}
        onMove={(position) => agent && act.mutate(() => actions.move(agent.name, position))}
        onStart={() => act.mutate(() => actions.start(task, agent))}
        onEdit={(text) => act.mutateAsync(() => actions.edit(task, text))}
        onDelete={() => act.mutate(() => actions.remove(task))}
        onDone={() => act.mutate(() => actions.markDone(task))}
        onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })}
      />
    );
  };

  return (
    <div className="mx-auto grid max-w-4xl gap-5 px-4 py-6 md:px-8 md:py-7">
      {queueOn ? (
        <SlotsStrip slots={slots} loading={queueQuery.isPending} />
      ) : (
        settings.data && (
          <p className="px-1 text-[12px] text-subtle">
            Agent queue is off: a task's Start {target === 'lead' ? 'sends it to the lead' : 'makes its agent'} right away. Turn the queue on in Settings to queue tasks instead.
          </p>
        )
      )}
      {settings.data && (
        <p className="-mt-3 px-1 text-[12px] text-subtle" data-task-target={target}>
          Tasks go to {target === 'lead' ? "the project's lead, which can split one across several agents" : 'a new agent each'}, unless a task says otherwise. Change it in Settings.
        </p>
      )}

      <form
        className="grid gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (prompt.split('\n')[0].trim()) addTask.mutate();
        }}
      >
        <Textarea
          aria-label="New task"
          className="min-h-20"
          placeholder="What needs doing? The first line is the goal; anything after it is the brief."
          value={prompt}
          onChange={(event) => setPrompt(event.target.value)}
        />
        <div className="flex items-center justify-end gap-2">
          {addTask.error && <Notice className="mr-auto">{errorMessage(addTask.error)}</Notice>}
          <Button type="submit" variant="primary" size="sm" disabled={!prompt.split('\n')[0].trim() || addTask.isPending}>
            {addTask.isPending ? <LoaderCircle className="animate-spin" /> : <Plus />}
            Add to backlog
          </Button>
        </div>
      </form>

      {tasksQuery.error && <Notice>{errorMessage(tasksQuery.error)}</Notice>}
      {!tasksQuery.isPending && total === 0 && (
        <Panel className="rounded-2xl">
          <EmptyState icon={ListTodo} title="No tasks yet">
            This list is yours: write the next thing to do above. Nothing adds tasks here on its own.
          </EmptyState>
        </Panel>
      )}

      {queueOn && (lanes.queue.length > 0 || total > 0) && (
        <Lane title="Queue" count={lanes.queue.length} hint="Starts in this order as slots free up.">
          {lanes.queue.length === 0 ? <p className="px-1 text-[12px] text-faint">Nothing queued. Move a backlog task to the queue to hand it to an agent.</p> : lanes.queue.map((t, i) => row(t, i))}
        </Lane>
      )}
      {!queueOn && lanes.queue.length > 0 && (
        <Lane title="Queue" count={lanes.queue.length} hint="Left from when the agent queue was on: these start at once.">
          {lanes.queue.map((t, i) => row(t, i))}
        </Lane>
      )}
      {lanes.running.length > 0 && (
        <Lane title="In progress" count={lanes.running.length}>
          {lanes.running.map((t) => row(t))}
        </Lane>
      )}
      {lanes.backlog.length > 0 && (
        <Lane title="Backlog" count={lanes.backlog.length}>
          {lanes.backlog.map((t) => row(t))}
        </Lane>
      )}
      {lanes.done.length > 0 && (
        <div className="grid gap-2">
          <button type="button" className="justify-self-start px-1 text-[12px] text-subtle underline-offset-2 hover:text-tertiary hover:underline" onClick={() => setShowDone((v) => !v)}>
            {showDone ? 'Hide done' : `Show done (${lanes.done.length})`}
          </button>
          {showDone && lanes.done.map((t) => row(t))}
        </div>
      )}
    </div>
  );
}

function Lane({ title, count, hint, children }: { title: string; count: number; hint?: string; children: ReactNode }) {
  return (
    <section className="grid gap-2" data-task-lane={title.toLowerCase()}>
      <div className="flex items-baseline gap-2 px-1">
        <h3 className="text-[12.5px] font-semibold text-secondary">{title}</h3>
        <span className="text-[11.5px] text-faint">{count}</span>
        {hint && <span className="ml-auto text-[11.5px] text-subtle">{hint}</span>}
      </div>
      {children}
    </section>
  );
}

// SlotsStrip is a project's queue at a glance: how many of its slots are in
// use, how many agents are waiting for one, and — when it's Auto — what each
// one is estimated to cost, learned from this project's own agents once it
// has run any, else the installation's memory limit.
function SlotsStrip({ slots, loading }: { slots: T.ProjectSlots | undefined; loading: boolean }) {
  if (!slots) {
    return <p className="px-1 text-[12px] text-subtle">{loading ? 'Loading the queue…' : ''}</p>;
  }
  const auto = slots.pinned === 0;
  return (
    <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1 rounded-xl border border-line bg-surface-faint px-3.5 py-2.5 text-[12.5px] text-secondary">
      <span className="font-medium text-primary">
        {slots.running} of {slots.slots} slot{slots.slots === 1 ? '' : 's'} in use
      </span>
      <span className="text-subtle">·</span>
      <span>{slots.queued} queued</span>
      <span className="text-subtle">·</span>
      <span className="text-subtle">
        {auto ? `auto, ~${humanBytes(slots.peak)} per agent${slots.peakLearned ? '' : ' (from the memory limit)'}` : `fixed at ${slots.slots}`}
      </span>
    </div>
  );
}

// LaneChoice is a task's Backlog | Queue switch: two buttons, the current one
// pressed.
function LaneChoice({ lane, disabled, onChange, goal }: { lane: 'backlog' | 'queue'; disabled: boolean; onChange: (lane: 'backlog' | 'queue') => void; goal: string }) {
  return (
    <div role="radiogroup" aria-label={`Where “${goal}” waits`} className="inline-flex rounded-lg border border-line bg-surface-faint p-0.5">
      {(['backlog', 'queue'] as const).map((option) => (
        <button
          key={option}
          type="button"
          role="radio"
          aria-checked={lane === option}
          disabled={disabled}
          onClick={() => lane !== option && onChange(option)}
          className={cn(
            'rounded-md px-2.5 py-1 text-[12px] font-medium transition-colors disabled:opacity-50',
            lane === option ? 'bg-surface-strong text-primary shadow-sm' : 'text-subtle hover:text-secondary',
          )}
        >
          {option === 'backlog' ? 'Backlog' : 'Queue'}
        </button>
      ))}
    </div>
  );
}

// RouteChoice is where a backlog task goes when it starts: wherever Settings'
// "Tasks go to" says, which is every new task's, or a new agent or the lead
// whatever that says.
function RouteChoice({
  route,
  target,
  defaultTarget,
  goal,
  disabled,
  onChange,
}: {
  route: string | undefined;
  target: TaskTarget;
  defaultTarget: TaskTarget;
  goal: string;
  disabled: boolean;
  onChange: (route: '' | TaskTarget) => void;
}) {
  const value = route === 'agent' || route === 'lead' ? route : '';
  return (
    <Select
      aria-label={`Where “${goal}” goes`}
      data-task-route={value || 'follow'}
      data-task-target={target}
      className="h-8 w-auto text-[12px]"
      value={value} disabled={disabled} onChange={(next) => next !== value && onChange(next as '' | TaskTarget)}
    >
      <SelectOption value="">Follow setting ({defaultTarget === 'lead' ? 'the lead' : 'a new agent'})</SelectOption>
      <SelectOption value="agent">A new agent</SelectOption>
      <SelectOption value="lead">The lead</SelectOption>
    </Select>
  );
}

function TaskRow({
  task,
  agent,
  queueOn,
  target,
  defaultTarget,
  busy,
  place,
  queueIndex,
  queueLength,
  onRoute,
  onOpenChat,
  onLane,
  onMove,
  onStart,
  onEdit,
  onDelete,
  onDone,
  onOpenAgent,
}: {
  task: T.Task;
  agent: T.Agent | undefined;
  queueOn: boolean;
  target: TaskTarget;
  defaultTarget: TaskTarget;
  busy: boolean;
  place?: number;
  queueIndex?: number;
  queueLength: number;
  onRoute: (route: '' | TaskTarget) => void;
  onOpenChat: () => void;
  onLane: (lane: 'backlog' | 'queue') => void;
  onMove: (position: number) => void;
  onStart: () => void;
  onEdit: (text: string) => Promise<unknown>;
  onDelete: () => void;
  onDone: () => void;
  onOpenAgent: (ref: string) => void;
}) {
  const lane = taskLane(task, agent);
  const [editing, setEditing] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (editing !== null) {
    return (
      <form
        className="panel grid gap-2 rounded-2xl px-4 py-3"
        data-task={task.id}
        onSubmit={async (event) => {
          event.preventDefault();
          if (!editing.split('\n')[0].trim()) return;
          await onEdit(editing).then(
            () => setEditing(null),
            () => {},
          );
        }}
      >
        <Textarea aria-label={`Edit “${task.goal}”`} className="min-h-20" value={editing} onChange={(event) => setEditing(event.target.value)} autoFocus />
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>
            Cancel
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={busy || !editing.split('\n')[0].trim()}>
            Save
          </Button>
        </div>
      </form>
    );
  }

  return (
    <div className="panel flex flex-wrap items-start gap-3 rounded-2xl px-4 py-3" data-task={task.id} data-task-status={task.status}>
      {lane === 'queue' && <span className="mt-0.5 w-6 shrink-0 text-center text-[12.5px] font-semibold text-subtle tabular-nums">#{(place ?? 0) + 1}</span>}
      <div className="min-w-0 flex-1">
        <p className={cn('text-[13.5px] font-medium text-primary', lane === 'done' && 'text-subtle line-through')}>{task.goal}</p>
        {task.detail && <p className="mt-1 line-clamp-2 whitespace-pre-line text-[12px] text-subtle">{task.detail}</p>}
        <p className="mt-1 text-[11px] text-faint">
          {timeAgo(task.createdAt)}
          {lane === 'queue' && task.leadQueuedAt && ' · for the lead'}
          {lane === 'running' && task.agent === leadOwner && (
            <>
              {' · '}
              <button type="button" className="inline-flex items-center gap-1 text-tertiary underline-offset-2 hover:text-primary hover:underline" onClick={onOpenChat}>
                <MessagesSquare className="size-3" />
                lead
              </button>
            </>
          )}
          {lane === 'running' && agent && task.agent !== leadOwner && (
            <>
              {' · '}
              <button type="button" className="text-tertiary underline-offset-2 hover:text-primary hover:underline" onClick={() => onOpenAgent(`${task.project}/${agent.name}`)}>
                {agent.title || agent.name}
              </button>
            </>
          )}
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        {lane === 'backlog' && <RouteChoice route={task.route} target={target} defaultTarget={defaultTarget} goal={task.goal} disabled={busy} onChange={onRoute} />}
        {lane === 'queue' && queueIndex !== undefined && (
          <>
            <Button size="icon-sm" variant="ghost" aria-label={`Move “${task.goal}” up`} disabled={busy || queueIndex === 0} onClick={() => onMove(queueIndex)}>
              <ArrowUp />
            </Button>
            <Button size="icon-sm" variant="ghost" aria-label={`Move “${task.goal}” down`} disabled={busy || queueIndex >= queueLength - 1} onClick={() => onMove(queueIndex + 2)}>
              <ArrowDown />
            </Button>
          </>
        )}
        {(lane === 'backlog' || lane === 'queue') && queueOn && <LaneChoice lane={lane} goal={task.goal} disabled={busy} onChange={onLane} />}
        {lane === 'queue' && !queueOn && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => onLane('backlog')}>
            Back to backlog
          </Button>
        )}
        {lane === 'backlog' && !queueOn && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={onStart}>
            <Play />
            Start
          </Button>
        )}
        {lane === 'running' && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={onDone}>
            <Check />
            Done
          </Button>
        )}
        <Button size="icon-sm" variant="ghost" aria-label={`Edit “${task.goal}”`} disabled={busy} onClick={() => setEditing(taskText(task))}>
          <Pencil />
        </Button>
        {confirmDelete ? (
          <>
            <Button size="sm" variant="danger" disabled={busy} onClick={onDelete}>
              Delete
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>
              Keep
            </Button>
          </>
        ) : (
          <Button size="icon-sm" variant="ghost" aria-label={`Delete “${task.goal}”`} disabled={busy} onClick={() => setConfirmDelete(true)}>
            <Trash2 />
          </Button>
        )}
      </div>
    </div>
  );
}

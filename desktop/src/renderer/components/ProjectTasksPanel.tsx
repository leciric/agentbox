import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Check, GitMerge, ListTodo, LoaderCircle, Pencil, Play, Plus, RotateCcw, Trash2 } from 'lucide-react';
import { type ReactNode, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { type TaskList, taskActions, taskLane, taskLanes, taskListCounts, taskOutcome, taskText } from '../lib/tasks';
import { cn, errorMessage, humanBytes, timeAgo } from '../lib/utils';
import { Button } from './ui/button';
import { EmptyState, Notice, Panel } from './ui/card';
import { Textarea } from './ui/input';

// ProjectTasksPanel is the project's task list, which only the user writes:
// nothing in AgentBox adds a task on its own, and neither the project's chat
// nor an agent can change one. Each task sits in the Backlog, where a new one
// goes, or in the Queue, which hands it to the agent queue as a queued agent
// (lib/tasks.ts); queued tasks can be moved up and down or sent back. With the
// agent queue off there is no Queue: a backlog task's Start makes its agent
// at once. What's done — by hand, or by its agent's pull request merging — is
// a second list, switched to above the lanes.
export function ProjectTasksPanel({ project, onSelect }: { project: string; onSelect: (view: View) => void }) {
  const queryClient = useQueryClient();
  const tasksQuery = useQuery({ queryKey: ['memoryTasks', project], queryFn: () => api.memoryTasks(project) });
  // Refetching every few seconds keeps the slot count and each task's queue
  // position live even when nothing here triggers an event; the agent events
  // stream (queue invalidation in lib/events.ts) is what makes it feel instant.
  const queueQuery = useQuery({ queryKey: ['queue', project], queryFn: () => api.queue(project), refetchInterval: 5000 });
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const [list, setList] = useState<TaskList>('open');
  const [prompt, setPrompt] = useState('');

  // The installation's own switch (Settings → Agents → Agent queue).
  const queueOn = settings.data?.agentQueue ?? false;
  const slots = queueQuery.data?.projects.find((p) => p.project === project);
  const agentsByName = new Map((agentsQuery.data ?? []).filter((a) => a.project === project).map((a) => [a.name, a] as const));
  const lanes = taskLanes(tasksQuery.data ?? [], agentsByName);
  const total = tasksQuery.data?.length ?? 0;
  const counts = taskListCounts(lanes);
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

  const row = (task: T.Task, index?: number) => {
    const agent = task.agent ? agentsByName.get(task.agent) : undefined;
    return (
      <TaskRow
        key={task.id}
        task={task}
        agent={agent}
        queueOn={queueOn}
        busy={act.isPending}
        queueIndex={index}
        queueLength={lanes.queue.length}
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
        settings.data && <p className="px-1 text-[12px] text-subtle">Agent queue is off: a task's Start makes its agent right away. Turn the queue on in Settings to queue tasks instead.</p>
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

      {total > 0 && <ListChoice list={list} counts={counts} onChange={setList} />}

      {list === 'open' && total > 0 && counts.open === 0 && <p className="px-1 text-[12px] text-faint">Nothing left to do. Write the next task above.</p>}
      {list === 'open' && queueOn && (lanes.queue.length > 0 || total > 0) && (
        <Lane title="Queue" count={lanes.queue.length} hint="Starts in this order as slots free up.">
          {lanes.queue.length === 0 ? <p className="px-1 text-[12px] text-faint">Nothing queued. Move a backlog task to the queue to hand it to an agent.</p> : lanes.queue.map((t, i) => row(t, i))}
        </Lane>
      )}
      {list === 'open' && !queueOn && lanes.queue.length > 0 && (
        <Lane title="Queue" count={lanes.queue.length} hint="Left from when the agent queue was on: these start at once.">
          {lanes.queue.map((t, i) => row(t, i))}
        </Lane>
      )}
      {list === 'open' && lanes.running.length > 0 && (
        <Lane title="In progress" count={lanes.running.length}>
          {lanes.running.map((t) => row(t))}
        </Lane>
      )}
      {list === 'open' && lanes.backlog.length > 0 && (
        <Lane title="Backlog" count={lanes.backlog.length}>
          {lanes.backlog.map((t) => row(t))}
        </Lane>
      )}
      {list === 'done' && total > 0 && (
        <section className="grid gap-2" data-task-lane="done">
          {lanes.done.length === 0 ? (
            <p className="px-1 text-[12px] text-faint">Nothing done yet. Mark a task done from its row, or it moves here when its agent's pull request merges.</p>
          ) : (
            lanes.done.map((t) => (
              <DoneRow
                key={t.id}
                task={t}
                busy={act.isPending}
                onReopen={() => act.mutate(() => actions.reopen(t))}
                onDelete={() => act.mutate(() => actions.remove(t))}
              />
            ))
          )}
        </section>
      )}
    </div>
  );
}

// ListChoice is the tab's Open | Done switch, each with its count.
function ListChoice({ list, counts, onChange }: { list: TaskList; counts: Record<TaskList, number>; onChange: (list: TaskList) => void }) {
  return (
    <div role="tablist" aria-label="Tasks" className="inline-flex justify-self-start rounded-lg border border-line bg-surface-faint p-0.5">
      {(['open', 'done'] as const).map((option) => (
        <button
          key={option}
          type="button"
          role="tab"
          aria-selected={list === option}
          onClick={() => onChange(option)}
          className={cn(
            'flex items-baseline gap-1.5 rounded-md px-3 py-1 text-[12.5px] font-medium transition-colors',
            list === option ? 'bg-surface-strong text-primary shadow-sm' : 'text-subtle hover:text-secondary',
          )}
        >
          {option === 'open' ? 'Open' : 'Done'}
          <span className="text-[11.5px] font-normal text-faint tabular-nums">{counts[option]}</span>
        </button>
      ))}
    </div>
  );
}

// DoneRow is a task in the Done list: how it ended — done by hand,
// implemented by a merged pull request, or abandoned — and when, with Reopen
// to put it back in the backlog.
function DoneRow({ task, busy, onReopen, onDelete }: { task: T.Task; busy: boolean; onReopen: () => void; onDelete: () => void }) {
  const outcome = taskOutcome(task);
  const [confirmDelete, setConfirmDelete] = useState(false);
  return (
    <div className="panel flex flex-wrap items-start gap-3 rounded-2xl px-4 py-3" data-task={task.id} data-task-status={task.status} data-task-outcome={outcome.kind}>
      <div className="min-w-0 flex-1">
        <p className={cn('text-[13.5px] font-medium text-secondary', outcome.kind === 'abandoned' && 'text-subtle line-through')}>{task.goal}</p>
        {task.detail && <p className="mt-1 line-clamp-2 whitespace-pre-line text-[12px] text-subtle">{task.detail}</p>}
        <p className="mt-1 flex flex-wrap items-center gap-x-1 text-[11px] text-faint">
          {outcome.kind === 'implemented' ? (
            <>
              <GitMerge className="size-3 text-brand-300" />
              <span className="text-tertiary">Implemented in</span>
              <a href={outcome.url} target="_blank" rel="noreferrer" className="text-tertiary underline underline-offset-2 hover:text-primary">
                #{outcome.number}
              </a>
            </>
          ) : outcome.kind === 'abandoned' ? (
            <span className="text-tertiary">Abandoned</span>
          ) : (
            <>
              <Check className="size-3" />
              <span className="text-tertiary">Done by hand</span>
            </>
          )}
          <span>· {timeAgo(task.closedAt ?? task.updatedAt)}</span>
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        <Button size="sm" variant="ghost" disabled={busy} onClick={onReopen}>
          <RotateCcw />
          Reopen
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

function TaskRow({
  task,
  agent,
  queueOn,
  busy,
  queueIndex,
  queueLength,
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
  busy: boolean;
  queueIndex?: number;
  queueLength: number;
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
      {lane === 'queue' && <span className="mt-0.5 w-6 shrink-0 text-center text-[12.5px] font-semibold text-subtle tabular-nums">#{agent?.queuePosition ?? (queueIndex ?? 0) + 1}</span>}
      <div className="min-w-0 flex-1">
        <p className={cn('text-[13.5px] font-medium text-primary', lane === 'done' && 'text-subtle line-through')}>{task.goal}</p>
        {task.detail && <p className="mt-1 line-clamp-2 whitespace-pre-line text-[12px] text-subtle">{task.detail}</p>}
        <p className="mt-1 text-[11px] text-faint">
          {timeAgo(task.createdAt)}
          {lane === 'running' && agent && (
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
        {lane !== 'done' && (
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

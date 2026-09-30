import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ListTodo, LoaderCircle, Play, Plus } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { errorMessage, humanBytes, timeAgo } from '../lib/utils';
import { taskOpenStatuses, taskStatusLabel, taskStatusVariant } from './ProjectMemoryPanel';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { EmptyState, Notice, Panel } from './ui/card';
import { Textarea } from './ui/input';
import { Switch } from './ui/switch';

// ProjectTasksPanel is a project's plan, worked from the queue: what's still
// to do, and one switch per task to send it to a slot — the same queue create
// (POST /v1/agents with queue: true, taskId) and remove (DELETE
// /v1/queue/…) NewAgentDialog and the rail's context menu already use. It
// doesn't replace the Memory tab's own Tasks section, which is the whole plan
// as a tree with subtasks and blockers (ProjectMemoryPanel.tsx); this is the
// flat, queue-focused view: what's open, who's on it or queued for it, and a
// quick way to write the next one.
export function ProjectTasksPanel({ project, onSelect }: { project: string; onSelect: (view: View) => void }) {
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

  // The installation's own switch (Settings → Agents → Agent queue). Off,
  // there's no queue to show or add to: the strip disappears, and a task's
  // toggle starts an agent right away instead of offering to queue it.
  const queueOn = settings.data?.agentQueue ?? false;
  const tasks = tasksQuery.data ?? [];
  const slots = queueQuery.data?.projects.find((p) => p.project === project);
  const agentsByName = new Map((agentsQuery.data ?? []).filter((a) => a.project === project).map((a) => [a.name, a] as const));

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['memoryTasks', project] });
    await queryClient.invalidateQueries({ queryKey: ['queue', project] });
  };

  const addTask = useMutation({
    mutationFn: () => {
      // The first line is what a task list is scanned by; anything after it
      // is the brief behind it, same as Memory's Add task dialog does with
      // separate fields, folded into one box here since this is meant to be
      // quick.
      const lines = prompt.split('\n');
      const goal = lines[0].trim();
      const detail = lines.slice(1).join('\n').trim();
      return api.addTask(project, { goal, detail: detail || undefined } satisfies T.AddTaskRequest);
    },
    onSuccess: async () => {
      setPrompt('');
      await refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  const queueTask = useMutation({
    mutationFn: (task: T.Task) => api.createAgent({ project, taskId: task.id, queue: true, ai: 'claude' }),
    onSuccess: async () => {
      await refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  const unqueueTask = useMutation({
    mutationFn: (agentName: string) => api.removeQueued(project, agentName),
    onSuccess: async () => {
      await refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  // Agent queue off: a task starts an agent right away, taskId and all, just
  // without the queue flag — the same create NewAgentDialog sends unqueued.
  const startTask = useMutation({
    mutationFn: (task: T.Task) => api.createAgent({ project, taskId: task.id, ai: 'claude' }),
    onSuccess: async () => {
      await refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  const visible = tasks.filter((t) => showDone || taskOpenStatuses.has(t.status));
  const doneCount = tasks.length - tasks.filter((t) => taskOpenStatuses.has(t.status)).length;

  return (
    <div className="mx-auto grid max-w-4xl gap-5 px-4 py-6 md:px-8 md:py-7">
      {queueOn ? (
        <SlotsStrip slots={slots} loading={queueQuery.isPending} />
      ) : (
        settings.data && <p className="px-1 text-[12px] text-subtle">Agent queue is off: a task starts an agent right away. Turn it on in Settings to queue them instead.</p>
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
            Add task
          </Button>
        </div>
      </form>

      <div className="flex items-center gap-2">
        <span className="text-[11.5px] text-subtle">
          {tasks.length === 0 ? 'Nothing planned yet' : `${visible.length} of ${tasks.length} task${tasks.length === 1 ? '' : 's'}`}
        </span>
        {doneCount > 0 && (
          <button type="button" className="ml-auto text-[12px] text-subtle underline-offset-2 hover:text-tertiary hover:underline" onClick={() => setShowDone((v) => !v)}>
            {showDone ? 'Hide done' : `Show done (${doneCount})`}
          </button>
        )}
      </div>

      {tasksQuery.error && <Notice>{errorMessage(tasksQuery.error)}</Notice>}
      {!tasksQuery.isPending && tasks.length === 0 && (
        <Panel className="rounded-2xl">
          <EmptyState icon={ListTodo} title="No plan yet">
            Write the next thing to do above, or let the project's chat add tasks as it hands work to agents.
          </EmptyState>
        </Panel>
      )}

      <div className="grid gap-2">
        {visible.map((task) => (
          <QueueTaskRow
            key={task.id}
            task={task}
            agent={task.agent ? agentsByName.get(task.agent) : undefined}
            queueOn={queueOn}
            onQueue={() => queueTask.mutate(task)}
            onUnqueue={(agentName) => unqueueTask.mutate(agentName)}
            onStart={() => startTask.mutate(task)}
            onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })}
            queuing={queueTask.isPending}
            unqueuing={unqueueTask.isPending}
            starting={startTask.isPending}
          />
        ))}
      </div>
    </div>
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

function QueueTaskRow({
  task,
  agent,
  queueOn,
  onQueue,
  onUnqueue,
  onStart,
  onOpenAgent,
  queuing,
  unqueuing,
  starting,
}: {
  task: T.Task;
  agent: T.Agent | undefined;
  queueOn: boolean;
  onQueue: () => void;
  onUnqueue: (agentName: string) => void;
  onStart: () => void;
  onOpenAgent: (ref: string) => void;
  queuing: boolean;
  unqueuing: boolean;
  starting: boolean;
}) {
  // Queued agents that exist — made before Agent queue was turned off, say —
  // still show their place in line either way; only the toggle that would
  // make a new one changes with the setting.
  const queued = agent?.state === 'queued';
  const running = !!task.agent && !!agent && !queued;
  const assigned = !!task.agent;

  return (
    <div className="panel flex flex-wrap items-start gap-3 rounded-2xl px-4 py-3" data-queue-task={task.id} data-task-status={task.status}>
      <Badge variant={taskStatusVariant[task.status] ?? 'default'}>{taskStatusLabel[task.status] ?? task.status}</Badge>
      <div className="min-w-0 flex-1">
        <p className="text-[13.5px] font-medium text-primary">{task.goal}</p>
        {task.detail && <p className="mt-1 truncate text-[12px] text-subtle">{task.detail}</p>}
        <p className="mt-1 text-[11px] text-faint">{timeAgo(task.createdAt)}</p>
      </div>
      <div className="flex shrink-0 items-center gap-2.5">
        {running && (
          <button type="button" className="text-[12.5px] text-tertiary underline-offset-2 hover:text-primary hover:underline" onClick={() => onOpenAgent(`${task.project}/${task.agent}`)}>
            {agent.title || agent.name}
          </button>
        )}
        {queued && <span className="text-[12.5px] text-subtle">Queued #{agent.queuePosition ?? '?'}</span>}
        {queued && !queueOn && (
          <button
            type="button"
            className="text-[11.5px] text-subtle underline-offset-2 hover:text-rose-300 hover:underline"
            disabled={unqueuing}
            onClick={() => task.agent && onUnqueue(task.agent)}
          >
            Remove
          </button>
        )}
        {queueOn && (
          <Switch
            aria-label={assigned ? `Unqueue ${task.goal}` : `Queue ${task.goal}`}
            checked={assigned}
            disabled={running || queuing || unqueuing}
            onCheckedChange={(on) => {
              if (on) onQueue();
              else if (task.agent) onUnqueue(task.agent);
            }}
          />
        )}
        {!queueOn && !assigned && (
          <Button size="sm" variant="ghost" disabled={starting} onClick={onStart}>
            {starting ? <LoaderCircle className="animate-spin" /> : <Play />}
            Start
          </Button>
        )}
      </div>
    </div>
  );
}

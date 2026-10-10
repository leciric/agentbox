import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Check,
  GitMerge,
  ListTodo,
  LoaderCircle,
  MessagesSquare,
  Pencil,
  Play,
  Plus,
  RotateCcw,
  Trash2,
} from 'lucide-react';
import { type ReactNode, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import {
  leadOwner,
  type TaskList,
  taskActions,
  taskLane,
  taskLanes,
  taskListCounts,
  taskOutcome,
  taskTarget,
  taskText,
  type TaskTarget,
} from '../lib/tasks';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { Button } from './ui/button';
import { EmptyState, Notice, Panel } from './ui/card';
import { Textarea } from './ui/input';
import { Select, SelectOption } from './ui/select';

// ProjectTasksPanel is the project's task list, which only the user writes:
// nothing in AgentBox adds a task on its own, and neither the project's chat
// nor an agent can change one. Each task sits in the Backlog, where a new one
// goes, or in the Queue (lib/tasks.ts). There is no agent queue any more, so a
// backlog task's Start makes its agent at once; the Queue only holds what an
// earlier release left there. A task goes to a new agent or to the project's lead, as Settings'
// "Tasks go to" says unless the task chose for itself; one the lead has shows
// the lead as its owner, a link to the chat. What's done — by hand, or by its
// agent's pull request merging — is a second list, switched to above the
// lanes.
export function ProjectTasksPanel({
  project,
  onSelect,
  onOpenChat,
}: {
  project: string;
  onSelect: (view: View) => void;
  onOpenChat: () => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const tasksQuery = useQuery({ queryKey: ['memoryTasks', project], queryFn: () => api.memoryTasks(project) });
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const [list, setList] = useState<TaskList>('open');
  const [prompt, setPrompt] = useState('');

  // There is no agent queue to switch on: the lane switch stays in the code
  // until the Tasks tab is reworked.
  const queueOn = false;
  const target = settings.data?.taskTarget;
  const agentsByName = new Map((agentsQuery.data ?? []).filter((a) => a.project === project).map((a) => [a.name, a] as const));
  const lanes = taskLanes(tasksQuery.data ?? [], agentsByName);
  const total = tasksQuery.data?.length ?? 0;
  const counts = taskListCounts(lanes);
  const actions = taskActions(api, project);

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['memoryTasks', project] });
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
        target={taskTarget(task, target)}
        defaultTarget={taskTarget({}, target)}
        busy={act.isPending}
        place={index}
        onRoute={(route) => act.mutate(() => actions.setRoute(task, route))}
        onOpenChat={onOpenChat}
        onLane={(lane) => act.mutate(() => actions.setLane(task, agent, lane))}
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
      {settings.data && (
        <p className="-mt-3 px-1 text-[12px] text-subtle" data-task-target={target}>
          {t('memory.tasks.goTo', { target: target === 'lead' ? 'lead' : 'agent' })}
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
          aria-label={t('memory.tasks.new')}
          className="min-h-20"
          placeholder={t('memory.tasks.placeholder')}
          value={prompt}
          onChange={(event) => setPrompt(event.target.value)}
        />
        <div className="flex items-center justify-end gap-2">
          {addTask.error && <Notice className="mr-auto">{errorMessage(addTask.error)}</Notice>}
          <Button type="submit" variant="primary" size="sm" disabled={!prompt.split('\n')[0].trim() || addTask.isPending}>
            {addTask.isPending ? <LoaderCircle className="animate-spin" /> : <Plus />}
            {t('memory.tasks.addToBacklog')}
          </Button>
        </div>
      </form>

      {tasksQuery.error && <Notice>{errorMessage(tasksQuery.error)}</Notice>}
      {!tasksQuery.isPending && total === 0 && (
        <Panel className="rounded-2xl">
          <EmptyState icon={ListTodo} title={t('memory.tasks.empty.title')}>
            {t('memory.tasks.empty.body')}
          </EmptyState>
        </Panel>
      )}

      {total > 0 && <ListChoice list={list} counts={counts} onChange={setList} />}

      {list === 'open' && total > 0 && counts.open === 0 && <p className="px-1 text-[12px] text-faint">{t('memory.tasks.nothingLeft')}</p>}
      {list === 'open' && queueOn && (lanes.queue.length > 0 || total > 0) && (
        <Lane id="queue" title={t('memory.tasks.lane.queue')} count={lanes.queue.length} hint={t('memory.tasks.lane.queueHint')}>
          {lanes.queue.length === 0 ? <p className="px-1 text-[12px] text-faint">{t('memory.tasks.lane.queueEmpty')}</p> : lanes.queue.map((q, i) => row(q, i))}
        </Lane>
      )}
      {list === 'open' && !queueOn && lanes.queue.length > 0 && (
        <Lane id="queue" title={t('memory.tasks.lane.queue')} count={lanes.queue.length} hint={t('memory.tasks.lane.queueStale')}>
          {lanes.queue.map((q, i) => row(q, i))}
        </Lane>
      )}
      {list === 'open' && lanes.running.length > 0 && (
        <Lane id="in progress" title={t('memory.tasks.lane.running')} count={lanes.running.length}>
          {lanes.running.map((q) => row(q))}
        </Lane>
      )}
      {list === 'open' && lanes.backlog.length > 0 && (
        <Lane id="backlog" title={t('memory.tasks.lane.backlog')} count={lanes.backlog.length}>
          {lanes.backlog.map((q) => row(q))}
        </Lane>
      )}
      {list === 'done' && total > 0 && (
        <section className="grid gap-2" data-task-lane="done">
          {lanes.done.length === 0 ? (
            <p className="px-1 text-[12px] text-faint">{t('memory.tasks.doneEmpty')}</p>
          ) : (
            lanes.done.map((d) => (
              <DoneRow
                key={d.id}
                task={d}
                busy={act.isPending}
                onReopen={() => act.mutate(() => actions.reopen(d))}
                onDelete={() => act.mutate(() => actions.remove(d))}
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
  const t = useT();
  return (
    <div role="tablist" aria-label={t('memory.tasks.title')} className="inline-flex justify-self-start rounded-lg border border-line bg-surface-faint p-0.5">
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
          {option === 'open' ? t('common.open') : t('common.done')}
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
  const t = useT();
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
              <span className="text-tertiary">{t('memory.tasks.implementedIn')}</span>
              <a href={outcome.url} target="_blank" rel="noreferrer" className="text-tertiary underline underline-offset-2 hover:text-primary">
                #{outcome.number}
              </a>
            </>
          ) : outcome.kind === 'abandoned' ? (
            <span className="text-tertiary">{t('memory.tasks.abandoned')}</span>
          ) : (
            <>
              <Check className="size-3" />
              <span className="text-tertiary">{t('memory.tasks.doneByHand')}</span>
            </>
          )}
          <span>· {timeAgo(task.closedAt ?? task.updatedAt)}</span>
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        <Button size="sm" variant="ghost" disabled={busy} onClick={onReopen}>
          <RotateCcw />
          {t('memory.tasks.reopen')}
        </Button>
        {confirmDelete ? (
          <>
            <Button size="sm" variant="danger" disabled={busy} onClick={onDelete}>
              {t('common.delete')}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>
              {t('memory.tasks.keep')}
            </Button>
          </>
        ) : (
          <Button size="icon-sm" variant="ghost" aria-label={t('memory.tasks.deleteTask', { goal: task.goal })} disabled={busy} onClick={() => setConfirmDelete(true)}>
            <Trash2 />
          </Button>
        )}
      </div>
    </div>
  );
}

function Lane({ id, title, count, hint, children }: { id: string; title: string; count: number; hint?: string; children: ReactNode }) {
  return (
    <section className="grid gap-2" data-task-lane={id}>
      <div className="flex items-baseline gap-2 px-1">
        <h3 className="text-[12.5px] font-semibold text-secondary">{title}</h3>
        <span className="text-[11.5px] text-faint">{count}</span>
        {hint && <span className="ml-auto text-[11.5px] text-subtle">{hint}</span>}
      </div>
      {children}
    </section>
  );
}

// LaneChoice is a task's Backlog | Queue switch: two buttons, the current one
// pressed.
function LaneChoice({ lane, disabled, onChange, goal }: { lane: 'backlog' | 'queue'; disabled: boolean; onChange: (lane: 'backlog' | 'queue') => void; goal: string }) {
  const t = useT();
  return (
    <div role="radiogroup" aria-label={t('memory.tasks.waits', { goal })} className="inline-flex rounded-lg border border-line bg-surface-faint p-0.5">
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
          {option === 'backlog' ? t('memory.tasks.lane.backlog') : t('memory.tasks.lane.queue')}
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
  const t = useT();
  const value = route === 'agent' || route === 'lead' ? route : '';
  return (
    <Select
      aria-label={t('memory.tasks.goes', { goal })}
      data-task-route={value || 'follow'}
      data-task-target={target}
      className="h-8 w-auto text-[12px]"
      value={value} disabled={disabled} onChange={(next) => next !== value && onChange(next as '' | TaskTarget)}
    >
      <SelectOption value="">{t('memory.tasks.route.follow', { target: defaultTarget })}</SelectOption>
      <SelectOption value="agent">{t('memory.tasks.route.agent')}</SelectOption>
      <SelectOption value="lead">{t('memory.tasks.route.lead')}</SelectOption>
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
  onRoute,
  onOpenChat,
  onLane,
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
  onRoute: (route: '' | TaskTarget) => void;
  onOpenChat: () => void;
  onLane: (lane: 'backlog' | 'queue') => void;
  onStart: () => void;
  onEdit: (text: string) => Promise<unknown>;
  onDelete: () => void;
  onDone: () => void;
  onOpenAgent: (ref: string) => void;
}) {
  const t = useT();
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
        <Textarea aria-label={t('memory.tasks.editTask', { goal: task.goal })} className="min-h-20" value={editing} onChange={(event) => setEditing(event.target.value)} autoFocus />
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>
            {t('common.cancel')}
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={busy || !editing.split('\n')[0].trim()}>
            {t('common.save')}
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
          {lane === 'queue' && task.leadQueuedAt && ` · ${t('memory.tasks.forLead')}`}
          {lane === 'running' && task.agent === leadOwner && (
            <>
              {' · '}
              <button type="button" className="inline-flex items-center gap-1 text-tertiary underline-offset-2 hover:text-primary hover:underline" onClick={onOpenChat}>
                <MessagesSquare className="size-3" />
                {t('memory.tasks.leadLink')}
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
        {(lane === 'backlog' || lane === 'queue') && queueOn && <LaneChoice lane={lane} goal={task.goal} disabled={busy} onChange={onLane} />}
        {lane === 'queue' && !queueOn && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => onLane('backlog')}>
            {t('memory.tasks.backToBacklog')}
          </Button>
        )}
        {lane === 'backlog' && !queueOn && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={onStart}>
            <Play />
            {t('common.start')}
          </Button>
        )}
        {lane !== 'done' && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={onDone}>
            <Check />
            {t('common.done')}
          </Button>
        )}
        <Button size="icon-sm" variant="ghost" aria-label={t('memory.tasks.editTask', { goal: task.goal })} disabled={busy} onClick={() => setEditing(taskText(task))}>
          <Pencil />
        </Button>
        {confirmDelete ? (
          <>
            <Button size="sm" variant="danger" disabled={busy} onClick={onDelete}>
              {t('common.delete')}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>
              {t('memory.tasks.keep')}
            </Button>
          </>
        ) : (
          <Button size="icon-sm" variant="ghost" aria-label={t('memory.tasks.deleteTask', { goal: task.goal })} disabled={busy} onClick={() => setConfirmDelete(true)}>
            <Trash2 />
          </Button>
        )}
      </div>
    </div>
  );
}

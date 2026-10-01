// The project's tasks, as the Tasks tab works them: a list only the user
// writes, each open task in the backlog (the default) or in the queue, and
// the rest in a list of their own once they're done. Starting or queueing a
// task sends it where it goes (POST …/tasks/{id}/start): a new agent, the way
// it always did, or the project's lead, which may split it across several
// agents. A task queued for an agent is a queued agent made for it, so the
// queue's own order is the agents' queue positions; one queued for the lead
// waits behind the agents queued before it, and holds no slot. Sending a task
// back to the backlog takes it out of the queue either way. Kept apart from
// ProjectTasksPanel so the rules are tested without rendering it.
import type * as T from '../../shared/api';

export type TaskLane = 'backlog' | 'queue' | 'running' | 'done';

const closedStatuses = new Set(['done', 'abandoned']);

// The lead's name as a task's agent: a task the lead was handed is its.
export const leadOwner = 'lead';

export type TaskTarget = 'agent' | 'lead';

// taskTarget is where a task goes when it starts: its own choice when it made
// one, else the installation's "Tasks go to", else a new agent. The daemon
// decides the same way (memory.EffectiveTaskRoute); this is for showing it.
export function taskTarget(task: Pick<T.Task, 'route'>, setting: string | undefined): TaskTarget {
  if (task.route === 'agent' || task.route === 'lead') return task.route;
  return setting === 'lead' ? 'lead' : 'agent';
}

// taskLane is where a task sits. A task given to an agent that's gone —
// destroyed, say — is back in the backlog: nothing is working on it. The
// lead never goes, so a task it was handed stays in progress until it's done.
export function taskLane(task: Pick<T.Task, 'status' | 'agent' | 'leadQueuedAt'>, agent: Pick<T.Agent, 'state'> | undefined): TaskLane {
  if (closedStatuses.has(task.status)) return 'done';
  if (task.leadQueuedAt) return 'queue';
  if (task.agent === leadOwner) return 'running';
  if (!task.agent || !agent) return 'backlog';
  return agent.state === 'queued' ? 'queue' : 'running';
}

export interface TaskLanes {
  queue: T.Task[];
  running: T.Task[];
  backlog: T.Task[];
  done: T.Task[];
}

// taskLanes sorts a project's tasks into lanes: the queue in the order it
// will start, the backlog newest first, so what was just written is on top,
// and what's done by when it was, latest first. A task queued for the lead
// sits behind the agents queued before it.
export function taskLanes(tasks: T.Task[], agents: Map<string, Pick<T.Agent, 'state' | 'queuePosition' | 'createdAt'>>): TaskLanes {
  const lanes: TaskLanes = { queue: [], running: [], backlog: [], done: [] };
  for (const task of tasks) lanes[taskLane(task, task.agent ? agents.get(task.agent) : undefined)].push(task);
  const queued = [...agents.values()].filter((a) => a.state === 'queued');
  const position = (t: T.Task) => {
    if (t.leadQueuedAt) {
      const at = Date.parse(t.leadQueuedAt);
      return queued.filter((a) => Date.parse(a.createdAt) < at).length + 0.5;
    }
    return agents.get(t.agent ?? '')?.queuePosition ?? Number.MAX_SAFE_INTEGER;
  };
  lanes.queue.sort((a, b) => position(a) - position(b));
  lanes.backlog.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  lanes.done.sort((a, b) => (b.closedAt ?? b.updatedAt).localeCompare(a.closedAt ?? a.updatedAt));
  return lanes;
}

// The tab shows one of two lists: what's still to do — the queue, what's in
// progress and the backlog — or what's done.
export type TaskList = 'open' | 'done';

export function taskListCounts(lanes: TaskLanes): Record<TaskList, number> {
  return { open: lanes.queue.length + lanes.running.length + lanes.backlog.length, done: lanes.done.length };
}

// TaskOutcome is how a task in the Done list ended: marked done by hand,
// implemented by its agent's pull request merging (the daemon closes it then,
// internal/daemon/taskdone.go), or abandoned.
export type TaskOutcome = { kind: 'done' } | { kind: 'implemented'; url: string; number: number } | { kind: 'abandoned' };

export function taskOutcome(task: Pick<T.Task, 'status' | 'pullUrl' | 'pullNumber'>): TaskOutcome {
  if (task.status === 'abandoned') return { kind: 'abandoned' };
  if (task.pullUrl) return { kind: 'implemented', url: task.pullUrl, number: task.pullNumber ?? 0 };
  return { kind: 'done' };
}

// taskFromText is a task out of one box: the first line is its goal, what it
// is scanned by, and anything after it is the brief behind it.
export function taskFromText(text: string): { goal: string; detail: string } {
  const [first, ...rest] = text.split('\n');
  return { goal: first.trim(), detail: rest.join('\n').trim() };
}

// taskText is the other way round, for editing a task in that same box.
export function taskText(task: Pick<T.Task, 'goal' | 'detail'>): string {
  return task.detail ? `${task.goal}\n${task.detail}` : task.goal;
}

// The calls the tab makes, so the tests can stand in for the daemon.
export interface TasksApi {
  addTask: (project: string, req: T.AddTaskRequest) => Promise<unknown>;
  updateTask: (project: string, id: string, req: T.UpdateTaskRequest) => Promise<unknown>;
  deleteTask: (project: string, id: string) => Promise<unknown>;
  startTask: (project: string, id: string, req: T.StartTaskRequest) => Promise<unknown>;
  unqueueTask: (project: string, id: string) => Promise<unknown>;
  moveQueued: (project: string, agent: string, position: number) => Promise<unknown>;
}

export function taskActions(api: TasksApi, project: string) {
  return {
    add: (text: string) => {
      const { goal, detail } = taskFromText(text);
      return api.addTask(project, { goal, detail: detail || undefined });
    },
    edit: (task: T.Task, text: string) => {
      const { goal, detail } = taskFromText(text);
      return api.updateTask(project, task.id, { goal, detail });
    },
    // Deleting a queued task takes its agent out of the queue too; the
    // daemon does that, so it's one call either way.
    remove: (task: T.Task) => api.deleteTask(project, task.id),
    // Any open task can be marked done; a queued one leaves the queue with
    // it, which the daemon does, so it's one call whatever the lane.
    markDone: (task: T.Task) => api.updateTask(project, task.id, { status: 'done' }),
    // Reopening puts a done task back in the backlog, let go of whichever
    // agent had it.
    reopen: (task: T.Task) => api.updateTask(project, task.id, { status: 'open', agent: '' }),
    // Starting a task with the queue off sends it now: its agent is made, or
    // the lead is sent it.
    start: async (task: T.Task, agent: Pick<T.Agent, 'state'> | undefined) => {
      await releaseStale(task, agent);
      return api.startTask(project, task.id, {});
    },
    // route is the task's own choice of where it goes; '' follows the setting.
    setRoute: (task: T.Task, route: '' | TaskTarget) => api.updateTask(project, task.id, { route }),
    setLane: async (task: T.Task, agent: Pick<T.Agent, 'name' | 'state'> | undefined, lane: 'backlog' | 'queue') => {
      const now = taskLane(task, agent);
      if (now === lane) return;
      if (lane === 'queue' && now === 'backlog') {
        await releaseStale(task, agent);
        return api.startTask(project, task.id, { queue: true });
      }
      if (lane === 'backlog' && now === 'queue') return api.unqueueTask(project, task.id);
      throw new Error(`a ${now} task can't move to the ${lane}`);
    },
    // move puts a queued task's agent at a position in the queue, 1 for next.
    move: (agentName: string, position: number) => api.moveQueued(project, agentName, Math.max(1, position)),
  };

  // A task still naming an agent that's gone has to be let go first: the
  // daemon won't make an agent for a task that's somebody else's.
  async function releaseStale(task: T.Task, agent: unknown) {
    if (task.agent && !agent) await api.updateTask(project, task.id, { agent: '' });
  }
}

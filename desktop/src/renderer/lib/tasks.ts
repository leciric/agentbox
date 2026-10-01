// The project's tasks, as the Tasks tab works them: a list only the user
// writes, each task in the backlog (the default) or in the queue. A queued
// task is a queued agent made for it (POST /v1/agents with queue and taskId),
// so the queue's own order is the agents' queue positions, and sending a task
// back to the backlog is taking its agent out of the queue, which hands the
// task back. Kept apart from ProjectTasksPanel so the rules are tested
// without rendering it.
import type * as T from '../../shared/api';

export type TaskLane = 'backlog' | 'queue' | 'running' | 'done';

const closedStatuses = new Set(['done', 'abandoned']);

// taskLane is where a task sits. A task given to an agent that's gone —
// destroyed, say — is back in the backlog: nothing is working on it.
export function taskLane(task: Pick<T.Task, 'status' | 'agent'>, agent: Pick<T.Agent, 'state'> | undefined): TaskLane {
  if (closedStatuses.has(task.status)) return 'done';
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
// will start, the backlog newest first, so what was just written is on top.
export function taskLanes(tasks: T.Task[], agents: Map<string, Pick<T.Agent, 'state' | 'queuePosition'>>): TaskLanes {
  const lanes: TaskLanes = { queue: [], running: [], backlog: [], done: [] };
  for (const task of tasks) lanes[taskLane(task, task.agent ? agents.get(task.agent) : undefined)].push(task);
  const position = (t: T.Task) => agents.get(t.agent ?? '')?.queuePosition ?? Number.MAX_SAFE_INTEGER;
  lanes.queue.sort((a, b) => position(a) - position(b));
  lanes.backlog.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  return lanes;
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
  createAgent: (req: T.CreateAgentRequest) => Promise<unknown>;
  removeQueued: (project: string, agent: string) => Promise<unknown>;
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
    markDone: (task: T.Task) => api.updateTask(project, task.id, { status: 'done' }),
    // Starting a task with the queue off makes its agent now.
    start: async (task: T.Task, agent: Pick<T.Agent, 'state'> | undefined) => {
      await releaseStale(task, agent);
      return api.createAgent({ project, taskId: task.id, ai: 'claude' });
    },
    setLane: async (task: T.Task, agent: Pick<T.Agent, 'name' | 'state'> | undefined, lane: 'backlog' | 'queue') => {
      const now = taskLane(task, agent);
      if (now === lane) return;
      if (lane === 'queue' && now === 'backlog') {
        await releaseStale(task, agent);
        return api.createAgent({ project, taskId: task.id, queue: true, ai: 'claude' });
      }
      if (lane === 'backlog' && now === 'queue' && agent) return api.removeQueued(project, agent.name);
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

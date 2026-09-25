// Typed calls to the daemon's HTTP API, sent through the main process.
import type { ApiResponse } from '../../preload';
import * as T from '../../shared/api';
import { errorMessage } from './utils';

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function call<R>(method: string, path: string, body?: unknown): Promise<R> {
  let res: ApiResponse;
  try {
    res = await window.agentbox.request(method, path, body);
  } catch (err) {
    throw new ApiError(errorMessage(err), 0);
  }
  if (res.status >= 400) {
    let message = res.body.trim();
    try {
      message = (JSON.parse(res.body) as T.Error).error;
    } catch {
      // not JSON
    }
    throw new ApiError(message || `HTTP ${res.status}`, res.status);
  }
  if (res.status === 204 || res.body === '') return undefined as R;
  return (res.contentType.includes('application/json') ? JSON.parse(res.body) : res.body) as R;
}

const project = (name: string) => `/v1/projects/${encodeURIComponent(name)}`;
const agent = (ref: string) => `/v1/agents/${ref.split('/').map(encodeURIComponent).join('/')}`;

// A chat belongs to an agent, or to a project. A project's chat is driven by its
// lead, which has no machine of its own, so it lives under the project.
const chatBase = (ref: string) => {
  const [project, name] = ref.split('/');
  return !name || name === T.LeadName ? `/v1/projects/${encodeURIComponent(project)}/chat` : `${agent(ref)}/chat`;
};

export const isProjectChat = (ref: string) => {
  const [, name] = ref.split('/');
  return !name || name === T.LeadName;
};
export const agentPath = agent;

// A worktree's files live at the same base as its chat: the lead's under the
// project, an agent's under the agent.
const filesBase = (ref: string) => {
  const [project, name] = ref.split('/');
  return !name || name === T.LeadName ? `/v1/projects/${encodeURIComponent(project)}/files` : `${agent(ref)}/files`;
};

// A secrets target is a project ("pawly") or one agent ("pawly/agent-01"),
// the same two scopes the command line takes.
const secretsBase = (target: string) => (target.includes('/') ? `${agent(target)}/secrets` : `${project(target)}/secrets`);

export type AgentAction = 'start' | 'stop' | 'pause' | 'resume';

// A part of the token ledger: every project, one project, or one agent, since
// a time or a stretch back from now ("5h", "7d"); no since is all of it.
export interface TokenQuery {
  project?: string;
  agent?: string;
  since?: string;
}

const tokenParams = (q: TokenQuery, extra: Record<string, string> = {}) => {
  const params = new URLSearchParams(extra);
  if (q.project) params.set('project', q.project);
  if (q.agent) params.set('agent', q.agent);
  if (q.since) params.set('since', q.since);
  const s = params.toString();
  return s ? `?${s}` : '';
};

export const api = {
  settings: () => call<T.Settings>('GET', '/v1/settings'),
  update: () => call<T.UpdateStatus>('GET', '/v1/update'),
  updateSettings: (req: T.UpdateSettingsRequest) => call<T.Settings>('PATCH', '/v1/settings', req),
  tokens: (q: TokenQuery) => call<T.TokenReport>('GET', `/v1/tokens${tokenParams(q)}`),
  claudeLimits: () => call<T.ClaudeLimit[]>('GET', '/v1/limits'),
  tokenTurns: (q: TokenQuery, limit = 100) => call<T.TokenTurn[]>('GET', `/v1/tokens/turns${tokenParams(q, { limit: String(limit) })}`),
  theme: () => call<T.Theme>('GET', '/v1/theme'),
  updateTheme: (req: T.UpdateThemeRequest) => call<T.Theme>('PATCH', '/v1/theme', req),
  projects: () => call<T.Project[]>('GET', '/v1/projects'),
  addProject: (req: T.AddProjectRequest) => call<T.Project>('POST', '/v1/projects', req),
  updateProject: (name: string, req: T.UpdateProjectRequest) => call<T.Project>('PATCH', project(name), req),
  removeProject: (name: string) => call<void>('DELETE', project(name)),
  // How the projects list is organised (D79): the sections, and one call that
  // carries a whole new order rather than a move for the daemon to infer.
  sections: () => call<T.Section[]>('GET', '/v1/sections'),
  addSection: (name: string) => call<T.Section>('POST', '/v1/sections', { name } satisfies T.AddSectionRequest),
  updateSection: (id: string, req: T.UpdateSectionRequest) => call<T.Section>('PATCH', `/v1/sections/${encodeURIComponent(id)}`, req),
  removeSection: (id: string) => call<void>('DELETE', `/v1/sections/${encodeURIComponent(id)}`),
  setProjectLayout: (layout: T.ProjectLayout) => call<T.Project[]>('PUT', '/v1/projects/layout', layout),
  brief: (name: string) => call<string>('GET', `${project(name)}/brief`),
  notes: (name: string) => call<T.Notes>('GET', `${project(name)}/notes`),
  saveNotes: (name: string, text: string) => call<T.Notes>('PUT', `${project(name)}/notes`, { text } satisfies T.NotesRequest),
  base: async (name: string) => {
    try {
      return await call<T.Base>('GET', `${project(name)}/base`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) return null;
      throw err;
    }
  },
  // Saving is a job: it snapshots a whole machine, copies it and scrubs the
  // copy, which takes seconds to minutes. The other three are renames and
  // deletes, and answer in the request.
  saveBase: (name: string, agent: string) => call<T.Job>('POST', `${project(name)}/base`, { agent } satisfies T.SaveBaseRequest),
  revertBase: (name: string) => call<T.Base>('POST', `${project(name)}/base/revert`),
  removeBase: (name: string) => call<void>('DELETE', `${project(name)}/base`),
  removePreviousBase: (name: string) => call<void>('DELETE', `${project(name)}/base?previous=1`),

  agents: () => call<T.Agent[]>('GET', '/v1/agents'),
  createAgent: (req: T.CreateAgentRequest) => call<T.Job>('POST', '/v1/agents', req),
  updateAgent: (ref: string, req: T.UpdateAgentRequest) => call<T.Agent>('PATCH', agent(ref), req),
  destroyAgent: (ref: string, force: boolean, deleteBranch: boolean, deleteMedia: boolean) =>
    call<void>('DELETE', `${agent(ref)}?force=${force}&deleteBranch=${deleteBranch}&deleteMedia=${deleteMedia}`),
  agentAction: (ref: string, action: AgentAction) => call<T.Agent>('POST', `${agent(ref)}/${action}`),
  diffStat: (ref: string) => call<string>('GET', `${agent(ref)}/diff?stat=true`),

  chat: (ref: string) => call<T.ChatThread>('GET', chatBase(ref)),
  startChat: (ref: string) => call<T.ChatSession>('POST', `${chatBase(ref)}/start`),
  sendChat: (ref: string, text: string, images?: T.ChatImageUpload[]) =>
    call<T.ChatItem>('POST', `${chatBase(ref)}/messages`, { text, images } satisfies T.ChatMessageRequest),
  chatImageUrl: (ref: string, id: string) => window.agentbox.chatImageUrl(`${chatBase(ref)}/images/${encodeURIComponent(id)}`),
  cancelChat: (ref: string) => call<T.ChatSession>('POST', `${chatBase(ref)}/cancel`),
  answerChat: (ref: string, item: string, optionId: string) =>
    call<T.ChatItem>('POST', `${chatBase(ref)}/permissions/${encodeURIComponent(item)}`, { optionId } satisfies T.ChatAnswerRequest),
  setChatOption: (ref: string, option: string, value: string) =>
    call<T.ChatSession>('PUT', `${chatBase(ref)}/options/${encodeURIComponent(option)}`, { value } satisfies T.ChatOptionRequest),
  clearChat: (ref: string) => call<void>('DELETE', chatBase(ref)),
  // A project chat's prompt cache, and the card that asks before a message
  // re-sends a context whose cache is about to expire (or has).
  chatCache: (name: string) => call<T.ChatCache>('GET', `${project(name)}/chat/cache`),
  chooseChatCache: (name: string, choice: T.ChatCacheChoice) => call<T.ChatItem | undefined>('POST', `${project(name)}/chat/cache`, choice),
  files: (ref: string) => call<T.WorktreeFiles>('GET', filesBase(ref)),

  // Secrets: names in, names out. A value only ever goes in — no call here
  // reads one back, because the daemon has no route that returns one.
  secrets: (target: string) => call<T.Secret[]>('GET', secretsBase(target)),
  setSecret: (target: string, name: string, value: string) =>
    call<T.Secret>('PUT', `${secretsBase(target)}/${encodeURIComponent(name)}`, { value } satisfies T.SetSecretRequest),
  removeSecret: (target: string, name: string) => call<void>('DELETE', `${secretsBase(target)}/${encodeURIComponent(name)}`),

  fleet: (project: string) => call<T.Fleet>('GET', `/v1/projects/${encodeURIComponent(project)}/fleet`),

  // What a project's agents reported, newest first, which the rail reads for
  // when each last did; and the questions they asked, which the avatars and
  // the credential cards read.
  agentEvents: (name: string) => call<T.AgentEvent[]>('GET', `${project(name)}/agent-events`),
  questions: (name: string) => call<T.Question[]>('GET', `${project(name)}/questions?all=1`),
  // An agent's credential request: the value goes to the daemon, which puts it
  // into the agent, and the agent is told only what happened (D95).
  answerCredential: (name: string, id: string, req: T.AnswerCredentialRequest) =>
    call<T.Question>('POST', `${project(name)}/questions/${encodeURIComponent(id)}/credential`, req),
  retire: (project: string, req: T.RetireRequest) => call<T.RetireResult>('POST', `/v1/projects/${encodeURIComponent(project)}/retire`, req),
  projectMedia: (project: string, agent = '', kind = '') => {
    const q = new URLSearchParams();
    if (agent) q.set('agent', agent);
    if (kind) q.set('kind', kind);
    const query = q.toString();
    return call<T.MediaItem[]>('GET', `/v1/projects/${encodeURIComponent(project)}/media${query ? `?${query}` : ''}`);
  },
  projectPullRequests: (project: string) => call<T.ProjectPullRequests>('GET', `/v1/projects/${encodeURIComponent(project)}/pulls`),
  mergePullRequest: (project: string, number: number, method: string) =>
    call<T.PullRequest>('POST', `/v1/projects/${encodeURIComponent(project)}/pulls/${number}/merge`, { method } satisfies T.MergePullRequestRequest),

  saveGitHubToken: (token: string, account?: string) => call<{ user: string }>('POST', '/v1/auth/github', { token, account } satisfies T.GitHubTokenRequest),
  removeGitHubAccount: (account: string) => call<void>('DELETE', `/v1/auth/github/${encodeURIComponent(account)}`),
  setDefaultGitHubAccount: (account: string) => call<void>('POST', `/v1/auth/github/${encodeURIComponent(account)}/default`),
  renameGitHubAccount: (account: string, name: string) =>
    call<T.RenamedGitHubAccount>('POST', `/v1/auth/github/${encodeURIComponent(account)}/rename`, { name } satisfies T.RenameGitHubAccountRequest),

  projectChat: (project: string) => call<T.ProjectChat>('GET', `/v1/projects/${encodeURIComponent(project)}/lead`),
  resetProjectChat: (project: string) => call<void>('DELETE', `/v1/projects/${encodeURIComponent(project)}/lead`),

  // Project memory (D72): what the project knows, over and above any one
  // agent's conversation. Memories() and Reports() and Artifacts() already
  // return at most memory.MaxLimit (200), newest or most important first, so
  // nothing here paginates past what the daemon already caps.
  memoryWorking: (name: string) => call<T.WorkingMemory>('GET', `${project(name)}/memory/working`),
  setMemoryWorking: (name: string, patch: T.WorkingMemoryPatch) => call<T.WorkingMemory>('PATCH', `${project(name)}/memory/working`, patch),
  memories: (name: string, kinds: string[] = []) => {
    const q = new URLSearchParams();
    for (const kind of kinds) q.append('kind', kind);
    const query = q.toString();
    return call<T.Memory[]>('GET', `${project(name)}/memory/memories${query ? `?${query}` : ''}`);
  },
  addMemory: (name: string, req: T.AddMemoryRequest) => call<T.Memory>('POST', `${project(name)}/memory/memories`, req),
  searchMemory: (name: string, req: T.MemorySearchRequest) => call<T.MemorySearchResults>('POST', `${project(name)}/memory/search`, req),
  memoryEvents: (name: string, opts: { agent?: string; types?: string[]; limit?: number } = {}) => {
    const q = new URLSearchParams();
    if (opts.agent) q.set('agent', opts.agent);
    for (const type of opts.types ?? []) q.append('type', type);
    if (opts.limit) q.set('limit', String(opts.limit));
    const query = q.toString();
    return call<T.MemoryEvent[]>('GET', `${project(name)}/memory/events${query ? `?${query}` : ''}`);
  },
  memoryArtifacts: (name: string) => call<T.MemoryArtifact[]>('GET', `${project(name)}/memory/artifacts`),
  memoryReports: (name: string, agent = '') => call<T.AgentReport[]>('GET', `${project(name)}/memory/reports${agent ? `?agent=${encodeURIComponent(agent)}` : ''}`),
  memoryContext: (name: string, req: T.ContextRequest) => call<T.ContextResult>('POST', `${project(name)}/memory/context`, req),
  memoryContextStats: (name: string) => call<T.ContextAccount>('GET', `${project(name)}/memory/context/stats`),
  memoryConsolidation: (name: string) => call<T.MemoryConsolidation>('GET', `${project(name)}/memory/consolidation`),
  memoryDuplicates: (name: string) => call<T.MemoryDuplicate[]>('GET', `${project(name)}/memory/duplicates`),
  resolveMemory: (name: string, req: T.ResolveMemoryRequest) => call<T.Memory>('POST', `${project(name)}/memory/resolve`, req),
  // The task graph (D77): the plan, and what is blocked on what. A bare call
  // is the whole graph, edges both ways, which is what a tree view needs.
  memoryTasks: (name: string) => call<T.Task[]>('GET', `${project(name)}/memory/tasks`),
  addTask: (name: string, req: T.AddTaskRequest) => call<T.Task>('POST', `${project(name)}/memory/tasks`, req),
  updateTask: (name: string, id: string, req: T.UpdateTaskRequest) => call<T.Task>('PATCH', `${project(name)}/memory/tasks/${encodeURIComponent(id)}`, req),
  linkTasks: (name: string, req: T.LinkTasksRequest) => call<T.Task>('POST', `${project(name)}/memory/tasks/link`, req),
  unlinkTasks: (name: string, req: T.LinkTasksRequest) => call<T.Task>('POST', `${project(name)}/memory/tasks/unlink`, req),

  snapshots: (ref: string) => call<T.Snapshot[]>('GET', `${agent(ref)}/snapshots`),
  takeSnapshot: (ref: string, req: T.SnapshotRequest) => call<T.Snapshot>('POST', `${agent(ref)}/snapshots`, req),
  deleteSnapshot: (ref: string, name: string) => call<void>('DELETE', `${agent(ref)}/snapshots/${encodeURIComponent(name)}`),
  restore: (ref: string, snapshot: string) => call<T.Job>('POST', `${agent(ref)}/restore`, { snapshot } satisfies T.RestoreRequest),
  fork: (ref: string, req: T.ForkRequest) => call<T.Job>('POST', `${agent(ref)}/fork`, req),

  browser: (ref: string) => call<T.BrowserStatus>('GET', `${agent(ref)}/browser`),
  browserAction: (ref: string, action: 'start' | 'stop') => call<T.BrowserStatus>('POST', `${agent(ref)}/browser/${action}`),
  openInBrowser: (ref: string, url: string) =>
    call<T.BrowserStatus>('POST', `${agent(ref)}/browser/open`, { url } satisfies T.BrowserOpenRequest),
  preview: () => call<T.PreviewInfo>('GET', '/v1/preview'),

  android: (ref: string) => call<T.AndroidStatus>('GET', `${agent(ref)}/android`),
  startAndroid: (ref: string, req: T.AndroidStartRequest = {}) => call<T.AndroidStatus>('POST', `${agent(ref)}/android/start`, req),
  stopAndroid: (ref: string) => call<T.AndroidStatus>('POST', `${agent(ref)}/android/stop`),

  media: (ref: string) => call<T.MediaItem[]>('GET', `${agent(ref)}/media`),
  deleteMedia: (id: string) => call<void>('DELETE', `/v1/media/${encodeURIComponent(id)}`),
  // Bulk deletes: the items named by id, or everything the list filters match.
  deleteProjectMedia: (project: string, req: T.DeleteMediaRequest) =>
    call<T.DeleteMediaResult>('POST', `/v1/projects/${encodeURIComponent(project)}/media/delete`, req),
  deleteAgentMedia: (ref: string, req: T.DeleteMediaRequest) => call<T.DeleteMediaResult>('POST', `${agent(ref)}/media/delete`, req),
  screenshot: (ref: string, req: T.ScreenshotRequest = {}) => call<T.MediaItem>('POST', `${agent(ref)}/media/screenshot`, req),
  recording: (ref: string) => call<T.RecordingStatus>('GET', `${agent(ref)}/media/record`),
  startRecording: (ref: string, req: T.RecordRequest = {}) => call<T.RecordingStatus>('POST', `${agent(ref)}/media/record/start`, req),
  stopRecording: (ref: string) => call<T.MediaItem>('POST', `${agent(ref)}/media/record/stop`),
  addNote: (ref: string, req: T.NoteRequest) => call<T.MediaItem>('POST', `${agent(ref)}/media/note`, req),
  addLogs: (ref: string, req: T.LogsRequest) => call<T.MediaItem>('POST', `${agent(ref)}/media/logs`, req),
  exportMedia: (ref: string) => call<T.ExportResult>('POST', `${agent(ref)}/media/export`, {}),

  usage: () => call<T.Usage>('GET', '/v1/usage?interval=500ms'),
  auth: () => call<T.AuthStatus>('GET', '/v1/auth'),
  saveClaudeToken: (token: string, account?: string) => call<void>('POST', '/v1/auth/claude', { token, account } satisfies T.ClaudeTokenRequest),
  // Logging in from the app: the daemon runs `claude setup-token` as a job, and
  // says which page to open while it waits for you to approve it.
  startClaudeLogin: (account?: string) => call<T.Job>('POST', '/v1/auth/claude/login', { account } satisfies T.ClaudeLoginRequest),
  claudeLogin: (job: string) => call<T.ClaudeLogin>('GET', `/v1/auth/claude/login/${encodeURIComponent(job)}`),
  claudeLoginCode: (job: string, code: string) =>
    call<void>('POST', `/v1/auth/claude/login/${encodeURIComponent(job)}/code`, { code } satisfies T.ClaudeLoginCodeRequest),
  removeClaudeAccount: (account: string) => call<void>('DELETE', `/v1/auth/claude/${encodeURIComponent(account)}`),
  setDefaultClaudeAccount: (account: string) => call<void>('POST', `/v1/auth/claude/${encodeURIComponent(account)}/default`),
  renameClaudeAccount: (account: string, name: string) =>
    call<T.RenamedClaudeAccount>('POST', `/v1/auth/claude/${encodeURIComponent(account)}/rename`, { name } satisfies T.RenameClaudeAccountRequest),
  setup: () => call<T.SetupStatus>('GET', '/v1/setup'),
  image: () => call<{ ready: boolean; snapshot: string }>('GET', '/v1/image'),
  buildImage: (req: T.BuildImageRequest = {}) => call<T.Job>('POST', '/v1/image/build', req),

  jobs: () => call<T.Job[]>('GET', '/v1/jobs'),
  job: (id: string) => call<T.Job>('GET', `/v1/jobs/${id}`),
  jobLog: (id: string) => call<string>('GET', `/v1/jobs/${id}/log`),
  cancelJob: (id: string) => call<T.Job>('POST', `/v1/jobs/${id}/cancel`),
};

// Typed calls to the daemon's HTTP API, sent through the main process.
import type { ApiResponse } from '../../preload';
import * as T from '../../shared/api.ts';
import { recordRequest, clientSpansPath } from './appTiming.ts';
import type * as D from './diagnostics.ts';
import { errorMessage } from './utils.ts';
import { ensureVMRunning } from './vm.ts';

export class ApiError extends Error {
  readonly status: number;
  // code is the daemon's Error.code: which refusal this is, when it says.
  readonly code?: string;
  constructor(message: string, status: number, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function call<R>(method: string, path: string, body?: unknown): Promise<R> {
  let res: ApiResponse;
  // Each round trip is a span of the app's own (appTiming.ts).
  const started = performance.now();
  try {
    res = await window.agentbox.request(method, path, body);
  } catch (err) {
    recordRequest(method, path, started, 0, errorMessage(err));
    throw new ApiError(errorMessage(err), 0);
  }
  recordRequest(method, path, started, res.status);
  if (res.status >= 400) {
    let message = res.body.trim();
    let code: string | undefined;
    try {
      ({ error: message, code } = JSON.parse(res.body) as T.Error);
    } catch {
      // not JSON
    }
    throw new ApiError(message || `HTTP ${res.status}`, res.status, code);
  }
  if (res.status === 204 || res.body === '') return undefined as R;
  return (res.contentType.includes('application/json') ? JSON.parse(res.body) : res.body) as R;
}

// page is call for a list read a page at a time (?limit=), whose answer is
// a page with its items in. A daemon from before these lists paged (still
// running after the app was updated: restartIfStale leaves one busy with jobs
// alone) answers the plain list, whose page.items was undefined, and the app
// crashed with "Cannot read properties of undefined (reading 'map')". Such a
// list is one page holding it all.
async function page<P extends { items: unknown[]; next?: string; total?: number }>(path: string): Promise<P> {
  const res = await call<P | P['items'] | null | undefined>('GET', path);
  if (Array.isArray(res)) return { items: res, total: res.length } as unknown as P;
  if (!res || typeof res !== 'object') return { items: [], total: 0 } as unknown as P;
  return { ...res, items: Array.isArray(res.items) ? res.items : [] };
}

// text is call for an endpoint that answers plain text. An empty answer is ''
// rather than call's undefined: a job that hasn't logged a line, an agent with
// no changes or an empty brief is an empty body, and callers treat it as text.
async function text(method: string, path: string): Promise<string> {
  return (await call<string | undefined>(method, path)) ?? '';
}

// pagePath is the daemon path of a Hatch page's HTML, which the app frames
// through agentbox-media://api (main/media.ts).
export const pagePath = (name: string, id: string) =>
  `/v1/projects/${encodeURIComponent(name)}/artifacts/${encodeURIComponent(id)}/page`;

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
// The WebSocket paths of an agent's VNC displays, for lib/vnc.
export const browserViewPath = (ref: string) => `${agent(ref)}/browser/view`;
export const androidViewPath = (ref: string) => `${agent(ref)}/android/view`;

// The Home chat is a lead kept under T.HomeProject rather than a project.
export const isHomeChat = (ref: string) => ref.split('/')[0] === T.HomeProject;

// A worktree's files live at the same base as its chat: the lead's under the
// project, an agent's under the agent.
// A past agent's ("past:<ref>", lib/rightPanel.ts) are read from its branch.
const filesBase = (ref: string) => {
  if (ref.startsWith('past:')) return `/v1/archived-agents/${ref.slice(5).split('/').map(encodeURIComponent).join('/')}/files`;
  const [project, name] = ref.split('/');
  return !name || name === T.LeadName ? `/v1/projects/${encodeURIComponent(project)}/files` : `${agent(ref)}/files`;
};

// A secrets target is a project ("pawly") or one agent ("pawly/agent-01"),
// the same two scopes the command line takes.
const secretsBase = (target: string) => (target.includes('/') ? `${agent(target)}/secrets` : `${project(target)}/secrets`);
// Connectors take the same two targets, and '' for the AgentBox-wide ones.
const connectorsBase = (target: string) =>
  target === '' ? '/v1/connectors' : target.includes('/') ? `${agent(target)}/connectors` : `${project(target)}/connectors`;
const connectorPath = (target: string, name: string) => `${connectorsBase(target)}/${encodeURIComponent(name)}`;

export type AgentAction = 'start' | 'stop' | 'pause' | 'resume';

// A past agent, by ref, under /v1/archived-agents.
const archived = (ref: string) => `/v1/archived-agents/${ref.split('/').map(encodeURIComponent).join('/')}`;

// Which past agents to list: of one project or every one, matching q (name,
// title, task, branch, pull request), by their pull request's state (or
// 'none'), archived at or after since and up to until (dates or ISO times).
export interface ArchivedQuery {
  project?: string;
  q?: string;
  pr?: string;
  since?: string;
  until?: string;
  limit?: number;
}

// ChatPage is which part of a chat to read (api.chat), by item id.
export interface ChatPage {
  limit: number;
  before?: string;
  from?: string;
  around?: string;
  after?: string;
}

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

// providerQuery names an OpenCode account's provider in its path.
function providerQuery(provider?: string): string {
  return provider ? `?provider=${encodeURIComponent(provider)}` : '';
}

export const api = {
  settings: () => call<T.Settings>('GET', '/v1/settings'),
  // Chatting from a phone on the local network.
  lan: () => call<T.LANStatus>('GET', '/v1/lan'),
  updateLAN: (req: T.UpdateLANRequest) => call<T.LANStatus>('PATCH', '/v1/lan', req),
  pairLAN: () => call<T.LANPairing>('POST', '/v1/lan/pairings'),
  removeLANPhone: (id: string) => call<void>('DELETE', `/v1/lan/phones/${encodeURIComponent(id)}`),
  updateLANPhone: (id: string, scopes: string[]) =>
    call<T.LANPhone>('PATCH', `/v1/lan/phones/${encodeURIComponent(id)}`, { scopes } satisfies T.UpdateLANPhoneRequest),
  lanPhoneAudit: (id: string, before = 0) =>
    call<T.LANAudit>('GET', `/v1/lan/phones/${encodeURIComponent(id)}/audit${before > 0 ? `?before=${before}` : ''}`),
  update: () => call<T.UpdateStatus>('GET', '/v1/update'),
  latestRelease: () => call<T.UpdateRelease>('GET', '/v1/update/release'),
  updateSettings: (req: T.UpdateSettingsRequest) => call<T.Settings>('PATCH', '/v1/settings', req),
  usageStatsPending: () => call<T.UsageStatsPending>('GET', '/v1/usage-stats/pending'),
  countFeature: (feature: string) => call<void>('POST', `/v1/usage-stats/${encodeURIComponent(feature)}`),
  reportDraft: (req: T.ReportDraftRequest) => call<T.ReportDraft>('POST', '/v1/reports/draft', req),
  sendReport: (req: T.ReportRequest) => call<T.ReportSent>('POST', '/v1/reports', req),
  tokens: (q: TokenQuery) => call<T.TokenReport>('GET', `/v1/tokens${tokenParams(q)}`),
  claudeLimits: () => call<T.ClaudeLimit[]>('GET', '/v1/limits'),
  codexLimits: () => call<T.ClaudeLimit[]>('GET', '/v1/limits?tool=codex'),
  tokenTurns: (q: TokenQuery, limit = 100) => call<T.TokenTurn[]>('GET', `/v1/tokens/turns${tokenParams(q, { limit: String(limit) })}`),
  theme: () => call<T.Theme>('GET', '/v1/theme'),
  updateTheme: (req: T.UpdateThemeRequest) => call<T.Theme>('PATCH', '/v1/theme', req),
  projects: () => call<T.Project[]>('GET', '/v1/projects'),
  addProject: (req: T.AddProjectRequest) => call<T.Project>('POST', '/v1/projects', req),
  updateProject: (name: string, req: T.UpdateProjectRequest) => call<T.Project>('PATCH', project(name), req),
  removeProject: (name: string) => call<void>('DELETE', project(name)),
  // When the user last wrote to each project and when anything last happened
  // in its chats: v2's sidebar sorts and settles projects by them.
  projectActivity: () => call<T.ProjectActivity[]>('GET', '/v1/project-activity'),
  // A project's icon: the user's choice, else the one found in its repository.
  projectIcon: (name: string) => call<T.ProjectIcon>('GET', `${project(name)}/icon`),
  setProjectIcon: (name: string, req: T.SetProjectIconRequest) => call<T.ProjectIcon>('PUT', `${project(name)}/icon`, req),
  resetProjectIcon: (name: string) => call<T.ProjectIcon>('DELETE', `${project(name)}/icon`),
  // How the projects list is organised (D79): the sections, and one call that
  // carries a whole new order rather than a move for the daemon to infer.
  sections: () => call<T.Section[]>('GET', '/v1/sections'),
  addSection: (name: string) => call<T.Section>('POST', '/v1/sections', { name } satisfies T.AddSectionRequest),
  updateSection: (id: string, req: T.UpdateSectionRequest) => call<T.Section>('PATCH', `/v1/sections/${encodeURIComponent(id)}`, req),
  removeSection: (id: string) => call<void>('DELETE', `/v1/sections/${encodeURIComponent(id)}`),
  // UI v2's sidebar's hidden projects, replaced whole.
  sidebarLayout: () => call<T.SidebarLayout>('GET', '/v1/sidebar'),
  setSidebarLayout: (layout: T.SidebarLayout) => call<T.SidebarLayout>('PUT', '/v1/sidebar', layout),
  setProjectLayout: (layout: T.ProjectLayout) => call<T.Project[]>('PUT', '/v1/projects/layout', layout),
  brief: (name: string) => text('GET', `${project(name)}/brief`),
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
  // Making or starting an agent in VM mode brings the VM up first, so the
  // one click is all it takes after Free resources turned everything off.
  createAgent: async (req: T.CreateAgentRequest) => {
    await ensureVMRunning();
    return call<T.Job>('POST', '/v1/agents', req);
  },
  // Groups: one task on several agents at once, compared to keep one.
  fanOut: async (req: T.FanOutRequest) => {
    await ensureVMRunning();
    return call<T.FanOutResult>('POST', '/v1/groups', req);
  },
  groups: () => call<T.Group[]>('GET', '/v1/groups'),
  group: (id: string) => call<T.Group>('GET', `/v1/groups/${encodeURIComponent(id)}`),
  keepInGroup: (id: string, agent: string) => call<T.Group>('POST', `/v1/groups/${encodeURIComponent(id)}/keep`, { agent } satisfies T.KeepRequest),
  closeGroup: (id: string) => call<T.Group>('POST', `/v1/groups/${encodeURIComponent(id)}/close`),
  judgeGroup: (id: string) => call<T.Group>('POST', `/v1/groups/${encodeURIComponent(id)}/judge`),
  updateAgent: (ref: string, req: T.UpdateAgentRequest) => call<T.Agent>('PATCH', agent(ref), req),
  destroyAgent: (ref: string, force: boolean, deleteBranch: boolean, deleteMedia: boolean) =>
    call<void>('DELETE', `${agent(ref)}?force=${force}&deleteBranch=${deleteBranch}&deleteMedia=${deleteMedia}`),
  agentDisk: (ref: string) => call<T.AgentDisk>('GET', `${agent(ref)}/disk`),
  agentCredential: (ref: string) => call<T.ToolCredential>('GET', `${agent(ref)}/credential`),
  agentAction: async (ref: string, action: AgentAction) => {
    if (action === 'start' || action === 'resume') await ensureVMRunning();
    return call<T.Agent>('POST', `${agent(ref)}/${action}`);
  },
  // Free resources: stop every running or paused agent, or only refs, as a
  // job whose result is a StopAgentsResult.
  stopAgents: (refs?: string[]) => call<T.Job>('POST', '/v1/agents/stop', { refs } satisfies T.StopAgentsRequest),
  diffStat: (ref: string) => text('GET', `${agent(ref)}/diff?stat=true`),
  // diff is the agent's whole diff against its base, for a review with no pull request.
  diff: (ref: string) => text('GET', `${agent(ref)}/diff`),

  // Past agents: an agent whose machine was removed is kept, with its chat,
  // until it is deleted here. Its chat reads through the live routes
  // (api.chat, api.searchChat), and nothing can be sent to it; continuing it
  // makes a new agent on its branch, as a job like createAgent's.
  archivedAgents: (q: ArchivedQuery = {}) => {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries(q)) if (v) params.set(k, String(v));
    const query = params.toString();
    return call<T.ArchivedAgent[]>('GET', `/v1/archived-agents${query ? `?${query}` : ''}`);
  },
  archivedAgent: (ref: string) => call<T.ArchivedAgent>('GET', archived(ref)),
  deleteArchived: (ref: string) => call<void>('DELETE', archived(ref)),
  deleteArchivedMany: (refs: string[]) => call<T.DeleteArchivedResult>('POST', '/v1/archived-agents/delete', { refs } satisfies T.DeleteArchivedRequest),
  continueArchived: async (ref: string, req: T.ContinueArchivedRequest) => {
    await ensureVMRunning();
    return call<T.Job>('POST', `${archived(ref)}/continue`, req);
  },

  // A chat is read a page at a time: the latest limit messages, the ones
  // before an item, everything from an item on, limit messages on each side
  // of an item (around), or the ones after it. Without a page it is the whole
  // conversation.
  chat: (ref: string, page?: ChatPage) => {
    const q = page && Object.entries({ ...page, limit: String(page.limit) }).filter((kv): kv is [string, string] => !!kv[1]);
    return call<T.ChatThread>('GET', q ? `${chatBase(ref)}?${new URLSearchParams(q)}` : chatBase(ref));
  },
  // What in a chat holds query, loaded or not: its items, oldest first.
  // kinds narrows it to items of those kinds (user, aside, assistant…).
  searchChat: (ref: string, query: string, kinds: string[] = []) =>
    call<T.ChatSearch>('GET', `${chatBase(ref)}/search?${new URLSearchParams({ q: query, ...(kinds.length ? { kinds: kinds.join(',') } : {}) })}`),
  startChat: (ref: string) => call<T.ChatSession>('POST', `${chatBase(ref)}/start`),
  sendChat: (ref: string, text: string, images?: T.ChatImageUpload[], files?: T.ChatFileUpload[]) =>
    call<T.ChatItem>('POST', `${chatBase(ref)}/messages`, { text, images, files: files?.length ? files : undefined } satisfies T.ChatMessageRequest),
  chatImageUrl: (ref: string, id: string) => window.agentbox.chatImageUrl(`${chatBase(ref)}/images/${encodeURIComponent(id)}`),
  // openChatFile opens a file sent in a chat with the system's default app:
  // the desktop downloads it first, a browser saves it.
  openChatFile: (ref: string, file: T.ChatFile) => window.agentbox.openChatFile(`${chatBase(ref)}/files/${encodeURIComponent(file.id)}`, file.name),
  cancelChat: (ref: string) => call<T.ChatSession>('POST', `${chatBase(ref)}/cancel`),
  // resumeChat is "Resume now" on an agent paused on a usage limit.
  resumeChat: (ref: string) => call<void>('POST', `${chatBase(ref)}/resume`),
  reloadChatTools: (ref: string) => call<T.ChatSession>('POST', `${chatBase(ref)}/reload`),
  answerChat: (ref: string, item: string, optionId: string) =>
    call<T.ChatItem>('POST', `${chatBase(ref)}/permissions/${encodeURIComponent(item)}`, { optionId } satisfies T.ChatAnswerRequest),
  setChatOption: (ref: string, option: string, value: string) =>
    call<T.ChatSession>('PUT', `${chatBase(ref)}/options/${encodeURIComponent(option)}`, { value } satisfies T.ChatOptionRequest),
  clearChat: (ref: string) => call<void>('DELETE', chatBase(ref)),
  // A lead's AI tool and account, chosen in its composer: what it can switch
  // to, and the choice, which applies from its next message. project is the
  // project's name, or T.HomeProject for the Home chat.
  chatRunners: (name: string) => call<T.LeadRunners>('GET', `${project(name)}/chat/runners`),
  setChatRunner: (name: string, req: T.LeadRunnerRequest) => call<T.ChatSession>('PUT', `${project(name)}/chat/runner`, req),
  // A project chat's prompt cache, and the card that asks before a message
  // re-sends a context whose cache is about to expire (or has).
  chatCache: (name: string) => call<T.ChatCache>('GET', `${project(name)}/chat/cache`),
  chooseChatCache: (name: string, choice: T.ChatCacheChoice) => call<T.ChatItem | undefined>('POST', `${project(name)}/chat/cache`, choice),
  files: (ref: string) => call<T.WorktreeFiles>('GET', filesBase(ref)),
  // The file tree and viewer beside a chat (components/files).
  listFolder: (ref: string, path: string, ignored: boolean) =>
    call<T.FileList>('GET', `${filesBase(ref)}/list?${new URLSearchParams({ path, ...(ignored ? { ignored: '1' } : {}) })}`),
  readFile: (ref: string, path: string, full = false) =>
    call<T.FileContent>('GET', `${filesBase(ref)}/read?${new URLSearchParams({ path, base: '1', ...(full ? { full: '1' } : {}) })}`),
  fileStatus: (ref: string, fresh = false) => call<T.FileStatus>('GET', `${filesBase(ref)}/status${fresh ? '?fresh=1' : ''}`),

  // Scheduled tasks (internal/daemon/schedules.go): a project's, and their runs.
  schedules: (projectName: string) => call<T.Schedule[]>('GET', `${project(projectName)}/schedules`),
  addSchedule: (projectName: string, req: T.ScheduleRequest) => call<T.Schedule>('POST', `${project(projectName)}/schedules`, req),
  updateSchedule: (projectName: string, id: string, req: T.ScheduleRequest) =>
    call<T.Schedule>('PATCH', `${project(projectName)}/schedules/${encodeURIComponent(id)}`, req),
  removeSchedule: (projectName: string, id: string) => call<void>('DELETE', `${project(projectName)}/schedules/${encodeURIComponent(id)}`),
  runSchedule: (projectName: string, id: string) => call<T.ScheduleRun>('POST', `${project(projectName)}/schedules/${encodeURIComponent(id)}/run`),
  scheduleRuns: (projectName: string) => call<T.ScheduleRun[]>('GET', `${project(projectName)}/schedules/runs`),

  // Skills (internal/skills): stored by the daemon, installed into every agent
  // and lead whose project has them on. A project's list says, for each,
  // whether its agents get it (active).
  skills: (projectName?: string) => call<T.Skill[]>('GET', projectName ? `${project(projectName)}/skills` : '/v1/skills'),
  skill: (name: string) => call<T.SkillDetail>('GET', `/v1/skills/${encodeURIComponent(name)}`),
  saveSkill: (name: string, body: T.SaveSkillRequest) => call<T.Skill>('PUT', `/v1/skills/${encodeURIComponent(name)}`, body),
  setSkillEnabled: (name: string, enabled: boolean) =>
    call<T.Skill>('PATCH', `/v1/skills/${encodeURIComponent(name)}`, { enabled } satisfies T.UpdateSkillRequest),
  setSkillOverride: (projectName: string, name: string, override: '' | 'on' | 'off') =>
    call<T.Skill>('PUT', `${project(projectName)}/skills/${encodeURIComponent(name)}`, { override } satisfies T.SkillOverrideRequest),
  removeSkill: (name: string) => call<void>('DELETE', `/v1/skills/${encodeURIComponent(name)}`),
  scanSkills: (source: string) => call<T.SkillCandidate[]>('POST', '/v1/skills/scan', { source } satisfies T.ScanSkillsRequest),
  importSkills: (body: T.ImportSkillsRequest) => call<T.Skill[]>('POST', '/v1/skills/import', body),

  // Secrets: names in, names out. A value only ever goes in — no call here
  // reads one back, because the daemon has no route that returns one.
  secrets: (target: string) => call<T.Secret[]>('GET', secretsBase(target)),
  setSecret: (target: string, name: string, value: string) =>
    call<T.Secret>('PUT', `${secretsBase(target)}/${encodeURIComponent(name)}`, { value } satisfies T.SetSecretRequest),
  removeSecret: (target: string, name: string) => call<void>('DELETE', `${secretsBase(target)}/${encodeURIComponent(name)}`),

  // A project's browser cookies, imported from an export the user made: the
  // export goes in, and only domains and counts come back (internal/cookieimport).
  browserCookies: (name: string) => call<T.BrowserCookies>('GET', `${project(name)}/browser-cookies`),
  previewBrowserCookies: (name: string, exported: string) =>
    call<T.BrowserCookiesPreview>('POST', `${project(name)}/browser-cookies/preview`, { export: exported } satisfies T.BrowserCookiesPreviewRequest),
  importBrowserCookies: (name: string, exported: string, domains: string[]) =>
    call<T.BrowserCookies>('PUT', `${project(name)}/browser-cookies`, { export: exported, domains } satisfies T.ImportBrowserCookiesRequest),
  removeBrowserCookies: (name: string) => call<void>('DELETE', `${project(name)}/browser-cookies`),
  // The browsers installed on the host, to import cookies from directly
  // (internal/cookieimport).
  browserProfiles: (name: string) => call<T.BrowserProfiles>('GET', `${project(name)}/browser-cookies/profiles`),
  // Import every cookie of one installed browser profile. For a Chromium
  // profile the keyring passphrase is fetched on the host first (the daemon
  // in the VM can't reach the keyring) and passed in; Firefox needs none.
  importFromBrowser: async (name: string, profile: T.BrowserProfile) => {
    const keyringSecret = profile.keyring ? await window.agentbox.browserKeyringSecret(profile.keyring) : '';
    return call<T.BrowserCookies>('POST', `${project(name)}/browser-cookies/from-browser`, {
      profileId: profile.id,
      keyringSecret,
    } satisfies T.ImportFromBrowserRequest);
  },

  // SnapShots waiting for the composer (agentbox snap), and sending one.
  snaps: () => call<T.Snap[]>('GET', '/v1/snaps'),
  sendSnap: (id: string, req: T.SnapSendRequest) => call<T.ChatItem>('POST', `/v1/snaps/${id}/send`, req),
  dropSnap: (id: string) => call<void>('DELETE', `/v1/snaps/${id}`),

  // Connectors: remote MCP servers the agents use, signed in on the host. No
  // answer carries a token (internal/connectors).
  connectors: (target: string) => call<T.Connector[]>('GET', connectorsBase(target)),
  setConnector: (target: string, name: string, req: T.SetConnectorRequest) => call<T.Connector>('PUT', connectorPath(target, name), req),
  removeConnector: (target: string, name: string) => call<void>('DELETE', connectorPath(target, name)),
  connectConnector: (target: string, name: string) => call<T.ConnectResult>('POST', `${connectorPath(target, name)}/connect`),
  disconnectConnector: (target: string, name: string) => call<T.Connector>('POST', `${connectorPath(target, name)}/disconnect`),
  // A project's say on an AgentBox-wide connector: 'on', 'off', or '' to follow AgentBox's.
  setConnectorOverride: (projectName: string, name: string, override: '' | 'on' | 'off') =>
    call<T.Connector>('PUT', `${connectorPath(projectName, name)}/override`, { override } satisfies T.ConnectorOverrideRequest),
  // The MCP servers the user's own AI tools have, as they would be imported
  // into a project ('' for AgentBox-wide); a header's value never comes back.
  scanConnectors: (project: string) => call<T.ConnectorScan>('POST', '/v1/connectors/scan', { project } satisfies T.ScanConnectorsRequest),
  importConnectors: (req: T.ImportConnectorsRequest) => call<T.ImportConnectorsResult>('POST', '/v1/connectors/import', req),

  // The pages a project's chats published on Hatch, and one of them brought
  // up to date from Hatch; its HTML is framed from pageUrl, and snapshotted
  // for its thumbnail by the main process (main/pagethumbs.ts).
  pages: (name: string) => call<T.Artifacts>('GET', `${project(name)}/artifacts`),
  pagePreview: (name: string, id: string) => call<T.ArtifactPreview>('GET', `${project(name)}/artifacts/${encodeURIComponent(id)}`),
  pageUrl: (name: string, id: string) => window.agentbox.chatImageUrl(pagePath(name, id)),
  // publishRender publishes a block a chat rendered on Hatch, as a page; the
  // chat gets the publish_page line, which makes it one of the project's pages.
  publishRender: (ref: string, req: T.PublishRenderRequest) => call<T.PublishRenderResult>('POST', `${chatBase(ref)}/publish`, req),
  pageThumb: (name: string, id: string, key: string): Promise<string | null> =>
    window.agentbox.pageThumb(pagePath(name, id), key),

  fleet: (project: string) => call<T.Fleet>('GET', `/v1/projects/${encodeURIComponent(project)}/fleet`),
  // When each of the project's agents last used its desktop (components/pip).
  projectDesktops: (project: string) => call<T.DesktopUse[]>('GET', `/v1/projects/${encodeURIComponent(project)}/desktops`),
  projectCredentials: (project: string) => call<T.ToolCredential[]>('GET', `/v1/projects/${encodeURIComponent(project)}/credentials`),

  // What a project's agents reported, newest first, which the rail reads for
  // when each last did; and the questions they asked, which the avatars and
  // the credential cards read.
  agentEvents: (name: string) => call<T.AgentEvent[]>('GET', `${project(name)}/agent-events`),
  // "Ready to review": a project's agents that finished and nobody has
  // looked at since, or every project's, a page at a time (query: limit, cursor);
  // and a verdict on one.
  readyToReviewPage: (query: URLSearchParams, name?: string) => page<T.ReviewPage>(`${name ? `${project(name)}/review` : '/v1/review'}?${query}`),
  // Reviewed: the agents already looked at or dismissed, the latest first.
  reviewedPage: (query: URLSearchParams, name?: string) => page<T.ReviewPage>(`${name ? `${project(name)}/review` : '/v1/review'}?reviewed=1&${query}`),
  review: (ref: string, req: T.ReviewRequest) => call<void>('POST', `${agent(ref)}/review`, req),
  requestChanges: (ref: string, req: T.RequestChangesRequest) => call<T.ChatItem>('POST', `${agent(ref)}/request-changes`, req),
  // Notes on lines of an agent's diff, drafts until Request changes sends them.
  reviewNotes: (ref: string) => call<T.ReviewNote[]>('GET', `${agent(ref)}/review-notes`),
  addReviewNote: (ref: string, req: T.AddReviewNoteRequest) => call<T.ReviewNote>('POST', `${agent(ref)}/review-notes`, req),
  editReviewNote: (ref: string, id: number, req: T.EditReviewNoteRequest) => call<T.ReviewNote>('PATCH', `${agent(ref)}/review-notes/${id}`, req),
  deleteReviewNote: (ref: string, id: number) => call<void>('DELETE', `${agent(ref)}/review-notes/${id}`),
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
  // refresh re-reads GitHub behind the answer whatever the age of the
  // daemon's cache: the tab's refresh button.
  projectPullRequests: (project: string, refresh = false) =>
    call<T.ProjectPullRequests>('GET', `/v1/projects/${encodeURIComponent(project)}/pulls${refresh ? '?refresh=1' : ''}`),
  projectLabels: (project: string) => call<T.ProjectLabels>('GET', `/v1/projects/${encodeURIComponent(project)}/labels`),
  editPullRequestLabels: (project: string, number: number, edit: T.EditLabelsRequest) =>
    call<T.Label[]>('POST', `/v1/projects/${encodeURIComponent(project)}/pulls/${number}/labels`, edit satisfies T.EditLabelsRequest),
  // confirmed is the phone saying the user confirmed the merge there, which
  // the daemon asks of a phone (internal/daemon/lan.go).
  mergePullRequest: (project: string, number: number, method: string, confirmed?: boolean) =>
    call<T.PullRequest>('POST', `/v1/projects/${encodeURIComponent(project)}/pulls/${number}/merge`, { method, confirmed } satisfies T.MergePullRequestRequest),
  pullRequestDetail: (project: string, number: number) => call<T.PullRequestDetail>('GET', `/v1/projects/${encodeURIComponent(project)}/pulls/${number}`),
  pullRequestFiles: (project: string, number: number) => call<T.PullRequestFiles>('GET', `/v1/projects/${encodeURIComponent(project)}/pulls/${number}/files`),
  pullFileDiff: (project: string, number: number, path: string) =>
    call<T.PullFileDiff>('GET', `/v1/projects/${encodeURIComponent(project)}/pulls/${number}/diff?path=${encodeURIComponent(path)}`),
  // pullImageUrl is where the renderer loads a picture in a pull request's
  // description: the daemon reads it with the project's GitHub account.
  pullImageUrl: (project: string, src: string) =>
    window.agentbox.chatImageUrl(`/v1/projects/${encodeURIComponent(project)}/pulls/image?src=${encodeURIComponent(src)}`),

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
  // AgentBox-wide memory: what the user asked to hold in every project, which
  // every project reads beside its own. Deleting one is the user's alone.
  globalMemories: () => call<T.Memory[]>('GET', '/v1/global/memory/memories'),
  deleteGlobalMemory: (id: string) => call<void>('DELETE', `/v1/global/memory/memories/${encodeURIComponent(id)}`),

  snapshots: (ref: string) => call<T.Snapshot[]>('GET', `${agent(ref)}/snapshots`),
  takeSnapshot: (ref: string, req: T.SnapshotRequest) => call<T.Snapshot>('POST', `${agent(ref)}/snapshots`, req),
  deleteSnapshot: (ref: string, name: string) => call<void>('DELETE', `${agent(ref)}/snapshots/${encodeURIComponent(name)}`),
  restore: (ref: string, snapshot: string) => call<T.Job>('POST', `${agent(ref)}/restore`, { snapshot } satisfies T.RestoreRequest),
  fork: (ref: string, req: T.ForkRequest) => call<T.Job>('POST', `${agent(ref)}/fork`, req),
  checkpoints: (ref: string) => call<T.Checkpoint[]>('GET', `${agent(ref)}/checkpoints`),
  rollback: (ref: string, checkpoint: string) => call<T.RollbackResult>('POST', `${agent(ref)}/rollback`, { checkpoint } satisfies T.RollbackRequest),

  browser: (ref: string) => call<T.BrowserStatus>('GET', `${agent(ref)}/browser`),
  browserAction: (ref: string, action: 'start' | 'stop') => call<T.BrowserStatus>('POST', `${agent(ref)}/browser/${action}`),
  // pickElement shows the element picker on the page in ref's browser and
  // answers once an element is clicked, or with none once it is cancelled.
  pickElement: (ref: string, hint: string) => call<T.ElementPickResult>('POST', `${agent(ref)}/browser/pick`, { hint } satisfies T.ElementPickRequest),
  cancelPick: (ref: string) => call<void>('POST', `${agent(ref)}/browser/pick/cancel`),
  openInBrowser: (ref: string, url: string) =>
    call<T.BrowserStatus>('POST', `${agent(ref)}/browser/open`, { url } satisfies T.BrowserOpenRequest),
  preview: () => call<T.PreviewInfo>('GET', '/v1/preview'),

  android: (ref: string) => call<T.AndroidStatus>('GET', `${agent(ref)}/android`),
  startAndroid: (ref: string, req: T.AndroidStartRequest = {}) => call<T.AndroidStatus>('POST', `${agent(ref)}/android/start`, req),
  stopAndroid: (ref: string) => call<T.AndroidStatus>('POST', `${agent(ref)}/android/stop`),

  media: (ref: string) => call<T.MediaItem[]>('GET', `${agent(ref)}/media`),
  // What an agent did, as steps read out of its chat (internal/timeline); a
  // past agent's too.
  timeline: (ref: string) => call<T.Timeline>('GET', `${agent(ref)}/timeline`),
  deleteMedia: (id: string) => call<void>('DELETE', `/v1/media/${encodeURIComponent(id)}`),
  setMediaFavorite: (id: string, favorite: boolean) =>
    call<T.MediaItem>('PATCH', `/v1/media/${encodeURIComponent(id)}`, { favorite } satisfies T.UpdateMediaRequest),
  // Everything AgentBox keeps, in every project, that has the words typed: the search palette.
  // What every project holds that matches query: of only the kinds asked for,
  // in one project, at or after since (an ISO time), when those are given;
  // with archived, past agents' chats too.
  search: (query: string, opts: { limit?: number; kinds?: string[]; project?: string; since?: string; archived?: boolean } = {}) => {
    const params = new URLSearchParams({ q: query });
    if (opts.limit) params.set('limit', String(opts.limit));
    if (opts.kinds?.length) params.set('kinds', opts.kinds.join(','));
    if (opts.project) params.set('project', opts.project);
    if (opts.since) params.set('since', opts.since);
    if (opts.archived) params.set('archived', 'true');
    return call<T.SearchResults>('GET', `/v1/search?${params}`);
  },
  // Every project's media, of the given kinds, each marked unseen while its notification is.
  allMedia: (kinds: string[] = []) => call<T.MediaItem[]>('GET', `/v1/media${kinds.length ? `?kind=${kinds.join(',')}` : ''}`),
  // A page of a Media list, and the counts on its filters (lib/mediaPages.ts).
  mediaPage: (query: URLSearchParams) => page<T.MediaPage>(`/v1/media?${query}`),
  mediaCounts: (query: URLSearchParams) => call<T.MediaCounts>('GET', `/v1/media/counts?${query}`),
  // What needs you from the daemon: questions, a page at a time (query: limit, cursor, unseen).
  notificationPage: (query: URLSearchParams) => page<T.NotificationPage>(`/v1/notifications?${query}`),
  seeNotifications: (req: T.SeeNotificationsRequest) => call<T.SeeNotificationsResult>('POST', '/v1/notifications/seen', req),
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
  // Settings → Diagnostics: spans and stalls since a stretch back from now
  // ("30m"), resource samples over a window ("1h"), and the app's own spans.
  diagnosticSpans: (since: string) => call<T.DiagnosticsSpans>('GET', `/v1/diagnostics/spans?since=${encodeURIComponent(since)}`),
  diagnosticStalls: (since: string) => call<T.DiagnosticsStalls>('GET', `/v1/diagnostics/stalls?since=${encodeURIComponent(since)}`),
  diagnosticResources: (span: D.ResourceWindow) => call<T.DiagnosticsResources>('GET', `/v1/diagnostics/resources?window=${span}`),
  sendClientSpans: (spans: T.TraceSpan[]) => call<void>('POST', clientSpansPath, { spans } satisfies T.ClientSpansRequest),
  diskUsage: () => call<T.DiskUsage>('GET', '/v1/usage/disk'),
  diskGuard: () => call<T.DiskGuard>('GET', '/v1/disk'),
  memoryUsage: () => call<T.MemoryUsage>('GET', '/v1/usage/memory'),
  cpuUsage: () => call<T.CPUUsage>('GET', '/v1/usage/cpu?interval=500ms'),
  // The end of the named agents' terminals, for the agent list's thumbnails.
  terminalTails: (project: string, agents: string[], lines?: number) =>
    call<T.TerminalTails>('GET', `/v1/projects/${encodeURIComponent(project)}/terminals?agents=${encodeURIComponent(agents.join(','))}${lines ? `&lines=${lines}` : ''}`),
  auth: () => call<T.AuthStatus>('GET', '/v1/auth'),
  // Cursor signs in with an API key (checked with Cursor before it is kept) or
  // through its own browser flow, which the daemon runs and reports on.
  saveCursorKey: (apiKey: string, account?: string) =>
    call<T.CursorKeyResponse>('POST', '/v1/auth/cursor', { apiKey, account } satisfies T.CursorKeyRequest),
  startCursorLogin: (account?: string) => call<T.CursorLogin>('POST', '/v1/auth/cursor/login', { account } satisfies T.CursorLoginRequest),
  // Codex, OpenCode and Cursor accounts: an API key stored under a name
  // (OpenCode's for a provider), and the stored ones managed like GitHub's.
  saveToolAccount: (tool: string, req: T.ToolAccountRequest) => call<void>('POST', `/v1/auth/accounts/${encodeURIComponent(tool)}`, req),
  removeToolAccount: (tool: string, account: string, provider?: string) =>
    call<void>('DELETE', `/v1/auth/accounts/${encodeURIComponent(tool)}/${encodeURIComponent(account)}${providerQuery(provider)}`),
  setDefaultToolAccount: (tool: string, account: string, provider?: string) =>
    call<void>('POST', `/v1/auth/accounts/${encodeURIComponent(tool)}/${encodeURIComponent(account)}/default${providerQuery(provider)}`),
  renameToolAccount: (tool: string, account: string, name: string, provider?: string) =>
    call<T.RenamedToolAccount>(
      'POST',
      `/v1/auth/accounts/${encodeURIComponent(tool)}/${encodeURIComponent(account)}/rename${providerQuery(provider)}`,
      { name } satisfies T.RenameToolAccountRequest,
    ),
  cursorLogin: () => call<T.CursorLogin>('GET', '/v1/auth/cursor/login'),
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
  image: () => call<T.ImageStatus>('GET', '/v1/image'),
  buildImage: (req: T.BuildImageRequest = {}) => call<T.Job>('POST', '/v1/image/build', req),

  jobs: () => call<T.Job[]>('GET', '/v1/jobs'),
  job: (id: string) => call<T.Job>('GET', `/v1/jobs/${id}`),
  jobLog: (id: string) => text('GET', `/v1/jobs/${id}/log`),
  cancelJob: (id: string) => call<T.Job>('POST', `/v1/jobs/${id}/cancel`),
};

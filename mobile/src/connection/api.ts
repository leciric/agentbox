// Typed calls to the daemon's API as a paired phone reaches it: under /api on
// the address it was paired at, with its token. The daemon passes on only
// what the phone's scopes allow (lanRouteOf in internal/daemon/lan.go), and
// a 401 means the pairing was revoked, which forgets it here.
// The paths are the desktop app's (desktop/src/renderer/lib/api.ts). What the
// screens read is made safe on the way in (parse.ts).
import * as T from '../shared/api.ts';
import * as P from './parse.ts';
import { forget, session } from './session.ts';

export class ApiError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function call<R>(method: string, path: string, body?: unknown): Promise<R> {
  const s = session();
  if (!s) throw new ApiError('not paired', 401);
  let res: Response;
  try {
    res = await fetch(`${s.origin}/api${path}`, {
      method,
      headers: { Authorization: `Bearer ${s.token}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (err) {
    throw new ApiError(err instanceof Error ? err.message : String(err), 0);
  }
  const text = await res.text();
  if (res.status === 401) void forget();
  if (!res.ok) {
    let message = text.trim();
    try {
      message = (JSON.parse(text) as T.Error).error || message;
    } catch {
      // not JSON
    }
    throw new ApiError(message || `HTTP ${res.status}`, res.status);
  }
  return (text ? JSON.parse(text) : undefined) as R;
}

const project = (name: string) => `/v1/projects/${encodeURIComponent(name)}`;
const agent = (ref: string) => `/v1/agents/${ref.split('/').map(encodeURIComponent).join('/')}`;

// isLead says a chat ref is a project's chat: "web" or "web/lead".
export const isLead = (ref: string) => {
  const [, name] = ref.split('/');
  return !name || name === T.LeadName;
};

// A project's chat is its lead's, under the project; an agent's is its own.
const chatBase = (ref: string) => (isLead(ref) ? `${project(ref.split('/')[0])}/chat` : `${agent(ref)}/chat`);

export interface ChatPage {
  limit?: number;
  before?: string;
  from?: string;
}

export const api = {
  settings: () => call<unknown>('GET', '/v1/settings').then(P.settings),
  projects: () => call<unknown>('GET', '/v1/projects').then(P.projects),
  agents: () => call<unknown>('GET', '/v1/agents').then(P.agents),
  projectChat: (name: string) => call<unknown>('GET', `${project(name)}/lead`).then(P.projectChat),
  chat: (ref: string, page: ChatPage = {}) => {
    const q = new URLSearchParams();
    if (page.limit) q.set('limit', String(page.limit));
    if (page.before) q.set('before', page.before);
    if (page.from) q.set('from', page.from);
    const qs = q.toString();
    return call<unknown>('GET', `${chatBase(ref)}${qs ? `?${qs}` : ''}`).then(P.chatThread);
  },
  startChat: (ref: string) => call<T.ChatSession>('POST', `${chatBase(ref)}/start`),
  sendMessage: (ref: string, text: string) => call<T.ChatItem>('POST', `${chatBase(ref)}/messages`, { text } satisfies T.ChatMessageRequest),
  cancelChat: (ref: string) => call<T.ChatSession>('POST', `${chatBase(ref)}/cancel`),
  answerPermission: (ref: string, item: string, optionId: string) =>
    call<T.ChatItem>('POST', `${chatBase(ref)}/permissions/${encodeURIComponent(item)}`, { optionId } satisfies T.ChatAnswerRequest),
  startAgent: (ref: string, paused: boolean) => call<void>('POST', `${agent(ref)}/${paused ? 'resume' : 'start'}`),
  questions: (name: string) => call<unknown>('GET', `${project(name)}/questions?all=1`).then(P.questions),
  answerQuestion: (name: string, id: string, answer: string) =>
    call<T.Question>('POST', `${project(name)}/questions/${encodeURIComponent(id)}/answer`, { answer } satisfies T.AnswerQuestionRequest),
  readyToReview: (name: string) => call<unknown>('GET', `${project(name)}/review?limit=50`).then(P.reviewPage),
  // Marking work reviewed, and asking for changes, are the "answer" scope's
  // (lanWrites in internal/daemon/lan.go).
  markReviewed: (ref: string, fingerprint: string) => call<void>('POST', `${agent(ref)}/review`, { verdict: 'reviewed', fingerprint } satisfies T.ReviewRequest),
  requestChanges: (ref: string, text: string, fingerprint: string) => call<unknown>('POST', `${agent(ref)}/request-changes`, { text, fingerprint } satisfies T.RequestChangesRequest),
  agentMedia: (ref: string) => call<unknown>('GET', `${agent(ref)}/media`).then(P.media),
  pulls: (name: string, refresh = false) => call<unknown>('GET', `${project(name)}/pulls${refresh ? '?refresh=1' : ''}`).then(P.pulls),
  // confirmed says the user confirmed the merge on the phone, which the
  // daemon asks of a phone's merge (lanAPI in internal/daemon/lan.go).
  merge: (name: string, number: number, method: string) =>
    call<T.PullRequest>('POST', `${project(name)}/pulls/${number}/merge`, { method, confirmed: true } satisfies T.MergePullRequestRequest),
};

// mediaSource is an image of the daemon's for <Image source>: its address
// and the token it needs.
export function mediaSource(path: string): { uri: string; headers: Record<string, string> } | undefined {
  const s = session();
  return s ? { uri: `${s.origin}/api${path}`, headers: { Authorization: `Bearer ${s.token}` } } : undefined;
}

export const mediaFilePath = (id: string) => `/v1/media/${encodeURIComponent(id)}/file`;
export const chatImagePath = (ref: string, id: string) => `${chatBase(ref)}/images/${encodeURIComponent(id)}`;

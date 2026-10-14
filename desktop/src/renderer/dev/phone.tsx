// The phone's web app (web/PhoneApp.tsx) against made-up chats, for
// preview.tsx's ?phone= scenarios: two projects whose chats are in every
// state a phone shows — a turn running live (thinking, reading a file,
// running the tests, writing its answer, done, and again), a permission to
// give, a question, an error, work left in the background, an unread
// answer — with the connection to drop and get back.
//
//   ?phone=list             the chat list (the default)
//   ?phone=chat             agent-12's chat, its turn running live
//   ?phone=lead             the project's chat, rich blocks and code in it
//   ?phone=frame            the list in a 390×844 frame in the middle of the
//                           page, for watching it on a wide screen
//   &controls=1             a button to drop the connection and get it back
//                           (beside the phone, with ?phone=frame)
//   &offline=1              starts with the connection dropped
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRoot } from 'react-dom/client';
import { Toaster } from 'sonner';
import type { ConnectionState } from '../../preload';
import type * as T from '../../shared/api';
import { TooltipProvider } from '../components/ui/tooltip';
import { connectEvents } from '../lib/events';
import { PhoneApp } from '../web/PhoneApp';
import { seen } from '../web/seen';
import { buildFixtures, PROJECT, richChat } from './fixtures';

const OTHER = 'reminders-app';
const at = (ago: number) => new Date(Date.now() - ago).toISOString();
const session = (over: Partial<T.ChatSession> = {}): T.ChatSession => ({ state: 'ready', tool: 'claude', options: [], commands: [], ...over });

type Bridge = {
  request: (method: string, path: string, body?: unknown) => Promise<unknown>;
  onEvent: (fn: (event: unknown) => void) => () => void;
  connection: () => Promise<ConnectionState>;
  onConnection: (fn: (state: ConnectionState) => void) => () => void;
};

function agentOf(name: string, project: string, over: Partial<T.Agent>): T.Agent {
  return {
    ref: `${project}/${name}`,
    project,
    name,
    title: '',
    instance: '',
    ai: 'claude',
    autonomous: true,
    branch: `agentbox/${name}`,
    baseRef: 'main',
    baseCommit: 'abc123',
    worktree: `/w/${name}`,
    source: 'main',
    claudeAccount: '',
    githubAccount: '',
    interface: 'claude',
    state: 'running',
    chat: 'ready',
    ip: '',
    createdAt: at(3_600_000),
    ...over,
  };
}

// A finished turn: your message, some work, and its answer.
function settled(id: string, ask: string, answer: string, took: number, ago: number, tools: Partial<T.ChatTool>[] = []): T.ChatItem[] {
  const start = ago + took;
  return [
    { id, turn: '', kind: 'user', text: ask, result: { state: 'completed', endedAt: at(ago) }, createdAt: at(start), updatedAt: at(ago) },
    ...tools.map((tool, i) => ({
      id: `${id}-t${i}`,
      turn: id,
      kind: 'tool',
      tool: { callId: `${id}-c${i}`, title: tool.command ?? tool.title ?? 'tool', kind: 'other', status: 'completed', ...tool },
      createdAt: at(start - 1000 * (i + 1)),
      updatedAt: at(start - 1000 * (i + 1)),
    })),
    { id: `${id}-a`, turn: id, kind: 'assistant', text: answer, createdAt: at(ago + 500), updatedAt: at(ago) },
  ] as T.ChatItem[];
}

export function startPhonePreview(mode: string, params: URLSearchParams): void {
  const base = buildFixtures().projects[0];
  const projects: T.Project[] = [
    { ...base, name: PROJECT, displayName: 'AgentBox' },
    { ...base, name: OTHER, displayName: 'Reminders app' },
  ];
  const agents: T.Agent[] = [
    agentOf('agent-12', PROJECT, { title: 'Reminders page with tests', chat: 'running' }),
    agentOf('agent-09', PROJECT, { title: 'Clean up the build folder', chat: 'waiting' }),
    agentOf('agent-31', PROJECT, { title: 'Fix the login redirect', outcome: 'asking', outcomeNote: 'Should the redirect keep the query string?' }),
    agentOf('agent-20', PROJECT, { title: 'Watch CI on #301', background: ['gh run watch 1832'] }),
    agentOf('agent-04', PROJECT, { title: 'Translate the settings page', outcome: 'done' }),
    agentOf('agent-07', OTHER, { title: 'Flaky upload test', outcome: 'failed', outcomeNote: 'websocket: close 1006 (abnormal closure)' }),
    agentOf('agent-02', OTHER, { title: 'Dark mode for the calendar', state: 'stopped', chat: 'off' }),
  ];
  const lead = (project: string, chat: string): T.ProjectChat => ({ project, ref: `${project}/lead`, started: true, chat, baseRef: 'main' });
  const leads: Record<string, T.ProjectChat> = { [PROJECT]: lead(PROJECT, 'running'), [OTHER]: lead(OTHER, 'ready') };

  // The chats, as the daemon holds them.
  const rich = richChat();
  const threads = new Map<string, T.ChatThread>();
  const put = (ref: string, items: T.ChatItem[], s: T.ChatSession = session()) => threads.set(ref, { agent: ref, seq: 1, session: s, items });
  put(`${PROJECT}/lead`, [
    ...rich.items.map((it) => ({ ...it, createdAt: at(600_000), updatedAt: at(590_000) })),
    { id: 'lu2', turn: '', kind: 'user', text: 'Check what agent-12 is doing and merge it once CI passes.', createdAt: at(41_000), updatedAt: at(41_000) },
    { id: 'lu2-t', turn: 'lu2', kind: 'tool', tool: { callId: 'x1', title: 'gh pr checks 302 --watch', kind: 'execute', command: 'gh pr checks 302 --watch', status: 'in_progress' }, createdAt: at(38_000), updatedAt: at(38_000) },
  ] as T.ChatItem[], session({ state: 'running', turnStartedAt: at(41_000) }));
  put(`${OTHER}/lead`, settled('ou1', 'What is left before the release?', 'Two things: the flaky upload test (agent-07 stopped on it) and the calendar’s dark mode.', 64_000, 3_000_000));
  put(`${PROJECT}/agent-09`, [
    { id: 'u9', turn: '', kind: 'user', text: 'Clean the build folder and rebuild.', createdAt: at(80_000), updatedAt: at(80_000) },
    { id: 'p9', turn: 'u9', kind: 'permission', permission: { callId: 'c9', title: 'rm -rf build', options: [{ id: 'allow', name: 'Allow', kind: 'allow_once' }, { id: 'reject', name: 'Reject', kind: 'reject_once' }] }, createdAt: at(20_000), updatedAt: at(20_000) },
  ] as T.ChatItem[], session({ state: 'waiting', turnStartedAt: at(80_000) }));
  put(`${PROJECT}/agent-31`, settled('u31', 'Fix the login redirect.', 'I found two ways to fix it. **Should the redirect keep the query string?** Keeping it is safer for deep links.', 142_000, 400_000, [{ kind: 'read', title: 'Read auth.go', paths: ['/w/internal/auth.go'] }]));
  put(`${PROJECT}/agent-20`, settled('u20', 'Watch CI on #301.', 'CI is running; I’m watching it in the background.', 21_000, 200_000), session({ background: ['gh run watch 1832'] }));
  put(`${PROJECT}/agent-04`, settled('u4', 'Translate the settings page into Portuguese.', 'Done: 84 strings in `pt-BR.ts`, and the PR is #305.', 318_000, 90_000, [{ kind: 'edit', title: 'pt-BR.ts', paths: ['/w/desktop/src/shared/i18n/pt-BR.ts'] }]));
  put(`${OTHER}/agent-07`, [
    ...settled('u7', 'Find out why the upload test is flaky.', 'Looking at the retries now.', 95_000, 700_000),
    { id: 'e7', turn: 'u7', kind: 'error', text: 'websocket: close 1006 (abnormal closure)', createdAt: at(699_000), updatedAt: at(699_000) },
  ] as T.ChatItem[]);
  put(`${OTHER}/agent-02`, settled('u2', 'Dark mode for the calendar.', 'Paused here: the machine was stopped.', 30_000, 86_400_000));
  // agent-12's earlier turn, then the live one (below).
  put(`${PROJECT}/agent-12`, settled('u12a', 'Plan the reminders page.', 'Plan: an API under `/v1/reminders`, a page, and tests for both.', 47_000, 900_000, [
    { kind: 'search', title: 'routes' },
    { kind: 'read', title: 'Read router.go', paths: ['/w/internal/api/router.go'] },
  ]));

  // What this phone read: agent-04's answer came after, so it is new.
  localStorage.removeItem('agentbox.phone.seen');
  seen.read(`${PROJECT}/agent-04`, at(600_000));

  // The bridge: the daemon's answers, its events and the connection.
  const bridge = (window as unknown as { agentbox: Bridge }).agentbox;
  const inner = bridge.request;
  const answer = (value: unknown) => ({ status: 200, body: JSON.stringify(value), contentType: 'application/json' });
  const listeners = new Set<(event: unknown) => void>();
  const connListeners = new Set<(state: ConnectionState) => void>();
  let connection: ConnectionState = params.get('offline') === '1' ? { state: 'disconnected', error: "AgentBox on your computer isn't reachable" } : { state: 'connected' };
  const chatRef = (path: string) => {
    const lead = /^\/v1\/projects\/([^/]+)\/chat$/.exec(path);
    if (lead) return `${decodeURIComponent(lead[1])}/lead`;
    const agent = /^\/v1\/agents\/([^/]+)\/([^/]+)\/chat$/.exec(path);
    return agent ? `${decodeURIComponent(agent[1])}/${decodeURIComponent(agent[2])}` : undefined;
  };
  bridge.request = async (method, path, body) => {
    if (connection.state !== 'connected') return { status: 502, body: '{"error":"unreachable"}', contentType: 'application/json' };
    const [route] = path.split('?');
    if (method === 'GET' && route === '/v1/projects') return answer(projects);
    if (method === 'GET' && route === '/v1/agents') return answer(agents);
    if (method === 'GET' && route === '/v1/settings') return answer({ language: params.get('lang') ?? 'en-US' });
    const leadOf = method === 'GET' ? /^\/v1\/projects\/([^/]+)\/lead$/.exec(route) : null;
    if (leadOf) return answer(leads[decodeURIComponent(leadOf[1])]);
    const ref = chatRef(route);
    if (method === 'GET' && ref && threads.has(ref)) return answer(threads.get(ref));
    if (method === 'POST' && route.endsWith('/chat/start')) return answer(session());
    if (method === 'GET' && route.endsWith('/chat/runners')) return answer({ tool: 'claude', runners: [{ tool: 'claude', name: 'Claude Code', ready: true, installed: true }] } satisfies T.LeadRunners);
    // Lists no fixture fills: none waiting, rather than the dev bridge's {}.
    if (method === 'GET' && /\/(questions|events|pages|connectors|credentials|skills|checkpoints|notes)$/.test(route)) return answer([]);
    return inner(method, path, body);
  };
  bridge.onEvent = (fn) => {
    listeners.add(fn);
    return () => listeners.delete(fn);
  };
  bridge.connection = async () => connection;
  bridge.onConnection = (fn) => {
    connListeners.add(fn);
    return () => connListeners.delete(fn);
  };
  const setConnection = (next: ConnectionState) => {
    connection = next;
    for (const fn of connListeners) fn(next);
  };
  const toggle = () => setConnection(connection.state === 'connected' ? { state: 'disconnected', error: "AgentBox on your computer isn't reachable" } : { state: 'connected' });
  // The frame's button, outside the phone (startPhoneFrame).
  window.addEventListener('message', (event) => {
    if (event.data === 'phone-preview:toggle') toggle();
  });

  // An event changes the daemon's thread, and reaches the app only while
  // connected: what happens while it's away is read again on reconnecting.
  const emit = (ref: string, ev: Omit<T.ChatEvent, 'agent' | 'seq'>) => {
    const thread = threads.get(ref)!;
    const seq = thread.seq + 1;
    let items = thread.items;
    if (ev.item) {
      const i = items.findIndex((it) => it.id === ev.item!.id);
      items = i < 0 ? [...items, ev.item] : items.with(i, ev.item);
    }
    if (ev.append) items = items.map((it) => (it.id === ev.append!.id ? { ...it, text: (it.text ?? '') + ev.append!.text } : it));
    threads.set(ref, { ...thread, seq, items, session: ev.session ?? thread.session });
    if (ev.session) {
      const a = agents.find((x) => x.ref === ref);
      if (a) a.chat = ev.session.state;
    }
    if (connection.state !== 'connected') return;
    for (const fn of listeners) fn({ type: 'chat', time: new Date().toISOString(), data: { agent: ref, seq, ...ev } });
  };

  // agent-12's turn, over and over: thinking, reading, the tests, its
  // answer streaming in, done.
  const ref = `${PROJECT}/agent-12`;
  let turn = 0;
  const live = () => {
    turn++;
    const id = `live${turn}`;
    const started = new Date().toISOString();
    const user: T.ChatItem = { id, turn: '', kind: 'user', text: turn === 1 ? 'Add the reminders page, with tests.' : `Run the tests again (pass ${turn}).`, createdAt: started, updatedAt: started };
    const toolItem = (n: number, tool: Partial<T.ChatTool>, status: string): T.ChatItem => ({
      id: `${id}-t${n}`,
      turn: id,
      kind: 'tool',
      tool: { callId: `${id}-c${n}`, title: tool.title ?? '', kind: 'other', status, ...tool },
      createdAt: started,
      updatedAt: new Date().toISOString(),
    });
    const read = { kind: 'read', title: 'Read reminders.go', paths: ['/w/internal/api/reminders.go'] };
    const test = { kind: 'execute', title: 'go test ./internal/api/...', command: 'go test ./internal/api/...' };
    const words = 'All 14 tests pass. The page lists reminders by due date, and **Snooze** moves one to tomorrow. Next I’ll open the pull request.'.split(/(?<= )/);
    const steps: [number, () => void][] = [
      [0, () => emit(ref, { item: user })],
      [100, () => emit(ref, { session: session({ state: 'running', turnStartedAt: started }) })],
      [2500, () => emit(ref, { item: toolItem(1, read, 'in_progress') })],
      [2500, () => emit(ref, { item: toolItem(1, read, 'completed') })],
      [300, () => emit(ref, { item: toolItem(2, test, 'in_progress') })],
      [5000, () => emit(ref, { item: { ...toolItem(2, test, 'completed'), tool: { ...toolItem(2, test, 'completed').tool!, output: 'ok  \tagentbox/internal/api\t2.41s' } } })],
      [1500, () => emit(ref, { item: { id: `${id}-a`, turn: id, kind: 'assistant', text: '', streaming: true, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() } })],
      ...words.map((w): [number, () => void] => [140, () => emit(ref, { append: { id: `${id}-a`, text: w } })]),
      [400, () => {
        const text = words.join('');
        const now = new Date().toISOString();
        emit(ref, { item: { id: `${id}-a`, turn: id, kind: 'assistant', text, createdAt: now, updatedAt: now } });
        emit(ref, { item: { ...user, result: { state: 'completed', endedAt: now }, updatedAt: now } });
        emit(ref, { session: session() });
      }],
      [7000, live],
    ];
    let wait = 0;
    for (const [delay, step] of steps) {
      wait += delay;
      setTimeout(step, wait);
    }
  };
  setTimeout(live, 600);

  if (mode === 'chat') location.hash = `#/a/${PROJECT}/agent-12`;
  else if (mode === 'lead') location.hash = `#/p/${PROJECT}`;
  else if (!location.hash.startsWith('#/')) location.hash = '#/';

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 5_000 } } });
  connectEvents(queryClient);
  const phone: T.LANPhone = { id: 'p1', name: 'Pixel 9', paired: at(86_400_000), scopes: ['read', 'chat', 'answer', 'merge'] };
  createRoot(document.getElementById('root')!).render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider delayDuration={250}>
        <PhoneApp phone={phone} />
        <Toaster position="top-center" />
        {params.get('controls') === '1' && (
          <div style={{ position: 'fixed', right: 8, bottom: 92, zIndex: 80, display: 'grid', gap: 6 }}>
            <button
              data-dev-drop
              className="rounded-lg border border-line-strong bg-overlay px-2.5 py-1 text-[11px] text-secondary shadow-md"
              onClick={toggle}
            >
              Drop / restore
            </button>
          </div>
        )}
      </TooltipProvider>
    </QueryClientProvider>,
  );
}

// PhoneFrame shows the phone's page at a phone's size, in a frame in the
// middle of a wide window: the page itself, at 390 px, in an iframe.
export function startPhoneFrame(params: URLSearchParams): void {
  const inner = new URLSearchParams(params);
  inner.set('phone', inner.get('open') ?? 'list');
  inner.delete('open');
  inner.delete('controls');
  let frame: HTMLIFrameElement | null = null;
  document.body.style.margin = '0';
  document.body.style.background = params.get('theme') === 'light' ? '#e7e7ee' : '#030306';
  createRoot(document.getElementById('root')!).render(
    <div style={{ display: 'flex', height: '100vh', alignItems: 'center', justifyContent: 'center', gap: 24 }}>
      <iframe
        ref={(el) => {
          frame = el;
        }}
        title="phone"
        src={`${location.pathname}?${inner}`}
        style={{ width: 390, height: 844, maxHeight: 'calc(100vh - 24px)', border: '10px solid #1c1c22', borderRadius: 44, background: 'transparent', boxShadow: '0 20px 60px rgb(0 0 0 / 0.5)' }}
      />
      {params.get('controls') === '1' && (
        <button
          data-dev-drop
          style={{ padding: '6px 12px', borderRadius: 8, border: '1px solid #444', background: '#1c1c22', color: '#ddd', font: '13px system-ui' }}
          onClick={() => frame?.contentWindow?.postMessage('phone-preview:toggle', '*')}
        >
          Drop / restore the connection
        </button>
      )}
    </div>,
  );
}

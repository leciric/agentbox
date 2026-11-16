// ?app=office&office=1: the Office (components/office) with more than one
// project in it. The fixture project's agents are joined by Storefront's and
// Quill's (dev/review.tsx's seedV2 makes those projects), in every state a
// desk shows: working, a question for you, finished with a pull request whose
// checks fail, merged, idle and stopped.
import type { QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { addDevAgent, buildFixtures, PROJECT } from './fixtures';

const min = 60_000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

// typical (&fleet=typical) keeps six of the fixture project's agents, for a
// team of about the size most have: the room as it usually is, rather than
// the crowded fixture.
export function seedOffice(queryClient: QueryClient, opts: { typical?: boolean } = {}): void {
  const base = buildFixtures().agents[0];
  const make = (project: string, name: string, title: string, over: Partial<T.Agent>, age: number): T.Agent => ({
    ...base,
    ref: `${project}/${name}`,
    project,
    name,
    title,
    branch: `agentbox/${name}`,
    createdAt: ago(age * min),
    outcome: undefined,
    outcomeNote: undefined,
    leadSession: undefined,
    goal: undefined,
    ...over,
  });
  const agents: T.Agent[] = [
    make('storefront', 'agent-3', 'Checkout fails on Safari when the cart is empty', { chat: 'running' }, 90),
    make('storefront', 'agent-4', 'Add Apple Pay to checkout', { chat: 'ready', outcome: 'asking', outcomeNote: 'Should Apple Pay show for guests too, or only signed-in shoppers?' }, 80),
    make('storefront', 'agent-5', 'Product page images load slowly', { chat: 'ready', ai: 'gemini' }, 70),
    make('storefront', 'agent-6', 'Translate the emails into Portuguese', { chat: 'running', ai: 'codex' }, 60),
    make('quill', 'agent-8', 'Export notes as Markdown', { chat: 'ready' }, 50),
    make('quill', 'agent-9', 'Sync drafts across devices', { state: 'stopped', chat: undefined }, 40),
  ];
  for (const a of agents) addDevAgent(a);
  queryClient.setQueryData<T.Agent[]>(['agents'], (all) => [...(all ?? []), ...agents]);
  const keep = new Set((queryClient.getQueryData<T.Agent[]>(['agents']) ?? []).filter((a) => a.project === PROJECT).slice(0, 6).map((a) => a.ref));
  const kept = (all: T.Agent[]) => (opts.typical ? all.filter((a) => a.project !== PROJECT || keep.has(a.ref)) : all);
  queryClient.setQueryData<T.Agent[]>(['agents'], (all) => kept(all ?? []));
  const pr = (number: number, title: string, over: Partial<T.PullRequest> = {}): T.PullRequest => ({ number, title, state: 'OPEN', checks: 'passing', url: `https://github.com/example/storefront/pull/${number}`, ...over });
  const fleetAgent = (a: T.Agent, extra: Partial<T.FleetAgent>): T.FleetAgent => ({ ...a, changes: { files: 0, insertions: 0, deletions: 0, dirty: false }, media: 0, retire: { safe: false }, ...extra });
  // The fixture project's fleet too: the dev bridge has no answer for it, so
  // its pull requests would go missing at the first refetch.
  const fleets: Record<string, T.Fleet> = {
    [PROJECT]: buildFixtures().fleet,
    storefront: {
      project: 'storefront',
      creating: [],
      idle: 0,
      agents: [
        fleetAgent(agents[0], { changes: { files: 3, insertions: 48, deletions: 12, dirty: true }, lastActive: ago(1 * min) }),
        fleetAgent(agents[1], { lastActive: ago(6 * min) }),
        fleetAgent(agents[2], { pr: pr(212, 'perf: product images load in a quarter of the time', { checks: 'failing' }), changes: { files: 5, insertions: 120, deletions: 64, dirty: false } }),
        fleetAgent(agents[3], { changes: { files: 9, insertions: 310, deletions: 4, dirty: true } }),
      ],
    },
    quill: {
      project: 'quill',
      creating: [],
      idle: 0,
      agents: [fleetAgent(agents[4], { pr: pr(41, 'feat: export notes as Markdown', { state: 'MERGED' }) }), fleetAgent(agents[5], {})],
    },
  };
  for (const [name, fleet] of Object.entries(fleets)) if (name !== PROJECT) queryClient.setQueryData(['fleet', name], fleet);
  queryClient.setQueryData(['questions', 'storefront'], []);
  queryClient.setQueryData(['questions', 'quill'], []);
  const json = (v: unknown) => ({ status: 200, body: JSON.stringify(v), contentType: 'application/json' });
  // In front of the dev bridge, as other scenarios wrap it: a later answer for
  // any project's fleet would otherwise be an empty one.
  type Bridge = { request: (method: string, path: string, body?: unknown) => Promise<unknown> };
  const bridge = (window as unknown as { agentbox: Bridge }).agentbox;
  const inner = bridge.request;
  bridge.request = async (method, path, body) => {
    const m = method === 'GET' && /^\/v1\/projects\/([^/]+)\/fleet$/.exec(path);
    if (m && fleets[decodeURIComponent(m[1])]) return json(fleets[decodeURIComponent(m[1])]);
    if (method === 'GET' && /^\/v1\/projects\/(storefront|quill)\/questions$/.test(path)) return json([]);
    // Their pull requests' details, as the daemon gives them: the Office's
    // board and cards open them.
    const pull = method === 'GET' && /^\/v1\/projects\/(storefront|quill)\/pulls\/(\d+)(\/files)?$/.exec(path);
    if (pull) {
      const row = fleets[pull[1]]?.agents.map((x) => x.pr).find((x) => x?.number === Number(pull[2]));
      if (row) return json(pull[3] ? ({ files: [], truncated: false } satisfies T.PullRequestFiles) : ({ ...row, body: 'What it changes, and how it was checked.', changedFiles: 0, checkRuns: [] } satisfies T.PullRequestDetail));
    }
    if (opts.typical && method === 'GET' && path === '/v1/agents') {
      const r = (await inner(method, path, body)) as { status: number; body: string; contentType: string };
      return { ...r, body: JSON.stringify(kept([...(JSON.parse(r.body) as T.Agent[]), ...agents.filter((a) => !r.body.includes(`"${a.ref}"`))])) };
    }
    return inner(method, path, body);
  };
}

// stubProjectChats answers the chats the dev bridge has none of: seedV2's
// extra projects' (Storefront, Quill, Courier…), their leads' and agents',
// and, in the Office, the fixture project's agents beyond the few it has a
// conversation for. A chat that can't be read shows an error where a quiet
// chat belongs. Each agent's opens on its task and, when it's asking, its
// question; what you send stays in it, as the daemon keeps it.
export function stubProjectChats(): void {
  type Bridge = { request: (method: string, path: string, body?: unknown) => Promise<unknown> };
  type Reply = { status: number; body: string; contentType: string };
  const bridge = (window as unknown as { agentbox: Bridge }).agentbox;
  const inner = bridge.request;
  const threads = new Map<string, T.ChatThread>();
  let agents: Promise<T.Agent[]> | undefined;
  const agentList = () =>
    (agents ??= (inner('GET', '/v1/agents') as Promise<Reply>).then((r) => {
      try {
        return JSON.parse(r.body) as T.Agent[];
      } catch {
        return [];
      }
    }));
  const item = (id: string, kind: string, text: string, at: number): T.ChatItem => ({ id, turn: id, kind, text, createdAt: ago(at), updatedAt: ago(at) }) as T.ChatItem;
  const threadOf = async (ref: string): Promise<T.ChatThread> => {
    const had = threads.get(ref);
    if (had) return had;
    const session: T.ChatSession = { state: 'off', tool: 'claude', options: [], commands: [] } as unknown as T.ChatSession;
    const items: T.ChatItem[] = [];
    if (!ref.endsWith('/lead')) {
      const a = (await agentList()).find((x) => x.ref === ref);
      if (a?.title) items.push(item(`${ref}#task`, 'user', a.title, 30 * min));
      if (a?.outcome === 'asking' && a.outcomeNote) items.push(item(`${ref}#ask`, 'assistant', a.outcomeNote, 5 * min));
      else if (a?.title) items.push(item(`${ref}#on`, 'assistant', 'On it: reading the code first, then a plan.', 20 * min));
    }
    const t: T.ChatThread = { agent: ref, seq: items.length, session, items };
    threads.set(ref, t);
    return t;
  };
  const reply = (v: unknown): Reply => ({ status: 200, body: JSON.stringify(v), contentType: 'application/json' });
  bridge.request = async (method, path, body) => {
    const m = /^\/v1\/(?:projects\/([^/]+)(?:\/sessions\/[^/]+)?|agents\/([^/]+)\/([^/?]+))\/chat(\/messages)?(?:\?|$)/.exec(path);
    if (!m) return inner(method, path, body);
    const project = decodeURIComponent(m[1] ?? m[2]);
    const ref = m[1] ? `${project}/lead` : `${project}/${decodeURIComponent(m[3])}`;
    if (method === 'GET' && !m[4]) {
      // The fixture project's own conversations, where it has them.
      if (project === PROJECT && !threads.has(ref)) {
        const r = (await inner(method, path, body)) as Reply;
        try {
          if (Array.isArray((JSON.parse(r.body) as T.ChatThread).items)) return r;
        } catch {
          // Not one: made up below.
        }
      }
      return reply(await threadOf(ref));
    }
    // Sent to a chat made up here: it stays in it.
    if (method === 'POST' && m[4] && (project !== PROJECT || threads.has(ref))) {
      const t = await threadOf(ref);
      const text = (body as { text?: string } | undefined)?.text ?? '';
      const sent = item(`${ref}#${t.items.length}`, 'user', text, 0);
      t.items = [...t.items, sent];
      t.seq += 1;
      return reply(sent);
    }
    return inner(method, path, body);
  };
}

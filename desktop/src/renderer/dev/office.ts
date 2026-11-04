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

export function seedOffice(queryClient: QueryClient): void {
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
    make('storefront', 'agent-5', 'Product page images load slowly', { chat: 'ready' }, 70),
    make('storefront', 'agent-6', 'Translate the emails into Portuguese', { chat: 'running', ai: 'codex' }, 60),
    make('quill', 'agent-8', 'Export notes as Markdown', { chat: 'ready' }, 50),
    make('quill', 'agent-9', 'Sync drafts across devices', { state: 'stopped', chat: undefined }, 40),
  ];
  for (const a of agents) addDevAgent(a);
  queryClient.setQueryData<T.Agent[]>(['agents'], (all) => [...(all ?? []), ...agents]);
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
    return inner(method, path, body);
  };
}

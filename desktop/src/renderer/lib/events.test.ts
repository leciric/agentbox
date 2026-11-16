// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { QueryClient, QueryObserver } from '@tanstack/react-query';
import type { ConnectionState } from '../../preload';
import type * as T from '../../shared/api';

// A bridge whose events and connection changes the test sends itself.
let emit: (event: unknown) => void = () => {};
let reconnect: (state: ConnectionState) => void = () => {};
(globalThis as unknown as { window: unknown }).window = {
  agentbox: {
    onEvent: (fn: (event: unknown) => void) => {
      emit = fn;
      return () => {};
    },
    onConnection: (fn: (state: ConnectionState) => void) => {
      reconnect = fn;
      return () => {};
    },
    connection: async (): Promise<ConnectionState> => ({ state: 'connected' }),
  },
};
const { connectEvents } = await import('./events.ts');

const agent = (name: string) => ({ ref: `p/${name}`, project: 'p', name, state: 'running' }) as T.Agent;

// agentsQuery is ['agents'] as the sidebar and the rail watch it, holding two
// agents, with a fetch that waits until the test answers it.
async function agentsQuery() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  connectEvents(client);
  let answer: (agents: T.Agent[]) => void = () => {};
  let fetches = 0;
  const observer = new QueryObserver<T.Agent[]>(client, {
    queryKey: ['agents'],
    queryFn: () => {
      fetches++;
      return new Promise((resolve) => (answer = resolve));
    },
  });
  const seen: (T.Agent[] | undefined)[] = [];
  observer.subscribe((result) => seen.push(result.data));
  answer([agent('a'), agent('b')]);
  await new Promise((resolve) => setImmediate(resolve));
  return { client, seen, answer: (agents: T.Agent[]) => answer(agents), fetches: () => fetches };
}

const settle = () => new Promise((resolve) => setImmediate(resolve));

// An agent removed after its pull request merged makes the daemon announce
// it, and the list is fetched again: until the new list arrives, the one
// before is what's shown, never nothing.
test('the agents stay listed while a removal refetches them', async () => {
  const q = await agentsQuery();
  emit({ type: 'agent', data: { ref: 'p/b', state: '', removed: true } satisfies Partial<T.AgentChange> });
  await settle();
  assert.equal(q.fetches(), 2);
  assert.deepEqual(q.client.getQueryData<T.Agent[]>(['agents'])?.map((a) => a.name), ['a', 'b']);
  q.answer([agent('a')]);
  await settle();
  assert.deepEqual(q.client.getQueryData<T.Agent[]>(['agents'])?.map((a) => a.name), ['a']);
  assert.ok(q.seen.slice(1).every((data) => data !== undefined && data.length > 0), 'the list was never empty on the way');
});

test('the agents stay listed while a finished job refetches them', async () => {
  const q = await agentsQuery();
  emit({ type: 'job', data: { id: 'j1', kind: 'destroy', status: 'succeeded', createdAt: new Date().toISOString() } satisfies Partial<T.Job> });
  await settle();
  assert.equal(q.fetches(), 2);
  assert.equal(q.client.getQueryData<T.Agent[]>(['agents'])?.length, 2);
  q.answer([agent('a'), agent('b')]);
  await settle();
  assert.ok(q.seen.every((data, i) => i === 0 || data?.length === 2));
});

test('the agents stay listed while a reconnect refetches everything', async () => {
  const q = await agentsQuery();
  reconnect({ state: 'disconnected', error: 'gone' });
  reconnect({ state: 'connected' });
  await settle();
  assert.equal(q.client.getQueryData<T.Agent[]>(['agents'])?.length, 2);
  q.answer([agent('a'), agent('b')]);
  await settle();
  assert.ok(q.seen.every((data, i) => i === 0 || data?.length === 2));
});

// A re-read of GitHub that found nothing new still ends the tab's
// "refreshing"; only one that found something redraws the fleet too.
test('a pulls event refetches the tab, and the fleet only when something moved', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  connectEvents(client);
  client.setQueryData(['pulls', 'p'], { refreshing: true });
  client.setQueryData(['fleet', 'p'], {});
  const stale = (key: string) => client.getQueryState([key, 'p'])?.isInvalidated;
  emit({ type: 'pulls', data: { project: 'p', github: 'acme/x', fetchedAt: '', unchanged: true } satisfies T.PullsChange });
  await settle();
  assert.equal(stale('pulls'), true);
  assert.equal(stale('fleet'), false);
  emit({ type: 'pulls', data: { project: 'p', github: 'acme/x', fetchedAt: '' } satisfies T.PullsChange });
  await settle();
  assert.equal(stale('fleet'), true);
});

// A project just added announces itself once its chat has its first session:
// a session list read before that, empty or not found, is read again.
test('a project announced reads its chat sessions again', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  connectEvents(client);
  let fetches = 0;
  const observer = new QueryObserver(client, { queryKey: ['leadSessions', 'fresh'], queryFn: async () => (++fetches === 1 ? [] : [{ id: 'main' }]) });
  const unsubscribe = observer.subscribe(() => {});
  await settle();
  assert.deepEqual(observer.getCurrentResult().data, []);
  emit({ type: 'project', data: { name: 'fresh' } satisfies T.ProjectChange });
  await settle();
  await settle();
  assert.deepEqual(observer.getCurrentResult().data, [{ id: 'main' }]);
  emit({ type: 'project', data: { name: 'fresh', removed: true } satisfies T.ProjectChange });
  await settle();
  assert.equal(fetches, 2, 'a project removed reads nothing again');
  unsubscribe();
  client.clear();
});

test('a live resource sample lands on the agent history, and the window slides', async () => {
  const { appendResourcePoint } = await import('./events.ts');
  const client = new QueryClient();
  connectEvents(client);
  const point = (s: number, cpu: number): T.AgentResourcePoint => ({ at: new Date(Date.UTC(2026, 10, 6, 12, 0, s)).toISOString(), cpu, memory: 1, swap: 0, diskRead: 0, diskWrite: 0 });
  // No history fetched (the view isn't open): nothing is made up.
  emit({ type: 'agent-resources', data: { ref: 'p/a', point: point(2, 10) } });
  assert.equal(client.getQueryData(['agentResources', 'p/a']), undefined);
  client.setQueryData<T.AgentResourceHistory>(['agentResources', 'p/a'], { ref: 'p/a', intervalSeconds: 2, windowSeconds: 10, sampling: true, points: [point(0, 5)] });
  emit({ type: 'agent-resources', data: { ref: 'p/a', point: point(2, 10) } });
  emit({ type: 'agent-resources', data: { ref: 'p/a', point: point(2, 10) } }); // the same one twice
  emit({ type: 'agent-resources', data: { ref: 'p/a', point: point(11, 20) } });
  const h = client.getQueryData<T.AgentResourceHistory>(['agentResources', 'p/a']);
  assert.deepEqual(h?.points.map((p) => p.cpu), [10, 20]);
  assert.equal(appendResourcePoint(h!, point(1, 1)), h, 'an older sample changes nothing');
});

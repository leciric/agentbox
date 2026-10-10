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

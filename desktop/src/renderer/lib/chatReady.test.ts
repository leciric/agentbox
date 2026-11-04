// Run with `npm test` (node's own test runner, which strips the types).
//
// The order opening a project just added can take, against the query options
// its chat's panel uses (ProjectChatPanel's SessionChatPanel).
import assert from 'node:assert/strict';
import { test } from 'node:test';

// TanStack Query decides once, as it loads, whether it runs in a browser: with
// no window it takes itself for a server and never polls (refetchInterval).
(globalThis as unknown as { window: unknown }).window = {};
const { QueryClient, QueryObserver } = await import('@tanstack/react-query');
const { chatLoadRetry, chatReadyRetry } = await import('./chat.ts');

// A project's chat asked about before the daemon has the project, the order
// opening a project just added can take: the first answers are "not found",
// then the project is there. The chat waits for it, where it used to keep
// the first "not found" and say the chat was unavailable until the project
// was opened again.
function projectNotThereYet(answersBefore: number) {
  let asked = 0;
  const queryFn = async () => {
    asked++;
    if (asked <= answersBefore) throw Object.assign(new Error('project "p": not found'), { status: 404 });
    return [{ id: 'main', ref: 'p/lead.main' }];
  };
  return { queryFn, asked: () => asked };
}

async function settled(observer: InstanceType<typeof QueryObserver<unknown>>, until: (r: { status: string }) => boolean) {
  const seen: { status: string; error: unknown }[] = [];
  return new Promise<typeof seen>((resolve) => {
    const unsubscribe = observer.subscribe((r) => {
      seen.push({ status: r.status, error: r.error });
      if (until(r)) {
        unsubscribe();
        resolve(seen);
      }
    });
  });
}

test('a project chat asked about before the daemon has it waits for it instead of failing', async () => {
  const client = new QueryClient();
  const daemon = projectNotThereYet(2);
  const observer = new QueryObserver<unknown>(client, { queryKey: ['leadSessions', 'p'], queryFn: daemon.queryFn, ...chatReadyRetry, retryDelay: 1 });
  const seen = await settled(observer, (r) => r.status !== 'pending');
  assert.equal(seen.at(-1)!.status, 'success');
  assert.ok(seen.every((r) => !r.error), 'never shown as unavailable on the way');
  assert.equal(daemon.asked(), 3);
  client.clear();
});

test('before: the same order left the chat unavailable', async () => {
  const client = new QueryClient();
  const daemon = projectNotThereYet(1);
  const observer = new QueryObserver<unknown>(client, { queryKey: ['projectChat', 'p', 'main'], queryFn: daemon.queryFn, ...chatLoadRetry, retryDelay: 1 });
  const seen = await settled(observer, (r) => r.status !== 'pending');
  assert.equal(seen.at(-1)!.status, 'error');
  client.clear();
});

test('a project chat that still failed is asked again by itself, not only when reopened', async () => {
  const client = new QueryClient();
  const daemon = projectNotThereYet(7); // more than the retries wait for
  const observer = new QueryObserver<unknown>(client, {
    queryKey: ['leadSessions', 'p'],
    queryFn: daemon.queryFn,
    ...chatReadyRetry,
    retryDelay: 1,
    refetchInterval: (q) => (q.state.status === 'error' ? 5 : false),
  });
  const seen = await settled(observer, (r) => r.status === 'success');
  assert.ok(seen.some((r) => r.status === 'error'), 'it did fail first');
  assert.equal(seen.at(-1)!.status, 'success');
  client.clear();
});


// The ?past= scenarios' past agents (components/PastAgents.tsx): a list the
// dev bridge filters the way the daemon does, deletes from, and whose chats
// are seeded, so the list, its filters and a read-only chat can be seen and
// clicked through without a daemon.
import type { QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { asleepChat, PROJECT } from './fixtures';

const hour = 3_600_000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

const pr = (number: number, state: string, title: string): T.PullRequest => ({ number, title, state, checks: '', url: `https://github.com/leciric/agentbox/pull/${number}` });

function fixtures(): T.ArchivedAgent[] {
  const past = (name: string, project: string, title: string, branch: string, archived: number, tokens: number, p?: T.PullRequest): T.ArchivedAgent => ({
    ref: `${project}/${name}`,
    project,
    name,
    title,
    task: title,
    ai: 'claude',
    interface: 'chat',
    branch,
    baseRef: 'main',
    baseCommit: 'b7126d9a',
    createdAt: ago(archived + 6 * hour),
    archivedAt: ago(archived),
    pr: p,
    tokens,
    costUsd: tokens / 1e6,
  });
  return [
    past('agent-31', PROJECT, 'Add a dark mode toggle to Settings', 'agentbox/dark-mode-toggle', 2 * hour, 1_840_000, pr(214, 'merged', 'feat: a dark mode toggle in Settings')),
    past('agent-29', PROJECT, 'Retry the image download when a part fails its checksum halfway through', 'agentbox/retry-image-download-when-a-part-fails-its-checksum', 20 * hour, 3_120_000, pr(209, 'open', 'fix: retry a part of the base image that fails its checksum')),
    past('agent-24', 'pawly', 'Paginate the reminders page', 'agentbox/reminders-pagination', 3 * 24 * hour, 920_000, pr(57, 'closed', 'feat: paginate reminders')),
    past('agent-22', PROJECT, '', 'agentbox/agent-22', 9 * 24 * hour, 0),
    past('agent-17', 'pawly', 'Try a SQLite-backed queue', 'agentbox/sqlite-queue', 40 * 24 * hour, 410_000),
  ];
}

let past: T.ArchivedAgent[] = [];

// seedPastAgents makes the list again and seeds each past agent's chat.
export function seedPastAgents(queryClient: QueryClient): T.ArchivedAgent[] {
  past = fixtures();
  for (const a of past) queryClient.setQueryData(['chat', a.ref], asleepChat(a.ref));
  return past;
}

const json = (status: number, body: unknown) => ({ status, body: body === undefined ? '' : JSON.stringify(body), contentType: 'application/json' });

// pastRequest answers the dev bridge for /v1/archived-agents, or undefined
// for anything else.
export function pastRequest(method: string, path: string, body?: unknown): { status: number; body: string; contentType: string } | undefined {
  if (!path.startsWith('/v1/archived-agents')) return undefined;
  const url = new URL(path, 'http://dev');
  const parts = url.pathname.split('/').slice(3).map(decodeURIComponent);
  if (method === 'GET' && parts.length === 0) {
    const q = (url.searchParams.get('q') ?? '').toLowerCase();
    const state = url.searchParams.get('pr') ?? '';
    const project = url.searchParams.get('project') ?? '';
    const since = url.searchParams.get('since');
    const limit = Number(url.searchParams.get('limit') ?? 0);
    const found = past.filter(
      (a) =>
        (!project || a.project === project) &&
        (!q || [a.name, a.title, a.task, a.branch, a.pr?.title ?? '', a.pr ? `#${a.pr.number}` : ''].some((s) => s.toLowerCase().includes(q))) &&
        (!state || (state === 'none' ? !a.pr : a.pr?.state === state)) &&
        (!since || a.archivedAt >= since),
    );
    return json(200, limit ? found.slice(0, limit) : found);
  }
  if (method === 'POST' && parts[0] === 'delete') {
    const refs = (body as T.DeleteArchivedRequest).refs;
    past = past.filter((a) => !refs.includes(a.ref));
    return json(200, { deleted: refs } satisfies T.DeleteArchivedResult);
  }
  const ref = parts.slice(0, 2).join('/');
  const a = past.find((x) => x.ref === ref);
  if (!a) return json(404, { error: `no past agent ${ref}` });
  if (method === 'GET') return json(200, a);
  if (method === 'DELETE') {
    past = past.filter((x) => x.ref !== ref);
    return { status: 204, body: '', contentType: '' };
  }
  return json(400, { error: 'the preview does not continue past agents' });
}

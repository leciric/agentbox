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

// liveWork is the Work page's live agents: one waiting for review, with a
// report and media, one whose report says it stopped short, and one at work.
function liveWork(): T.WorkItem[] {
  const live = (name: string, title: string, status: string, finished: number, more: Partial<T.WorkItem> = {}, item: Partial<T.ReviewItem> = {}): T.WorkItem => ({
    item: { project: PROJECT, agent: name, ref: `${PROJECT}/${name}`, title, branch: `agentbox/${title.toLowerCase().replace(/[^a-z]+/g, '-').replace(/-$/, '')}`, ai: 'claude', finishedAt: ago(finished), fingerprint: status === 'working' ? '' : `{"f":"${name}"}`, ...item },
    status,
    at: ago(finished),
    ...more,
  });
  const report = (agent: string, status: string, summary: string, remainingIssues?: string[]): T.AgentReport => ({ id: `rep-${agent}`, project: PROJECT, agent, createdAt: ago(hour / 2), status, summary, remainingIssues });
  return [
    live('agent-40', 'Work page: everything agents did, in place of Today', 'working', 10 * 60_000),
    live(
      'agent-38',
      'Show the VM memory breakdown in a popover from the memory bubble',
      'review',
      hour / 2,
      { report: report('agent-38', 'done', 'Added the popover with the breakdown by agent, the page cache and swap. Tests pass; screenshots attached.'), screenshots: 2, recordings: 1 },
      { changes: { files: 6, insertions: 214, deletions: 18, dirty: false }, pr: { ...pr(371, 'open', 'feat: the VM memory breakdown in a popover'), checks: 'passing' }, summary: 'Added the popover.' },
    ),
    live(
      'agent-36',
      'Pause only the heavy command when swap is full',
      'reviewed',
      5 * hour,
      { report: report('agent-36', 'partial', 'Pausing the heavy command works; `go test -race` still stalls under full swap.', ['go test -race stalls with swap full', 'No test for an idle machine']), screenshots: 1 },
      { changes: { files: 3, insertions: 88, deletions: 40, dirty: false }, pr: pr(318, 'open', 'fix: pause only the heavy command'), verdict: 'reviewed', reviewedAt: ago(4 * hour) },
    ),
  ];
}

// workRequest answers the dev bridge for /v1/work and a project's, the live
// agents above and the past ones, filtered and searched as the daemon does.
function workRequest(path: string): { status: number; body: string; contentType: string } | undefined {
  const url = new URL(path, 'http://dev');
  const m = url.pathname.match(/^\/v1\/(?:projects\/([^/]+)\/)?work$/);
  if (!m) return undefined;
  const project = m[1] ? decodeURIComponent(m[1]) : (url.searchParams.get('project') ?? '');
  const q = (url.searchParams.get('q') ?? '').toLowerCase();
  const filter = url.searchParams.get('filter') ?? '';
  const pastItems = past.map(
    (a): T.WorkItem => ({
      item: { project: a.project, agent: a.name, ref: a.ref, title: a.title, branch: a.branch, ai: a.ai, pr: a.pr, finishedAt: a.archivedAt, fingerprint: '', changes: { files: 4, insertions: 120, deletions: 9, dirty: false } },
      task: a.task,
      past: true,
      status: 'done',
      at: a.archivedAt,
      screenshots: a.pr ? 3 : 0,
    }),
  );
  const all = [...liveWork(), ...pastItems]
    .filter((w) => !project || w.item.project === project)
    .filter((w) => !q || [w.item.title ?? '', w.item.agent, w.item.branch ?? '', w.task ?? '', w.item.pr?.title ?? '', w.item.pr ? `#${w.item.pr.number}` : ''].some((s) => s.toLowerCase().includes(q)))
    .sort((a, b) => b.at.localeCompare(a.at));
  const inFilter = (w: T.WorkItem, f: string) => (f === 'review' ? w.status === 'review' : f === 'none' ? !w.item.pr : w.item.pr?.state === f);
  const counts: Record<string, number> = { '': all.length };
  for (const f of ['review', 'open', 'merged', 'closed', 'none']) counts[f] = all.filter((w) => inFilter(w, f)).length;
  const items = filter ? all.filter((w) => inFilter(w, filter)) : all;
  return json(200, { items, total: items.length, counts } satisfies T.WorkPage);
}

// pastRequest answers the dev bridge for /v1/archived-agents and the Work
// page, or undefined for anything else.
export function pastRequest(method: string, path: string, body?: unknown): { status: number; body: string; contentType: string } | undefined {
  if (method === 'GET') {
    const work = workRequest(path);
    if (work) return work;
  }
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
  if (method === 'GET' && parts[2] === 'diff') {
    return { status: 200, body: 'diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1,2 +1,3 @@\n # Project\n+A line the past agent added.\n Text\n', contentType: 'text/plain' };
  }
  if (method === 'GET') return json(200, a);
  if (method === 'DELETE') {
    past = past.filter((x) => x.ref !== ref);
    return { status: 204, body: '', contentType: '' };
  }
  return json(400, { error: 'the preview does not continue past agents' });
}

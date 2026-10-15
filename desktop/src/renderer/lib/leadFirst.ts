// UI v2's sidebar (components/v2/SidebarV2.tsx): Home and a row per project's
// lead, each project's agents folded under it. What it shows of an agent is
// one of five states, and filters like T3Code's narrow the list by state,
// project and a search. Here is the reading; the drawing is the component's.
import type * as T from '../../shared/api';
import { chatLabel } from './agentStatus.ts';

// AgentSt is where an agent stands in the sidebar: it asks you something
// (a question, a credential, a permission), stopped on an error, is ready
// for you to review, is working, or is done (settled, reviewed, asleep).
export type AgentSt = 'needs' | 'error' | 'review' | 'working' | 'done';

// The order states sort and count in: what needs you first.
export const stOrder: readonly AgentSt[] = ['needs', 'error', 'review', 'working', 'done'];

// The states that need the user, and the only ones the sidebar colours.
export const attention = (st: AgentSt) => st === 'needs' || st === 'error';

export type StContext = {
  // Agents ready to review (T.ReviewItem refs).
  review: ReadonlySet<string>;
  // Agents with a question or a credential request waiting on the user.
  asking: ReadonlySet<string>;
};

// changesPending is whether the agent is still on changes the user asked
// for from the review (T.Agent.changesRequested): set until its turn ends.
export function changesPending(a: Pick<T.Agent, 'changesRequested' | 'chat'>): boolean {
  return !!a.changesRequested && (a.chat === 'running' || a.chat === 'starting' || a.chat === 'waiting');
}

export function agentSt(a: T.Agent, ctx: StContext): AgentSt {
  const { tone } = chatLabel(a);
  if (tone === 'urgent' || ctx.asking.has(a.ref)) return 'needs';
  if (changesPending(a)) return 'working';
  // Paused on a usage limit: it resumes by itself, so nobody has to look.
  if (a.outcome === 'limited' && a.chat !== 'running') return 'working';
  // One that failed and finished is in review, with its outcome.
  if (ctx.review.has(a.ref)) return 'review';
  if (tone === 'error') return 'error';
  if (tone === 'live' || a.chat === 'starting') return 'working';
  return 'done';
}

export type Counts = Record<AgentSt, number>;

export function countSt(sts: readonly AgentSt[]): Counts {
  const out: Counts = { needs: 0, error: 0, review: 0, working: 0, done: 0 };
  for (const st of sts) out[st]++;
  return out;
}

export type Filters = {
  st: AgentSt[];
  projects: string[];
  q: string;
};

export const noFilters: Filters = { st: [], projects: [], q: '' };

// defaultFilters is what v2's sidebar opens with until the user changes it:
// the agents at work and the ones to review (v2Projects.ts keeps their
// choice after that).
export const defaultFilters: Filters = { st: ['working', 'review'], projects: [], q: '' };

// filterCount is how many filters are on, for the filter button's badge: the
// search box shows itself.
export const filterCount = (f: Filters) => f.st.length + f.projects.length;

// narrowing is whether the filters narrow agents (a state or a search), which
// hides the projects with no match; a project filter alone only hides the
// other projects.
export const narrowing = (f: Filters) => f.st.length > 0 || f.q.trim() !== '';

export type Row = { agent: T.Agent; st: AgentSt };

// matches is whether an agent passes the filters. q matches its title, its
// name, its branch and its project's name, ignoring case.
export function matches(row: Row, f: Filters, projectName: string): boolean {
  if (f.st.length && !f.st.includes(row.st)) return false;
  if (f.projects.length && !f.projects.includes(row.agent.project)) return false;
  const q = f.q.trim().toLowerCase();
  if (!q) return true;
  return [row.agent.title, row.agent.name, row.agent.branch, projectName].some((x) => x?.toLowerCase().includes(q));
}

// sortRows puts what needs you first, then by state, then newest first.
export function sortRows(rows: readonly Row[]): Row[] {
  return [...rows].sort(
    (a, b) => stOrder.indexOf(a.st) - stOrder.indexOf(b.st) || b.agent.createdAt.localeCompare(a.agent.createdAt) || a.agent.name.localeCompare(b.agent.name),
  );
}

export type ProjectRows = {
  project: T.Project;
  // Its live agents, for its counts: the done ones are in Past agents, not
  // the sidebar.
  all: Row[];
  // The ones the filters let through, sorted.
  shown: Row[];
  // Whether its agents' list is open: by the user, or by a search finding
  // agents in it.
  open: boolean;
};

// live is whether an agent belongs in the sidebar: at work, asking, stopped
// on an error or ready to review. A finished one is in Past agents (#296).
export const live = (st: AgentSt) => st !== 'done';

// liveOrder is stOrder without done, the states the sidebar shows and filters by.
export const liveOrder: readonly AgentSt[] = stOrder.filter(live);

// Where the sidebar stands on each project besides its agents.
export type ProjectCtx = {
  // Projects whose lead is busy (mid-turn): they count as working.
  busy: ReadonlySet<string>;
  // Projects that stay in the list whatever the state filter says: the pinned.
  keep: ReadonlySet<string>;
};

// leadBusy is whether a lead's chat is mid-turn: thinking, running tools or
// answering.
export const leadBusy = (chat: string | undefined) => chat === 'running';

// projectRows is the sidebar's list of projects, in the order given, each
// with its live agents, under the filters. A state filter keeps the projects
// with an agent in one of its states, or, for Working, a busy lead, and
// a search leaves out a project it narrows to nothing, unless it matches the
// project's own name, and opens those it found agents in. expanded holds the
// projects the user opened, which a filter never changes.
export function projectRows(projects: readonly T.Project[], rows: readonly Row[], f: Filters, expanded: ReadonlySet<string>, label: (p: T.Project) => string, ctx: ProjectCtx = { busy: new Set(), keep: new Set() }): ProjectRows[] {
  const q = f.q.trim().toLowerCase();
  const out: ProjectRows[] = [];
  for (const project of projects) {
    if (f.projects.length && !f.projects.includes(project.name)) continue;
    const name = label(project);
    const all = rows.filter((r) => r.agent.project === project.name && live(r.st));
    const shown = sortRows(all.filter((r) => matches(r, f, name)));
    if (q && shown.length === 0 && !name.toLowerCase().includes(q)) continue;
    const stMatch = shown.length > 0 || (f.st.includes('working') && ctx.busy.has(project.name));
    if (!q && f.st.length && !stMatch && !ctx.keep.has(project.name)) continue;
    // The chevron alone opens a project, under any state filter, so it
    // folds the same with Working on as with nothing; only a search opens
    // the projects it found agents in.
    out.push({ project, all, shown, open: (q !== '' && shown.length > 0) || expanded.has(project.name) });
  }
  return out;
}

// toggle adds x to list, or takes it out.
export const toggle = <X>(list: readonly X[], x: X): X[] => (list.includes(x) ? list.filter((y) => y !== x) : [...list, x]);

// leadLine is a lead's last message for its row: one line, without markdown's
// marks, and who wrote it.
export function leadLine(last: T.ChatLine | undefined): { text: string; mine: boolean } | undefined {
  if (!last?.text) return undefined;
  const text = last.text
    .replace(/```[\s\S]*?(```|$)/g, ' ')
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/^\s{0,3}(#{1,6}|>|[-*+]\s)\s*/gm, '')
    .replace(/[*`]+/g, '')
    .replace(/\s+/g, ' ')
    .trim();
  return text ? { text, mine: last.kind === 'user' } : undefined;
}

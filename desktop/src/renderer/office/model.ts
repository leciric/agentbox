// What the Office shows, worked out from what the app already reads: every
// agent with its state as the sidebar sees it (useAgentCounts' rows), its pull
// request from its project's fleet, the questions waiting on the user, each
// lead's chat and the agents ready to review. Nothing here knows about three.js:
// the scene (scene.ts) draws an OfficeModel, and the React view (OfficeView)
// builds one, so this is the part with tests.
import type * as T from '../../shared/api';
import type { AgentSt, Row } from '../lib/leadFirst.ts';

// DeskTone is an agent's state at a glance, the colour of its beacon: it needs
// you (a question, a permission, a credential), stopped on an error, finished
// (ready for you to review), working, idle with its machine up, or stopped.
export type DeskTone = 'needs' | 'error' | 'finished' | 'working' | 'idle' | 'stopped';

// The order a list of desks reads in: what needs you first.
export const toneOrder: readonly DeskTone[] = ['needs', 'error', 'finished', 'working', 'idle', 'stopped'];

export type PrLook = {
  number: number;
  state: 'open' | 'draft' | 'merged' | 'closed';
  checks: 'passing' | 'failing' | 'pending' | 'none';
  url: string;
  title: string;
};

export type DeskModel = {
  // The agent's ref, project/name: a desk is always the same agent's.
  id: string;
  project: string;
  name: string;
  title: string;
  branch: string;
  ai: string;
  tone: DeskTone;
  pr?: PrLook;
  changes?: { files: number; insertions: number; deletions: number };
  // What it asks you, in its words, when it does.
  question?: string;
  // Since when it's been in this state, roughly: its last activity.
  lastActive?: string;
};

// A project's lead is its front desk: it has no machine, only its chat.
export type LeadTone = 'working' | 'needs' | 'idle';

export type AreaModel = {
  project: string;
  label: string;
  // The project's colour, a hue in degrees, the same every time.
  hue: number;
  lead: { tone: LeadTone; line?: string };
  desks: DeskModel[];
};

export type BoardItem = {
  // The desk it's about (an agent's ref), or a project's lead (project/lead).
  ref: string;
  project: string;
  title: string;
  text?: string;
  tone: DeskTone;
  pr?: PrLook;
};

export type OfficeModel = {
  areas: AreaModel[];
  board: { needs: BoardItem[]; review: BoardItem[] };
};

export type OfficeInputs = {
  projects: readonly Pick<T.Project, 'name' | 'displayName'>[];
  rows: readonly Row[];
  // Each project's fleet, for the agents' pull requests and changes.
  fleets: ReadonlyMap<string, T.Fleet | undefined>;
  // Each project's questions (pending ones are what an agent asks you).
  questions: ReadonlyMap<string, readonly T.Question[] | undefined>;
  // Each project's lead chat: its state ('running', 'waiting', 'ready'…) and last line.
  leads: ReadonlyMap<string, Pick<T.ProjectChat, 'chat' | 'last'> | undefined>;
  // The agents ready to review, with their pull requests.
  review: readonly T.ReviewItem[];
};

// leadRef is the ref a lead's board item and front desk go by.
export const leadRef = (project: string) => `${project}/lead`;

// hueOf is a project's colour: its name hashed onto the colour wheel, skipping
// the muddy yellow-greens so every area reads as a colour of its own.
export function hueOf(name: string): number {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  const hues = [8, 28, 200, 220, 250, 275, 300, 330, 170, 150];
  return hues[h % hues.length];
}

// deskTone is a sidebar state (AgentSt) as a desk shows it: done splits into
// idle, its machine still up, and stopped.
export function deskTone(agent: Pick<T.Agent, 'state'>, st: AgentSt): DeskTone {
  switch (st) {
    case 'needs':
      return 'needs';
    case 'error':
      return 'error';
    case 'review':
      return 'finished';
    case 'working':
      return 'working';
    default:
      return agent.state === 'running' ? 'idle' : 'stopped';
  }
}

export function prLook(pr: T.PullRequest | undefined): PrLook | undefined {
  if (!pr || !pr.number) return undefined;
  const raw = pr.state.toLowerCase();
  const state: PrLook['state'] = pr.draft && raw === 'open' ? 'draft' : raw === 'merged' ? 'merged' : raw === 'closed' ? 'closed' : 'open';
  const checks: PrLook['checks'] = pr.checks === 'passing' || pr.checks === 'failing' || pr.checks === 'pending' ? pr.checks : 'none';
  return { number: pr.number, state, checks, url: pr.url, title: pr.title };
}

// leadTone is a lead's chat state as its front desk shows it: waiting on a
// permission is the user's to give.
export function leadTone(chat: string | undefined): LeadTone {
  if (chat === 'waiting') return 'needs';
  if (chat === 'running' || chat === 'starting') return 'working';
  return 'idle';
}

const byAge = (a: Row, b: Row) => (a.agent.createdAt < b.agent.createdAt ? -1 : a.agent.createdAt > b.agent.createdAt ? 1 : a.agent.ref < b.agent.ref ? -1 : 1);
const toneRank = (t: DeskTone) => toneOrder.indexOf(t);

// officeModel puts it together. Areas go in the projects' order, desks in
// the order their agents were made, so a desk stays where it was as others
// come and go; the board lists what needs you, then what waits for review.
export function officeModel(inp: OfficeInputs): OfficeModel {
  const reviewByRef = new Map(inp.review.map((r) => [r.ref, r]));
  const asks = new Map<string, string>();
  for (const list of inp.questions.values())
    // The ones waiting on you, as the sidebar counts them (agentStatus.ts's
    // isAsking): a credential, or a question the lead passed on.
    for (const q of list ?? []) if ((q.status === 'escalated' || (q.status === 'pending' && !!q.kind)) && !asks.has(q.ref)) asks.set(q.ref, q.question);

  const rowsBy = new Map<string, Row[]>();
  for (const r of inp.rows) {
    const list = rowsBy.get(r.agent.project) ?? [];
    list.push(r);
    rowsBy.set(r.agent.project, list);
  }

  const areas: AreaModel[] = [];
  const needs: BoardItem[] = [];
  const review: BoardItem[] = [];
  const named = new Set(inp.projects.map((p) => p.name));
  // A project the projects list doesn't have yet still gets its agents' area.
  const projects = [...inp.projects, ...[...rowsBy.keys()].filter((n) => !named.has(n)).map((name) => ({ name, displayName: '' }))];
  for (const p of projects) {
    const label = p.displayName || p.name;
    const fleet = inp.fleets.get(p.name);
    const fleetBy = new Map((fleet?.agents ?? []).map((a) => [a.ref, a]));
    const desks = [...(rowsBy.get(p.name) ?? [])].sort(byAge).map((r): DeskModel => {
      const a = r.agent;
      const f = fleetBy.get(a.ref);
      const tone = deskTone(a, r.st);
      const question = tone === 'needs' ? (asks.get(a.ref) ?? (a.outcome === 'asking' ? a.outcomeNote : undefined)) : tone === 'error' ? a.outcomeNote : undefined;
      return {
        id: a.ref,
        project: p.name,
        name: a.name,
        title: a.title || a.name,
        branch: a.branch,
        ai: a.ai,
        tone,
        pr: prLook(f?.pr ?? reviewByRef.get(a.ref)?.pr),
        changes: f?.changes && (f.changes.files > 0 || f.changes.insertions > 0) ? { files: f.changes.files, insertions: f.changes.insertions, deletions: f.changes.deletions } : undefined,
        question,
        lastActive: f?.lastActive,
      };
    });
    const leadChat = inp.leads.get(p.name);
    const lead = { tone: leadTone(leadChat?.chat), line: leadChat?.last?.text };
    // A project with no agents and a quiet lead has no area: an empty rug
    // helps no one, and its chat is a click away in the sidebar.
    if (desks.length === 0 && lead.tone === 'idle') continue;
    areas.push({ project: p.name, label, hue: hueOf(p.name), lead, desks });

    if (lead.tone === 'needs') needs.push({ ref: leadRef(p.name), project: p.name, title: label, tone: 'needs' });
    for (const d of desks) {
      if (d.tone === 'needs' || d.tone === 'error') needs.push({ ref: d.id, project: p.name, title: d.title, text: d.question, tone: d.tone, pr: d.pr });
      else if (d.tone === 'finished' || (d.pr && d.pr.state === 'open' && d.tone !== 'working')) review.push({ ref: d.id, project: p.name, title: d.title, tone: d.tone, pr: d.pr });
    }
  }
  needs.sort((a, b) => toneRank(a.tone) - toneRank(b.tone));
  // Two agents with the same title are told apart by their names.
  const seen = new Map<string, number>();
  for (const a of areas) for (const d of a.desks) seen.set(d.title, (seen.get(d.title) ?? 0) + 1);
  const withName = (title: string, name: string) => ((seen.get(title) ?? 0) > 1 ? `${title} (${name})` : title);
  for (const a of areas) for (const d of a.desks) d.title = withName(d.title, d.name);
  for (const i of [...needs, ...review]) {
    const d = i.ref.endsWith('/lead') ? undefined : areas.find((a) => a.project === i.project)?.desks.find((x) => x.id === i.ref);
    if (d) i.title = d.title;
  }
  return { areas, board: { needs, review } };
}

// deskOf finds a desk by its agent's ref.
export function deskOf(model: OfficeModel, ref: string): DeskModel | undefined {
  for (const a of model.areas) for (const d of a.desks) if (d.id === ref) return d;
  return undefined;
}

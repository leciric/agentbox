// Every count of agents the app shows — a sidebar row's, the filter's, the
// agent panel's groups and its Pull requests tab, Home's — is read here, from
// one reading of where each agent stands (leadFirst.ts's agentSt), so no two
// of them can disagree. They used to: the sidebar's "N to review" pill read a
// project's review page, its status line the review head, the filter left out
// the busy leads that the rows counted, and the approvals were counted apart
// from the asks they are part of. Each agent is in one state, the approvals
// are the asks that wait on a permission, and a lead mid-turn is not an agent
// at work: its row has its own spinner, and the panel's Working group, which
// lists agents, would be one short of a count that took it in. Leads are
// counted apart, in leads. useAgentCounts.ts reads the inputs from the daemon.
import type * as T from '../../shared/api';
import { agentSt, awaitsApproval, countSt, leadBusy, type Counts, type Row, type StContext } from './leadFirst.ts';

export type Tally = Counts & {
  // Of the needs, how many wait on a permission in the agent's chat.
  approvals: number;
  // Busy leads (mid-turn), not in working.
  leads: number;
  // The agents counted (live and done), leads aside.
  agents: number;
};

export const emptyTally = (): Tally => ({ needs: 0, error: 0, review: 0, working: 0, done: 0, approvals: 0, leads: 0, agents: 0 });

// stRows is each agent with its one state, and its pull request's number
// when prs has one.
export function stRows(agents: readonly T.Agent[], ctx: StContext, prs?: ReadonlyMap<string, number>): Row[] {
  return agents.map((agent) => ({ agent, st: agentSt(agent, ctx), pr: prs?.get(agent.ref) }));
}

// tally counts rows by state, and the busy leads (leads: project → its
// chat's state) apart. With project, only that project's.
export function tally(rows: readonly Row[], leads: ReadonlyMap<string, string | undefined> = new Map(), project?: string): Tally {
  const mine = project === undefined ? rows : rows.filter((r) => r.agent.project === project);
  const out: Tally = { ...emptyTally(), ...countSt(mine.map((r) => r.st)), agents: mine.length };
  out.approvals = mine.filter((r) => r.st === 'needs' && awaitsApproval(r.agent)).length;
  for (const [name, chat] of leads) if ((project === undefined || name === project) && leadBusy(chat)) out.leads++;
  return out;
}

// tallyByProject is tally for each project with an agent or a busy lead.
export function tallyByProject(rows: readonly Row[], leads: ReadonlyMap<string, string | undefined> = new Map()): Map<string, Tally> {
  const names = new Set([...rows.map((r) => r.agent.project), ...[...leads].filter(([, c]) => leadBusy(c)).map(([n]) => n)]);
  return new Map([...names].map((n) => [n, tally(rows, leads, n)]));
}

// counted is whether a tally has anything a row shows: work, review, an ask or a failure.
export const counted = (c: Tally) => c.working + c.review + c.needs + c.error > 0;

// PanelGroup is one of the agent panel's groups, in the order they show:
// what needs you, what failed, what's ready to review, what's at work, and
// the rest (done, asleep), idle.
export type PanelGroup = 'needs' | 'error' | 'review' | 'working' | 'idle';
export const panelGroups: readonly PanelGroup[] = ['needs', 'error', 'review', 'working', 'idle'];

// groupRows puts rows in panelGroups' order, each group keeping the order
// they came in; empty groups are left out.
export function groupRows<R extends Pick<Row, 'st'>>(rows: readonly R[]): { group: PanelGroup; rows: R[] }[] {
  const of = (st: Row['st']): PanelGroup => (st === 'done' ? 'idle' : st);
  return panelGroups.map((group) => ({ group, rows: rows.filter((r) => of(r.st) === group) })).filter((g) => g.rows.length > 0);
}

// initials is what tells agents apart where only a small tile shows, as in
// the folded panel: the first letters of its title's first two words, else
// its number ("agent-12" → "12"), else its name's first two letters.
export function initials(a: Pick<T.Agent, 'title' | 'name'>): string {
  const [first, second] = (a.title ?? '').match(/[\p{L}\p{N}]+/gu) ?? [];
  if (first && second) return (first.charAt(0) + second.charAt(0)).toUpperCase();
  if (first) return first.slice(0, 2).toUpperCase();
  const n = /(\d+)$/.exec(a.name)?.[1];
  if (n) return n.slice(-3);
  return a.name.slice(0, 2).toUpperCase();
}

// tileLabels is each agent's tile label in a strip of them: its initials,
// unless another agent's are the same ("Needs a secret", "Needs a GitHub
// account"), then its number, which is its own.
export function tileLabels(agents: readonly Pick<T.Agent, 'ref' | 'title' | 'name'>[]): Map<string, string> {
  const first = new Map(agents.map((a) => [a.ref, initials(a)]));
  const seen = new Map<string, number>();
  for (const label of first.values()) seen.set(label, (seen.get(label) ?? 0) + 1);
  return new Map(agents.map((a) => {
    const label = first.get(a.ref)!;
    const n = /(\d+)$/.exec(a.name)?.[1];
    return [a.ref, (seen.get(label) ?? 0) > 1 && n ? n.slice(-3) : label];
  }));
}

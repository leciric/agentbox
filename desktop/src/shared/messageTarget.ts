// Who a message written in a project's chat goes to: its lead, by default, or
// one of the project's agents (api.ChatMessageRequest.to), which the daemon
// sends it to while the lead is told in a line it reads at its next turn. The
// desktop's composer (renderer/lib/messageTarget.ts) and the phone's
// (mobile/src/features/threads) both choose with this: the targets in order,
// cycling through them, "@agent-31 " at the start of a message, and an agent
// that was retired falling back to the lead.
import { LeadName, type Agent } from './api.ts';

// Lead is the target that is no agent: the session's own lead.
export const Lead = undefined;

export interface Targets {
  // own are the agents the session made, others the rest of the
  // project's: another session's, the user's own, or nobody's.
  own: Agent[];
  others: Agent[];
}

const isLeadName = (name: string) => name === LeadName || name.startsWith(`${LeadName}.`);

// targetsOf is what a session of project's chat can write to besides its lead:
// the agents it made first, then the project's others, each by name.
export function targetsOf(agents: readonly Agent[], project: string, session: string | undefined): Targets {
  const mine = agents.filter((a) => a.project === project && !isLeadName(a.name)).sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
  const own = session ? mine.filter((a) => a.leadSession === session) : [];
  return { own, others: mine.filter((a) => !own.includes(a)) };
}

// targetOrder is the targets as the chip lists them and the keys cycle them:
// the lead, then the session's own agents, then the others.
export function targetOrder(targets: Targets): (string | undefined)[] {
  return [Lead, ...targets.own.map((a) => a.name), ...targets.others.map((a) => a.name)];
}

// cycleTarget is the target after (or, with step -1, before) current,
// wrapping round. One no longer there starts again from the lead.
export function cycleTarget(order: readonly (string | undefined)[], current: string | undefined, step: 1 | -1): string | undefined {
  if (order.length === 0) return Lead;
  const i = Math.max(order.indexOf(current), 0);
  return order[(i + step + order.length) % order.length];
}

// liveTarget is the target chosen, while that agent is still the project's;
// the lead once it was retired.
export function liveTarget(chosen: string | undefined, targets: Targets): string | undefined {
  if (!chosen) return Lead;
  return [...targets.own, ...targets.others].some((a) => a.name === chosen) ? chosen : Lead;
}

// atTarget reads "@agent-31 " at the start of a message as that message's
// target, when it names one of the project's agents: the agent, and the
// message without it. An @ anywhere else is a mention for the lead to read.
export function atTarget(text: string, names: readonly string[]): { to: string; text: string } | undefined {
  const match = /^\s*@(\S+)\s+([\s\S]*)$/.exec(text);
  if (!match || !names.includes(match[1])) return undefined;
  return { to: match[1], text: match[2].trim() };
}

// otherSession is the other session of the chat that an agent
// belongs to, which a message to it asks about first; undefined for one of
// its own, nobody's, or one whose session is gone.
export function otherSession<S extends { id: string }>(agent: Agent | undefined, session: string | undefined, sessions: readonly S[]): S | undefined {
  if (!agent?.leadSession || agent.leadSession === session) return undefined;
  return sessions.find((s) => s.id === agent.leadSession);
}

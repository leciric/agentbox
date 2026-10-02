import type * as T from '../../shared/api';
import { projectLabel } from './projectName.ts';

// MeterPick is what the top bar's usage meter shows: the AI tool, and the
// account whose limits it spends, with the reading AgentBox has for it.
// reading is missing when nothing is known about that account's usage, and
// the meter is then hidden rather than showing some other account's.
export interface MeterPick {
  tool: string;
  reading?: T.ClaudeLimit;
  // whose is how the tooltip says why this account: "agentbox's account",
  // "the machine's default account".
  whose: string;
}

// pickMeter chooses the usage the top bar shows for what is open (D85).
//
// - An agent spends its own account (the one whose token it holds, or the
//   machine's default when it has none), on its own AI tool. An agent with no
//   AI tool spends nothing, and gets its project's.
// - A project spends its own Claude account, else the machine's default: its
//   chat runs Claude Code, and so do its new agents unless told otherwise.
// - With no project (Home, Jobs, Settings), the machine's default account.
//
// Only Claude accounts have readings: claude-agent-acp relays Anthropic's
// limits on usage_update, while codex-acp keeps Codex's to itself (only its
// /status text shows them) and OpenCode reports none. So a Codex or OpenCode
// agent gets no reading, and the meter hides, until the daemon records one.
export function pickMeter({ limits, project, agent }: { limits: T.ClaudeLimit[]; project?: T.Project; agent?: T.Agent }): MeterPick {
  const byAccount = (name: string, whose: string): MeterPick => {
    const reading = name ? limits.find((l) => l.account === name) : limits.find((l) => l.default);
    return { tool: 'claude', reading, whose: name ? whose : "the machine's default account" };
  };
  if (agent && agent.ai !== 'none' && agent.ai !== '') {
    const whose = `${agent.title || agent.name}'s account`;
    if (agent.ai !== 'claude') return { tool: agent.ai, whose };
    return byAccount(agent.claudeAccount, whose);
  }
  if (project) return byAccount(project.claudeAccount, `${projectLabel(project)}'s account`);
  return byAccount('', '');
}

// projectLimits is the readings a project's Tokens tab shows: the account it
// spends — its own, else the machine's default — first, then any other account
// one of its Claude Code agents holds. Every other account is left out, the
// default too when nothing of the project's uses it: its readings say nothing
// about what this project is spending against.
export function projectLimits({ limits, project, agents }: { limits: T.ClaudeLimit[]; project: T.Project; agents: T.Agent[] }): T.ClaudeLimit[] {
  const resolve = (name: string) => (name ? limits.find((l) => l.account === name) : limits.find((l) => l.default));
  const out: T.ClaudeLimit[] = [];
  const add = (reading?: T.ClaudeLimit) => {
    if (reading && !out.includes(reading)) out.push(reading);
  };
  add(resolve(project.claudeAccount));
  for (const a of agents) {
    if (a.project === project.name && a.ai === 'claude') add(resolve(a.claudeAccount));
  }
  return out;
}

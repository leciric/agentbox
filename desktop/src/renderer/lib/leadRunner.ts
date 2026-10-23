import type * as T from '../../shared/api';
import { t } from '../../shared/i18n/index.ts';

// A lead's AI tool and account are chosen in its composer (RunnerPicker), and
// the session says which it runs: the chat's lead "agent" is built by the app
// (leadAgentFrom, homeAgentFrom) before the session is read, so leadOn puts
// the session's tool and account on it. An agent's own are on its record.
export function leadOn(agent: T.Agent, session?: T.ChatSession): T.Agent {
  if (agent.name !== 'lead' || !session?.tool) return agent;
  const on = (tool: string, own?: string) => (session.tool === tool ? (session.account ?? own ?? '') : '');
  return {
    ...agent,
    ai: session.tool,
    claudeAccount: on('claude', agent.claudeAccount),
    codexAccount: on('codex', agent.codexAccount),
    cursorAccount: on('cursor', agent.cursorAccount),
  };
}

// RunnerChoice is one row of the picker: a tool, or Claude Code, Codex or
// Cursor on one of the accounts the chat may use.
export interface RunnerChoice {
  tool: string;
  account: string; // '' for OpenCode, whose accounts are the project's, one per provider
  name: string;
  ready: boolean;
  // note is what the row says under its name: why it can't be chosen, or that
  // the first message installs it.
  note?: string;
  current: boolean; // what the chat runs, or will from its next message
  reading?: T.ClaudeLimit;
}

// runnerChoices lists the picker's rows: each tool in the daemon's order, and
// Claude Code, Codex and Cursor once per account when one has more than one,
// so the account is chosen in the same click as the tool. OpenCode is one row,
// saying which account each provider is on. A tool that isn't signed in stays
// on the list, disabled, saying how to sign in: hiding it would leave nobody
// knowing it could be chosen at all.
export function runnerChoices(runners: T.LeadRunners): RunnerChoice[] {
  const target = runners.nextTool || runners.tool;
  const out: RunnerChoice[] = [];
  for (const r of runners.runners ?? []) {
    const opencode = r.tool === 'opencode' && r.ready ? runners.toolAccounts?.opencode : undefined;
    const note = !r.ready ? t('chat.runner.notSignedIn', { tool: r.name }) : !r.installed ? t('chat.runner.installs') : opencode || undefined;
    const accounts = r.accounts ?? [];
    // The account the chat runs the tool on, or would.
    const on = r.tool === 'claude' ? runners.account : runners.toolAccounts?.[r.tool];
    if (accounts.length > 1) {
      for (const a of accounts) {
        out.push({ tool: r.tool, account: a.name, name: r.name, ready: r.ready, note, current: target === r.tool && on === a.name, reading: a.limit });
      }
      continue;
    }
    out.push({ tool: r.tool, account: accounts[0]?.name ?? '', name: r.name, ready: r.ready, note, current: target === r.tool, reading: accounts[0]?.limit });
  }
  return out;
}

// runnerReason is the full reason a tool can't be chosen, for the row's tooltip.
export function runnerReason(runners: T.LeadRunners, tool: string): string | undefined {
  return runners.runners.find((r) => r.tool === tool && !r.ready)?.reason;
}

// fiveHour is the share of an account's 5-hour limit used, 0 to 1, or null
// when it has no reading, or the window has started again since.
export function fiveHour(reading: T.ClaudeLimit | undefined, now = Date.now()): number | null {
  const w = reading?.windows.find((x) => x.name === 'five_hour') ?? reading?.windows[0];
  if (!w || Date.parse(w.resetsAt) <= now) return null;
  return Math.max(0, Math.min(1, w.utilization));
}

// runnerLabel names the picker's icon-only trigger, as its tooltip and label:
// the tool, and the account when the chat runs on one.
export function runnerLabel(tool: string, account?: string): string {
  const name = tool === 'claude' ? 'Claude Code' : tool === 'codex' ? 'Codex' : tool === 'opencode' ? 'OpenCode' : tool === 'cursor' ? 'Cursor' : tool;
  return account ? t('chat.runner.labelAccount', { tool: name, account }) : t('chat.runner.label', { tool: name });
}

// accountTools are the tools whose account an agent's composer can change:
// OpenCode's are one per provider, which the lead's switch_agent_account sets.
export const accountTools = ['claude', 'codex', 'cursor'];

// hasRoom says an account's reading leaves room in both its 5-hour and its
// weekly window; an account with no reading may have room, and is offered.
export function hasRoom(reading: T.ClaudeLimit | undefined, now = Date.now()): boolean {
  return !(reading?.windows ?? []).some((w) => (w.name === 'five_hour' || w.name === 'seven_day') && Date.parse(w.resetsAt) > now && w.utilization >= 0.98);
}

// roomiest is the account of the chat's own tool to move to when its account
// stopped on a usage limit: another one with room, the least used first, read
// ones before unread. undefined when there is none.
export function roomiest(choices: RunnerChoice[], tool: string, now = Date.now()): RunnerChoice | undefined {
  const score = (c: RunnerChoice) => fiveHour(c.reading, now) ?? (c.reading ? 0 : 0.5);
  return choices
    .filter((c) => c.tool === tool && c.account && !c.current && c.ready && hasRoom(c.reading, now))
    .sort((a, b) => score(a) - score(b))[0];
}

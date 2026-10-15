// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { fiveHour, leadOn, runnerChoices, runnerLabel, runnerReason } from './leadRunner.ts';

const reading = (account: string, utilization: number, resetsAt = '2099-01-01T00:00:00Z'): T.ClaudeLimit => ({
  account,
  default: false,
  at: '2026-10-14T10:00:00Z',
  status: 'allowed',
  windows: [{ name: 'five_hour', label: '5-hour', utilization, resetsAt }],
});
const account = (name: string, limit?: T.ClaudeLimit): T.LeadAccount => ({ name, default: false, project: false, agents: 0, limit });
const runners = (over: Partial<T.LeadRunners> = {}, accounts = [account('personal', reading('personal', 0.3)), account('work')]): T.LeadRunners => ({
  tool: 'claude',
  account: 'personal',
  runners: [
    { tool: 'claude', name: 'Claude Code', ready: true, installed: true, accounts },
    { tool: 'codex', name: 'Codex', ready: true, installed: false },
    { tool: 'opencode', name: 'OpenCode', ready: false, installed: false, reason: 'agents have no OpenCode login: run agentbox auth opencode' },
    { tool: 'cursor', name: 'Cursor', ready: true, installed: true },
  ],
  ...over,
});

test('Claude Code is offered once per account, and the account it runs on is current', () => {
  const rows = runnerChoices(runners());
  assert.deepEqual(
    rows.map((r) => [r.tool, r.account, r.current]),
    [
      ['claude', 'personal', true],
      ['claude', 'work', false],
      ['codex', '', false],
      ['opencode', '', false],
      ['cursor', '', false],
    ],
  );
  assert.equal(rows[0].reading?.account, 'personal');
});

test('with one account, Claude Code is one row', () => {
  const rows = runnerChoices(runners({}, [account('personal')]));
  assert.deepEqual(rows.filter((r) => r.tool === 'claude').map((r) => r.account), ['personal']);
});

test('a tool chosen for the next message is the current row', () => {
  const rows = runnerChoices(runners({ nextTool: 'codex' }));
  assert.deepEqual(rows.filter((r) => r.current).map((r) => r.tool), ['codex']);
});

test("a tool that isn't signed in is listed, disabled, saying why; one not installed says it installs", () => {
  const rows = runnerChoices(runners());
  const opencode = rows.find((r) => r.tool === 'opencode')!;
  assert.equal(opencode.ready, false);
  assert.match(opencode.note ?? '', /Settings → Accounts/);
  assert.match(runnerReason(runners(), 'opencode') ?? '', /agentbox auth opencode/);
  assert.match(rows.find((r) => r.tool === 'codex')!.note ?? '', /first message/);
  assert.equal(rows.find((r) => r.tool === 'cursor')!.note, undefined);
});

test("a lead runs its session's tool and account, an agent its own", () => {
  const lead = { name: 'lead', ai: 'claude', claudeAccount: 'personal' } as T.Agent;
  assert.deepEqual(
    (({ ai, claudeAccount }) => ({ ai, claudeAccount }))(leadOn(lead, { tool: 'codex' } as T.ChatSession)),
    { ai: 'codex', claudeAccount: '' },
  );
  assert.equal(leadOn(lead, { tool: 'claude', account: 'work' } as T.ChatSession).claudeAccount, 'work');
  const agent = { name: 'agent-01', ai: 'claude', claudeAccount: 'personal' } as T.Agent;
  assert.equal(leadOn(agent, { tool: 'codex' } as T.ChatSession), agent);
});

test('the 5-hour reading is gone once its window has started again', () => {
  assert.equal(fiveHour(reading('a', 0.42), Date.parse('2026-10-14T00:00:00Z')), 0.42);
  assert.equal(fiveHour(reading('a', 0.42, '2026-10-13T00:00:00Z'), Date.parse('2026-10-14T00:00:00Z')), null);
  assert.equal(fiveHour(undefined), null);
});

test('Codex and Cursor are offered once per account too, and the one the chat runs on is current', () => {
  const codex = [account('default'), account('team')];
  const rows = runnerChoices({
    ...runners(),
    tool: 'codex',
    toolAccounts: { codex: 'team', opencode: 'anthropic: work' },
    runners: runners().runners.map((r) => (r.tool === 'codex' ? { ...r, accounts: codex } : r.tool === 'opencode' ? { ...r, ready: true, installed: true } : r)),
  });
  assert.deepEqual(
    rows.filter((r) => r.tool === 'codex').map((r) => [r.account, r.current]),
    [
      ['default', false],
      ['team', true],
    ],
  );
  assert.equal(rows.find((r) => r.tool === 'opencode')!.note, 'anthropic: work');
  assert.equal(rows.filter((r) => r.tool === 'claude').some((r) => r.current), false);
});

test("a lead's session puts the account of the tool it runs on the lead", () => {
  const agent = { name: 'lead', ai: 'claude', claudeAccount: 'work', codexAccount: '', cursorAccount: '' } as T.Agent;
  const on = leadOn(agent, { tool: 'codex', account: 'team' } as T.ChatSession);
  assert.deepEqual([on.ai, on.claudeAccount, on.codexAccount], ['codex', '', 'team']);
});

test("the picker's icon-only trigger names the tool, and the account when there is one", () => {
  assert.equal(runnerLabel('codex'), 'Codex: change the AI tool or account');
  assert.equal(runnerLabel('claude', 'work'), 'Claude Code · work: change the AI tool or account');
});

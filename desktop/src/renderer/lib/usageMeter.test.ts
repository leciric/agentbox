// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { pickMeter } from './usageMeter.ts';

const reading = (account: string, isDefault = false): T.ClaudeLimit => ({
  account,
  default: isDefault,
  at: '2026-09-25T10:00:00Z',
  status: 'allowed',
  windows: [{ name: 'five_hour', label: '5-hour', utilization: 0.4, resetsAt: '2099-01-01T00:00:00Z' }],
});
const limits = [reading('personal', true), reading('work')];
const project = (claudeAccount: string) => ({ name: 'agentbox', claudeAccount }) as T.Project;
const agent = (ai: string, claudeAccount = '') => ({ name: 'agent-07', title: '', ai, claudeAccount }) as T.Agent;

test('Home shows the default account', () => {
  const pick = pickMeter({ limits });
  assert.equal(pick.reading?.account, 'personal');
  assert.equal(pick.whose, "the machine's default account");
});

test("a project shows its own account, else the default", () => {
  assert.equal(pickMeter({ limits, project: project('work') }).reading?.account, 'work');
  assert.equal(pickMeter({ limits, project: project('work') }).whose, "agentbox's account");
  assert.equal(pickMeter({ limits, project: project('') }).reading?.account, 'personal');
});

test('an account with no reading shows nothing, not another account', () => {
  assert.equal(pickMeter({ limits, project: project('gone') }).reading, undefined);
  assert.equal(pickMeter({ limits: [reading('work')] }).reading, undefined);
});

test("a Claude agent shows the account it holds; one with no AI tool, its project's", () => {
  assert.equal(pickMeter({ limits, project: project(''), agent: agent('claude', 'work') }).reading?.account, 'work');
  assert.equal(pickMeter({ limits, project: project('work'), agent: agent('claude') }).reading?.account, 'personal');
  assert.equal(pickMeter({ limits, project: project('work'), agent: agent('none') }).reading?.account, 'work');
});

test('Codex and OpenCode have no reading, so the meter hides', () => {
  for (const ai of ['codex', 'opencode']) {
    const pick = pickMeter({ limits, project: project('work'), agent: agent(ai) });
    assert.equal(pick.tool, ai);
    assert.equal(pick.reading, undefined);
  }
});

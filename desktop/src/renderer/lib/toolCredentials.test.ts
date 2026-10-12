// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { aiToolsReading, credentialTone, summarizeCredentials } from './toolCredentials.ts';

test('a name an AI tool picks its credential by is flagged, with every tool that reads it', () => {
  assert.deepEqual(aiToolsReading('ANTHROPIC_API_KEY'), ['claude', 'opencode']);
  assert.deepEqual(aiToolsReading(' OPENAI_API_KEY '), ['codex', 'opencode']);
  assert.deepEqual(aiToolsReading('ANTHROPIC_BASE_URL'), ['claude', 'opencode']);
  assert.deepEqual(aiToolsReading('STRIPE_SECRET_KEY'), []);
  assert.deepEqual(aiToolsReading(''), []);
});

test('a tool billing a variable is a warning; its account or its own sign-in is fine', () => {
  const cred = (c: Partial<T.ToolCredential>) => ({ agent: 'p/a', tool: 'claude', running: true, ...c }) as T.ToolCredential;
  assert.equal(credentialTone(cred({ variable: 'CLAUDE_CODE_OAUTH_TOKEN', account: 'work' })), 'ok');
  assert.equal(credentialTone(cred({})), 'ok');
  assert.equal(credentialTone(cred({ variable: 'ANTHROPIC_API_KEY', account: 'work' })), 'warning');
  assert.equal(credentialTone(cred({ running: false })), 'idle');
});

test('a project sums up in one line: the agents off their account, then the rest by what they run on', () => {
  const cred = (c: Partial<T.ToolCredential>) => ({ agent: 'p/a', tool: 'claude', running: true, ...c }) as T.ToolCredential;
  const work = cred({ variable: 'CLAUDE_CODE_OAUTH_TOKEN', account: 'work' });
  assert.deepEqual(summarizeCredentials([work, work, work, cred({ running: false })]), { off: 0, variables: [], groups: [{ on: 'work', count: 3 }, { on: 'idle', count: 1 }] });
  assert.deepEqual(summarizeCredentials([work, cred({ variable: 'ANTHROPIC_API_KEY', account: 'work' }), cred({ tool: 'codex' })]), {
    off: 1,
    variables: ['ANTHROPIC_API_KEY'],
    groups: [{ on: 'work', count: 1 }, { on: 'own', count: 1 }],
  });
});

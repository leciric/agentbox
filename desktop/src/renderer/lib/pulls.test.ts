// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { byAuthor, byState, labelHex, labelStyle, matchLabels, readMine, withEdits, withLabels, writeMine } from './pulls.ts';

const pr = (number: number, state: string, author?: string, labels?: T.Label[]) => ({ number, state, author, labels }) as T.PullRequest;
const nightly: T.Label = { name: 'nightly', color: '5319e7' };
const ciFull: T.Label = { name: 'ci:full', color: '0e8a16', description: 'Run every CI job' };
const deployDev: T.Label = { name: 'deploy-dev', color: 'fbca04' };

test('the state chips and Mine combine', () => {
  const prs = [pr(1, 'open', 'Octocat'), pr(2, 'merged', 'octocat'), pr(3, 'open', 'hubot'), pr(4, 'closed')];
  assert.deepEqual(byState(prs, 'open').map((p) => p.number), [1, 3]);
  assert.deepEqual(byState(prs, 'closed').map((p) => p.number), [2, 4]);
  assert.equal(byState(prs, 'all').length, 4);
  // Logins compare without case, as GitHub's do.
  assert.deepEqual(byAuthor(prs, 'octocat').map((p) => p.number), [1, 2]);
  assert.deepEqual(byState(byAuthor(prs, 'octocat'), 'open').map((p) => p.number), [1]);
  // No login known: Mine can't say, so nothing is hidden.
  assert.equal(byAuthor(prs, undefined).length, 4);
});

test('pending label edits show over what the daemon said, in order', () => {
  const edits = [
    { id: 0, number: 9, add: [nightly], remove: ['ci:full'] },
    { id: 1, number: 7, add: [deployDev], remove: [] },
    { id: 2, number: 9, add: [nightly, deployDev], remove: [] },
  ];
  assert.deepEqual(withEdits([ciFull], 9, edits), [nightly, deployDev]);
  assert.deepEqual(withEdits(undefined, 7, edits), [deployDev]);
  // Dropping a failed edit is the rollback.
  assert.deepEqual(withEdits([ciFull], 9, []), [ciFull]);
  // Taking off one put on by an earlier pending edit.
  assert.deepEqual(withEdits([], 9, [edits[0], { id: 3, number: 9, add: [], remove: ['nightly'] }]), []);
});

test('withLabels replaces one pull request’s labels', () => {
  const data = { project: 'p', pullRequests: [pr(1, 'open', 'a', [ciFull]), pr(2, 'open')] } as T.ProjectPullRequests;
  const out = withLabels(data, 2, [nightly]);
  assert.deepEqual(out.pullRequests[1].labels, [nightly]);
  assert.equal(out.pullRequests[0], data.pullRequests[0]);
  assert.equal(data.pullRequests[1].labels, undefined);
});

test('the picker searches names and descriptions, names starting with it first', () => {
  const all = [deployDev, nightly, ciFull];
  assert.deepEqual(matchLabels(all, ''), all);
  assert.deepEqual(matchLabels(all, 'CI').map((l) => l.name), ['ci:full']);
  assert.deepEqual(matchLabels(all, 'every').map((l) => l.name), ['ci:full']);
  assert.deepEqual(matchLabels(all, 'de').map((l) => l.name), ['deploy-dev']);
  assert.deepEqual(matchLabels([nightly, { name: 'needs-design', color: 'eeeeee' }], 'n').map((l) => l.name), ['nightly', 'needs-design']);
});

test('labels wear GitHub’s colour, and grey for anything else', () => {
  assert.equal(labelHex('5319E7'), '#5319E7');
  assert.equal(labelHex('red'), '#8b949e');
  const style = labelStyle('0e8a16');
  assert.equal(style.backgroundColor, '#0e8a162e');
  assert.match(style.color, /color-mix\(in oklab, #0e8a16/);
});

test('Mine is remembered per project', () => {
  const store = new Map<string, string>();
  const storage = { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v) };
  assert.equal(readMine('a', storage), false);
  writeMine('a', true, storage);
  assert.equal(readMine('a', storage), true);
  assert.equal(readMine('b', storage), false);
  writeMine('a', false, storage);
  assert.equal(readMine('a', storage), false);
});

// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as T from '../../shared/api.ts';
import { duration, errorMessage, githubAccountLabel, githubErrorSentence, humanBytes, shortCommit, timeAgo, timeUntil } from './utils.ts';

test('humanBytes picks the largest unit that keeps the number small', () => {
  assert.equal(humanBytes(500), '500 B');
  assert.equal(humanBytes(1536), '1.5 KiB');
  assert.equal(humanBytes(1024 * 1024 * 3), '3.0 MiB');
});

test('timeAgo reads recent as "just now"', () => {
  const now = Date.parse('2026-01-01T00:01:00Z');
  assert.equal(timeAgo('2026-01-01T00:00:40Z', now), 'just now');
});

test('timeAgo moves through minutes, hours and days', () => {
  const now = Date.parse('2026-01-02T00:00:00Z');
  assert.equal(timeAgo('2026-01-01T23:58:00Z', now), '2m ago');
  assert.equal(timeAgo('2026-01-01T21:00:00Z', now), '3h ago');
  assert.equal(timeAgo('2025-12-30T00:00:00Z', now), '3d ago');
});

test('timeUntil is timeAgo pointed at the future', () => {
  const now = Date.parse('2026-01-01T00:00:00Z');
  assert.equal(timeUntil('2026-01-01T00:30:00Z', now), '30m');
  assert.equal(timeUntil('2026-01-02T00:00:00Z', now), '1d');
});

test('timeUntil never goes negative for a moment already past', () => {
  const now = Date.parse('2026-01-01T00:00:00Z');
  assert.equal(timeUntil('2025-12-31T00:00:00Z', now), '1m');
});

test('duration formats under a minute in seconds', () => {
  assert.equal(duration('2026-01-01T00:00:00.000Z', '2026-01-01T00:00:05.500Z'), '5.5s');
});

test('duration formats a minute or more as m and s', () => {
  assert.equal(duration('2026-01-01T00:00:00Z', '2026-01-01T00:02:05Z'), '2m 5s');
});

test('duration measures to now with no end given', () => {
  const before = Date.now();
  const text = duration(new Date(before - 2000).toISOString());
  assert.match(text, /^\d+(\.\d)?s$/);
});

test('errorMessage strips Electron\'s remote-invocation prefix', () => {
  assert.equal(errorMessage(new Error("Error invoking remote method 'agentbox:request': Error: the daemon is down")), 'the daemon is down');
  assert.equal(errorMessage(new Error('plain failure')), 'plain failure');
  assert.equal(errorMessage('a string'), 'a string');
});

test('shortCommit takes the first 7 characters of a sha', () => {
  assert.equal(shortCommit('1234567890abcdef'), '1234567');
});

test('githubAccountLabel names the account by its login when known', () => {
  assert.equal(githubAccountLabel('acct', 'octocat'), 'octocat (acct)');
  assert.equal(githubAccountLabel('acct'), 'acct');
  assert.equal(githubAccountLabel(''), '');
});

test('githubErrorSentence explains each kind of GitHub failure', () => {
  const base = { account: 'acct', repo: 'org/repo', message: 'boom' };
  assert.equal(githubErrorSentence({ ...base, kind: T.GitHubNoAccess } as T.GitHubError), "The account acct can't see org/repo.");
  assert.equal(
    githubErrorSentence({ ...base, kind: T.GitHubBadToken } as T.GitHubError),
    'GitHub refused the account acct: its token was revoked, or has expired.',
  );
  assert.equal(
    githubErrorSentence({ ...base, kind: T.GitHubNoAccount } as T.GitHubError),
    'This project\'s GitHub account "acct" isn\'t stored any more.',
  );
  assert.equal(
    githubErrorSentence({ ...base, account: '', kind: T.GitHubNoAccount } as T.GitHubError),
    "No GitHub account is stored, so org/repo's pull requests can't be read.",
  );
  assert.equal(githubErrorSentence({ ...base, kind: 'other' } as unknown as T.GitHubError), 'GitHub: boom');
});

test('githubErrorSentence leads with the login when there is one', () => {
  const err = { account: 'acct', login: 'octocat', repo: 'org/repo', message: 'boom', kind: T.GitHubNoAccess } as T.GitHubError;
  assert.equal(githubErrorSentence(err), "The account acct (octocat) can't see org/repo.");
});

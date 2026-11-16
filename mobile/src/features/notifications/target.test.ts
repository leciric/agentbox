import assert from 'node:assert/strict';
import { test } from 'node:test';
import { hhmm, notificationTarget, sameScreen, shiftQuiet } from './target.ts';

test('a notification opens its agent, or the review for a pull request', () => {
  assert.equal(notificationTarget({ kind: 'needs_you', ref: 'web/agent-3' }), '/chat/web/agent-3');
  assert.equal(notificationTarget({ kind: 'finished', ref: 'my app/agent-3' }), '/chat/my%20app/agent-3');
  assert.equal(notificationTarget({ kind: 'ci_failed', ref: 'web/agent-3', pr: '7' }), '/chat/web/agent-3');
  assert.equal(notificationTarget({ kind: 'pr_ready', ref: 'web/agent-3', pr: '12' }), '/review/web?agent=web%2Fagent-3');
  // the agent is gone: the project's review lists its pull request
  assert.equal(notificationTarget({ kind: 'ci_failed', ref: 'web', pr: '7' }), '/review/web');
  assert.equal(notificationTarget({ kind: 'needs_you', ref: 'web' }), '/chat/web');
  assert.equal(notificationTarget({ kind: 'test', ref: '' }), undefined);
  assert.equal(notificationTarget(undefined), undefined);
});

test('a notification about the screen open is not shown over it', () => {
  assert.ok(sameScreen('/chat/my%20app/agent-3', '/chat/my app/agent-3'));
  assert.ok(sameScreen('/review/web?agent=web%2Fagent-3', '/review/web'));
  assert.ok(!sameScreen('/chat/web/agent-3', '/chat/web'));
  assert.ok(!sameScreen(undefined, '/'));
});

test('quiet hours step round the clock', () => {
  assert.equal(hhmm(22 * 60), '22:00');
  assert.equal(hhmm(7 * 60 + 30), '07:30');
  assert.equal(shiftQuiet(23 * 60 + 30, 30), 0);
  assert.equal(shiftQuiet(0, -30), 23 * 60 + 30);
});

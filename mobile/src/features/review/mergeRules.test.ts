import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { mergeBlock, pickMethod } from './mergeRules.ts';

const pr = (more: Partial<T.PullRequest> = {}): T.PullRequest => ({ number: 7, title: 'x', state: 'open', checks: 'passing', url: '', ...more });
const pulls = (more: Partial<T.ProjectPullRequests> = {}): T.ProjectPullRequests => ({ project: 'web', pullRequests: [], ...more });

test('what keeps a pull request from merging', () => {
  assert.equal(mergeBlock(pr(), pulls()), undefined);
  assert.equal(mergeBlock(pr({ draft: true }), pulls()), 'draft');
  assert.equal(mergeBlock(pr({ conflict: true }), pulls()), 'conflict');
  assert.equal(mergeBlock(pr({ checks: 'failing' }), pulls()), 'failing');
  assert.equal(mergeBlock(pr(), pulls({ canMergeKnown: true, canMerge: false })), 'noPermission');
  assert.equal(mergeBlock(pr(), pulls({ canMergeKnown: false, canMerge: false })), undefined);
});

test('the method last used, when the repository still allows it', () => {
  assert.equal(pickMethod(['squash', 'merge'], 'merge'), 'merge');
  assert.equal(pickMethod(['squash', 'merge'], 'rebase'), 'squash');
  assert.equal(pickMethod(['rebase'], null), 'rebase');
});

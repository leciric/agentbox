// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { composerSkills, frontmatter, originOf, searchSkills, skillMentionAt, skillNameOf, skillTitle } from './skills.ts';

const skill = (name: string, description = '', extra: Partial<T.Skill> = {}) =>
  ({ name, description, enabled: true, userInvocable: true, ...extra }) as T.Skill;

test('searchSkills ranks the name above the description, and finds letters in order', () => {
  const skills = [skill('deploy-app', 'Ship the review app'), skill('review-pr', 'Review a pull request'), skill('pdf', 'Fill forms')];
  assert.deepEqual(searchSkills(skills, 'review').map((s) => s.name), ['review-pr', 'deploy-app']);
  assert.deepEqual(searchSkills(skills, '$rvpr').map((s) => s.name), ['review-pr']);
  assert.deepEqual(searchSkills(skills, '/').map((s) => s.name), ['deploy-app', 'pdf', 'review-pr']);
  assert.deepEqual(searchSkills(skills, 'zzz'), []);
});

test('composerSkills offers what the chat has and a user may invoke', () => {
  const skills = [skill('a', '', { active: true }), skill('b', '', { active: false }), skill('c', '', { active: true, userInvocable: false }), skill('d', '', { enabled: false })];
  assert.deepEqual(composerSkills(skills, true).map((s) => s.name), ['a']);
  assert.deepEqual(composerSkills(skills, false).map((s) => s.name), ['a', 'b']);
});

test('skillMentionAt finds the $word under the cursor and nothing else', () => {
  assert.deepEqual(skillMentionAt('use $rev', 8), { start: 4, query: 'rev' });
  assert.deepEqual(skillMentionAt('$', 1), { start: 0, query: '' });
  assert.equal(skillMentionAt('costs a$5', 9), undefined);
  assert.equal(skillMentionAt('use $rev now', 6), undefined);
});

test('frontmatter reads folded and quoted values', () => {
  const { fields, body } = frontmatter('---\nname: pdf\ndescription: >\n  Fill PDF forms\n  and read them.\nlicense: "MIT"\n---\n# PDF\n');
  assert.deepEqual(fields, [['name', 'pdf'], ['description', 'Fill PDF forms and read them.'], ['license', 'MIT']]);
  assert.equal(body, '# PDF\n');
  assert.deepEqual(frontmatter('plain'), { fields: [], body: 'plain' });
});

test('names, titles and origins', () => {
  assert.equal(skillNameOf('My Great_Skill!'), 'my-great-skill');
  assert.equal(skillNameOf('review-', true), 'review-');
  assert.equal(skillTitle('review-pr'), 'Review Pr');
  assert.equal(originOf(''), 'agentbox');
  assert.equal(originOf('claude-plugin:toolkit/pdf'), 'claude-plugin');
  assert.equal(originOf('https://github.com/a/b#x'), 'git');
  assert.equal(originOf('/home/me/skills/x'), 'folder');
});

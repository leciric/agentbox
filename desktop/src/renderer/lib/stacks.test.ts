// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { stackHead, stackOrder } from './stacks.ts';

interface PR {
  n: number;
  head: string;
  base: string;
}
const order = (prs: PR[]) => stackOrder(prs, (p) => p.head, (p) => p.base).map((s) => `${'  '.repeat(s.depth)}#${s.item.n}${s.parent ? ` on #${s.parent.n}` : ''}`);

test('a child follows its parent, indented, wherever it was listed', () => {
  const prs = [
    { n: 15, head: 'agentbox/csv-import', base: 'agentbox/csv-export' },
    { n: 9, head: 'fix/login', base: 'main' },
    { n: 12, head: 'agentbox/csv-export', base: 'main' },
    { n: 18, head: 'agentbox/csv-preview', base: 'agentbox/csv-import' },
  ];
  assert.deepEqual(order(prs), ['#9', '#12', '  #15 on #12', '    #18 on #15']);
});

test('one standing on a branch that is not listed is a root', () => {
  assert.deepEqual(order([{ n: 15, head: 'b', base: 'a' }]), ['#15']);
});

test('a cycle is broken rather than lost', () => {
  const out = order([
    { n: 1, head: 'a', base: 'b' },
    { n: 2, head: 'b', base: 'a' },
  ]);
  assert.equal(out.length, 2);
});

test('children of one parent keep their order', () => {
  const s = stackOrder(
    [
      { n: 1, head: 'p', base: 'main' },
      { n: 3, head: 'c3', base: 'p' },
      { n: 2, head: 'c2', base: 'p' },
    ],
    (p) => p.head,
    (p) => p.base,
  );
  assert.deepEqual(s[0].children.map((c) => c.n), [3, 2]);
});

test('a fork pull request whose head is main is not a parent', () => {
  const prs = [
    { n: 526, headBranch: 'main', baseBranch: 'main', fork: true },
    { n: 458, headBranch: 'agentbox/a', baseBranch: 'main' },
    { n: 489, headBranch: 'agentbox/b', baseBranch: 'main' },
  ];
  const out = stackOrder(prs, stackHead, (p) => p.baseBranch);
  assert.deepEqual(out.map((s) => [s.item.n, s.depth, s.parent?.n]), [[526, 0, undefined], [458, 0, undefined], [489, 0, undefined]]);
  // The same pull request from the repository itself would be one.
  const own = stackOrder([{ ...prs[0], fork: false }, ...prs.slice(1)], stackHead, (p) => p.baseBranch);
  assert.equal(own[1]?.parent?.n, 526);
});

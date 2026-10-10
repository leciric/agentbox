import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { Root } from 'mdast';
import type * as T from '../../shared/api';
import { byAuthor, byState, labelHex, labelStyle, matchLabels, readMine, withEdits, withLabels, writeMine, fileKind, fileTree, firstToReview, imagesInHTML, initiallyOpen, isGitHubImage, languageOfPath, nextToLoad, parsePatch, remarkHTMLImages, treeOrder } from './pulls.ts';

test('imagesInHTML reads what GitHub’s uploader writes', () => {
  assert.deepEqual(imagesInHTML('<img width="640" alt="The &quot;modal&quot;" src="https://github.com/user-attachments/assets/abc" />'), [
    { src: 'https://github.com/user-attachments/assets/abc', alt: 'The "modal"', width: '640' },
  ]);
  assert.deepEqual(imagesInHTML("<p align=center><IMG src='a.png'><img alt=x></p>"), [{ src: 'a.png', alt: '', width: undefined }]);
  assert.deepEqual(imagesInHTML('<details><summary>x</summary></details>'), []);
});

test('remarkHTMLImages turns raw HTML pictures into images, block and inline', () => {
  const tree: Root = {
    type: 'root',
    children: [
      { type: 'html', value: '<img src="https://github.com/a.png" alt="a">' },
      { type: 'paragraph', children: [{ type: 'text', value: 'see ' }, { type: 'html', value: '<img src="b.png" width="20">' }] },
      { type: 'html', value: '<details>' },
    ],
  };
  remarkHTMLImages()(tree);
  assert.deepEqual(tree.children[0], { type: 'paragraph', children: [{ type: 'image', url: 'https://github.com/a.png', alt: 'a', data: undefined }] });
  assert.deepEqual(tree.children[1], {
    type: 'paragraph',
    children: [{ type: 'text', value: 'see ' }, { type: 'image', url: 'b.png', alt: '', data: { hProperties: { width: '20' } } }],
  });
  assert.deepEqual(tree.children[2], { type: 'html', value: '<details>' });
});

test('isGitHubImage is only GitHub’s own hosts, over HTTPS', () => {
  assert.equal(isGitHubImage('https://github.com/user-attachments/assets/abc'), true);
  assert.equal(isGitHubImage('https://private-user-images.githubusercontent.com/1/2-x.png?jwt=y'), true);
  assert.equal(isGitHubImage('http://github.com/x.png'), false);
  assert.equal(isGitHubImage('https://img.shields.io/badge/x'), false);
  assert.equal(isGitHubImage('relative.png'), false);
});

test('fileKind collapses what a program wrote', () => {
  const f = (path: string, additions = 3, deletions = 1, hasDiff = true, status = 'modified') => fileKind({ path, additions, deletions, hasDiff, status });
  assert.equal(f('internal/daemon/pulls.go'), 'code');
  assert.equal(f('desktop/src/shared/api.ts'), 'generated');
  assert.equal(f('desktop/package-lock.json'), 'lockfile');
  assert.equal(f('go.sum'), 'lockfile');
  assert.equal(f('internal/brief/testdata/agent.golden'), 'golden');
  assert.equal(f('logo.png', 0, 0, false, 'added'), 'binary');
  assert.equal(f('old.go', 0, 0, false, 'renamed'), 'renamed');
  assert.equal(f('huge.go', 5000, 0, false), 'large');
  assert.equal(f('long.go', 300, 200), 'large');
});

test('initiallyOpen is the first few small files somebody reads', () => {
  const file = (path: string, n = 10) => ({ path, additions: n, deletions: 0, hasDiff: true });
  const open = initiallyOpen([file('desktop/src/shared/api.ts'), file('a.go'), file('big.go', 300), file('b.go'), file('c.go'), file('d.go')]);
  assert.deepEqual([...open], ['a.go', 'b.go', 'c.go']);
});

test('parsePatch numbers each side', () => {
  const lines = parsePatch('@@ -10,3 +10,3 @@ func x()\n a\n-b\n+c\n d\n\\ No newline at end of file');
  assert.deepEqual(lines, [
    { kind: 'hunk', text: '@@ -10,3 +10,3 @@ func x()' },
    { kind: 'context', text: 'a', old: 10, new: 10 },
    { kind: 'del', text: 'b', old: 11 },
    { kind: 'add', text: 'c', new: 11 },
    { kind: 'context', text: 'd', old: 12, new: 12 },
    { kind: 'note', text: 'No newline at end of file' },
  ]);
});

test('languageOfPath goes by extension and a few names', () => {
  assert.equal(languageOfPath('internal/daemon/pulls.go'), 'go');
  assert.equal(languageOfPath('desktop/src/App.tsx'), 'tsx');
  assert.equal(languageOfPath('mise.toml'), 'toml');
  assert.equal(languageOfPath('include/x.h'), 'c');
  assert.equal(languageOfPath('Dockerfile'), 'dockerfile');
  assert.equal(languageOfPath('LICENSE'), undefined);
  assert.equal(languageOfPath('Makefile'), undefined);
});

test('nextToLoad is the first open file whose diff hasn’t loaded, one at a time', () => {
  const files = [
    { path: 'a.go', hasDiff: true },
    { path: 'logo.png', hasDiff: false },
    { path: 'b.go', hasDiff: true },
    { path: 'c.go', hasDiff: true },
  ];
  const open = new Set(['a.go', 'logo.png', 'c.go']);
  assert.equal(nextToLoad(files, open, new Set()), 'a.go');
  assert.equal(nextToLoad(files, open, new Set(['a.go'])), 'c.go');
  assert.equal(nextToLoad(files, open, new Set(['a.go', 'c.go'])), undefined);
  assert.equal(nextToLoad(files, new Set(['b.go', 'c.go']), new Set(['a.go'])), 'b.go');
});

test('fileTree puts folders first, by name, and folds single-folder chains', () => {
  const files = ['desktop/src/renderer/lib/pulls.ts', 'desktop/src/renderer/components/Modal.tsx', 'go.mod', 'internal/github/detail.go', 'README.md'].map((path) => ({ path }));
  const names = (nodes: ReturnType<typeof fileTree<{ path: string }>>): unknown =>
    nodes.map((n) => (n.kind === 'dir' ? { [n.name]: names(n.children) } : n.name));
  assert.deepEqual(names(fileTree(files)), [
    { 'desktop/src/renderer': [{ components: ['Modal.tsx'] }, { lib: ['pulls.ts'] }] },
    { 'internal/github': ['detail.go'] },
    'go.mod',
    'README.md',
  ]);
  const tree = fileTree(files);
  assert.equal(tree[0].kind === 'dir' && tree[0].children[0].kind === 'dir' && tree[0].children[0].path, 'desktop/src/renderer/components');
  assert.deepEqual(
    treeOrder(tree).map((f) => f.path),
    ['desktop/src/renderer/components/Modal.tsx', 'desktop/src/renderer/lib/pulls.ts', 'internal/github/detail.go', 'go.mod', 'README.md'],
  );
});

test('firstToReview skips what a program wrote', () => {
  const f = (path: string) => ({ path, additions: 1, deletions: 0, hasDiff: true });
  assert.equal(firstToReview([f('desktop/package-lock.json'), f('desktop/src/shared/api.ts'), f('internal/x.go')])?.path, 'internal/x.go');
  assert.equal(firstToReview([f('go.sum')])?.path, 'go.sum');
  assert.equal(firstToReview([]), undefined);
});

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

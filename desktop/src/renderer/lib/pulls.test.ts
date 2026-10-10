import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { Root } from 'mdast';
import { fileKind, imagesInHTML, initiallyOpen, isGitHubImage, languageOfPath, parsePatch, remarkHTMLImages } from './pulls.ts';

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

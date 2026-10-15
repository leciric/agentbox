// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as T from '../../shared/api.ts';
import { ancestorsOf, changesFiles, panelLayout, diffRows, refreshFiles, treeRows, closeFile, fileLinkOf, formatSize, hasFiles, languageOfPath, openFile, openedFile, panelPrefs, relativeTo, resolveFile, splitPosition, statusOf, statusesOf } from './files.ts';

test('splitPosition reads a line and column off a path, or a #L hash', () => {
  assert.deepEqual(splitPosition('src/a.go:12'), { path: 'src/a.go', line: 12 });
  assert.deepEqual(splitPosition('src/a.go:12:3'), { path: 'src/a.go', line: 12, column: 3 });
  assert.deepEqual(splitPosition('src/a.go', '#L40'), { path: 'src/a.go', line: 40 });
  assert.deepEqual(splitPosition('src/a.go', '#L40C2'), { path: 'src/a.go', line: 40, column: 2 });
  assert.deepEqual(splitPosition('src/a.go', '#L4-L9'), { path: 'src/a.go', line: 4 });
  assert.deepEqual(splitPosition('src/a.go:0'), { path: 'src/a.go' });
  assert.deepEqual(splitPosition('src/a.go'), { path: 'src/a.go' });
});

test('fileLinkOf takes what reads as a file in the worktree', () => {
  const root = '/w/repo';
  assert.deepEqual(fileLinkOf('internal/api/files.go', root), { path: 'internal/api/files.go' });
  assert.deepEqual(fileLinkOf('internal/api/files.go:42', root), { path: 'internal/api/files.go', line: 42 });
  assert.deepEqual(fileLinkOf('./README.md', root), { path: 'README.md' });
  assert.deepEqual(fileLinkOf('Composer.tsx', root), { path: 'Composer.tsx' });
  assert.deepEqual(fileLinkOf('Makefile', root), { path: 'Makefile' });
  assert.deepEqual(fileLinkOf('/w/repo/src/a.ts:3:1', root), { path: 'src/a.ts', line: 3, column: 1 });
  assert.deepEqual(fileLinkOf('docs/a.md#L7', root), { path: 'docs/a.md', line: 7 });
  assert.deepEqual(fileLinkOf('.github/workflows/ci.yml', root), { path: '.github/workflows/ci.yml' });
});

test('fileLinkOf leaves alone what is not a file', () => {
  const root = '/w/repo';
  for (const text of ['go test ./...', 'https://example.com/a.go', '1.2.3', 'v2.0', 'e.g', 'useState', '--flag', '$HOME/x.txt', '/etc/passwd', '/w/repo2/a.go', '../up.go', 'src/', 'a|b.go', '~/x.go', '', 'foo()', 'x.y.z()', 'mailto:a@b.co']) {
    assert.equal(fileLinkOf(text, root), undefined, text);
  }
});

test('resolveFile finds a bare file name when exactly one file has it', () => {
  const files = ['src/a/Composer.tsx', 'src/b/util.ts', 'src/c/util.ts', 'README.md'];
  assert.equal(resolveFile('Composer.tsx', files), 'src/a/Composer.tsx');
  assert.equal(resolveFile('util.ts', files), 'util.ts');
  assert.equal(resolveFile('README.md', files), 'README.md');
  assert.equal(resolveFile('src/b/util.ts', files), 'src/b/util.ts');
  assert.equal(resolveFile('x.ts', undefined), 'x.ts');
});

test('relativeTo strips the worktree from a path inside it only', () => {
  assert.equal(relativeTo('/w/repo/src/a.go', '/w/repo/'), 'src/a.go');
  assert.equal(relativeTo('/w/repo2/a.go', '/w/repo'), '/w/repo2/a.go');
  assert.equal(relativeTo('src/a.go', ''), 'src/a.go');
});

test('statusesOf colours files, the folders above them, and what is in an untracked folder', () => {
  const s = statusesOf({ 'src/api/a.go': T.FileModified, 'README.md': T.FileAdded, 'docs/': T.FileUntracked, 'old.go': T.FileDeleted });
  assert.equal(statusOf(s, 'src/api/a.go', false), T.FileModified);
  assert.equal(statusOf(s, 'src/api', true), T.FileModified);
  assert.equal(statusOf(s, 'src', true), T.FileModified);
  assert.equal(statusOf(s, 'README.md', false), T.FileAdded);
  assert.equal(statusOf(s, 'docs', true), T.FileUntracked);
  assert.equal(statusOf(s, 'docs/guide/x.md', false), T.FileUntracked);
  assert.equal(statusOf(s, 'docs/guide', true), T.FileUntracked);
  assert.equal(statusOf(s, 'src/other.go', false), undefined);
  assert.equal(statusOf(s, 'srcx', true), undefined);
  assert.equal(statusOf(statusesOf(undefined), 'a', false), undefined);
});

test('languageOfPath picks the highlighter language by file name', () => {
  assert.equal(languageOfPath('internal/api/files.go'), 'go');
  assert.equal(languageOfPath('src/App.tsx'), 'tsx');
  assert.equal(languageOfPath('scripts/build.mjs'), 'javascript');
  assert.equal(languageOfPath('a/Dockerfile'), 'dockerfile');
  assert.equal(languageOfPath('.env.local'), 'ini');
  assert.equal(languageOfPath('README.md'), 'markdown');
  assert.equal(languageOfPath('LICENSE'), undefined);
  assert.equal(languageOfPath('x.unknownext'), undefined);
});

test('formatSize and panelLayout', () => {
  assert.equal(formatSize(512), '512 B');
  assert.equal(formatSize(2048), '2.0 KB');
  assert.equal(formatSize(3 * 1024 * 1024), '3.0 MB');
  assert.deepEqual(panelLayout(100, 2000), { width: 320, overlay: false });
  assert.deepEqual(panelLayout(1580, 2000), { width: 1580, overlay: false });
  assert.deepEqual(panelLayout(1900, 2000), { width: 1900, overlay: true });
  assert.deepEqual(panelLayout(700, 2000), { width: 700, overlay: false });
  // Wider than the room beside the chat: over it, leaving a strip of it.
  assert.deepEqual(panelLayout(640, 575), { width: 527, overlay: true });
  assert.deepEqual(panelLayout(400, 575), { width: 400, overlay: true });
  assert.deepEqual(panelLayout(320, 745), { width: 320, overlay: false });
  assert.deepEqual(panelLayout(640, 745), { width: 640, overlay: true });
  assert.deepEqual(panelLayout(640, 300), { width: 300, overlay: true });
});

test('openFile opens the panel on the file, and each opening is new', () => {
  openFile('p/agent-1', 'src/a.go', 4);
  const first = openedFile('p/agent-1');
  assert.equal(first?.path, 'src/a.go');
  assert.equal(first?.line, 4);
  assert.equal(panelPrefs().open, true);
  openFile('p/agent-1', 'src/a.go', 4);
  assert.notEqual(openedFile('p/agent-1')?.nonce, first?.nonce);
  assert.equal(openedFile('p/agent-2'), null);
  closeFile('p/agent-1');
  assert.equal(openedFile('p/agent-1'), null);
});

test('hasFiles: not the Home chat, nor a past agent', () => {
  assert.equal(hasFiles('p/agent-1'), true);
  assert.equal(hasFiles('p/lead'), true);
  assert.equal(hasFiles(`${T.HomeProject}/lead`), false);
  assert.equal(hasFiles('p/agent-1', true), false);
});

test('treeRows walks the open folders, marking the ones still loading', () => {
  const entry = (path: string, dir = false): T.FileEntry => ({ name: path.slice(path.lastIndexOf('/') + 1), path, dir });
  const lists = new Map<string, T.FileList | undefined>([
    ['', { path: '', entries: [entry('src', true), entry('docs', true), entry('README.md')], truncated: false }],
    ['src', { path: 'src', entries: [entry('src/a.go')], truncated: true }],
  ]);
  const rows = treeRows(lists, new Set(['src', 'docs']));
  assert.deepEqual(
    rows.map((r) => `${r.depth}:${r.entry?.path ?? (r.loading ? 'loading' : 'more')}`),
    ['0:src', '1:src/a.go', '1:more', '0:docs', '1:loading', '0:README.md'],
  );
  assert.deepEqual(treeRows(new Map(), new Set()).map((r) => r.loading), [true]);
});

test('ancestorsOf lists the folders above a path', () => {
  assert.deepEqual(ancestorsOf('a/b/c.go'), ['a', 'a/b']);
  assert.deepEqual(ancestorsOf('c.go'), []);
});

test('diffRows numbers both sides of a whole-file diff', () => {
  const rows = diffRows('a\nb\nc\n', 'a\nB\nc\nd\n');
  assert.deepEqual(
    rows.map((r) => `${r.sign}${r.old ?? '-'}:${r.new ?? '-'} ${r.text}`),
    [' 1:1 a', '-2:- b', '+-:2 B', ' 3:3 c', '+-:4 d'],
  );
});

test('changesFiles: a finished edit, delete, move or command', () => {
  const item = (kind: string, status: string): T.ChatItem => ({ id: 'i', turn: 't', kind: 'tool', createdAt: '', updatedAt: '', tool: { callId: 'c', title: '', kind, status } });
  assert.equal(changesFiles(item('edit', 'completed')), true);
  assert.equal(changesFiles(item('execute', 'failed')), true);
  assert.equal(changesFiles(item('edit', 'in_progress')), false);
  assert.equal(changesFiles(item('read', 'completed')), false);
  assert.equal(changesFiles({ id: 'i', turn: 't', kind: 'assistant', createdAt: '', updatedAt: '' }), false);
});

test('refreshFiles reads the status fresh, only for a chat whose panel is open', async () => {
  const { QueryClient, QueryObserver } = await import('@tanstack/react-query');
  const client = new QueryClient();
  const calls: string[] = [];
  const status = async (ref: string, fresh: boolean) => {
    calls.push(`${ref}:${fresh}`);
    return { base: 'HEAD', changes: {}, truncated: false };
  };
  refreshFiles(client, 'p/a', status, 0);
  await new Promise((r) => setTimeout(r, 10));
  assert.deepEqual(calls, []);
  const observer = new QueryObserver(client, { queryKey: ['fileStatus', 'p/a'], queryFn: () => status('p/a', false) });
  const unsubscribe = observer.subscribe(() => {});
  await new Promise((r) => setTimeout(r, 10));
  refreshFiles(client, 'p/a', status, 0);
  refreshFiles(client, 'p/a', status, 0);
  await new Promise((r) => setTimeout(r, 10));
  assert.deepEqual(calls, ['p/a:false', 'p/a:true']);
  unsubscribe();
  client.clear();
});

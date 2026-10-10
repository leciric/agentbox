// What the pull request view needs beyond the chat's Markdown: pictures written
// as HTML in a description, which GitHub's own uploader writes, and how each
// changed file is shown — its diff split into lines, and whether it starts
// collapsed.
//
// And, for the Pull requests tab, what it works out from the list it is given: which pull
// requests a filter keeps, how labels look and what an edit of them shows
// before GitHub answers. Kept apart from the component so it can be tested.
import type { Nodes, Parent, PhrasingContent, Root, RootContent } from 'mdast';
import type * as T from '../../shared/api';
import { languageOf } from './highlight.ts';

export type HTMLImage = { src: string; alt: string; width?: string };

// imagesInHTML is every <img> in a piece of raw HTML, with the attributes a
// picture needs. GitHub's uploader writes `<img width="640" alt="…" src="…">`.
export function imagesInHTML(html: string): HTMLImage[] {
  const out: HTMLImage[] = [];
  for (const [tag] of html.matchAll(/<img\b[^>]*>/gi)) {
    const attrs: Record<string, string> = {};
    for (const m of tag.matchAll(/([a-zA-Z-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/g)) {
      attrs[m[1].toLowerCase()] = decodeEntities(m[2] ?? m[3] ?? m[4] ?? '');
    }
    if (attrs.src) out.push({ src: attrs.src, alt: attrs.alt ?? '', width: /^\d+%?$/.test(attrs.width ?? '') ? attrs.width : undefined });
  }
  return out;
}

function decodeEntities(s: string): string {
  return s.replace(/&(amp|quot|#39|lt|gt);/g, (_, e: string) => ({ amp: '&', quot: '"', '#39': "'", lt: '<', gt: '>' })[e] ?? '');
}

// remarkHTMLImages turns the raw HTML pictures in a description into Markdown
// images, so they show; the chat's Markdown drops raw HTML, and any other tag
// still goes.
export function remarkHTMLImages() {
  return (tree: Root) => {
    const visit = (node: Nodes) => {
      if (!('children' in node)) return;
      const parent = node as Parent;
      parent.children = (parent.children as RootContent[]).flatMap((child): RootContent[] => {
        if (child.type !== 'html') {
          visit(child);
          return [child];
        }
        const images = imagesInHTML(child.value).map((img): RootContent => ({
          type: 'image',
          url: img.src,
          alt: img.alt,
          data: img.width ? { hProperties: { width: img.width } } : undefined,
        }));
        if (images.length === 0) return [child];
        // An HTML block sits where a paragraph would; inline HTML is already in one.
        return parent.type === 'root' || parent.type === 'blockquote' || parent.type === 'listItem'
          ? [{ type: 'paragraph', children: images as PhrasingContent[] }]
          : images;
      }) as typeof parent.children;
    };
    visit(tree);
  };
}

// githubImageHosts are the hosts the daemon reads a description's pictures
// from (github.Client.Image); a picture anywhere else is shown as a link.
const githubImageHosts = new Set([
  'github.com',
  'raw.githubusercontent.com',
  'private-user-images.githubusercontent.com',
  'user-images.githubusercontent.com',
  'camo.githubusercontent.com',
  'avatars.githubusercontent.com',
  'objects.githubusercontent.com',
]);

export function isGitHubImage(src: string): boolean {
  try {
    const u = new URL(src);
    return u.protocol === 'https:' && githubImageHosts.has(u.hostname.toLowerCase());
  } catch {
    return false;
  }
}

// What a changed file is, for whether its diff starts open: one somebody
// reads, or one a program wrote.
export type FileKind = 'code' | 'generated' | 'lockfile' | 'golden' | 'binary' | 'large' | 'renamed';

const lockfiles = new Set([
  'package-lock.json',
  'pnpm-lock.yaml',
  'yarn.lock',
  'bun.lockb',
  'go.sum',
  'go.work.sum',
  'Cargo.lock',
  'poetry.lock',
  'uv.lock',
  'Pipfile.lock',
  'Gemfile.lock',
  'composer.lock',
  'flake.lock',
  'mix.lock',
  'pubspec.lock',
]);

// generatedPaths are files this repository generates (api.ts from Go, D18;
// the VNC client's build) and the usual shapes of generated code elsewhere.
const generatedPaths = [
  /(^|\/)desktop\/src\/shared\/api\.ts$/,
  /(^|\/)vnc\.js$/,
  /(^|\/)CHANGELOG\.md$/,
  /\.min\.(js|css)$/,
  /\.pb\.go$/,
  /_gen(erated)?\.go$/,
  /\.generated\.\w+$/,
  /\.map$/,
];

// largeDiff is how many changed lines make a diff too long to open unasked.
export const largeDiff = 400;

type FileShape = { path: string; status?: string; additions: number; deletions: number; hasDiff: boolean };

export function fileKind(f: FileShape): FileKind {
  const name = f.path.slice(f.path.lastIndexOf('/') + 1);
  if (lockfiles.has(name)) return 'lockfile';
  if (name.endsWith('.golden') || /(^|\/)__snapshots__\//.test(f.path) || name.endsWith('.snap')) return 'golden';
  if (generatedPaths.some((re) => re.test(f.path))) return 'generated';
  // GitHub sends no diff for a binary file, nor for one too big to send.
  if (!f.hasDiff) return f.additions + f.deletions > 0 ? 'large' : f.status === 'renamed' ? 'renamed' : 'binary';
  if (f.additions + f.deletions > largeDiff) return 'large';
  return 'code';
}

// openByDefault is how many of the first files, of those somebody reads and
// small enough, start with their diff open.
export const openByDefault = 3;
const smallDiff = 120;

// initiallyOpen is the paths whose diff starts open: the first few small,
// readable files.
export function initiallyOpen(files: FileShape[]): Set<string> {
  const out = new Set<string>();
  for (const f of files) {
    if (out.size >= openByDefault) break;
    if (fileKind(f) === 'code' && f.additions + f.deletions <= smallDiff) out.add(f.path);
  }
  return out;
}

// A line of a unified diff: one it adds, one it removes, one around them, or
// a hunk's header. Old and new are its numbers on each side.
export type DiffLine =
  | { kind: 'hunk'; text: string }
  | { kind: 'add' | 'del' | 'context'; text: string; old?: number; new?: number }
  | { kind: 'note'; text: string };

export function parsePatch(patch: string): DiffLine[] {
  const out: DiffLine[] = [];
  let oldLine = 0;
  let newLine = 0;
  for (const line of patch.split('\n')) {
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[2]);
      out.push({ kind: 'hunk', text: line });
    } else if (line.startsWith('+')) out.push({ kind: 'add', text: line.slice(1), new: newLine++ });
    else if (line.startsWith('-')) out.push({ kind: 'del', text: line.slice(1), old: oldLine++ });
    else if (line.startsWith('\\')) out.push({ kind: 'note', text: line.slice(1).trim() });
    else if (out.length > 0) out.push({ kind: 'context', text: line.slice(1), old: oldLine++, new: newLine++ });
  }
  return out;
}

const extensions: Record<string, string> = {
  h: 'c',
  hpp: 'cpp',
  cc: 'cpp',
  cxx: 'cpp',
  htm: 'html',
  svg: 'xml',
  plist: 'xml',
  jsonc: 'json',
  json5: 'json',
  tmpl: 'markdown',
  mdx: 'markdown',
  env: 'ini',
  conf: 'ini',
  cfg: 'ini',
  kts: 'kotlin',
};

// languageOfPath is the Shiki language a file is highlighted as, if any.
export function languageOfPath(path: string): string | undefined {
  const name = path.slice(path.lastIndexOf('/') + 1);
  if (/^Dockerfile(\..+)?$/.test(name) || name.endsWith('.Dockerfile')) return 'dockerfile';
  if (name === 'Makefile' || name.endsWith('.mk')) return undefined;
  const dot = name.lastIndexOf('.');
  if (dot < 0) return undefined;
  const ext = name.slice(dot + 1).toLowerCase();
  return languageOf(extensions[ext] ?? ext);
}

// nextToLoad is the one diff that may load now: the first open file, in the
// pull request's order, with a diff not loaded yet. Diffs load one by one,
// each after the one before it, rather than every open file at once.
export function nextToLoad(files: { path: string; hasDiff: boolean }[], open: ReadonlySet<string>, loaded: ReadonlySet<string>): string | undefined {
  return files.find((f) => f.hasDiff && open.has(f.path) && !loaded.has(f.path))?.path;
}

// A pull request's files as a tree of folders, the way a review goes through
// them: folders first, then files, each by name, with a folder that holds only
// one folder folded into it (desktop/src/renderer), as GitHub shows them.
export type TreeNode<F extends { path: string }> = { kind: 'dir'; name: string; path: string; children: TreeNode<F>[] } | { kind: 'file'; name: string; file: F };

export function fileTree<F extends { path: string }>(files: F[]): TreeNode<F>[] {
  type Dir = { dirs: Map<string, Dir>; files: F[] };
  const root: Dir = { dirs: new Map(), files: [] };
  for (const f of files) {
    const parts = f.path.split('/');
    let dir = root;
    for (const part of parts.slice(0, -1)) {
      let next = dir.dirs.get(part);
      if (!next) dir.dirs.set(part, (next = { dirs: new Map(), files: [] }));
      dir = next;
    }
    dir.files.push(f);
  }
  const byName = (a: string, b: string) => a.localeCompare(b);
  const build = (dir: Dir, prefix: string): TreeNode<F>[] => [
    ...[...dir.dirs.entries()]
      .sort(([a], [b]) => byName(a, b))
      .map(([name, sub]): TreeNode<F> => {
        // Fold a chain of folders that each hold only the next one.
        while (sub.files.length === 0 && sub.dirs.size === 1) {
          const [only, inner] = [...sub.dirs.entries()][0];
          name = `${name}/${only}`;
          sub = inner;
        }
        const path = prefix + name;
        return { kind: 'dir', name, path, children: build(sub, `${path}/`) };
      }),
    ...dir.files
      .map((file): TreeNode<F> => ({ kind: 'file', name: file.path.slice(file.path.lastIndexOf('/') + 1), file }))
      .sort((a, b) => byName(a.name, b.name)),
  ];
  return build(root, '');
}

// treeOrder is the files in the order the tree shows them, top to bottom.
export function treeOrder<F extends { path: string }>(nodes: TreeNode<F>[]): F[] {
  return nodes.flatMap((n) => (n.kind === 'file' ? [n.file] : treeOrder(n.children)));
}

// firstToReview is the file a review starts on: the tree's first that
// somebody wrote by hand, or its first at all.
export function firstToReview<F extends FileShape>(files: F[]): F | undefined {
  const order = treeOrder(fileTree(files));
  return order.find((f) => fileKind(f) === 'code') ?? order[0];
}

export type PullsState = 'open' | 'closed' | 'all';

// byState keeps the pull requests a state chip shows: open, closed and merged
// ("closed"), or all of them.
export function byState(prs: readonly T.PullRequest[], state: PullsState): T.PullRequest[] {
  if (state === 'all') return [...prs];
  return prs.filter((pr) => (state === 'open' ? pr.state === 'open' : pr.state !== 'open'));
}

// byAuthor keeps the ones login opened, as GitHub's logins compare: without
// case. No login keeps them all, since the account's isn't known.
export function byAuthor(prs: readonly T.PullRequest[], login: string | undefined): T.PullRequest[] {
  if (!login) return [...prs];
  const me = login.toLowerCase();
  return prs.filter((pr) => pr.author?.toLowerCase() === me);
}

// A label edit not answered yet: what it puts on and takes off one pull
// request. The tab shows a pull request's labels with every pending edit
// applied over what the daemon last said, so an edit shows at once, survives
// a refetch that lands before GitHub has answered, and rolls back by itself
// when it fails and is dropped.
export interface LabelEdit {
  id: number;
  number: number;
  add: T.Label[];
  remove: string[];
}

export function withEdits(labels: readonly T.Label[] | undefined, number: number, edits: readonly LabelEdit[]): T.Label[] {
  let out = [...(labels ?? [])];
  for (const edit of edits) {
    if (edit.number !== number) continue;
    out = out.filter((l) => !edit.remove.includes(l.name));
    for (const l of edit.add) if (!out.some((o) => o.name === l.name)) out.push(l);
  }
  return out;
}

// withLabels is the list with one pull request's labels replaced by what
// GitHub answered.
export function withLabels(data: T.ProjectPullRequests, number: number, labels: T.Label[]): T.ProjectPullRequests {
  return { ...data, pullRequests: data.pullRequests.map((pr) => (pr.number === number ? { ...pr, labels } : pr)) };
}

// matchLabels is the picker's search: by name or description, without case,
// names that start with it first.
export function matchLabels(labels: readonly T.Label[], query: string): T.Label[] {
  const q = query.trim().toLowerCase();
  if (!q) return [...labels];
  const hits = labels.filter((l) => l.name.toLowerCase().includes(q) || l.description?.toLowerCase().includes(q));
  const starts = (l: T.Label) => (l.name.toLowerCase().startsWith(q) ? 0 : 1);
  return hits.sort((a, b) => starts(a) - starts(b));
}

// labelStyle is a label in GitHub's colour, readable on either theme: a
// tinted fill and border of the colour itself, and text that mixes it with
// the text colour around it, so yellow is legible on white and navy on black.
export function labelStyle(color: string): { backgroundColor: string; borderColor: string; color: string } {
  const hex = labelHex(color);
  return {
    backgroundColor: `${hex}2e`,
    borderColor: `${hex}73`,
    color: `color-mix(in oklab, ${hex} 60%, currentColor)`,
  };
}

// labelHex is a label's colour as CSS, grey when GitHub sent something else.
export function labelHex(color: string): string {
  return /^[0-9a-f]{6}$/i.test(color) ? `#${color}` : '#8b949e';
}

// The Mine filter is remembered per project, on this machine.
const mineKey = (project: string) => `agentbox.pulls.mine.${project}`;

export function readMine(project: string, storage: Pick<Storage, 'getItem'> = localStorage): boolean {
  return storage.getItem(mineKey(project)) === '1';
}

export function writeMine(project: string, on: boolean, storage: Pick<Storage, 'setItem'> = localStorage): void {
  storage.setItem(mineKey(project), on ? '1' : '0');
}

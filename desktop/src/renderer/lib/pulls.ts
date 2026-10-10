// What the pull request view needs beyond the chat's Markdown: pictures written
// as HTML in a description, which GitHub's own uploader writes, and how each
// changed file is shown — its diff split into lines, and whether it starts
// collapsed.
import type { Nodes, Parent, PhrasingContent, Root, RootContent } from 'mdast';
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

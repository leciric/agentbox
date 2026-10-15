// The file tree and viewer beside a chat (components/files): what is pure about
// them, and the panel's state.
//
// Modelled on T3Code: a path with a position is "path:line:col" or a
// "#L12C3" hash (packages/shared/src/fileLinks.ts, splitFilePathPosition),
// inline code that reads as a file is a link to it
// (apps/web/src/markdown-links.ts, resolveInlineCodeFileLinkMeta), and the
// panel's open state and width are remembered (apps/web/src/rightPanelStore.ts,
// hooks/usePreviewPanelInlineSize.ts).
import { diffLines } from 'diff';
import type { QueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import * as T from '../../shared/api.ts';
import { languageOf } from './highlight.ts';

export interface FilePosition {
  path: string;
  line?: number;
  column?: number;
}

const positionSuffix = /:(\d+)(?::(\d+))?$/;
const positionHash = /^#L(\d+)(?:C(\d+))?(?:-L?\d+)?$/i;

// splitPosition takes the ":12" or ":12:3" off a path, or reads a "#L12" hash.
export function splitPosition(path: string, hash = ''): FilePosition {
  const suffix = positionSuffix.exec(path);
  const match = suffix ?? positionHash.exec(hash);
  if (!match) return { path };
  const line = Number(match[1]);
  const column = match[2] === undefined ? undefined : Number(match[2]);
  return {
    path: suffix ? path.slice(0, -suffix[0].length) : path,
    ...(line > 0 ? { line } : {}),
    ...(column && column > 0 ? { column } : {}),
  };
}

// Names a file has without a dot in them.
const bareNames = new Set(['Makefile', 'Dockerfile', 'Containerfile', 'Justfile', 'Procfile', 'Gemfile', 'Rakefile', 'LICENSE', 'Caddyfile', 'Vagrantfile']);
// An extension: letters, maybe digits, short.
const extension = /\.[A-Za-z][A-Za-z0-9]{0,9}$/;

// fileLinkOf reads inline code, or a link's target, as a file in the worktree
// at root, or undefined when it doesn't look like one: a URL, words, a
// command, a version number. A path needs a folder or an extension, or a
// name like Makefile; an absolute one must be under root.
export function fileLinkOf(text: string, root: string): FilePosition | undefined {
  let s = text.trim();
  if (!s || s.length > 300 || /\s/.test(s) || /^[a-z][a-z0-9+.-]*:\/\//i.test(s) || /^(mailto|data|javascript):/i.test(s)) return undefined;
  let hash = '';
  const h = s.indexOf('#');
  if (h >= 0) {
    hash = s.slice(h);
    s = s.slice(0, h);
  }
  const pos = splitPosition(s, hash);
  let p = pos.path.replace(/^\.\//, '');
  if (!p || /[<>"'`|;&$(){}[\]*?\\]/.test(p) || p.startsWith('-') || p.startsWith('~') || p.startsWith('../') || p.endsWith('/')) return undefined;
  if (p.startsWith('/')) {
    const r = root.replace(/\/+$/, '');
    if (!r || !p.startsWith(`${r}/`)) return undefined;
    p = p.slice(r.length + 1);
  }
  const base = p.slice(p.lastIndexOf('/') + 1);
  if (!base || base === '.' || base === '..') return undefined;
  // "1.2.3", "v2.0", "e.g": no folder and nothing like a file's extension.
  if (!p.includes('/') && !bareNames.has(base) && (!extension.test(base) || /^v?\d+(\.\d+)+$/.test(base) || /^[a-z]\.[a-z]$/i.test(base))) return undefined;
  if (!/[A-Za-z]/.test(base)) return undefined;
  return { ...pos, path: p };
}

// resolveFile finds what a link names among the worktree's files: the path
// itself, or, for a bare file name, the one file with that name, as T3Code's
// openFileInPanel looks basenames up. files is undefined while unknown.
export function resolveFile(path: string, files: string[] | undefined): string {
  if (!files || path.includes('/') || files.includes(path)) return path;
  const found = files.filter((f) => f === path || f.endsWith(`/${path}`));
  return found.length === 1 ? found[0] : path;
}

// relativeTo shows a path inside root relative to it.
export function relativeTo(path: string, root: string): string {
  const r = root.replace(/\/+$/, '');
  return r && path.startsWith(`${r}/`) ? path.slice(r.length + 1) : path;
}

export type Status = typeof T.FileModified | typeof T.FileAdded | typeof T.FileDeleted | typeof T.FileUntracked;

// Statuses are what the daemon says changed (T.FileStatus.changes), read for
// the tree: a file's own, and a folder's, which is "modified" when anything
// under it changed, or "untracked" when git called the whole folder so.
export interface Statuses {
  files: Map<string, Status>;
  folders: Map<string, Status>;
  untrackedFolders: string[];
}

export function statusesOf(changes: Record<string, string> | undefined): Statuses {
  const files = new Map<string, Status>();
  const folders = new Map<string, Status>();
  const untrackedFolders: string[] = [];
  for (const [path, status] of Object.entries(changes ?? {})) {
    let p = path;
    if (p.endsWith('/')) {
      p = p.slice(0, -1);
      untrackedFolders.push(p);
      folders.set(p, T.FileUntracked);
    } else {
      files.set(p, status as Status);
    }
    for (let i = p.lastIndexOf('/'); i > 0; i = p.lastIndexOf('/', i - 1)) {
      const dir = p.slice(0, i);
      if (!folders.has(dir)) folders.set(dir, T.FileModified);
    }
  }
  return { files, folders, untrackedFolders };
}

// statusOf is a path's status in the tree, or undefined when it didn't change.
export function statusOf(s: Statuses, path: string, dir: boolean): Status | undefined {
  const own = dir ? s.folders.get(path) : s.files.get(path);
  if (own) return own;
  return s.untrackedFolders.some((f) => path.startsWith(`${f}/`)) ? T.FileUntracked : undefined;
}

// languageOfPath is the highlighter's language for a file, by its name.
const byExtension: Record<string, string> = {
  mjs: 'javascript',
  cjs: 'javascript',
  mts: 'typescript',
  cts: 'typescript',
  h: 'c',
  hpp: 'cpp',
  cc: 'cpp',
  htm: 'html',
  svg: 'xml',
  plist: 'xml',
  jsonc: 'json',
  json5: 'json',
  conf: 'ini',
  cfg: 'ini',
  env: 'ini',
  mdx: 'markdown',
  gql: 'graphql',
  kts: 'kotlin',
};
export function languageOfPath(path: string): string | undefined {
  const base = path.slice(path.lastIndexOf('/') + 1);
  if (base === 'Dockerfile' || base === 'Containerfile' || base.endsWith('.dockerfile')) return 'dockerfile';
  if (base.startsWith('.env')) return 'ini';
  if (base === 'Makefile') return 'bash';
  const dot = base.lastIndexOf('.');
  if (dot <= 0) return undefined;
  const ext = base.slice(dot + 1).toLowerCase();
  return languageOf(byExtension[ext] ?? ext);
}

export const isMarkdown = (path: string) => /\.(md|mdx|markdown)$/i.test(path);

// formatSize is a file's size for people.
export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

// highlightLimit is the most text the viewer highlights; past it, a file is
// shown plain, as T3Code stops tokenizing past DEFAULT_TOKENIZE_MAX_LENGTH.
export const highlightLimit = 256 * 1024;

// The panel, remembered across restarts: whether it is open, how wide it and
// its tree are, whether ignored files show. Which file each chat has open is
// remembered for the session.
export interface PanelPrefs {
  open: boolean;
  width: number;
  treeWidth: number;
  ignored: boolean;
  tree: boolean; // the tree beside an open file, or the file alone
  splitTreeWidth?: number; // the tree's in UI v2's right-hand panel, beside the file
}

const prefsKey = 'ab.files.panel';
export const panelMin = 320;
export const treeMin = 160;
const defaults: PanelPrefs = { open: false, width: 640, treeWidth: 240, ignored: false, tree: true };

function load(): PanelPrefs {
  try {
    return { ...defaults, ...(JSON.parse(globalThis.localStorage?.getItem(prefsKey) ?? '{}') as Partial<PanelPrefs>) };
  } catch {
    return defaults;
  }
}

let prefs: PanelPrefs | undefined;
const listeners = new Set<() => void>();
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};
const emit = () => {
  for (const l of listeners) l();
};

export function panelPrefs(): PanelPrefs {
  prefs ??= load();
  return prefs;
}

export function setPanelPrefs(change: Partial<PanelPrefs>): void {
  prefs = { ...panelPrefs(), ...change };
  try {
    globalThis.localStorage?.setItem(prefsKey, JSON.stringify(prefs));
  } catch {
    // not kept, still applied
  }
  emit();
}

export function usePanelPrefs(): PanelPrefs {
  return useSyncExternalStore(subscribe, panelPrefs);
}

// panelLayout fits the panel into the width the chat and it share: beside the
// chat while that leaves the chat chatMin, and over it, as a drawer leaving a
// strip of the chat in view, when the panel is wider than that. Dragging it
// wider than the room beside the chat makes it a drawer, narrower puts it back.
export const chatMin = 420;
export function panelLayout(width: number, available: number): { width: number; overlay: boolean } {
  const w = Math.max(Math.min(panelMin, available), width);
  if (w <= available - chatMin) return { width: Math.round(w), overlay: false };
  return { width: Math.round(Math.max(Math.min(panelMin, available), Math.min(w, available - 48))), overlay: true };
}

// narrowPanel is a panel too narrow for the tree and a file side by side: it
// shows one of them.
export const narrowPanel = 560;

// What each chat's viewer has open: a file, at a line. nonce makes opening
// the same place twice scroll there again.
export interface Opened {
  path: string;
  line?: number;
  nonce: number;
}

const opened = new Map<string, Opened | null>();
let nonce = 0;

export function openedFile(ref: string): Opened | null {
  return opened.get(ref) ?? null;
}

// Who else shows the files: UI v2's right-hand panel listens, to open on the
// Files tab for ref (components/v2/RightPanel.tsx).
const showListeners = new Set<(ref: string) => void>();
export function onShowFiles(listener: (ref: string) => void): () => void {
  showListeners.add(listener);
  return () => showListeners.delete(listener);
}

// openFile shows a file in ref's viewer, opening the panel.
export function openFile(ref: string, path: string, line?: number): void {
  opened.set(ref, { path, line, nonce: ++nonce });
  setPanelPrefs({ open: true });
  for (const l of showListeners) l(ref);
}

export function closeFile(ref: string): void {
  opened.set(ref, null);
  emit();
}

export function useOpenedFile(ref: string): Opened | null {
  return useSyncExternalStore(subscribe, () => openedFile(ref));
}

// Ctrl+P asks the visible panel to focus its filter.
let finds = 0;
export function requestFind(ref?: string): void {
  finds++;
  setPanelPrefs({ open: true });
  if (ref) for (const l of showListeners) l(ref);
}
export function useFindRequests(): number {
  return useSyncExternalStore(subscribe, () => finds);
}

// hasFiles says whether a chat has a worktree to show: the Home chat and a
// past agent's don't.
export const hasFiles = (ref: string, readOnly?: boolean) => !readOnly && !!ref && ref.split('/')[0] !== T.HomeProject;

// A row of the tree: an entry at its depth, or a folder still loading, or the
// note that a folder had more entries than were sent.
export interface TreeRow {
  key: string;
  depth: number;
  entry?: T.FileEntry;
  loading?: boolean;
  truncated?: boolean;
}

// treeRows lays out the folders loaded so far: lists by folder path ("" the
// root), the open ones walked into.
export function treeRows(lists: ReadonlyMap<string, T.FileList | undefined>, expanded: ReadonlySet<string>): TreeRow[] {
  const rows: TreeRow[] = [];
  const walk = (dir: string, depth: number) => {
    const list = lists.get(dir);
    if (!list) {
      rows.push({ key: `${dir}\0loading`, depth, loading: true });
      return;
    }
    for (const entry of list.entries) {
      rows.push({ key: entry.path, depth, entry });
      if (entry.dir && expanded.has(entry.path)) walk(entry.path, depth + 1);
    }
    if (list.truncated) rows.push({ key: `${dir}\0more`, depth, truncated: true });
  };
  walk('', 0);
  return rows;
}

// ancestorsOf is the folders a path is in, outermost first.
export function ancestorsOf(path: string): string[] {
  const out: string[] = [];
  for (let i = path.indexOf('/'); i > 0; i = path.indexOf('/', i + 1)) out.push(path.slice(0, i));
  return out;
}

export interface DiffRow {
  sign: '+' | '-' | ' ';
  old?: number;
  new?: number;
  text: string;
}

// diffRows is the whole of a file against its base, line by line, numbered
// on both sides.
export function diffRows(oldText: string, newText: string): DiffRow[] {
  const rows: DiffRow[] = [];
  let o = 1;
  let n = 1;
  for (const part of diffLines(oldText, newText)) {
    const texts = part.value.replace(/\n$/, '').split('\n');
    for (const text of texts) {
      if (part.added) rows.push({ sign: '+', new: n++, text });
      else if (part.removed) rows.push({ sign: '-', old: o++, text });
      else rows.push({ sign: ' ', old: o++, new: n++, text });
    }
  }
  return rows;
}

// changesFiles says a chat item is a tool call that has just finished and may
// have changed files: an edit, a delete, a move or a command.
export function changesFiles(item: T.ChatItem): boolean {
  const tool = item.tool;
  return !!tool && (tool.status === 'completed' || tool.status === 'failed') && ['edit', 'delete', 'move', 'execute'].includes(tool.kind);
}

// refreshFiles reads a chat's open tree, open file and status again after its
// AI tool changed files, a moment after the last such call. The daemon watches
// only the folders the tree has open, so this is what notices an edit in one
// that is closed. Nothing is read when no panel shows that chat's files.
const refreshing = new Map<string, ReturnType<typeof setTimeout>>();
export function refreshFiles(queryClient: QueryClient, ref: string, status: (ref: string, fresh: boolean) => Promise<T.FileStatus>, delay = 400): void {
  if (!queryClient.getQueryCache().find({ queryKey: ['fileStatus', ref] })?.isActive()) return;
  clearTimeout(refreshing.get(ref));
  refreshing.set(
    ref,
    setTimeout(() => {
      refreshing.delete(ref);
      void queryClient.fetchQuery({ queryKey: ['fileStatus', ref], queryFn: () => status(ref, true), staleTime: 0 }).catch(() => {});
      void queryClient.invalidateQueries({ queryKey: ['fileTree', ref] });
      void queryClient.invalidateQueries({ queryKey: ['file', ref] });
    }, delay),
  );
}

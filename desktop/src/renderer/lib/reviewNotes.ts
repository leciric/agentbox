// Notes on an agent's diff, left in the review's Changes view (Orca's diff
// notes are the model): a range of a file's diff lines, quoted, with what the
// user wants changed there. Here is what doesn't need React: which lines a
// selection is, where a note goes back in a diff, and an agent's whole diff
// cut into files.
import type * as T from '../../shared/api';
import type { DiffLine } from './pulls.ts';

// NoteRange is a selection of a file's diff lines as a note keeps it: the
// side its lines are numbered on ("new" unless they're all removed lines),
// the first and last of them, and the code as diff lines.
export type NoteRange = { path: string; side: 'new' | 'old'; start: number; end: number; code: string };

type CodeLine = Extract<DiffLine, { kind: 'add' | 'del' | 'context' }>;

const isCode = (l: DiffLine | undefined): l is CodeLine => !!l && (l.kind === 'add' || l.kind === 'del' || l.kind === 'context');

const sign = { add: '+', del: '-', context: ' ' } as const;

// noteRange is the note rows from to to (either way round) of a file's diff
// make, or null when they hold no line of code.
export function noteRange(path: string, lines: DiffLine[], from: number, to: number): NoteRange | null {
  const [a, b] = from <= to ? [from, to] : [to, from];
  const rows = lines.slice(a, b + 1).filter(isCode);
  if (rows.length === 0) return null;
  const side = rows.every((l) => l.kind === 'del') ? 'old' : 'new';
  const numbers = rows.map((l) => l[side]).filter((n): n is number => n !== undefined);
  return {
    path,
    side,
    start: Math.min(...numbers),
    end: Math.max(...numbers),
    code: rows.map((l) => sign[l.kind] + l.text).join('\n'),
  };
}

// lineOn is a row's line number on a side, if it has one there.
export const lineOn = (l: DiffLine, side: string): number | undefined => (isCode(l) ? (side === 'old' ? l.old : l.new) : undefined);

// noteAt is the row a note shows under in a file's diff: its last line, if
// the diff still has the code the note quotes there. A note sent on an
// earlier round whose lines moved isn't put on whatever is there now.
export function noteAt(lines: DiffLine[], note: Pick<T.ReviewNote, 'side' | 'start' | 'end' | 'code'>): number {
  const i = lines.findIndex((l) => lineOn(l, note.side) === note.end);
  if (i < 0) return -1;
  const quoted = note.code
    .split('\n')
    .filter((l) => (note.side === 'old' ? l[0] === '-' || l[0] === ' ' : l[0] === '+' || l[0] === ' '))
    .map((l) => l.slice(1));
  const last = quoted[quoted.length - 1];
  return last === undefined || last === lines[i].text ? i : -1;
}

// inNote is whether a row is one of a note's lines.
export function inNote(line: DiffLine, note: Pick<T.ReviewNote, 'side' | 'start' | 'end'>): boolean {
  const n = lineOn(line, note.side);
  return n !== undefined && n >= note.start && n <= note.end;
}

// whereLabel is a note's lines as the app says them, for t().
export function whereLabel(note: Pick<T.ReviewNote, 'side' | 'start' | 'end'>): { key: 'review.notes.line' | 'review.notes.lines' | 'review.notes.removedLine' | 'review.notes.removedLines'; start: number; end: number } {
  const one = note.start === note.end;
  if (note.side === 'old') return { key: one ? 'review.notes.removedLine' : 'review.notes.removedLines', start: note.start, end: note.end };
  return { key: one ? 'review.notes.line' : 'review.notes.lines', start: note.start, end: note.end };
}

// FileDiffText is one file of a whole diff: its path and its hunks, or
// none for a binary file or one only renamed.
export type FileDiffText = { path: string; patch: string };

// splitGitDiff cuts `git diff` output into its files, each from its first
// hunk, without the headers parsePatch would read as lines.
export function splitGitDiff(text: string): FileDiffText[] {
  const out: { path: string; lines: string[]; inHunks: boolean }[] = [];
  for (const line of text.split('\n')) {
    const head = /^diff --git a\/(.*) b\/(.*)$/.exec(line);
    if (head) {
      out.push({ path: head[2], lines: [], inHunks: false });
      continue;
    }
    const file = out[out.length - 1];
    if (!file) continue;
    if (line.startsWith('@@')) file.inHunks = true;
    if (file.inHunks) file.lines.push(line);
    else {
      const to = /^\+\+\+ b\/(.*)$/.exec(line);
      if (to) file.path = to[1];
    }
  }
  return out.map((f) => ({ path: f.path, patch: f.lines.join('\n').replace(/\n$/, '') }));
}

// drafts are the notes not sent yet, and sent the rest.
export const drafts = (notes: readonly T.ReviewNote[]) => notes.filter((n) => n.status === 'draft');
export const sent = (notes: readonly T.ReviewNote[]) => notes.filter((n) => n.status !== 'draft');

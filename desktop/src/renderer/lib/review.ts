// "Ready to review": the agents that finished and the user hasn't looked at
// since (the daemon's internal/daemon/review.go decides which; opening one in
// the review marks it reviewed), and the carousel of everything one of them
// made.
import type * as T from '../../shared/api';
import { recordedFor, videoAt } from './recording.ts';

// Proof is what an agent showed it checked, for the review's Proof tab:
// its recordings, the latest first, each in chapters; its screenshots,
// oldest first; and its Hatch pages.
export interface Proof {
  recordings: Take[];
  screenshots: T.MediaItem[];
  pages: T.Artifact[];
}

// A take is one recording and the chapters in it.
export interface Take {
  media: T.MediaItem;
  chapters: Chapter[];
}

// A chapter is one thing an agent checked while it recorded: a note it saved
// (`agentbox media note`) or, in a recording with no notes, a screenshot it
// took, at seconds into the recording. shots are the screenshots taken
// during it.
export interface Chapter {
  at: number;
  title: string;
  detail?: string;
  shots: T.MediaItem[];
}

const ms = (iso: string) => new Date(iso).getTime();

// proofOf sorts an agent's media into its proof. A recording's item is made
// when it stops but dated when it started (internal/agent/media.go), so what
// was saved between that and its duration later happened on screen. Logs,
// reports and files aren't proof to look at; notes are only chapters.
export function proofOf(media: readonly T.MediaItem[], pages: readonly T.Artifact[]): Proof {
  const byTime = [...media].sort((a, b) => ms(a.createdAt) - ms(b.createdAt));
  const screenshots = byTime.filter((m) => m.kind === 'screenshot');
  const notes = byTime.filter((m) => m.kind === 'note');
  const recordings = byTime
    .filter((m) => m.kind === 'recording')
    .reverse()
    .map((rec) => ({ media: rec, chapters: chaptersOf(rec, notes, screenshots) }));
  return { recordings, screenshots, pages: [...pages].sort((a, b) => ms(a.createdAt) - ms(b.createdAt)) };
}

// A shown item is one piece of proof the review's gallery steps through:
// a recording, a screenshot or a page.
export type Shown = { kind: 'recording'; take: Take } | { kind: 'screenshot'; media: T.MediaItem } | { kind: 'page'; page: T.Artifact };

// gallery is every piece of proof in the order the review shows it: the
// recordings, the latest first, then the screenshots and the pages, oldest
// first. ← → step through it, whatever the kind.
export function gallery(proof: Proof): Shown[] {
  return [
    ...proof.recordings.map((take): Shown => ({ kind: 'recording', take })),
    ...proof.screenshots.map((media): Shown => ({ kind: 'screenshot', media })),
    ...proof.pages.map((page): Shown => ({ kind: 'page', page })),
  ];
}

function chaptersOf(rec: T.MediaItem, notes: readonly T.MediaItem[], screenshots: readonly T.MediaItem[]): Chapter[] {
  const start = ms(rec.createdAt);
  const length = recordedFor(rec.meta) * 1000;
  const during = (m: T.MediaItem) => ms(m.createdAt) >= start && ms(m.createdAt) <= start + length;
  // Where it was made, on the video's timeline, which a trimmed recording's
  // cuts make shorter than the wall clock's.
  const at = (m: T.MediaItem) => videoAt(rec.meta, (ms(m.createdAt) - start) / 1000);
  const shots = screenshots.filter(during);
  const marks = notes.filter(during);
  const chapters: Chapter[] = marks.length
    ? marks.map((n) => ({ at: at(n), title: n.name, detail: noteDetail(n), shots: [] }))
    : shots.map((s) => ({ at: at(s), title: readableName(s.name), shots: [] }));
  // Each screenshot belongs to the chapter it was taken in; one taken before
  // the first note, to the first.
  for (const s of shots) {
    const i = chapters.findLastIndex((c) => c.at <= at(s));
    chapters[Math.max(0, i)]?.shots.push(s);
  }
  return chapters;
}

// noteDetail is what a note says past its title, when it says more.
function noteDetail(n: T.MediaItem): string | undefined {
  const text = (n.text ?? '').trim();
  if (!text || text === n.name) return undefined;
  const rest = text.startsWith(n.name) ? text.slice(n.name.length).trim() : text;
  return rest || undefined;
}

// readableName turns a media name like "settings-at-1024" into words.
export function readableName(name: string): string {
  const words = name.replace(/\.[a-z0-9]+$/i, '').replace(/[-_]+/g, ' ').trim();
  return words ? words[0].toUpperCase() + words.slice(1) : name;
}

// chapterAt is the index of the chapter playing at t seconds.
export function chapterAt(chapters: readonly Chapter[], t: number): number {
  return Math.max(0, chapters.findLastIndex((c) => c.at <= t + 0.05));
}

// step moves through a carousel of count slides, stopping at either end.
export function step(index: number, count: number, by: number): number {
  if (count <= 0) return 0;
  return Math.min(count - 1, Math.max(0, index + by));
}

// countLabel is a button's count, capped so it stays a badge.
export const countLabel = (n: number) => (n > 99 ? '99+' : String(n));

// MergeBlock is why a pull request can't be merged from the review: it
// isn't open any more, it's a draft, its checks fail, it conflicts with its
// base, or the project's account may not merge in this repository.
export type MergeBlock = 'merged' | 'closed' | 'draft' | 'failing' | 'conflict' | 'noPermission';

// mergeBlock is what stops pr being merged, or null when it can be: the
// confirmation still warns about checks that haven't finished.
export function mergeBlock(pr: T.PullRequest, pulls?: Pick<T.ProjectPullRequests, 'canMergeKnown' | 'canMerge'>): MergeBlock | null {
  if (pr.state === 'merged') return 'merged';
  if (pr.state !== 'open') return 'closed';
  if (pr.draft) return 'draft';
  if (pulls?.canMergeKnown && !pulls.canMerge) return 'noPermission';
  if (pr.conflict) return 'conflict';
  if (pr.checks === 'failing') return 'failing';
  return null;
}

// queueOf is the order "Review all" walks: the agents waiting when it began,
// then any that arrived since. An agent reviewed on the way stays in it, so
// the position doesn't jump.
export function queueOf(order: readonly string[], live: readonly T.ReviewItem[]): string[] {
  const seen = new Set(order);
  return [...order, ...live.map((i) => i.ref).filter((r) => !seen.has(r))];
}

// nextPending is the agent after current in the walk that still waits for
// review, or undefined when current was the last.
export function nextPending(order: readonly string[], current: string, live: readonly T.ReviewItem[]): T.ReviewItem | undefined {
  const queue = queueOf(order, live);
  const byRef = new Map(live.map((i) => [i.ref, i]));
  for (const ref of queue.slice(queue.indexOf(current) + 1)) {
    const item = byRef.get(ref);
    if (item && ref !== current) return item;
  }
  return undefined;
}

// remainingAfter is what still waits for review once current is done, in
// the walk's order, then the rest: the agents skipped with Next or Back
// and those that arrived on the way. Empty only when nothing is left.
export function remainingAfter(order: readonly string[], current: string, live: readonly T.ReviewItem[]): T.ReviewItem[] {
  const byRef = new Map(live.map((i) => [i.ref, i]));
  return queueOf(order, live).flatMap((ref) => (ref !== current && byRef.has(ref) ? [byRef.get(ref) as T.ReviewItem] : []));
}

// wrapWalk is the walk carried on from its end to the first of remaining.
export const wrapWalk = (walk: Walk, remaining: readonly T.ReviewItem[]): Walk => ({
  ...goForward(walk, remaining[0]),
  order: remaining.map((i) => i.ref),
});

// ReviewAction is what a key does in the review. merge, reviewed and
// requestChanges are the verdicts (each goes on to the next agent waiting);
// nextItem and previousItem move through the walk without one.
export type ReviewAction =
  | 'merge'
  | 'requestChanges'
  | 'reviewed'
  | 'nextItem'
  | 'previousItem'
  | 'openPr'
  | 'toggleDiff'
  | 'toggleDetails'
  | 'nextFile'
  | 'previousFile'
  | 'previousMedia'
  | 'nextMedia'
  | 'play'
  | 'previousChapter'
  | 'nextChapter'
  | 'expand'
  | 'help';

// The keys, as the review's ? overlay lists them: the verdicts, then the
// proof, then the diff and the rest. Letters, as GitHub's and Gmail's are, so
// a hand on the keys never leaves them; none fires while typing
// (ReviewCarousel). keys is what the overlay shows, match the KeyboardEvent
// keys that stand for it, when they differ.
export const reviewKeys: readonly { keys: readonly string[]; match?: readonly string[]; action: ReviewAction }[] = [
  { keys: ['M'], action: 'merge' },
  { keys: ['C'], action: 'requestChanges' },
  { keys: ['R'], action: 'reviewed' },
  { keys: ['N'], action: 'nextItem' },
  { keys: ['P'], action: 'previousItem' },
  { keys: ['←'], match: ['ArrowLeft'], action: 'previousMedia' },
  { keys: ['→'], match: ['ArrowRight'], action: 'nextMedia' },
  { keys: ['Space'], match: [' '], action: 'play' },
  { keys: ['['], action: 'previousChapter' },
  { keys: [']'], action: 'nextChapter' },
  { keys: ['F'], action: 'expand' },
  { keys: ['D'], action: 'toggleDiff' },
  { keys: ['J'], action: 'nextFile' },
  { keys: ['K'], action: 'previousFile' },
  { keys: ['I'], action: 'toggleDetails' },
  { keys: ['O'], action: 'openPr' },
  { keys: ['?'], action: 'help' },
];

const byKey = new Map(reviewKeys.flatMap((k) => (k.match ?? k.keys).map((key) => [key.toLowerCase(), k.action] as const)));

// reviewShortcut is the action a key press stands for in the review, or
// null: none with Ctrl, Alt or Cmd held, which belong to the app and the
// system. Shift is allowed: ? is Shift+/ on most layouts.
export function reviewShortcut(e: { key: string; ctrlKey?: boolean; altKey?: boolean; metaKey?: boolean }): ReviewAction | null {
  if (e.ctrlKey || e.altKey || e.metaKey) return null;
  return byKey.get(e.key.toLowerCase()) ?? null;
}

// proofKey is whether action works the proof's gallery, which only the proof
// pane takes: in the diff, the arrows scroll.
export const proofKey = (action: ReviewAction) =>
  action === 'previousMedia' || action === 'nextMedia' || action === 'play' || action === 'previousChapter' || action === 'nextChapter' || action === 'expand';

// repeatable is whether holding a key down may act again: moving through
// files and agents may, a verdict or a toggle may not.
export const repeatable = (action: ReviewAction) =>
  action === 'nextFile' || action === 'previousFile' || action === 'nextItem' || action === 'previousItem' || action === 'previousMedia' || action === 'nextMedia' || action === 'previousChapter' || action === 'nextChapter';

// isTyping is whether a key press belongs to what has focus rather than to
// the review: a field, an editable element, or a menu's typeahead.
export function isTyping(target: { tagName?: string; isContentEditable?: boolean; closest?: (selector: string) => unknown } | null): boolean {
  if (!target) return false;
  if (target.isContentEditable) return true;
  const tag = target.tagName?.toUpperCase();
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
  return !!target.closest?.('[role="menu"],[role="listbox"],[role="combobox"]');
}

// pressable is whether Space belongs to what has focus: a button, a link
// or a control presses on Space, so the review's play key leaves it be.
export function pressable(target: { tagName?: string; closest?: (selector: string) => unknown } | null): boolean {
  return !!target?.closest?.('button,a[href],summary,[role="button"],[role="tab"],[role="radio"],[role="checkbox"],[role="switch"]');
}

// firstLine is a summary cut to its first line, without Markdown's heading
// and list marks, for the review's header.
export function firstLine(text: string | undefined): string {
  const line = (text ?? '').split('\n').map((l) => l.trim()).find((l) => l && !/^[-*_]{3,}$/.test(l)) ?? '';
  return line.replace(/^(#{1,6}\s+|[-*+]\s+|\d+\.\s+|>\s*)/, '').replace(/\*\*|__|`/g, '');
}

// walkItems is what a walk goes through: every agent waiting, or one
// project's (openReview({ project })), so its end is that project's last.
export const walkItems = (items: readonly T.ReviewItem[], project?: string): T.ReviewItem[] => (project ? items.filter((i) => i.project === project) : [...items]);

// Walk is where a review walk starts: on one agent, or the first of every
// agent waiting (or of one project's), with the order to go through.
// trail is the agents shown before it, the last one first to go back to,
// reviewed on the way or not.
export type Walk = { item: T.ReviewItem; order: string[] | null; project?: string; trail?: T.ReviewItem[] };

// walkOrder is the order a walk goes through: its own, or for a review
// opened on one agent (an agent's card, the Reviewed tab), that agent and
// then whatever else waits, so Merge and Reviewed go on to the next.
export const walkOrder = (walk: Walk): string[] => walk.order ?? [walk.item.ref];

// goForward is the walk moved on to item, remembering where it was.
export const goForward = (walk: Walk, item: T.ReviewItem): Walk => ({ ...walk, item, trail: [...(walk.trail ?? []), walk.item] });

// goBack is the walk returned to the agent shown just before, or itself when
// there was none.
export function goBack(walk: Walk): Walk {
  const trail = walk.trail ?? [];
  return trail.length ? { ...walk, item: trail[trail.length - 1], trail: trail.slice(0, -1) } : walk;
}

// startWalk is openReview's walk: on the agent asked for (the one a card
// shows: it opens on it, whatever the lists say, and goes on to its
// project's others), else through the waiting
// items, one project's when asked; null when there is nothing to show.
export function startWalk(waiting: readonly T.ReviewItem[], opts: { project?: string; item?: T.ReviewItem } = {}): Walk | null {
  if (opts.item) return { item: opts.item, order: null, project: opts.item.project };
  const items = walkItems(waiting, opts.project);
  return items.length ? { item: items[0], order: items.map((i) => i.ref), project: opts.project } : null;
}

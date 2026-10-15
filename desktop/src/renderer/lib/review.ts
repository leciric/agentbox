// "Ready to review": the agents that finished and the user hasn't looked at
// since (the daemon's internal/daemon/review.go decides which; opening one in
// the review marks it reviewed), and the carousel of everything one of them
// made.
import type * as T from '../../shared/api';

// A slide is one thing an agent made, in the order it made them, and its
// pull request last.
export type Slide =
  | { kind: 'screenshot' | 'recording'; id: string; at: string; media: T.MediaItem }
  | { kind: 'page'; id: string; at: string; page: T.Artifact }
  | { kind: 'pr'; id: string; pr: T.PullRequest };

// slidesOf is an agent's carousel: its screenshots, recordings and Hatch
// pages, oldest first, then its pull request when it opened one. Other
// media (notes, logs, reports) isn't something to look at in a carousel.
export function slidesOf(media: readonly T.MediaItem[], pages: readonly T.Artifact[], pr?: T.PullRequest): Slide[] {
  const made: Exclude<Slide, { kind: 'pr' }>[] = [
    ...media
      .filter((m) => m.kind === 'screenshot' || m.kind === 'recording')
      .map((m) => ({ kind: m.kind as 'screenshot' | 'recording', id: m.id, at: m.createdAt, media: m })),
    ...pages.map((p) => ({ kind: 'page' as const, id: p.id, at: p.createdAt, page: p })),
  ];
  made.sort((a, b) => new Date(a.at).getTime() - new Date(b.at).getTime());
  return pr ? [...made, { kind: 'pr', id: `pr-${pr.number}`, pr }] : made;
}

// step moves through a carousel of count slides, stopping at either end.
export function step(index: number, count: number, by: number): number {
  if (count <= 0) return 0;
  return Math.min(count - 1, Math.max(0, index + by));
}

// swipeStep is how far a swipe of (dx, dy) pixels moves: a mostly
// sideways drag of at least minSwipe goes to the next slide leftwards, the
// previous one rightwards, and anything else stays put.
export const minSwipe = 50;
export function swipeStep(dx: number, dy: number): number {
  if (Math.abs(dx) < minSwipe || Math.abs(dx) < Math.abs(dy)) return 0;
  return dx < 0 ? 1 : -1;
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

// ReviewAction is what a key does in the review.
export type ReviewAction = 'previous' | 'next' | 'first' | 'last' | 'nextAgent' | 'previousAgent' | 'merge' | 'requestChanges';

// The keys, as the review lists them under the slides.
export const reviewKeys: readonly { keys: readonly string[]; action: ReviewAction }[] = [
  { keys: ['←', '→'], action: 'next' },
  { keys: ['N'], action: 'nextAgent' },
  { keys: ['P'], action: 'previousAgent' },
  { keys: ['C'], action: 'requestChanges' },
  { keys: ['M'], action: 'merge' },
];

// reviewShortcut is the action a key press stands for in the review, or
// null: none with Ctrl, Alt or Cmd held, which belong to the app and the
// system.
export function reviewShortcut(e: { key: string; ctrlKey?: boolean; altKey?: boolean; metaKey?: boolean }): ReviewAction | null {
  if (e.ctrlKey || e.altKey || e.metaKey) return null;
  switch (e.key) {
    case 'ArrowLeft':
      return 'previous';
    case 'ArrowRight':
      return 'next';
    case 'Home':
      return 'first';
    case 'End':
      return 'last';
    case 'n':
    case 'N':
      return 'nextAgent';
    case 'p':
    case 'P':
      return 'previousAgent';
    case 'c':
    case 'C':
      return 'requestChanges';
    case 'm':
    case 'M':
      return 'merge';
    default:
      return null;
  }
}

// walkItems is what a walk goes through: every agent waiting, or one
// project's (openReview({ project })), so its end is that project's last.
export const walkItems = (items: readonly T.ReviewItem[], project?: string): T.ReviewItem[] => (project ? items.filter((i) => i.project === project) : [...items]);

// Walk is where a review walk starts: on one agent, or the first of every
// agent waiting (or of one project's), with the order to go through.
// trail is the agents shown before it, the last one first to go back to,
// reviewed on the way or not.
export type Walk = { item: T.ReviewItem; order: string[] | null; project?: string; trail?: T.ReviewItem[] };

// goForward is the walk moved on to item, remembering where it was.
export const goForward = (walk: Walk, item: T.ReviewItem): Walk => ({ ...walk, item, trail: [...(walk.trail ?? []), walk.item] });

// goBack is the walk returned to the agent shown just before, or itself when
// there was none.
export function goBack(walk: Walk): Walk {
  const trail = walk.trail ?? [];
  return trail.length ? { ...walk, item: trail[trail.length - 1], trail: trail.slice(0, -1) } : walk;
}

// startWalk is openReview's walk: on the agent asked for (the one a card
// shows: it opens on it, whatever the lists say), else through the waiting
// items, one project's when asked; null when there is nothing to show.
export function startWalk(waiting: readonly T.ReviewItem[], opts: { project?: string; item?: T.ReviewItem } = {}): Walk | null {
  if (opts.item) return { item: opts.item, order: null };
  const items = walkItems(waiting, opts.project);
  return items.length ? { item: items[0], order: items.map((i) => i.ref), project: opts.project } : null;
}

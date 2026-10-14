// "Ready to review": the agents that finished and the user hasn't reviewed or
// dismissed since (the daemon's internal/daemon/review.go decides which), and
// the carousel of everything one of them made.
import type * as T from '../../shared/api';

export const reviewKey = (project?: string) => (project ? (['review', project] as const) : (['review'] as const));

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

// withoutAgent is the list with one agent taken off, as a verdict does
// before the daemon's answer comes back.
export const withoutAgent = (list: T.ReviewItem[] | undefined, ref: string) => list?.filter((i) => i.ref !== ref);

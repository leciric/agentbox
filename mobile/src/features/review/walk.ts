// The order the phone's review goes through, as the desktop's (lib/review.ts,
// #322): the agents waiting when it began, then any that arrived since. An
// agent reviewed on the way stays in the order, so the position doesn't
// jump, and Back returns to the one shown just before.
import type * as T from '../../shared/api.ts';

export type Walk = { item: T.ReviewItem; order: string[]; trail: T.ReviewItem[] };

export const startWalk = (items: readonly T.ReviewItem[]): Walk | null => (items.length ? { item: items[0], order: items.map((i) => i.ref), trail: [] } : null);

export function queueOf(order: readonly string[], live: readonly T.ReviewItem[]): string[] {
  const seen = new Set(order);
  return [...order, ...live.map((i) => i.ref).filter((r) => !seen.has(r))];
}

// nextPending is the agent after the walk's, in the order, that still waits.
export function nextPending(walk: Walk, live: readonly T.ReviewItem[]): T.ReviewItem | undefined {
  const queue = queueOf(walk.order, live);
  const byRef = new Map(live.map((i) => [i.ref, i]));
  for (const ref of queue.slice(queue.indexOf(walk.item.ref) + 1)) {
    const item = byRef.get(ref);
    if (item && ref !== walk.item.ref) return item;
  }
  return undefined;
}

// remainingAfter is what still waits besides the walk's agent: those skipped
// with Next or Back and those that arrived on the way.
export function remainingAfter(walk: Walk, live: readonly T.ReviewItem[]): T.ReviewItem[] {
  const byRef = new Map(live.map((i) => [i.ref, i]));
  return queueOf(walk.order, live).flatMap((ref) => (ref !== walk.item.ref && byRef.has(ref) ? [byRef.get(ref) as T.ReviewItem] : []));
}

export const goForward = (walk: Walk, item: T.ReviewItem): Walk => ({ ...walk, item, trail: [...walk.trail, walk.item] });

export function goBack(walk: Walk): Walk {
  return walk.trail.length ? { ...walk, item: walk.trail[walk.trail.length - 1], trail: walk.trail.slice(0, -1) } : walk;
}

// advance is the walk after its agent is done or skipped: on the next one
// waiting, else wrapped to the first of what was skipped, else null (all done).
export function advance(walk: Walk, live: readonly T.ReviewItem[]): Walk | null {
  const next = nextPending(walk, live);
  if (next) return goForward(walk, next);
  const left = remainingAfter(walk, live);
  return left.length ? { ...goForward(walk, left[0]), order: left.map((i) => i.ref) } : null;
}

// position is "n of count" in the walk's queue.
export function position(walk: Walk, live: readonly T.ReviewItem[]): { n: number; count: number } {
  const queue = queueOf(walk.order, live);
  return { n: queue.indexOf(walk.item.ref) + 1, count: queue.length };
}

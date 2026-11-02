// Before/after pairs: two screenshots, or two recordings, of the same thing,
// one taken before a change and one after, which the review shows together
// to compare (the desktop's ReviewCompare, the phone's ReviewScreen). An item
// says it is one half with `--pair <name> --before|--after` (meta.pair and
// meta.side), or by its name alone: "settings-dark-before", "after_settings".
import type * as T from './api.ts';

export type Side = 'before' | 'after';

type Pairable = Pick<T.MediaItem, 'id' | 'name' | 'kind' | 'createdAt'> & { meta?: Pick<T.MediaMeta, 'pair' | 'side'> };

// A pair is the before and the after of one thing, name being what both show.
export interface Pair<M extends Pairable = T.MediaItem> {
  key: string;
  name: string;
  before: M;
  after: M;
}

const sideInName = /^(?:(before|after)[-_ ]+(.+)|(.+?)[-_ ]+(before|after))$/i;

// sideOf is which half of which pair an item is, or null for one that isn't
// in any: its own meta first, then its name.
export function sideOf(m: Pairable): { pair: string; side: Side } | null {
  const side = m.meta?.side?.toLowerCase();
  if (m.meta?.pair && (side === 'before' || side === 'after')) return { pair: m.meta.pair, side };
  const match = sideInName.exec(m.name.trim().replace(/\.(png|jpe?g|webp|mp4|webm)$/i, ''));
  if (!match) return null;
  return match[1] ? { pair: match[2], side: match[1].toLowerCase() as Side } : { pair: match[3], side: match[4].toLowerCase() as Side };
}

// A pair's halves are of one kind, a screenshot's after never a recording.
const keyOf = (kind: string, pair: string) => `${kind}:${pair.trim().toLowerCase().replace(/[-_\s]+/g, '-')}`;

const ms = (iso: string) => new Date(iso).getTime();

// pairUp sorts items into pairs, in the order their first half was taken,
// and the rest, in the order they came. An item retaken (the same pair's
// same side twice) pairs by its latest take; the earlier ones are the rest.
// A half without its other half is the rest too.
export function pairUp<M extends Pairable>(items: readonly M[]): { pairs: Pair<M>[]; rest: M[] } {
  const groups = new Map<string, { name: string; before?: M; after?: M }>();
  for (const m of items) {
    const s = sideOf(m);
    if (!s) continue;
    const key = keyOf(m.kind, s.pair);
    const g = groups.get(key) ?? { name: s.pair };
    const had = g[s.side];
    if (!had || ms(m.createdAt) >= ms(had.createdAt)) g[s.side] = m;
    groups.set(key, g);
  }
  const pairs: Pair<M>[] = [];
  for (const [key, g] of groups) if (g.before && g.after) pairs.push({ key, name: g.name, before: g.before, after: g.after });
  pairs.sort((a, b) => Math.min(ms(a.before.createdAt), ms(a.after.createdAt)) - Math.min(ms(b.before.createdAt), ms(b.after.createdAt)));
  const paired = new Set(pairs.flatMap((p) => [p.before.id, p.after.id]));
  return { pairs, rest: items.filter((m) => !paired.has(m.id)) };
}

// inPairOrder is items with each pair's halves together, before then after,
// the pairs first in the order they were taken, then everything else as it
// came: the order a list of media shows them in.
export function inPairOrder<M extends Pairable>(items: readonly M[]): M[] {
  const { pairs, rest } = pairUp(items);
  return [...pairs.flatMap((p) => [p.before, p.after]), ...rest];
}

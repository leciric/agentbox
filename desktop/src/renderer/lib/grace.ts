// A grace before something stops counting: UI v2's sidebar keeps a project
// working for graceMs after the last sign of work, so the gaps a busy project
// goes through (a lead's turn ending as the next queued one starts, a
// permission asked mid-turn, an agent finishing before the review list says
// it is to review) don't drop it to the Idle list and bring it back a moment
// later, which is what made working projects flicker.
import { useEffect, useMemo, useRef, useState } from 'react';

export const graceMs = 15_000;

// hold is the names seen within grace of at, now included, with seen the
// last time each was: it records now's in it and forgets the expired ones.
// until is when the next of those not in now expires, to look again then.
export function hold(seen: Map<string, number>, now: Iterable<string>, at: number, grace = graceMs): { held: Set<string>; until?: number } {
  const present = new Set(now);
  for (const name of present) seen.set(name, at);
  const held = new Set<string>();
  let until: number | undefined;
  for (const [name, last] of seen) {
    const end = last + grace;
    if (end <= at) {
      seen.delete(name);
      continue;
    }
    held.add(name);
    if (!present.has(name) && (until === undefined || end < until)) until = end;
  }
  return { held, until };
}

// useGrace is names with a grace: one stays in for grace after it left, and
// the set is the same object while what it holds is.
export function useGrace(names: ReadonlySet<string>, grace = graceMs): ReadonlySet<string> {
  const seen = useRef(new Map<string, number>());
  const [, wake] = useState(0);
  const { held, until } = hold(seen.current, names, Date.now(), grace);
  useEffect(() => {
    if (until === undefined) return;
    const timer = setTimeout(() => wake((n) => n + 1), Math.max(0, until - Date.now()) + 10);
    return () => clearTimeout(timer);
  }, [until]);
  const key = [...held].sort().join('\n');
  return useMemo(() => new Set(key ? key.split('\n') : []), [key]);
}

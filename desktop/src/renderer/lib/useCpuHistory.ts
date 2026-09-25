import { useRef } from 'react';
import type * as T from '../../shared/api';

// useCpuHistory keeps the last ~20 CPU samples per agent, so a sparkline can
// show a trend instead of a single flat number. Mutating the ref during render
// (guarded by object identity) is safe: it never affects this render's output,
// only what later renders read.
export function useCpuHistory(usage: T.Usage | undefined): Map<string, number[]> {
  const store = useRef(new Map<string, number[]>());
  const seen = useRef<T.Usage | undefined>(undefined);
  if (usage && usage !== seen.current) {
    seen.current = usage;
    for (const u of usage.agents) {
      const samples = store.current.get(u.ref) ?? [];
      samples.push(u.cpu);
      if (samples.length > 20) samples.shift();
      store.current.set(u.ref, samples);
    }
  }
  return store.current;
}

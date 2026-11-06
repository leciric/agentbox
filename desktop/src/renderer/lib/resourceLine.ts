// The Resources view's lines (components/v2/AgentResources.tsx).

// runs splits a series into the stretches sampled without a break: two points
// more than gapMs apart weren't watched in between.
export function runs(values: [string, number][], start: number, windowMs: number, gapMs = 6_000): [number, number][][] {
  const out: [number, number][][] = [];
  let prev = -Infinity;
  for (const [at, v] of values) {
    const ms = new Date(at).getTime();
    if (ms < start || ms > start + windowMs) continue;
    if (ms - prev > gapMs || out.length === 0) out.push([]);
    out[out.length - 1].push([ms, v]);
    prev = ms;
  }
  return out.filter((r) => r.length > 1);
}

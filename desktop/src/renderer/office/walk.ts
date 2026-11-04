// How you get around the Office: the keys move you and you slide along what
// you bump into rather than stop dead, and a click walks you there round the
// desks. Boxes here are already grown by your radius (layout.walkable), so the
// walker is a point. Pure, with tests; the scene calls it every frame.
import type { Box } from './layout.ts';

export type P = { x: number; z: number };

const inside = (b: Box, p: P) => p.x > b.x0 && p.x < b.x1 && p.z > b.z0 && p.z < b.z1;
export const blocked = (boxes: readonly Box[], room: Box, p: P) =>
  p.x < room.x0 || p.x > room.x1 || p.z < room.z0 || p.z > room.z1 || boxes.some((b) => inside(b, p));

// slide moves from p by d, one axis at a time, so a move into a desk's side
// keeps the part of it along the side. room is the walkable floor, already
// shrunk by the radius.
export function slide(boxes: readonly Box[], room: Box, p: P, d: P): P {
  let x = p.x;
  let z = p.z;
  if (!blocked(boxes, room, { x: x + d.x, z })) x += d.x;
  if (!blocked(boxes, room, { x, z: z + d.z })) z += d.z;
  return { x, z };
}

// clear is whether the straight line from a to b crosses nothing, sampled
// finer than the thinnest obstacle.
export function clear(boxes: readonly Box[], room: Box, a: P, b: P, step = 0.1): boolean {
  const len = Math.hypot(b.x - a.x, b.z - a.z);
  const n = Math.max(1, Math.ceil(len / step));
  for (let i = 1; i <= n; i++) {
    const t = i / n;
    if (blocked(boxes, room, { x: a.x + (b.x - a.x) * t, z: a.z + (b.z - a.z) * t })) return false;
  }
  return true;
}

const CELL = 0.25;

// path is a walk from a to b round the obstacles: a grid search (A*, eight
// ways), then the corners it can cut cut, so you walk in straight lines from
// one desk's corner to the next. A goal inside something is moved to the free
// floor nearest it; none reachable is an empty path.
export function path(boxes: readonly Box[], room: Box, a: P, b: P): P[] {
  if (clear(boxes, room, a, b)) return [b];
  const cols = Math.max(1, Math.ceil((room.x1 - room.x0) / CELL));
  const rows = Math.max(1, Math.ceil((room.z1 - room.z0) / CELL));
  const centre = (i: number): P => ({ x: room.x0 + ((i % cols) + 0.5) * CELL, z: room.z0 + (Math.floor(i / cols) + 0.5) * CELL });
  const cellOf = (p: P) => {
    const cx = Math.min(cols - 1, Math.max(0, Math.floor((p.x - room.x0) / CELL)));
    const cz = Math.min(rows - 1, Math.max(0, Math.floor((p.z - room.z0) / CELL)));
    return cz * cols + cx;
  };
  const free = new Uint8Array(cols * rows);
  for (let i = 0; i < free.length; i++) free[i] = blocked(boxes, room, centre(i)) ? 0 : 1;

  const nearestFree = (from: number): number => {
    if (free[from]) return from;
    // Rings outwards from the cell, the first free one.
    const fx = from % cols;
    const fz = Math.floor(from / cols);
    for (let r = 1; r < Math.max(cols, rows); r++) {
      let best = -1;
      let bestD = Infinity;
      for (let dz = -r; dz <= r; dz++)
        for (let dx = -r; dx <= r; dx++) {
          if (Math.max(Math.abs(dx), Math.abs(dz)) !== r) continue;
          const x = fx + dx;
          const z = fz + dz;
          if (x < 0 || z < 0 || x >= cols || z >= rows || !free[z * cols + x]) continue;
          const d = dx * dx + dz * dz;
          if (d < bestD) {
            bestD = d;
            best = z * cols + x;
          }
        }
      if (best >= 0) return best;
    }
    return -1;
  };
  const start = nearestFree(cellOf(a));
  const goalCell = nearestFree(cellOf(b));
  if (start < 0 || goalCell < 0) return [];
  const goal = free[cellOf(b)] && !blocked(boxes, room, b) ? b : centre(goalCell);

  const g = new Float32Array(cols * rows).fill(Infinity);
  const from = new Int32Array(cols * rows).fill(-1);
  const closed = new Uint8Array(cols * rows);
  const gx = goalCell % cols;
  const gz = Math.floor(goalCell / cols);
  const h = (i: number) => {
    const dx = Math.abs((i % cols) - gx);
    const dz = Math.abs(Math.floor(i / cols) - gz);
    return Math.max(dx, dz) + (Math.SQRT2 - 1) * Math.min(dx, dz);
  };
  // A binary heap of [f, cell].
  const heap: [number, number][] = [];
  const push = (f: number, i: number) => {
    heap.push([f, i]);
    let k = heap.length - 1;
    while (k > 0) {
      const up = (k - 1) >> 1;
      if (heap[up][0] <= heap[k][0]) break;
      [heap[up], heap[k]] = [heap[k], heap[up]];
      k = up;
    }
  };
  const pop = (): number => {
    const top = heap[0][1];
    const last = heap.pop()!;
    if (heap.length) {
      heap[0] = last;
      let k = 0;
      for (;;) {
        const l = k * 2 + 1;
        const r = l + 1;
        let m = k;
        if (l < heap.length && heap[l][0] < heap[m][0]) m = l;
        if (r < heap.length && heap[r][0] < heap[m][0]) m = r;
        if (m === k) break;
        [heap[m], heap[k]] = [heap[k], heap[m]];
        k = m;
      }
    }
    return top;
  };
  g[start] = 0;
  push(h(start), start);
  let found = false;
  while (heap.length) {
    const i = pop();
    if (closed[i]) continue;
    closed[i] = 1;
    if (i === goalCell) {
      found = true;
      break;
    }
    const x = i % cols;
    const z = Math.floor(i / cols);
    for (let dz = -1; dz <= 1; dz++)
      for (let dx = -1; dx <= 1; dx++) {
        if (!dx && !dz) continue;
        const nx = x + dx;
        const nz = z + dz;
        if (nx < 0 || nz < 0 || nx >= cols || nz >= rows) continue;
        const n = nz * cols + nx;
        if (!free[n] || closed[n]) continue;
        // No squeezing diagonally between two blocked cells.
        if (dx && dz && (!free[z * cols + nx] || !free[nz * cols + x])) continue;
        const cost = g[i] + (dx && dz ? Math.SQRT2 : 1);
        if (cost < g[n]) {
          g[n] = cost;
          from[n] = i;
          push(cost + h(n), n);
        }
      }
  }
  if (!found) return [];
  const cells: P[] = [];
  for (let i = goalCell; i !== start && i >= 0; i = from[i]) cells.push(centre(i));
  cells.reverse();
  cells[cells.length - 1] = goal;
  // Cut what corners there are to cut: from where you are, the furthest point
  // still in a straight line.
  const out: P[] = [];
  let at = a;
  let k = 0;
  while (k < cells.length) {
    let far = k;
    for (let j = cells.length - 1; j > k; j--)
      if (clear(boxes, room, at, cells[j])) {
        far = j;
        break;
      }
    out.push(cells[far]);
    at = cells[far];
    k = far + 1;
  }
  return out;
}

// pathLength is how far a walk goes, from a.
export function pathLength(a: P, route: readonly P[]): number {
  let len = 0;
  let at = a;
  for (const p of route) {
    len += Math.hypot(p.x - at.x, p.z - at.z);
    at = p;
  }
  return len;
}

// turn eases an angle towards another the short way round, by at most max.
export function turn(from: number, to: number, max: number): number {
  let d = to - from;
  while (d > Math.PI) d -= Math.PI * 2;
  while (d < -Math.PI) d += Math.PI * 2;
  return from + Math.max(-max, Math.min(max, d));
}

// Where everything in the Office goes, in metres on the floor: x to the right,
// z towards the camera, the back wall at z = 0. One area per project, a rug
// with its agents' desks in rows and its lead's front desk at the front; the
// wall board on the back wall, with an aisle before it; a lobby at the front,
// where you start, with a sofa to hang out on. Pure, so it has tests, and the
// scene (scene.ts) only draws what this says.
import type { OfficeModel } from './model.ts';

export type Box = { x0: number; z0: number; x1: number; z1: number };
export type Spot = { x: number; z: number; face: number };

export type DeskPlace = {
  id: string;
  // The desk's centre. The agent sits behind it (at z - SEAT) facing you, its
  // laptop before it; the monitor on the desk's front corner faces you too.
  x: number;
  z: number;
  // Where you stand to look at its monitor, across the desk from the agent.
  spot: Spot;
};

export type AreaPlace = {
  project: string;
  rug: Box;
  // The lead's counter, its centre, and where you stand before it.
  lead: { x: number; z: number; spot: Spot };
  desks: DeskPlace[];
  // The area's sign, hung over its back edge.
  sign: { x: number; z: number };
};

export type Layout = {
  room: Box;
  areas: AreaPlace[];
  board: { x: number; w: number; spot: Spot };
  lobby: Box;
  spawn: Spot;
  sofa: Box;
  // Everything you can't walk through, walls aside (the room's bounds are).
  obstacles: Box[];
  // Plants, at the room's corners and the areas' ends.
  plants: { x: number; z: number }[];
  windows: { side: 'back' | 'left' | 'right'; at: number; w: number }[];
};

export const DESK = { w: 1.5, d: 0.75, h: 0.74 };
export const SEAT = 0.72; // the chair's distance behind the desk's centre
export const PITCH_X = 2.5; // desk to desk across a row
export const PITCH_Z = 2.9; // row to row
export const COUNTER = { w: 2.4, d: 0.7 };
const STAND = 1.3; // from the desk's centre to where you stand
const AREA_PAD = 0.8; // the rug's margin around its desks
const LEAD_DEPTH = 2.7; // the counter's strip at the front of an area
const AISLE = 1.8; // between areas
const BACK_AISLE = 3.8; // before the board
const LOBBY = 5.2;
const WALL_PAD = 1.6;
const MAX_ROW_W = 22; // areas wrap onto a new row past this, at least
const MIN_ROOM_W = 16;

// columns is how many desks an area puts side by side: a squarish block, up
// to six across, so a big project is a block rather than a corridor.
export function columns(n: number): number {
  return Math.max(1, Math.min(6, Math.ceil(Math.sqrt(n))));
}

const expand = (b: Box, r: number): Box => ({ x0: b.x0 - r, z0: b.z0 - r, x1: b.x1 + r, z1: b.z1 + r });
const box = (x: number, z: number, w: number, d: number): Box => ({ x0: x - w / 2, z0: z - d / 2, x1: x + w / 2, z1: z + d / 2 });

// layout places a model's areas. It depends only on how many desks each area
// has and their order, so a desk's place changes only when an agent before it
// in its project comes or goes.
export function layout(model: Pick<OfficeModel, 'areas'>): Layout {
  // Each area's size, then rows of areas, left to right.
  const sizes = model.areas.map((a) => {
    const cols = columns(a.desks.length);
    const rows = Math.ceil(a.desks.length / cols);
    const w = Math.max(cols * PITCH_X, COUNTER.w + 1.2) + AREA_PAD * 2;
    const d = rows * PITCH_Z + LEAD_DEPTH + AREA_PAD;
    return { cols, rows, w, d };
  });
  // Rows wrap at a width that keeps the room wider than deep, never narrower
  // than its widest area.
  const floorArea = sizes.reduce((sum, s) => sum + (s.w + AISLE) * (s.d + AISLE), 0);
  const maxRow = Math.max(MAX_ROW_W, Math.sqrt(floorArea * 2.2), ...sizes.map((s) => s.w));
  const lines: number[][] = [];
  let lineW = 0;
  sizes.forEach((s, i) => {
    if (lines.length === 0 || (lineW + AISLE + s.w > maxRow && lines[lines.length - 1].length > 0)) {
      lines.push([]);
      lineW = -AISLE;
    }
    lines[lines.length - 1].push(i);
    lineW += AISLE + s.w;
  });
  const lineWidth = (l: number[]) => l.reduce((sum, i) => sum + sizes[i].w, 0) + AISLE * Math.max(0, l.length - 1);
  const innerW = Math.max(MIN_ROOM_W - WALL_PAD * 2, ...lines.map(lineWidth));
  const roomW = innerW + WALL_PAD * 2;

  const areas: AreaPlace[] = Array.from({ length: model.areas.length });
  const obstacles: Box[] = [];
  const plants: { x: number; z: number }[] = [];
  let z = BACK_AISLE;
  for (const line of lines) {
    const depth = Math.max(...line.map((i) => sizes[i].d));
    // Centred across the room.
    let x = WALL_PAD + (innerW - lineWidth(line)) / 2;
    for (const i of line) {
      const s = sizes[i];
      const a = model.areas[i];
      const rug: Box = { x0: x, z0: z, x1: x + s.w, z1: z + depth };
      const cx = x + s.w / 2;
      const desks: DeskPlace[] = a.desks.map((d, k) => {
        const col = k % s.cols;
        const row = Math.floor(k / s.cols);
        // The last row's desks are centred under the ones above.
        const inRow = row === s.rows - 1 ? a.desks.length - row * s.cols : s.cols;
        const dx = cx + (col - (inRow - 1) / 2) * PITCH_X;
        const dz = z + AREA_PAD + 1.1 + row * PITCH_Z;
        obstacles.push(box(dx, dz, DESK.w, DESK.d));
        // The chair, under the robot, which sits left of centre.
        obstacles.push(box(dx - 0.48, dz - SEAT, 0.62, 0.62));
        // You stand beside the desk, at the robot's end, as you'd stop by a
        // colleague's: the close-up sees the two of you and its monitor.
        return { id: d.id, x: dx, z: dz, spot: { x: dx - 1.17, z: dz + 0.45, face: Math.PI / 2 } };
      });
      // The counter sits at the area's front, its lead behind it facing you.
      const lz = z + depth - LEAD_DEPTH / 2 - 0.1;
      obstacles.push(box(cx, lz, COUNTER.w, COUNTER.d));
      obstacles.push(box(cx, lz - 0.75, 0.7, 0.6));
      areas[i] = { project: a.project, rug, lead: { x: cx, z: lz, spot: { x: cx - 0.3, z: lz + 1.25, face: Math.PI } }, desks, sign: { x: cx, z } };
      // The sign's posts, at the rug's back edge.
      obstacles.push(box(cx - 1.35, z + 0.13, 0.12, 0.12), box(cx + 1.35, z + 0.13, 0.12, 0.12));
      plants.push({ x: rug.x0 + 0.35, z: rug.z1 - 0.35 }, { x: rug.x1 - 0.35, z: rug.z1 - 0.35 });
      obstacles.push(box(rug.x0 + 0.35, rug.z1 - 0.35, 0.5, 0.5), box(rug.x1 - 0.35, rug.z1 - 0.35, 0.5, 0.5));
      x += s.w + AISLE;
    }
    z += depth + AISLE;
  }
  const areasEnd = lines.length ? z - AISLE : BACK_AISLE;
  const lobby: Box = { x0: 0, z0: areasEnd + 0.6, x1: roomW, z1: areasEnd + 0.6 + LOBBY };
  const roomD = lobby.z1;
  const cx = roomW / 2;
  // The sofa faces the room from the lobby's front, a coffee table before it;
  // the water cooler and plants by the walls.
  const sofa = box(cx, lobby.z1 - 1.0, 2.6, 0.95);
  obstacles.push(sofa, box(cx, lobby.z1 - 2.25, 1.3, 0.7));
  obstacles.push(box(0.6, lobby.z0 + 1.2, 0.6, 0.6));
  const corners = [
    { x: 0.55, z: 0.55 },
    { x: roomW - 0.55, z: 0.55 },
    { x: 0.55, z: roomD - 0.55 },
    { x: roomW - 0.55, z: roomD - 0.55 },
  ];
  for (const p of corners) {
    plants.push(p);
    obstacles.push(box(p.x, p.z, 0.6, 0.6));
  }
  // The board's ledge and the bookshelves beside the sofa.
  const boardW = Math.min(5.2, roomW - 6);
  obstacles.push(box(cx, 0.2, boardW, 0.4));
  const windows: Layout['windows'] = [];
  const side = (roomW - boardW) / 2;
  if (side > 4) {
    windows.push({ side: 'back', at: side / 2, w: Math.min(3.2, side - 1.6) }, { side: 'back', at: roomW - side / 2, w: Math.min(3.2, side - 1.6) });
  }
  for (let wz = 3; wz < roomD - 2; wz += 5.5) {
    windows.push({ side: 'left', at: wz + 1.2, w: 2.4 }, { side: 'right', at: wz + 1.2, w: 2.4 });
  }
  return {
    room: { x0: 0, z0: 0, x1: roomW, z1: roomD },
    areas,
    // You stand at the board's left end, out of the way of reading it and of
    // its list, which is on the right.
    board: { x: cx, w: boardW, spot: { x: cx - boardW / 2 + 0.2, z: 2.2, face: Math.PI } },
    lobby,
    spawn: { x: cx, z: lobby.z0 + 1.6, face: Math.PI },
    sofa,
    obstacles,
    plants,
    windows,
  };
}

// spotOf is where you stand to look at a desk, a lead's counter or the board.
export function spotOf(l: Layout, ref: string): Spot | undefined {
  if (ref === 'board') return l.board.spot;
  for (const a of l.areas) {
    if (ref === `${a.project}/lead`) return a.lead.spot;
    for (const d of a.desks) if (d.id === ref) return d.spot;
  }
  return undefined;
}

// nearest is what you're standing at: the desk, counter or board whose spot is
// within reach of p, the closest one.
export function nearest(l: Layout, p: { x: number; z: number }, reach = 1.1): string | undefined {
  let best: string | undefined;
  let bestD = reach * reach;
  const consider = (ref: string, s: Spot) => {
    const d = (s.x - p.x) ** 2 + (s.z - p.z) ** 2;
    if (d < bestD) {
      bestD = d;
      best = ref;
    }
  };
  consider('board', l.board.spot);
  for (const a of l.areas) {
    consider(`${a.project}/lead`, a.lead.spot);
    for (const d of a.desks) consider(d.id, d.spot);
  }
  return best;
}

// walkable is the obstacles grown by the walker's radius, which is what a
// point (the walker's centre) mustn't enter.
export function walkable(l: Layout, radius: number): Box[] {
  return l.obstacles.map((b) => expand(b, radius));
}

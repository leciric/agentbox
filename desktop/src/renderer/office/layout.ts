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
  // Its pods' rugs: a pod is up to four desks, two by two.
  pods: Box[];
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
  // The room's features: a glass meeting room in the back left corner, a big
  // window and a moss wall on the left wall, a brick wall on the back one,
  // a planter dividing the lobby, a server rack in its corner.
  meeting: Box;
  // Free floor the room furnishes: a lounge in the lobby's left half, a
  // café where the work area has room to spare.
  lounge: Box;
  cafe?: Box;
  window: { at: number; w: number };
  moss: { at: number; w: number };
  brick: { at: number; w: number };
  planter: Box;
  rack: { x: number; z: number };
};

export const DESK = { w: 1.5, d: 0.75, h: 0.74 };
export const SEAT = 0.72; // the chair's distance behind the desk's centre
export const PITCH_X = 2.5; // desk to desk across a row
export const PITCH_Z = 2.9; // row to row
export const COUNTER = { w: 2.4, d: 0.7 };
const POD = 4;
const POD_W = 2 * PITCH_X;
const POD_D = 2 * PITCH_Z;
const POD_GAP_X = 1.6;
const POD_GAP_Z = 1.0;
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
  // Desks sit in pods of up to four, two by two, with floor between the
  // pods; an area is a squarish block of its pods, its lead at the front.
  const sizes = model.areas.map((a) => {
    const pods = Math.ceil(a.desks.length / POD);
    const cols = columns(pods);
    const rows = Math.ceil(pods / cols);
    const inner = cols * POD_W + Math.max(0, cols - 1) * POD_GAP_X;
    const w = Math.max(inner, COUNTER.w + 1.2) + AREA_PAD * 2;
    const d = rows * POD_D + Math.max(0, rows - 1) * POD_GAP_Z + LEAD_DEPTH + AREA_PAD;
    return { cols, rows, w, d, inner };
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
      const pods: Box[] = [];
      const podX = (p: number) => cx - s.inner / 2 + (p % s.cols) * (POD_W + POD_GAP_X) + POD_W / 2;
      const podZ = (p: number) => z + AREA_PAD + 1.1 + Math.floor(p / s.cols) * (POD_D + POD_GAP_Z);
      for (let p = 0; p * POD < a.desks.length; p++) {
        const n = Math.min(POD, a.desks.length - p * POD);
        const px = podX(p);
        const pz = podZ(p);
        // The rug runs from its first desk to its last, a margin round them.
        const left = px - PITCH_X / 2;
        const right = n > 1 ? px + PITCH_X / 2 : left;
        const back = pz;
        const front = pz + (n > 2 ? PITCH_Z : 0);
        pods.push({ x0: left - 1.0, z0: back - 1.3, x1: right + 1.0, z1: front + 1.15 });
      }
      const desks: DeskPlace[] = a.desks.map((d, k) => {
        const p = Math.floor(k / POD);
        const j = k % POD;
        const dx = podX(p) + ((j % 2) - 0.5) * PITCH_X;
        const dz = podZ(p) + Math.floor(j / 2) * PITCH_Z;
        obstacles.push(box(dx, dz, DESK.w, DESK.d));
        // The chair, under the robot, which sits left of centre.
        obstacles.push(box(dx - 0.48, dz - SEAT, 0.62, 0.62));
        // You stand beside the desk, at the agent's end, as you'd stop by a
        // colleague's, turned half to the room: the close-up sees your face too.
        return { id: d.id, x: dx, z: dz, spot: { x: dx - 1.4, z: dz + 0.5, face: Math.PI / 4 } };
      });
      // The counter sits at the area's front, its lead behind it facing you.
      const lz = z + depth - LEAD_DEPTH / 2 - 0.1;
      obstacles.push(box(cx, lz, COUNTER.w, COUNTER.d));
      obstacles.push(box(cx, lz - 0.75, 0.7, 0.6));
      areas[i] = { project: a.project, rug, pods, lead: { x: cx, z: lz, spot: { x: cx - 1.65, z: lz + 0.55, face: Math.PI / 2 } }, desks, sign: { x: cx, z } };
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
  // The meeting room, in the back left corner, behind glass.
  const meeting: Box = { x0: 0, z0: 0, x1: Math.min(6, roomW * 0.28), z1: BACK_AISLE - 0.4 };
  obstacles.push({ x0: meeting.x0, z0: meeting.z0, x1: meeting.x1 + 0.05, z1: meeting.z1 + 0.05 });
  // The sofa faces the room from the lobby's front, a coffee table before it.
  const sofa = box(cx + roomW * 0.12, lobby.z1 - 1.0, 2.6, 0.95);
  obstacles.push(sofa, box(cx + roomW * 0.12, lobby.z1 - 2.25, 1.3, 0.7));
  // The planter divides the lobby from the room, on its left half: the way
  // in is round its end.
  const planter: Box = { x0: 0.5, z0: lobby.z0 + 0.15, x1: Math.max(3, roomW * 0.5), z1: lobby.z0 + 0.6 };
  obstacles.push(planter);
  const rack = { x: roomW - 0.55, z: lobby.z1 - 0.7 };
  // The lounge: two armchairs and a low table on a rug, by the left wall.
  const lounge: Box = { x0: 0.6, z0: lobby.z0 + 1.2, x1: Math.min(4.2, roomW * 0.3), z1: lobby.z1 - 0.5 };
  obstacles.push({ x0: lounge.x0 + 0.3, z0: lounge.z0 + 0.5, x1: lounge.x1 - 0.3, z1: lounge.z1 - 0.6 });
  // A café in the work area's spare floor, right of the areas, when wide enough.
  const areasRight = areas.reduce((m, a) => Math.max(m, a.rug.x1), 0);
  const cafe: Box | undefined = roomW - areasRight > 4.2 ? { x0: areasRight + 1.2, z0: BACK_AISLE + 0.4, x1: roomW - 0.8, z1: Math.min(lobby.z0 - 0.6, BACK_AISLE + 5) } : undefined;
  if (cafe) obstacles.push({ x0: cafe.x0 + 0.4, z0: cafe.z0 + 0.4, x1: cafe.x1 - 0.4, z1: cafe.z1 - 0.4 });
  obstacles.push(box(rack.x, rack.z, 0.65, 0.65));
  const corners = [
    { x: roomW - 0.55, z: 0.55 },
    { x: 0.55, z: roomD - 0.55 },
  ];
  for (const p of corners) {
    plants.push(p);
    obstacles.push(box(p.x, p.z, 0.6, 0.6));
  }
  // The board, right of the meeting room; the brick wall where there's room.
  const boardW = Math.min(5.2, roomW - meeting.x1 - 3);
  const boardX = Math.min(roomW - boardW / 2 - 1, Math.max(cx, meeting.x1 + boardW / 2 + 1));
  obstacles.push(box(boardX, 0.2, boardW, 0.4));
  const leftGap = boardX - boardW / 2 - meeting.x1;
  const rightGap = roomW - (boardX + boardW / 2);
  const brick = leftGap >= rightGap ? { at: meeting.x1 + leftGap / 2, w: Math.max(1.6, Math.min(2.6, leftGap - 0.5)) } : { at: roomW - rightGap / 2, w: Math.max(1.6, Math.min(2.6, rightGap - 0.5)) };
  // The left wall: the window past the meeting room, the moss wall after it.
  const winW = Math.min(4.6, Math.max(2.2, (lobby.z0 - meeting.z1) * 0.5));
  const window = { at: meeting.z1 + 0.4 + winW / 2, w: winW };
  const mossW = Math.min(3.6, Math.max(1.4, lobby.z0 - (window.at + winW / 2) - 0.8));
  const moss = { at: window.at + winW / 2 + 0.4 + mossW / 2, w: mossW };
  return {
    room: { x0: 0, z0: 0, x1: roomW, z1: roomD },
    areas,
    // You stand at the board's left end, out of the way of reading it and of
    // its list, which is on the right.
    board: { x: boardX, w: boardW, spot: { x: boardX - boardW / 2 + 0.2, z: 2.2, face: Math.PI } },
    lobby,
    spawn: { x: cx + roomW * 0.12, z: lobby.z0 + 1.6, face: Math.PI },
    sofa,
    obstacles,
    plants,
    meeting,
    window,
    moss,
    brick,
    planter,
    rack,
    lounge,
    cafe,
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

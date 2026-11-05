// Where everything in the Office goes, in metres on the floor: x to the right,
// z towards the camera. The Office is a campus of rooms (campus, below), each
// project's its own small diorama, or a few floors of it for a big one: a
// room (roomLayout) is its project's zone of pods on terrazzo, its lead's
// counter at the front, the board on the back wall, and its commons on the
// wood side. Pure, so it has tests, and the scene (scene.ts) only draws what
// this says.
import type { OfficeModel } from './model.ts';

export type Box = { x0: number; z0: number; x1: number; z1: number };
export type Spot = { x: number; z: number; face: number };

export type DeskPlace = {
  id: string;
  // The desk's centre. The agent sits behind it (at z - SEAT) facing the
  // room, at its outer end (side: -1 the left, 1 the right): two desks make
  // a pod, a bench pushed together, its agents at either end.
  x: number;
  z: number;
  side: -1 | 1;
  // Where you stand to look at its monitor, across the desk from the agent.
  spot: Spot;
};

export type AreaPlace = {
  project: string;
  rug: Box;
  // The lead's counter, its centre, and where you stand before it; on its
  // project's first floor only.
  lead?: { x: number; z: number; spot: Spot };
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
  // The wood-floored side (lobby is its old name).
  lobby: Box;
  // The wood-floored commons: its first, and each of them.
  wood: Box;
  woods: Box[];
  spawn: Spot;
  sofa: Box;
  shelf: Box;
  // Everything you can't walk through, walls aside (the room's bounds are).
  obstacles: Box[];
  // Plants, at the room's corners and the areas' ends.
  plants: { x: number; z: number }[];
  // Everything else it's furnished with (FOOT says their sizes).
  props: Prop[];
  // Where framed prints hang on the back wall, along it.
  art: number[];
  // The room's features: a glass meeting room in the back left corner, a big
  // window and a moss wall on the left wall, a brick wall on the back one,
  // a planter dividing the lobby, a server rack in its corner.
  meeting: Box;
  // The commons' lounge, on the wood side.
  lounge: Box;
  // Which of its commons it has (0-2 a big room's, 3-5 a small one's), and
  // which of its finishes (decor.ts, 0-2): each project's, each floor's, its own.
  variant: number;
  finish: number;
  window: { at: number; w: number };
  moss: { at: number; w: number };
  brick: { at: number; w: number };
  planter: Box;
  rack: { x: number; z: number };
};

export const DESK = { w: 1.5, d: 0.75, h: 0.74 };
export const SEAT = 0.72; // the chair's distance behind the desk's centre
export const COUNTER = { w: 2.4, d: 0.7 };
const POD = 2;
// The room is a grid of cells, two rows of them, each holding one thing: a
// pod of two desks, the lead's counter, the meeting room, the kitchen, the
// lounge, a games corner or a corner of things. Pods take every other cell,
// a checkerboard, so desks are spread through the room with the commons
// between them, as the target's are, rather than desks on one side and the
// commons on the other.
const CELL_W = 5.8;
const CELL_D = 4.8;
// A small team's cells closer together: a cosy room, not a big one half bare.
const SMALL_W = 5.6;
const SMALL_D = 4.3;
const SMALL_BACK = 2.7;
const ROWS = 2;
const PAD_L = 2.0; // the left wall's strip: its window, moss, lockers and racks, and room beside them
const PAD_R = 0.6;
const BACK = 3.1; // the back aisle, before the board
const FRONT = 0.5;
// From a cell's back to its pod's desks.
const DESK_IN = 2.2;

// columns is how many pods an area puts side by side: a block a little wider
// than deep, so its room stays the target's size.
export function columns(pods: number): number {
  if (pods <= 1) return 1;
  // Three pods are two and one staggered behind them: a cluster, not a row.
  const rows = Math.max(1, Math.round(Math.sqrt(pods / 1.2)));
  return Math.ceil(pods / rows);
}

const expand = (b: Box, r: number): Box => ({ x0: b.x0 - r, z0: b.z0 - r, x1: b.x1 + r, z1: b.z1 + r });
const box = (x: number, z: number, w: number, d: number): Box => ({ x0: x - w / 2, z0: z - d / 2, x1: x + w / 2, z1: z + d / 2 });

// An area as a room lays it out: a floor of a project past the first has no
// lead (its counter is on the first).
type AreaIn = Pick<OfficeModel, 'areas'>['areas'][number] & { noLead?: boolean; floor?: number };

type CellKind = 'pod' | 'lead' | 'meeting' | 'kitchen' | 'lounge' | 'games' | 'corner';

// roomLayout lays out one room: its project's first area (a room holds one).
// It depends only on how many desks the area has and their order, so a
// desk's place changes only when an agent before it comes or goes.
export function roomLayout(model: { areas: AreaIn[] }): Layout {
  const a = model.areas[0];
  const n = a?.desks.length ?? 0;
  const P = Math.ceil(n / POD);
  const hasLead = !!a && !a.noLead;
  // A small team's room has no meeting room: it's small.
  const compact = !!a && n <= 4;
  const project = a?.project ?? '';
  let h = 0;
  for (let i = 0; i < project.length; i++) h = (h * 31 + project.charCodeAt(i)) >>> 0;
  const floor = a?.floor ?? 1;
  const meetIn = !compact;
  // The glass meeting room at the front left, as the target's, or the back
  // left: floor to floor in turn.
  const meetFront = meetIn && ((h >> 1) + floor) % 2 === 0;
  const C = Math.max(2, Math.ceil((P + (hasLead ? 1 : 0) + (meetIn ? 1 : 0) + 2) / ROWS));
  const CW = compact ? SMALL_W : CELL_W;
  const CD = compact ? SMALL_D : CELL_D;
  const BK = compact ? SMALL_BACK : BACK;
  const roomW = PAD_L + C * CW + PAD_R;
  const roomD = BK + ROWS * CD + FRONT;
  const cellBox = (c: number, r: number): Box => ({ x0: PAD_L + c * CW, z0: BK + r * CD, x1: PAD_L + (c + 1) * CW, z1: BK + (r + 1) * CD });
  const kinds: (CellKind | undefined)[][] = Array.from({ length: C }, () => Array.from({ length: ROWS }, () => undefined));
  const cells: [number, number][] = [];
  for (let c = 0; c < C; c++) for (let r = 0; r < ROWS; r++) cells.push([c, r]);
  const open = (r?: number) => cells.filter(([c, rr]) => !kinds[c][rr] && (r === undefined || rr === r));
  const mid = (list: [number, number][]) => [...list].sort((p, q) => Math.abs(p[0] - (C - 1) / 2) - Math.abs(q[0] - (C - 1) / 2) || p[0] - q[0])[0];
  if (meetIn) kinds[0][meetFront ? 1 : 0] = 'meeting';
  // Pods on the checkerboard the meeting room isn't on, left to right; any
  // more than it holds where there's room.
  // (A small room's first pod at the back, under its board.)
  const p0 = meetIn ? (meetFront ? 0 : 1) : 0;
  // Fewer pods than the checkerboard has room for: spread evenly along it,
  // not bunched at one end.
  const checker = cells.filter(([c, r]) => (c + r) % 2 === p0 && !kinds[c][r]);
  const podCells: [number, number][] = [];
  if (P > 0 && P < checker.length) for (let i = 0; i < P; i++) podCells.push(checker[P === 1 ? 0 : Math.round((i * (checker.length - 1)) / (P - 1))]);
  else podCells.push(...checker.slice(0, P));
  for (const [c, r] of cells) if (podCells.length < P && (c + r) % 2 !== p0 && !kinds[c][r]) podCells.push([c, r]);
  podCells.sort((p, q) => p[0] - q[0] || p[1] - q[1]);
  for (const [c, r] of podCells) kinds[c][r] = 'pod';
  // The lead's counter in the front row, nearest its middle: who you see first.
  let leadCell: [number, number] | undefined;
  if (hasLead) {
    leadCell = mid(open(1)) ?? mid(open());
    if (leadCell) kinds[leadCell[0]][leadCell[1]] = 'lead';
  }
  // The board on the back wall over a back cell near the middle that leaves
  // the wall free: a pod's, or an open one's.
  const boardCol = (mid(cells.filter(([c, r]) => r === 0 && (kinds[c][r] === 'pod' || !kinds[c][r]))) ?? [Math.min(1, C - 1), 0])[0];
  // The kitchen against the back wall at one end, its counters on it; the
  // lounge on the back wall too where there's room, its sofa by the brick.
  // (Over four floors, with the meeting room's turn, no two alike.)
  const swap = ((h >> 2) + (floor >> 1)) % 2 === 0;
  const backOpen = () => open(0).filter(([c]) => c !== boardCol).sort((p, q) => (swap ? p[0] - q[0] : q[0] - p[0]));
  // (Neither ever the board's: its counters or sofa would stand before it.)
  const notBoard = () => open().filter(([c, r]) => !(r === 0 && c === boardCol));
  // Floor to floor, the kitchen or the lounge has the back wall first.
  let kitchenCell: [number, number] | undefined;
  let loungeCell: [number, number] | undefined;
  const takeKitchen = () => {
    kitchenCell = backOpen()[0] ?? notBoard()[0];
    if (kitchenCell) kinds[kitchenCell[0]][kitchenCell[1]] = 'kitchen';
  };
  const takeLounge = () => {
    loungeCell = backOpen()[0] ?? open(1)[0] ?? notBoard()[0];
    if (loungeCell) kinds[loungeCell[0]][loungeCell[1]] = 'lounge';
  };
  if (swap && !compact) {
    takeLounge();
    takeKitchen();
  } else {
    takeKitchen();
    takeLounge();
  }
  // The rest: a games corner, then corners of things.
  let games = false;
  for (const [c, r] of open()) {
    kinds[c][r] = !games && !compact && c !== boardCol ? 'games' : 'corner';
    if (kinds[c][r] === 'games') games = true;
  }

  const obstacles: Box[] = [];
  const plants: { x: number; z: number }[] = [];
  const props: Prop[] = [];
  const woods: Box[] = [];
  // Floor no cell's thing uses, which the room furnishes (below). Low says it's
  // where a close-up looks across it: nothing tall.
  const free: (Box & { low?: boolean })[] = [];
  const prop = (kind: PropKind, x: number, z: number, rot = 0) => {
    props.push({ kind, x, z, rot });
    // A mat is floor: walked over.
    if (kind === 'mat') return;
    const [w, d] = footOf(kind);
    const turned = Math.abs(Math.sin(rot)) > 0.5;
    obstacles.push(box(x, z, turned ? d : w, turned ? w : d));
  };
  const clearAt = (x: number, z: number, r: number) => !obstacles.some((b) => x + r > b.x0 && x - r < b.x1 && z + r > b.z0 && z - r < b.z1);

  // The pods: a bench of two desks on its rug, an agent at either end.
  const pods: Box[] = [];
  const desks: DeskPlace[] = [];
  for (const [p, [c, r]] of podCells.entries()) {
    const cb = cellBox(c, r);
    const px = (cb.x0 + cb.x1) / 2;
    const dz = cb.z0 + DESK_IN;
    const k = Math.min(POD, n - p * POD);
    pods.push({ x0: px - 1.95, z0: dz - 1.35, x1: k > 1 ? px + 1.95 : px + 0.45, z1: dz + 0.75 });
    for (let j = 0; j < k; j++) {
      const d = a!.desks[p * POD + j];
      const side = j ? 1 : -1;
      const dx = px + side * (DESK.w / 2);
      obstacles.push(box(dx, dz, DESK.w, DESK.d));
      // The chair and the agent on it: you walk round, not through.
      obstacles.push(box(dx + side * 0.48, dz - SEAT, 1.2, 1.1));
      // You stand beside the agent, level with it, facing it, a good step
      // out: seen from the front, clear of it either side, never over it.
      desks.push({ id: d.id, x: dx, z: dz, side: side as -1 | 1, spot: { x: Math.min(roomW - PAD_R - 0.1, Math.max(1.6, dx + side * 2.3)), z: dz - SEAT + 0.5, face: -side * 1.4 } });
    }
  }
  // A potted plant at each pod's cell's front corner, one side or the other:
  // a desk's own green.
  for (const [p, [c, r]] of podCells.entries()) {
    const cb = cellBox(c, r);
    const x = p % 2 ? cb.x1 - 0.55 : cb.x0 + 0.55;
    const z = cb.z1 - 0.45;
    plants.push({ x, z });
    obstacles.push(box(x, z, 0.55, 0.55));
  }
  // The lead's counter, its lead behind it facing the room, on a mat.
  let lead: AreaPlace['lead'];
  if (leadCell) {
    const cb = cellBox(...leadCell);
    const cx = (cb.x0 + cb.x1) / 2;
    // At its cell's front: well clear of the pods behind.
    const cz = cb.z0 + 2.9;
    obstacles.push(box(cx, cz, COUNTER.w, COUNTER.d), box(cx, cz - 0.75, 0.7, 0.6));
    props.push({ kind: 'mat', x: cx, z: cz + 0.25, rot: 0 });
    lead = { x: cx, z: cz, spot: { x: cx - 1.95, z: cz + 0.55, face: Math.PI / 2 } };
  }

  // The meeting room, behind glass, at the left wall.
  const m0 = cellBox(0, meetFront ? 1 : 0);
  const meeting: Box = !meetIn ? { x0: 0, z0: 0, x1: 0, z1: 0 } : meetFront ? { x0: 0, z0: m0.z0 + 0.3, x1: m0.x1 - 0.8, z1: roomD } : { x0: 0, z0: 0, x1: m0.x1 - 0.8, z1: m0.z0 + 2.8 };
  if (meetIn) obstacles.push({ x0: meeting.x0, z0: meeting.z0 - 0.05, x1: meeting.x1 + 0.05, z1: meeting.z1 + 0.05 });

  // The kitchen: counters and a fridge against the back wall, an island before
  // them with stools; in the front row, where there's no wall, an island alone.
  const [kw, kd] = footOf('kitchen');
  const [fw, fd] = footOf('fridge');
  const [, id] = footOf('island');
  let kitchenBox: Box | undefined;
  if (kitchenCell) {
    const cb = cellBox(...kitchenCell);
    const cx = (cb.x0 + cb.x1) / 2;
    if (kitchenCell[1] === 0) {
      const fridge = cb.x1 - cb.x0 - 0.6 >= kw + fw + 0.1;
      const kx = cx - (fridge ? (fw + 0.1) / 2 : 0);
      prop('kitchen', kx, kd / 2 + 0.02);
      if (fridge) prop('fridge', kx + kw / 2 + 0.1 + fw / 2, fd / 2 + 0.02);
      prop('island', cx, cb.z0 + 0.3 + id / 2);
      kitchenBox = { x0: kx - kw / 2, z0: 0, x1: fridge ? kx + kw / 2 + 0.1 + fw : kx + kw / 2, z1: 1 };
    } else {
      // (Clear of the spot at the pod next door.)
      prop('vending', cb.x1 - footOf('vending')[0] / 2 - 0.85, cb.z0 + 0.3 + footOf('vending')[1] / 2);
      prop('island', cx - 0.4, cb.z0 + 1.6 + id / 2);
    }
    woods.push({ x0: cb.x0 + 0.15, z0: kitchenCell[1] === 0 ? 0 : cb.z0 + 0.15, x1: cb.x1 - 0.15, z1: kitchenCell[1] === 1 ? roomD : cb.z1 - 0.15 });
  }

  // The lounge: a sectional sofa facing the room, a low table before it, a
  // lamp at its end, on a soft rug; a planter at its side, the target's.
  let lounge: Box = { x0: 0, z0: 0, x1: 0, z1: 0 };
  let sofa: Box = lounge;
  let planter: Box = { x0: 0, z0: 0, x1: 0.01, z1: 0.01 };
  if (loungeCell) {
    const cb = cellBox(...loungeCell);
    const back = loungeCell[1] === 0;
    const lw = Math.min(3.4, cb.x1 - cb.x0 - 2.6);
    const lx0 = cb.x0 + 1.6;
    lounge = { x0: lx0, z0: back ? 0.5 : cb.z0 + 0.4, x1: lx0 + lw, z1: back ? cb.z0 + 1.6 : cb.z0 + 4.4 };
    sofa = { x0: lounge.x0 + 0.3, z0: lounge.z0 + 0.1, x1: lounge.x1 - 0.3, z1: lounge.z0 + 1.05 };
    obstacles.push(sofa, { x0: sofa.x0, z0: sofa.z1, x1: sofa.x0 + 0.95, z1: sofa.z1 + 1.2 });
    obstacles.push(box((sofa.x0 + sofa.x1) / 2 + 0.4, lounge.z0 + 2.35, 1.5, 0.75));
    // The lamp, at the sofa's right end.
    obstacles.push(box(sofa.x1 + 0.3, sofa.z1 + 0.9, 0.45, 0.45));
    // (Clear of the spot at the pod next door.)
    planter = { x0: cb.x0 + 0.75, z0: back ? cb.z0 + 0.4 : cb.z0 + 0.6, x1: cb.x0 + 1.25, z1: cb.z1 - (back ? 0.6 : 0.4) };
    obstacles.push(planter);
    woods.push({ x0: cb.x0 + 0.15, z0: back ? 0 : cb.z0 + 0.15, x1: cb.x1 - 0.15, z1: back ? cb.z1 - 0.15 : roomD });
  }

  // Games: a ping-pong table and arcade cabinets; corners: a few things on a mat.
  for (const [c, r] of cells) {
    const kind = kinds[c][r];
    const cb = cellBox(c, r);
    const cx = (cb.x0 + cb.x1) / 2;
    const cz = (cb.z0 + cb.z1) / 2;
    if (kind === 'games') {
      prop('pingpong', cx - 0.5, cz);
      prop('arcade', cb.x1 - 1.1, cz - 0.5, -Math.PI / 2);
      prop('arcade', cb.x1 - 1.1, cz + 0.45, -Math.PI / 2);
      woods.push({ x0: cb.x0 + 0.15, z0: r === 0 ? cb.z0 + 0.15 : cb.z0 + 0.15, x1: cb.x1 - 0.15, z1: r === 1 ? roomD : cb.z1 - 0.15 });
    } else if (kind === 'corner') {
      // Clear of the spots at the next cells' pods, either side.
      free.push({ x0: cb.x0 + 1.0, z0: cb.z0 + 0.5, x1: cb.x1 - 1.0, z1: cb.z1 - 0.5, low: c === boardCol && r === 0 });
      woods.push({ x0: cb.x0 + 0.15, z0: cb.z0 + 0.15, x1: cb.x1 - 0.15, z1: r === 1 ? roomD : cb.z1 - 0.15 });
    }
  }

  // The left wall: the window (past a meeting room at the back), the moss wall
  // after it, lockers, and the bookshelf and racks at its far end (or before a
  // meeting room at the front).
  const wallFrom = meetIn && !meetFront ? meeting.z1 + 0.4 : 0.5;
  const wallTo = meetFront ? meeting.z0 - 0.3 : roomD - 1.0;
  const shelf: Box = { x0: 0, z0: wallTo - 1.6, x1: 0.4, z1: wallTo };
  obstacles.push(shelf);
  const rack = { x: 0.36, z: shelf.z0 - 0.5 };
  obstacles.push(box(rack.x, rack.z, 0.65, 0.65));
  prop('rack', footOf('rack')[1] / 2 + 0.03, shelf.z0 - 1.3, Math.PI / 2);
  const winW = Math.min(4.6, Math.max(2.2, (shelf.z0 - 1.8 - wallFrom) * 0.45));
  const window = { at: wallFrom + winW / 2, w: winW };
  const mossW = Math.min(3.6, Math.max(1.4, shelf.z0 - 1.9 - (window.at + winW / 2) - 0.4));
  const moss = { at: window.at + winW / 2 + 0.4 + mossW / 2, w: mossW };
  for (let lz = moss.at + mossW / 2 + 1.4; lz + 1.25 < shelf.z0 - 1.8; lz += 3.2) prop('lockers', footOf('lockers')[1] / 2 + 0.02, lz, Math.PI / 2);

  // The board, on the back wall over its cell; you read it from before its
  // left end, out of its way (you step out of the picture there).
  const bc = cellBox(boardCol, 0);
  const boardW = Math.min(5.2, CW - 0.8);
  const boardX = (bc.x0 + bc.x1) / 2;
  obstacles.push(box(boardX, 0.2, boardW, 0.4));
  // The brick wall with its sign: behind the lounge on the back wall, or else
  // on the free back wall farthest from the board.
  const wallUsed: [number, number][] = [[boardX - boardW / 2, boardX + boardW / 2]];
  if (meetIn && !meetFront) wallUsed.push([0, meeting.x1]);
  if (kitchenBox) wallUsed.push([kitchenBox.x0 - 0.2, kitchenBox.x1 + 0.2]);
  let brick = { at: (lounge.x0 + lounge.x1) / 2, w: Math.min(3.6, lounge.x1 - lounge.x0) };
  if (!(loungeCell && loungeCell[1] === 0)) {
    // A back cell not the board's nor the kitchen's.
    const cands = cells.filter(([c, r]) => r === 0 && c !== boardCol && kinds[c][r] !== 'kitchen' && kinds[c][r] !== 'meeting').map(([c]) => cellBox(c, 0));
    const at = cands.sort((p, q) => Math.abs((q.x0 + q.x1) / 2 - boardX) - Math.abs((p.x0 + p.x1) / 2 - boardX))[0];
    brick = at ? { at: (at.x0 + at.x1) / 2, w: 3.2 } : { at: roomW - 2.2, w: 2.4 };
  }
  wallUsed.push([brick.at - brick.w / 2, brick.at + brick.w / 2]);
  // Framed prints and low shelves of plants where the back wall is free.
  const art: number[] = [];
  const spans: [number, number][] = [];
  {
    const used = [...wallUsed].sort((p, q) => p[0] - q[0]);
    let x = 0.4;
    for (const [u0, u1] of used) {
      if (u0 - x > 1.6) spans.push([x, u0]);
      x = Math.max(x, u1);
    }
    if (roomW - 0.4 - x > 1.6) spans.push([x, roomW - 0.4]);
  }
  for (const [s0, s1] of spans) {
    const w = s1 - s0 - 0.6;
    const cx = (s0 + s1) / 2;
    // A low shelf only where nothing stands before it: over a pod's or a
    // corner's back aisle.
    if (w > 2.4 && clearAt(cx, 0.3, 0.6)) prop('shelf', cx, footOf('shelf')[1] / 2 + 0.02);
    if (w > 4.4) art.push(cx - 1.2, cx + 1.2);
    else if (w > 1.2) art.push(cx);
  }
  // A tree in the back right corner, and potted plants at the front corners,
  // where there's floor.
  if (clearAt(roomW - 0.7, 0.7, 0.6)) prop('tree', roomW - 0.6, 0.6);
  for (const p of [
    { x: 0.6, z: roomD - 0.6 },
    { x: roomW - 0.6, z: roomD - 0.6 },
  ])
    if (clearAt(p.x, p.z, 0.45)) {
      plants.push(p);
      obstacles.push(box(p.x, p.z, 0.6, 0.6));
    }

  // The corners, furnished: a few things together on a mat, or couches, or
  // islands of a tree, a standing table, a printer, each with room to pass.
  let nI = 0;
  let themed = 0;
  for (const f of free) {
    const w = f.x1 - f.x0;
    const d = f.z1 - f.z0;
    // A theme whose things all fit this corner, each room's in its own turn.
    const themes = f.low ? LOW_THEMES : THEMES;
    const fits = (th: Theme) =>
      th.every(([kind, dx, dz]) => {
        const [tw, td] = footOf(kind);
        return Math.abs(dx) + tw / 2 <= w / 2 - 0.1 && Math.abs(dz) + td / 2 <= d / 2 - 0.1;
      });
    const theme = Array.from({ length: themes.length }, (_, i) => themes[(themed + h + i) % themes.length]).find(fits);
    if (theme) {
      const cx = (f.x0 + f.x1) / 2;
      const cz = (f.z0 + f.z1) / 2;
      prop('mat', cx, cz);
      for (const [kind, dx, dz] of theme) prop(kind, cx + dx, cz + dz);
      themed++;
      continue;
    }
    const kinds2 = f.low ? LOW : ISLANDS;
    for (let t = 0; t < kinds2.length; t++) {
      const kind = kinds2[(nI + t) % kinds2.length];
      const [fw2, fd2] = footOf(kind);
      if (fw2 > w - 0.8 || fd2 > d - 0.8) continue;
      prop(kind, (f.x0 + f.x1) / 2, (f.z0 + f.z1) / 2);
      nI += t + 1;
      break;
    }
  }

  // You read the board from beside its end, in the picture with it: its left
  // end, or its right where the left is taken; else before its left part.
  const boardSpot: Spot = [
    { x: boardX - boardW / 2 - 0.75, z: 1.1, face: Math.PI / 2 },
    { x: boardX + boardW / 2 + 0.75, z: 1.1, face: -Math.PI / 2 },
  ].find((p) => clearAt(p.x, p.z, 0.6)) ?? { x: boardX - boardW / 2 + 0.8, z: 1.3, face: Math.PI / 2 };
  // You come in at the front, by the middle, in full view: the nearest spot
  // clear of everything, never inside a piece of furniture.
  const want = { x: roomW * 0.55, z: roomD - 1.3 };
  let spawn = { ...want, face: -0.5 };
  const around: { x: number; z: number; d: number }[] = [];
  for (let dx = -8; dx <= 8; dx += 0.25) for (let dz = -6; dz <= 1; dz += 0.25) around.push({ x: want.x + dx, z: want.z + dz, d: dx * dx + dz * dz });
  around.sort((p, q) => p.d - q.d);
  // (Well clear of every spot, so you come in at nothing.)
  const spots = [...desks.map((d) => d.spot), ...(lead ? [lead.spot] : []), boardSpot];
  for (const p of around)
    if (p.x > 0.8 && p.x < roomW - 0.8 && p.z > 0.8 && p.z < roomD - 0.8 && clearAt(p.x, p.z, 0.75) && spots.every((q) => Math.hypot(q.x - p.x, q.z - p.z) > 2.6)) {
      spawn = { x: p.x, z: p.z, face: -0.5 };
      break;
    }

  const areas: AreaPlace[] = a ? [{ project: a.project, rug: { x0: PAD_L - 0.5, z0: BK - 0.5, x1: roomW - PAD_R + 0.5, z1: roomD }, pods, lead, desks, sign: { x: boardX, z: BK } }] : [];
  const wood = woods[0] ?? { x0: roomW, z0: 0, x1: roomW, z1: 0 };
  return {
    room: { x0: 0, z0: 0, x1: roomW, z1: roomD },
    areas,
    board: { x: boardX, w: boardW, spot: boardSpot },
    lobby: wood,
    wood,
    woods,
    spawn,
    sofa,
    shelf,
    obstacles,
    plants,
    props,
    art,
    meeting,
    window,
    moss,
    brick,
    planter,
    rack,
    lounge,
    variant: kinds.flat().filter((k) => k === 'games').length ? h % 3 : 3 + (h % 3),
    finish: (h + floor - 1) % 3,
  };
}

// The things the room is furnished with, besides desks: their footprints,
// across (x) and deep (z) when not turned; turned a quarter, they swap.
export type PropKind = 'tree' | 'standing' | 'printer' | 'shelf' | 'booth' | 'beanbags' | 'whiteboard' | 'lockers' | 'kitchen' | 'fridge' | 'island' | 'pingpong' | 'arcade' | 'rack' | 'plants' | 'aquarium' | 'vending' | 'logo' | 'mat' | 'couch' | 'lowtable';
export type Prop = { kind: PropKind; x: number; z: number; rot: number };
export const FOOT: Record<PropKind, [number, number]> = {
  tree: [0.9, 0.9],
  standing: [1.4, 0.8],
  printer: [1.7, 0.7],
  shelf: [1.8, 0.45],
  booth: [1.3, 1.3],
  beanbags: [2.6, 2.2],
  whiteboard: [1.7, 0.5],
  lockers: [2.0, 0.5],
  kitchen: [2.8, 0.7],
  fridge: [0.8, 0.7],
  island: [2.4, 1.6],
  pingpong: [2.8, 1.6],
  arcade: [0.75, 0.75],
  rack: [0.65, 0.65],
  plants: [1.5, 0.9],
  aquarium: [1.5, 0.55],
  vending: [0.95, 0.8],
  logo: [1.1, 1.1],
  mat: [4.3, 2.4],
  couch: [1.8, 0.8],
  lowtable: [0.9, 0.5],
};
// Props are a little bigger than life, as a toy's furniture is.
export const PROP_SCALE = 1.25;
export const footOf = (k: PropKind): [number, number] => [FOOT[k][0] * PROP_SCALE, FOOT[k][1] * PROP_SCALE];
const ISLANDS: PropKind[] = ['tree', 'standing', 'aquarium', 'booth', 'plants', 'beanbags', 'vending', 'logo', 'printer', 'shelf', 'tree', 'whiteboard'];
// What may stand before an area: nothing taller than a desk's monitor, nor
// leafy, so a close-up looks over it.
const LOW: PropKind[] = ['standing', 'beanbags', 'shelf', 'printer'];
// Corners of a few things together, on a mat, each within 5.4 by 3: where
// each stands from the mat's middle. (Sizes are FOOT's, PROP_SCALE larger.)
type Theme = [PropKind, number, number][];
const NOOK: Theme = [
  ['beanbags', -0.95, 0],
  ['plants', 1.65, -0.55],
];
const GARDEN: Theme = [
  ['plants', -1.6, -0.45],
  ['logo', 0.3, 0.1],
  ['plants', 2.1, 0.5],
];
const READING: Theme = [
  ['aquarium', -1.25, -0.75],
  ['standing', 1.25, 0.35],
  ['plants', -1.2, 0.6],
];
const COFFEE: Theme = [
  ['vending', -1.95, -0.45],
  ['standing', 0, 0.15],
  ['tree', 1.95, -0.3],
];
const FOCUS: Theme = [
  ['booth', -1.15, 0],
  ['booth', 0.65, 0],
  ['tree', 2.25, 0.2],
];
const LOUNGE: Theme = [
  ['beanbags', -1.05, 0],
  ['shelf', 1.75, -0.8],
];
const STUDY: Theme = [
  ['shelf', -1.5, -0.85],
  ['standing', -1.2, 0.55],
  ['printer', 1.6, -0.6],
];
const THEMES: Theme[] = [COFFEE, NOOK, FOCUS, GARDEN, READING];
const LOW_THEMES: Theme[] = [LOUNGE, STUDY];

// The campus: each project's room, side by side as dioramas floating on the
// sky, a project of more desks than a room holds on floors of its own.
// A room holds the target's eight or so; more desks, more floors.
export const ROOM_DESKS = 8;
// Between rooms on the campus: wider front to back, where the walls rise.
const CAMPUS_GAP = 8;
const CAMPUS_GAP_Z = 13;
const CAMPUS_ASPECT = 1.6;
// A project's floors stand on each other over the campus, one building, each
// this far over the one below and a little back.
export const FLOOR_LIFT = 8.5;
const FLOOR_STEP = 3;
// Where a floor past the first really is, for walking in it: far behind the
// campus, clear of every other room (the campus shows it on its building).
const FLOOR_AWAY = 400;

export type RoomPlace = {
  // project#floor.
  key: string;
  project: string;
  floor: number;
  floors: number;
  // Where its room sits, its floor there: what you walk in.
  ox: number;
  oz: number;
  box: Box;
  // How far from there the campus shows it: a floor past the first stands
  // over its building's first, the rest where they are.
  shown: { dx: number; dy: number; dz: number };
  // The room as roomLayout laid it out, in its own frame (the scene draws its
  // diorama from it, moved to ox, oz).
  local: Layout;
  // Its board and where you come in, in the campus's frame.
  board: { x: number; w: number; spot: Spot };
  spawn: Spot;
  // The desks in it, and whether its project's lead is.
  desks: string[];
  lead: boolean;
};

export type Campus = {
  // All of it, as the campus shows it.
  room: Box;
  rooms: RoomPlace[];
  // Every room's areas, obstacles and plants, in the campus's frame.
  areas: (AreaPlace & { room: number })[];
  obstacles: Box[];
  plants: { x: number; z: number }[];
  spawn: Spot;
};

const moveBox = (b: Box, ox: number, oz: number): Box => ({ x0: b.x0 + ox, z0: b.z0 + oz, x1: b.x1 + ox, z1: b.z1 + oz });
const moveSpot = (s: Spot, ox: number, oz: number): Spot => ({ x: s.x + ox, z: s.z + oz, face: s.face });

// floorsOf splits a project's desks across the floors it needs, as evenly as
// it can, in order.
export function floorsOf(n: number): number[] {
  const floors = Math.max(1, Math.ceil(n / ROOM_DESKS));
  const base = Math.floor(n / floors);
  return Array.from({ length: floors }, (_, f) => base + (f < n % floors ? 1 : 0));
}

// layout places a model's projects as a campus: each project a building, one
// room a floor, side by side in rows. A room depends only on its own
// project's desks, so a desk keeps its place while other projects change.
export function layout(model: Pick<OfficeModel, 'areas'>): Campus {
  const parts: { area: AreaIn; floor: number; floors: number }[] = [];
  for (const a of model.areas) {
    const sizes = floorsOf(a.desks.length);
    let at = 0;
    sizes.forEach((n, f) => {
      parts.push({ area: { ...a, desks: a.desks.slice(at, at + n), noLead: f > 0, floor: f + 1 }, floor: f + 1, floors: sizes.length });
      at += n;
    });
  }
  const locals = parts.length ? parts.map((p) => roomLayout({ areas: [p.area] })) : [roomLayout({ areas: [] })];
  // Each building's footprint: its floors' widest and deepest, the steps back
  // included.
  const buildings: number[][] = [];
  for (let i = 0; i < locals.length; i++) {
    const last = buildings[buildings.length - 1];
    if (last && parts[i] && parts[last[0]]?.area.project === parts[i].area.project) last.push(i);
    else buildings.push([i]);
  }
  const footW = (b: number[]) => Math.max(...b.map((i) => locals[i].room.x1));
  const footD = (b: number[]) => Math.max(...b.map((i, k) => locals[i].room.z1 + k * FLOOR_STEP));
  // Rows of buildings: as many to a row as shows them biggest in a window
  // of about the app's shape, seen as the campus's view sees them (a little
  // from the right, from above), their height counted; a wider one when it's
  // about as good.
  const place = (perRow: number) => {
    const at: { ox: number; oz: number }[] = [];
    const rows: number[][] = [];
    for (let b = 0; b < buildings.length; b++) {
      if (!rows.length || rows[rows.length - 1].length >= perRow) rows.push([]);
      rows[rows.length - 1].push(b);
    }
    let z = 0;
    for (const row of rows) {
      let x = 0;
      for (const b of row) {
        // Its floors back a step each from the one below.
        at[b] = { ox: x, oz: z };
        x += footW(buildings[b]) + CAMPUS_GAP;
      }
      z += Math.max(...row.map((b) => footD(buildings[b]))) + CAMPUS_GAP_Z;
    }
    const w = Math.max(...buildings.map((bl, b) => at[b].ox + footW(bl)));
    const d = Math.max(...buildings.map((bl, b) => at[b].oz + footD(bl)));
    // A row narrower than the widest is centred under it: no hole in a corner.
    for (const row of rows) {
      const end = Math.max(...row.map((b) => at[b].ox + footW(buildings[b])));
      for (const b of row) at[b] = { ox: at[b].ox + (w - end) / 2, oz: at[b].oz };
    }
    const tall = Math.max(...buildings.map((bl) => bl.length - 1)) * FLOOR_LIFT;
    return { at, w, d, tall };
  };
  const size = (c: { w: number; d: number; tall: number }) => {
    const pw = c.w * Math.cos(0.3) + c.d * Math.sin(0.3);
    const ph = (c.w * Math.sin(0.3) + c.d * Math.cos(0.3)) * 0.6 + 3 + c.tall * 0.8;
    return Math.max(pw / CAMPUS_ASPECT, ph);
  };
  let best = place(buildings.length);
  for (let k = buildings.length - 1; k >= 1; k--) {
    const c = place(k);
    if (size(c) < size(best) * 0.92) best = c;
  }
  const rooms: RoomPlace[] = [];
  const areas: Campus['areas'] = [];
  const obstacles: Box[] = [];
  const plants: { x: number; z: number }[] = [];
  const shownBoxes: Box[] = [];
  buildings.forEach((bl, b) => {
    const base = best.at[b];
    bl.forEach((i, k) => {
      const l = locals[i];
      const p = parts[i];
      // Shown: on its building, a step back for each floor below.
      const sx = base.ox;
      const sz = base.oz + k * FLOOR_STEP;
      // Walked in: the first floor where it's shown, the rest far behind.
      const ox = sx;
      const oz = k === 0 ? sz : sz + FLOOR_AWAY * k;
      shownBoxes.push(moveBox(l.room, sx, sz));
      for (const a of l.areas)
        areas.push({
          ...a,
          room: i,
          rug: moveBox(a.rug, ox, oz),
          pods: a.pods.map((q) => moveBox(q, ox, oz)),
          lead: a.lead && { x: a.lead.x + ox, z: a.lead.z + oz, spot: moveSpot(a.lead.spot, ox, oz) },
          desks: a.desks.map((d) => ({ ...d, x: d.x + ox, z: d.z + oz, spot: moveSpot(d.spot, ox, oz) })),
          sign: { x: a.sign.x + ox, z: a.sign.z + oz },
        });
      for (const q of l.obstacles) obstacles.push(moveBox(q, ox, oz));
      for (const q of l.plants) plants.push({ x: q.x + ox, z: q.z + oz });
      rooms[i] = {
        key: p ? `${p.area.project}#${p.floor}` : '#1',
        project: p?.area.project ?? '',
        floor: p?.floor ?? 1,
        floors: p?.floors ?? 1,
        ox,
        oz,
        box: moveBox(l.room, ox, oz),
        shown: { dx: sx - ox, dy: k * FLOOR_LIFT, dz: sz - oz },
        local: l,
        board: { x: l.board.x + ox, w: l.board.w, spot: moveSpot(l.board.spot, ox, oz) },
        spawn: moveSpot(l.spawn, ox, oz),
        desks: l.areas.flatMap((a) => a.desks.map((d) => d.id)),
        lead: !!p && !p.area.noLead,
      };
    });
  });
  const room = { x0: 0, z0: 0, x1: Math.max(...shownBoxes.map((q) => q.x1)), z1: Math.max(...shownBoxes.map((q) => q.z1)) };
  return { room, rooms, areas, obstacles, plants, spawn: rooms[0].spawn };
}

// boardRef is a room's board, as a ref.
export const boardRef = (r: RoomPlace) => `board:${r.key}`;

// roomOf is the room a ref (a desk, a lead, a board) is in.
export function roomOf(c: Campus, ref: string): number | undefined {
  if (ref.startsWith('board:')) {
    const i = c.rooms.findIndex((r) => boardRef(r) === ref);
    return i >= 0 ? i : undefined;
  }
  if (ref.endsWith('/lead')) {
    const i = c.rooms.findIndex((r) => r.lead && `${r.project}/lead` === ref);
    return i >= 0 ? i : undefined;
  }
  const i = c.rooms.findIndex((r) => r.desks.includes(ref));
  return i >= 0 ? i : undefined;
}

// roomAt is the room whose floor p is on.
export function roomAt(c: Campus, p: { x: number; z: number }, margin = 0): number | undefined {
  const i = c.rooms.findIndex((r) => p.x >= r.box.x0 - margin && p.x <= r.box.x1 + margin && p.z >= r.box.z0 - margin && p.z <= r.box.z1 + margin);
  return i >= 0 ? i : undefined;
}

// spotOf is where you stand to look at a desk, a lead's counter or a board.
export function spotOf(c: Campus, ref: string): Spot | undefined {
  if (ref.startsWith('board:')) return c.rooms.find((r) => boardRef(r) === ref)?.board.spot;
  for (const a of c.areas) {
    if (a.lead && ref === `${a.project}/lead`) return a.lead.spot;
    for (const d of a.desks) if (d.id === ref) return d.spot;
  }
  return undefined;
}

// nearest is what you're standing at: the desk, counter or board whose spot is
// within reach of p, the closest one.
export function nearest(c: Campus, p: { x: number; z: number }, reach = 1.1): string | undefined {
  let best: string | undefined;
  let bestD = reach * reach;
  const consider = (ref: string, s: Spot) => {
    const d = (s.x - p.x) ** 2 + (s.z - p.z) ** 2;
    if (d < bestD) {
      bestD = d;
      best = ref;
    }
  };
  for (const r of c.rooms) consider(boardRef(r), r.board.spot);
  for (const a of c.areas) {
    if (a.lead) consider(`${a.project}/lead`, a.lead.spot);
    for (const d of a.desks) consider(d.id, d.spot);
  }
  return best;
}

// walkable is the obstacles grown by the walker's radius, which is what a
// point (the walker's centre) mustn't enter.
export function walkable(c: Pick<Campus, 'obstacles'>, radius: number): Box[] {
  return c.obstacles.map((b) => expand(b, radius));
}

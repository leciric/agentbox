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
  wood: Box;
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
// A pod's width: its bench of two desks, and room at either end to stand.
const POD_SPAN = 2 * DESK.w + 2.4;
// Pod to pod across a row, and row to row; every other row is staggered by
// half a pod, so the pods read as islands rather than a classroom's rows.
const POD_PX = 5.2;
const POD_PZ = 3.8;
const STAGGER = POD_PX / 2;
const FIRST = 1.5; // from an area's back to its first pods' desks
const AREA_PAD = 0.5; // the area's margin around its pods
const LEAD_DEPTH = 2.3; // the counter's strip at the front of an area
const AISLE = 2.0; // between areas
const BACK_AISLE = 3.4; // before the board
const FRONT = 1.0; // floor before the front areas
const WALL_PAD = 1.6;
const PLANTER = 0.5;
// The wood-floored side, the commons: about a third of the work's width.
const woodOf = (workW: number) => Math.min(9.2, Math.max(7.4, workW * 0.32));
const MIN_ROOM_D = 12;
const MIN_WORK_W = 8.5;
const COMPACT_WOOD = 5.8;
// The room is about twice as wide as it's deep, as a diorama seen from its
// open corner: areas wrap onto a new row at whatever width comes closest.
const ASPECT = 2.2;

// columns is how many pods an area puts side by side: a block a little wider
// than deep, so its room stays the target's size.
export function columns(pods: number): number {
  if (pods <= 1) return 1;
  const rows = Math.max(1, Math.round(Math.sqrt(pods / 1.5)));
  return Math.ceil(pods / rows);
}

const expand = (b: Box, r: number): Box => ({ x0: b.x0 - r, z0: b.z0 - r, x1: b.x1 + r, z1: b.z1 + r });
const box = (x: number, z: number, w: number, d: number): Box => ({ x0: x - w / 2, z0: z - d / 2, x1: x + w / 2, z1: z + d / 2 });

// layout places a model's areas. It depends only on how many desks each area
// has and their order, so a desk's place changes only when an agent before it
// in its project comes or goes.
// An area as a room lays it out: a floor of a project past the first has no
// lead (its counter is on the first).
type AreaIn = Pick<OfficeModel, 'areas'>['areas'][number] & { noLead?: boolean };

export function roomLayout(model: { areas: AreaIn[] }): Layout {
  // Each area's size, then rows of areas, left to right.
  // Desks sit in pods of up to four, two by two, with floor between the
  // pods; an area is a block of its pods, its lead at the front.
  // A small team's room is small, its commons a lounge and a corner.
  const compact = model.areas.length > 0 && model.areas.reduce((n, a) => n + a.desks.length, 0) <= 4;
  const sizes = model.areas.map((a) => {
    const pods = Math.ceil(a.desks.length / POD);
    const cols = columns(pods);
    const rows = Math.ceil(pods / cols);
    const inner = POD_SPAN + Math.max(0, cols - 1) * POD_PX + (rows > 1 ? STAGGER : 0);
    const w = Math.max(inner, COUNTER.w + 1.2) + AREA_PAD * 2;
    const d = AREA_PAD + FIRST + Math.max(0, rows - 1) * POD_PZ + 1.0 + LEAD_DEPTH;
    return { cols, rows, w, d, inner };
  });
  // Rows of columns of areas: an area stacks under the one before it when
  // it's no wider and the stack stays within a third more than the row's
  // deepest, so small projects fill the floor beside a big one rather than
  // a row of their own; otherwise it starts a column, or past maxRow a row.
  type Row = number[][];
  const colW = (c: number[]) => Math.max(...c.map((i) => sizes[i].w));
  const colD = (c: number[]) => c.reduce((sum, i) => sum + sizes[i].d, 0) + AISLE * (c.length - 1);
  const rowW = (r: Row) => r.reduce((sum, c) => sum + colW(c), 0) + AISLE * Math.max(0, r.length - 1);
  const rowD = (r: Row) => Math.max(...r.map(colD));
  const wrap = (maxRow: number): Row[] => {
    const rows: Row[] = [];
    sizes.forEach((s, i) => {
      const row = rows[rows.length - 1];
      const col = row?.[row.length - 1];
      if (row && col && s.w <= colW(col) + 1e-6 && colD(col) + AISLE + s.d <= rowD(row) * 1.35 + 1e-6) col.push(i);
      else if (row && rowW(row) + AISLE + s.w <= maxRow + 1e-6) row.push([i]);
      else rows.push([[i]]);
    });
    return rows;
  };
  const dims = (rows: Row[]) => {
    const innerW = Math.max(MIN_WORK_W - WALL_PAD * 2, ...rows.map(rowW));
    const workW = innerW + WALL_PAD * 2;
    const deep = rows.reduce((sum, r) => sum + rowD(r), 0) + AISLE * Math.max(0, rows.length - 1);
    const D = Math.max(MIN_ROOM_D, BACK_AISLE + deep + FRONT);
    return { innerW, workW, W: workW + PLANTER + (compact ? COMPACT_WOOD : woodOf(workW)), D };
  };
  // The row width whose room comes closest to the aspect with the least bare
  // floor between the areas, the narrower of two as good.
  const used = sizes.reduce((sum, s) => sum + s.w * s.d, 0);
  const widest = Math.max(0, ...sizes.map((s) => s.w));
  const total = sizes.reduce((sum, s) => sum + s.w, 0) + AISLE * Math.max(0, sizes.length - 1);
  let rows = wrap(total);
  let bestErr = Infinity;
  const tries: number[] = [];
  for (let m = widest; m < total; m += 0.5) tries.push(m);
  tries.push(total);
  for (const m of tries) {
    const rs = wrap(m);
    const d = dims(rs);
    const bare = 1 - used / Math.max(1, d.workW * (d.D - BACK_AISLE));
    const err = Math.abs(Math.log(d.W / d.D / ASPECT)) + 0.8 * bare;
    if (err < bestErr - 1e-3) {
      bestErr = err;
      rows = rs;
    }
  }
  const { innerW, workW, W: roomW, D: roomD } = dims(rows);

  const areas: AreaPlace[] = Array.from({ length: model.areas.length });
  const obstacles: Box[] = [];
  const plants: { x: number; z: number }[] = [];
  // Floor no area uses, which the room furnishes (fill, below). Low says
  // it's before an area, where the close-ups look across it: nothing tall.
  const free: (Box & { low?: boolean })[] = [];
  let z = BACK_AISLE;
  for (const [r, row] of rows.entries()) {
    const depth = rowD(row);
    const last = r === rows.length - 1;
    const below = last ? roomD - 0.5 : z + depth + AISLE / 2;
    // Centred across the work side.
    let x = WALL_PAD + (innerW - rowW(row)) / 2;
    if (x - AISLE / 2 - 1.0 > 2) free.push({ x0: 1.0, z0: z, x1: x - AISLE / 2, z1: z + depth, low: true });
    const end = x + rowW(row);
    if (WALL_PAD + innerW + 0.6 - (end + AISLE / 2) > 2) free.push({ x0: end + AISLE / 2, z0: z, x1: WALL_PAD + innerW + 0.6, z1: z + depth, low: true });
    for (const col of row) {
      const cw = colW(col);
      let cz = z;
      for (const i of col) {
        const s = sizes[i];
        const a = model.areas[i];
        const ad = s.d;
        const ax = x + (cw - s.w) / 2;
        const rug: Box = { x0: ax, z0: cz, x1: ax + s.w, z1: cz + ad };
        placeArea(i, a, s, rug);
        cz += ad + AISLE;
      }
      if (below - (z + colD(col) + AISLE / 2) > 1.9) free.push({ x0: x - 0.2, z0: z + colD(col) + AISLE / 2, x1: x + cw + 0.2, z1: below, low: true });
      x += cw + AISLE;
    }
    z += depth + AISLE;
  }
  // Below the last row, when the room is deeper than its areas.
  if (rows.length === 0) free.push({ x0: 1.0, z0: BACK_AISLE + 0.6, x1: WALL_PAD + innerW + 0.6, z1: roomD - 0.5, low: true });
  function placeArea(i: number, a: AreaIn, s: (typeof sizes)[number], rug: Box) {
    const z = rug.z0;
    const depth = rug.z1 - rug.z0;
    const cx = (rug.x0 + rug.x1) / 2;
    const pods: Box[] = [];
    const podX = (p: number) => cx - s.inner / 2 + POD_SPAN / 2 + (p % s.cols) * POD_PX + (Math.floor(p / s.cols) % 2 ? STAGGER : 0);
    const podZ = (p: number) => z + AREA_PAD + FIRST + Math.floor(p / s.cols) * POD_PZ;
    for (let p = 0; p * POD < a.desks.length; p++) {
      const n = Math.min(POD, a.desks.length - p * POD);
      const px = podX(p);
      const pz = podZ(p);
      // The rug under its bench and chairs, and where you stand at its ends.
      pods.push({ x0: px - 1.95, z0: pz - 1.35, x1: n > 1 ? px + 1.95 : px + 0.45, z1: pz + 0.75 });
    }
    const desks: DeskPlace[] = a.desks.map((d, k) => {
      const p = Math.floor(k / POD);
      const side = k % POD ? 1 : -1;
      const dx = podX(p) + side * (DESK.w / 2);
      const dz = podZ(p);
      obstacles.push(box(dx, dz, DESK.w, DESK.d));
      // The chair, under the agent, at the bench's outer end.
      obstacles.push(box(dx + side * 0.48, dz - SEAT, 0.9, 0.8));
      // You stand at the bench's end, beside the agent, as you'd stop by a
      // colleague's, turned half to the room: the close-up sees your face too.
      return { id: d.id, x: dx, z: dz, side, spot: { x: dx + side * 1.35, z: dz + 0.1, face: side < 0 ? Math.PI / 3 : -Math.PI / 3 } };
    });
    // The counter sits at the area's front, its lead behind it facing you.
    const lz = z + depth - LEAD_DEPTH / 2 - 0.1;
    // Either side of it, between the pods and the rug's front: floor to
    // furnish, clear of the counter's spot.
    const podsFront = pods.reduce((m, p) => Math.max(m, p.z1), z);
    const hasLead = !a.noLead;
    if (rug.z1 - 0.15 - (podsFront + 0.3) > 1.8) {
      const sides = hasLead
        ? [
            { x0: rug.x0 + 0.8, x1: cx - COUNTER.w / 2 - 2.4 },
            { x0: cx + COUNTER.w / 2 + 0.9, x1: rug.x1 - 0.8 },
          ]
        : [{ x0: rug.x0 + 0.8, x1: rug.x1 - 0.8 }];
      for (const sd of sides) if (sd.x1 - sd.x0 > 2.4) free.push({ x0: sd.x0, z0: podsFront + 0.3, x1: sd.x1, z1: rug.z1 - 0.15, low: true });
    }
    if (hasLead) {
      obstacles.push(box(cx, lz, COUNTER.w, COUNTER.d));
      obstacles.push(box(cx, lz - 0.75, 0.7, 0.6));
    }
    areas[i] = { project: a.project, rug, pods, lead: hasLead ? { x: cx, z: lz, spot: { x: cx - 1.65, z: lz + 0.55, face: Math.PI / 2 } } : undefined, desks, sign: { x: cx, z } };
    // The sign's posts, at the rug's back edge.
    obstacles.push(box(cx - 1.35, z + 0.13, 0.12, 0.12), box(cx + 1.35, z + 0.13, 0.12, 0.12));
    plants.push({ x: rug.x0 + 0.35, z: rug.z1 - 0.35 }, { x: rug.x1 - 0.35, z: rug.z1 - 0.35 });
    obstacles.push(box(rug.x0 + 0.35, rug.z1 - 0.35, 0.5, 0.5), box(rug.x1 - 0.35, rug.z1 - 0.35, 0.5, 0.5));
  }
  // The wood-floored side, right of the work, nearest the camera: the
  // commons. A sectional sofa on a rug at the back, its brick wall behind
  // it; a kitchen along the back wall, its island and a few stools; a
  // ping-pong table, arcade cabinets, bean bags; the server racks in the
  // corner. Things to do rather than more chairs. A planter divides it from
  // the work, open at the back to walk round.
  const wood: Box = { x0: workW + PLANTER, z0: 0, x1: roomW, z1: roomD };
  const wx = wood.x0;
  const planter: Box = { x0: workW, z0: Math.min(roomD - 3, Math.max(BACK_AISLE + 2.4, roomD * 0.34)), x1: workW + PLANTER, z1: roomD - 0.35 };
  obstacles.push(planter);
  const props: Prop[] = [];
  const prop = (kind: PropKind, x: number, z: number, rot = 0) => {
    props.push({ kind, x, z, rot });
    // A mat is floor: walked over.
    if (kind === 'mat') return;
    const [w, d] = footOf(kind);
    const turned = Math.abs(Math.sin(rot)) > 0.5;
    obstacles.push(box(x, z, turned ? d : w, turned ? w : d));
  };
  const [kw, kd] = footOf('kitchen');
  const [fw, fd] = footOf('fridge');
  // A fridge beside the kitchen when the lounge keeps room for its sofa.
  const fridge = !compact && roomW - fw - 0.2 - kw - 0.3 - (wx + 0.8) >= 3.2;
  const lounge: Box = compact ? { x0: wx + 0.5, z0: 0.5, x1: wx + 4.1, z1: 4.6 } : { x0: wx + 0.8, z0: 0.5, x1: Math.min(wx + 4.6, roomW - kw - 0.4 - (fridge ? fw + 0.1 : 0)), z1: 4.6 };
  // The sectional along the brick wall, facing the room, its chaise on the
  // left; a low table before it.
  const sofa: Box = { x0: lounge.x0 + 0.3, z0: lounge.z0 + 0.1, x1: lounge.x1 - 0.3, z1: lounge.z0 + 1.05 };
  obstacles.push(sofa, { x0: sofa.x0, z0: sofa.z1, x1: sofa.x0 + 0.95, z1: sofa.z1 + 1.2 });
  obstacles.push(box((sofa.x0 + sofa.x1) / 2 + 0.4, lounge.z0 + 2.35, 1.5, 0.75));
  // The kitchen: a counter and a fridge against the back wall, an island
  // before them with three stools; a small room's, a vending machine.
  if (compact) prop('vending', roomW - footOf('vending')[0] / 2 - 0.05, footOf('vending')[1] / 2 + 0.05);
  else {
    const kx = roomW - (fridge ? fw + 0.1 : 0) - 0.05 - kw / 2;
    if (fridge) prop('fridge', roomW - fw / 2 - 0.05, fd / 2 + 0.02);
    prop('kitchen', kx, kd / 2 + 0.02);
    prop('island', Math.min(kx + 0.3, roomW - footOf('island')[0] / 2 - 0.05), kd + 0.9 + footOf('island')[1] / 2);
  }
  // Past the lounge, each project's commons its own: ping-pong and arcade
  // cabinets, or a coffee corner, or a quiet corner with a fish tank.
  const zp = Math.max(lounge.z1 + 2.6, roomD * 0.42);
  const project = model.areas[0]?.project ?? '';
  let h = 0;
  for (let i = 0; i < project.length; i++) h = (h * 31 + project.charCodeAt(i)) >>> 0;
  const kind = compact ? 3 : h % 3;
  if (kind === 3) {
    prop('plants', wx + 1.6, zp);
    prop('standing', roomW - footOf('standing')[0] / 2 - 0.6, zp);
  } else if (kind === 0) {
    prop('pingpong', Math.min(wx + 4.6, roomW - 1.15 - footOf('pingpong')[0] / 2), zp);
    prop('arcade', roomW - 0.6, zp - 0.5, -Math.PI / 2);
    prop('arcade', roomW - 0.6, zp + 0.45, -Math.PI / 2);
  } else if (kind === 1) {
    prop('standing', wx + 2.6, zp);
    prop('plants', wx + 5.2, zp);
    prop('vending', roomW - 0.75, zp, -Math.PI / 2);
  } else {
    prop('aquarium', wx + 2.8, zp - 0.4);
    prop('booth', wx + 5.6, zp);
    prop('plants', roomW - 1.2, zp + 1.6);
  }
  // Bean bags round a little table, at the front.
  const zb = Math.min(roomD - 2.0, zp + 3.8);
  prop('beanbags', wx + 2.7, zb);
  if (!compact) prop('whiteboard', roomW - 1.3, zb);
  if (roomD > 18) {
    prop('tree', roomW - 0.7, (zp + zb) / 2 + 0.8);
    prop('standing', roomW - 2.6, zp + 2.0 + (zb - zp - 3.8) / 2);
  }
  // The meeting room, in the back left corner, behind glass.
  const meeting: Box = { x0: 0, z0: 0, x1: Math.min(6, workW * 0.3), z1: BACK_AISLE - 0.4 };
  obstacles.push({ x0: meeting.x0, z0: meeting.z0, x1: meeting.x1 + 0.05, z1: meeting.z1 + 0.05 });
  const corners = [
    { x: 0.55, z: roomD - 0.55 },
    { x: wx + 0.5, z: lounge.z1 + 0.6 },
    { x: wx + 0.5, z: roomD - 0.7 },
  ];
  for (const p of corners) {
    plants.push(p);
    obstacles.push(box(p.x, p.z, 0.6, 0.6));
  }
  // The bookshelf against the left wall, at the front, and the server racks
  // beside it: a corner of their own.
  const shelf: Box = { x0: 0, z0: roomD - 3.2, x1: 0.4, z1: roomD - 1.6 };
  obstacles.push(shelf);
  const rack = { x: 0.36, z: shelf.z0 - 0.5 };
  obstacles.push(box(rack.x, rack.z, 0.65, 0.65));
  prop('rack', footOf('rack')[1] / 2 + 0.03, shelf.z0 - 1.3, Math.PI / 2);
  // The board, right of the meeting room, on the work's back wall.
  const boardW = Math.min(5.2, workW - meeting.x1 - 2);
  const boardX = Math.min(workW - boardW / 2 - 0.8, Math.max(workW / 2, meeting.x1 + boardW / 2 + 1));
  obstacles.push(box(boardX, 0.2, boardW, 0.4));
  // Along the back wall either side of the board, low shelves of plants,
  // and a tree in the work's back right corner.
  const backGaps: Box[] = [
    { x0: meeting.x1 + 0.6, z0: 0, x1: boardX - boardW / 2 - 0.6, z1: 0.5 },
    { x0: boardX + boardW / 2 + 0.6, z0: 0, x1: workW - 1.4, z1: 0.5 },
  ];
  // Framed prints over them, where the wall's free.
  const art: number[] = [];
  for (const g of backGaps) {
    const w = g.x1 - g.x0;
    if (w > 2.2) prop('shelf', (g.x0 + g.x1) / 2, footOf('shelf')[1] / 2 + 0.02);
    if (w > 4.4) art.push((g.x0 + g.x1) / 2 - 1.2, (g.x0 + g.x1) / 2 + 1.2);
    else if (w > 1.4) art.push((g.x0 + g.x1) / 2);
  }
  prop('tree', workW - 0.6, 0.6);
  // The left wall: the window past the meeting room, the moss wall after it,
  // lockers after that.
  const winW = Math.min(4.6, Math.max(2.2, (roomD - meeting.z1) * 0.4));
  const window = { at: meeting.z1 + 0.4 + winW / 2, w: winW };
  const mossW = Math.min(3.6, Math.max(1.4, roomD - (window.at + winW / 2) - 1.6));
  const moss = { at: window.at + winW / 2 + 0.4 + mossW / 2, w: mossW };
  for (let lz = moss.at + mossW / 2 + 1.6; lz + 1.25 < shelf.z0 - 1.8; lz += 3.2) prop('lockers', footOf('lockers')[1] / 2 + 0.02, lz, Math.PI / 2);
  // The brick wall behind the lounge, its sign over the sofa.
  const brick = { at: (lounge.x0 + lounge.x1) / 2, w: Math.min(3.6, lounge.x1 - lounge.x0) };
  // The commons' front, when the room is deep.
  if (roomD - 0.9 - (zb + 1.6) > 2.6) free.push({ x0: wx + 0.4, z0: zb + 1.6, x1: roomW - 0.5, z1: roomD - 0.9 });
  // The work's bare floor, furnished: islands of trees, standing tables,
  // a printer and water cooler, a phone booth, bean bags, low shelves, a
  // rolling whiteboard, each on a cell of its own with room to pass.
  let n = 0;
  let themed = 0;
  for (const f of free) {
    const w = f.x1 - f.x0;
    const d = f.z1 - f.z0;
    // Room for a corner of its own: a few things together on a mat.
    if (w >= 5.8 && d >= 3.2) {
      const themes = f.low ? LOW_THEMES : THEMES;
      const cols = Math.floor(w / 5.8);
      const rowsN = Math.max(1, Math.floor(d / 3.4));
      const cw = w / cols;
      const cd = d / rowsN;
      for (let r = 0; r < rowsN; r++)
        for (let c = 0; c < cols; c++) {
          const cx = f.x0 + (c + 0.5) * cw;
          const cz = f.z0 + (r + 0.5) * cd;
          prop('mat', cx, cz);
          for (const [kind, dx, dz] of themes[themed % themes.length]) prop(kind, cx + dx, cz + dz);
          themed++;
        }
      continue;
    }
    const cols = Math.max(1, Math.floor(w / 3.1));
    const rowsN = Math.max(1, Math.round(d / 2.6));
    const cw = w / cols;
    const cd = d / rowsN;
    for (let r = 0; r < rowsN; r++)
      for (let c = 0; c < cols; c++) {
        const kinds = f.low ? LOW : ISLANDS;
        for (let t = 0; t < kinds.length; t++) {
          const kind = kinds[(n + t) % kinds.length];
          const [fw, fd] = footOf(kind);
          if (fw > cw - 0.8 || fd > cd - 0.8) continue;
          prop(kind, f.x0 + (c + 0.5) * cw, f.z0 + (r + 0.5) * cd);
          n += t + 1;
          break;
        }
      }
  }
  return {
    room: { x0: 0, z0: 0, x1: roomW, z1: roomD },
    areas,
    // You stand at the board's left end, out of the way of reading it and of
    // its list, which is on the right.
    board: { x: boardX, w: boardW, spot: { x: boardX - boardW / 2 + 0.2, z: 2.2, face: Math.PI } },
    lobby: wood,
    wood,
    spawn: { x: wx + 2.4, z: lounge.z1 + 1.1, face: -0.5 },
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
  };
}

// The things the room is furnished with, besides desks: their footprints,
// across (x) and deep (z) when not turned; turned a quarter, they swap.
export type PropKind = 'tree' | 'standing' | 'printer' | 'shelf' | 'booth' | 'beanbags' | 'whiteboard' | 'lockers' | 'kitchen' | 'fridge' | 'island' | 'pingpong' | 'arcade' | 'rack' | 'plants' | 'aquarium' | 'vending' | 'logo' | 'mat';
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
export const ROOM_DESKS = 12;
// Between rooms on the campus: wider front to back, where the walls rise.
const CAMPUS_GAP = 8;
const CAMPUS_GAP_Z = 13;
const FLOOR_GAP = 5;
const CAMPUS_ASPECT = 2.1;

export type RoomPlace = {
  // project#floor.
  key: string;
  project: string;
  floor: number;
  floors: number;
  // Where its room sits in the campus, and its floor there.
  ox: number;
  oz: number;
  box: Box;
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
  // All of it.
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

// layout places a model's projects as a campus of rooms. A room depends only
// on its own project's desks, so a desk keeps its place while other projects
// change; rooms are side by side in rows, about twice as wide as deep.
export function layout(model: Pick<OfficeModel, 'areas'>): Campus {
  const parts: { area: AreaIn; floor: number; floors: number }[] = [];
  for (const a of model.areas) {
    const sizes = floorsOf(a.desks.length);
    let at = 0;
    sizes.forEach((n, f) => {
      parts.push({ area: { ...a, desks: a.desks.slice(at, at + n), noLead: f > 0 }, floor: f + 1, floors: sizes.length });
      at += n;
    });
  }
  const locals = parts.length ? parts.map((p) => roomLayout({ areas: [p.area] })) : [roomLayout({ areas: [] })];
  // Rows of rooms: as many to a row as makes the campus closest to its
  // aspect, a project's floors never split across two.
  const place = (perRow: number) => {
    const rows: number[][] = [[]];
    for (let i = 0; i < locals.length; ) {
      let j = i + 1;
      while (j < locals.length && parts[j]?.area.project === parts[i]?.area.project) j++;
      const group = Array.from({ length: j - i }, (_, k) => i + k);
      const row = rows[rows.length - 1];
      if (row.length && row.length + group.length > perRow) rows.push([...group]);
      else row.push(...group);
      i = j;
    }
    const at: { ox: number; oz: number }[] = Array.from({ length: locals.length });
    let z = 0;
    for (const row of rows) {
      let x = 0;
      for (const [j, k] of row.entries()) {
        at[k] = { ox: x, oz: z };
        // A project's floors close together: one place.
        const sameNext = j + 1 < row.length && parts[k]?.area.project === parts[row[j + 1]]?.area.project;
        x += locals[k].room.x1 + (sameNext ? FLOOR_GAP : CAMPUS_GAP);
      }
      z += Math.max(...row.map((k) => locals[k].room.z1)) + CAMPUS_GAP_Z;
    }
    const w = Math.max(...at.map((p, i) => p.ox + locals[i].room.x1));
    const d = Math.max(...at.map((p, i) => p.oz + locals[i].room.z1));
    return { at, w, d };
  };
  let best = place(locals.length);
  for (let k = 1; k <= locals.length; k++) {
    const c = place(k);
    if (Math.abs(Math.log(c.w / c.d / CAMPUS_ASPECT)) < Math.abs(Math.log(best.w / best.d / CAMPUS_ASPECT)) - 1e-6) best = c;
  }
  const rooms: RoomPlace[] = [];
  const areas: Campus['areas'] = [];
  const obstacles: Box[] = [];
  const plants: { x: number; z: number }[] = [];
  locals.forEach((l, i) => {
    const { ox, oz } = best.at[i];
    const p = parts[i];
    for (const a of l.areas)
      areas.push({
        ...a,
        room: i,
        rug: moveBox(a.rug, ox, oz),
        pods: a.pods.map((b) => moveBox(b, ox, oz)),
        lead: a.lead && { x: a.lead.x + ox, z: a.lead.z + oz, spot: moveSpot(a.lead.spot, ox, oz) },
        desks: a.desks.map((d) => ({ ...d, x: d.x + ox, z: d.z + oz, spot: moveSpot(d.spot, ox, oz) })),
        sign: { x: a.sign.x + ox, z: a.sign.z + oz },
      });
    for (const b of l.obstacles) obstacles.push(moveBox(b, ox, oz));
    for (const q of l.plants) plants.push({ x: q.x + ox, z: q.z + oz });
    rooms.push({
      key: p ? `${p.area.project}#${p.floor}` : '#1',
      project: p?.area.project ?? '',
      floor: p?.floor ?? 1,
      floors: p?.floors ?? 1,
      ox,
      oz,
      box: moveBox(l.room, ox, oz),
      local: l,
      board: { x: l.board.x + ox, w: l.board.w, spot: moveSpot(l.board.spot, ox, oz) },
      spawn: moveSpot(l.spawn, ox, oz),
      desks: l.areas.flatMap((a) => a.desks.map((d) => d.id)),
      lead: !!p && !p.area.noLead,
    });
  });
  return { room: { x0: 0, z0: 0, x1: best.w, z1: best.d }, rooms, areas, obstacles, plants, spawn: rooms[0].spawn };
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

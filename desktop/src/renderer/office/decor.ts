// The Office's room as a diorama: a cut-away slab floating on a pale sky,
// its thickness showing, two warm walls and no ceiling, a terrazzo floor with
// a wooden lobby, and the things that make it a place: a big window, a moss
// wall, a brick wall with a framed sign, a planter dividing the lobby, a
// glass meeting room, a server rack. Everything is rounded and toy-like, in
// a pastel and warm-wood palette. Static, so each piece is merged into a few
// meshes (bake) and costs a handful of draws.
import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';
import { bake } from './figures.ts';
import type { Box, Layout } from './layout.ts';
import { furnish } from './props.ts';

export const PALETTE = {
  wall: '#f2dcc0',
  wallTop: '#faeedd',
  wallEdge: '#e7c9a4',
  slab: '#e2b27f',
  slabDark: '#c39262',
  terrazzo: '#f7eee2',
  wood: '#e2b27c',
  frame: '#2f4a3a',
  brick: '#b8664a',
  moss: ['#3f7d3c', '#5c9b46', '#78b35a', '#2f6a33'],
  glass: '#d5ecf2',
};

const std = (color: THREE.ColorRepresentation, o: Partial<THREE.MeshStandardMaterialParameters> = {}) => new THREE.MeshStandardMaterial({ color, roughness: 0.7, ...o });
const rbox = (w: number, h: number, d: number, r = 0.04) => new RoundedBoxGeometry(w, h, d, 3, Math.min(r, w / 2 - 1e-3, h / 2 - 1e-3, d / 2 - 1e-3));

function canvas(w: number, h: number) {
  const c = document.createElement('canvas');
  c.width = w;
  c.height = h;
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.anisotropy = 4;
  return { ctx: c.getContext('2d')!, tex };
}

// terrazzo: cream with chips of warm colour, the floor of the work area.
function terrazzo(): THREE.CanvasTexture {
  const { ctx, tex } = canvas(512, 512);
  ctx.fillStyle = PALETTE.terrazzo;
  ctx.fillRect(0, 0, 512, 512);
  let seed = 11;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  // Few chips, soft: a calm floor, not a speckled one.
  const chips = ['#e9b48f', '#dfa088', '#b7cfaa', '#d6c9b6', '#f1d29e', '#c9bcb0'];
  for (let i = 0; i < 650; i++) {
    ctx.fillStyle = chips[Math.floor(rnd() * chips.length)];
    ctx.globalAlpha = 0.5 + rnd() * 0.3;
    const r = 1 + rnd() * 2.6;
    ctx.beginPath();
    ctx.ellipse(rnd() * 512, rnd() * 512, r, r * (0.6 + rnd() * 0.5), rnd() * 3, 0, Math.PI * 2);
    ctx.fill();
  }
  ctx.globalAlpha = 1;
  tex.wrapS = tex.wrapT = THREE.RepeatWrapping;
  return tex;
}

// planks: long, warm boards for the lobby.
function planks(): THREE.CanvasTexture {
  const { ctx, tex } = canvas(512, 512);
  const rows = 10;
  const h = 512 / rows;
  let seed = 5;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  for (let r = 0; r < rows; r++) {
    let x = -rnd() * 240;
    while (x < 512) {
      const w = 220 + rnd() * 200;
      ctx.fillStyle = `hsl(${31 + rnd() * 4} ${40 + rnd() * 6}% ${69 + rnd() * 5}%)`;
      ctx.fillRect(x, r * h, w, h);
      ctx.fillStyle = 'rgba(255,255,255,0.08)';
      ctx.fillRect(x, r * h, w, 3);
      ctx.fillStyle = 'rgba(120,60,20,0.16)';
      ctx.fillRect(x, r * h, 2, h);
      x += w;
    }
    ctx.fillStyle = 'rgba(120,60,20,0.2)';
    ctx.fillRect(0, r * h, 512, 1.5);
  }
  tex.wrapS = tex.wrapT = THREE.RepeatWrapping;
  return tex;
}

// printTexture is one of the framed prints: soft shapes in the palette.
function printTexture(i: number): THREE.CanvasTexture {
  const { ctx, tex } = canvas(168, 216);
  const bg = ['#f6e7d2', '#e3efe8', '#efe3f4'][i % 3];
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, 168, 216);
  const cols = [
    ['#ff9150', '#ffc94d', '#45cbb8'],
    ['#8b7cf6', '#5aaef0', '#ff7aa2'],
    ['#86c95e', '#ffc94d', '#e077c6'],
  ][i % 3];
  if (i % 3 === 0) {
    ctx.fillStyle = cols[0];
    ctx.beginPath();
    ctx.arc(84, 92, 46, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = cols[2];
    ctx.fillRect(0, 150, 168, 66);
    ctx.fillStyle = cols[1];
    ctx.fillRect(0, 150, 168, 10);
  } else if (i % 3 === 1) {
    for (let k = 0; k < 3; k++) {
      ctx.fillStyle = cols[k];
      ctx.beginPath();
      ctx.roundRect(24 + k * 14, 30 + k * 52, 120 - k * 28, 40, 20);
      ctx.fill();
    }
  } else {
    ctx.fillStyle = cols[0];
    ctx.beginPath();
    ctx.moveTo(20, 190);
    ctx.lineTo(84, 60);
    ctx.lineTo(148, 190);
    ctx.fill();
    ctx.fillStyle = cols[1];
    ctx.beginPath();
    ctx.arc(126, 52, 18, 0, Math.PI * 2);
    ctx.fill();
  }
  return tex;
}

// bricks: a feature wall's worth.
function bricks(): THREE.CanvasTexture {
  const { ctx, tex } = canvas(256, 256);
  ctx.fillStyle = '#e8d6c4';
  ctx.fillRect(0, 0, 256, 256);
  const bh = 256 / 10;
  let seed = 3;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  for (let r = 0; r < 10; r++)
    for (let c = -1; c < 5; c++) {
      const x = c * 64 + (r % 2 ? 32 : 0);
      ctx.fillStyle = `hsl(${12 + rnd() * 8} ${45 + rnd() * 12}% ${44 + rnd() * 9}%)`;
      ctx.beginPath();
      ctx.roundRect(x + 2, r * bh + 2, 60, bh - 4, 3);
      ctx.fill();
    }
  tex.wrapS = tex.wrapT = THREE.RepeatWrapping;
  return tex;
}

// wallHeight is the walls' height: a quarter of the room's depth or so, low
// enough that the overview looks over them into the room, as a diorama.
export function wallHeight(l: Layout): number {
  return Math.min(4.6, Math.max(3.4, l.room.z1 * 0.24));
}

export type Diorama = { group: THREE.Group; dispose(): void; meeting: THREE.Vector3 };

// diorama builds the room for a layout: slab, floors, walls and what's on
// them, the lobby's planter and rack, the meeting room.
export function diorama(l: Layout, sign: string): Diorama {
  const own: { dispose(): void }[] = [];
  const keep = <T extends { dispose(): void }>(x: T) => (own.push(x), x);
  // Materials shared by colour and finish, so the bake merges what shares one.
  const made = new Map<string, THREE.MeshStandardMaterial>();
  const m = (c: THREE.ColorRepresentation, o?: Partial<THREE.MeshStandardMaterialParameters>) => {
    const key = o && Object.values(o).some((v) => typeof v === 'object') ? undefined : `${new THREE.Color(c).getHexString()}|${JSON.stringify(o ?? {})}`;
    const hit = key ? made.get(key) : undefined;
    if (hit) return hit;
    const x = keep(std(c, o));
    if (key) made.set(key, x);
    return x;
  };
  const g = new THREE.Group();
  const parts: THREE.Mesh[] = [];
  const put = (geo: THREE.BufferGeometry, mat: THREE.Material, x: number, y: number, z: number, rot?: [number, number, number]) => {
    keep(geo);
    const o = new THREE.Mesh(geo, mat);
    o.position.set(x, y, z);
    if (rot) o.rotation.set(...rot);
    o.castShadow = true;
    o.receiveShadow = true;
    g.add(o);
    parts.push(o);
    return o;
  };
  const W = l.room.x1;
  const D = l.room.z1;
  const T = 0.62; // the slab's thickness
  const WALL = wallHeight(l);
  const WT = 0.22; // walls' thickness

  // The slab, lit on its edges, darker below.
  const slab = m(PALETTE.slab, { roughness: 0.6 });
  put(rbox(W + 0.5, T, D + 0.5, 0.08), slab, W / 2, -T / 2 - 0.001, D / 2);
  put(rbox(W + 0.3, 0.06, D + 0.3, 0.03), m(PALETTE.slabDark), W / 2, -T - 0.02, D / 2);

  // The floors: terrazzo for the work, planks for the wood side.
  const WX = l.wood.x0;
  const tz = keep(terrazzo());
  tz.repeat.set(WX / 3, D / 3);
  const tzFloor = new THREE.Mesh(keep(new THREE.PlaneGeometry(WX, D)), m('#ffffff', { map: tz, roughness: 0.55 }));
  tzFloor.rotation.x = -Math.PI / 2;
  tzFloor.position.set(WX / 2, 0.001, D / 2);
  tzFloor.receiveShadow = true;
  tzFloor.userData.floor = true;
  g.add(tzFloor);
  const pk = keep(planks());
  // The boards run along the room, towards the camera's corner.
  pk.rotation = Math.PI / 2;
  pk.repeat.set(D / 4, (W - WX) / 4);
  const woodFloor = new THREE.Mesh(keep(new THREE.PlaneGeometry(W - WX, D)), m('#ffffff', { map: pk, roughness: 0.6 }));
  woodFloor.rotation.x = -Math.PI / 2;
  woodFloor.position.set((WX + W) / 2, 0.002, D / 2);
  woodFloor.receiveShadow = true;
  woodFloor.userData.floor = true;
  g.add(woodFloor);

  // The two walls, back and left, with a lighter cap and a skirting.
  const wall = m(PALETTE.wall, { roughness: 0.85 });
  const cap = m(PALETTE.wallTop, { roughness: 0.8 });
  const skirt = m(PALETTE.wallEdge);
  put(rbox(W + WT, WALL, WT, 0.03), wall, W / 2 - WT / 2, WALL / 2, -WT / 2);
  put(rbox(WT, WALL, D + WT, 0.03), wall, -WT / 2, WALL / 2, D / 2 - WT / 2);
  put(rbox(W + WT + 0.04, 0.06, WT + 0.06, 0.02), cap, W / 2 - WT / 2, WALL + 0.02, -WT / 2);
  put(rbox(WT + 0.06, 0.06, D + WT + 0.04, 0.02), cap, -WT / 2, WALL + 0.02, D / 2 - WT / 2);
  put(rbox(W, 0.12, 0.04, 0.01), skirt, W / 2, 0.06, 0.02);
  put(rbox(0.04, 0.12, D, 0.01), skirt, 0.02, 0.06, D / 2);

  const frame = m(PALETTE.frame, { roughness: 0.5 });

  // The meeting room, in the back left corner: glass on two sides, a round
  // table and chairs.
  const mr = l.meeting;
  const glass = keep(new THREE.MeshStandardMaterial({ color: '#bfe1ee', transparent: true, opacity: 0.42, roughness: 0.08, metalness: 0.1, depthWrite: false }));
  const gw = mr.x1 - mr.x0;
  const gd = mr.z1 - mr.z0;
  const glassFront = new THREE.Mesh(keep(new THREE.PlaneGeometry(gw, 2.2)), glass);
  glassFront.position.set(mr.x0 + gw / 2, 1.1, mr.z1);
  const glassSide = new THREE.Mesh(keep(new THREE.PlaneGeometry(gd, 2.2)), glass);
  glassSide.rotation.y = Math.PI / 2;
  glassSide.position.set(mr.x1, 1.1, mr.z0 + gd / 2);
  for (const p of [glassFront, glassSide]) {
    p.renderOrder = 3;
    g.add(p);
  }
  // Its frame: posts and rails, dark green.
  for (const x of [mr.x0 + 0.05, mr.x0 + gw / 2, mr.x1 - 0.6]) put(rbox(0.06, 2.2, 0.06, 0.02), frame, x, 1.1, mr.z1);
  put(rbox(0.06, 2.2, 0.06, 0.02), frame, mr.x1, 1.1, mr.z1);
  put(rbox(0.06, 2.2, 0.06, 0.02), frame, mr.x1, 1.1, mr.z0 + gd / 2);
  put(rbox(gw, 0.06, 0.06, 0.02), frame, mr.x0 + gw / 2, 2.2, mr.z1);
  put(rbox(0.06, 0.06, gd, 0.02), frame, mr.x1, 2.2, mr.z0 + gd / 2);
  put(rbox(gw, 0.05, 0.05, 0.02), frame, mr.x0 + gw / 2, 0.03, mr.z1);
  put(rbox(0.05, 0.05, gd, 0.02), frame, mr.x1, 0.03, mr.z0 + gd / 2);
  const tcx = mr.x0 + gw * 0.55;
  const tcz = mr.z0 + gd * 0.55;
  const oak = m('#e1a86d', { roughness: 0.5 });
  put(keep(new THREE.CylinderGeometry(0.55, 0.55, 0.06, 32)), oak, tcx, 0.74, tcz);
  put(keep(new THREE.CylinderGeometry(0.05, 0.08, 0.72, 12)), m('#3b3f46'), tcx, 0.36, tcz);
  const seatCols = ['#e9b44c', '#e07a5f', '#4aa3a2', '#e9b44c'];
  for (let i = 0; i < 4; i++) {
    const a = (i / 4) * Math.PI * 2 + 0.4;
    const sx = tcx + Math.sin(a) * 0.85;
    const sz = tcz + Math.cos(a) * 0.85;
    const c = m(seatCols[i]);
    put(rbox(0.38, 0.08, 0.38, 0.04), c, sx, 0.46, sz);
    put(rbox(0.36, 0.32, 0.06, 0.03), c, sx + Math.sin(a) * 0.17, 0.66, sz + Math.cos(a) * 0.17, [0, a, 0]);
    put(keep(new THREE.CylinderGeometry(0.02, 0.02, 0.44, 8)), m('#3b3f46'), sx, 0.22, sz);
  }
  // A sofa against its back wall, sage green.
  const sage = m('#9cb89a', { roughness: 0.9 });
  put(rbox(1.6, 0.36, 0.7, 0.12), sage, mr.x0 + gw * 0.45, 0.22, mr.z0 + 0.5);
  put(rbox(1.6, 0.5, 0.2, 0.08), sage, mr.x0 + gw * 0.45, 0.6, mr.z0 + 0.18);

  // The left wall: a big gridded window and a moss wall.
  const win = l.window;
  const sky = canvas(256, 256);
  const gsky = sky.ctx.createLinearGradient(0, 0, 0, 256);
  gsky.addColorStop(0, '#bfe2f5');
  gsky.addColorStop(1, '#f4e6c8');
  sky.ctx.fillStyle = gsky;
  sky.ctx.fillRect(0, 0, 256, 256);
  sky.ctx.fillStyle = '#d9c2a0';
  for (let x = 0; x < 256; x += 22) sky.ctx.fillRect(x, 200 - ((x * 7) % 60), 18, 60 + ((x * 7) % 60));
  keep(sky.tex);
  const pane = new THREE.Mesh(keep(new THREE.PlaneGeometry(win.w, 1.7)), keep(new THREE.MeshBasicMaterial({ map: sky.tex })));
  pane.rotation.y = Math.PI / 2;
  pane.position.set(0.012, 1.55, win.at);
  g.add(pane);
  const cols = Math.max(3, Math.round(win.w / 0.55));
  for (let i = 0; i <= cols; i++) put(rbox(0.05, 1.8, 0.05, 0.015), frame, 0.03, 1.55, win.at - win.w / 2 + (i * win.w) / cols);
  for (const y of [0.7, 1.55, 2.4]) put(rbox(0.05, 0.05, win.w + 0.05, 0.015), frame, 0.03, y, win.at);
  put(rbox(0.16, 0.05, win.w + 0.1, 0.02), m('#f7e2c4'), 0.08, 0.66, win.at);
  // The moss wall: a dark frame full of soft green tufts.
  const moss = l.moss;
  put(rbox(0.08, 1.9, moss.w + 0.12, 0.03), frame, 0.04, 1.45, moss.at);
  const tuft = keep(new THREE.SphereGeometry(1, 10, 8));
  const mossMats = PALETTE.moss.map((c) => m(c, { roughness: 0.95 }));
  let seed = 17;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  const tufts: THREE.Mesh[] = [];
  for (let i = 0; i < Math.round(moss.w * 55); i++) {
    const t = new THREE.Mesh(tuft, mossMats[i % mossMats.length]);
    const r = 0.05 + rnd() * 0.06;
    t.scale.set(r * 0.6, r, r);
    t.position.set(0.1, 0.6 + rnd() * 1.7, moss.at - moss.w / 2 + rnd() * moss.w);
    g.add(t);
    tufts.push(t);
  }

  // The back wall: a brick feature wall with a framed sign.
  const br = l.brick;
  const brickTex = keep(bricks());
  brickTex.repeat.set(br.w / 1.2, 2.4);
  put(rbox(br.w, WALL - 0.06, 0.05, 0.01), m('#ffffff', { map: brickTex, roughness: 0.9 }), br.at, (WALL - 0.06) / 2, 0.025);
  const signC = canvas(512, 256);
  signC.ctx.fillStyle = '#1f2a3a';
  signC.ctx.fillRect(0, 0, 512, 256);
  signC.ctx.fillStyle = '#f6efe3';
  signC.ctx.font = '700 68px "Inter Variable", Inter, system-ui, sans-serif';
  signC.ctx.textAlign = 'center';
  signC.ctx.fillText(sign, 256, 140);
  signC.ctx.fillStyle = '#e8a273';
  signC.ctx.fillRect(196, 172, 120, 8);
  keep(signC.tex);
  put(rbox(1.5, 0.82, 0.06, 0.02), m('#6b4128'), br.at, 1.95, 0.08);
  const signFace = new THREE.Mesh(keep(new THREE.PlaneGeometry(1.36, 0.68)), keep(new THREE.MeshBasicMaterial({ map: signC.tex })));
  signFace.position.set(br.at, 1.95, 0.115);
  g.add(signFace);

  // The planter dividing the lobby: a long wooden box of round shrubs.
  const pl = l.planter;
  const box = m('#b57846', { roughness: 0.6 });
  put(rbox(pl.x1 - pl.x0, 0.38, pl.z1 - pl.z0, 0.05), box, (pl.x0 + pl.x1) / 2, 0.19, (pl.z0 + pl.z1) / 2);
  const shrub = keep(new THREE.SphereGeometry(1, 14, 10));
  const greens = [m('#4f9a52', { roughness: 0.85 }), m('#6db35a', { roughness: 0.85 })];
  const alongZ = pl.z1 - pl.z0 > pl.x1 - pl.x0;
  const len = alongZ ? pl.z1 - pl.z0 : pl.x1 - pl.x0;
  const n = Math.max(3, Math.floor(len / 0.36));
  for (let i = 0; i < n; i++) {
    const s = new THREE.Mesh(shrub, greens[i % 2]);
    const r = 0.2 + ((i * 7) % 5) * 0.012;
    s.scale.set(r, r * 0.85, r);
    const at = 0.2 + (i * (len - 0.4)) / Math.max(1, n - 1);
    if (alongZ) s.position.set((pl.x0 + pl.x1) / 2, 0.5, pl.z0 + at);
    else s.position.set(pl.x0 + at, 0.5, (pl.z0 + pl.z1) / 2);
    g.add(s);
    tufts.push(s);
  }

  // The server rack, in the lobby's corner, its lights blinking green.
  const rk = l.rack;
  put(rbox(0.6, 1.5, 0.6, 0.05), m('#1e242c', { roughness: 0.4 }), rk.x, 0.75, rk.z);
  const led = keep(new THREE.MeshBasicMaterial({ color: '#45e08a', toneMapped: false }));
  const ledBlue = keep(new THREE.MeshBasicMaterial({ color: '#5ab8ff', toneMapped: false }));
  for (let i = 0; i < 6; i++) put(rbox(0.32, 0.03, 0.02, 0.01), i % 3 === 2 ? ledBlue : led, rk.x - 0.31, 0.35 + i * 0.18, rk.z, [0, -Math.PI / 2, 0]);

  // The lounge: a sectional sofa along the brick wall, facing the room, its
  // chaise on the left, cushions and a throw; a low table with mugs and a
  // book before it, a floor lamp at its end, all on a soft rug.
  const lg = l.lounge;
  const sf = l.sofa;
  const lcx = (lg.x0 + lg.x1) / 2;
  put(rbox(lg.x1 - lg.x0, 0.02, lg.z1 - lg.z0, 0.01), m('#d7e4cf', { roughness: 0.95 }), lcx, 0.012, (lg.z0 + lg.z1) / 2);
  const fabric = m('#9cb89a', { roughness: 0.95 });
  const cushion = m('#b5cdb2', { roughness: 0.95 });
  const sw = sf.x1 - sf.x0;
  const scx = (sf.x0 + sf.x1) / 2;
  put(rbox(sw, 0.36, 0.95, 0.12), fabric, scx, 0.22, sf.z0 + 0.48);
  put(rbox(sw, 0.62, 0.24, 0.1), fabric, scx, 0.52, sf.z0 + 0.12);
  put(rbox(0.95, 0.36, 1.25, 0.12), fabric, sf.x0 + 0.475, 0.22, sf.z1 + 0.6);
  put(rbox(0.22, 0.52, 2.2, 0.09), fabric, sf.x0 + 0.11, 0.42, sf.z0 + 1.1);
  put(rbox(0.22, 0.52, 0.95, 0.09), fabric, sf.x1 - 0.11, 0.42, sf.z0 + 0.48);
  const seats = Math.max(2, Math.round((sw - 0.5) / 1.0));
  for (let i = 0; i < seats; i++) {
    const x = sf.x0 + 0.3 + ((i + 0.5) * (sw - 0.55)) / seats;
    put(rbox((sw - 0.6) / seats - 0.05, 0.14, 0.72, 0.07), cushion, x, 0.46, sf.z0 + 0.55);
    put(rbox((sw - 0.6) / seats - 0.08, 0.42, 0.16, 0.08), cushion, x, 0.75, sf.z0 + 0.3, [-0.12, 0, 0]);
  }
  put(rbox(0.68, 0.14, 1.0, 0.07), cushion, sf.x0 + 0.5, 0.46, sf.z1 + 0.62);
  for (const [i, c] of ['#ffc94d', '#ff7aa2', '#5aaef0'].entries()) {
    const p = put(rbox(0.34, 0.32, 0.12, 0.1), m(c, { roughness: 0.9 }), sf.x0 + 0.7 + i * (sw - 1.4) / 2, 0.72, sf.z0 + 0.42, [-0.2, (i - 1) * 0.3, (i - 1) * 0.12]);
    p.castShadow = true;
  }
  put(rbox(0.75, 0.03, 0.6, 0.02), m('#e9b44c', { roughness: 0.95 }), sf.x0 + 0.5, 0.545, sf.z1 + 0.9, [0, 0.2, 0]);
  // The low table.
  const ltx = scx + 0.4;
  const ltz = lg.z0 + 2.35;
  put(rbox(1.5, 0.06, 0.75, 0.2), m('#e1a86d', { roughness: 0.45 }), ltx, 0.4, ltz);
  for (const [dx, dz] of [[-0.6, -0.25], [0.6, -0.25], [-0.6, 0.25], [0.6, 0.25]]) put(keep(new THREE.CylinderGeometry(0.025, 0.02, 0.38, 8)), m('#3b3f46'), ltx + dx, 0.19, ltz + dz);
  put(keep(new THREE.CylinderGeometry(0.04, 0.035, 0.09, 14)), m('#fbfaf7', { roughness: 0.3 }), ltx + 0.35, 0.475, ltz + 0.1);
  put(keep(new THREE.CylinderGeometry(0.04, 0.035, 0.09, 14)), m('#ff7aa2', { roughness: 0.3 }), ltx + 0.15, 0.475, ltz - 0.15);
  put(rbox(0.32, 0.04, 0.24, 0.01), m('#2f5a4c', { roughness: 0.7 }), ltx - 0.35, 0.45, ltz, [0, 0.3, 0]);
  // A floor lamp at its right end, warm.
  put(keep(new THREE.CylinderGeometry(0.015, 0.015, 1.5, 8)), m('#3b3f46'), sf.x1 + 0.3, 0.75, sf.z1 + 0.9);
  put(keep(new THREE.CylinderGeometry(0.16, 0.18, 0.02, 20)), m('#3b3f46'), sf.x1 + 0.3, 0.01, sf.z1 + 0.9);
  put(keep(new THREE.ConeGeometry(0.22, 0.26, 20, 1, true)), m('#fff1d6', { emissive: '#ffd59a', emissiveIntensity: 0.6, side: THREE.DoubleSide }), sf.x1 + 0.3, 1.55, sf.z1 + 0.9);

  // A tall bookshelf against the left wall, at the front.
  const sh = l.shelf;
  const shZ = (sh.z0 + sh.z1) / 2;
  const shD = sh.z1 - sh.z0;
  put(rbox(0.38, 1.9, shD, 0.03), m('#c08b5c', { roughness: 0.6 }), 0.19, 0.95, shZ);
  const books = ['#c4622d', '#2f5a4c', '#e9b44c', '#7c8bd6', '#f4efe6', '#a8502f'].map((c) => m(c));
  for (let row = 0; row < 4; row++) {
    put(rbox(0.36, 0.03, shD - 0.08, 0.01), m('#a87648', { roughness: 0.6 }), 0.22, 0.2 + row * 0.45, shZ);
    for (let i = 0; i < 10; i++) {
      if ((i + row * 3) % 7 === 3) continue;
      const h = 0.24 + ((i * 7 + row) % 4) * 0.03;
      put(rbox(0.24, h, 0.09, 0.01), books[(i + row * 2) % books.length], 0.26, 0.22 + row * 0.45 + h / 2, sh.z0 + 0.12 + i * ((shD - 0.24) / 9));
    }
  }

  // On the work's back wall, either side of the board: framed prints.
  const prints = [printTexture(0), printTexture(1), printTexture(2)].map((t) => keep(t));
  const frameM = m('#f4efe6', { roughness: 0.5 });
  let pi = 0;
  for (const x of l.art) {
    put(rbox(1.0, 1.25, 0.05, 0.02), frameM, x, 2.45, 0.03);
    const face = new THREE.Mesh(keep(new THREE.PlaneGeometry(0.84, 1.08)), m('#ffffff', { map: prints[pi++ % prints.length], roughness: 0.7 }));
    face.position.set(x, 2.45, 0.06);
    g.add(face);
  }
  // A clock over the meeting room.
  {
    const cx = (l.meeting.x0 + l.meeting.x1) / 2;
    put(keep(new THREE.CylinderGeometry(0.32, 0.32, 0.06, 32)), m('#ffffff', { roughness: 0.4 }), cx, 2.95, 0.04, [Math.PI / 2, 0, 0]);
    put(keep(new THREE.TorusGeometry(0.32, 0.03, 8, 32)), m('#2f4a3a', { roughness: 0.5 }), cx, 2.95, 0.06);
    put(rbox(0.03, 0.2, 0.01, 0.005), m('#1f2a3a'), cx, 3.03, 0.08);
    put(rbox(0.15, 0.03, 0.01, 0.005), m('#1f2a3a'), cx + 0.07, 2.95, 0.08);
  }
  // The meeting room's screen, a chart on it.
  {
    const tv = canvas(256, 144);
    const c = tv.ctx;
    c.fillStyle = '#1f2433';
    c.fillRect(0, 0, 256, 144);
    const bars = [40, 62, 50, 84, 72, 104];
    for (const [i, h] of bars.entries()) {
      c.fillStyle = i === bars.length - 1 ? '#a78bfa' : '#5aaef0';
      c.fillRect(28 + i * 36, 122 - h, 22, h);
    }
    c.fillStyle = '#e6e8ee';
    c.font = '700 16px system-ui, sans-serif';
    c.fillText('Merged this week', 24, 24);
    keep(tv.tex);
    const tvX = mr.x0 + gw * 0.5;
    put(rbox(1.5, 0.88, 0.05, 0.02), m('#15181f', { roughness: 0.3 }), tvX, 1.75, 0.04);
    const scr = new THREE.Mesh(keep(new THREE.PlaneGeometry(1.42, 0.8)), keep(new THREE.MeshBasicMaterial({ map: tv.tex })));
    scr.position.set(tvX, 1.75, 0.07);
    g.add(scr);
  }

  // Everything else the room is furnished with.
  furnish(g, l.props, { m, keep, parts, soft: tufts, glass });

  bake(g, parts);
  bake(g, tufts, false);
  return {
    group: g,
    meeting: new THREE.Vector3(mr.x0 + gw / 2, 0, mr.z1),
    dispose() {
      for (const o of own) o.dispose();
      g.traverse((o) => (o as THREE.Mesh).isMesh && (o as THREE.Mesh).geometry.dispose());
    },
  };
}

export type { Box };

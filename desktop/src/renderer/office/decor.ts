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

export const PALETTE = {
  wall: '#efcfa5',
  wallTop: '#f7e2c4',
  wallEdge: '#e2b98a',
  slab: '#e6b77f',
  slabDark: '#c99862',
  terrazzo: '#f4e4cf',
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
  const chips = ['#e8a273', '#d97b5a', '#9fbf8f', '#c9b8a0', '#f0c27a', '#b9a89a'];
  for (let i = 0; i < 900; i++) {
    ctx.fillStyle = chips[Math.floor(rnd() * chips.length)];
    ctx.globalAlpha = 0.55 + rnd() * 0.4;
    const r = 1 + rnd() * 3.2;
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
      ctx.fillStyle = `hsl(${31 + rnd() * 5} ${46 + rnd() * 8}% ${63 + rnd() * 6}%)`;
      ctx.fillRect(x, r * h, w, h);
      ctx.fillStyle = 'rgba(255,255,255,0.08)';
      ctx.fillRect(x, r * h, w, 3);
      ctx.fillStyle = 'rgba(120,60,20,0.25)';
      ctx.fillRect(x, r * h, 2, h);
      x += w;
    }
    ctx.fillStyle = 'rgba(120,60,20,0.3)';
    ctx.fillRect(0, r * h, 512, 1.5);
  }
  tex.wrapS = tex.wrapT = THREE.RepeatWrapping;
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

export type Diorama = { group: THREE.Group; dispose(): void; meeting: THREE.Vector3 };

// diorama builds the room for a layout: slab, floors, walls and what's on
// them, the lobby's planter and rack, the meeting room.
export function diorama(l: Layout, sign: string): Diorama {
  const own: { dispose(): void }[] = [];
  const keep = <T extends { dispose(): void }>(x: T) => (own.push(x), x);
  const m = (c: THREE.ColorRepresentation, o?: Partial<THREE.MeshStandardMaterialParameters>) => keep(std(c, o));
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
  const T = 0.42; // the slab's thickness
  const WALL = 4.4;
  const WT = 0.22; // walls' thickness

  // The slab, lit on its edges, darker below.
  const slab = m(PALETTE.slab, { roughness: 0.6 });
  put(rbox(W + 0.5, T, D + 0.5, 0.08), slab, W / 2, -T / 2 - 0.001, D / 2);
  put(rbox(W + 0.3, 0.06, D + 0.3, 0.03), m(PALETTE.slabDark), W / 2, -T - 0.02, D / 2);

  // The floors: terrazzo for the work, planks for the lobby.
  const tz = keep(terrazzo());
  tz.repeat.set(W / 3, l.lobby.z0 / 3);
  const tzFloor = new THREE.Mesh(keep(new THREE.PlaneGeometry(W, l.lobby.z0)), m('#ffffff', { map: tz, roughness: 0.55 }));
  tzFloor.rotation.x = -Math.PI / 2;
  tzFloor.position.set(W / 2, 0.001, l.lobby.z0 / 2);
  tzFloor.receiveShadow = true;
  tzFloor.userData.floor = true;
  g.add(tzFloor);
  const pk = keep(planks());
  pk.repeat.set(W / 4, (D - l.lobby.z0) / 4);
  const woodFloor = new THREE.Mesh(keep(new THREE.PlaneGeometry(W, D - l.lobby.z0)), m('#ffffff', { map: pk, roughness: 0.6 }));
  woodFloor.rotation.x = -Math.PI / 2;
  woodFloor.position.set(W / 2, 0.002, (l.lobby.z0 + D) / 2);
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
  const glass = keep(new THREE.MeshStandardMaterial({ color: PALETTE.glass, transparent: true, opacity: 0.28, roughness: 0.1, depthWrite: false }));
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
  const n = Math.max(3, Math.floor((pl.x1 - pl.x0) / 0.36));
  for (let i = 0; i < n; i++) {
    const s = new THREE.Mesh(shrub, greens[i % 2]);
    const r = 0.2 + ((i * 7) % 5) * 0.012;
    s.scale.set(r, r * 0.85, r);
    s.position.set(pl.x0 + 0.2 + (i * (pl.x1 - pl.x0 - 0.4)) / Math.max(1, n - 1), 0.5, (pl.z0 + pl.z1) / 2);
    g.add(s);
    tufts.push(s);
  }

  // The server rack, in the lobby's corner, its lights blinking green.
  const rk = l.rack;
  put(rbox(0.6, 1.5, 0.6, 0.05), m('#1e242c', { roughness: 0.4 }), rk.x, 0.75, rk.z);
  const led = keep(new THREE.MeshBasicMaterial({ color: '#45e08a', toneMapped: false }));
  const ledBlue = keep(new THREE.MeshBasicMaterial({ color: '#5ab8ff', toneMapped: false }));
  for (let i = 0; i < 6; i++) put(rbox(0.32, 0.03, 0.02, 0.01), i % 3 === 2 ? ledBlue : led, rk.x - 0.31, 0.35 + i * 0.18, rk.z, [0, -Math.PI / 2, 0]);

  // The lounge: a rug, two armchairs and a low table, a floor lamp, a shelf.
  const lg = l.lounge;
  const lcx = (lg.x0 + lg.x1) / 2;
  const lcz = (lg.z0 + lg.z1) / 2;
  put(rbox(lg.x1 - lg.x0, 0.02, lg.z1 - lg.z0, 0.01), m('#cfe0c6', { roughness: 0.95 }), lcx, 0.012, lcz);
  const chairM = [m('#e9b44c', { roughness: 0.8 }), m('#4aa3a2', { roughness: 0.8 })];
  for (const [i, dx] of [-0.75, 0.75].entries()) {
    const c = chairM[i];
    put(rbox(0.75, 0.32, 0.7, 0.14), c, lcx + dx, 0.24, lcz - 0.25);
    put(rbox(0.75, 0.5, 0.18, 0.08), c, lcx + dx, 0.55, lcz - 0.58);
    for (const sx of [-0.33, 0.33]) put(rbox(0.12, 0.34, 0.7, 0.06), c, lcx + dx + sx, 0.42, lcz - 0.25);
  }
  put(keep(new THREE.CylinderGeometry(0.4, 0.4, 0.05, 28)), m('#e1a86d', { roughness: 0.5 }), lcx, 0.38, lcz + 0.45);
  put(keep(new THREE.CylinderGeometry(0.05, 0.08, 0.36, 10)), m('#3b3f46'), lcx, 0.18, lcz + 0.45);
  // A floor lamp, warm.
  put(keep(new THREE.CylinderGeometry(0.015, 0.015, 1.5, 8)), m('#3b3f46'), lg.x0 + 0.25, 0.75, lg.z0 + 0.3);
  put(keep(new THREE.ConeGeometry(0.22, 0.26, 20, 1, true)), m('#fff1d6', { emissive: '#ffd59a', emissiveIntensity: 0.6, side: THREE.DoubleSide }), lg.x0 + 0.25, 1.55, lg.z0 + 0.3);
  // A bookshelf against the left wall, by the lounge.
  const shelfZ = Math.max(lg.z0 - 0.9, l.planter.z1 + 0.6);
  put(rbox(0.36, 1.6, 1.4, 0.03), m('#c08b5c', { roughness: 0.6 }), 0.2, 0.8, shelfZ);
  const books = ['#c4622d', '#2f5a4c', '#e9b44c', '#7c8bd6', '#f4efe6', '#a8502f'].map((c) => m(c));
  for (let row = 0; row < 3; row++)
    for (let i = 0; i < 9; i++) {
      const h = 0.22 + ((i * 7 + row) % 4) * 0.03;
      put(rbox(0.22, h, 0.09, 0.01), books[(i + row * 2) % books.length], 0.24, 0.28 + row * 0.48 + h / 2, shelfZ - 0.58 + i * 0.135);
    }

  // The café: round tables with two chairs each, a counter with cups.
  if (l.cafe) {
    const cf = l.cafe;
    const cw = cf.x1 - cf.x0;
    const cd = cf.z1 - cf.z0;
    put(rbox(cw, 0.02, cd, 0.01), m('#f1dcc2', { roughness: 0.95 }), (cf.x0 + cf.x1) / 2, 0.012, (cf.z0 + cf.z1) / 2);
    const tables = Math.max(1, Math.floor(cd / 1.8));
    const seats = [m('#e07a5f', { roughness: 0.8 }), m('#8fb39a', { roughness: 0.8 }), m('#7c8bd6', { roughness: 0.8 })];
    for (let i = 0; i < tables; i++) {
      const tx = cf.x0 + cw / 2;
      const tzz = cf.z0 + 0.9 + i * 1.8;
      put(keep(new THREE.CylinderGeometry(0.38, 0.38, 0.05, 24)), oak, tx, 0.74, tzz);
      put(keep(new THREE.CylinderGeometry(0.04, 0.12, 0.72, 10)), m('#3b3f46'), tx, 0.36, tzz);
      for (const sx of [-0.65, 0.65]) {
        put(rbox(0.36, 0.08, 0.36, 0.05), seats[(i + (sx > 0 ? 1 : 0)) % 3], tx + sx, 0.46, tzz);
        put(keep(new THREE.CylinderGeometry(0.02, 0.02, 0.44, 8)), m('#3b3f46'), tx + sx, 0.22, tzz);
      }
      put(keep(new THREE.CylinderGeometry(0.04, 0.035, 0.09, 14)), m('#fbfaf7', { roughness: 0.3 }), tx + 0.1, 0.81, tzz - 0.05);
    }
  }

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

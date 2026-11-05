// The things the Office is furnished with besides desks (layout.ts places
// them as Props): trees, standing tables, a printer and water cooler, phone
// booths, bean bags, low shelves, rolling whiteboards, lockers, the commons'
// kitchen and island, a ping-pong table, arcade cabinets, server racks.
// Rounded and toy-like, in the diorama's palette; every part is handed to the
// diorama to bake, so they cost a few draws, not one each.
import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';
import { piece, sizeOf, type Piece } from './furniture.ts';
import { PROP_SCALE, type Prop } from './layout.ts';

export type Kit = {
  // A material, shared by colour.
  m: (c: THREE.ColorRepresentation, o?: Partial<THREE.MeshStandardMaterialParameters>) => THREE.Material;
  // Keeps a geometry, texture or material to dispose with the room.
  keep: <T extends { dispose(): void }>(x: T) => T;
  // Meshes to bake, which cast shadows, and soft ones (leaves) that bake apart.
  parts: THREE.Mesh[];
  soft: THREE.Mesh[];
  glass: THREE.Material;
};

const rbox = (w: number, h: number, d: number, r = 0.04) => new RoundedBoxGeometry(w, h, d, 3, Math.min(r, w / 2 - 1e-3, h / 2 - 1e-3, d / 2 - 1e-3));

// flatRug is a rounded rectangle lying on the floor, h thick.
function flatRug(w: number, d: number, r: number, h: number): THREE.BufferGeometry {
  const s = new THREE.Shape();
  const rr = Math.min(r, w / 2, d / 2);
  s.moveTo(-w / 2 + rr, -d / 2);
  s.lineTo(w / 2 - rr, -d / 2);
  s.quadraticCurveTo(w / 2, -d / 2, w / 2, -d / 2 + rr);
  s.lineTo(w / 2, d / 2 - rr);
  s.quadraticCurveTo(w / 2, d / 2, w / 2 - rr, d / 2);
  s.lineTo(-w / 2 + rr, d / 2);
  s.quadraticCurveTo(-w / 2, d / 2, -w / 2, d / 2 - rr);
  s.lineTo(-w / 2, -d / 2 + rr);
  s.quadraticCurveTo(-w / 2, -d / 2, -w / 2 + rr, -d / 2);
  const g = new THREE.ExtrudeGeometry(s, { depth: h, bevelEnabled: false, curveSegments: 6 });
  g.rotateX(Math.PI / 2);
  g.translate(0, h, 0);
  return g;
}

function canvasTex(k: Kit, w: number, h: number, draw: (ctx: CanvasRenderingContext2D) => void): THREE.CanvasTexture {
  const c = document.createElement('canvas');
  c.width = w;
  c.height = h;
  draw(c.getContext('2d')!);
  const t = k.keep(new THREE.CanvasTexture(c));
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 4;
  return t;
}

// furnish adds every prop to g.
export function furnish(g: THREE.Group, props: Prop[], k: Kit): void {
  const tex = textures(k);
  for (const [i, p] of props.entries()) {
    const o = new THREE.Group();
    o.position.set(p.x, 0, p.z);
    o.rotation.y = p.rot;
    o.scale.setScalar(PROP_SCALE);
    g.add(o);
    const put = (geo: THREE.BufferGeometry, mat: THREE.Material, x: number, y: number, z: number, rot?: [number, number, number], soft = false) => {
      const mesh = new THREE.Mesh(k.keep(geo), mat);
      mesh.position.set(x, y, z);
      if (rot) mesh.rotation.set(...rot);
      o.add(mesh);
      (soft ? k.soft : k.parts).push(mesh);
      return mesh;
    };
    // A piece of Kenney's kit, placed in the prop's own frame, if it's loaded.
    const kit = (name: Piece, x: number, y: number, z: number, rot = 0): boolean => {
      const q = piece(name);
      if (!q) return false;
      q.position.set(x, y, z);
      q.rotation.y = rot;
      o.add(q);
      q.traverse((m) => (m as THREE.Mesh).isMesh && k.parts.push(m as THREE.Mesh));
      return true;
    };
    BUILD[p.kind](put, k, tex, i, kit);
  }
}

type Put = (geo: THREE.BufferGeometry, mat: THREE.Material, x: number, y: number, z: number, rot?: [number, number, number], soft?: boolean) => THREE.Mesh;
type Tex = ReturnType<typeof textures>;

const OAK = '#e1a86d';
const CREAM = '#f3ece1';
const STEEL = '#cfc6ba';
const DARK = '#2f3440';
const LEAVES = ['#4f9a52', '#6db35a', '#3f8a4a'];
const POTS = ['#e9dccb', '#d9825b', '#f4efe6', '#9fb8a8'];

const cyl = (r0: number, r1: number, h: number, seg = 20) => new THREE.CylinderGeometry(r0, r1, h, seg);
const ball = (r: number) => new THREE.SphereGeometry(r, 16, 12);

// A little potted plant, for shelves and tables.
function sprig(put: Put, k: Kit, x: number, y: number, z: number, i: number) {
  put(cyl(0.07, 0.055, 0.12), k.m(POTS[i % POTS.length], { roughness: 0.6 }), x, y + 0.06, z);
  for (let j = 0; j < 3; j++) {
    const s = put(ball(0.07 + (j % 2) * 0.02), k.m(LEAVES[(i + j) % 3], { roughness: 0.85 }), x + (j - 1) * 0.05, y + 0.18 + (j % 2) * 0.05, z + ((j * 7) % 3 - 1) * 0.03, undefined, true);
    s.scale.y = 0.85;
  }
}

type KitPut = (name: Piece, x: number, y: number, z: number, rot?: number) => boolean;
const BUILD: Record<Prop['kind'], (put: Put, k: Kit, t: Tex, i: number, kit: KitPut) => void> = {
  // A tall potted tree: a round pot, a slim trunk, a crown of soft leaves.
  tree(put, k, _t, i) {
    put(cyl(0.3, 0.24, 0.5, 24), k.m(POTS[i % POTS.length], { roughness: 0.6 }), 0, 0.25, 0);
    put(cyl(0.27, 0.27, 0.02), k.m('#6b4a32', { roughness: 1 }), 0, 0.5, 0);
    put(cyl(0.035, 0.05, 1.1, 8), k.m('#7a5236', { roughness: 0.9 }), 0, 1.0, 0);
    const crown = [
      [0, 1.75, 0, 0.42],
      [0.24, 1.5, 0.1, 0.3],
      [-0.22, 1.55, -0.08, 0.32],
      [0.05, 1.45, -0.24, 0.28],
      [-0.06, 2.05, 0.06, 0.26],
    ];
    for (const [j, [x, y, z, r]] of crown.entries()) put(ball(r), k.m(LEAVES[(i + j) % 3], { roughness: 0.85 }), x, y, z);
  },
  // A standing table with a laptop open on it and a coffee.
  standing(put, k, t) {
    put(rbox(1.4, 0.05, 0.72, 0.02), k.m(OAK, { roughness: 0.5 }), 0, 1.05, 0);
    for (const x of [-0.55, 0.55]) {
      put(rbox(0.05, 1.02, 0.05, 0.015), k.m(STEEL, { roughness: 0.5 }), x, 0.52, 0);
      put(rbox(0.06, 0.03, 0.6, 0.015), k.m(STEEL, { roughness: 0.5 }), x, 0.02, 0);
    }
    put(rbox(0.36, 0.02, 0.25, 0.008), k.m('#d5d9e0', { roughness: 0.35, metalness: 0.3 }), -0.2, 1.085, 0.06);
    const lid = put(rbox(0.36, 0.24, 0.015, 0.008), k.m('#d5d9e0', { roughness: 0.35, metalness: 0.3 }), -0.2, 1.2, -0.07, [-0.25, 0, 0]);
    const screen = put(new THREE.PlaneGeometry(0.32, 0.2), k.m('#ffffff', { map: t.code, emissive: '#ffffff', emissiveMap: t.code, emissiveIntensity: 0.5, roughness: 0.4 }), 0, 0, 0.009);
    lid.add(screen);
    put(cyl(0.04, 0.035, 0.1, 14), k.m('#fbfaf7', { roughness: 0.3 }), 0.35, 1.125, 0.1);
    sprig(put, k, 0.5, 1.075, -0.18, 2);
  },
  // A printer on its cabinet, a water cooler beside it.
  printer(put, k) {
    put(rbox(0.66, 0.55, 0.55, 0.04), k.m('#d9d4cc', { roughness: 0.6 }), -0.45, 0.275, 0);
    put(rbox(0.6, 0.32, 0.5, 0.05), k.m('#f2f0ec', { roughness: 0.45 }), -0.45, 0.71, 0);
    put(rbox(0.5, 0.03, 0.24, 0.01), k.m('#ffffff', { roughness: 0.8 }), -0.45, 0.88, 0.1);
    put(rbox(0.16, 0.05, 0.02, 0.01), k.m(DARK, { roughness: 0.3 }), -0.32, 0.8, 0.255);
    put(ball(0.012), k.m('#45e08a', { emissive: '#45e08a', emissiveIntensity: 1.2 }), -0.2, 0.8, 0.26);
    put(rbox(0.38, 0.95, 0.38, 0.06), k.m('#f4f2ee', { roughness: 0.4 }), 0.45, 0.475, 0);
    put(cyl(0.15, 0.15, 0.42, 24), k.m('#9ad0f0', { roughness: 0.1, transparent: true, opacity: 0.75 }), 0.45, 1.16, 0);
    put(rbox(0.12, 0.05, 0.05, 0.015), k.m('#5aaef0', { roughness: 0.4 }), 0.45, 0.72, 0.2);
    put(cyl(0.025, 0.02, 0.06, 10), k.m('#ffffff', { roughness: 0.6 }), 0.32, 0.98, 0.12);
  },
  // A low oak sideboard, plants and books on it.
  shelf(put, k, _t, i) {
    put(rbox(1.8, 0.7, 0.42, 0.04), k.m(OAK, { roughness: 0.55 }), 0, 0.37, 0);
    for (const x of [-0.6, 0, 0.6]) put(rbox(0.55, 0.52, 0.02, 0.01), k.m('#d39a62', { roughness: 0.6 }), x, 0.38, 0.215);
    for (const x of [-0.6, 0, 0.6]) put(rbox(0.12, 0.02, 0.02, 0.008), k.m(DARK, { roughness: 0.4 }), x, 0.55, 0.23);
    for (const x of [-0.8, 0.8]) put(cyl(0.02, 0.02, 0.04, 8), k.m(DARK), x, 0.02, 0.15);
    sprig(put, k, -0.6, 0.72, 0, i);
    sprig(put, k, 0.62, 0.72, 0.02, i + 1);
    const books = ['#c4622d', '#2f5a4c', '#e9b44c', '#7c8bd6'];
    for (let b = 0; b < 4; b++) put(rbox(0.05, 0.24 + (b % 2) * 0.04, 0.18, 0.008), k.m(books[(b + i) % 4], { roughness: 0.7 }), -0.05 + b * 0.06, 0.84 + (b % 2) * 0.02, 0);
    put(rbox(0.26, 0.2, 0.02, 0.01), k.m('#ffffff', { roughness: 0.5 }), 0.25, 0.83, -0.1, [-0.15, 0, 0]);
  },
  // A phone booth: felt walls, a glass door, a little ledge and a lamp.
  booth(put, k) {
    const felt = k.m('#a9c4a4', { roughness: 0.95 });
    put(rbox(1.3, 0.08, 1.3, 0.03), k.m('#e6dccd', { roughness: 0.7 }), 0, 0.04, 0);
    put(rbox(1.3, 2.2, 0.08, 0.03), felt, 0, 1.14, -0.61);
    for (const x of [-0.61, 0.61]) put(rbox(0.08, 2.2, 1.3, 0.03), felt, x, 1.14, 0);
    put(rbox(1.36, 0.1, 1.36, 0.04), k.m('#8fb08a', { roughness: 0.9 }), 0, 2.28, 0);
    const door = put(new THREE.PlaneGeometry(1.14, 2.1), k.glass, 0, 1.12, 0.62);
    k.parts.splice(k.parts.indexOf(door), 1);
    door.renderOrder = 3;
    put(rbox(0.04, 2.15, 0.05, 0.015), k.m(DARK), 0.55, 1.12, 0.62);
    put(rbox(0.04, 2.15, 0.05, 0.015), k.m(DARK), -0.55, 1.12, 0.62);
    put(rbox(1.1, 0.04, 0.4, 0.015), k.m(OAK, { roughness: 0.5 }), 0, 1.0, -0.38);
    put(cyl(0.11, 0.14, 0.12, 18), k.m('#fff1d6', { emissive: '#ffd59a', emissiveIntensity: 0.7 }), 0.3, 2.12, -0.2);
  },
  // Bean bags round a little table, on a round rug.
  beanbags(put, k, _t, i) {
    put(cyl(1.1, 1.1, 0.02, 40), k.m('#f0d9bf', { roughness: 0.95 }), 0, 0.032, 0);
    const cols = ['#ffc94d', '#ff7aa2', '#45cbb8'];
    for (let j = 0; j < 3; j++) {
      const a = (j / 3) * Math.PI * 2 + 0.5;
      const b = put(ball(0.45), k.m(cols[(j + i) % 3], { roughness: 0.85 }), Math.sin(a) * 0.7, 0.24, Math.cos(a) * 0.62);
      b.scale.set(1, 0.58, 1);
      const back = put(ball(0.3), k.m(cols[(j + i) % 3], { roughness: 0.85 }), Math.sin(a) * 0.95, 0.44, Math.cos(a) * 0.85);
      back.scale.set(1, 0.8, 0.8);
      back.rotation.y = a;
    }
    put(cyl(0.22, 0.22, 0.04, 24), k.m(OAK, { roughness: 0.5 }), 0, 0.42, 0);
    put(cyl(0.04, 0.06, 0.4, 10), k.m(STEEL), 0, 0.2, 0);
    sprig(put, k, 0.05, 0.44, 0, i);
  },
  // A rolling whiteboard, mid-discussion.
  whiteboard(put, k, t) {
    put(rbox(1.56, 0.98, 0.04, 0.02), k.m('#ffffff', { map: t.scribble, roughness: 0.35 }), 0, 1.35, 0);
    put(rbox(1.62, 1.04, 0.03, 0.02), k.m('#bfc4cc', { roughness: 0.4 }), 0, 1.35, -0.02);
    for (const x of [-0.78, 0.78]) {
      put(rbox(0.04, 1.75, 0.04, 0.012), k.m('#bfc4cc', { roughness: 0.4 }), x, 0.9, 0);
      put(rbox(0.05, 0.04, 0.48, 0.012), k.m('#bfc4cc', { roughness: 0.4 }), x, 0.06, 0);
      for (const z of [-0.2, 0.2]) put(ball(0.035), k.m(DARK), x, 0.035, z);
    }
    put(rbox(0.6, 0.03, 0.08, 0.01), k.m('#bfc4cc', { roughness: 0.4 }), 0, 0.84, 0.05);
    for (const [j, c] of ['#ef4444', '#3b82f6', '#22c55e'].entries()) put(cyl(0.012, 0.012, 0.12, 8), k.m(c), -0.12 + j * 0.1, 0.87, 0.05, [0, 0, Math.PI / 2]);
  },
  // A row of lockers in pastels, against the wall.
  lockers(put, k, _t, i) {
    const cols = ['#bfe3d6', '#f6d7a7', '#c9d3f5', '#f5c6d0'];
    for (let j = 0; j < 4; j++) {
      const x = -0.75 + j * 0.5;
      put(rbox(0.48, 1.8, 0.46, 0.04), k.m(cols[(j + i) % 4], { roughness: 0.5 }), x, 0.92, 0);
      for (let v = 0; v < 3; v++) put(rbox(0.22, 0.015, 0.01, 0.005), k.m('#8d8a86', { roughness: 0.6 }), x, 1.6 - v * 0.04, 0.232);
      put(rbox(0.03, 0.12, 0.03, 0.01), k.m(STEEL, { metalness: 0.4, roughness: 0.3 }), x + 0.16, 1.0, 0.24);
    }
    put(rbox(2.02, 0.06, 0.48, 0.02), k.m('#e9e2d6', { roughness: 0.6 }), 0, 0.03, 0);
    sprig(put, k, -0.6, 1.82, 0, i);
    put(rbox(0.36, 0.22, 0.28, 0.04), k.m('#c08b5c', { roughness: 0.7 }), 0.45, 1.93, 0);
  },
  // The kitchen counter: cabinets, an oak top, a sink, the coffee machine,
  // a kettle and a fruit bowl; tiles behind and an open shelf of jars above.
  kitchen(put, k, _t, i, kit) {
    // Kenney's kitchen, when it's there: a drawer unit, the sink and a
    // cabinet, the microwave and coffee machine on top, wall cabinets above,
    // tiles behind.
    const cw = sizeOf('kitchenCabinet')?.w;
    const ch = sizeOf('kitchenCabinet')?.h ?? 0.9;
    if (cw && kit('kitchenCabinetDrawer', -cw, 0, 0) && kit('kitchenSink', 0, 0, 0) && kit('kitchenCabinet', cw, 0, 0)) {
      kit('kitchenMicrowave', -cw, ch, -0.05);
      kit('kitchenCoffeeMachine', cw - 0.1, ch, -0.05);
      kit('kitchenCabinetUpperDouble', -cw / 2, 1.55, -0.18);
      kit('kitchenCabinetUpperDouble', cw / 2, 1.55, -0.18);
      put(rbox(cw * 3, 0.55, 0.02, 0.01), k.m('#d6e8e1', { roughness: 0.3 }), 0, ch + 0.32, -0.31);
      put(cyl(0.09, 0.11, 0.2, 18), k.m('#ff7aa2', { roughness: 0.35 }), cw * 0.55, ch + 0.1, 0.05);
      return;
    }
    put(rbox(2.8, 0.86, 0.64, 0.03), k.m(CREAM, { roughness: 0.5 }), 0, 0.43, 0);
    for (let j = 0; j < 4; j++) put(rbox(0.66, 0.74, 0.02, 0.01), k.m('#ebe2d4', { roughness: 0.5 }), -1.05 + j * 0.7, 0.44, 0.325);
    for (let j = 0; j < 4; j++) put(rbox(0.16, 0.02, 0.02, 0.008), k.m(STEEL, { metalness: 0.5, roughness: 0.3 }), -1.05 + j * 0.7, 0.72, 0.34);
    put(rbox(2.86, 0.05, 0.7, 0.015), k.m(OAK, { roughness: 0.45 }), 0, 0.885, 0);
    put(rbox(2.8, 0.6, 0.02, 0.01), k.m('#d6e8e1', { roughness: 0.3 }), 0, 1.22, -0.33);
    put(rbox(0.5, 0.03, 0.36, 0.01), k.m('#c9ccd1', { metalness: 0.6, roughness: 0.25 }), -0.7, 0.9, 0.02);
    put(cyl(0.015, 0.015, 0.28, 8), k.m(STEEL, { metalness: 0.7, roughness: 0.2 }), -0.7, 1.05, -0.2);
    // The coffee machine.
    put(rbox(0.34, 0.44, 0.32, 0.05), k.m(DARK, { roughness: 0.35 }), 0.45, 1.13, -0.08);
    put(rbox(0.26, 0.08, 0.16, 0.02), k.m('#c9ccd1', { metalness: 0.7, roughness: 0.25 }), 0.45, 0.95, 0.08);
    put(ball(0.015), k.m('#ff9150', { emissive: '#ff9150', emissiveIntensity: 1.3 }), 0.55, 1.28, 0.085);
    put(cyl(0.035, 0.03, 0.08, 12), k.m('#fbfaf7', { roughness: 0.3 }), 0.45, 0.95, 0.12);
    // The kettle and the fruit bowl.
    put(cyl(0.09, 0.11, 0.2, 18), k.m('#ff7aa2', { roughness: 0.35 }), 1.05, 1.01, -0.05);
    put(new THREE.SphereGeometry(0.16, 20, 10, 0, Math.PI * 2, Math.PI / 2, Math.PI / 2), k.m('#f4efe6', { roughness: 0.4, side: THREE.DoubleSide }), -0.05, 1.07, 0.05);
    for (const [j, c] of ['#ffb02e', '#ff6b4a', '#9bd16a', '#ffb02e'].entries()) put(ball(0.055), k.m(c, { roughness: 0.5 }), -0.1 + (j % 2) * 0.09, 0.98 + Math.floor(j / 2) * 0.05, 0.02 + ((j * 5) % 3 - 1) * 0.04);
    // The open shelf, jars and cups on it.
    put(rbox(2.4, 0.04, 0.26, 0.01), k.m(OAK, { roughness: 0.5 }), 0, 1.78, -0.2);
    const jars = ['#f6d7a7', '#bfe3d6', '#ffffff', '#c9d3f5', '#f5c6d0'];
    for (let j = 0; j < 8; j++) put(cyl(0.05, 0.05, 0.12 + (j % 3) * 0.04, 14), k.m(jars[(j + i) % jars.length], { roughness: 0.35 }), -1.0 + j * 0.28, 1.86 + (j % 3) * 0.02, -0.2);
  },
  // A rounded retro fridge in mint.
  fridge(put, k, _t, _i, kit) {
    if (kit('kitchenFridge', 0, 0, 0)) return;
    put(rbox(0.76, 1.85, 0.66, 0.12), k.m('#bfe3d6', { roughness: 0.35 }), 0, 0.93, 0);
    put(rbox(0.7, 0.015, 0.02, 0.005), k.m('#9fc9ba', { roughness: 0.4 }), 0, 1.25, 0.335);
    for (const y of [0.85, 1.55]) put(rbox(0.03, 0.26, 0.04, 0.012), k.m(STEEL, { metalness: 0.7, roughness: 0.2 }), 0.28, y, 0.35);
    for (const [j, c] of ['#ff7aa2', '#ffc94d', '#5aaef0'].entries()) put(rbox(0.06, 0.06, 0.015, 0.01), k.m(c), -0.2 + j * 0.12, 1.5 - (j % 2) * 0.12, 0.335);
  },
  // The kitchen island and its three stools.
  island(put, k) {
    put(rbox(2.2, 0.9, 0.78, 0.04), k.m('#9fb8a8', { roughness: 0.55 }), 0, 0.45, -0.38);
    put(rbox(2.36, 0.05, 0.92, 0.02), k.m(OAK, { roughness: 0.45 }), 0, 0.925, -0.32);
    for (let j = 0; j < 3; j++) {
      const x = -0.75 + j * 0.75;
      put(cyl(0.19, 0.17, 0.07, 22), k.m(['#ffc94d', '#ff7aa2', '#45cbb8'][j], { roughness: 0.6 }), x, 0.7, 0.42);
      put(cyl(0.025, 0.03, 0.66, 8), k.m(STEEL, { metalness: 0.4, roughness: 0.35 }), x, 0.34, 0.42);
      put(new THREE.TorusGeometry(0.13, 0.012, 6, 20), k.m(STEEL, { metalness: 0.4, roughness: 0.35 }), x, 0.26, 0.42, [Math.PI / 2, 0, 0]);
      put(cyl(0.16, 0.18, 0.02, 20), k.m(STEEL, { metalness: 0.4, roughness: 0.35 }), x, 0.01, 0.42);
    }
    put(cyl(0.035, 0.03, 0.09, 12), k.m('#fbfaf7', { roughness: 0.3 }), -0.5, 0.995, -0.15);
    put(cyl(0.035, 0.03, 0.09, 12), k.m('#ffc94d', { roughness: 0.3 }), 0.35, 0.995, -0.25);
    sprig(put, k, 0.85, 0.95, -0.4, 1);
  },
  // A ping-pong table, its net, two paddles and a ball.
  pingpong(put, k) {
    put(rbox(2.74, 0.05, 1.525, 0.015), k.m('#2f8f6a', { roughness: 0.5 }), 0, 0.76, 0);
    const line = k.m('#ffffff', { roughness: 0.5 });
    for (const z of [-0.74, 0.74]) put(rbox(2.72, 0.006, 0.025, 0.002), line, 0, 0.787, z);
    for (const x of [-1.355, 1.355]) put(rbox(0.025, 0.006, 1.5, 0.002), line, x, 0.787, 0);
    put(rbox(2.72, 0.006, 0.012, 0.002), line, 0, 0.787, 0);
    for (const x of [-1.15, 1.15]) for (const z of [-0.6, 0.6]) put(rbox(0.06, 0.74, 0.06, 0.015), k.m(DARK, { roughness: 0.4 }), x, 0.37, z);
    put(rbox(0.015, 0.15, 1.68, 0.005), k.m('#ffffff', { roughness: 0.7, transparent: true, opacity: 0.75 }), 0, 0.86, 0);
    for (const z of [-0.84, 0.84]) put(rbox(0.03, 0.17, 0.03, 0.01), k.m(DARK), 0, 0.86, z);
    put(cyl(0.08, 0.08, 0.012, 20), k.m('#e04848', { roughness: 0.5 }), -0.8, 0.792, 0.3, [0, 0, 0]);
    put(rbox(0.03, 0.012, 0.1, 0.005), k.m(OAK), -0.8, 0.792, 0.42);
    put(cyl(0.08, 0.08, 0.012, 20), k.m('#2b2f3a', { roughness: 0.5 }), 0.9, 0.792, -0.35);
    put(rbox(0.03, 0.012, 0.1, 0.005), k.m(OAK), 0.9, 0.792, -0.47);
    put(ball(0.022), k.m('#ffffff', { roughness: 0.3 }), 0.3, 0.81, 0.1);
  },
  // An arcade cabinet: a glowing game, a joystick and buttons, a lit marquee.
  arcade(put, k, t, i) {
    const body = k.m(i % 2 ? '#7c6cf0' : '#45cbb8', { roughness: 0.45 });
    put(rbox(0.68, 1.1, 0.62, 0.05), body, 0, 0.55, -0.02);
    put(rbox(0.68, 0.72, 0.36, 0.05), body, 0, 1.45, -0.15);
    put(rbox(0.66, 0.08, 0.34, 0.03), body, 0, 1.12, 0.12, [0.35, 0, 0]);
    put(new THREE.PlaneGeometry(0.5, 0.42), k.m('#ffffff', { map: t.game, emissive: '#ffffff', emissiveMap: t.game, emissiveIntensity: 0.85, roughness: 0.3 }), 0, 1.45, 0.031, [-0.12, 0, 0]);
    put(rbox(0.68, 0.18, 0.4, 0.04), k.m(DARK, { roughness: 0.4 }), 0, 1.9, -0.12);
    put(new THREE.PlaneGeometry(0.56, 0.12), k.m('#ffffff', { map: t.marquee, emissive: '#ffffff', emissiveMap: t.marquee, emissiveIntensity: 0.9 }), 0, 1.9, 0.081);
    put(cyl(0.012, 0.012, 0.1, 8), k.m(DARK), -0.14, 1.2, 0.16);
    put(ball(0.035), k.m('#e04848', { roughness: 0.3 }), -0.14, 1.26, 0.16);
    for (const [j, c] of ['#ffc94d', '#5aaef0', '#ff7aa2'].entries()) put(cyl(0.025, 0.025, 0.02, 12), k.m(c, { roughness: 0.3 }), 0.04 + j * 0.08, 1.17, 0.15, [0.35, 0, 0]);
  },
  // Three potted plants of different heights on a low plinth.
  plants(put, k, _t, i) {
    put(rbox(1.5, 0.12, 0.9, 0.04), k.m('#e6dccd', { roughness: 0.7 }), 0, 0.06, 0);
    const pots: [number, number, number, number][] = [
      [-0.45, 0.2, 0.42, 0.62],
      [0.15, 0.17, 0.3, 0.38],
      [0.5, 0.14, 0.55, 0.3],
    ];
    for (const [j, [x, r, h, lr]] of pots.entries()) {
      put(cyl(r, r * 0.82, h, 22), k.m(POTS[(i + j) % POTS.length], { roughness: 0.6 }), x, 0.12 + h / 2, ((j * 5) % 3 - 1) * 0.12);
      for (let q = 0; q < 3; q++) put(ball(lr * (0.7 + q * 0.12)), k.m(LEAVES[(i + j + q) % 3], { roughness: 0.85 }), x + (q - 1) * lr * 0.35, 0.12 + h + lr * (0.6 + q * 0.25), ((j * 5) % 3 - 1) * 0.12 + (q % 2) * 0.08);
    }
  },
  // A fish tank on an oak cabinet: blue water, weed, a few bright fish.
  aquarium(put, k, t) {
    put(rbox(1.5, 0.72, 0.55, 0.04), k.m(OAK, { roughness: 0.55 }), 0, 0.36, 0);
    put(rbox(1.42, 0.62, 0.48, 0.02), k.m('#7fd0f0', { roughness: 0.05, transparent: true, opacity: 0.55, emissive: '#3fa9d8', emissiveIntensity: 0.25 }), 0, 1.05, 0);
    put(rbox(1.46, 0.05, 0.52, 0.02), k.m(DARK, { roughness: 0.4 }), 0, 1.38, 0);
    put(rbox(1.36, 0.06, 0.42, 0.02), k.m('#e8d9b8', { roughness: 0.9 }), 0, 0.77, 0);
    for (let j = 0; j < 5; j++) put(cyl(0.015, 0.025, 0.3 + (j % 3) * 0.08, 6), k.m('#3f8a4a', { roughness: 0.8 }), -0.55 + j * 0.27, 0.95 + (j % 3) * 0.04, -0.1 + (j % 2) * 0.12);
    for (const [j, c] of ['#ff9150', '#ffc94d', '#ff7aa2', '#5aaef0'].entries()) {
      const f = put(ball(0.045), k.m(c, { roughness: 0.4, emissive: c, emissiveIntensity: 0.2 }), -0.4 + j * 0.28, 0.98 + ((j * 7) % 3) * 0.1, 0.05 - (j % 2) * 0.1);
      f.scale.set(1.5, 0.9, 0.7);
    }
    void t;
  },
  // A vending machine, lit, its rows of drinks behind glass.
  vending(put, k, t) {
    put(rbox(0.92, 1.9, 0.78, 0.06), k.m('#ff7aa2', { roughness: 0.45 }), 0, 0.95, 0);
    put(new THREE.PlaneGeometry(0.56, 1.2), k.m('#ffffff', { map: t.drinks, emissive: '#ffffff', emissiveMap: t.drinks, emissiveIntensity: 0.6, roughness: 0.2 }), -0.1, 1.15, 0.395);
    put(rbox(0.18, 0.5, 0.03, 0.02), k.m(DARK, { roughness: 0.3 }), 0.3, 1.25, 0.39);
    put(rbox(0.5, 0.16, 0.03, 0.02), k.m(DARK, { roughness: 0.3 }), -0.1, 0.32, 0.39);
    put(rbox(0.92, 0.16, 0.6, 0.05), k.m('#ffffff', { emissive: '#ffffff', emissiveIntensity: 0.3, roughness: 0.4 }), 0, 1.98, 0.05);
  },
  // The AgentBox cube, a sculpture on a plinth: its gradient, white edges.
  logo(put, k, t) {
    put(rbox(0.8, 0.5, 0.8, 0.06), k.m(CREAM, { roughness: 0.5 }), 0, 0.25, 0);
    const cube = put(rbox(0.6, 0.6, 0.6, 0.12), k.m('#ffffff', { map: t.logo, roughness: 0.35 }), 0, 0.95, 0, [0.35, 0.65, 0.2]);
    void cube;
  },
  // A soft mat under a corner's things, in one of the room's pastels.
  mat(put, k, _t, i) {
    const cols = [
      ['#f3dcc6', '#e6c3a3'],
      ['#dcebd8', '#bfd8ba'],
      ['#e6e0f3', '#cdc3e6'],
      ['#f6e3c4', '#e8c99b'],
      ['#d9ebef', '#b9d6de'],
    ][i % 5];
    put(flatRug(4.3, 2.4, 0.9, 0.016), k.m(cols[1], { roughness: 0.95 }), 0, 0, 0);
    put(flatRug(4.1, 2.2, 0.8, 0.02), k.m(cols[0], { roughness: 0.95 }), 0, 0, 0);
  },
  // A two-seat couch, soft and low, facing the room.
  couch(put, k, _t, i) {
    const cols = ['#9cb89a', '#e9b44c', '#c9d3f5', '#f5c6d0'];
    const fab = k.m(cols[i % cols.length], { roughness: 0.95 });
    const cush = k.m('#fbf6ee', { roughness: 0.95 });
    put(rbox(1.8, 0.34, 0.75, 0.12), fab, 0, 0.2, 0.02);
    put(rbox(1.8, 0.5, 0.2, 0.09), fab, 0, 0.52, -0.3);
    for (const x of [-0.82, 0.82]) put(rbox(0.18, 0.46, 0.75, 0.08), fab, x, 0.36, 0.02);
    for (const x of [-0.38, 0.38]) put(rbox(0.7, 0.12, 0.6, 0.06), cush, x, 0.42, 0.06);
    put(rbox(0.34, 0.3, 0.1, 0.08), k.m(['#ffc94d', '#ff7aa2', '#45cbb8'][i % 3], { roughness: 0.9 }), 0.45, 0.62, -0.16, [-0.25, 0.2, 0]);
  },
  // A low table: a mug and a book on it.
  lowtable(put, k, _t, i) {
    put(rbox(0.9, 0.05, 0.5, 0.15), k.m(OAK, { roughness: 0.45 }), 0, 0.38, 0);
    for (const [x, z] of [[-0.36, -0.18], [0.36, -0.18], [-0.36, 0.18], [0.36, 0.18]]) put(cyl(0.02, 0.018, 0.36, 8), k.m(DARK), x, 0.18, z);
    put(cyl(0.035, 0.03, 0.08, 12), k.m('#fbfaf7', { roughness: 0.3 }), 0.2, 0.45, 0.05);
    put(rbox(0.24, 0.035, 0.17, 0.01), k.m(['#2f5a4c', '#c4622d', '#7c8bd6'][i % 3], { roughness: 0.7 }), -0.18, 0.425, -0.02, [0, 0.3, 0]);
  },
  // A server rack, its lights green and blue.
  rack(put, k) {
    put(rbox(0.6, 1.5, 0.6, 0.05), k.m('#1e242c', { roughness: 0.4 }), 0, 0.75, 0);
    for (let j = 0; j < 6; j++) put(rbox(0.32, 0.03, 0.02, 0.01), k.m(j % 3 === 2 ? '#5ab8ff' : '#45e08a', { emissive: j % 3 === 2 ? '#5ab8ff' : '#45e08a', emissiveIntensity: 1.4 }), 0, 0.35 + j * 0.18, 0.31);
  },
};

function textures(k: Kit) {
  // A laptop's screen: lines of code.
  const code = canvasTex(k, 128, 80, (c) => {
    c.fillStyle = '#1f2433';
    c.fillRect(0, 0, 128, 80);
    const cols = ['#a78bfa', '#5aaef0', '#86c95e', '#ffc94d', '#e6e8ee'];
    for (let y = 0; y < 9; y++) {
      c.fillStyle = cols[(y * 3) % cols.length];
      c.fillRect(8 + (y % 3) * 8, 6 + y * 8, 30 + ((y * 37) % 60), 4);
    }
  });
  // A whiteboard mid-discussion: boxes, arrows, sticky notes.
  const scribble = canvasTex(k, 256, 160, (c) => {
    c.fillStyle = '#ffffff';
    c.fillRect(0, 0, 256, 160);
    c.lineWidth = 3;
    c.strokeStyle = '#3b82f6';
    c.strokeRect(20, 24, 60, 34);
    c.strokeRect(120, 24, 60, 34);
    c.beginPath();
    c.moveTo(80, 41);
    c.lineTo(118, 41);
    c.moveTo(110, 35);
    c.lineTo(118, 41);
    c.lineTo(110, 47);
    c.stroke();
    c.strokeStyle = '#ef4444';
    c.beginPath();
    c.ellipse(150, 105, 40, 22, 0, 0, Math.PI * 2);
    c.stroke();
    c.strokeStyle = '#22c55e';
    c.beginPath();
    c.moveTo(24, 100);
    c.bezierCurveTo(50, 80, 70, 130, 100, 100);
    c.stroke();
    for (const [x, y, col] of [
      [200, 20, '#fde68a'],
      [222, 52, '#fbcfe8'],
      [200, 84, '#bbf7d0'],
    ] as const) {
      c.fillStyle = col;
      c.fillRect(x, y, 28, 26);
    }
  });
  // An arcade game: a little maze with a hero and a ghost.
  const game = canvasTex(k, 128, 108, (c) => {
    c.fillStyle = '#120f2a';
    c.fillRect(0, 0, 128, 108);
    c.strokeStyle = '#5aaef0';
    c.lineWidth = 4;
    c.strokeRect(8, 8, 112, 92);
    c.strokeRect(32, 30, 64, 48);
    c.fillStyle = '#ffc94d';
    for (let x = 18; x < 116; x += 10) c.fillRect(x, 18, 3, 3);
    for (let x = 18; x < 116; x += 10) c.fillRect(x, 88, 3, 3);
    c.beginPath();
    c.moveTo(24, 50);
    c.arc(24, 50, 9, 0.6, Math.PI * 2 - 0.6);
    c.fill();
    c.fillStyle = '#ff7aa2';
    c.beginPath();
    c.arc(108, 52, 8, Math.PI, 0);
    c.lineTo(116, 62);
    c.lineTo(100, 62);
    c.fill();
  });
  const marquee = canvasTex(k, 128, 28, (c) => {
    const g = c.createLinearGradient(0, 0, 128, 0);
    g.addColorStop(0, '#ff7aa2');
    g.addColorStop(1, '#ffc94d');
    c.fillStyle = g;
    c.fillRect(0, 0, 128, 28);
    c.fillStyle = '#1f1a3a';
    c.font = '800 18px system-ui, sans-serif';
    c.textAlign = 'center';
    c.fillText('BOXIE', 64, 21);
  });
  // A vending machine's window: rows of cans and bottles.
  const drinks = canvasTex(k, 96, 200, (c) => {
    c.fillStyle = '#f4f6fb';
    c.fillRect(0, 0, 96, 200);
    const cols = ['#ef4444', '#3b82f6', '#22c55e', '#f59e0b', '#a78bfa'];
    for (let r = 0; r < 5; r++) {
      c.fillStyle = '#c9ccd1';
      c.fillRect(0, 36 + r * 38, 96, 3);
      for (let j = 0; j < 4; j++) {
        c.fillStyle = cols[(r + j) % cols.length];
        c.beginPath();
        c.roundRect(8 + j * 21, 10 + r * 38, 14, 26, 4);
        c.fill();
      }
    }
  });
  // The logo's face: its violet-to-indigo gradient, a white cube drawn on it.
  const logo = canvasTex(k, 128, 128, (c) => {
    const g = c.createLinearGradient(0, 0, 128, 128);
    g.addColorStop(0, '#a78bfa');
    g.addColorStop(0.55, '#7c3aed');
    g.addColorStop(1, '#4f46e5');
    c.fillStyle = g;
    c.fillRect(0, 0, 128, 128);
    c.strokeStyle = '#ffffff';
    c.lineWidth = 6;
    c.lineJoin = 'round';
    c.beginPath();
    c.moveTo(64, 26);
    c.lineTo(98, 45);
    c.lineTo(98, 84);
    c.lineTo(64, 103);
    c.lineTo(30, 84);
    c.lineTo(30, 45);
    c.closePath();
    c.moveTo(30, 45);
    c.lineTo(64, 64);
    c.lineTo(98, 45);
    c.moveTo(64, 64);
    c.lineTo(64, 103);
    c.stroke();
  });
  return { code, scribble, game, marquee, drinks, logo };
}

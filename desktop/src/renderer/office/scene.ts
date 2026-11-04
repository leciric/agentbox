// The Office in three.js: the room layout.ts lays out, a desk and a robot for
// every agent, each project's lead at its counter, the wall board, and you.
// The React view (OfficeView) owns the data and the words and calls apply();
// this owns the canvas, the camera and the input: keys and the mouse walk you,
// a click on a desk walks you to it, a click on the desk you're at opens it.
//
// It renders only while it's worth it: every frame while you move or anything
// eases, a few frames a second for the robots' idle life once you've let go,
// nothing while the window is hidden, and with reduced motion only when
// something changed.
import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';
import { robot, walker as makeWalker, type Figure } from './figures.ts';
import { COUNTER, DESK, SEAT, layout as layOut, nearest, spotOf, walkable, type Box, type Layout } from './layout.ts';
import type { DeskTone, LeadTone, OfficeModel } from './model.ts';
import { canvasTexture, drawBoard, type BoardRow, drawBubble, drawScreen, drawSign, drawTag, paintSky, planks, toneColor, type BoardScreen, type Screen } from './textures.ts';
import { path, pathLength, slide, turn, type P } from './walk.ts';

// pathLengthFrom is a route's length after its first point.
const pathLengthFrom = (route: readonly P[]) => (route.length > 1 ? pathLength(route[0], route.slice(1)) : 0);

export type SceneInput = {
  model: OfficeModel;
  // Each desk's monitor, by agent ref, and each lead's (project/lead).
  screens: ReadonlyMap<string, Screen>;
  board: BoardScreen;
  you: string;
  // Each area's label from far off: its name and, in words, its counts.
  areaLabels: ReadonlyMap<string, { name: string; sub: string }>;
};

export type OfficeSceneOptions = {
  reducedMotion: boolean;
  // What you're standing at: a desk's ref, project/lead, 'board', or nothing.
  onNear(ref: string | undefined): void;
  // What the pointer is over, for the cursor and the hint.
  onHover(ref: string | undefined): void;
  // A click on what you're standing at, or E: open it.
  onActivate(ref: string): void;
  // You moved for the first time (the controls hint can go).
  onMoved(): void;
  // N: on to the next desk that needs you.
  onNext(): void;
  // A click on a row of the board, standing at it: the desk it's about.
  onBoardRow(ref: string): void;
  // A marker's name for screen readers: "Walk to {title}".
  markerLabel(title: string): string;
};

export type OfficeScene = {
  apply(input: SceneInput): void;
  walkTo(ref: string): void;
  overview(): void;
  setReducedMotion(on: boolean): void;
  // How much of the canvas's left edge an overlay covers, in CSS pixels: the
  // overview frames the room in the rest.
  setInset(left: number): void;
  dispose(): void;
};

// Where you were when you left the Office, so coming back from an agent's
// chat puts you back at its desk rather than at the door.
let lastVisit: { pos: P; facing: number; follow: number; yaw: number; dist: number } | undefined;

const RADIUS = 0.3;
const SPEED = 3.4; // metres a second, walking
const FOV = 38;

const leadAsDesk = (t: LeadTone): DeskTone => (t === 'needs' ? 'needs' : t === 'working' ? 'working' : 'idle');
const std = (color: THREE.ColorRepresentation, o: Partial<THREE.MeshStandardMaterialParameters> = {}) => new THREE.MeshStandardMaterial({ color, roughness: 0.7, ...o });

// Materials every desk shares, made once.
function sharedMaterials() {
  return {
    deskTop: std('#e8dcc8', { roughness: 0.5 }),
    deskLeg: std('#3a3f4b', { metalness: 0.4, roughness: 0.4 }),
    chair: std('#2b3242'),
    chairSeat: std('#3b4458'),
    laptop: std('#c7ccd6', { metalness: 0.6, roughness: 0.3 }),
    monitorFrame: std('#1b2029', { roughness: 0.35 }),
    pole: std('#9aa3b2', { metalness: 0.6, roughness: 0.3 }),
    pot: std('#c96f4a'),
    leaf: std('#3f8f55', { roughness: 0.8 }),
    leafLight: std('#5fb36b', { roughness: 0.8 }),
    mug: std('#f8fafc'),
    hit: new THREE.MeshBasicMaterial({ visible: false }),
  };
}
type Mats = ReturnType<typeof sharedMaterials>;

const geo = {
  box: new THREE.BoxGeometry(1, 1, 1),
  cyl: new THREE.CylinderGeometry(1, 1, 1, 20),
  sphere: new THREE.SphereGeometry(1, 18, 12),
  plane: new THREE.PlaneGeometry(1, 1),
  octa: new THREE.OctahedronGeometry(0.11),
  ring: new THREE.RingGeometry(0.85, 1, 48),
  beam: new THREE.CylinderGeometry(0.75, 0.95, 5, 32, 1, true),
  roundTop: new RoundedBoxGeometry(1, 1, 1, 3, 0.04),
};

function mesh(g: THREE.BufferGeometry, m: THREE.Material, x: number, y: number, z: number, sx = 1, sy = 1, sz = 1, shadow = true): THREE.Mesh {
  const o = new THREE.Mesh(g, m);
  o.position.set(x, y, z);
  o.scale.set(sx, sy, sz);
  o.castShadow = shadow;
  o.receiveShadow = true;
  return o;
}

// plant is a potted plant, leaves in a loose bunch.
function plant(m: Mats, x: number, z: number, seed: number): THREE.Group {
  const g = new THREE.Group();
  g.position.set(x, 0, z);
  g.add(mesh(geo.cyl, m.pot, 0, 0.22, 0, 0.2, 0.44, 0.2));
  for (let i = 0; i < 7; i++) {
    const a = i * 2.4 + seed;
    const r = i === 0 ? 0 : 0.16;
    const leaf = mesh(geo.sphere, i % 2 ? m.leaf : m.leafLight, Math.cos(a) * r, 0.62 + (i % 3) * 0.16, Math.sin(a) * r, 0.17, 0.32, 0.17);
    leaf.rotation.set(Math.sin(a) * 0.5, 0, Math.cos(a) * 0.5);
    g.add(leaf);
  }
  return g;
}

// Sprite is a canvas on a card that always faces the camera.
function sprite(w: number, h: number, scale: number): { sprite: THREE.Sprite; ctx: CanvasRenderingContext2D; tex: THREE.CanvasTexture } {
  const { tex, ctx } = canvasTexture(w, h);
  const s = new THREE.Sprite(new THREE.SpriteMaterial({ map: tex, transparent: true, depthWrite: false }));
  s.scale.set(scale, (scale * h) / w, 1);
  return { sprite: s, ctx, tex };
}

type DeskObj = {
  ref: string;
  root: THREE.Group;
  figure: Figure;
  screen: { ctx: CanvasRenderingContext2D; tex: THREE.CanvasTexture; key: string; data?: Screen; mat: THREE.MeshBasicMaterial };
  tag: { sprite: THREE.Sprite; ctx: CanvasRenderingContext2D; tex: THREE.CanvasTexture; key: string };
  bubble: { sprite: THREE.Sprite; ctx: CanvasRenderingContext2D; tex: THREE.CanvasTexture; key: string };
  beacon: THREE.Mesh;
  ring: THREE.Mesh;
  beam: THREE.Mesh;
  flag: THREE.Group;
  flagMat: THREE.MeshStandardMaterial;
  glowMat: THREE.MeshStandardMaterial;
  tone: DeskTone;
  hit: THREE.Mesh;
  dispose: (() => void)[];
};

const bubbleOf: Partial<Record<DeskTone, [string, string]>> = {
  needs: ['!', '#fbbf24'],
  error: ['×', '#f87171'],
  finished: ['✓', '#34d399'],
  stopped: ['z', '#94a3b8'],
};

export function createOfficeScene(host: HTMLElement, opts: OfficeSceneOptions): OfficeScene {
  let motion = !opts.reducedMotion;
  let disposed = false;
  const renderer = new THREE.WebGLRenderer({ antialias: true, powerPreference: 'high-performance' });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = THREE.PCFSoftShadowMap;
  renderer.toneMapping = THREE.ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.05;
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  const canvas = renderer.domElement;
  canvas.style.display = 'block';
  canvas.style.width = '100%';
  canvas.style.height = '100%';
  canvas.style.touchAction = 'none';
  canvas.tabIndex = 0;
  canvas.setAttribute('data-office-canvas', '');
  host.appendChild(canvas);
  // Markers over the canvas for the desks that need you: from far off a name
  // tag is a smudge, and these are what you'd look for. Each walks you there.
  const markerLayer = document.createElement('div');
  markerLayer.style.cssText = 'position:absolute;inset:0;pointer-events:none;overflow:hidden;transition:opacity .15s';
  host.appendChild(markerLayer);
  const markers = new Map<string, HTMLButtonElement>();
  // Each area's label from far off, a map's: a click walks to its front desk.
  const areaChips = new Map<string, HTMLButtonElement>();
  function syncAreaChips(input: SceneInput) {
    for (const [project, chip] of areaChips)
      if (!input.areaLabels.has(project)) {
        chip.remove();
        areaChips.delete(project);
      }
    for (const area of input.model.areas) {
      const label = input.areaLabels.get(area.project);
      if (!label) continue;
      let chip = areaChips.get(area.project);
      if (!chip) {
        chip = document.createElement('button');
        chip.type = 'button';
        chip.dataset.officeArea = area.project;
        chip.style.cssText =
          'position:absolute;left:0;top:0;display:none;pointer-events:auto;flex-direction:column;align-items:center;padding:4px 12px 5px;border-radius:10px;background:rgba(10,14,24,.88);color:#fff;font:700 13px/1.2 var(--font-sans,system-ui),sans-serif;box-shadow:0 6px 18px rgba(0,0,0,.45);cursor:pointer;white-space:nowrap;will-change:transform';
        const name = document.createElement('span');
        const sub = document.createElement('span');
        sub.style.cssText = 'font-weight:500;font-size:11px;color:#cbd5e1';
        chip.append(name, sub);
        const ref = `${area.project}/lead`;
        chip.addEventListener('pointerdown', (e) => {
          e.preventDefault();
          e.stopPropagation();
          walkTo(ref);
        });
        markerLayer.prepend(chip);
        areaChips.set(area.project, chip);
      }
      chip.style.border = `1px solid hsl(${area.hue} 70% 62% / .7)`;
      (chip.firstChild as HTMLElement).textContent = label.name;
      (chip.lastChild as HTMLElement).textContent = label.sub;
      chip.setAttribute('aria-label', opts.markerLabel(label.name));
    }
  }
  // And you, from far off.
  const meMarker = document.createElement('div');
  meMarker.style.cssText =
    'position:absolute;left:0;top:0;display:none;padding:2px 8px;border-radius:999px;background:#7c3aed;color:#fff;font:700 11.5px/1.25 var(--font-sans,system-ui),sans-serif;box-shadow:0 0 0 3px rgba(167,139,250,.35),0 4px 14px rgba(0,0,0,.4);pointer-events:none;will-change:transform';
  markerLayer.appendChild(meMarker);
  // Until you first move, your chip pulses: this is you.
  let mePulse: Animation | undefined;
  if (!opts.reducedMotion && !lastVisit)
    mePulse = meMarker.animate([{ boxShadow: '0 0 0 3px rgba(167,139,250,.35)' }, { boxShadow: '0 0 0 12px rgba(167,139,250,0)' }], { duration: 1300, iterations: Infinity });

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(FOV, 1, 0.1, 200);
  const hemi = new THREE.HemisphereLight('#e0ecff', '#8a6a4c', 1.0);
  scene.add(hemi);
  const sun = new THREE.DirectionalLight('#fff3e0', 1.8);
  sun.castShadow = true;
  sun.shadow.mapSize.set(2048, 2048);
  sun.shadow.bias = -0.0005;
  sun.shadow.normalBias = 0.02;
  scene.add(sun, sun.target);
  const fill = new THREE.AmbientLight('#ffffff', 0.25);
  scene.add(fill);

  const mats = sharedMaterials();
  const floorTex = planks();
  const sky = canvasTexture(512, 256);
  let skyHour = -1;

  // What a layout builds: the room, its furniture and the desks.
  let room = new THREE.Group();
  scene.add(room);
  let roomDispose: (() => void)[] = [];
  let lay: Layout = layOut({ areas: [] });
  let layKey = '';
  let boxes: Box[] = [];
  let bounds: Box = lay.room;
  const desks = new Map<string, DeskObj>();
  const hits: THREE.Object3D[] = [];
  const signs: THREE.Group[] = [];
  let board: { ctx: CanvasRenderingContext2D; tex: THREE.CanvasTexture; key: string; face?: THREE.Mesh; rows: BoardRow[] } | undefined;
  let sideWalls: { mat: THREE.MeshStandardMaterial; side: 'left' | 'right' }[] = [];

  // You.
  const me = makeWalker();
  scene.add(me.group);
  const meRing = mesh(geo.ring, new THREE.MeshBasicMaterial({ color: '#a78bfa', transparent: true, opacity: 0.8, side: THREE.DoubleSide }), 0, 0.02, 0, 0.42, 0.42, 0.42, false);
  meRing.rotation.x = -Math.PI / 2;
  scene.add(meRing);
  const meTag = sprite(256, 80, 0.62);
  meTag.sprite.position.y = 2.0;
  me.group.add(meTag.sprite);
  let youLabel = '';
  const pos: P = { x: 0, z: 0 };
  let facing = Math.PI;
  let speed = 0;
  let route: P[] = [];
  let routeGoal: string | undefined;
  let placed = false;
  let moved = false;

  // The camera: an overview of the whole room to start, then following you;
  // closer, lower, when you stop at a desk.
  const cam = { yaw: 0, followK: 0, dist: 11, closeK: 0 };
  const eased = { tx: 0, ty: 0, tz: 0, dist: 20, pitch: 0.9, yaw: 0, init: false };
  let near: string | undefined;
  // What the close-up looks at, kept while it eases out.
  let focusRef: string | undefined;
  let focusAt: { x: number; z: number } | undefined;
  let settledFor = 0;
  let chipsShown = true;
  const chipDy = new WeakMap<HTMLElement, number>();
  let stillFor = 0;

  // Input.
  const keys = new Set<string>();
  let hover: string | undefined;
  let pointer: { x: number; y: number; id: number; dragged: boolean; button: number } | undefined;
  const ndc = new THREE.Vector2();
  let hoverDirty = false;
  const ray = new THREE.Raycaster();
  const floorPlane = new THREE.Plane(new THREE.Vector3(0, 1, 0), 0);

  let lastInput = performance.now();
  let dirty = true;
  const poke = () => {
    lastInput = performance.now();
    dirty = true;
    wake();
  };

  function build(input: SceneInput) {
    for (const d of roomDispose) d();
    roomDispose = [];
    scene.remove(room);
    for (const d of desks.values()) for (const f of d.dispose) f();
    desks.clear();
    for (const m of markers.values()) m.remove();
    markers.clear();
    hits.length = 0;
    signs.length = 0;
    room = new THREE.Group();
    scene.add(room);
    const own = <T extends { dispose(): void }>(x: T) => (roomDispose.push(() => x.dispose()), x);
    const l = lay;
    const W = l.room.x1;
    const D = l.room.z1;

    // The floor, its planks a metre and a bit wide.
    const tex = floorTex.clone();
    tex.repeat.set(W / 4, D / 4);
    tex.needsUpdate = true;
    own(tex);
    const floor = mesh(own(new THREE.PlaneGeometry(W, D)), own(std('#ffffff', { map: tex, roughness: 0.75 })), W / 2, 0, D / 2, 1, 1, 1, false);
    floor.rotation.x = -Math.PI / 2;
    floor.userData.floor = true;
    room.add(floor);

    // Walls: the back one, and the sides, which fade when the camera's behind them.
    const wallMat = own(std('#efe8dc', { roughness: 0.9 }));
    const H = 3.2;
    room.add(mesh(geo.box, wallMat, W / 2, H / 2, -0.1, W + 0.4, H, 0.2, false));
    sideWalls = [];
    for (const side of ['left', 'right'] as const) {
      const m = own(std('#e9e1d3', { roughness: 0.9, transparent: true }));
      sideWalls.push({ mat: m, side });
      const w = mesh(geo.box, m, side === 'left' ? -0.1 : W + 0.1, H / 2, D / 2, 0.2, H, D + 0.4, false);
      room.add(w);
    }
    const base = own(std('#8b7355'));
    room.add(mesh(geo.box, base, W / 2, 0.06, 0.01, W, 0.12, 0.04, false));

    // Windows, with the sky at this hour.
    const skyMat = own(new THREE.MeshBasicMaterial({ map: sky.tex }));
    const frame = own(std('#ffffff', { roughness: 0.4 }));
    for (const win of l.windows) {
      const g = new THREE.Group();
      const h = 1.5;
      if (win.side === 'back') g.position.set(win.at, 1.75, 0.005);
      else {
        g.position.set(win.side === 'left' ? 0.005 : W - 0.005, 1.75, win.at);
        g.rotation.y = win.side === 'left' ? Math.PI / 2 : -Math.PI / 2;
      }
      g.add(mesh(geo.plane, skyMat, 0, 0, 0, win.w, h, 1, false));
      g.add(mesh(geo.box, frame, 0, h / 2, 0.02, win.w + 0.12, 0.08, 0.06, false));
      g.add(mesh(geo.box, frame, 0, -h / 2, 0.04, win.w + 0.2, 0.08, 0.14, false));
      g.add(mesh(geo.box, frame, -win.w / 2, 0, 0.02, 0.07, h, 0.06, false));
      g.add(mesh(geo.box, frame, win.w / 2, 0, 0.02, 0.07, h, 0.06, false));
      g.add(mesh(geo.box, frame, 0, 0, 0.02, 0.05, h, 0.05, false));
      room.add(g);
    }

    // The board, on the back wall.
    {
      const bw = l.board.w;
      const bh = Math.min(2.6, bw * 0.52);
      const pxW = 1024;
      const pxH = Math.round((pxW * bh) / bw);
      const { tex: btex, ctx } = canvasTexture(pxW, pxH);
      own(btex);
      board = { ctx, tex: btex, key: '', rows: [] };
      const g = new THREE.Group();
      g.position.set(l.board.x, 0.45 + bh / 2, 0.04);
      g.add(mesh(geo.box, own(std('#5b4636', { roughness: 0.6 })), 0, 0, 0, bw + 0.24, bh + 0.24, 0.08, false));
      const face = mesh(geo.plane, own(new THREE.MeshBasicMaterial({ map: btex, toneMapped: false })), 0, 0, 0.045, bw, bh, 1, false);
      g.add(face);
      board.face = face;
      room.add(g);
      // A ledge below it, with markers.
      room.add(mesh(geo.box, own(std('#5b4636')), l.board.x, 0.3, 0.15, bw * 0.7, 0.05, 0.22));
      const hit = mesh(geo.box, mats.hit, l.board.x, 1.5, 0.3, bw, 3, 0.6, false);
      hit.userData.ref = 'board';
      room.add(hit);
      hits.push(hit);
    }

    // Each area: its rug, its counter with the lead and the project's sign.
    for (let i = 0; i < l.areas.length; i++) {
      const place = l.areas[i];
      const area = input.model.areas[i];
      const r = place.rug;
      const rugEdge = own(std(new THREE.Color().setHSL(area.hue / 360, 0.35, 0.45), { roughness: 0.95 }));
      const rugMat = own(std(new THREE.Color().setHSL(area.hue / 360, 0.28, 0.3), { roughness: 0.95 }));
      const rw = r.x1 - r.x0;
      const rd = r.z1 - r.z0;
      const edge = mesh(geo.box, rugEdge, (r.x0 + r.x1) / 2, 0.006, (r.z0 + r.z1) / 2, rw, 0.012, rd, false);
      const inner = mesh(geo.box, rugMat, (r.x0 + r.x1) / 2, 0.009, (r.z0 + r.z1) / 2, rw - 0.24, 0.014, rd - 0.24, false);
      room.add(edge, inner);

      // The counter: a long desk with a front in the project's colour.
      const { x: lx, z: lz } = place.lead;
      const counterFront = own(std(new THREE.Color().setHSL(area.hue / 360, 0.45, 0.42)));
      room.add(mesh(geo.roundTop, mats.deskTop, lx, 1.0, lz, COUNTER.w + 0.1, 0.06, COUNTER.d + 0.1));
      room.add(mesh(geo.box, counterFront, lx, 0.5, lz + 0.05, COUNTER.w, 0.96, COUNTER.d - 0.1));
      // The sign, on two posts over the counter.
      const sign = sprite(1024, 200, 3.0);
      own(sign.tex);
      drawSign(sign.ctx, area.label, area.hue);
      sign.tex.needsUpdate = true;
      // The sign stands at the area's back edge, behind its desks, so it is
      // never between the camera and one of them.
      const sg = new THREE.Group();
      sg.position.set(lx, 0, r.z0 + 0.15);
      sg.add(mesh(geo.plane, own(new THREE.MeshBasicMaterial({ map: sign.tex, transparent: true, toneMapped: false })), 0, 2.45, 0, 3.0, 0.586, 1, false));
      sg.add(mesh(geo.cyl, mats.pole, -1.35, 1.25, -0.02, 0.025, 2.5, 0.025), mesh(geo.cyl, mats.pole, 1.35, 1.25, -0.02, 0.025, 2.5, 0.025));
      room.add(sg);
      signs.push(sg);
      addDesk(input, `${area.project}/lead`, lx, lz, area.hue, true);
      for (const d of place.desks) addDesk(input, d.id, d.x, d.z, area.hue, false);
    }
    for (const [i, p] of l.plants.entries()) room.add(plant(mats, p.x, p.z, i));

    // The lobby: a sofa facing the room, a coffee table, a water cooler and a lamp.
    {
      const s = l.sofa;
      const sx = (s.x0 + s.x1) / 2;
      const sz = (s.z0 + s.z1) / 2;
      const fabric = own(std('#2f7d7a', { roughness: 0.95 }));
      const cushion = own(std('#3a9692', { roughness: 0.95 }));
      room.add(mesh(geo.roundTop, fabric, sx, 0.25, sz, 2.6, 0.4, 0.95));
      room.add(mesh(geo.roundTop, cushion, sx - 0.62, 0.5, sz - 0.05, 1.18, 0.16, 0.75), mesh(geo.roundTop, cushion, sx + 0.62, 0.5, sz - 0.05, 1.18, 0.16, 0.75));
      room.add(mesh(geo.roundTop, fabric, sx, 0.75, sz + 0.38, 2.6, 0.7, 0.22));
      room.add(mesh(geo.roundTop, fabric, sx - 1.25, 0.55, sz, 0.18, 0.5, 0.95), mesh(geo.roundTop, fabric, sx + 1.25, 0.55, sz, 0.18, 0.5, 0.95));
      const tz = s.z0 - 1.25 + 0.475;
      room.add(mesh(geo.roundTop, mats.deskTop, sx, 0.42, tz, 1.3, 0.06, 0.7));
      for (const [dx, dz] of [[-0.55, -0.25], [0.55, -0.25], [-0.55, 0.25], [0.55, 0.25]]) room.add(mesh(geo.cyl, mats.deskLeg, sx + dx, 0.2, tz + dz, 0.025, 0.4, 0.025));
      room.add(mesh(geo.cyl, mats.mug, sx + 0.3, 0.5, tz + 0.05, 0.05, 0.1, 0.05), mesh(geo.cyl, mats.mug, sx - 0.2, 0.5, tz - 0.1, 0.05, 0.1, 0.05));
      // Water cooler.
      const cz = l.lobby.z0 + 1.2;
      room.add(mesh(geo.box, own(std('#e5e7eb')), 0.6, 0.5, cz, 0.4, 1.0, 0.4));
      room.add(mesh(geo.cyl, own(std('#7dd3fc', { transparent: true, opacity: 0.7, roughness: 0.1 })), 0.6, 1.22, cz, 0.15, 0.45, 0.15));
      // A floor lamp by the sofa, warm.
      room.add(mesh(geo.cyl, mats.deskLeg, sx + 1.75, 0.8, sz, 0.02, 1.6, 0.02));
      lampShade = mesh(geo.cyl, own(std('#fde7c2', { emissive: '#ffcf8a', emissiveIntensity: 0.6 })), sx + 1.75, 1.65, sz, 0.22, 0.3, 0.22);
      room.add(lampShade);
    }

    // The sun's shadow covers the room.
    const half = Math.max(W, D) / 2 + 2;
    sun.shadow.camera.left = -half;
    sun.shadow.camera.right = half;
    sun.shadow.camera.top = half;
    sun.shadow.camera.bottom = -half;
    sun.shadow.camera.near = 1;
    sun.shadow.camera.far = 80;
    sun.shadow.camera.updateProjectionMatrix();
    sun.target.position.set(W / 2, 0, D / 2);
    lighting(true);
    boxes = walkable(l, RADIUS);
    bounds = { x0: l.room.x0 + RADIUS, z0: l.room.z0 + RADIUS, x1: l.room.x1 - RADIUS, z1: l.room.z1 - RADIUS };
  }
  let lampShade: THREE.Mesh | undefined;

  // addDesk puts an agent's desk at (x, z): the desk, its chair, the laptop
  // and the monitor facing you, the robot, its beacon, bubble, name tag and
  // pull request flag. A lead's is the counter, built above: here it gets
  // the robot and the rest.
  function addDesk(input: SceneInput, ref: string, x: number, z: number, hue: number, lead: boolean) {
    const disp: (() => void)[] = [];
    const g = new THREE.Group();
    g.position.set(x, 0, z);
    room.add(g);
    if (!lead) {
      g.add(mesh(geo.roundTop, mats.deskTop, 0, DESK.h, 0, DESK.w, 0.05, DESK.d));
      for (const [dx, dz] of [[-0.68, -0.32], [0.68, -0.32], [-0.68, 0.32], [0.68, 0.32]]) g.add(mesh(geo.box, mats.deskLeg, dx, DESK.h / 2, dz, 0.05, DESK.h, 0.05));
      // A mug, a plant or nothing, by desk.
      const n = [...ref].reduce((a, c) => a + c.charCodeAt(0), 0);
      // A mug on some, between the laptop and the monitor, clear of the robot.
      if (n % 2 === 0) g.add(mesh(geo.cyl, mats.mug, -0.08, DESK.h + 0.07, 0.22, 0.04, 0.1, 0.04));
    }
    // The chair, behind.
    const cz = lead ? -0.75 : -SEAT;
    g.add(mesh(geo.roundTop, mats.chairSeat, 0, 0.45, cz, 0.5, 0.08, 0.5));
    g.add(mesh(geo.roundTop, mats.chair, 0, 0.78, cz - 0.24, 0.48, 0.55, 0.07));
    g.add(mesh(geo.cyl, mats.deskLeg, 0, 0.22, cz, 0.03, 0.44, 0.03));

    const top = lead ? 1.03 : DESK.h + 0.025;
    // The laptop before the robot, its lid's back glowing in its state's colour.
    const glowMat = new THREE.MeshStandardMaterial({ color: '#60a5fa', emissive: '#60a5fa', emissiveIntensity: 0.6 });
    disp.push(() => glowMat.dispose());
    const lx = lead ? -0.55 : -0.48;
    g.add(mesh(geo.box, mats.laptop, lx, top + 0.01, -0.12, 0.42, 0.02, 0.28));
    const lid = new THREE.Group();
    lid.position.set(lx, top + 0.02, -0.02);
    lid.rotation.x = -0.35;
    lid.add(mesh(geo.box, mats.laptop, 0, 0.14, 0, 0.42, 0.28, 0.015));
    lid.add(mesh(geo.box, glowMat, 0, 0.14, 0.009, 0.08, 0.08, 0.002, false));
    g.add(lid);

    // The monitor on the desk's front corner, turned to you.
    const { tex, ctx } = canvasTexture(512, 320);
    disp.push(() => tex.dispose());
    const screenMat = new THREE.MeshBasicMaterial({ map: tex, toneMapped: false });
    disp.push(() => screenMat.dispose());
    const mon = new THREE.Group();
    mon.position.set(lead ? 0.45 : 0.3, top, lead ? 0.1 : 0.05);
    mon.rotation.y = -0.12;
    const mw = lead ? 0.86 : 0.92;
    const mh = mw * (320 / 512);
    mon.add(mesh(geo.box, mats.monitorFrame, 0, 0.06 + mh / 2 + 0.06, 0, mw + 0.05, mh + 0.05, 0.035));
    mon.add(mesh(geo.plane, screenMat, 0, 0.06 + mh / 2 + 0.06, 0.019, mw, mh, 1, false));
    mon.add(mesh(geo.box, mats.monitorFrame, 0, 0.06, -0.02, 0.05, 0.12, 0.05), mesh(geo.box, mats.monitorFrame, 0, 0.005, -0.02, 0.24, 0.01, 0.16));
    g.add(mon);

    // The robot, on its chair.
    const fig = robot(hue, [...ref].reduce((a, c) => a + c.charCodeAt(0), 0));
    disp.push(() => fig.dispose());
    fig.group.position.set(lead ? 0 : -0.48, 0, cz + 0.02);
    if (lead) fig.group.scale.setScalar(1.12);
    g.add(fig.group);

    // Its beacon and bubble above it, its name tag, and a ring and a beam of
    // light when it needs you.
    const beaconMat = new THREE.MeshStandardMaterial({ color: '#60a5fa', emissive: '#60a5fa', emissiveIntensity: 1.4 });
    disp.push(() => beaconMat.dispose());
    const beacon = mesh(geo.octa, beaconMat, fig.group.position.x, 1.95, cz, 1, 1, 1, false);
    g.add(beacon);
    const bubble = sprite(128, 150, 0.5);
    disp.push(() => bubble.tex.dispose(), () => bubble.sprite.material.dispose());
    bubble.sprite.position.set(fig.group.position.x + 0.3, 2.05, cz);
    g.add(bubble.sprite);
    const tag = sprite(640, 96, 1.9);
    disp.push(() => tag.tex.dispose(), () => tag.sprite.material.dispose());
    tag.sprite.position.set(0, lead ? 1.82 : 1.62, cz + 0.1);
    tag.sprite.renderOrder = 2;
    g.add(tag.sprite);
    const ringMat = new THREE.MeshBasicMaterial({ color: '#fbbf24', transparent: true, opacity: 0.7, side: THREE.DoubleSide, depthWrite: false });
    disp.push(() => ringMat.dispose());
    const ring = mesh(geo.ring, ringMat, 0, 0.025, -0.1, 1.15, 1.15, 1.15, false);
    ring.rotation.x = -Math.PI / 2;
    g.add(ring);
    const beamMat = new THREE.MeshBasicMaterial({ color: '#fbbf24', transparent: true, opacity: 0.1, side: THREE.DoubleSide, depthWrite: false, blending: THREE.AdditiveBlending });
    disp.push(() => beamMat.dispose());
    const beam = mesh(geo.beam, beamMat, 0, 2.5, -0.15, 1, 1, 1, false);
    g.add(beam);

    // The pull request's flag, at the desk's back corner.
    const flag = new THREE.Group();
    const flagMat = new THREE.MeshStandardMaterial({ color: '#34d399', side: THREE.DoubleSide });
    disp.push(() => flagMat.dispose());
    flag.position.set(lead ? 1.05 : 0.7, top, -0.3);
    flag.add(mesh(geo.cyl, mats.pole, 0, 0.25, 0, 0.008, 0.5, 0.008));
    const cloth = mesh(geo.plane, flagMat, 0.1, 0.42, 0, 0.2, 0.13, 1);
    flag.add(cloth);
    g.add(flag);

    // What a click picks: the desk and the robot, as one box.
    const hit = mesh(geo.box, mats.hit, 0, 0.75, lead ? -0.3 : -0.3, lead ? COUNTER.w : 1.5, 1.5, 1.45, false);
    hit.userData.ref = ref;
    g.add(hit);
    hits.push(hit);

    const obj: DeskObj = {
      ref,
      root: g,
      figure: fig,
      screen: { ctx, tex, key: '', mat: screenMat },
      tag: { sprite: tag.sprite, ctx: tag.ctx, tex: tag.tex, key: '' },
      bubble: { sprite: bubble.sprite, ctx: bubble.ctx, tex: bubble.tex, key: '' },
      beacon,
      ring,
      beam,
      flag,
      flagMat,
      glowMat,
      tone: 'idle',
      hit,
      dispose: disp,
    };
    desks.set(ref, obj);
    update(obj, input);
  }

  // update brings a desk up to date with the model: its tone, its monitor,
  // its name tag, its bubble, its flag.
  function update(d: DeskObj, input: SceneInput) {
    const isLead = d.ref.endsWith('/lead');
    const project = d.ref.split('/')[0];
    const area = input.model.areas.find((a) => a.project === project);
    const desk = isLead ? undefined : area?.desks.find((x) => x.id === d.ref);
    const tone: DeskTone = isLead ? leadAsDesk(area?.lead.tone ?? 'idle') : (desk?.tone ?? 'idle');
    d.tone = tone;
    const color = toneColor[tone];
    (d.beacon.material as THREE.MeshStandardMaterial).color.set(color);
    (d.beacon.material as THREE.MeshStandardMaterial).emissive.set(color);
    d.beacon.visible = tone !== 'stopped';
    d.glowMat.color.set(color);
    d.glowMat.emissive.set(color);
    d.glowMat.emissiveIntensity = tone === 'stopped' ? 0 : 0.8;
    const loud = tone === 'needs';
    d.ring.visible = loud || tone === 'error';
    (d.ring.material as THREE.MeshBasicMaterial).color.set(color);
    d.beam.visible = loud;

    const screen = input.screens.get(d.ref);
    const sKey = JSON.stringify(screen ?? null);
    if (screen && sKey !== d.screen.key) {
      d.screen.key = sKey;
      d.screen.data = screen;
      drawScreen(d.screen.ctx, screen);
      d.screen.tex.needsUpdate = true;
    }
    const title = isLead ? (area?.label ?? project) : (desk?.title ?? d.ref);
    const tagKey = `${title}\n${color}`;
    if (tagKey !== d.tag.key) {
      d.tag.key = tagKey;
      drawTag(d.tag.ctx, title, color);
      d.tag.tex.needsUpdate = true;
    }
    const b = bubbleOf[tone];
    d.bubble.sprite.visible = !!b;
    if (b && d.bubble.key !== tone) {
      d.bubble.key = tone;
      drawBubble(d.bubble.ctx, b[0], b[1]);
      d.bubble.tex.needsUpdate = true;
    }
    // Its marker, while it needs you.
    const loudNow = tone === 'needs' || tone === 'error';
    let marker = markers.get(d.ref);
    if (loudNow) {
      if (!marker) {
        marker = document.createElement('button');
        marker.type = 'button';
        marker.dataset.officeMarker = d.ref;
        marker.style.cssText =
          'position:absolute;left:0;top:0;pointer-events:auto;display:flex;align-items:center;gap:6px;max-width:150px;padding:2px 9px 2px 6px;border-radius:999px;background:rgba(10,14,24,.9);color:#fff;font:600 11.5px/1.25 var(--font-sans,system-ui),sans-serif;white-space:nowrap;box-shadow:0 4px 14px rgba(0,0,0,.4);cursor:pointer;will-change:transform';
        const dot = document.createElement('span');
        dot.style.cssText = 'flex:none;width:8px;height:8px;border-radius:50%';
        const label = document.createElement('span');
        label.style.cssText = 'overflow:hidden;text-overflow:ellipsis';
        marker.append(dot, label);
        const ref = d.ref;
        // On press, not click: the camera may still be easing, and a chip that
        // moved before the release would lose the click to the floor.
        marker.addEventListener('pointerdown', (e) => {
          e.preventDefault();
          e.stopPropagation();
          walkTo(ref);
        });
        markerLayer.appendChild(marker);
        markers.set(d.ref, marker);
      }
      (marker.firstChild as HTMLElement).style.background = color;
      marker.style.border = `1px solid ${color}88`;
      (marker.lastChild as HTMLElement).textContent = title;
      marker.setAttribute('aria-label', opts.markerLabel(title));
    } else if (marker) {
      marker.remove();
      markers.delete(d.ref);
    }
    const pr = desk?.pr;
    d.flag.visible = !!pr && pr.state !== 'closed';
    if (pr) d.flagMat.color.set(pr.checks === 'failing' ? '#ef4444' : pr.state === 'merged' ? '#a78bfa' : pr.state === 'draft' ? '#9ca3af' : '#22c55e');
  }

  // lighting follows the clock: daylight through the windows by day, a warm
  // lamp-lit room at night.
  function lighting(force = false) {
    const now = new Date();
    const hour = now.getHours() + now.getMinutes() / 60;
    const h = Math.floor(hour * 4) / 4;
    if (!force && h === skyHour) return;
    skyHour = h;
    paintSky(sky.ctx, hour);
    sky.tex.needsUpdate = true;
    const night = hour < 6 || hour >= 20;
    const dusk = !night && (hour < 8 || hour >= 17.5);
    const W = lay.room.x1;
    const D = lay.room.z1;
    // The sun comes in from the left in the morning, the right in the afternoon.
    const side = hour < 13 ? -1 : 1;
    sun.position.set(W / 2 + side * W * 0.6, 14, D / 2 - D * 0.5);
    if (night) {
      hemi.color.set('#c7d2fe');
      hemi.groundColor.set('#4b3a2e');
      hemi.intensity = 0.75;
      sun.color.set('#ffd9a8');
      sun.intensity = 0.9;
      fill.intensity = 0.35;
      scene.background = new THREE.Color('#141a2a');
    } else if (dusk) {
      hemi.color.set('#ffe2c4');
      hemi.groundColor.set('#7a5a44');
      hemi.intensity = 0.95;
      sun.color.set('#ffb27a');
      sun.intensity = 1.5;
      fill.intensity = 0.3;
      scene.background = new THREE.Color('#2a2236');
    } else {
      hemi.color.set('#e0ecff');
      hemi.groundColor.set('#8a6a4c');
      hemi.intensity = 1.05;
      sun.color.set('#fff3e0');
      sun.intensity = 1.9;
      fill.intensity = 0.25;
      scene.background = new THREE.Color('#1c2333');
    }
    if (lampShade) (lampShade.material as THREE.MeshStandardMaterial).emissiveIntensity = night || dusk ? 1.6 : 0.4;
  }

  function apply(input: SceneInput) {
    const key = input.model.areas.map((a) => `${a.project}:${a.label}:${a.hue}:${a.desks.map((d) => d.id).join(',')}`).join('|');
    if (key !== layKey) {
      layKey = key;
      lay = layOut(input.model);
      build(input);
      if (!placed && lastVisit) {
        Object.assign(pos, lastVisit.pos);
        facing = lastVisit.facing;
        followTarget = lastVisit.follow;
        cam.followK = lastVisit.follow;
        cam.yaw = lastVisit.yaw;
        cam.dist = lastVisit.dist;
        moved = true;
        placed = true;
        const back = path(boxes, bounds, pos, pos);
        if (back.length) Object.assign(pos, back[back.length - 1]);
        stillFor = 1;
        // Back where you were, close up already: no fade in from afar.
        focusRef = nearest(lay, pos);
        if (focusRef === 'board') {
          focusRef = undefined;
          held = true;
        } else if (focusRef) cam.closeK = 1;
      } else if (!placed) {
        pos.x = lay.spawn.x;
        pos.z = lay.spawn.z;
        facing = lay.spawn.face;
        placed = true;
      } else {
        // Where you stood may be a desk now: step to the nearest free floor.
        const route0 = path(boxes, bounds, pos, pos);
        if (route0.length) Object.assign(pos, route0[route0.length - 1]);
        if (routeGoal) walkTo(routeGoal);
      }
    } else for (const d of desks.values()) update(d, input);
    if (input.you !== youLabel) {
      youLabel = input.you;
      meMarker.textContent = youLabel;
      drawTag(meTag.ctx, youLabel, '#a78bfa');
      meTag.tex.needsUpdate = true;
    }
    syncAreaChips(input);
    const bKey = JSON.stringify(input.board);
    if (board && bKey !== board.key) {
      board.key = bKey;
      board.rows = drawBoard(board.ctx, input.board);
      board.tex.needsUpdate = true;
    }
    poke();
  }

  function walkTo(ref: string) {
    const spot = spotOf(lay, ref);
    if (!spot) return;
    route = path(boxes, bounds, pos, spot);
    routeGoal = ref;
    followTarget = 1;
    started();
    poke();
  }

  function walkToPoint(p: P) {
    route = path(boxes, bounds, pos, p);
    routeGoal = undefined;
    followTarget = 1;
    started();
    poke();
  }

  function started() {
    held = false;
    if (moved) return;
    moved = true;
    mePulse?.cancel();
    opts.onMoved();
  }

  let followTarget = 0;
  // overview stands back over the whole room, and stays there, close-up and
  // all, until you move again.
  let held = false;
  function overview() {
    followTarget = 0;
    cam.yaw = 0;
    held = true;
    poke();
  }

  // The frame.
  let raf = 0;
  let last = performance.now();
  let clock = 0;
  let running = false;
  let nap = 0;
  function wake() {
    if (document.hidden || disposed) return;
    if (nap) {
      // Napping between idle frames: wake now.
      window.clearTimeout(nap);
      nap = 0;
      raf = requestAnimationFrame(frame);
      return;
    }
    if (running) return;
    running = true;
    last = performance.now();
    raf = requestAnimationFrame(frame);
  }

  function frame(now: number) {
    if (disposed) return;
    const dt = Math.min(0.05, (now - last) / 1000);
    last = now;
    clock += dt;
    const idleFor = now - lastInput;
    const busy = step(dt);
    // While nothing moves: a few frames a second for the robots' life, or
    // none at all with reduced motion.
    const lively = busy || idleFor < 4000;
    if (lively || dirty || motion) {
      dirty = false;
      render(now);
    }
    if (lively) {
      raf = requestAnimationFrame(frame);
      return;
    }
    if (!motion) {
      running = false;
      return;
    }
    // Idle: the robots' life at 8 frames a second, 2 after a minute untouched.
    nap = window.setTimeout(() => {
      nap = 0;
      raf = requestAnimationFrame(frame);
    }, idleFor > 60_000 ? 500 : 125);
  }

  // step moves you and the camera; true while either is still moving.
  function step(dt: number): boolean {
    let moving = false;
    // The keys walk you, relative to where the camera looks.
    let kx = 0;
    let kz = 0;
    if (keys.has('w') || keys.has('arrowup')) kz -= 1;
    if (keys.has('s') || keys.has('arrowdown')) kz += 1;
    if (keys.has('a') || keys.has('arrowleft')) kx -= 1;
    if (keys.has('d') || keys.has('arrowright')) kx += 1;
    let vx = 0;
    let vz = 0;
    if (kx || kz) {
      route = [];
      routeGoal = undefined;
      followTarget = 1;
      started();
      const len = Math.hypot(kx, kz);
      const c = Math.cos(eased.yaw);
      const s = Math.sin(eased.yaw);
      vx = ((kx * c + kz * s) / len) * SPEED;
      vz = ((-kx * s + kz * c) / len) * SPEED;
    } else if (route.length) {
      const goal = route[0];
      const dx = goal.x - pos.x;
      const dz = goal.z - pos.z;
      const dist = Math.hypot(dx, dz);
      // A long walk is a jog, so a desk across the room is a moment away.
      const left = dist + pathLengthFrom(route);
      const pace = Math.min(14, Math.max(SPEED, left / 1.0));
      const stepLen = pace * dt;
      if (dist <= stepLen) {
        pos.x = goal.x;
        pos.z = goal.z;
        route.shift();
        if (!route.length && routeGoal) {
          const spot = spotOf(lay, routeGoal);
          if (spot) facing = motion ? facing : spot.face;
        }
      } else {
        vx = (dx / dist) * pace;
        vz = (dz / dist) * pace;
      }
    }
    if (vx || vz) {
      const next = kx || kz ? slide(boxes, bounds, pos, { x: vx * dt, z: vz * dt }) : { x: pos.x + vx * dt, z: pos.z + vz * dt };
      const moved1 = Math.hypot(next.x - pos.x, next.z - pos.z);
      pos.x = next.x;
      pos.z = next.z;
      speed = moved1 / Math.max(dt, 1e-3);
      facing = turn(facing, Math.atan2(vx, vz), motion ? dt * 12 : Math.PI);
      moving = true;
      stillFor = 0;
    } else {
      speed = 0;
      stillFor += dt;
      // Arrived: turn to face what you walked to.
      if (routeGoal) {
        const spot = spotOf(lay, routeGoal);
        if (spot) {
          const f = turn(facing, spot.face, motion ? dt * 8 : Math.PI);
          if (Math.abs(f - facing) > 1e-3) moving = true;
          facing = f;
        }
      }
    }
    me.group.position.set(pos.x, me.group.position.y, pos.z);
    me.group.rotation.y = facing;
    me.stride(speed, clock, motion);
    meRing.position.set(pos.x, 0.02, pos.z);

    // What you're at, once you've stopped.
    // Standing back for the overview, you're at nothing.
    const at = stillFor > 0.15 && !held ? nearest(lay, pos) : undefined;
    if (at !== near) {
      near = at;
      opts.onNear(near);
      dirty = true;
    }
    return camStep(dt) || moving;
  }

  // focusOf is what the close-up looks at, standing at ref: a desk's monitor,
  // a counter's, or the board, and from how far.
  function focusOf(ref: string, aspect: number): { x: number; y: number; z: number; dist: number; pitch: number; yaw: number } | undefined {
    if (ref === 'board') {
      // From high over the first row, so no desk is in the way, the whole
      // board in the part of the view the board's list (on the right) leaves.
      const tan = Math.tan(THREE.MathUtils.degToRad(FOV / 2));
      const bh = Math.min(2.6, lay.board.w * 0.52);
      const free = Math.max(0.45, 1 - 380 / Math.max(1, canvas.clientWidth));
      const pitch = 0.4;
      // Wide enough for you, standing at its left end, too.
      const dist = Math.max((lay.board.w + 1.6) / (free * 2 * tan * aspect), ((bh + 0.5) * Math.cos(pitch)) / (0.85 * 2 * tan));
      const vw = 2 * dist * tan * aspect;
      return { x: lay.board.x - 0.55 + (vw * (1 - free)) / 2, y: 0.45 + bh / 2, z: 0.1, dist, pitch, yaw: 0 };
    }
    for (const a of lay.areas) {
      if (ref === `${a.project}/lead`) return { x: a.lead.x + 0.3, y: 1.3, z: a.lead.z, dist: 3.9, pitch: 0.42, yaw: 0.32 };
      const d = a.desks.find((x) => x.id === ref);
      if (d) return { x: d.x + 0.15, y: 1.05, z: d.z + 0.1, dist: 4.3, pitch: 0.62, yaw: 0.3 };
    }
    return undefined;
  }

  // fitDistance is how far back the overview stands for the whole room, its
  // floor and the tops of its walls, to be in view: found by trying, since a
  // tilted view of a box has no neat formula. Kept until the room or the
  // window's shape changes.
  const fitProbe = new THREE.PerspectiveCamera(FOV, 1, 0.1, 400);
  let fitCache = { key: '', dist: 20, shift: 0 };
  let inset = 0;
  function fitDistance(W: number, D: number, aspect: number, pitch: number): { dist: number; shift: number } {
    const key = `${W.toFixed(2)}:${D.toFixed(2)}:${aspect.toFixed(3)}:${inset}`;
    if (fitCache.key === key) return fitCache;
    const tan = Math.tan(THREE.MathUtils.degToRad(FOV / 2));
    // The share of the width the overlay covers, which the target moves left
    // by half of, so the room sits in the middle of what's left.
    const cover = Math.min(0.45, inset / Math.max(1, canvas.clientWidth));
    const left = -0.94 + 2 * cover;
    fitProbe.aspect = aspect;
    fitProbe.updateProjectionMatrix();
    const tx = W / 2;
    const tz = D / 2 + 0.6;
    const corners = [0, W].flatMap((x) => [0, D].flatMap((z) => [0, 2.2].map((y) => new THREE.Vector3(x, y, z))));
    const v = new THREE.Vector3();
    const shiftAt = (d: number) => d * tan * aspect * cover;
    const fits = (d: number) => {
      const sx = tx - shiftAt(d);
      fitProbe.position.set(sx, Math.sin(pitch) * d, tz + Math.cos(pitch) * d);
      fitProbe.lookAt(sx, 0, tz);
      fitProbe.updateMatrixWorld();
      return corners.every((c) => {
        v.copy(c).project(fitProbe);
        return v.x > left && v.x < 0.94 && v.y < 0.7 && v.y > -0.8 && v.z < 1;
      });
    };
    let lo = 4;
    let hi = 200;
    for (let i = 0; i < 24; i++) {
      const mid = (lo + hi) / 2;
      if (fits(mid)) hi = mid;
      else lo = mid;
    }
    fitCache = { key, dist: hi, shift: shiftAt(hi) };
    return fitCache;
  }

  // camStep eases the camera to where it should be; true while it's easing.
  function camStep(dt: number): boolean {
    const W = lay.room.x1;
    const D = lay.room.z1;
    const aspect = camera.aspect || 1;
    // From the overview down to following you is a long way: taken gently.
    // Standing back is quicker than coming down to you.
    const k = motion ? 1 - Math.exp(-dt * (followTarget < cam.followK ? 4.5 : 2.6)) : 1;
    cam.followK += (followTarget - cam.followK) * k;
    if (Math.abs(followTarget - cam.followK) < 1e-3) cam.followK = followTarget;
    // Close up at what you're at, and gliding into it over a walk's last
    // few metres, so you arrive in the close-up rather than settle into it.
    let closeTarget = near && stillFor > 0.15 && !held ? 1 : 0;
    if (!closeTarget && routeGoal && route.length) {
      const rem = Math.hypot(route[0].x - pos.x, route[0].z - pos.z) + pathLengthFrom(route);
      if (rem < 3.5) closeTarget = 1 - rem / 3.5;
    }
    if (closeTarget > 0) focusRef = near ?? routeGoal;
    cam.closeK += (closeTarget - cam.closeK) * (motion ? 1 - Math.exp(-dt * 3) : 1);
    if (Math.abs(closeTarget - cam.closeK) < 1e-3) cam.closeK = closeTarget;

    // The overview fits the room, from the front and above.
    const oPitch = 0.92;
    const { dist: oDist, shift: oShift } = fitDistance(W, D, aspect, oPitch);
    // Following: behind you, looking down at you; close up at a desk, lower
    // and nearer, the monitor and the robot in view.
    // Close up, the camera looks past your shoulder at the monitor, from the
    // right so you don't stand in the way.
    const c = cam.closeK;
    const focus = focusRef ? focusOf(focusRef, aspect) : undefined;
    focusAt = focus;
    const fPitch = 0.78;
    const fDist = cam.dist;
    const f = cam.followK;
    let tx = W / 2 - oShift + (pos.x - W / 2 + oShift) * f;
    let tz = D / 2 + 0.6 + (pos.z - D / 2 - 0.6) * f;
    let ty = 0.9 * f;
    let dist = oDist + (fDist - oDist) * f;
    let pitch = oPitch + (fPitch - oPitch) * f;
    let yaw = cam.yaw;
    if (focus && c > 0) {
      tx += (focus.x - tx) * c;
      ty += (focus.y - ty) * c;
      tz += (focus.z - tz) * c;
      dist += (focus.dist - dist) * c;
      pitch += (focus.pitch - pitch) * c;
      yaw += (focus.yaw - yaw) * c;
    }
    const before = eased.tx + eased.tz + eased.dist + eased.pitch + eased.yaw;
    if (!eased.init) {
      Object.assign(eased, { tx, ty, tz, dist, pitch, yaw, init: true });
    } else {
      // Standing back for the overview settles quickly; following you, gently.
      const e = motion ? 1 - Math.exp(-dt * (held ? 7 : 3.6)) : 1;
      eased.tx += (tx - eased.tx) * e;
      eased.ty += (ty - eased.ty) * e;
      eased.tz += (tz - eased.tz) * e;
      eased.dist += (dist - eased.dist) * e;
      eased.pitch += (pitch - eased.pitch) * e;
      eased.yaw += (yaw - eased.yaw) * (motion ? 1 - Math.exp(-dt * 10) : 1);
    }
    const cp = Math.cos(eased.pitch);
    camera.position.set(eased.tx + Math.sin(eased.yaw) * cp * eased.dist, eased.ty + Math.sin(eased.pitch) * eased.dist, eased.tz + Math.cos(eased.yaw) * cp * eased.dist);
    camera.lookAt(eased.tx, eased.ty, eased.tz);
    // A side wall the camera is behind fades away.
    for (const w of sideWalls) {
      const behind = w.side === 'left' ? camera.position.x < 0.5 : camera.position.x > W - 0.5;
      const o = behind ? 0.08 : 1;
      w.mat.opacity += (o - w.mat.opacity) * (motion ? 0.2 : 1);
      w.mat.depthWrite = w.mat.opacity > 0.9;
    }
    const after = eased.tx + eased.tz + eased.dist + eased.pitch + eased.yaw;
    // The chips over the canvas show only once the camera has settled: one
    // sliding under the pointer would send a click somewhere else.
    // Moving is moving visibly, not the easing's last hair.
    const moving = Math.abs(after - before) > 0.02;
    settledFor = moving ? 0 : settledFor + dt;
    const show = settledFor > 0.12;
    if (show !== chipsShown) {
      chipsShown = show;
      // Still there, and still to be clicked, while it moves: a little dimmed.
      markerLayer.style.opacity = show ? '1' : '0.7';
    }
    return Math.abs(after - before) > 1e-4 || cam.followK !== followTarget || cam.closeK !== closeTarget;
  }

  function render(now: number) {
    const t = now / 1000;
    lighting();
    for (const d of desks.values()) {
      d.figure.pose(d.tone, t, motion);
      // Up close, what floats over the desks beside the camera would be in
      // the way of the one you're looking at.
      // So does the row in front of the desk you're at: it sits between you
      // and the camera.
      const p0 = d.root.position;
      const ahead = !!focusAt && focusRef !== 'board' && p0.z > focusAt.z + 1 && p0.z - focusAt.z < 4.5 && Math.abs(p0.x - focusAt.x) < 3.2;
      const inWay = cam.closeK > 0.3 && d.ref !== focusRef && (p0.distanceTo(camera.position) < 4.2 || ahead);
      d.root.visible = !(inWay && ahead);
      d.bubble.sprite.visible = !inWay && !!bubbleOf[d.tone];
      const b = d.beacon;
      b.visible = !inWay && d.tone !== 'stopped';
      b.rotation.y = motion ? t * (d.tone === 'working' ? 2.2 : 0.8) : 0.6;
      b.position.y = 1.95 + (motion ? Math.sin(t * 2 + b.position.x) * 0.05 : 0);
      const s = d.bubble.sprite;
      s.position.y = 2.05 + (motion && d.tone === 'needs' ? Math.abs(Math.sin(t * 3.5)) * 0.18 : 0);
      if (d.ring.visible) {
        const p = motion ? (t * 0.8) % 1 : 0.5;
        d.ring.scale.setScalar(0.9 + p * 0.5);
        (d.ring.material as THREE.MeshBasicMaterial).opacity = 0.85 * (1 - p);
      }
      // The beams stand down while you read the board over them.
      d.beam.visible = d.tone === 'needs' && !(focusRef === 'board' && cam.closeK > 0.2);
      if (d.beam.visible) (d.beam.material as THREE.MeshBasicMaterial).opacity = 0.09 + (motion ? Math.sin(t * 3) * 0.035 : 0);
      if (d.flag.visible && motion) d.flag.children[1].rotation.y = Math.sin(t * 3 + b.position.x) * 0.25;
      // The monitor you're at blinks its cursor.
      if (d.ref === near && d.screen.data && d.tone === 'working') {
        const blink = Math.floor(t * 2);
        if (blink !== (d.screen as { blink?: number }).blink) {
          (d.screen as { blink?: number }).blink = blink;
          drawScreen(d.screen.ctx, d.screen.data, t);
          d.screen.tex.needsUpdate = true;
        }
      }
      // Hovered: lifted a little.
      d.root.position.y = d.ref === hover ? 0.02 : 0;
    }
    // Signs give way to the camera, and to the board's close-up over them.
    for (const sg of signs) sg.visible = !(focusRef === 'board' && cam.closeK > 0.2) && sg.position.distanceTo(camera.position) > 3.5;
    // From far off, each area's label floats over its sign. What the labels
    // and your chip take, the desks' chips keep clear of.
    const taken: { x0: number; x1: number; y0: number; y1: number }[] = [];
    const ap = new THREE.Vector3();
    for (const [i, place] of lay.areas.entries()) {
      const chip = areaChips.get(place.project);
      if (!chip) continue;
      const sg = signs[i];
      ap.set(sg ? sg.position.x : place.lead.x, 3.4, sg ? sg.position.z : place.rug.z0);
      const far = ap.distanceTo(camera.position) > 15;
      ap.project(camera);
      const on = far && ap.z < 1 && Math.abs(ap.x) < 1 && Math.abs(ap.y) < 1;
      chip.style.display = on ? 'flex' : 'none';
      if (on) {
        const cx = ((ap.x + 1) / 2) * canvas.clientWidth;
        const cy = ((1 - ap.y) / 2) * canvas.clientHeight;
        chip.style.transform = `translate(${cx}px, ${cy}px) translate(-50%, -100%)`;
        const w = chip.offsetWidth || 140;
        const h = chip.offsetHeight || 38;
        taken.push({ x0: cx - w / 2, x1: cx + w / 2, y0: cy - h, y1: cy });
      }
      if (sg && on) sg.visible = false;
    }
    // You fade while you look at a monitor, so you never stand in front of it.
    // At a counter you'd stand in front of its monitor: you all but fade. At
    // a desk you stand aside, half seen through, so you hide nothing.
    me.fade(1 - cam.closeK * (focusRef?.endsWith('/lead') ? 0.85 : 0.68));
    meTag.sprite.visible = cam.closeK < 0.3;
    // Name tags grow with distance to stay readable, up to a point; from far
    // off only the ones that need you stay, and up close they get out of the way.
    const tp = new THREE.Vector3();
    const shown: { el: HTMLButtonElement; x: number; y: number }[] = [];
    for (const d of desks.values()) {
      const tag = d.tag.sprite;
      tag.getWorldPosition(tp);
      const dist = tp.distanceTo(camera.position);
      const marker = markers.get(d.ref);
      // From afar, a marker stands in for the tag.
      const far = dist > 15;
      if (marker) {
        tp.project(camera);
        const onScreen = far && tp.z < 1 && Math.abs(tp.x) < 1.05 && Math.abs(tp.y) < 1.05;
        marker.style.display = onScreen ? 'flex' : 'none';
        if (onScreen) shown.push({ el: marker, x: ((tp.x + 1) / 2) * canvas.clientWidth, y: ((1 - tp.y) / 2) * canvas.clientHeight });
      }
      tag.visible = !(d.ref === focusRef && cam.closeK > 0.3) && dist > 2.6 && !(marker && far) && dist < 18 && !(cam.closeK > 0.3 && d.root.position.distanceTo(camera.position) < 4.2) && d.root.visible;
      const w = Math.min(2.1, Math.max(0.8, dist * 0.085));
      tag.scale.set(w, (w * 96) / 640, 1);
    }
    // The desks' chips stack clear of the labels and of each other, the
    // nearest (lowest on screen) where it belongs. Their stacking is worked
    // out only while the camera is still, and kept while it moves, so a chip
    // never jumps or goes as you reach for it; one with no room stays where
    // it is rather than vanish.
    shown.sort((a, b) => b.y - a.y);
    const h = 24;
    const placed: { x0: number; x1: number; y: number }[] = taken.flatMap((r) => {
      // As the anchors of chips that would cover it (a chip's box runs from
      // 31px above its anchor to 7 above), a row every 20px.
      const rows: { x0: number; x1: number; y: number }[] = [];
      for (let y = r.y1 + 31; y > r.y0 + 7; y -= 20) rows.push({ x0: r.x0, x1: r.x1, y });
      return rows;
    });
    const restack = chipsShown;
    for (const m of shown) {
      const w = m.el.offsetWidth || 120;
      const x0 = m.x - w / 2;
      const x1 = m.x + w / 2;
      let dy = chipDy.get(m.el) ?? 0;
      if (restack || !chipDy.has(m.el)) {
        const clash = (y: number) => placed.some((p) => x0 < p.x1 + 4 && x1 > p.x0 - 4 && Math.abs(p.y - y) < h);
        dy = [0, -h, h, -2 * h, 2 * h, -3 * h, 3 * h].find((d) => !clash(m.y + d)) ?? 0;
        chipDy.set(m.el, dy);
      }
      placed.push({ x0, x1, y: m.y + dy });
      m.el.style.transform = `translate(${m.x}px, ${m.y + dy}px) translate(-50%, -130%)`;
    }
    // You, from far off: a chip like theirs, in your colour, over your head,
    // or under your feet when a chip or a label is there. It gives way to
    // them, never they to it.
    meTag.sprite.getWorldPosition(tp);
    const meFar = tp.distanceTo(camera.position) > 15;
    tp.project(camera);
    meMarker.style.display = meFar && tp.z < 1 ? 'block' : 'none';
    if (meFar) {
      const mx = ((tp.x + 1) / 2) * canvas.clientWidth;
      const my = ((1 - tp.y) / 2) * canvas.clientHeight;
      const mw = meMarker.offsetWidth || 44;
      const over = { x0: mx - mw / 2, x1: mx + mw / 2, y0: my - 40, y1: my - 18 };
      const hits = (r: { x0: number; x1: number; y0: number; y1: number }) => over.x0 < r.x1 && over.x1 > r.x0 && over.y0 < r.y1 && over.y1 > r.y0;
      const under = taken.some(hits) || placed.some((p) => hits({ x0: p.x0, x1: p.x1, y0: p.y - 31, y1: p.y - 7 }));
      meMarker.style.transform = `translate(${mx}px, ${my}px) translate(-50%, ${under ? '140%' : '-110%'})`;
      meTag.sprite.visible = false;
    }
    const ringO = 0.55 + (motion ? Math.sin(t * 2.5) * 0.25 : 0.2);
    (meRing.material as THREE.MeshBasicMaterial).opacity = ringO;
    renderer.render(scene, camera);
  }

  // Sizes the canvas to its box.
  function resize() {
    const w = Math.max(1, host.clientWidth);
    const h = Math.max(1, host.clientHeight);
    renderer.setSize(w, h, false);
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
    poke();
  }
  const ro = new ResizeObserver(resize);
  ro.observe(host);
  resize();

  function pick(clientX: number, clientY: number): { ref?: string; row?: string; floor?: P } {
    const r = canvas.getBoundingClientRect();
    ndc.set(((clientX - r.left) / r.width) * 2 - 1, -((clientY - r.top) / r.height) * 2 + 1);
    ray.setFromCamera(ndc, camera);
    const hit = ray.intersectObjects(hits, false)[0];
    if (hit && hit.object.userData.ref === 'board' && board?.face) {
      // A row on the board itself: what it's about.
      const f = ray.intersectObject(board.face, false)[0];
      if (f?.uv) {
        const { width: W, height: H } = board.ctx.canvas;
        const px = f.uv.x * W;
        const py = (1 - f.uv.y) * H;
        const row = board.rows.find((b) => px >= b.x && px <= b.x + b.w && py >= b.y && py <= b.y + b.h);
        if (row) return { ref: 'board', row: row.ref };
      }
    }
    if (hit) return { ref: hit.object.userData.ref as string };
    const p = new THREE.Vector3();
    if (ray.ray.intersectPlane(floorPlane, p)) return { floor: { x: p.x, z: p.z } };
    return {};
  }

  const onPointerDown = (e: PointerEvent) => {
    canvas.focus({ preventScroll: true });
    pointer = { x: e.clientX, y: e.clientY, id: e.pointerId, dragged: false, button: e.button };
    canvas.setPointerCapture(e.pointerId);
    poke();
  };
  const onPointerMove = (e: PointerEvent) => {
    if (pointer && pointer.id === e.pointerId) {
      const dx = e.clientX - pointer.x;
      if (!pointer.dragged && Math.hypot(dx, e.clientY - pointer.y) > 5) pointer.dragged = true;
      if (pointer.dragged) {
        // Dragging turns the room, within limits.
        cam.yaw = Math.max(-1.0, Math.min(1.0, cam.yaw - e.movementX * 0.006));
        canvas.style.cursor = 'grabbing';
        poke();
        return;
      }
    }
    hoverDirty = true;
    hoverAt = { x: e.clientX, y: e.clientY };
    if (!pendingHover) {
      pendingHover = true;
      requestAnimationFrame(() => {
        pendingHover = false;
        if (!hoverDirty || !hoverAt) return;
        hoverDirty = false;
        const { ref } = pick(hoverAt.x, hoverAt.y);
        canvas.style.cursor = ref ? 'pointer' : 'default';
        if (ref !== hover) {
          hover = ref;
          opts.onHover(ref);
          poke();
        }
      });
    }
  };
  let hoverAt: { x: number; y: number } | undefined;
  let pendingHover = false;
  const onPointerUp = (e: PointerEvent) => {
    if (!pointer || pointer.id !== e.pointerId) return;
    const p = pointer;
    pointer = undefined;
    canvas.style.cursor = hover ? 'pointer' : 'default';
    if (p.dragged || p.button !== 0) return;
    const { ref, row, floor } = pick(e.clientX, e.clientY);
    // At the board, a row opens what it's about; from afar it walks you there.
    if (row && near === 'board') opts.onBoardRow(row);
    else if (ref) {
      if (ref === near && !route.length) opts.onActivate(ref);
      else walkTo(ref);
    } else if (floor) walkToPoint(floor);
  };
  const onPointerLeave = () => {
    if (hover) {
      hover = undefined;
      opts.onHover(undefined);
      poke();
    }
  };
  // The wheel zooms in steps from the overview down to following you, and
  // then closer; a trackpad's small deltas make small steps.
  const onWheel = (e: WheelEvent) => {
    e.preventDefault();
    // A mouse wheel's notch is a whole step, whatever its delta; a
    // trackpad's little deltas are parts of one.
    const d = e.deltaMode === 1 ? e.deltaY * 33 : e.deltaY;
    const n = Math.max(-3, Math.min(3, Math.abs(d) >= 50 ? Math.sign(d) * Math.max(1, Math.abs(d) / 120) : d / 100));
    if (n < 0) {
      if (followTarget < 1) followTarget = Math.min(1, followTarget - 0.34 * n);
      else cam.dist = Math.max(6.5, cam.dist * Math.pow(0.82, -n));
    } else if (followTarget >= 1 && cam.dist < 11) cam.dist = Math.min(11, cam.dist / Math.pow(0.82, n));
    else {
      followTarget = Math.max(0, followTarget - 0.34 * n);
      cam.dist = 11;
    }
    poke();
  };
  const editable = (el: Element | null) => !!el && (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || (el as HTMLElement).isContentEditable || el.closest('[role="dialog"]') !== null);
  const walkKeys = new Set(['w', 'a', 's', 'd', 'arrowup', 'arrowdown', 'arrowleft', 'arrowright']);
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.ctrlKey || e.metaKey || e.altKey || editable(document.activeElement) || document.querySelector('[role="dialog"]')) return;
    const k = e.key.toLowerCase();
    if (walkKeys.has(k)) {
      // Arrows walk only while the office has focus, so they still scroll
      // and move through lists elsewhere.
      if (k.startsWith('arrow') && document.activeElement !== canvas && document.activeElement !== document.body) return;
      keys.add(k);
      e.preventDefault();
      poke();
    } else if (k === 'escape' && (near || followTarget > 0) && !e.repeat) {
      // Escape stands back: out of the close-up, to the overview.
      overview();
    } else if (k === 'n' && !e.repeat) {
      e.preventDefault();
      opts.onNext();
    } else if ((k === 'e' || k === 'enter') && near && !e.repeat) {
      if (k === 'enter' && document.activeElement !== canvas) return;
      e.preventDefault();
      opts.onActivate(near);
    }
  };
  const onKeyUp = (e: KeyboardEvent) => {
    keys.delete(e.key.toLowerCase());
    poke();
  };
  const onBlur = () => keys.clear();
  const onVisibility = () => {
    if (document.hidden) {
      cancelAnimationFrame(raf);
      window.clearTimeout(nap);
      nap = 0;
      running = false;
    } else poke();
  };
  canvas.addEventListener('pointerdown', onPointerDown);
  canvas.addEventListener('pointermove', onPointerMove);
  canvas.addEventListener('pointerup', onPointerUp);
  canvas.addEventListener('pointerleave', onPointerLeave);
  canvas.addEventListener('wheel', onWheel, { passive: false });
  canvas.addEventListener('contextmenu', (e) => e.preventDefault());
  window.addEventListener('keydown', onKeyDown);
  window.addEventListener('keyup', onKeyUp);
  window.addEventListener('blur', onBlur);
  document.addEventListener('visibilitychange', onVisibility);
  // The sky moves on with the clock, even while you're away.
  const tick = window.setInterval(() => {
    const before = skyHour;
    lighting();
    if (skyHour !== before) poke();
  }, 60_000);

  return {
    apply,
    walkTo,
    overview,
    setReducedMotion(on) {
      motion = !on;
      poke();
    },
    setInset(left) {
      inset = left;
      fitCache.key = '';
      poke();
    },
    dispose() {
      // Coming back to the board is coming back to the room: the overview.
      lastVisit = near === 'board' ? { pos: { ...pos }, facing, follow: 0, yaw: 0, dist: 11 } : { pos: { ...pos }, facing, follow: followTarget, yaw: cam.yaw, dist: cam.dist };
      disposed = true;
      cancelAnimationFrame(raf);
      window.clearTimeout(nap);
      window.clearInterval(tick);
      ro.disconnect();
      window.removeEventListener('keydown', onKeyDown);
      window.removeEventListener('keyup', onKeyUp);
      window.removeEventListener('blur', onBlur);
      document.removeEventListener('visibilitychange', onVisibility);
      for (const d of roomDispose) d();
      for (const d of desks.values()) for (const f of d.dispose) f();
      mePulse?.cancel();
      markerLayer.remove();
      me.dispose();
      floorTex.dispose();
      sky.tex.dispose();
      for (const m of Object.values(mats)) m.dispose();
      renderer.dispose();
      canvas.remove();
    },
  };
}

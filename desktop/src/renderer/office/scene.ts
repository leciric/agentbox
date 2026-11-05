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
import { RoomEnvironment } from 'three/addons/environments/RoomEnvironment.js';
import { EffectComposer } from 'three/addons/postprocessing/EffectComposer.js';
import { GTAOPass } from 'three/addons/postprocessing/GTAOPass.js';
import { OutputPass } from 'three/addons/postprocessing/OutputPass.js';
import { RenderPass } from 'three/addons/postprocessing/RenderPass.js';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';
import { bake, hash, mascot, walker as makeWalker, type Figure } from './figures.ts';
import { loadKit, piece } from './furniture.ts';
import { player } from './kenney.ts';
import { boardRef, COUNTER, DESK, SEAT, layout as layOut, nearest, roomAt, roomOf, spotOf, walkable, type Box, type Campus } from './layout.ts';
import type { DeskTone, LeadTone, OfficeModel } from './model.ts';
import { diorama, wallHeight } from './decor.ts';
import { canvasTexture, drawBoard, type BoardRow, drawBubble, drawScreen, drawTag, toneColor, type BoardScreen, type Screen } from './textures.ts';
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
  areaLabels: ReadonlyMap<string, { name: string; sub: string; count: number }>;
};

export type OfficeSceneOptions = {
  reducedMotion: boolean;
  // What you're standing at: a desk's ref, project/lead, 'board', or nothing.
  onNear(ref: string | undefined): void;
  // Which room you're in (its project and floor), or none for the campus, and
  // whether the camera is at that room's (or the campus's) overview.
  onView?(view: { project?: string; floor: number; floors: number; overview: boolean; refs?: string[] }): void;
  // What the pointer is over, for the cursor and the hint.
  onHover(ref: string | undefined): void;
  // A click on what you're standing at, or E: open it.
  onActivate(ref: string): void;
  // You moved for the first time (the controls hint can go).
  onMoved(): void;
  // N: on to the next desk that needs you.
  onNext(): void;
  // A chip's "+n": show the list of who needs you.
  onMore(refs: string[], at: { x: number; y: number }): void;
  // A click on a row of the board, standing at it: the desk it's about.
  onBoardRow(ref: string): void;
  // The "+n" button's name for screen readers, and its words: "+2 more".
  moreLabel: string;
  moreLabelShort(n: number): string;
  // The word on a lead's counter, over its project's name.
  leadWord?: string;
  // A project's floor, as its pill says it: "agentbox · 2".
  floorLabel?(name: string, floor: number): string;
  // A marker's name for screen readers: "Walk to {title}".
  markerLabel(title: string): string;
};

export type OfficeScene = {
  apply(input: SceneInput): void;
  walkTo(ref: string): void;
  // A step back: from a desk or following you to the room's overview, from
  // there to the campus. campus goes straight there.
  overview(): void;
  campus(): void;
  setReducedMotion(on: boolean): void;
  // How much of the canvas's left edge an overlay covers, in CSS pixels: the
  // overview frames the room in the rest.
  setInset(left: number): void;
  // attach puts the room, as it is, into a page again, with that page's
  // handlers; detach takes it out and stops drawing, keeping it built.
  attach(host: HTMLElement, opts: OfficeSceneOptions): void;
  detach(): void;
  dispose(): void;
};

// Where you were when you left the Office, so coming back from an agent's
// chat puts you back at its desk rather than at the door.
let lastVisit: { pos: P; facing: number; follow: number; yaw: number; dist: number; scope: number | null } | undefined;

// A room's board, as a ref.
const isBoard = (r: string | undefined): r is string => !!r && r.startsWith('board:');

const RADIUS = 0.3;
const SPEED = 3.4; // metres a second, walking
const FOV = 24;
// The overview's 3/4 view: from the front right, over the open corner.
const HOME_YAW = 0.76;
// Over the campus, more from the front: its rows of rooms read left to right.
const CAMPUS_YAW = 0.3;
const OVERVIEW_PITCH = 0.62;
// Following you, the camera looks down this steeply.
const FOLLOW_PITCH = 0.7;

// rug is a flat rounded rectangle, w by d with corners of radius r, h thick,
// lying on the floor.
function rug(w: number, d: number, r: number, h: number): THREE.BufferGeometry {
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

// The layer of what's see-through, which ambient occlusion leaves out.
const SEE = 1;
function seeThrough(root: THREE.Object3D) {
  root.traverse((o) => {
    const mat = (o as THREE.Mesh).material as THREE.Material | THREE.Material[] | undefined;
    if ((o as THREE.Sprite).isSprite || (mat && !Array.isArray(mat) && mat.transparent)) o.layers.set(SEE);
  });
}

// press acts on a pointer's press (the camera may still be easing, and a
// chip that moved before the release would lose the click to the floor), and
// on a click that came without one: the keyboard's, a screen reader's.
function press(el: HTMLElement, fn: () => void) {
  let at = -1e9;
  el.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    e.stopPropagation();
    at = performance.now();
    fn();
  });
  el.addEventListener('click', (e) => {
    e.stopPropagation();
    if (performance.now() - at > 700) fn();
  });
}

const leadAsDesk = (t: LeadTone): DeskTone => (t === 'needs' ? 'needs' : t === 'working' ? 'working' : 'idle');
const std = (color: THREE.ColorRepresentation, o: Partial<THREE.MeshStandardMaterialParameters> = {}) => new THREE.MeshStandardMaterial({ color, roughness: 0.7, ...o });

// Materials every desk shares, made once.
function sharedMaterials() {
  const phys = (color: THREE.ColorRepresentation, o: Partial<THREE.MeshPhysicalMaterialParameters> = {}) => new THREE.MeshPhysicalMaterial({ color, roughness: 0.5, ...o });
  return {
    oak: phys('#dfa86e', { roughness: 0.5, clearcoat: 0.25, clearcoatRoughness: 0.5 }),
    chairs: ['#e9b44c', '#4aa3a2', '#e07a5f', '#8fb39a', '#7c8bd6'].map((c) => std(c, { roughness: 0.75 })),
    deskTop: phys('#efe6d8', { roughness: 0.45, clearcoat: 0.2 }),
    deskLeg: std('#2d333f', { metalness: 0.6, roughness: 0.35 }),
    // Light, warm frames, as the toy furniture's.
    metal: std('#d8c7b0', { metalness: 0.1, roughness: 0.55 }),
    plastic: phys('#1f2430', { roughness: 0.35, clearcoat: 0.5 }),
    fabric: std('#3a4763', { roughness: 0.92 }),
    alu: phys('#d3d7de', { metalness: 0.75, roughness: 0.28 }),
    keys: std('#262b35', { roughness: 0.7 }),
    bezel: phys('#141922', { roughness: 0.3, clearcoat: 0.8, clearcoatRoughness: 0.2 }),
    ceramic: phys('#fbfaf7', { roughness: 0.25, clearcoat: 0.8 }),
    penA: std('#ef6f6c'),
    penB: std('#4f86f7'),
    noteA: std('#ffe27a', { side: THREE.DoubleSide }),
    noteB: std('#ffb3c9', { side: THREE.DoubleSide }),
    noteC: std('#a6ecff', { side: THREE.DoubleSide }),
    pole: std('#9aa3b2', { metalness: 0.6, roughness: 0.3 }),
    pot: phys('#d27c55', { roughness: 0.75 }),
    soil: std('#4a3326', { roughness: 1 }),
    leaf: phys('#3f8f55', { roughness: 0.55, clearcoat: 0.3, side: THREE.DoubleSide }),
    leafLight: phys('#69b86d', { roughness: 0.55, clearcoat: 0.3, side: THREE.DoubleSide }),
    mug: phys('#fbfaf7', { roughness: 0.25, clearcoat: 0.8 }),
    contact: new THREE.MeshBasicMaterial({ map: contactTexture(), transparent: true, depthWrite: false, toneMapped: false, polygonOffset: true, polygonOffsetFactor: -2 }),
    hit: new THREE.MeshBasicMaterial({ visible: false }),
  };
}

// contactTexture is a soft dark blot, the shadow where things meet the floor.
function contactTexture(): THREE.CanvasTexture {
  const { tex, ctx } = canvasTexture(128, 128);
  const g = ctx.createRadialGradient(64, 64, 4, 64, 64, 62);
  g.addColorStop(0, 'rgba(20,14,8,0.55)');
  g.addColorStop(0.55, 'rgba(20,14,8,0.28)');
  g.addColorStop(1, 'rgba(20,14,8,0)');
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, 128, 128);
  return tex;
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
  roundSoft: new RoundedBoxGeometry(1, 1, 1, 4, 0.3),
  roundThin: new RoundedBoxGeometry(1, 1, 1, 2, 0.02),
  circle: new THREE.CircleGeometry(1, 24),
  mug: (() => {
    const g = new THREE.LatheGeometry([new THREE.Vector2(0, 0), new THREE.Vector2(0.038, 0), new THREE.Vector2(0.042, 0.01), new THREE.Vector2(0.043, 0.1), new THREE.Vector2(0.036, 0.1), new THREE.Vector2(0.036, 0.02), new THREE.Vector2(0, 0.02)], 24);
    return g;
  })(),
  handle: new THREE.TorusGeometry(0.026, 0.007, 8, 16, Math.PI),
  pot: new THREE.LatheGeometry([new THREE.Vector2(0, 0), new THREE.Vector2(0.15, 0), new THREE.Vector2(0.19, 0.36), new THREE.Vector2(0.215, 0.37), new THREE.Vector2(0.215, 0.42), new THREE.Vector2(0.19, 0.42), new THREE.Vector2(0.18, 0.38), new THREE.Vector2(0, 0.38)], 28),
  leaf: (() => {
    // A leaf: a pointed blade, a little cupped.
    const shape = new THREE.Shape();
    shape.moveTo(0, 0);
    shape.quadraticCurveTo(0.09, 0.18, 0, 0.42);
    shape.quadraticCurveTo(-0.09, 0.18, 0, 0);
    const g = new THREE.ShapeGeometry(shape, 8);
    const p = g.attributes.position;
    for (let i = 0; i < p.count; i++) p.setZ(i, Math.abs(p.getX(i)) * 0.6);
    g.computeVertexNormals();
    return g;
  })(),
};

function mesh(g: THREE.BufferGeometry, m: THREE.Material, x: number, y: number, z: number, sx = 1, sy = 1, sz = 1, shadow = true): THREE.Mesh {
  const o = new THREE.Mesh(g, m);
  o.position.set(x, y, z);
  o.scale.set(sx, sy, sz);
  o.castShadow = shadow;
  o.receiveShadow = true;
  return o;
}

// plant is a potted plant: a terracotta pot with a rim, soil, and a rosette of
// curved leaves in two greens, merged into a few meshes.
function plant(m: Mats, x: number, z: number, seed: number): THREE.Group {
  const g = new THREE.Group();
  g.position.set(x, 0, z);
  // Kenney's potted plant, when the kit's there, a little bigger or smaller.
  const k = piece('pottedPlant');
  if (k) {
    k.scale.multiplyScalar(0.9 + (seed % 3) * 0.14);
    k.rotation.y = seed * 1.3;
    k.traverse((o) => {
      if ((o as THREE.Mesh).isMesh) o.castShadow = true;
    });
    g.add(k);
    return g;
  }
  const parts: THREE.Mesh[] = [];
  const put = (o: THREE.Mesh) => (g.add(o), parts.push(o), o);
  put(mesh(geo.pot, m.pot, 0, 0, 0));
  put(mesh(geo.cyl, m.soil, 0, 0.375, 0, 0.18, 0.01, 0.18));
  const count = 9 + (seed % 4);
  for (let i = 0; i < count; i++) {
    const a = (i / count) * Math.PI * 2 + seed * 0.7;
    const tilt = 0.35 + (i % 3) * 0.22;
    const leaf = put(mesh(geo.leaf, i % 2 ? m.leaf : m.leafLight, Math.sin(a) * 0.04, 0.38, Math.cos(a) * 0.04, 1.1 + (i % 4) * 0.12, 1.1 + (i % 3) * 0.25, 1));
    leaf.rotation.set(0, a, 0, 'YXZ');
    leaf.rotateX(tilt);
  }
  const blot = mesh(geo.plane, m.contact, 0, 0.02, 0, 0.75, 0.75, 1, false);
  blot.rotation.x = -Math.PI / 2;
  g.add(blot);
  bake(g, parts);
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
  // The room it's in.
  room: number;
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
  working: ['⋯', '#60a5fa'],
  error: ['×', '#f87171'],
  finished: ['✓', '#34d399'],
  stopped: ['z', '#94a3b8'],
};

export function createOfficeScene(host0: HTMLElement, opts0: OfficeSceneOptions): OfficeScene {
  // The page it's in, and what it tells, both replaced as the Office is left
  // and come back to (attach): the room itself stays built.
  let host = host0;
  const opts: OfficeSceneOptions = { ...opts0 };
  let attached = true;
  let motion = !opts.reducedMotion;
  let disposed = false;
  // Transparent: the pale sky behind the diorama is the page's own.
  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'high-performance' });
  renderer.setClearColor(0x000000, 0);
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = THREE.PCFShadowMap;
  // AgX keeps highlights and saturated colours soft; output is sRGB.
  // Neutral keeps the pastels true; output is sRGB.
  renderer.toneMapping = THREE.NeutralToneMapping;
  renderer.toneMappingExposure = 1.0;
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
  const markers = new Map<string, HTMLDivElement>();
  // Each area's label from far off, a map's: a click walks to its front desk.
  const areaChips = new Map<string, HTMLButtonElement>();
  // Each room's pill: its project's name (and floor), its waiting count.
  function syncAreaChips(input: SceneInput) {
    const keys = new Set(lay.rooms.map((r) => r.key));
    for (const [k, chip] of areaChips)
      if (!keys.has(k)) {
        chip.remove();
        areaChips.delete(k);
      }
    for (const [ri, rm] of lay.rooms.entries()) {
      const label = input.areaLabels.get(rm.project);
      const area = input.model.areas.find((a) => a.project === rm.project);
      if (!label || !area) continue;
      let chip = areaChips.get(rm.key);
      if (!chip) {
        chip = document.createElement('button');
        chip.type = 'button';
        chip.dataset.officeArea = rm.key;
        chip.style.cssText =
          'position:absolute;left:0;top:0;display:none;pointer-events:auto;align-items:center;gap:6px;padding:5px 14px;border-radius:999px;color:#fff;font:700 13px/1.2 var(--font-sans,system-ui),sans-serif;box-shadow:0 3px 10px rgba(40,30,20,.22);cursor:pointer;white-space:nowrap;will-change:transform';
        const name = document.createElement('span');
        const sub = document.createElement('span');
        sub.style.cssText = 'font-weight:700;font-size:10.5px;padding:0 6px;border-radius:999px;background:rgba(255,255,255,.28)';
        const bad = document.createElement('span');
        bad.style.cssText = 'display:none;font-weight:700;font-size:10.5px;padding:0 6px;border-radius:999px;background:#fff;color:#dc2626';
        const work = document.createElement('span');
        work.style.cssText = 'display:none;font-weight:700;font-size:10.5px;padding:0 6px;border-radius:999px;background:#fff;color:#2563eb';
        chip.append(name, sub, bad, work);
        const lead = rm.lead ? `${rm.project}/lead` : undefined;
        // Over the campus it goes into its room; in the room, to its lead.
        press(chip, () => {
          if (scope === null || !lead) enter(ri);
          else walkTo(lead);
        });
        markerLayer.prepend(chip);
        areaChips.set(rm.key, chip);
      }
      chip.style.background = `hsl(${area.hue} 58% 48%)`;
      const name = rm.floors > 1 ? opts.floorLabel?.(label.name, rm.floor) ?? `${label.name} ${rm.floor}` : label.name;
      (chip.firstChild as HTMLElement).textContent = name;
      chip.title = label.sub;
      chip.setAttribute('aria-label', opts.markerLabel(name));
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
  const camera = new THREE.PerspectiveCamera(FOV, 1, 0.1, 900);
  const hemi = new THREE.HemisphereLight('#e0ecff', '#8a6a4c', 1.0);
  scene.add(hemi);
  const sun = new THREE.DirectionalLight('#fff3e0', 1.8);
  sun.castShadow = true;
  sun.shadow.mapSize.set(2048, 2048);
  sun.shadow.bias = -0.0005;
  // Soft-edged shadows, as daylight through a big window gives.
  sun.shadow.radius = 8;
  sun.shadow.normalBias = 0.02;
  scene.add(sun, sun.target);
  const fill = new THREE.AmbientLight('#ffffff', 0.25);
  scene.add(fill);
  // Image-based light from a soft studio room: what gives the plastics,
  // ceramics and metals their sheen. Made once, cheaply.
  const pmrem = new THREE.PMREMGenerator(renderer);
  const envRoom = new RoomEnvironment();
  const env = pmrem.fromScene(envRoom, 0.04);
  envRoom.dispose();
  scene.environment = env.texture;
  scene.environmentIntensity = 0.42;
  // A cool rim from behind, against the warm sun: shapes read against the floor.
  const rim = new THREE.DirectionalLight('#c7dcff', 0.6);
  rim.position.set(0, 9, -12);
  scene.add(rim);

  const mats = sharedMaterials();

  // Soft ambient occlusion: the darkening where things meet, under the
  // chairs, in the room's corners, between the desks, which is most of what
  // makes the room read as a toy diorama. A pass over the frames that are
  // drawn anyway, so idle costs what it did. Multisampled, as the canvas was.
  const composer = new EffectComposer(renderer, new THREE.WebGLRenderTarget(1, 1, { type: THREE.HalfFloatType, samples: 4 }));
  composer.addPass(new RenderPass(scene, camera));
  // What's see-through (labels, bubbles, rings, glass, contact shadows) is
  // on a layer of its own that the occlusion pass's camera doesn't see: it
  // would take each for a solid quad and darken a square round it.
  camera.layers.enable(SEE);
  const aoCam = new THREE.PerspectiveCamera();
  const ao = new GTAOPass(scene, aoCam, 1, 1);
  ao.updateGtaoMaterial({ radius: 0.8, distanceExponent: 1.6, thickness: 1.4, scale: 1.2, samples: 12 });
  ao.updatePdMaterial({ lumaPhi: 10, depthPhi: 2, normalPhi: 3, radius: 6, rings: 2, samples: 12 });
  ao.blendIntensity = 1;
  composer.addPass(ao);
  composer.addPass(new OutputPass());

  // What a layout builds: the room, its furniture and the desks.
  let room = new THREE.Group();
  scene.add(room);
  let roomDispose: (() => void)[] = [];
  let lay: Campus = layOut({ areas: [] });
  // The room you're in, and what the overview frames: a room, or the whole
  // campus (null). The Office opens on the campus.
  let cur = 0;
  let scope: number | null = null;
  const roomBounds = (i: number): Box => {
    const b = lay.rooms[i].box;
    return { x0: b.x0 + RADIUS, z0: b.z0 + RADIUS, x1: b.x1 - RADIUS, z1: b.z1 - RADIUS };
  };
  let layKey = '';
  let boxes: Box[] = [];
  let bounds: Box = lay.room;
  const desks = new Map<string, DeskObj>();
  const hits: THREE.Object3D[] = [];
  let plantObjs: THREE.Group[] = [];
  const signs: THREE.Group[] = [];
  // Each room's board, by its ref.
  const boards = new Map<string, { ctx: CanvasRenderingContext2D; tex: THREE.CanvasTexture; key: string; face?: THREE.Mesh; rows: BoardRow[]; room: number; titles?: Map<string, string> }>();
  // The rows of the board you're at, as buttons laid over them on screen: a
  // click on a note is exactly that note, and the keys reach them too.
  const rowBtns: HTMLButtonElement[] = [];
  const rowBtn = (i: number) => {
    let b = rowBtns[i];
    if (!b) {
      b = document.createElement('button');
      b.type = 'button';
      b.dataset.officeBoardRow = '';
      b.style.cssText = 'position:absolute;left:0;top:0;display:none;padding:0;border:0;border-radius:8px;background:transparent;cursor:pointer;pointer-events:auto';
      const el = b;
      press(el, () => el.dataset.ref && opts.onBoardRow(el.dataset.ref));
      el.addEventListener('pointerenter', () => (el.style.background = 'rgba(124,58,237,.08)'));
      el.addEventListener('pointerleave', () => (el.style.background = 'transparent'));
      markerLayer.appendChild(el);
      rowBtns[i] = el;
    }
    return b;
  };
  let sideWalls: { mat: THREE.MeshStandardMaterial; side: 'left' | 'right' }[] = [];

  // You.
  const me = player(makeWalker(), () => poke());
  scene.add(me.group);
  const meRing = mesh(geo.ring, new THREE.MeshBasicMaterial({ color: '#a78bfa', transparent: true, opacity: 0.8, side: THREE.DoubleSide }), 0, 0.02, 0, 0.42, 0.42, 0.42, false);
  meRing.rotation.x = -Math.PI / 2;
  scene.add(meRing);
  // Over the agent whose card is up: a bobbing white arrow, so which of a
  // pod's two it's about is never a guess.
  const atMark = sprite(96, 96, 0.42);
  {
    const c = atMark.ctx;
    c.clearRect(0, 0, 96, 96);
    c.fillStyle = '#ffffff';
    c.strokeStyle = '#7c3aed';
    c.lineWidth = 8;
    c.lineJoin = 'round';
    c.beginPath();
    c.moveTo(18, 26);
    c.lineTo(78, 26);
    c.lineTo(48, 74);
    c.closePath();
    c.stroke();
    c.fill();
    atMark.tex.needsUpdate = true;
  }
  atMark.sprite.visible = false;
  atMark.sprite.material.depthTest = false;
  atMark.sprite.renderOrder = 7;
  scene.add(atMark.sprite);
  const meTag = sprite(256, 80, 0.62);
  meTag.sprite.position.y = 1.55;
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
  const cam = { yaw: HOME_YAW, followK: 0, dist: 13, closeK: 0 };
  // The wheel's one scale, 0 (the overview) to 1 (close behind you): the
  // first half comes down from the overview, the second half closer.
  let zoomZ = 0;
  const FOLLOW_FAR = 23;
  const FOLLOW_NEAR = 10.5;
  const zoomTo = (z: number) => {
    zoomZ = Math.max(0, Math.min(1, z));
    followTarget = Math.min(1, zoomZ * 2);
    cam.dist = zoomZ <= 0.5 ? FOLLOW_FAR : FOLLOW_FAR - (zoomZ - 0.5) * 2 * (FOLLOW_FAR - FOLLOW_NEAR);
    tellView();
  };
  const eased = { tx: 0, ty: 0, tz: 0, dist: 20, pitch: 0.9, yaw: 0, init: false };
  let near: string | undefined;
  // What the close-up looks at, kept while it eases out.
  let focusRef: string | undefined;
  const figPos = new THREE.Vector3();
  // Where the wheel zoomed in, until you walk.
  let zoomFocus: P | undefined;
  let settledFor = 0;
  let chipsShown = true;
  let lastRestack = '';
  // Each chip's offset from its desk, or null when it has no room.
  const chipDy = new WeakMap<HTMLElement, [number, number] | null>();
  const chipInto = new WeakMap<HTMLElement, HTMLDivElement>();
  let stillFor = 0;

  // Input.
  const keys = new Set<string>();
  let hover: string | undefined;
  let pointer: { x: number; y: number; id: number; dragged: boolean; button: number; aim: { ref?: string; row?: string; floor?: P; bubble?: boolean }; cam: string } | undefined;
  // Where the camera is, to tell whether it moved between a press and its release.
  const camAt = () => `${eased.tx.toFixed(2)}:${eased.tz.toFixed(2)}:${eased.dist.toFixed(2)}:${eased.pitch.toFixed(3)}:${eased.yaw.toFixed(3)}`;
  const ndc = new THREE.Vector2();
  let hoverDirty = false;
  const ray = new THREE.Raycaster();
  ray.layers.enable(SEE);
  const floorPlane = new THREE.Plane(new THREE.Vector3(0, 1, 0), 0);

  let lastInput = performance.now();
  let dirty = true;
  const poke = () => {
    lastInput = performance.now();
    dirty = true;
    wake();
  };

  function build(input: SceneInput) {
    variants = new Map();
    perRoom.clear();
    variantTool.clear();
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
    // What a room has is tagged with it, so a room's own view shows it alone.
    const addTo = (ri: number, o: THREE.Object3D) => {
      o.userData.room = ri;
      room.add(o);
    };
    const l = lay;
    boards.clear();
    sideWalls = [];

    // Each room: a diorama slab with its walls and features (decor.ts), moved
    // to its place on the campus, its project's name on its brick wall; and
    // its board on the back wall.
    for (const [ri, rm] of l.rooms.entries()) {
      const am = input.model.areas.find((a) => a.project === rm.project);
      const label = am?.label ?? 'AgentBox';
      const dio = diorama(rm.local, rm.floors > 1 ? `${label} ${rm.floor}` : label, am?.hue);
      roomDispose.push(() => dio.dispose());
      dio.group.position.set(rm.ox, 0, rm.oz);
      addTo(ri, dio.group);
      const bw = rm.board.w;
      const bh = Math.min(2.6, bw * 0.52);
      const pxW = 1024;
      const pxH = Math.round((pxW * bh) / bw);
      const { tex: btex, ctx } = canvasTexture(pxW, pxH);
      own(btex);
      const b = { ctx, tex: btex, key: '', rows: [] as BoardRow[], room: ri, face: undefined as THREE.Mesh | undefined };
      const g = new THREE.Group();
      g.position.set(rm.board.x, 0.45 + bh / 2, rm.oz + 0.04);
      // A whiteboard in a dark green frame, its columns of sticky notes.
      g.add(mesh(geo.roundTop, own(std('#2f4a3a', { roughness: 0.5 })), 0, 0, 0, bw + 0.16, bh + 0.16, 0.06, false));
      const face = mesh(geo.plane, own(new THREE.MeshBasicMaterial({ map: btex, toneMapped: false })), 0, 0, 0.045, bw, bh, 1, false);
      g.add(face);
      b.face = face;
      addTo(ri, g);
      // A ledge below it, with markers.
      addTo(ri, mesh(geo.roundTop, own(std('#2f4a3a')), rm.board.x, 0.42, rm.oz + 0.1, bw * 0.6, 0.04, 0.14));
      const hit = mesh(geo.box, mats.hit, rm.board.x, 1.5, rm.oz + 0.3, bw, 3, 0.6, false);
      hit.userData.ref = boardRef(rm);
      addTo(ri, hit);
      hits.push(hit);
      boards.set(boardRef(rm), b);
    }

    // A project on floors: a plinth under them all, so the campus reads them
    // as one place. (Over the campus only.)
    {
      const groups = new Map<string, Box>();
      for (const rm of l.rooms) {
        if (rm.floors < 2) continue;
        const b = groups.get(rm.project);
        groups.set(rm.project, b ? { x0: Math.min(b.x0, rm.box.x0), z0: Math.min(b.z0, rm.box.z0), x1: Math.max(b.x1, rm.box.x1), z1: Math.max(b.z1, rm.box.z1) } : rm.box);
      }
      for (const [project, b] of groups) {
        const hue = input.model.areas.find((a) => a.project === project)?.hue ?? 260;
        const plinth = mesh(geo.roundTop, own(std(new THREE.Color().setHSL(hue / 360, 0.35, 0.8), { roughness: 0.9 })), (b.x0 + b.x1) / 2, -1.0, (b.z0 + b.z1) / 2, b.x1 - b.x0 + 3.2, 0.2, b.z1 - b.z0 + 3.2);
        addTo(-1, plinth);
      }
    }

    // Each area: its rug, its counter with the lead and the project's sign.
    for (let i = 0; i < l.areas.length; i++) {
      const place = l.areas[i];
      const area = input.model.areas.find((a) => a.project === place.project)!;
      const r = place.rug;
      // Each pod's soft pastel rug, in the project's colour, corners rounded.
      for (const [pi, pod] of place.pods.entries()) {
        // Each pod a shade of its project's colour, so pods read apart.
        // Each pod its own pastel, near its project's colour: pods read apart.
        const shade = [0, 30, -30, 15, -15][pi % 5];
        const hh = ((area.hue + shade + 360) % 360) / 360;
        // Saturated, as a toy's rugs: the pods read as coloured islands.
        const rugMat = own(std(new THREE.Color().setHSL(hh, 0.72, pi % 2 ? 0.55 : 0.6), { roughness: 0.95 }));
        const rugEdge = own(std(new THREE.Color().setHSL(hh, 0.6, 0.45), { roughness: 0.95 }));
        const pw = pod.x1 - pod.x0;
        const pd = pod.z1 - pod.z0;
        addTo(place.room, mesh(own(rug(pw, pd, 0.5, 0.02)), rugEdge, (pod.x0 + pod.x1) / 2, 0, (pod.z0 + pod.z1) / 2, 1, 1, 1, false));
        addTo(place.room, mesh(own(rug(pw - 0.16, pd - 0.16, 0.42, 0.024)), rugMat, (pod.x0 + pod.x1) / 2, 0, (pod.z0 + pod.z1) / 2, 1, 1, 1, false));
      }
      void r;

      // The lead's counter, on its project's first floor: a rounded wooden
      // front desk, its front in the project's colour.
      for (const d of place.desks) addDesk(input, d.id, d.x, d.z, area.hue, false, d.side);
      if (!place.lead) continue;
      const { x: lx, z: lz } = place.lead;
      const counterFront = own(std(new THREE.Color().setHSL(area.hue / 360, 0.5, 0.62), { roughness: 0.6 }));
      addTo(place.room, mesh(geo.roundTop, mats.oak, lx, 1.0, lz, COUNTER.w + 0.1, 0.07, COUNTER.d + 0.1));
      addTo(place.room, mesh(geo.roundSoft, counterFront, lx, 0.5, lz + 0.05, COUNTER.w, 0.96, COUNTER.d - 0.1));
      // Its front says whose it is: the project's lead, its front desk.
      {
        const plate = canvasTexture(512, 160);
        const c = plate.ctx;
        c.clearRect(0, 0, 512, 160);
        c.fillStyle = '#ffffff';
        c.textAlign = 'center';
        c.font = '700 26px "Inter Variable", Inter, system-ui, sans-serif';
        c.globalAlpha = 0.85;
        c.fillText((opts.leadWord ?? 'Lead').toUpperCase(), 256, 46);
        c.globalAlpha = 1;
        c.font = '800 64px "Inter Variable", Inter, system-ui, sans-serif';
        let label = area.label;
        while (c.measureText(label).width > 480 && label.length > 3) label = `${label.slice(0, -2)}…`;
        c.fillText(label, 256, 118);
        plate.tex.needsUpdate = true;
        own(plate.tex);
        const pm = own(new THREE.MeshBasicMaterial({ map: plate.tex, transparent: true, depthWrite: false }));
        addTo(place.room, mesh(geo.plane, pm, lx, 0.52, lz + 0.05 + (COUNTER.d - 0.1) / 2 + 0.006, COUNTER.w * 0.86, COUNTER.w * 0.86 * (160 / 512), 1, false));
      }
      addDesk(input, `${area.project}/lead`, lx, lz, area.hue, true);
    }
    plantObjs = l.plants.map((p, i) => {
      const o = plant(mats, p.x, p.z, i);
      addTo(roomAt(lay, p, 0.5) ?? 0, o);
      return o;
    });

    lighting(true);
    showScope();
    seeThrough(scene);
    boxes = walkable(l, RADIUS);
    bounds = roomBounds(cur);
  }

  // addDesk puts an agent's desk at (x, z): the desk, its chair, the laptop
  // and the monitor facing you, the robot, its beacon, bubble, name tag and
  // pull request flag. A lead's is the counter, built above: here it gets
  // the robot and the rest.
  // variantOf is an agent's place among the agents of its tool, in the
  // room's order: what picks its accent, so neighbours don't match.
  let variants = new Map<string, number>();
  // Numbered within its room, so a room's first few are all different.
  const perRoom = new Map<number, number>();
  function variantOf(ref: string, ai: string | undefined): number {
    if (!variants.has(ref)) {
      const ri = roomOf(lay, ref) ?? 0;
      const n = perRoom.get(ri) ?? 0;
      perRoom.set(ri, n + 1);
      // Each room starts somewhere else in the looks: no two rooms' first
      // agents alike.
      variants.set(ref, n + ri * 3);
      variantTool.set(ref, ai || 'claude');
    }
    return variants.get(ref)!;
  }
  const variantTool = new Map<string, string>();

  function addDesk(input: SceneInput, ref: string, x: number, z: number, hue: number, lead: boolean, side: -1 | 1 = -1) {
    const disp: (() => void)[] = [];
    const g = new THREE.Group();
    g.position.set(x, 0, z);
    const ri = roomOf(lay, ref) ?? 0;
    g.userData.room = ri;
    room.add(g);
    const n = hash(ref);
    const rx = lead ? 0 : side * 0.48; // where the agent sits, and its chair, at the bench's end
    const out = -side; // mirrors what's laid out for a left-hand desk
    const cz = lead ? -0.75 : -SEAT;
    const top = lead ? 1.03 : DESK.h + 0.025;
    // What never moves is merged into a few meshes for the desk (bake).
    const fixed: THREE.Mesh[] = [];
    const put = (parent: THREE.Object3D, o: THREE.Mesh) => (parent.add(o), fixed.push(o), o);
    if (!lead) {
      // An oak top on two metal side frames, with a modesty panel.
      put(g, mesh(geo.roundTop, mats.oak, 0, DESK.h, 0, DESK.w, 0.05, DESK.d));
      for (const sx of [-1, 1]) {
        put(g, mesh(geo.roundTop, mats.metal, sx * 0.66, DESK.h / 2, 0, 0.05, DESK.h - 0.02, 0.08));
        put(g, mesh(geo.roundTop, mats.metal, sx * 0.66, 0.02, 0, 0.07, 0.04, DESK.d - 0.04));
        put(g, mesh(geo.roundTop, mats.metal, sx * 0.66, DESK.h - 0.04, 0, 0.06, 0.03, DESK.d - 0.06));
      }
      put(g, mesh(geo.roundTop, mats.metal, 0, 0.48, 0.3, 1.28, 0.32, 0.02));
      // A mug, a pen cup, a sticky note on the monitor: some of each, by desk.
      if (n % 3 !== 0) {
        const mug = put(g, mesh(geo.mug, mats.ceramic, 0.02 * out, DESK.h + 0.025, 0.25, 1, 1, 1));
        mug.rotation.y = (n % 7) * 0.8;
        const handle = put(g, mesh(geo.handle, mats.ceramic, (0.02 + 0.045) * out, DESK.h + 0.075, 0.25, 1, 1, 1));
        handle.rotation.y = Math.PI / 2;
      }
      if (n % 2 === 0) {
        put(g, mesh(geo.cyl, mats.metal, (-0.72 + 0.1) * out, DESK.h + 0.07, -0.25, 0.035, 0.1, 0.035));
        for (const [dx, c] of [[-0.012, mats.penA], [0.012, mats.penB]] as const) {
          const pen = put(g, mesh(geo.cyl, c, (-0.62 + dx) * out, DESK.h + 0.14, -0.25, 0.006, 0.14, 0.006));
          pen.rotation.z = dx * 8;
        }
      }
    }
    // A little plant where a bench's two desks meet (Kenney's, when it's there).
    if (!lead && side > 0) {
      const sprig = piece((['plantSmall1', 'plantSmall2', 'plantSmall3'] as const)[n % 3]);
      if (sprig) {
        sprig.position.set(-0.72, DESK.h + 0.025, -0.26);
        sprig.scale.multiplyScalar(1.3);
        g.add(sprig);
      }
    }
    // An office chair under the agent, in one of the room's pastels.
    const seatMat = mats.chairs[n % mats.chairs.length];
    put(g, mesh(geo.roundSoft, seatMat, rx, 0.45, cz, 0.5, 0.08, 0.48));
    const back = put(g, mesh(geo.roundSoft, seatMat, rx, 0.8, cz - 0.25, 0.48, 0.5, 0.07));
    back.rotation.x = 0.12;
    put(g, mesh(geo.roundTop, mats.metal, rx, 0.6, cz - 0.27, 0.05, 0.22, 0.03));
    put(g, mesh(geo.cyl, mats.metal, rx, 0.26, cz, 0.028, 0.34, 0.028));
    for (let k = 0; k < 5; k++) {
      const a = (k / 5) * Math.PI * 2 + 0.3;
      const spoke = put(g, mesh(geo.roundTop, mats.plastic, rx + Math.sin(a) * 0.13, 0.07, cz + Math.cos(a) * 0.13, 0.05, 0.04, 0.27));
      spoke.rotation.y = a;
      put(g, mesh(geo.sphere, mats.plastic, rx + Math.sin(a) * 0.26, 0.03, cz + Math.cos(a) * 0.26, 0.03, 0.03, 0.03));
    }

    // The laptop before the robot, its lid's back glowing in its state's colour.
    const glowMat = new THREE.MeshStandardMaterial({ color: '#60a5fa', emissive: '#60a5fa', emissiveIntensity: 0.6 });
    disp.push(() => glowMat.dispose());
    const lx = lead ? 0.4 : rx;
    put(g, mesh(geo.roundThin, mats.alu, lx, top + 0.012, -0.16, 0.4, 0.022, 0.27));
    put(g, mesh(geo.plane, mats.keys, lx, top + 0.024, -0.13, 0.34, 0.13, 1, false)).rotation.x = -Math.PI / 2;
    const lid = new THREE.Group();
    lid.position.set(lx, top + 0.022, -0.035);
    // Open wide and low, so the agent's face shows over it.
    lid.rotation.x = -0.75;
    g.add(lid);
    put(lid, mesh(geo.roundThin, mats.alu, 0, 0.1, 0, 0.4, 0.2, 0.012));
    lid.add(mesh(geo.circle, glowMat, 0, 0.1, 0.0075, 0.03, 0.03, 1, false));

    // The monitor across the desk from the agent, towards the bench's middle,
    // turned to it; its back, to the room, lit in its state's colour.
    const { tex, ctx } = canvasTexture(512, 320);
    disp.push(() => tex.dispose());
    const screenMat = new THREE.MeshBasicMaterial({ map: tex, toneMapped: false });
    disp.push(() => screenMat.dispose());
    const mon = new THREE.Group();
    // A lead's to its left, clear of its face from the camera's side.
    mon.position.set(lead ? -0.62 : rx - side * 0.55, top, lead ? 0.05 : 0.2);
    mon.rotation.y = lead ? 2.45 : side * (Math.PI - 0.59);
    g.add(mon);
    const mw = lead ? 0.86 : 0.76;
    const mh = mw * (320 / 512);
    const my = 0.07 + mh / 2 + 0.06;
    put(mon, mesh(geo.roundThin, mats.bezel, 0, my, 0, mw + 0.05, mh + 0.05, 0.035));
    put(mon, mesh(geo.roundThin, mats.bezel, 0, my, -0.03, mw * 0.6, mh * 0.6, 0.04));
    put(mon, mesh(geo.roundTop, mats.metal, 0, 0.09, -0.04, 0.05, 0.16, 0.03));
    put(mon, mesh(geo.cyl, mats.metal, 0, 0.006, -0.03, 0.13, 0.012, 0.09));
    mon.add(mesh(geo.plane, screenMat, 0, my, 0.0185, mw, mh, 1, false));
    const backLight = mesh(geo.circle, glowMat, 0, my + 0.02, -0.052, 0.06, 0.06, 1, false);
    backLight.rotation.y = Math.PI;
    mon.add(backLight);
    if (!lead && n % 4 === 1) {
      const note = put(mon, mesh(geo.plane, [mats.noteA, mats.noteB, mats.noteC][n % 3], mw / 2 - 0.02, my + mh / 2 - 0.02, 0.019, 0.08, 0.08, 1, false));
      note.rotation.z = 0.12;
    }
    // A soft contact shadow under it all.
    const blot = mesh(geo.plane, mats.contact, lead ? 0 : side * 0.1, 0.02, lead ? -0.3 : -0.2, lead ? 3.0 : 2.1, lead ? 2.2 : 1.9, 1, false);
    blot.rotation.x = -Math.PI / 2;
    g.add(blot);
    for (const merged of bake(g, fixed)) merged.castShadow = merged.material !== mats.keys;

    // The agent, its tool's mascot, on its chair.
    const ai = lead ? 'claude' : input.model.areas.find((a) => a.project === ref.split('/')[0])?.desks.find((d) => d.id === ref)?.ai;
    const fig = mascot(ref, ai, { lead, variant: lead ? undefined : variantOf(ref, ai) });
    void hue;
    disp.push(() => fig.dispose());
    // Scaled up, so seated on the seat from below.
    fig.group.position.set(rx, lead ? -0.36 : -0.3, cz + 0.1);
    g.add(fig.group);

    // Its beacon and bubble above it, its name tag, and a ring and a beam of
    // light when it needs you.
    const beaconMat = new THREE.MeshStandardMaterial({ color: '#60a5fa', emissive: '#60a5fa', emissiveIntensity: 1.4 });
    disp.push(() => beaconMat.dispose());
    const beacon = mesh(geo.octa, beaconMat, fig.group.position.x, 1.95, cz, 1, 1, 1, false);
    g.add(beacon);
    const bubble = sprite(128, 150, 0.4);
    disp.push(() => bubble.tex.dispose(), () => bubble.sprite.material.dispose());
    bubble.sprite.position.set(fig.group.position.x, 2.05, cz + 0.05);
    // Over everything, glass included, and a click on it is a click on its desk.
    bubble.sprite.material.depthTest = false;
    bubble.sprite.renderOrder = 6;
    bubble.sprite.userData.ref = ref;
    hits.push(bubble.sprite);
    g.add(bubble.sprite);
    const tag = sprite(640, 96, 1.9);
    disp.push(() => tag.tex.dispose(), () => tag.sprite.material.dispose());
    // At the desk's front edge, under the agent's face rather than over it.
    tag.sprite.position.set(rx, 0.45, cz);
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
    flag.position.set(lead ? 1.05 : out * 0.6, top, -0.28);
    // Big enough to read from the room's view: its pull request's state.
    flag.add(mesh(geo.cyl, mats.pole, 0, 0.45, 0, 0.014, 0.9, 0.014));
    const cloth = mesh(geo.plane, flagMat, 0.2, 0.75, 0, 0.4, 0.26, 1);
    flag.add(cloth);
    g.add(flag);

    // What a click picks: the desk and the robot, as one box.
    // Tight round the desk and its robot, so the floor beside it is floor.
    const hit = mesh(geo.box, mats.hit, lead ? 0 : side * 0.1, 0.7, lead ? -0.3 : -0.25, lead ? COUNTER.w : 1.5, 1.4, lead ? 1.45 : 1.25, false);
    hit.userData.ref = ref;
    g.add(hit);
    hits.push(hit);

    const obj: DeskObj = {
      ref,
      room: ri,
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
    // The antenna's bulb says the state now; the floating beacon is gone.
    d.beacon.visible = false;
    d.glowMat.color.set(color);
    d.glowMat.emissive.set(color);
    d.glowMat.emissiveIntensity = tone === 'stopped' ? 0 : 0.8;
    const loud = tone === 'needs';
    d.ring.visible = loud || tone === 'error';
    (d.ring.material as THREE.MeshBasicMaterial).color.set(color);
    d.beam.visible = false;

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
    // Every desk has a label over the canvas, crisp at any distance; the ones
    // that need you are loud, and the only ones shown from afar.
    const loudNow = tone === 'needs' || tone === 'error';
    let marker = markers.get(d.ref);
    {
      if (!marker) {
        // A chip: a button that walks you there, and beside it a "+n" button
        // for the chips folded into it, which opens the list of everyone.
        marker = document.createElement('div');
        marker.dataset.officeMarker = d.ref;
        marker.style.cssText =
          'position:absolute;left:0;top:0;display:flex;align-items:center;gap:3px;pointer-events:none;will-change:transform';
        const walk = document.createElement('button');
        walk.type = 'button';
        walk.style.cssText =
          'position:relative;pointer-events:auto;display:flex;align-items:center;gap:5px;max-width:var(--tag-w,170px);padding:2px 8px 2px 6px;border-radius:7px;background:#fff;color:#2b2620;font:600 11px/1.3 var(--font-sans,system-ui),sans-serif;white-space:nowrap;box-shadow:0 2px 6px rgba(40,30,20,.18);cursor:pointer';
        const dot = document.createElement('span');
        dot.style.cssText = 'flex:none;width:8px;height:8px;border-radius:50%';
        const label = document.createElement('span');
        label.style.cssText = 'overflow:hidden;text-overflow:ellipsis';
        // A little tail up to its agent: whose tag it is.
        const tail = document.createElement('span');
        tail.style.cssText = 'position:absolute;left:50%;top:-8px;margin-left:-7px;width:0;height:0;border-left:7px solid transparent;border-right:7px solid transparent;border-bottom:8px solid #fff;pointer-events:none';
        walk.append(dot, label, tail);
        const more = document.createElement('button');
        more.type = 'button';
        more.dataset.officeMore = '';
        more.style.cssText =
          'pointer-events:auto;display:none;padding:3px 9px;border-radius:999px;background:#fbbf24;border:1px solid rgba(0,0,0,.25);color:#1a1405;font:700 11px/1.25 var(--font-sans,system-ui),sans-serif;box-shadow:0 4px 14px rgba(0,0,0,.4);cursor:pointer;white-space:nowrap';
        // The "+n" opens the list of this chip's desk and the ones folded into it.
        const host = marker;
        press(more, () => {
          const inside = [...markers].filter(([, el]) => el !== host && el.style.display === 'none' && chipInto.get(el) === host).map(([r]) => r);
          const box = more.getBoundingClientRect();
          const hostBox = host.getBoundingClientRect();
          opts.onMore([ref, ...inside], { x: box.left - hostBox.left, y: box.bottom - hostBox.top + 6 });
        });
        marker.append(walk, more);
        const ref = d.ref;
        // On press, not click: the camera may still be easing, and a chip that
        // moved before the release would lose the click to the floor.
        press(walk, () => walkTo(ref));
        markerLayer.appendChild(marker);
        markers.set(d.ref, marker);
      }
      const walkBtn = marker.children[0] as HTMLButtonElement;
      (walkBtn.children[0] as HTMLElement).style.background = color;
      walkBtn.style.border = loudNow ? `2px solid ${color}` : '1px solid rgba(0,0,0,.06)';
      (walkBtn.children[1] as HTMLElement).textContent = title;
      walkBtn.setAttribute('aria-label', opts.markerLabel(title));
      marker.dataset.loud = loudNow ? '1' : '';
      walkBtn.style.fontWeight = loudNow ? '700' : '600';
      (marker.children[1] as HTMLElement).setAttribute('aria-label', opts.moreLabel);
    }
    const pr = desk?.pr;
    d.flag.visible = !!pr && pr.state !== 'closed';
    if (pr) d.flagMat.color.set(pr.checks === 'failing' ? '#ef4444' : pr.state === 'merged' ? '#a78bfa' : pr.state === 'draft' ? '#9ca3af' : '#22c55e');
  }

  // lighting follows the clock: daylight through the windows by day, a warm
  // lamp-lit room at night.
  // lighting is one bright, soft day: a warm sky light, a sun from the
  // camera's side for faces and soft shadows, a little bounce.
  // In a room's own view, only it; over the campus, all of them. (Desks and
  // plants are also set each frame, which steps them aside too.)
  const inScope = (o: THREE.Object3D) => scope === null || o.userData.room === undefined || o.userData.room === scope;
  function showScope() {
    for (const c of room.children) c.visible = inScope(c);
  }

  // scopeBox is what the overview frames: the room's floor, or the campus's.
  const homeYaw = () => (scope === null ? CAMPUS_YAW : HOME_YAW);
  const scopeBox = (): Box => (scope === null ? lay.room : lay.rooms[scope].box);
  const scopeWall = () => (scope === null ? Math.max(...lay.rooms.map((r) => wallHeight(r.local))) : wallHeight(lay.rooms[scope].local));

  function lighting(force = false) {
    if (!force) return;
    // The sun from the camera's side, over what's framed; its shadow just
    // covers it, sharp enough wherever you are.
    const b = scopeBox();
    const W = b.x1 - b.x0;
    const D = b.z1 - b.z0;
    const half = Math.max(W, D) / 2 + 3;
    const cx = (b.x0 + b.x1) / 2;
    const cz = (b.z0 + b.z1) / 2;
    const far = 24 + half;
    sun.position.set(cx + 0.55 * far, 0.65 * far, cz + 0.52 * far);
    sun.target.position.set(cx, 0, cz);
    sun.shadow.camera.left = -half;
    sun.shadow.camera.right = half;
    sun.shadow.camera.top = half;
    sun.shadow.camera.bottom = -half;
    sun.shadow.camera.near = 1;
    sun.shadow.camera.far = far + half * 2;
    sun.shadow.camera.updateProjectionMatrix();
    hemi.color.set('#fff6ea');
    hemi.groundColor.set('#e8cfae');
    // Warm and low in contrast: much sky, a gentle sun, light shadows.
    hemi.intensity = 1.7;
    sun.color.set('#ffe9cf');
    sun.intensity = 1.55;
    sun.shadow.intensity = 0.62;
    fill.intensity = 0.2;
    scene.environmentIntensity = 0.3;
    scene.background = null;
  }

  // Kenney's furniture comes a moment later: the room is built again with it.
  let lastApplied: SceneInput | undefined;
  void loadKit().then(() => {
    if (disposed || !lastApplied) return;
    layKey = '';
    apply(lastApplied);
  });

  function apply(input: SceneInput) {
    lastApplied = input;
    const key = input.model.areas.map((a) => `${a.project}:${a.label}:${a.hue}:${a.desks.map((d) => d.id).join(',')}`).join('|');
    if (key !== layKey) {
      layKey = key;
      lay = layOut(input.model);
      cur = Math.min(placed ? (roomAt(lay, pos) ?? cur) : 0, lay.rooms.length - 1);
      if (scope !== null) scope = Math.min(scope, lay.rooms.length - 1);
      build(input);
      if (!placed && lastVisit) {
        Object.assign(pos, lastVisit.pos);
        cur = roomAt(lay, pos) ?? 0;
        scope = lastVisit.scope === null ? null : Math.min(lastVisit.scope, lay.rooms.length - 1);
        bounds = roomBounds(cur);
        lighting(true);
        showScope();
        facing = lastVisit.facing;
        zoomTo(lastVisit.follow / 2);
        cam.followK = lastVisit.follow;
        cam.yaw = lastVisit.yaw;
        cam.dist = lastVisit.dist;
        zoomZ = lastVisit.follow < 1 ? lastVisit.follow / 2 : 0.5 + ((FOLLOW_FAR - lastVisit.dist) / (FOLLOW_FAR - FOLLOW_NEAR)) * 0.5;
        moved = true;
        placed = true;
        const back = path(boxes, bounds, pos, pos);
        if (back.length) Object.assign(pos, back[back.length - 1]);
        stillFor = 1;
        // Back where you were, close up already: no fade in from afar.
        focusRef = nearest(lay, pos);
        // Back from a chat a step out, gliding in to the desk again.
        if (focusRef) cam.closeK = 0;
      } else if (!placed) {
        // In the first room, the camera over the whole campus.
        cur = 0;
        scope = null;
        cam.yaw = CAMPUS_YAW;
        bounds = roomBounds(cur);
        pos.x = lay.spawn.x;
        pos.z = lay.spawn.z;
        facing = lay.spawn.face;
        placed = true;
        lighting(true);
        showScope();
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
    // Each room's board: what of the Office's is in that room.
    for (const b of boards.values()) {
      const rm = lay.rooms[b.room];
      const inRoom = (ref: string) => (ref.endsWith('/lead') ? rm.lead && ref === `${rm.project}/lead` : rm.desks.includes(ref));
      const bs = { ...input.board, needs: { ...input.board.needs, items: input.board.needs.items.filter((i) => inRoom(i.ref)) }, review: { ...input.board.review, items: input.board.review.items.filter((i) => inRoom(i.ref)) } };
      const k = JSON.stringify(bs);
      if (k !== b.key) {
        b.key = k;
        b.rows = drawBoard(b.ctx, bs);
        b.titles = new Map([...bs.needs.items, ...bs.review.items].map((i) => [i.ref, i.title]));
        b.tex.needsUpdate = true;
      }
    }
    poke();
  }

  // into goes into a room: you come in at its door if you were in another,
  // and the camera frames it.
  function into(i: number) {
    if (i !== cur) {
      cur = i;
      const s0 = lay.rooms[i].spawn;
      pos.x = s0.x;
      pos.z = s0.z;
      facing = s0.face;
      route = [];
      bounds = roomBounds(i);
      speed = 0;
    }
    if (scope !== i) {
      scope = i;
      fitCache.key = '';
      cam.yaw = HOME_YAW;
      lighting(true);
      showScope();
    }
  }

  // enter shows a room from its overview, you at its door.
  function enter(i: number) {
    into(i);
    zoomTo(0);
    zoomFocus = undefined;
    turned = false;
    held = true;
    cam.yaw = HOME_YAW;
    tellView();
    poke();
  }

  function walkTo(ref0: string) {
    // 'board' is the board of the room you're in.
    const ref = ref0 === 'board' ? boardRef(lay.rooms[cur]) : ref0;
    const ri = roomOf(lay, ref);
    const spot = spotOf(lay, ref);
    if (!spot || ri === undefined) return;
    // In another room: there already, at it, the camera gliding in.
    const other = ri !== cur;
    into(ri);
    if (other) {
      pos.x = spot.x;
      pos.z = spot.z;
      facing = spot.face;
    }
    route = path(boxes, bounds, pos, spot);
    routeGoal = ref;
    zoomTo(Math.max(zoomZ, 0.5));
    started();
    tellView();
    poke();
  }

  function walkToPoint(p: P) {
    // On another room's floor: into it, from its door.
    const ri = roomAt(lay, p);
    if (ri !== undefined && ri !== cur) into(ri);
    route = path(boxes, bounds, pos, p);
    routeGoal = undefined;
    // From the overview a click on the floor comes halfway down, not all the
    // way behind you.
    if (zoomZ === 0) zoomTo(0.28);
    started();
    poke();
  }

  function started() {
    held = false;
    turned = false;
    // Moving, you're in your room's view, not the campus's.
    if (scope !== cur) {
      scope = cur;
      fitCache.key = '';
      cam.yaw = HOME_YAW;
      lighting(true);
      showScope();
    }
    zoomFocus = undefined;
    if (moved) return;
    moved = true;
    mePulse?.cancel();
    opts.onMoved();
  }

  let followTarget = 0;
  // overview stands back over the whole room, and stays there, close-up and
  // all, until you move again.
  let held = false;
  // Turned away from a close-up by a drag: still at the desk, its card up,
  // the camera stood back, until you walk on.
  let turned = false;
  // overview steps back: to the room's overview, or from there to the campus.
  function overview() {
    if (scope !== null && followTarget === 0 && !near) return campus();
    zoomTo(0);
    zoomFocus = undefined;
    turned = false;
    cam.yaw = HOME_YAW;
    held = true;
    tellView();
    poke();
  }

  // campus stands back over every room.
  function campus() {
    scope = null;
    fitCache.key = '';
    lighting(true);
    showScope();
    zoomTo(0);
    zoomFocus = undefined;
    turned = false;
    cam.yaw = CAMPUS_YAW;
    held = true;
    tellView();
    poke();
  }

  // tellView says which room the camera's in, and whether at its overview.
  let toldView = '';
  function tellView() {
    const rm = scope === null ? undefined : lay.rooms[scope];
    const v = { project: rm?.project, floor: rm?.floor ?? 1, floors: rm?.floors ?? 1, overview: followTarget === 0, refs: rm ? [...rm.desks, ...(rm.lead ? [`${rm.project}/lead`] : [])] : undefined };
    const k = JSON.stringify(v);
    if (k === toldView) return;
    toldView = k;
    opts.onView?.(v);
  }

  // The frame.
  let raf = 0;
  let last = performance.now();
  let clock = 0;
  let running = false;
  let nap = 0;
  function wake() {
    if (document.hidden || disposed || !attached) return;
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
    if (busy && hoverAt && !pointer) refreshHover();
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
      if (zoomZ < 0.5) zoomTo(0.5);
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
      // A walk you asked for by name (a chip, N, the list) is quick.
      // To a desk by name, briskly: its card is what you came for.
      const pace = Math.min(22, Math.max(routeGoal ? 9 : SPEED, left / (routeGoal ? 0.4 : 0.6)));
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
    // Walking to a desk by name, its card is already up on the way.
    const at = held ? undefined : stillFor > 0.15 ? nearest(lay, pos) : routeGoal && route.length ? routeGoal : undefined;
    if (at !== near) {
      near = at;
      opts.onNear(isBoard(near) ? 'board' : near);
      dirty = true;
    }
    return camStep(dt) || moving;
  }

  // focusOf is what the close-up looks at, standing at ref: a desk's monitor,
  // a counter's, or the board, and from how far.
  function focusOf(ref: string, aspect: number): { x: number; y: number; z: number; dist: number; pitch: number; yaw: number } | undefined {
    if (isBoard(ref)) {
      // From high over the first row, so no desk is in the way, the whole
      // board in the part of the view the board's list (on the right) leaves.
      const rm = lay.rooms[roomOf(lay, ref) ?? cur];
      const tan = Math.tan(THREE.MathUtils.degToRad(FOV / 2));
      const bh = Math.min(2.6, rm.board.w * 0.52);
      const free = 1;
      const pitch = 0.4;
      // Wide enough for you, standing at its left end, too.
      const dist = Math.max((rm.board.w + 3.6) / (free * 2 * tan * aspect), ((bh + 0.9) * Math.cos(pitch)) / (0.7 * 2 * tan));
      const vw = 2 * dist * tan * aspect;
      return { x: rm.board.x + (vw * (1 - free)) / 2, y: 0.45 + bh / 2, z: rm.oz + 0.1, dist, pitch, yaw: 0 };
    }
    for (const a of lay.areas) {
      if (a.lead && ref === `${a.project}/lead`) return aside({ x: a.lead.x - 0.4, y: 1.1, z: a.lead.z - 0.2, dist: 12.5, pitch: 0.58, yaw: 0.3 }, aspect);
      const d = a.desks.find((x) => x.id === ref);
      // From the far side from where you stand, so you never stand in the way.
      if (d) return aside({ x: d.x + d.side * 0.35, y: 0.9, z: d.z - 0.3, dist: 19, pitch: 0.8, yaw: d.side < 0 ? 0.5 : 0.08 }, aspect);
    }
    return undefined;
  }

  // aside moves a close-up's centre right, so what it looks at sits in the
  // middle of the part of the view the desk's card (bottom right) leaves.
  const CARD = 336 + 24;
  function aside(f: { x: number; y: number; z: number; dist: number; pitch: number; yaw: number }, aspect: number) {
    const w = Math.max(1, canvas.clientWidth);
    const cover = Math.min(0.5, CARD / w);
    const vw = 2 * f.dist * Math.tan(THREE.MathUtils.degToRad(FOV / 2)) * aspect;
    const shift = (vw * cover) / 2;
    return { ...f, x: f.x + Math.cos(f.yaw) * shift, z: f.z - Math.sin(f.yaw) * shift };
  }

  // fitDistance is how far back the overview stands for the whole room, its
  // floor and the tops of its walls, to be in view: found by trying, since a
  // tilted view of a box has no neat formula. Kept until the room or the
  // window's shape changes.
  const fitProbe = new THREE.PerspectiveCamera(FOV, 1, 0.1, 400);
  let fitCache = { key: '', dist: 20, x: 0, z: 0 };
  let inset = 0;
  function fitDistance(bx: Box, H: number, aspect: number, pitch: number): { dist: number; x: number; z: number } {
    const yaw0 = homeYaw();
    const key = `${scope}:${bx.x0.toFixed(2)}:${bx.z0.toFixed(2)}:${bx.x1.toFixed(2)}:${bx.z1.toFixed(2)}:${H}:${aspect.toFixed(3)}:${inset}:${yaw0}`;
    if (fitCache.key === key) return fitCache;
    // The share of the width the overlay covers: the room sits in the middle
    // of what's left.
    const cover = Math.min(0.45, inset / Math.max(1, canvas.clientWidth));
    // The window the diorama fills, in clip space: nearly all of it, the
    // top bar's and the legend's clear parts over its corners and edges.
    // The campus keeps clear of the top bar too: its back rooms' pills.
    const win = scope === null ? { x0: -0.94 + 2 * cover, x1: 0.9, y0: -0.74, y1: 0.78 } : { x0: -0.98 + 2 * cover, x1: 0.99, y0: -0.82, y1: 0.9 };
    fitProbe.aspect = aspect;
    fitProbe.updateProjectionMatrix();
    // The diorama's outline: the slab's underside, the walls' tops where
    // there are walls (back and left), the floor's open corner.
    // Over the campus, every room's; else the room's.
    const outlines = scope === null ? lay.rooms.map((r) => r.box) : [bx];
    const corners = outlines.flatMap((b) => [
      ...[b.x0 - 0.3, b.x1 + 0.3].flatMap((x) => [b.z0 - 0.3, b.z1 + 0.3].map((z) => new THREE.Vector3(x, -0.72, z))),
      new THREE.Vector3(b.x0 - 0.3, H, b.z0 - 0.3),
      new THREE.Vector3(b.x1 + 0.1, H, b.z0 - 0.3),
      new THREE.Vector3(b.x0 - 0.3, H, b.z1 + 0.1),
    ]);
    const v = new THREE.Vector3();
    const right = new THREE.Vector3(Math.cos(yaw0), 0, -Math.sin(yaw0));
    const back = new THREE.Vector3(-Math.sin(yaw0), 0, -Math.cos(yaw0));
    const bounds = (t: THREE.Vector3, d: number) => {
      fitProbe.position.set(t.x + Math.sin(yaw0) * Math.cos(pitch) * d, Math.sin(pitch) * d, t.z + Math.cos(yaw0) * Math.cos(pitch) * d);
      fitProbe.lookAt(t);
      fitProbe.updateMatrixWorld();
      const b = { x0: Infinity, x1: -Infinity, y0: Infinity, y1: -Infinity, behind: false };
      for (const c of corners) {
        v.copy(c).project(fitProbe);
        if (v.z >= 1) b.behind = true;
        b.x0 = Math.min(b.x0, v.x);
        b.x1 = Math.max(b.x1, v.x);
        b.y0 = Math.min(b.y0, v.y);
        b.y1 = Math.max(b.y1, v.y);
      }
      return b;
    };
    // Closer while the outline fits the window's size; then the outline is
    // centred in the window, by moving where the camera looks, and again.
    const t = new THREE.Vector3((bx.x0 + bx.x1) / 2, 0, (bx.z0 + bx.z1) / 2);
    let dist = 20;
    const tan = Math.tan(THREE.MathUtils.degToRad(FOV / 2));
    for (let round = 0; round < 3; round++) {
      let lo = 3;
      let hi = 300;
      for (let i = 0; i < 26; i++) {
        const mid = (lo + hi) / 2;
        const b = bounds(t, mid);
        if (!b.behind && b.x1 - b.x0 < win.x1 - win.x0 && b.y1 - b.y0 < win.y1 - win.y0) hi = mid;
        else lo = mid;
      }
      dist = hi;
      const b = bounds(t, dist);
      const dx = (b.x0 + b.x1) / 2 - (win.x0 + win.x1) / 2;
      const dy = (b.y0 + b.y1) / 2 - (win.y0 + win.y1) / 2;
      // Clip space to the floor: a unit across is half the view's width
      // there; up the screen is back across the floor, foreshortened.
      const vh = dist * tan;
      t.addScaledVector(right, dx * vh * aspect);
      t.addScaledVector(back, (dy * vh) / Math.max(0.2, Math.sin(pitch)));
    }
    fitCache = { key, dist, x: t.x, z: t.z };
    return fitCache;
  }

  // camStep eases the camera to where it should be; true while it's easing.
  function camStep(dt: number): boolean {
    const aspect = camera.aspect || 1;
    // From the overview down to following you is a long way: taken gently.
    // Standing back is quicker than coming down to you.
    const k = motion ? 1 - Math.exp(-dt * (followTarget < cam.followK ? 6.5 : 4.2)) : 1;
    cam.followK += (followTarget - cam.followK) * k;
    if (Math.abs(followTarget - cam.followK) < 1e-3) cam.followK = followTarget;
    // Close up at what you're at, and gliding into it over a walk's last
    // few metres, so you arrive in the close-up rather than settle into it.
    let closeTarget = near && stillFor > 0.15 && !held && !turned ? 1 : 0;
    if (!closeTarget && routeGoal && route.length) {
      const rem = Math.hypot(route[0].x - pos.x, route[0].z - pos.z) + pathLengthFrom(route);
      if (rem < 8) closeTarget = 1 - rem / 8;
    }
    if (closeTarget > 0) focusRef = near ?? routeGoal;
    cam.closeK += (closeTarget - cam.closeK) * (motion ? 1 - Math.exp(-dt * 6.5) : 1);
    if (Math.abs(closeTarget - cam.closeK) < 1e-3) cam.closeK = closeTarget;

    // The overview fits the room, from the front and above.
    const oPitch = OVERVIEW_PITCH;
    const { dist: oDist, x: oX, z: oZ } = fitDistance(scopeBox(), scopeWall(), aspect, oPitch);
    // Following: behind you, looking down at you; close up at a desk, lower
    // and nearer, the monitor and the robot in view.
    // Close up, the camera looks past your shoulder at the monitor, from the
    // right so you don't stand in the way.
    const c = cam.closeK;
    const focus = focusRef ? focusOf(focusRef, aspect) : undefined;
    const fPitch = FOLLOW_PITCH;
    const fDist = cam.dist;
    const f = cam.followK;
    // Following you, or the spot the wheel zoomed in on.
    const fx = zoomFocus ? zoomFocus.x : pos.x;
    const fz = zoomFocus ? zoomFocus.z : pos.z;
    let tx = oX + (fx - oX) * f;
    let tz = oZ + (fz - oZ) * f;
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
      const e = motion ? 1 - Math.exp(-dt * (held ? 10 : 8)) : 1;
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
      const behind = w.side === 'left' ? camera.position.x < 0.5 : camera.position.x > lay.room.x1 - 0.5;
      const o = behind ? 0.08 : 1;
      w.mat.opacity += (o - w.mat.opacity) * (motion ? 0.2 : 1);
      w.mat.depthWrite = w.mat.opacity > 0.9;
    }
    const after = eased.tx + eased.tz + eased.dist + eased.pitch + eased.yaw;
    // The chips over the canvas show only once the camera has settled: one
    // sliding under the pointer would send a click somewhere else.
    // Moving is moving visibly, not the easing's last hair.
    // As a speed, so it means the same at any frame rate.
    const moving = Math.abs(after - before) / Math.max(dt, 1e-3) > 0.45;
    settledFor = moving ? 0 : settledFor + dt;
    const show = settledFor > 0.15;
    if (show !== chipsShown) {
      chipsShown = show;
      // Away while it moves, and back once laid out where they'll stay: a tag
      // is never clicked on its way somewhere else.
      markerLayer.style.opacity = show ? '1' : '0';
      markerLayer.style.pointerEvents = 'none';
      for (const el of markerLayer.children) (el as HTMLElement).style.visibility = show ? '' : 'hidden';
    }
    return Math.abs(after - before) > 1e-4 || cam.followK !== followTarget || cam.closeK !== closeTarget;
  }

  function render(now: number) {
    const t = now / 1000;
    lighting();
    let markHere = false;
    // Over the campus every room's at once; in a room, a dozen at most.
    const crowded = scope === null;
    // How far the board you're looking at is from the camera, in its close-up.
    let boardDist = 0;
    if (isBoard(focusRef) && cam.closeK > 0.3) {
      const rb = lay.rooms[roomOf(lay, focusRef) ?? cur].board;
      boardDist = camera.position.distanceTo(new THREE.Vector3(rb.x, 1.5, rb.spot.z - 2.1));
    }
    // Where the agents' heads are on screen, which the projects' pills keep off.
    const heads: { x: number; y: number }[] = [];
    {
      const yv = new THREE.Vector3(pos.x, 0.8, pos.z).project(camera);
      heads.push({ x: ((yv.x + 1) / 2) * canvas.clientWidth, y: ((1 - yv.y) / 2) * canvas.clientHeight });
    }
    // In a close-up: where on screen the agent you're at and you are, and how
    // far the camera is from it, for what's in the way (below).
    let lookAt: { dist: number; spots: { x: number; y: number }[] } | undefined;
    const focusDesk = focusRef ? desks.get(focusRef) : undefined;
    if (focusDesk && cam.closeK > 0.3) {
      const v = new THREE.Vector3();
      const scr = (w: THREE.Vector3) => {
        w.project(camera);
        return { x: ((w.x + 1) / 2) * canvas.clientWidth, y: ((1 - w.y) / 2) * canvas.clientHeight };
      };
      focusDesk.figure.group.getWorldPosition(v);
      v.y += 1.0;
      const dist = v.distanceTo(camera.position);
      const a = scr(v.clone());
      const me0 = scr(new THREE.Vector3(pos.x, 0.7, pos.z));
      lookAt = { dist, spots: [a, me0] };
    } else if (scope !== null && cam.followK > 0.5) {
      // Following you: whoever stands between you and the camera steps aside.
      const v = new THREE.Vector3(pos.x, 0.8, pos.z);
      const dist = v.distanceTo(camera.position);
      v.project(camera);
      const me0 = { x: ((v.x + 1) / 2) * canvas.clientWidth, y: ((1 - v.y) / 2) * canvas.clientHeight };
      lookAt = { dist, spots: [me0] };
    }
    for (const d of desks.values()) {
      // Where you are, from the robot: near enough, it turns to look at you.
      const fx = d.root.position.x + d.figure.group.position.x;
      const fz = d.root.position.z + d.figure.group.position.z;
      const near2 = (pos.x - fx) ** 2 + (pos.z - fz) ** 2 < 25;
      d.figure.pose(d.tone, t, motion, near2 ? { x: pos.x - fx, z: pos.z - fz } : undefined);
      // From afar the characters grow a little, toy-like, so the room reads
      // as theirs; always seated on their chairs.
      const fg = d.figure.group;
      const base = (fg.userData.base as number | undefined) ?? (fg.userData.base = fg.scale.x);
      const far = fg.getWorldPosition(figPos).distanceTo(camera.position);
      // Not while the camera follows you: grown, they'd hide you.
      // Over the campus they grow to be seen; in a room, already at its
      // scale, hardly: grown, they'd crowd and stand out of their chairs.
      const k = base * (1 + Math.min(crowded ? 0.65 : 0.15, Math.max(0, (far - 12) / 36)) * (1 - cam.followK));
      fg.scale.setScalar(k);
      fg.position.y = 0.49 * (1 - k);
      if (d.root.visible) {
        const hv = new THREE.Vector3(fx, 0.49 + 0.75 * k, fz).project(camera);
        heads.push({ x: ((hv.x + 1) / 2) * canvas.clientWidth, y: ((1 - hv.y) / 2) * canvas.clientHeight });
      }
      // Up close, what floats over the desks beside the camera would be in
      // the way of the one you're looking at.
      // So does the row in front of the desk you're at: it sits between you
      // and the camera.
      // Any desk nearer the camera than the one you're at, on screen over it
      // or you, steps out of the way.
      let ahead = false;
      let leadHidesFace = false;
      if (lookAt && d.ref !== focusRef) {
        figPos.y += 1.0;
        const nearer = figPos.distanceTo(camera.position) < lookAt.dist - 0.8;
        figPos.project(camera);
        const sx = ((figPos.x + 1) / 2) * canvas.clientWidth;
        const sy = ((1 - figPos.y) / 2) * canvas.clientHeight;
        ahead = nearer && lookAt.spots.some((p) => Math.abs(sx - p.x) < 190 && Math.abs(sy - p.y) < 230);
        leadHidesFace = ahead;
      }
      const inWay = cam.closeK > 0.3 && d.ref !== focusRef && (d.root.position.distanceTo(camera.position) < 4.2 || ahead);
      // At a board, what stands between it and the camera steps aside.
      const boardWay = boardDist > 0 && d.root.position.distanceTo(camera.position) < boardDist - 1.5;
      // A lead in the way only stands aside itself: its counter stays, and
      // takes clicks.
      const isLeadDesk = d.ref.endsWith('/lead');
      d.root.visible = inScope(d.root) && (isLeadDesk || !(inWay && ahead)) && !boardWay;
      d.figure.group.visible = !(isLeadDesk && inWay && ahead && leadHidesFace);
      d.bubble.sprite.visible = !inWay && !!bubbleOf[d.tone] && !(cam.closeK > 0.3 && d.ref !== focusRef && far < 8);
      const b = d.beacon;
      b.visible = false;
      b.rotation.y = motion ? t * (d.tone === 'working' ? 2.2 : 0.8) : 0.6;
      const lift = d.ref.endsWith('/lead') ? 0.3 : 0.12;
      b.position.y = 1.95 + lift + (motion ? Math.sin(t * 2 + b.position.x) * 0.05 : 0);
      const s = d.bubble.sprite;
      // From far off the bubbles grow, so who needs you shows from the
      // overview; over the grown figures' heads.
      // Only the loud ones (needs you, failed) grow: the overview shows those.
      const loudTone = d.tone === 'needs' || d.tone === 'error';
      // Big over the campus, where they say which room needs you; in a room
      // a little, clear of each other.
      const grow = Math.min(d.tone === 'needs' ? (crowded ? 8 : 2.9) : loudTone ? (crowded ? 4.5 : 2.2) : crowded ? 1 : 1.35, Math.max(1, (far - 8) / 12));
      // A crowd's quiet bubbles show as you come in: from afar they'd bury the loud ones.
      if (crowded && !loudTone && far > 30) s.visible = false;
      const sb = (s.userData.base as THREE.Vector2 | undefined) ?? (s.userData.base = new THREE.Vector2(s.scale.x, s.scale.y));
      s.scale.set(sb.x * grow, sb.y * grow, 1);
      // Clear of the head and whatever it wears.
      s.position.y = 0.49 + 1.18 * k + lift * 0.5 + (grow - 1) * 0.2 + (motion && d.tone === 'needs' ? Math.abs(Math.sin(t * 3.5)) * 0.12 * grow : 0);
      if (d.ref === near) {
        atMark.sprite.visible = cam.closeK > 0.2 || cam.followK > 0.3;
        // A lead's just over its own head, in front of whoever sits behind it.
        if (d.ref.endsWith('/lead')) atMark.sprite.position.set(fx, 0.49 + 0.95 * k + (motion ? Math.abs(Math.sin(t * 3)) * 0.06 : 0), fz + 0.35);
        else atMark.sprite.position.set(fx, s.position.y + (bubbleOf[d.tone] && s.visible ? 0.42 * grow : 0) + 0.05 + (motion ? Math.abs(Math.sin(t * 3)) * 0.08 : 0), fz);
        markHere = true;
      }
      // The desk you're at wears a soft white ring; a loud one keeps its own.
      const chosen = d.ref === near && !(d.tone === 'needs' || d.tone === 'error');
      d.ring.visible = chosen || d.tone === 'needs' || d.tone === 'error';
      (d.ring.material as THREE.MeshBasicMaterial).color.set(chosen ? '#ffffff' : toneColor[d.tone]);
      if (d.ring.visible) {
        const p = motion ? (t * 0.8) % 1 : 0.5;
        d.ring.scale.setScalar((0.9 + p * 0.5) * Math.min(1.8, grow));
        (d.ring.material as THREE.MeshBasicMaterial).opacity = 0.55 * (1 - p);
      }
      // The beams stand down while you read the board over them.
      // No columns of light: the ring, the chip and the bubble say it.
      d.beam.visible = false;
      if (d.beam.visible) (d.beam.material as THREE.MeshBasicMaterial).opacity = 0.045 + (motion ? Math.sin(t * 3) * 0.015 : 0);
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
    if (!markHere) atMark.sprite.visible = false;
    // The potted plants step aside as the desks do.
    for (const o of plantObjs) {
      let hide = false;
      if (lookAt) {
        figPos.set(o.position.x, 0.9, o.position.z);
        const nearer = figPos.distanceTo(camera.position) < lookAt.dist - 0.8;
        figPos.project(camera);
        const sx = ((figPos.x + 1) / 2) * canvas.clientWidth;
        const sy = ((1 - figPos.y) / 2) * canvas.clientHeight;
        hide = nearer && lookAt.spots.some((p) => Math.abs(sx - p.x) < 170 && Math.abs(sy - p.y) < 220);
      }
      o.visible = inScope(o) && !hide;
    }
    // Signs give way to the camera, and to the board's close-up over them.
    for (const sg of signs) sg.visible = !(isBoard(focusRef) && cam.closeK > 0.2) && sg.position.distanceTo(camera.position) > (cam.closeK > 0.3 ? 7 : 3.5);
    // From far off, each area's label floats over its sign. What the labels
    // and your chip take, the desks' chips keep clear of.
    const taken: { x0: number; x1: number; y0: number; y1: number }[] = [];
    const ap = new THREE.Vector3();
    // Each room's waiting count, on its pill.
    // Who needs you, in amber; what failed, in red beside it.
    const waiting = new Map<number, number>();
    const failed = new Map<number, number>();
    const busy = new Map<number, number>();
    for (const d of desks.values()) {
      if (d.tone !== 'working') continue;
      const ri = roomOf(lay, d.ref);
      if (ri !== undefined) busy.set(ri, (busy.get(ri) ?? 0) + 1);
    }
    for (const d of desks.values()) {
      if (d.tone !== 'needs' && d.tone !== 'error') continue;
      const ri = roomOf(lay, d.ref);
      if (ri === undefined) continue;
      const m = d.tone === 'needs' ? waiting : failed;
      m.set(ri, (m.get(ri) ?? 0) + 1);
    }
    for (const [ri, rm] of lay.rooms.entries()) {
      const chip = areaChips.get(rm.key);
      if (!chip) continue;
      const n0 = waiting.get(ri) ?? 0;
      const f0 = failed.get(ri) ?? 0;
      const w0 = busy.get(ri) ?? 0;
      const sub = chip.children[1] as HTMLElement;
      const bad = chip.children[2] as HTMLElement;
      const work = chip.children[3] as HTMLElement;
      if (sub.dataset.n !== `${n0}:${f0}:${w0}`) {
        sub.dataset.n = `${n0}:${f0}:${w0}`;
        work.textContent = w0 ? `● ${w0}` : '';
        work.style.display = w0 && !n0 && !f0 ? 'inline' : 'none';
        sub.textContent = n0 ? String(n0) : '';
        sub.style.display = n0 ? 'inline' : 'none';
        // Amber, as everything that needs you is.
        sub.style.background = '#fbbf24';
        sub.style.color = '#3a2a05';
        bad.textContent = f0 ? `× ${f0}` : '';
        bad.style.display = f0 ? 'inline' : 'none';
      }
      let on = false;
      if (scope === null) {
        // Over the campus: at its room's front corner, the one nearest you,
        // its name plate, clear of the rooms behind.
        ap.set(rm.box.x1 - Math.min(5, (rm.box.x1 - rm.box.x0) / 3), -0.4, rm.box.z1 + 1.0);
        ap.project(camera);
        on = ap.z < 1 && Math.abs(ap.x) < 1 && Math.abs(ap.y) < 1;
      }
      // (In a room, its name is in the breadcrumb and on its lead's counter.)
      // Not under the top bar, nor over the legend and the hint at the bottom.
      const ay = ((1 - ap.y) / 2) * canvas.clientHeight;
      on = on && ay > 100 && ay < canvas.clientHeight - 96;
      chip.style.display = on ? 'flex' : 'none';
      if (on) {
        const cx = ((ap.x + 1) / 2) * canvas.clientWidth;
        const cy = ay;
        const cxIn = Math.min(canvas.clientWidth - 80, Math.max(80, cx));
        chip.style.transform = `translate(${cxIn}px, ${cy}px) translate(-50%, -50%)`;
        const w = chip.offsetWidth || 140;
        const h = chip.offsetHeight || 38;
        taken.push({ x0: cx - w / 2, x1: cx + w / 2, y0: cy - h / 2, y1: cy + h / 2 });
      }
    }
    // You stand beside what you look at, never in front of it: always seen.
    me.fade(1);
    meTag.sprite.visible = cam.closeK < 0.3;
    // Name tags grow with distance to stay readable, up to a point; from far
    // off only the ones that need you stay, and up close they get out of the way.
    const tp = new THREE.Vector3();
    const shown: { el: HTMLDivElement; x: number; y: number; loud: boolean; needs?: boolean }[] = [];
    // Where the overlay's card or board list is: no chip shows under it.
    const cardEl = document.querySelector('[data-office-desk], [data-office-board]');
    const cr = canvas.getBoundingClientRect();
    const cardBox = cardEl ? (() => {
      const r = cardEl.getBoundingClientRect();
      return { x0: r.left - cr.left - 60, x1: r.right - cr.left + 8, y0: r.top - cr.top - 8, y1: r.bottom - cr.top + 34 };
    })() : undefined;
    // The face of the agent you're at, on screen: no one else's tag covers it.
    const atDesk = near ? desks.get(near) : undefined;
    let face: { x: number; y: number } | undefined;
    if (atDesk && cam.closeK > 0.3) {
      atDesk.figure.group.getWorldPosition(tp);
      tp.y += 1.1;
      tp.project(camera);
      face = { x: ((tp.x + 1) / 2) * canvas.clientWidth, y: ((1 - tp.y) / 2) * canvas.clientHeight };
    }
    // Small from afar, as name tags are; wider as you come in to read them.
    const tagW = cam.followK < 0.3 && cam.closeK < 0.3 ? '150px' : '170px';
    const smallRoom = scope !== null && lay.rooms[scope].desks.length <= 8;
    if (markerLayer.style.getPropertyValue('--tag-w') !== tagW) markerLayer.style.setProperty('--tag-w', tagW);
    for (const d of desks.values()) {
      const tag = d.tag.sprite;
      tag.getWorldPosition(tp);
      const dist = tp.distanceTo(camera.position);
      const marker = markers.get(d.ref);
      // The labels: the loud ones from anywhere, the rest only near enough
      // to mean something; none for the desk you're at (its card says it)
      // or one stepped aside for the close-up.
      // From a room's overview only who needs you; what failed (its red ✕
      // says it) as you come in.
      const loud = marker?.dataset.loud === '1' && (d.tone === 'needs' || cam.followK > 0.2 || cam.closeK > 0.3);
      tag.visible = false;
      if (marker) {
        tp.project(camera);
        // Every character wears its name tag, as in a real office; up close
        // only the loud ones.
        // From the overview only the ones that need you; nearer, everyone's.
        // The overview shows the pods' pills and counts and the bubbles over
        // heads; the tags come as you come in.
        // Quiet ones only near enough to read, so a mid view isn't a wall of tags.
        // From the overview a small team's loud ones only, a crowd's none (its
        // bubbles say it); coming in, the loud ones, then everyone's near you.
        // From the overview a small team's all, as name tags in a room, and a
        // crowd's loud ones; coming in, everyone's near you. (They keep off
        // faces, and a quiet one with no room steps out of the way.)
        // (None over the campus: its rooms' pills and bubbles say it.)
        // A small room shows everyone's from its overview, as the target does;
        // a full one its loud ones, the rest as you come in.
        const want = scope !== null && d.root.visible && !(d.ref === focusRef && cam.closeK > 0.3) && dist > 2.6 && (loud || smallRoom || cam.followK > 0.4) && (loud || (cam.closeK < 0.3 && (smallRoom || dist < 17)));
        const onScreen = want && tp.z < 1 && Math.abs(tp.x) < 1.05 && Math.abs(tp.y) < 1.05 && ((1 - tp.y) / 2) * canvas.clientHeight > 110;
        const mx = ((tp.x + 1) / 2) * canvas.clientWidth;
        const my = Math.min(canvas.clientHeight - 130, ((1 - tp.y) / 2) * canvas.clientHeight);
        const underCard = (!!cardBox && mx > cardBox.x0 && mx < cardBox.x1 && my > cardBox.y0 && my < cardBox.y1) || (!!face && Math.abs(mx - face.x) < 140 && my > face.y - 200 && my < face.y + 160);
        const show = onScreen && !underCard;
        marker.style.display = show ? 'flex' : 'none';
        if (show) shown.push({ el: marker, x: mx, y: my, loud, needs: d.tone === 'needs' });
      }
    }
    // You, from far off: a chip in your colour over your head, or under your
    // feet when a label is there. Placed first: the desks' chips keep clear of it.
    meTag.sprite.getWorldPosition(tp);
    const meFar = scope !== null && tp.distanceTo(camera.position) > 15 && cam.closeK < 0.3 && cam.followK < 0.3;
    tp.project(camera);
    meMarker.style.display = meFar && tp.z < 1 ? 'block' : 'none';
    if (meFar) {
      const mx = ((tp.x + 1) / 2) * canvas.clientWidth;
      const my = ((1 - tp.y) / 2) * canvas.clientHeight;
      const mw = meMarker.offsetWidth || 44;
      const over = { x0: mx - mw / 2, x1: mx + mw / 2, y0: my - 40, y1: my - 18 };
      const under = taken.some((r) => over.x0 < r.x1 && over.x1 > r.x0 && over.y0 < r.y1 && over.y1 > r.y0) || heads.slice(1).some((hd) => Math.abs(hd.x - mx) < mw / 2 + 16 && hd.y > over.y0 - 26 && hd.y < over.y1 + 12);
      meMarker.style.transform = `translate(${mx}px, ${my}px) translate(-50%, ${under ? '140%' : '-110%'})`;
      taken.push(under ? { x0: over.x0, x1: over.x1, y0: my + 30, y1: my + 52 } : over);
      meTag.sprite.visible = false;
    } else if (tp.z < 1) {
      // Near, you have no chip, but the others' keep off your head.
      const mx = ((tp.x + 1) / 2) * canvas.clientWidth;
      const my = ((1 - tp.y) / 2) * canvas.clientHeight;
      taken.push({ x0: mx - 40, x1: mx + 40, y0: my - 30, y1: my + 70 });
    }
    // The desks' chips stack clear of the labels and of each other, the
    // nearest (lowest on screen) where it belongs. Their stacking is worked
    // out only while the camera is still, and kept while it moves, so a chip
    // never jumps or goes as you reach for it; one with no room stays where
    // it is rather than vanish.
    shown.sort((a, b) => Number(b.loud) - Number(a.loud) || b.y - a.y);
    const h = 24;
    const folded = new Map<HTMLElement, number>();
    // Heads are taken too: a tag never covers a face.
    const headRects = heads.slice(1).map((hd) => ({ x0: hd.x - 20, x1: hd.x + 20, y0: hd.y - 26, y1: hd.y + 12 }));
    const placed: { x0: number; x1: number; y: number }[] = [...taken, ...headRects].flatMap((r) => {
      // As the anchors of chips that would cover it (a chip's box runs from
      // 7px below its anchor to 31 below), a row every 20px.
      const rows: { x0: number; x1: number; y: number }[] = [];
      for (let y = r.y1 - 7; y > r.y0 - 31; y -= 20) rows.push({ x0: r.x0, x1: r.x1, y });
      return rows;
    });
    // A chip with no room of its own folds into the one in its way, as a
    // "+n" on it: N, or the list, still gets you to each.
    // Laid out once each time the camera comes to rest (or what's shown
    // changes), then held: a chip never trades places under the pointer.
    const restackKey = chipsShown ? `${eased.tx.toFixed(1)}:${eased.tz.toFixed(1)}:${eased.dist.toFixed(1)}:${eased.yaw.toFixed(2)}:${shown.map((m) => m.el.dataset.officeMarker).join(',')}` : lastRestack;
    const restack = restackKey !== lastRestack;
    lastRestack = restackKey;
    const chips: { x0: number; x1: number; y: number; el: HTMLDivElement; loud: boolean }[] = [];
    for (const m of shown) {
      // With room for a "+n" it may get, so it doesn't grow into a neighbour.
      const moreShown = (m.el.children[1] as HTMLElement).style.display !== 'none';
      const w = (m.el.offsetWidth || 120) + (moreShown ? 0 : 30);
      // Kept inside the canvas, whichever way it steps.
      const inside = (x: number) => Math.min(canvas.clientWidth - w / 2 - 6, Math.max(w / 2 + 6, x));
      const clash = (x: number, y: number) => placed.some((p) => inside(x) - w / 2 < p.x1 + 4 && inside(x) + w / 2 > p.x0 - 4 && Math.abs(p.y - y) < h);
      // Where to try, nearest first: above and below, then a little aside.
      const tries: [number, number][] = [];
      for (const dy of [0, -h, h, -2 * h, 2 * h]) for (const dx of [0, w * 0.4, -w * 0.4]) tries.push([dx, dy]);
      tries.sort((a, b) => Math.abs(a[1]) + Math.abs(a[0]) / 2 - (Math.abs(b[1]) + Math.abs(b[0]) / 2));
      let off = chipDy.get(m.el);
      // Kept while the camera moves, unless where it was is taken now.
      // Laid out again only while the camera is still: moving, they keep their
      // places, overlapping for a moment rather than jumping.
      if (restack || off === undefined) {
        off = tries.find(([dx, dy]) => !clash(m.x + dx, m.y + dy)) ?? null;
        chipDy.set(m.el, off);
      }
      if (!off && !m.loud) {
        // A quiet label with no room simply steps out of the way.
        m.el.style.display = 'none';
        continue;
      }
      // Who needs you is never folded away: its tag stays where it belongs.
      if (!off && m.needs) off = [0, 0];
      if (!off) {
        // No room: folded into the chip in its way, if there's one nearby;
        // else it stays where it belongs.
        // The nearest loud chip takes it.
        // Only one close by: a "+n" stands for its neighbours, not the room.
        const near1 = chips
          .filter((c) => c.loud && Math.hypot((c.x0 + c.x1) / 2 - m.x, c.y - m.y) < 110)
          .sort((a, b) => Math.hypot((a.x0 + a.x1) / 2 - m.x, a.y - m.y) - Math.hypot((b.x0 + b.x1) / 2 - m.x, b.y - m.y))[0];
        const into = (restack ? undefined : chipInto.get(m.el)) ?? near1?.el;
        if (into) {
          chipInto.set(m.el, into);
          m.el.style.display = 'none';
          folded.set(into, (folded.get(into) ?? 0) + 1);
          continue;
        }
        // Nowhere to go and no one to fold into: who needs you keeps a tag,
        // where it belongs.
        off = [0, 0];
      }
      const x = inside(m.x + off[0]);
      const y = m.y + off[1];
      placed.push({ x0: x - w / 2, x1: x + w / 2, y });
      chips.push({ x0: x - w / 2, x1: x + w / 2, y, el: m.el, loud: m.loud });
      // Under its desk, as a name tag hangs: never over a face or a bubble.
      m.el.style.transform = `translate(${x}px, ${y}px) translate(-50%, 30%)`;
    }
    for (const c of chips) {
      const n = folded.get(c.el) ?? 0;
      const more = c.el.children[1] as HTMLElement;
      more.style.display = n ? 'inline' : 'none';
      if (n) more.textContent = opts.moreLabelShort(n);
    }
    // The board you're at: a button over each of its rows.
    const atBoard = isBoard(near) && cam.closeK > 0.6 && chipsShown ? boards.get(near) : undefined;
    let usedRows = 0;
    if (atBoard?.face) {
      const { width: W, height: H } = atBoard.ctx.canvas;
      const face = atBoard.face;
      face.updateWorldMatrix(true, false);
      const corner = (px: number, py: number) => {
        const v = new THREE.Vector3(px / W - 0.5, 0.5 - py / H, 0).applyMatrix4(face.matrixWorld).project(camera);
        return { x: ((v.x + 1) / 2) * canvas.clientWidth, y: ((1 - v.y) / 2) * canvas.clientHeight };
      };
      for (const r of atBoard.rows) {
        const a = corner(r.x, r.y);
        const b = corner(r.x + r.w, r.y + r.h);
        const btn = rowBtn(usedRows++);
        btn.dataset.ref = r.ref;
        btn.setAttribute('aria-label', r.ref.startsWith('more:') ? opts.moreLabel : opts.markerLabel(atBoard.titles?.get(r.ref) ?? r.ref));
        btn.style.display = 'block';
        btn.style.transform = `translate(${Math.min(a.x, b.x)}px, ${Math.min(a.y, b.y)}px)`;
        btn.style.width = `${Math.abs(b.x - a.x)}px`;
        btn.style.height = `${Math.abs(b.y - a.y)}px`;
      }
    }
    for (let i = usedRows; i < rowBtns.length; i++) rowBtns[i].style.display = 'none';
    const ringO = 0.55 + (motion ? Math.sin(t * 2.5) * 0.25 : 0.2);
    (meRing.material as THREE.MeshBasicMaterial).opacity = ringO;
    // From afar your ring grows, so you're found at a glance.
    meRing.scale.setScalar(0.42 * Math.min(3, Math.max(1, (camera.position.distanceTo(me.group.position) - 10) / 12)));
    aoCam.copy(camera);
    aoCam.layers.set(0);
    composer.render();
  }

  // Sizes the canvas to its box.
  function resize() {
    const w = Math.max(1, host.clientWidth);
    const h = Math.max(1, host.clientHeight);
    renderer.setSize(w, h, false);
    composer.setSize(w, h);
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
    poke();
  }
  const ro = new ResizeObserver(resize);
  ro.observe(host);
  resize();

  // floorAt is the floor under a point of the canvas, whatever stands on it.
  function floorAt(clientX: number, clientY: number): P | undefined {
    const r = canvas.getBoundingClientRect();
    ndc.set(((clientX - r.left) / r.width) * 2 - 1, -((clientY - r.top) / r.height) * 2 + 1);
    ray.setFromCamera(ndc, camera);
    const p = new THREE.Vector3();
    return ray.ray.intersectPlane(floorPlane, p) ? { x: p.x, z: p.z } : undefined;
  }

  function pick(clientX: number, clientY: number): { ref?: string; row?: string; floor?: P; bubble?: boolean } {
    const r = canvas.getBoundingClientRect();
    ndc.set(((clientX - r.left) / r.width) * 2 - 1, -((clientY - r.top) / r.height) * 2 + 1);
    ray.setFromCamera(ndc, camera);
    // Standing at the board, its rows come first: the desks in front of it
    // mustn't take a click meant for the board.
    // Only what's shown: a hidden bubble or a desk stepped aside takes no click.
    const shownUp = (o: THREE.Object3D | null): boolean => !o || (o.visible && shownUp(o.parent));
    const hit = ray.intersectObjects(hits, false).find((h) => shownUp(h.object));
    if (hit) return { ref: hit.object.userData.ref as string, bubble: (hit.object as THREE.Sprite).isSprite === true };
    const p = new THREE.Vector3();
    if (ray.ray.intersectPlane(floorPlane, p)) return { floor: { x: p.x, z: p.z } };
    return {};
  }

  // Two fingers on a touch screen pinch to zoom.
  const touches = new Map<number, { x: number; y: number }>();
  let pinchFrom = 0;
  const pinchDist = () => {
    const [a, b] = [...touches.values()];
    return a && b ? Math.hypot(a.x - b.x, a.y - b.y) : 0;
  };
  const onPointerDown = (e: PointerEvent) => {
    if (e.pointerType === 'touch') {
      touches.set(e.pointerId, { x: e.clientX, y: e.clientY });
      if (touches.size === 2) {
        pinchFrom = pinchDist();
        pointer = undefined;
        return;
      }
    }
    canvas.focus({ preventScroll: true });
    // What's under the pointer as it's pressed is what the click is for, even
    // if the camera moves on before it's released.
    pointer = { x: e.clientX, y: e.clientY, id: e.pointerId, dragged: false, button: e.button, aim: pick(e.clientX, e.clientY), cam: camAt() };
    canvas.setPointerCapture(e.pointerId);
    poke();
  };
  const onPointerMove = (e: PointerEvent) => {
    if (e.pointerType === 'touch' && touches.has(e.pointerId)) {
      touches.set(e.pointerId, { x: e.clientX, y: e.clientY });
      if (touches.size === 2 && pinchFrom > 0) {
        const now = pinchDist();
        // Spreading the fingers comes in; pinching them goes out.
        zoomTo(zoomZ + Math.log(now / pinchFrom) * 0.9);
        pinchFrom = now;
        poke();
        return;
      }
    }
    if (pointer && pointer.id === e.pointerId) {
      const dx = e.clientX - pointer.x;
      if (!pointer.dragged && Math.hypot(dx, e.clientY - pointer.y) > 5) pointer.dragged = true;
      if (pointer.dragged) {
        // Dragging turns the room, within limits.
        // Turning steps out of a close-up, so the turn shows at once.
        // You're still at the desk, its card up: only the camera stands back.
        if (cam.closeK > 0.3 && !held && !turned) {
          turned = true;
          cam.yaw = eased.yaw;
        }
        cam.yaw = Math.max(homeYaw() - 0.7, Math.min(homeYaw() + 0.5, cam.yaw - e.movementX * 0.0035));
        canvas.style.cursor = 'grabbing';
        poke();
        return;
      }
    }
    hoverAt = { x: e.clientX, y: e.clientY };
    refreshHover();
  };
  let hoverAt: { x: number; y: number } | undefined;
  let pendingHover = false;
  // refreshHover works out, by the next frame, what's under the pointer: as
  // it moves, and as the camera moves under a pointer that doesn't.
  function refreshHover() {
    hoverDirty = true;
    if (pendingHover) return;
    pendingHover = true;
    requestAnimationFrame(() => {
      pendingHover = false;
      if (!hoverDirty || !hoverAt) return;
      hoverDirty = false;
      const { ref, row } = pick(hoverAt.x, hoverAt.y);
      // At the board only its rows do something.
      canvas.style.cursor = ref && !(isBoard(ref) && ref === near && !row) ? 'pointer' : 'default';
      if (ref !== hover) {
        hover = ref;
        opts.onHover(isBoard(ref) ? 'board' : ref);
        poke();
      }
    });
  }
  const onPointerUp = (e: PointerEvent) => {
    touches.delete(e.pointerId);
    if (touches.size < 2) pinchFrom = 0;
    if (!pointer || pointer.id !== e.pointerId) return;
    const p = pointer;
    pointer = undefined;
    canvas.style.cursor = hover ? 'pointer' : 'default';
    if (p.dragged || p.button !== 0) return;
    // What's under the pointer as it's let go: the press's, unless the camera
    // moved between the two, when it's looked again.
    const { ref, row, floor, bubble } = p.cam === camAt() ? p.aim : pick(e.clientX, e.clientY);
    // On the way to a board, a click on what's in front of it waits: its notes
    // (buttons of their own) are what's meant.
    if (ref && isBoard(routeGoal) && (route.length || cam.closeK < 0.95)) return;
    // Over the campus, a click anywhere on a room goes into it.
    // (A "!" over a room's agent goes to that agent: that's what it says.)
    if (scope === null) {
      if (ref && bubble) return walkTo(ref);
      const ri = ref ? roomOf(lay, ref) : floor ? roomAt(lay, floor, 0.4) : undefined;
      if (ri !== undefined) enter(ri);
      return;
    }
    // A row on the board opens what it's about, from wherever you see it.
    if (row && (isBoard(near) || !row.startsWith('more:'))) opts.onBoardRow(row);
    else if (ref) {
      if (ref === near && !route.length) {
        if (!isBoard(ref)) opts.onActivate(ref);
      }
      else walkTo(ref);
    } else if (floor) {
      // Over the campus, a click on a room goes into it; in a room, walks.
      const ri = roomAt(lay, floor, 0.4);
      if (scope === null) {
        if (ri !== undefined) enter(ri);
      } else walkToPoint(floor);
    }
  };
  const onPointerLeave = () => {
    hoverAt = undefined;
    if (hover) {
      hover = undefined;
      opts.onHover(undefined);
      poke();
    }
  };
  // The wheel zooms in steps from the overview down to following you, and
  // then closer; a trackpad's small deltas make small steps.
  const zoomProbe = new THREE.PerspectiveCamera(FOV, 1, 0.1, 400);
  const zoomRay = new THREE.Raycaster();
  const onWheel = (e: WheelEvent) => {
    e.preventDefault();
    // A mouse wheel's notch is a whole step, whatever its delta; a
    // trackpad's little deltas are parts of one.
    // A trackpad's pinch arrives as a ctrl-wheel with small deltas: scaled
    // up so a pinch zooms about as far as it moves.
    const d = (e.deltaMode === 1 ? e.deltaY * 33 : e.deltaY) * (e.ctrlKey ? 4 : 1);
    const n = Math.max(-3, Math.min(3, Math.abs(d) >= 50 ? Math.sign(d) * Math.max(1, Math.abs(d) / 120) : d / 100));
    // Over the campus, a turn inwards goes into the room under the pointer;
    // at a room's overview, a turn outwards stands back to the campus.
    const over = floorAt(e.clientX, e.clientY);
    const overRoom = over ? roomAt(lay, over, 0.4) : undefined;
    if (scope === null && n < 0) {
      if (overRoom !== undefined) enter(overRoom);
    } else if (scope !== null && n > 0 && zoomZ === 0) {
      campus();
    } else if (cam.closeK > 0.5 && n > 0) {
      // At a desk, a turn of the wheel outwards stands back from it.
      held = true;
      zoomTo(0.5);
    } else {
      // Zooming keeps the floor under the pointer where it is on screen: the
      // camera's target moves towards it as the camera comes in. Out to the
      // overview, it lets go.
      const under = floorAt(e.clientX, e.clientY);
      if (under && cam.closeK < 0.5) {
        const sb = scopeBox();
        const before = { x: eased.tx, z: eased.tz, d: eased.dist };
        const z1 = Math.max(0, Math.min(1, zoomZ - n * 0.07));
        const f1 = Math.min(1, z1 * 2);
        const { dist: oDist, x: cx, z: cz } = fitDistance(scopeBox(), scopeWall(), camera.aspect || 1, OVERVIEW_PITCH);
        const fd1 = z1 <= 0.5 ? FOLLOW_FAR : FOLLOW_FAR - (z1 - 0.5) * 2 * (FOLLOW_FAR - FOLLOW_NEAR);
        const d1 = oDist + (fd1 - oDist) * f1;
        const k = d1 / Math.max(0.01, before.d);
        // Where the target should be for the point to hold still: scaled
        // about it, then put right for the camera tilting and rising as it
        // comes in, by where the pointer would land on the floor.
        let tx = under.x + (before.x - under.x) * k;
        let tz = under.z + (before.z - under.z) * k;
        const r = canvas.getBoundingClientRect();
        const at = new THREE.Vector2(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
        const p1 = OVERVIEW_PITCH + (FOLLOW_PITCH - OVERVIEW_PITCH) * f1;
        const g = new THREE.Vector3();
        for (let i = 0; i < 3; i++) {
          zoomProbe.aspect = camera.aspect || 1;
          zoomProbe.updateProjectionMatrix();
          const cp = Math.cos(p1);
          zoomProbe.position.set(tx + Math.sin(eased.yaw) * cp * d1, 0.9 * f1 + Math.sin(p1) * d1, tz + Math.cos(eased.yaw) * cp * d1);
          zoomProbe.lookAt(tx, 0.9 * f1, tz);
          zoomProbe.updateMatrixWorld();
          zoomRay.setFromCamera(at, zoomProbe);
          if (!zoomRay.ray.intersectPlane(floorPlane, g)) break;
          tx += under.x - g.x;
          tz += under.z - g.z;
        }
        // ...and the focus that, blended with the overview's centre, gives it.
        if (f1 > 0.02) zoomFocus = { x: Math.min(sb.x1, Math.max(sb.x0, cx + (tx - cx) / f1)), z: Math.min(sb.z1, Math.max(sb.z0, cz + (tz - cz) / f1)) };
      }
      zoomTo(zoomZ - n * 0.07);
      if (zoomZ === 0) zoomFocus = undefined;
    }
    poke();
  };
  const editable = (el: Element | null) => !!el && (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || (el as HTMLElement).isContentEditable || el.closest('[role="dialog"]') !== null);
  const walkKeys = new Set(['w', 'a', 's', 'd', 'arrowup', 'arrowdown', 'arrowleft', 'arrowright']);
  // Whether a dialog was open as the key went down: one that closes on
  // Escape is gone by the time the Office hears it, which mustn't then step
  // back too.
  let dialogAtKey = false;
  const onKeyCapture = () => {
    dialogAtKey = !!document.querySelector('[role="dialog"], [aria-modal="true"]');
  };
  const onKeyDown = (e: KeyboardEvent) => {
    if (!attached) return;
    if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey || editable(document.activeElement) || dialogAtKey || document.querySelector('[role="dialog"]')) return;
    const k = e.key.toLowerCase();
    if (walkKeys.has(k)) {
      // Arrows walk only while the office has focus, so they still scroll
      // and move through lists elsewhere.
      if (k.startsWith('arrow') && document.activeElement !== canvas && document.activeElement !== document.body) return;
      keys.add(k);
      e.preventDefault();
      poke();
    } else if (k === 'escape' && (near || followTarget > 0 || scope !== null) && !e.repeat) {
      // Escape stands back a step: out of a close-up, then to the room's
      // overview, then to the campus.
      if (cam.closeK > 0.3) {
        held = true;
        turned = false;
        cam.yaw = HOME_YAW;
        zoomTo(0);
        tellView();
        poke();
      } else overview();
    } else if (k === 'n' && !e.repeat) {
      e.preventDefault();
      opts.onNext();
    } else if ((k === 'e' || k === 'enter') && near && !e.repeat) {
      if (k === 'enter' && document.activeElement !== canvas) return;
      e.preventDefault();
      if (!isBoard(near)) opts.onActivate(near);
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
  canvas.addEventListener('pointercancel', onPointerUp);
  canvas.addEventListener('pointerleave', onPointerLeave);
  canvas.addEventListener('wheel', onWheel, { passive: false });
  // Over a label, too: they cover much of a close view, and a pinch over one
  // would zoom the page.
  markerLayer.addEventListener('wheel', onWheel, { passive: false });
  canvas.addEventListener('contextmenu', (e) => e.preventDefault());
  window.addEventListener('keydown', onKeyCapture, true);
  window.addEventListener('keydown', onKeyDown);
  window.addEventListener('keyup', onKeyUp);
  window.addEventListener('blur', onBlur);
  document.addEventListener('visibilitychange', onVisibility);
  // The sky moves on with the clock, even while you're away.

  return {
    apply,
    walkTo,
    overview,
    campus,
    setReducedMotion(on) {
      motion = !on;
      poke();
    },
    attach(next, nextOpts) {
      Object.assign(opts, nextOpts);
      ro.disconnect();
      host = next;
      host.appendChild(canvas);
      host.appendChild(markerLayer);
      attached = true;
      ro.observe(host);
      resize();
      // The new page hears where you are and which room, as the old one had,
      // and the keys are the room's again.
      opts.onNear(isBoard(near) ? 'board' : near);
      toldView = '';
      tellView();
      requestAnimationFrame(() => attached && canvas.focus({ preventScroll: true }));
      poke();
    },
    detach() {
      attached = false;
      keys.clear();
      cancelAnimationFrame(raf);
      window.clearTimeout(nap);
      nap = 0;
      running = false;
      ro.disconnect();
      canvas.remove();
      markerLayer.remove();
    },
    setInset(left) {
      inset = left;
      fitCache.key = '';
      poke();
    },
    dispose() {
      lastVisit = { pos: { ...pos }, facing, follow: followTarget, yaw: cam.yaw, dist: cam.dist, scope };
      disposed = true;
      cancelAnimationFrame(raf);
      window.clearTimeout(nap);
      ro.disconnect();
      window.removeEventListener('keydown', onKeyCapture, true);
      window.removeEventListener('keydown', onKeyDown);
      window.removeEventListener('keyup', onKeyUp);
      window.removeEventListener('blur', onBlur);
      document.removeEventListener('visibilitychange', onVisibility);
      for (const d of roomDispose) d();
      for (const d of desks.values()) for (const f of d.dispose) f();
      mePulse?.cancel();
      markerLayer.remove();
      me.dispose();
      for (const m of Object.values(mats)) (Array.isArray(m) ? m : [m]).forEach((x) => x.dispose());
      env.dispose();
      pmrem.dispose();
      ao.dispose();
      composer.dispose();
      renderer.dispose();
      canvas.remove();
    },
  };
}

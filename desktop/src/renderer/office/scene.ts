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
import { player } from './kenney.ts';
import { COUNTER, DESK, SEAT, layout as layOut, nearest, spotOf, walkable, type Box, type Layout } from './layout.ts';
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
  // attach puts the room, as it is, into a page again, with that page's
  // handlers; detach takes it out and stops drawing, keeping it built.
  attach(host: HTMLElement, opts: OfficeSceneOptions): void;
  detach(): void;
  dispose(): void;
};

// Where you were when you left the Office, so coming back from an agent's
// chat puts you back at its desk rather than at the door.
let lastVisit: { pos: P; facing: number; follow: number; yaw: number; dist: number } | undefined;

const RADIUS = 0.3;
const SPEED = 3.4; // metres a second, walking
const FOV = 24;
// The overview's 3/4 view: from the front right, over the open corner.
const HOME_YAW = 0.76;
const OVERVIEW_PITCH = 0.62;
// Following you, the camera looks down this steeply.
const FOLLOW_PITCH = 0.78;

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
          'position:absolute;left:0;top:0;display:none;pointer-events:auto;align-items:center;gap:6px;padding:5px 14px;border-radius:999px;color:#fff;font:700 13px/1.2 var(--font-sans,system-ui),sans-serif;box-shadow:0 3px 10px rgba(40,30,20,.22);cursor:pointer;white-space:nowrap;will-change:transform';
        const name = document.createElement('span');
        const sub = document.createElement('span');
        sub.style.cssText = 'font-weight:700;font-size:10.5px;padding:0 6px;border-radius:999px;background:rgba(255,255,255,.28)';
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
      // The pod's pill, in its project's colour, its waiting count beside.
      chip.style.background = `hsl(${area.hue} 58% 48%)`;
      (chip.firstChild as HTMLElement).textContent = label.name;
      (chip.lastChild as HTMLElement).textContent = label.count ? String(label.count) : '';
      (chip.lastChild as HTMLElement).style.display = label.count ? 'inline' : 'none';
      chip.title = label.sub;
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
  const FOLLOW_FAR = 19;
  const FOLLOW_NEAR = 10.5;
  const zoomTo = (z: number) => {
    zoomZ = Math.max(0, Math.min(1, z));
    followTarget = Math.min(1, zoomZ * 2);
    cam.dist = zoomZ <= 0.5 ? FOLLOW_FAR : FOLLOW_FAR - (zoomZ - 0.5) * 2 * (FOLLOW_FAR - FOLLOW_NEAR);
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
  // Where each project's pill went, and for which view.
  const pillAt = new WeakMap<HTMLElement, { key: string; at: number }>();
  let stillFor = 0;

  // Input.
  const keys = new Set<string>();
  let hover: string | undefined;
  let pointer: { x: number; y: number; id: number; dragged: boolean; button: number; aim: { ref?: string; row?: string; floor?: P } } | undefined;
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
    const l = lay;
    const W = l.room.x1;
    const D = l.room.z1;

    // The room: a diorama slab with its walls and features (decor.ts).
    const dio = diorama(l, 'AgentBox');
    roomDispose.push(() => dio.dispose());
    room.add(dio.group);
    sideWalls = [];

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
      // A whiteboard in a dark green frame, its columns of sticky notes.
      g.add(mesh(geo.roundTop, own(std('#2f4a3a', { roughness: 0.5 })), 0, 0, 0, bw + 0.16, bh + 0.16, 0.06, false));
      const face = mesh(geo.plane, own(new THREE.MeshBasicMaterial({ map: btex, toneMapped: false })), 0, 0, 0.045, bw, bh, 1, false);
      g.add(face);
      board.face = face;
      room.add(g);
      // A ledge below it, with markers.
      room.add(mesh(geo.roundTop, own(std('#2f4a3a')), l.board.x, 0.42, 0.1, bw * 0.6, 0.04, 0.14));
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
      // Each pod's soft pastel rug, in the project's colour, corners rounded.
      // The project's zone: a soft floor of its colour under all of it.
      {
        const zr = place.rug;
        const zh = area.hue / 360;
        const zoneMat = own(std(new THREE.Color().setHSL(zh, 0.45, 0.88), { roughness: 0.95 }));
        const zoneEdge = own(std(new THREE.Color().setHSL(zh, 0.4, 0.76), { roughness: 0.95 }));
        room.add(mesh(own(rug(zr.x1 - zr.x0, zr.z1 - zr.z0, 0.9, 0.008)), zoneEdge, (zr.x0 + zr.x1) / 2, 0, (zr.z0 + zr.z1) / 2, 1, 1, 1, false));
        room.add(mesh(own(rug(zr.x1 - zr.x0 - 0.24, zr.z1 - zr.z0 - 0.24, 0.8, 0.01)), zoneMat, (zr.x0 + zr.x1) / 2, 0, (zr.z0 + zr.z1) / 2, 1, 1, 1, false));
      }
      for (const [pi, pod] of place.pods.entries()) {
        // Each pod a shade of its project's colour, so pods read apart.
        // Each pod its own pastel, near its project's colour: pods read apart.
        const shade = [0, 38, -38, 76, -76, 19, -19][pi % 7];
        const hh = ((area.hue + shade + 360) % 360) / 360;
        const rugMat = own(std(new THREE.Color().setHSL(hh, 0.62, pi % 2 ? 0.68 : 0.73), { roughness: 0.95 }));
        const rugEdge = own(std(new THREE.Color().setHSL(hh, 0.52, 0.58), { roughness: 0.95 }));
        const pw = pod.x1 - pod.x0;
        const pd = pod.z1 - pod.z0;
        room.add(mesh(own(rug(pw, pd, 0.5, 0.02)), rugEdge, (pod.x0 + pod.x1) / 2, 0, (pod.z0 + pod.z1) / 2, 1, 1, 1, false));
        room.add(mesh(own(rug(pw - 0.16, pd - 0.16, 0.42, 0.024)), rugMat, (pod.x0 + pod.x1) / 2, 0, (pod.z0 + pod.z1) / 2, 1, 1, 1, false));
      }
      void r;

      // The lead's counter: a rounded wooden front desk, its front in the
      // project's colour.
      const { x: lx, z: lz } = place.lead;
      const counterFront = own(std(new THREE.Color().setHSL(area.hue / 360, 0.5, 0.62), { roughness: 0.6 }));
      room.add(mesh(geo.roundTop, mats.oak, lx, 1.0, lz, COUNTER.w + 0.1, 0.07, COUNTER.d + 0.1));
      room.add(mesh(geo.roundSoft, counterFront, lx, 0.5, lz + 0.05, COUNTER.w, 0.96, COUNTER.d - 0.1));
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
        room.add(mesh(geo.plane, pm, lx, 0.52, lz + 0.05 + (COUNTER.d - 0.1) / 2 + 0.006, COUNTER.w * 0.86, COUNTER.w * 0.86 * (160 / 512), 1, false));
      }
      addDesk(input, `${area.project}/lead`, lx, lz, area.hue, true);
      for (const d of place.desks) addDesk(input, d.id, d.x, d.z, area.hue, false, d.side);
    }
    for (const [i, p] of l.plants.entries()) room.add(plant(mats, p.x, p.z, i));

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
    seeThrough(scene);
    boxes = walkable(l, RADIUS);
    bounds = { x0: l.room.x0 + RADIUS, z0: l.room.z0 + RADIUS, x1: l.room.x1 - RADIUS, z1: l.room.z1 - RADIUS };
  }

  // addDesk puts an agent's desk at (x, z): the desk, its chair, the laptop
  // and the monitor facing you, the robot, its beacon, bubble, name tag and
  // pull request flag. A lead's is the counter, built above: here it gets
  // the robot and the rest.
  // variantOf is an agent's place among the agents of its tool, in the
  // room's order: what picks its accent, so neighbours don't match.
  let variants = new Map<string, number>();
  function variantOf(ref: string, ai: string | undefined): number {
    if (!variants.has(ref)) {
      variants.set(ref, variants.size);
      variantTool.set(ref, ai || 'claude');
    }
    return variants.get(ref)!;
  }
  const variantTool = new Map<string, string>();

  function addDesk(input: SceneInput, ref: string, x: number, z: number, hue: number, lead: boolean, side: -1 | 1 = -1) {
    const disp: (() => void)[] = [];
    const g = new THREE.Group();
    g.position.set(x, 0, z);
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
    tag.sprite.position.set(lead ? 0 : rx * 0.6, lead ? 0.45 : 0.25, lead ? 0.55 : DESK.d / 2 + 0.25);
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
    flag.add(mesh(geo.cyl, mats.pole, 0, 0.25, 0, 0.008, 0.5, 0.008));
    const cloth = mesh(geo.plane, flagMat, 0.1, 0.42, 0, 0.2, 0.13, 1);
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
          'pointer-events:auto;display:flex;align-items:center;gap:5px;max-width:var(--tag-w,170px);padding:2px 8px 2px 6px;border-radius:7px;background:#fff;color:#2b2620;font:600 11px/1.3 var(--font-sans,system-ui),sans-serif;white-space:nowrap;box-shadow:0 2px 6px rgba(40,30,20,.18);cursor:pointer';
        const dot = document.createElement('span');
        dot.style.cssText = 'flex:none;width:8px;height:8px;border-radius:50%';
        const label = document.createElement('span');
        label.style.cssText = 'overflow:hidden;text-overflow:ellipsis';
        walk.append(dot, label);
        const more = document.createElement('button');
        more.type = 'button';
        more.dataset.officeMore = '';
        more.style.cssText =
          'pointer-events:auto;display:none;padding:3px 9px;border-radius:999px;background:#fbbf24;border:1px solid rgba(0,0,0,.25);color:#1a1405;font:700 11px/1.25 var(--font-sans,system-ui),sans-serif;box-shadow:0 4px 14px rgba(0,0,0,.4);cursor:pointer;white-space:nowrap';
        // The "+n" opens the list of this chip's desk and the ones folded into it.
        const host = marker;
        more.addEventListener('pointerdown', (e) => {
          e.preventDefault();
          e.stopPropagation();
          const inside = [...markers].filter(([, el]) => el !== host && el.style.display === 'none' && chipInto.get(el) === host).map(([r]) => r);
          const box = more.getBoundingClientRect();
          const hostBox = host.getBoundingClientRect();
          opts.onMore([ref, ...inside], { x: box.left - hostBox.left, y: box.bottom - hostBox.top + 6 });
        });
        marker.append(walk, more);
        const ref = d.ref;
        // On press, not click: the camera may still be easing, and a chip that
        // moved before the release would lose the click to the floor.
        walk.addEventListener('pointerdown', (e) => {
          e.preventDefault();
          e.stopPropagation();
          walkTo(ref);
        });
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
  function lighting(force = false) {
    if (!force) return;
    const W = lay.room.x1;
    const D = lay.room.z1;
    sun.position.set(W / 2 + W * 0.45, 16, D / 2 + D * 0.55);
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

  function apply(input: SceneInput) {
    const key = input.model.areas.map((a) => `${a.project}:${a.label}:${a.hue}:${a.desks.map((d) => d.id).join(',')}`).join('|');
    if (key !== layKey) {
      layKey = key;
      lay = layOut(input.model);
      build(input);
      if (!placed && lastVisit) {
        Object.assign(pos, lastVisit.pos);
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
    zoomTo(Math.max(zoomZ, 0.5));
    started();
    poke();
  }

  function walkToPoint(p: P) {
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
  function overview() {
    zoomTo(0);
    zoomFocus = undefined;
    turned = false;
    cam.yaw = HOME_YAW;
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
      const pace = Math.min(18, Math.max(routeGoal ? 6 : SPEED, left / 0.6));
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
      const free = Math.max(0.5, 1 - 390 / Math.max(1, canvas.clientWidth));
      const pitch = 0.4;
      // Wide enough for you, standing at its left end, too.
      const dist = Math.max((lay.board.w + 0.5) / (free * 2 * tan * aspect), ((bh + 0.5) * Math.cos(pitch)) / (0.7 * 2 * tan));
      const vw = 2 * dist * tan * aspect;
      return { x: lay.board.x + (vw * (1 - free)) / 2, y: 0.45 + bh / 2, z: 0.1, dist, pitch, yaw: 0 };
    }
    for (const a of lay.areas) {
      if (ref === `${a.project}/lead`) return aside({ x: a.lead.x - 0.4, y: 1.1, z: a.lead.z - 0.2, dist: 12.5, pitch: 0.58, yaw: 0.3 }, aspect);
      const d = a.desks.find((x) => x.id === ref);
      if (d) return aside({ x: d.x + d.side * 0.35, y: 0.9, z: d.z - 0.3, dist: 13.5, pitch: 0.64, yaw: 0.4 }, aspect);
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
  function fitDistance(W: number, D: number, aspect: number, pitch: number): { dist: number; x: number; z: number } {
    const H = wallHeight(lay);
    const key = `${W.toFixed(2)}:${D.toFixed(2)}:${H}:${aspect.toFixed(3)}:${inset}:${HOME_YAW}`;
    if (fitCache.key === key) return fitCache;
    // The share of the width the overlay covers: the room sits in the middle
    // of what's left.
    const cover = Math.min(0.45, inset / Math.max(1, canvas.clientWidth));
    // The window the diorama fills, in clip space: nearly all of it, the
    // top bar's and the legend's clear parts over its corners and edges.
    const win = { x0: -0.98 + 2 * cover, x1: 0.99, y0: -0.86, y1: 0.9 };
    fitProbe.aspect = aspect;
    fitProbe.updateProjectionMatrix();
    // The diorama's outline: the slab's underside, the walls' tops where
    // there are walls (back and left), the floor's open corner.
    const corners = [
      ...[-0.3, W + 0.3].flatMap((x) => [-0.3, D + 0.3].map((z) => new THREE.Vector3(x, -0.72, z))),
      new THREE.Vector3(-0.3, H, -0.3),
      new THREE.Vector3(W + 0.1, H, -0.3),
      new THREE.Vector3(-0.3, H, D + 0.1),
    ];
    const v = new THREE.Vector3();
    const right = new THREE.Vector3(Math.cos(HOME_YAW), 0, -Math.sin(HOME_YAW));
    const back = new THREE.Vector3(-Math.sin(HOME_YAW), 0, -Math.cos(HOME_YAW));
    const bounds = (t: THREE.Vector3, d: number) => {
      fitProbe.position.set(t.x + Math.sin(HOME_YAW) * Math.cos(pitch) * d, Math.sin(pitch) * d, t.z + Math.cos(HOME_YAW) * Math.cos(pitch) * d);
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
    const t = new THREE.Vector3(W / 2, 0, D / 2);
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
    const W = lay.room.x1;
    const D = lay.room.z1;
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
    cam.closeK += (closeTarget - cam.closeK) * (motion ? 1 - Math.exp(-dt * 4.8) : 1);
    if (Math.abs(closeTarget - cam.closeK) < 1e-3) cam.closeK = closeTarget;

    // The overview fits the room, from the front and above.
    const oPitch = OVERVIEW_PITCH;
    const { dist: oDist, x: oX, z: oZ } = fitDistance(W, D, aspect, oPitch);
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
      const e = motion ? 1 - Math.exp(-dt * (held ? 9 : 6)) : 1;
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
    // As a speed, so it means the same at any frame rate.
    const moving = Math.abs(after - before) / Math.max(dt, 1e-3) > 0.6;
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
    let markHere = false;
    const crowded = desks.size > 16;
    // Where the agents' heads are on screen, which the projects' pills keep off.
    const heads: { x: number; y: number }[] = [];
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
      const k = base * (1 + Math.min(0.65, Math.max(0, (far - 12) / 36)) * (1 - cam.followK));
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
      if (lookAt && d.ref !== focusRef) {
        figPos.y += 1.0;
        const nearer = figPos.distanceTo(camera.position) < lookAt.dist - 0.8;
        figPos.project(camera);
        const sx = ((figPos.x + 1) / 2) * canvas.clientWidth;
        const sy = ((1 - figPos.y) / 2) * canvas.clientHeight;
        ahead = nearer && lookAt.spots.some((p) => Math.abs(sx - p.x) < 150 && Math.abs(sy - p.y) < 190);
      }
      const inWay = cam.closeK > 0.3 && d.ref !== focusRef && (d.root.position.distanceTo(camera.position) < 4.2 || ahead);
      d.root.visible = !(inWay && ahead);
      d.bubble.sprite.visible = !inWay && !!bubbleOf[d.tone];
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
      const grow = Math.min(d.tone === 'needs' ? 3.6 : loudTone ? 2.6 : crowded ? 1 : 1.7, Math.max(1, (far - 8) / 12));
      // A crowd's quiet bubbles show as you come in: from afar they'd bury the loud ones.
      if (crowded && !loudTone && far > 30) s.visible = false;
      const sb = (s.userData.base as THREE.Vector2 | undefined) ?? (s.userData.base = new THREE.Vector2(s.scale.x, s.scale.y));
      s.scale.set(sb.x * grow, sb.y * grow, 1);
      s.position.y = 1.95 + lift * 0.5 + (k / base - 1) * 1.25 + (grow - 1) * 0.2 + (motion && d.tone === 'needs' ? Math.abs(Math.sin(t * 3.5)) * 0.12 * grow : 0);
      if (d.ref === near) {
        atMark.sprite.visible = cam.closeK > 0.2 || cam.followK > 0.3;
        atMark.sprite.position.set(fx, s.position.y + (bubbleOf[d.tone] && s.visible ? 0.42 * grow : 0) + 0.05 + (motion ? Math.abs(Math.sin(t * 3)) * 0.08 : 0), fz);
        markHere = true;
      }
      // The desk you're at wears a soft white ring; a loud one keeps its own.
      const chosen = d.ref === near && !(d.tone === 'needs' || d.tone === 'error');
      d.ring.visible = chosen || d.tone === 'needs' || d.tone === 'error';
      (d.ring.material as THREE.MeshBasicMaterial).color.set(chosen ? '#ffffff' : toneColor[d.tone]);
      if (d.ring.visible) {
        const p = motion ? (t * 0.8) % 1 : 0.5;
        d.ring.scale.setScalar((0.9 + p * 0.5) * Math.min(1.8, grow));
        (d.ring.material as THREE.MeshBasicMaterial).opacity = 0.85 * (1 - p);
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
    // Signs give way to the camera, and to the board's close-up over them.
    for (const sg of signs) sg.visible = !(focusRef === 'board' && cam.closeK > 0.2) && sg.position.distanceTo(camera.position) > (cam.closeK > 0.3 ? 7 : 3.5);
    // From far off, each area's label floats over its sign. What the labels
    // and your chip take, the desks' chips keep clear of.
    const taken: { x0: number; x1: number; y0: number; y1: number }[] = [];
    const ap = new THREE.Vector3();
    for (const [i, place] of lay.areas.entries()) {
      const chip = areaChips.get(place.project);
      if (!chip) continue;
      const sg = signs[i];
      void sg;
      // A name plate by its zone: of a few places at its edges, the one over
      // the fewest heads (and pills), chosen as the camera comes to rest and
      // kept while it moves.
      const zr = place.rug;
      const zw = zr.x1 - zr.x0;
      // Before it first, then at its sides, inside it last.
      const spots: [number, number][] = [
        [(zr.x0 + zr.x1) / 2, zr.z1 + 0.75],
        [zr.x0 + Math.min(2.6, zw / 2), zr.z1 + 0.75],
        [zr.x1 - Math.min(2.6, zw / 2), zr.z1 + 0.75],
        [zr.x0 - 0.5, (zr.z0 + zr.z1) / 2],
        [zr.x1 + 0.5, (zr.z0 + zr.z1) / 2],
        [zr.x0 + Math.min(2.6, zw / 2), zr.z1 - 0.45],
        [zr.x1 - Math.min(2.6, zw / 2), zr.z1 - 0.45],
      ];
      const w0 = chip.offsetWidth || 140;
      const h0 = chip.offsetHeight || 30;
      const pkey = `${eased.tx.toFixed(1)}:${eased.tz.toFixed(1)}:${eased.dist.toFixed(1)}:${eased.yaw.toFixed(2)}:${heads.length}`;
      let pick0 = pillAt.get(chip);
      if (chipsShown && (!pick0 || pick0.key !== pkey)) {
        let best = 0;
        let bestN = Infinity;
        for (const [si, [sx, sz]] of spots.entries()) {
          ap.set(sx, 0.05, sz).project(camera);
          const px = ((ap.x + 1) / 2) * canvas.clientWidth;
          const py = ((1 - ap.y) / 2) * canvas.clientHeight;
          const off = ap.z >= 1 || Math.abs(ap.x) > 0.95 || py < 100 || py > canvas.clientHeight - 96;
          const covers = heads.filter((hd) => Math.abs(hd.x - px) < w0 / 2 + 14 && Math.abs(hd.y - py) < h0 / 2 + 22).length;
          const clash = taken.some((r) => px + w0 / 2 > r.x0 && px - w0 / 2 < r.x1 && py + h0 / 2 > r.y0 && py - h0 / 2 < r.y1) ? 5 : 0;
          const n = (off ? 100 : 0) + covers + clash + si * 0.3;
          if (n < bestN) {
            bestN = n;
            best = si;
          }
        }
        pick0 = { key: pkey, at: best };
        pillAt.set(chip, pick0);
      }
      const [px0, pz0] = spots[pick0?.at ?? 0];
      ap.set(px0, 0.05, pz0);
      const far = cam.closeK < 0.3;
      ap.project(camera);
      // Not under the top bar, whose 4.5rem it would hide beneath.
      // Nor over the legend and the hint along the bottom.
      const ay = ((1 - ap.y) / 2) * canvas.clientHeight;
      const on = far && ap.z < 1 && Math.abs(ap.x) < 1 && Math.abs(ap.y) < 1 && ay > 100 && ay < canvas.clientHeight - 96;
      chip.style.display = on ? 'flex' : 'none';
      if (on) {
        const cx = ((ap.x + 1) / 2) * canvas.clientWidth;
        const cy = ((1 - ap.y) / 2) * canvas.clientHeight;
        const cxIn = Math.min(canvas.clientWidth - 80, Math.max(80, cx));
        chip.style.transform = `translate(${cxIn}px, ${cy}px) translate(-50%, -50%)`;
        const w = chip.offsetWidth || 140;
        const h = chip.offsetHeight || 38;
        taken.push({ x0: cx - w / 2, x1: cx + w / 2, y0: cy - h / 2, y1: cy + h / 2 });
      }
      if (sg && on) sg.visible = false;
    }
    // You stand beside what you look at, never in front of it: always seen.
    me.fade(1);
    meTag.sprite.visible = cam.closeK < 0.3;
    // Name tags grow with distance to stay readable, up to a point; from far
    // off only the ones that need you stay, and up close they get out of the way.
    const tp = new THREE.Vector3();
    const shown: { el: HTMLDivElement; x: number; y: number; loud: boolean }[] = [];
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
    // A small team's tags show from anywhere, as name tags in a room do; a
    // big one's as you come near.
    const few = !crowded;
    // Small from afar, as name tags are; wider as you come in to read them.
    const tagW = cam.followK < 0.3 && cam.closeK < 0.3 ? '150px' : '170px';
    if (markerLayer.style.getPropertyValue('--tag-w') !== tagW) markerLayer.style.setProperty('--tag-w', tagW);
    for (const d of desks.values()) {
      const tag = d.tag.sprite;
      tag.getWorldPosition(tp);
      const dist = tp.distanceTo(camera.position);
      const marker = markers.get(d.ref);
      // The labels: the loud ones from anywhere, the rest only near enough
      // to mean something; none for the desk you're at (its card says it)
      // or one stepped aside for the close-up.
      const loud = marker?.dataset.loud === '1';
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
        const want = d.root.visible && !(d.ref === focusRef && cam.closeK > 0.3) && dist > 2.6 && (loud ? few || cam.followK > 0.2 : cam.followK > 0.4) && (loud || (cam.closeK < 0.3 && dist < 17));
        const onScreen = want && tp.z < 1 && Math.abs(tp.x) < 1.05 && Math.abs(tp.y) < 1.05 && ((1 - tp.y) / 2) * canvas.clientHeight > 110;
        const mx = ((tp.x + 1) / 2) * canvas.clientWidth;
        const my = Math.min(canvas.clientHeight - 70, ((1 - tp.y) / 2) * canvas.clientHeight);
        const underCard = (!!cardBox && mx > cardBox.x0 && mx < cardBox.x1 && my > cardBox.y0 && my < cardBox.y1) || (!!face && Math.abs(mx - face.x) < 120 && my > face.y - 70 && my < face.y + 150);
        const show = onScreen && !underCard;
        marker.style.display = show ? 'flex' : 'none';
        if (show) shown.push({ el: marker, x: mx, y: my, loud });
      }
    }
    // You, from far off: a chip in your colour over your head, or under your
    // feet when a label is there. Placed first: the desks' chips keep clear of it.
    meTag.sprite.getWorldPosition(tp);
    const meFar = tp.distanceTo(camera.position) > 15 && cam.closeK < 0.3 && cam.followK < 0.3;
    tp.project(camera);
    meMarker.style.display = meFar && tp.z < 1 ? 'block' : 'none';
    if (meFar) {
      const mx = ((tp.x + 1) / 2) * canvas.clientWidth;
      const my = ((1 - tp.y) / 2) * canvas.clientHeight;
      const mw = meMarker.offsetWidth || 44;
      const over = { x0: mx - mw / 2, x1: mx + mw / 2, y0: my - 40, y1: my - 18 };
      const under = taken.some((r) => over.x0 < r.x1 && over.x1 > r.x0 && over.y0 < r.y1 && over.y1 > r.y0);
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
    const placed: { x0: number; x1: number; y: number }[] = taken.flatMap((r) => {
      // As the anchors of chips that would cover it (a chip's box runs from
      // 31px above its anchor to 7 above), a row every 20px.
      const rows: { x0: number; x1: number; y: number }[] = [];
      for (let y = r.y1 + 31; y > r.y0 + 7; y -= 20) rows.push({ x0: r.x0, x1: r.x1, y });
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
      if (!off) {
        // No room: folded into the chip in its way, if there's one nearby;
        // else it stays where it belongs.
        // The nearest loud chip takes it.
        const near1 = chips.filter((c) => c.loud).sort((a, b) => Math.hypot((a.x0 + a.x1) / 2 - m.x, a.y - m.y) - Math.hypot((b.x0 + b.x1) / 2 - m.x, b.y - m.y))[0];
        const into = (restack ? undefined : chipInto.get(m.el)) ?? near1?.el;
        if (into) {
          chipInto.set(m.el, into);
          m.el.style.display = 'none';
          folded.set(into, (folded.get(into) ?? 0) + 1);
          continue;
        }
        off = [0, 0];
      }
      const x = inside(m.x + off[0]);
      const y = m.y + off[1];
      placed.push({ x0: x - w / 2, x1: x + w / 2, y });
      chips.push({ x0: x - w / 2, x1: x + w / 2, y, el: m.el, loud: m.loud });
      m.el.style.transform = `translate(${x}px, ${y}px) translate(-50%, -130%)`;
    }
    for (const c of chips) {
      const n = folded.get(c.el) ?? 0;
      const more = c.el.children[1] as HTMLElement;
      more.style.display = n ? 'inline' : 'none';
      if (n) more.textContent = opts.moreLabelShort(n);
    }
    const ringO = 0.55 + (motion ? Math.sin(t * 2.5) * 0.25 : 0.2);
    (meRing.material as THREE.MeshBasicMaterial).opacity = ringO;
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

  function pick(clientX: number, clientY: number): { ref?: string; row?: string; floor?: P } {
    const r = canvas.getBoundingClientRect();
    ndc.set(((clientX - r.left) / r.width) * 2 - 1, -((clientY - r.top) / r.height) * 2 + 1);
    ray.setFromCamera(ndc, camera);
    // Standing at the board, its rows come first: the desks in front of it
    // mustn't take a click meant for the board.
    if (near === 'board' && board?.face) {
      const f = ray.intersectObject(board.face, false)[0];
      if (f?.uv) {
        const { width: W, height: H } = board.ctx.canvas;
        const px = f.uv.x * W;
        const py = (1 - f.uv.y) * H;
        const row = board.rows.find((b) => px >= b.x && px <= b.x + b.w && py >= b.y && py <= b.y + b.h);
        return { ref: 'board', row: row?.ref };
      }
    }
    // Only what's shown: a hidden bubble or a desk stepped aside takes no click.
    const shownUp = (o: THREE.Object3D | null): boolean => !o || (o.visible && shownUp(o.parent));
    const hit = ray.intersectObjects(hits, false).find((h) => shownUp(h.object));
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
    pointer = { x: e.clientX, y: e.clientY, id: e.pointerId, dragged: false, button: e.button, aim: pick(e.clientX, e.clientY) };
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
        cam.yaw = Math.max(HOME_YAW - 0.7, Math.min(HOME_YAW + 0.5, cam.yaw - e.movementX * 0.0035));
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
      canvas.style.cursor = ref && !(ref === 'board' && near === 'board' && !row) ? 'pointer' : 'default';
      if (ref !== hover) {
        hover = ref;
        opts.onHover(ref);
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
    const { ref, row, floor } = p.aim;
    // While the camera is still gliding, a click on the room waits (what was
    // under it a moment ago isn't what will be), but a board row is a row.
    if (settledFor < 0.15 && !route.length) return;
    // Walking somewhere by name, or gliding into a close-up, a desk or a
    // board note under the pointer now isn't where it will be: wait.
    if ((ref || row) && ((route.length && routeGoal) || (cam.closeK > 0.05 && cam.closeK < 0.95))) return;
    // A row on the board opens what it's about, from wherever you see it.
    if (row && (near === 'board' || !row.startsWith('more:'))) opts.onBoardRow(row);
    else if (ref) {
      if (ref === near && !route.length) opts.onActivate(ref);
      else walkTo(ref);
    } else if (floor) walkToPoint(floor);
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
    // At a desk, a turn of the wheel outwards stands back from it.
    if (cam.closeK > 0.5 && n > 0) {
      held = true;
      zoomTo(0.5);
    } else {
      // Zooming keeps the floor under the pointer where it is on screen: the
      // camera's target moves towards it as the camera comes in. Out to the
      // overview, it lets go.
      const under = pick(e.clientX, e.clientY).floor;
      if (under && cam.closeK < 0.5) {
        const W = lay.room.x1;
        const D = lay.room.z1;
        const before = { x: eased.tx, z: eased.tz, d: eased.dist };
        const z1 = Math.max(0, Math.min(1, zoomZ - n * 0.07));
        const f1 = Math.min(1, z1 * 2);
        const { dist: oDist, x: cx, z: cz } = fitDistance(W, D, camera.aspect || 1, OVERVIEW_PITCH);
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
        if (f1 > 0.02) zoomFocus = { x: Math.min(W, Math.max(0, cx + (tx - cx) / f1)), z: Math.min(D, Math.max(0, cz + (tz - cz) / f1)) };
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
    if (e.ctrlKey || e.metaKey || e.altKey || editable(document.activeElement) || dialogAtKey || document.querySelector('[role="dialog"]')) return;
    const k = e.key.toLowerCase();
    if (walkKeys.has(k)) {
      // Arrows walk only while the office has focus, so they still scroll
      // and move through lists elsewhere.
      if (k.startsWith('arrow') && document.activeElement !== canvas && document.activeElement !== document.body) return;
      keys.add(k);
      e.preventDefault();
      poke();
    } else if (k === 'escape' && (near || followTarget > 0) && !e.repeat) {
      // Escape stands back a step: out of a close-up, then to the overview.
      if (cam.closeK > 0.3) {
        held = true;
        zoomTo(Math.min(zoomZ, 0.5));
        poke();
      } else overview();
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
      // The new page hears where you are, as the old one had.
      opts.onNear(near);
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
      lastVisit = { pos: { ...pos }, facing, follow: followTarget, yaw: cam.yaw, dist: cam.dist };
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

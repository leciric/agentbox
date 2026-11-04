// The Office's people, as rounded toys: soft plastic shading, chunky shapes,
// big eyes, squash and stretch.
//
// Every agent is Boxie, the AgentBox character, chibi: its head as big as
// the rest of it, the logo's cube rounded off in its violet-to-indigo
// gradient with white piping, a face screen with big eyes that change with
// its mood, and a tiny body with stubby limbs. One rig and one set of poses:
// typing, waving, celebrating, an error, asleep. Variety is in the details:
// the agent's colour (its body), something to wear (a hat, headphones, a
// bow…), and a badge on its chest for its AI tool, all from a stable hash.
//
// You are a chibi person: a big head, a stubby body, big eyes, a hoodie.
import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js';
import type { DeskTone } from './model.ts';

export type Look = { x: number; z: number } | undefined;

export type Figure = {
  group: THREE.Group;
  // Poses the figure for its state at time t (seconds). look is where you
  // stand, in the figure's own frame, when you're near enough to be looked at.
  pose(tone: DeskTone, t: number, motion: boolean, look?: Look): void;
  dispose(): void;
};

export type Tool = 'claude' | 'codex' | 'opencode' | 'cursor' | 'gemini';
export const toolOf = (ai: string | undefined): Tool => (ai === 'codex' || ai === 'opencode' || ai === 'cursor' || ai === 'gemini' ? ai : 'claude');

// hash is a stable number for a string: an agent is the same figure every time.
export function hash(s: string): number {
  let h = 2166136261;
  for (const ch of s) h = Math.imul(h ^ ch.charCodeAt(0), 16777619) >>> 0;
  return h >>> 0;
}
function rng(seed: number) {
  let x = seed || 1;
  return () => {
    x ^= x << 13;
    x ^= x >>> 17;
    x ^= x << 5;
    return ((x >>> 0) % 100000) / 100000;
  };
}

// The accents, bright against every mascot's own colours.
export const ACCENTS = ['#ff5d8f', '#3dd6c4', '#ffd23f', '#7b6cff', '#5ec2ff', '#ff8a3d', '#9be564', '#f15bb5'];
const ACCESSORIES = ['beanie', 'headphones', 'bow', 'cap', 'scarf', 'crown', 'sprout', 'none'] as const;
export type Accessory = (typeof ACCESSORIES)[number] | 'headset';

// accentOf is an agent's accent, from its ref: a colour and a thing to wear.
// variant, when given, is the agent's place in the room: then no two agents
// wear the same thing in the same colour until sixty-four of them do.
export function accentOf(ref: string, variant?: number): { color: string; accessory: Accessory; tint: number } {
  const r = rng(hash(ref));
  const a = r();
  const c = r();
  const tint = r() * 2 - 1;
  if (variant === undefined) return { color: ACCENTS[Math.floor(c * ACCENTS.length)], accessory: ACCESSORIES[Math.floor(a * ACCESSORIES.length)], tint };
  // Sixty-four pairings of a thing and a colour before any repeats.
  return { color: ACCENTS[(Math.floor(variant / ACCESSORIES.length) + variant * 3) % ACCENTS.length], accessory: ACCESSORIES[variant % ACCESSORIES.length], tint };
}

// Toy plastic: smooth, soft, a little sheen.
const toon = (color: THREE.ColorRepresentation, o: Partial<THREE.MeshStandardMaterialParameters> = {}) => new THREE.MeshStandardMaterial({ color, roughness: 0.48, metalness: 0, ...o });

// ease moves v towards target at rate per second, at any frame rate.
const ease = (v: number, target: number, rate: number, dt: number) => v + (target - v) * (1 - Math.exp(-rate * dt));
// bump rises and falls once, smoothly, over [0, 1].
const bump = (u: number) => (u <= 0 || u >= 1 ? 0 : Math.sin(Math.PI * u) ** 2);
const at = <T extends THREE.Object3D>(o: T, x: number, y: number, z: number): T => (o.position.set(x, y, z), o);

// bake merges the static meshes among parts that share a material into one
// mesh each, in root's frame: a desk or a body is a few draws, not dozens.
export function bake(root: THREE.Object3D, parts: THREE.Mesh[], castShadow = true): THREE.Mesh[] {
  root.updateMatrixWorld(true);
  const inv = new THREE.Matrix4().copy(root.matrixWorld).invert();
  const byMat = new Map<THREE.Material, THREE.BufferGeometry[]>();
  for (const m of parts) {
    const g0 = m.geometry.index ? m.geometry.toNonIndexed() : m.geometry.clone();
    g0.applyMatrix4(new THREE.Matrix4().multiplyMatrices(inv, m.matrixWorld));
    for (const name of Object.keys(g0.attributes)) if (name !== 'position' && name !== 'normal' && name !== 'uv' && name !== 'color') g0.deleteAttribute(name);
    if (!g0.attributes.uv) g0.setAttribute('uv', new THREE.Float32BufferAttribute(new Float32Array(g0.attributes.position.count * 2), 2));
    const list = byMat.get(m.material as THREE.Material) ?? [];
    list.push(g0);
    byMat.set(m.material as THREE.Material, list);
    m.parent?.remove(m);
  }
  const out: THREE.Mesh[] = [];
  for (const [mat, geos] of byMat) {
    // Merged geometries must agree: drop colours unless every part has them.
    if (geos.some((g) => !g.attributes.color)) for (const g of geos) if (g.attributes.color) g.deleteAttribute('color');
    const merged = mergeGeometries(geos, false)!;
    for (const g of geos) g.dispose();
    const mesh = new THREE.Mesh(merged, mat);
    mesh.castShadow = castShadow;
    mesh.receiveShadow = true;
    root.add(mesh);
    out.push(mesh);
  }
  return out;
}

// A mascot's parts, as its builder makes them: the body (which squashes, its
// pivot on the seat), its face, its two arms (pivots, raised by rotation.z,
// reaching forward by rotation.x), and how its eyes are drawn.
type Build = {
  body: THREE.Group;
  face: THREE.Group;
  arms: [THREE.Group, THREE.Group];
  eyes: 'pixel' | 'big' | 'screen';
  eyeGap: number;
  eyeSize: number;
  height: number;
  // Floats a little above the seat (the cube, the sparkle, the cloud).
  hover: number;
};

type Kit = { m: <T extends THREE.Material>(x: T) => T; tint: number; accent: string };

// The logo's colours: violet to indigo, white lines.
const LOGO = { light: new THREE.Color('#a78bfa'), mid: new THREE.Color('#7c3aed'), dark: new THREE.Color('#4f46e5') };

// gradient paints a geometry's vertex colours from the logo's violet at its
// top left to its indigo at the bottom right.
function gradient(g: THREE.BufferGeometry, size: number) {
  const pos = g.attributes.position;
  const col = new Float32Array(pos.count * 3);
  const c = new THREE.Color();
  for (let i = 0; i < pos.count; i++) {
    const u = THREE.MathUtils.clamp(0.5 + (pos.getX(i) - pos.getY(i) + pos.getZ(i) * 0.3) / (2.4 * size), 0, 1);
    if (u < 0.55) c.copy(LOGO.light).lerp(LOGO.mid, u / 0.55);
    else c.copy(LOGO.mid).lerp(LOGO.dark, (u - 0.55) / 0.45);
    col.set([c.r, c.g, c.b], i * 3);
  }
  g.setAttribute('color', new THREE.BufferAttribute(col, 3));
  return g;
}

// boxie is the AgentBox character, chibi: a head as big as the rest of it,
// the logo's cube rounded off, in its gradient with white piping along the
// edges, a face screen with big eyes; a tiny body in the agent's colour with
// stubby arms and legs. Seated, facing +z; its origin on the seat.
function boxie(k: Kit): Build {
  const body = new THREE.Group();
  const coat = k.m(toon(k.accent, { roughness: 0.55 }));
  const shoe = k.m(toon('#ffffff', { roughness: 0.4 }));
  // The body: a soft little bean, legs sticking forward off the seat.
  const torso = at(new THREE.Mesh(new THREE.CapsuleGeometry(0.15, 0.1, 8, 18), coat), 0, 0.2, 0);
  torso.scale.set(1.05, 1, 0.9);
  body.add(torso);
  for (const sx of [-1, 1]) {
    const leg = at(new THREE.Mesh(new THREE.CapsuleGeometry(0.055, 0.1, 6, 12), coat), sx * 0.07, 0.07, 0.11);
    leg.rotation.x = Math.PI / 2;
    body.add(leg);
    body.add(at(new THREE.Mesh(new THREE.SphereGeometry(0.065, 14, 10), shoe), sx * 0.07, 0.06, 0.2));
  }
  // The head: the logo's cube, rounded.
  const S = 0.46;
  const headG = gradient(new RoundedBoxGeometry(S, S * 0.92, S * 0.9, 6, 0.12), S);
  const head = at(new THREE.Mesh(headG, k.m(toon('#ffffff', { vertexColors: true, roughness: 0.35 }))), 0, 0.34 + S * 0.46, 0);
  body.add(head);
  // White piping along its edges, as the logo's line art.
  const pipe = k.m(new THREE.MeshStandardMaterial({ color: '#ffffff', roughness: 0.3 }));
  const hw = S / 2 - 0.035;
  const hh = (S * 0.92) / 2 - 0.035;
  const hd = (S * 0.9) / 2 - 0.035;
  const edge = (len: number, x: number, y: number, z: number, axis: 'x' | 'y' | 'z') => {
    const e = at(new THREE.Mesh(new THREE.CapsuleGeometry(0.009, len, 4, 8), pipe), x, y, z);
    if (axis === 'x') e.rotation.z = Math.PI / 2;
    if (axis === 'z') e.rotation.x = Math.PI / 2;
    head.add(e);
  };
  for (const y of [-hh, hh]) for (const z of [-hd, hd]) edge(S - 0.16, 0, y + Math.sign(y) * 0.03, z + Math.sign(z) * 0.03, 'x');
  for (const x of [-hw, hw]) for (const z of [-hd, hd]) edge(S * 0.92 - 0.16, x + Math.sign(x) * 0.03, 0, z + Math.sign(z) * 0.03, 'y');
  for (const x of [-hw, hw]) for (const y of [-hh, hh]) edge(S * 0.9 - 0.16, x + Math.sign(x) * 0.03, y + Math.sign(y) * 0.03, 0, 'z');
  // The face: a dark, glossy screen on the front.
  const screen = at(new THREE.Mesh(new RoundedBoxGeometry(S * 0.78, S * 0.6, 0.03, 4, 0.06), k.m(new THREE.MeshStandardMaterial({ color: '#1a1530', roughness: 0.2, metalness: 0.1, emissive: '#221a44', emissiveIntensity: 0.4 }))), 0, -0.01, S * 0.45);
  head.add(screen);
  const face = at(new THREE.Group(), 0, -0.005, S * 0.45 + 0.017);
  head.add(face);
  // Stubby arms with round hands.
  const arms = [-1, 1].map((sx) => {
    const p = at(new THREE.Group(), sx * 0.16, 0.27, 0.01);
    p.add(at(new THREE.Mesh(new THREE.CapsuleGeometry(0.045, 0.07, 6, 12), coat), 0, -0.06, 0));
    p.add(at(new THREE.Mesh(new THREE.SphereGeometry(0.052, 14, 10), shoe), 0, -0.13, 0));
    body.add(p);
    return p;
  }) as [THREE.Group, THREE.Group];
  // The status light and the hats sit on the head; its tool's badge on its chest.
  body.userData.chest = new THREE.Vector3(0.06, 0.24, 0.14);
  return { body, face, arms, eyes: 'screen', eyeGap: 0.075, eyeSize: 0.046, height: 0.34 + S * 0.92 + 0.02, hover: 0 };
}

// The badge's look for each tool: a colour and a glyph.
const BADGES: Record<Tool, { bg: string; fg: string; glyph: string }> = {
  claude: { bg: '#d97757', fg: '#ffffff', glyph: '✳' },
  codex: { bg: '#111318', fg: '#ffffff', glyph: '>_' },
  opencode: { bg: '#f2f2f2', fg: '#111318', glyph: '▣' },
  cursor: { bg: '#2b2f37', fg: '#ffffff', glyph: '◆' },
  gemini: { bg: '#5b7cf6', fg: '#ffffff', glyph: '✦' },
};
const badgeTextures = new Map<Tool, THREE.CanvasTexture>();
function badgeTexture(tool: Tool): THREE.CanvasTexture {
  let tex = badgeTextures.get(tool);
  if (tex) return tex;
  const c = document.createElement('canvas');
  c.width = c.height = 128;
  const g = c.getContext('2d')!;
  const b = BADGES[tool];
  g.fillStyle = '#ffffff';
  g.beginPath();
  g.arc(64, 64, 62, 0, Math.PI * 2);
  g.fill();
  g.fillStyle = b.bg;
  g.beginPath();
  g.arc(64, 64, 54, 0, Math.PI * 2);
  g.fill();
  g.fillStyle = b.fg;
  g.font = `800 ${b.glyph.length > 1 ? 50 : 66}px "Inter Variable", Inter, system-ui, sans-serif`;
  g.textAlign = 'center';
  g.textBaseline = 'middle';
  g.fillText(b.glyph, 64, 68);
  tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  badgeTextures.set(tool, tex);
  return tex;
}

// wear puts the agent's accent on its head (or round its neck).
function wear(k: Kit, b: Build, accessory: Accessory, color: string) {
  const fabric = k.m(toon(color));
  const top = at(new THREE.Group(), 0, b.height, 0);
  b.body.add(top);
  const add = (o: THREE.Mesh, line = true) => {
    top.add(o);
    void line;
    return o;
  };
  switch (accessory) {
    case 'beanie': {
      const hat = add(new THREE.Mesh(new THREE.SphereGeometry(0.15, 20, 10, 0, Math.PI * 2, 0, Math.PI / 2), fabric));
      hat.scale.set(1.2, 0.8, 1.1);
      add(at(new THREE.Mesh(new THREE.SphereGeometry(0.045, 12, 10), k.m(toon('#ffffff'))), 0, 0.13, 0));
      break;
    }
    case 'cap': {
      const crown = add(new THREE.Mesh(new THREE.SphereGeometry(0.15, 20, 10, 0, Math.PI * 2, 0, Math.PI / 2), fabric));
      crown.scale.set(1.15, 0.6, 1.1);
      add(at(new THREE.Mesh(new RoundedBoxGeometry(0.22, 0.02, 0.13, 2, 0.008), fabric), 0, 0.01, 0.17));
      break;
    }
    case 'headphones':
    case 'headset': {
      add(at(new THREE.Mesh(new THREE.TorusGeometry(0.22, 0.02, 8, 28, Math.PI), fabric), 0, -0.12, 0));
      for (const sx of [-1, 1]) add(at(new THREE.Mesh(new RoundedBoxGeometry(0.07, 0.13, 0.12, 2, 0.03), fabric), sx * 0.23, -0.14, 0));
      if (accessory === 'headset') {
        const boom = add(at(new THREE.Mesh(new THREE.CylinderGeometry(0.008, 0.008, 0.2, 6), k.m(toon('#333333'))), 0.2, -0.22, 0.1), false);
        boom.rotation.set(Math.PI / 2, 0, -0.6);
      }
      break;
    }
    case 'bow': {
      for (const s of [-1, 1]) {
        const loop = add(at(new THREE.Mesh(new THREE.ConeGeometry(0.06, 0.1, 12), fabric), s * 0.06, 0.03, 0));
        loop.rotation.z = (s * Math.PI) / 2;
      }
      add(at(new THREE.Mesh(new THREE.SphereGeometry(0.03, 10, 8), fabric), 0, 0.03, 0));
      top.position.x = 0.1;
      break;
    }
    case 'crown': {
      const gold = k.m(toon('#ffcf3f'));
      add(new THREE.Mesh(new THREE.CylinderGeometry(0.1, 0.11, 0.06, 12, 1, true), gold));
      for (let i = 0; i < 5; i++) {
        const a = (i / 5) * Math.PI * 2;
        add(at(new THREE.Mesh(new THREE.ConeGeometry(0.025, 0.06, 6), gold), Math.sin(a) * 0.1, 0.05, Math.cos(a) * 0.1), false);
      }
      add(at(new THREE.Mesh(new THREE.SphereGeometry(0.02, 8, 6), fabric), 0, 0.02, 0.105), false);
      break;
    }
    case 'sprout': {
      const leaf = k.m(toon('#5cc46b'));
      add(at(new THREE.Mesh(new THREE.CylinderGeometry(0.008, 0.01, 0.1, 6), leaf), 0, 0.05, 0), false);
      for (const s of [-1, 1]) {
        const l = add(at(new THREE.Mesh(new THREE.SphereGeometry(0.045, 12, 8), leaf), s * 0.045, 0.1, 0));
        l.scale.set(1, 0.4, 0.6);
        l.rotation.z = s * 0.5;
      }
      break;
    }
    case 'scarf': {
      top.position.y = 0.16;
      const scarf = add(new THREE.Mesh(new THREE.TorusGeometry(0.19, 0.035, 10, 28), fabric));
      scarf.rotation.x = Math.PI / 2;
      scarf.scale.set(1.15, 0.85, 1);
      const tail = add(at(new THREE.Mesh(new RoundedBoxGeometry(0.06, 0.14, 0.03, 2, 0.01), fabric), 0.1, -0.08, 0.2));
      tail.rotation.z = 0.2;
      break;
    }
  }
}

// The eyes: pixel blocks (Clawd's) or big ones, white with pupils that look
// about and a glint. Each has its open shape, a happy arc, a cross for an
// error, and closed for asleep.
type Eye = { open: THREE.Object3D; pupil?: THREE.Object3D; happy: THREE.Object3D; cross: THREE.Object3D; sleep: THREE.Object3D };
function makeEyes(k: Kit, b: Build): Eye[] {
  const inkM = k.m(new THREE.MeshBasicMaterial({ color: '#16131f' }));
  const white = k.m(new THREE.MeshBasicMaterial({ color: '#ffffff' }));
  const s = b.eyeSize;
  // On a dark face screen the moods are drawn in light.
  const lineM = b.eyes === 'screen' ? white : inkM;
  return [-1, 1].map((sx) => {
    const g = at(new THREE.Group(), sx * b.eyeGap, 0, 0);
    b.face.add(g);
    let open: THREE.Object3D;
    let pupil: THREE.Object3D | undefined;
    if (b.eyes === 'pixel') {
      open = new THREE.Mesh(new THREE.CapsuleGeometry(s * 0.38, s * 0.85, 6, 12), inkM);
      open.scale.z = 0.25;
    } else {
      const eye = new THREE.Group();
      const sclera = new THREE.Mesh(new THREE.CircleGeometry(s, 24), white);
      sclera.scale.y = 1.25;
      const ring = new THREE.Mesh(new THREE.RingGeometry(s, s * 1.18, 24), inkM);
      ring.scale.y = 1.25;
      pupil = at(new THREE.Group(), 0, 0, 0.002);
      const p = new THREE.Mesh(new THREE.CircleGeometry(s * 0.58, 20), inkM);
      p.scale.y = 1.2;
      pupil.add(p, at(new THREE.Mesh(new THREE.CircleGeometry(s * 0.2, 12), white), s * 0.2, s * 0.28, 0.002));
      eye.add(sclera, ring, pupil);
      open = eye;
    }
    const happy = new THREE.Mesh(new THREE.TorusGeometry(s * 0.75, s * 0.22, 6, 16, Math.PI), lineM);
    const cross = new THREE.Group();
    for (const a of [0.78, -0.78]) {
      const bar = new THREE.Mesh(new THREE.BoxGeometry(s * 1.6, s * 0.35, 0.01), lineM);
      bar.rotation.z = a;
      cross.add(bar);
    }
    const sleep = new THREE.Mesh(new THREE.TorusGeometry(s * 0.7, s * 0.18, 6, 16, Math.PI), lineM);
    sleep.rotation.z = Math.PI;
    g.add(open, happy, cross, sleep);
    return { open, pupil, happy, cross, sleep };
  });
}

type Pose = { lean: number; yaw: number; roll: number; squash: number; lift: number; lax: number; laz: number; rax: number; raz: number; eyeOpen: number; lookX: number; lookY: number };
const restPose = (): Pose => ({ lean: 0, yaw: 0, roll: 0, squash: 1, lift: 0, lax: 0, laz: -0.25, rax: 0, raz: 0.25, eyeOpen: 1, lookX: 0, lookY: 0 });
const toneColors: Record<DeskTone, string> = { needs: '#fbbf24', error: '#f87171', finished: '#34d399', working: '#60a5fa', idle: '#cbd5e1', stopped: '#4b5563' };

// mascot is an agent as its tool's character, seated, facing +z, its origin
// on the floor under its seat.
export function mascot(ref: string, ai: string | undefined, opts: { lead?: boolean; variant?: number } = {}): Figure {
  const tool = toolOf(ai);
  const acc = accentOf(ref, opts.variant);
  const r = rng(hash(`${ref}:life`));
  const mats: THREE.Material[] = [];
  const kit: Kit = { m: (x) => (mats.push(x), x), tint: acc.tint, accent: acc.color };
  const b = boxie(kit);
  wear(kit, b, opts.lead ? 'headset' : acc.accessory, acc.color);
  // Its AI tool's badge, on its chest: what tells a Claude Code agent from a Codex one.
  const badge = at(new THREE.Mesh(new THREE.CircleGeometry(0.045, 24), kit.m(new THREE.MeshStandardMaterial({ map: badgeTexture(tool), roughness: 0.4 }))), ...(b.body.userData.chest as THREE.Vector3).toArray());
  badge.rotation.x = -0.15;
  b.body.add(badge);
  const eyes = makeEyes(kit, b);
  // A blush, for when it's pleased or waving.
  const cheekM = kit.m(new THREE.MeshBasicMaterial({ color: '#ff7fa8', transparent: true, opacity: 0.6, depthWrite: false }));
  const cheekGeo = new THREE.CircleGeometry(b.eyeSize * 0.55, 14);
  const cheeks = [-1, 1].map((sx) => {
    const c = at(new THREE.Mesh(cheekGeo, cheekM), sx * (b.eyeGap + b.eyeSize * 1.1), -b.eyeSize * 1.5, 0.001);
    b.face.add(c);
    return c;
  });
  // Pixel faces get a pixel mouth: open when it calls you, a grin when pleased.
  const mouthM = kit.m(new THREE.MeshBasicMaterial({ color: '#16131f' }));
  const mouth = b.eyes === 'pixel' ? at(new THREE.Mesh(new THREE.BoxGeometry(b.eyeSize * 1.2, b.eyeSize * 0.5, 0.02), mouthM), 0, -b.eyeSize * 1.5, 0) : b.eyes === 'screen' ? at(new THREE.Mesh(new THREE.TorusGeometry(b.eyeSize * 0.45, b.eyeSize * 0.13, 6, 16, Math.PI), mouthM), 0, -b.eyeSize * 1.55, 0) : undefined;
  if (mouth && b.eyes === 'screen') {
    mouthM.color.set('#ffffff');
    mouth.rotation.z = Math.PI;
  }
  if (mouth) b.face.add(mouth);
  // The status light: a little glowing pip over its head.
  const pipM = kit.m(new THREE.MeshBasicMaterial({ color: '#60a5fa', toneMapped: false }));
  const pip = at(new THREE.Mesh(new THREE.SphereGeometry(0.035, 14, 10), pipM), 0, b.height + 0.22, 0);
  b.body.add(pip);

  const g = new THREE.Group();
  const seat = at(new THREE.Group(), 0, 0.49, 0);
  seat.add(b.body);
  g.add(seat);
  g.traverse((o) => {
    const mesh = o as THREE.Mesh;
    if (!mesh.isMesh) return;
    mesh.castShadow = !o.userData.ink && !(mesh.material instanceof THREE.MeshBasicMaterial);
    mesh.receiveShadow = false;
  });
  // Bigger than life: they're who you came to see.
  g.scale.setScalar(opts.lead ? 1.5 : 1.38);

  const cur = restPose();
  const T = restPose();
  let lastT: number | undefined;
  let blinkAt = 1 + r() * 3;
  let glance = { until: 0, x: 0, y: 0 };
  const phase = r() * 20;

  return {
    group: g,
    pose(tone, t0, motion, lookAt) {
      const dt = lastT === undefined ? 1 : Math.min(0.1, Math.max(0, t0 - lastT));
      lastT = t0;
      const t = motion ? t0 + phase : phase;
      Object.assign(T, restPose());
      let mood: 'open' | 'happy' | 'cross' | 'sleep' = 'open';
      const looking = !!lookAt && (tone === 'needs' || tone === 'idle' || tone === 'finished') && Math.hypot(lookAt.x, lookAt.z) < 4.5;
      switch (tone) {
        case 'working': {
          // Typing: arms forward, tapping in turn, a little squash with each
          // tap, eyes down at the keys.
          const tap = (o: number) => bump(((t * 3.4 + o) % 1) * 1.7);
          T.lax = -1.05 + tap(0) * 0.5;
          T.rax = -1.05 + tap(0.5) * 0.5;
          T.laz = -0.15;
          T.raz = 0.15;
          T.lean = 0.03;
          T.squash = 1 - (tap(0) + tap(0.5)) * 0.025;
          T.lookY = -0.4;
          T.yaw = Math.sin(t * 0.37) * 0.1;
          break;
        }
        case 'needs': {
          // Waving at you, hopping: stretched in the air, squashed landing.
          const hop = Math.abs(Math.sin(t * 3.6));
          T.raz = 2.6 + Math.sin(t * 8) * 0.6;
          T.rax = -0.2;
          T.laz = -0.5;
          T.lift = hop * 0.06;
          T.squash = 1 + (hop - 0.35) * 0.12;
          T.roll = Math.sin(t * 4) * 0.08;
          T.eyeOpen = 1.15;
          break;
        }
        case 'finished': {
          // A happy jump every few seconds, arms up; content between.
          const j = (t % 3.6) / 0.9;
          const up = bump(j);
          T.laz = -0.5 - up * 2.1;
          T.raz = 0.5 + up * 2.1;
          T.lift = up * 0.12;
          T.squash = j > 0 && j < 1 ? 1 + up * 0.15 - (j > 0.85 ? 0.12 : 0) : 1 + Math.sin(t * 2) * 0.01;
          T.lean = -0.06;
          mood = 'happy';
          break;
        }
        case 'error': {
          // Arms up to its head, shaking it now and then; squashed, glum.
          const shake = bump((t % 3.2) / 0.8) * Math.sin(t * 18) * 0.2;
          T.lax = -2.6;
          T.rax = -2.6;
          T.laz = -0.4;
          T.raz = 0.4;
          T.yaw = shake;
          T.lean = 0.15;
          T.squash = 0.93;
          mood = 'cross';
          break;
        }
        case 'idle': {
          // At rest: breathing, looking about now and then.
          const rr = rng(hash(`${ref}:${Math.floor(t / 3.2)}`));
          T.yaw = (rr() - 0.5) * 0.9;
          T.squash = 1 + Math.sin(t * 1.8) * 0.015;
          break;
        }
        case 'stopped': {
          T.lean = 0.1;
          T.roll = 0.1;
          T.squash = 0.97 + Math.sin(t * 0.8) * 0.02;
          T.laz = -0.1;
          T.raz = 0.1;
          mood = 'sleep';
          break;
        }
      }
      if (looking && lookAt) {
        // A turn of the body towards you, the eyes doing the rest, so its
        // face stays to the camera.
        // The eyes look at you; the body stays to the room, its face to the camera.
        const toward = Math.atan2(lookAt.x, lookAt.z);
        T.yaw = Math.max(-0.15, Math.min(0.15, toward * 0.2));
        T.lookX = Math.max(-1, Math.min(1, toward));
        T.lean = Math.min(T.lean, -0.04);
      }
      if (motion && t0 > blinkAt) blinkAt = t0 + 2.2 + r() * 3.5;
      const blinking = motion && mood === 'open' && blinkAt - t0 > 0 && blinkAt - t0 < 0.13;
      if (motion && t0 > glance.until) glance = { until: t0 + 0.8 + r() * 2.5, x: (r() - 0.5) * 1.4, y: (r() - 0.5) * 0.8 };
      if (motion && tone !== 'working' && !looking) {
        T.lookX += glance.x;
        T.lookY += glance.y;
      }
      for (const key of Object.keys(cur) as (keyof Pose)[]) cur[key] = motion ? ease(cur[key], T[key], key === 'squash' || key === 'lift' ? 16 : 10, dt) : T[key];

      // Squash and stretch keep its volume: thinner as it stretches.
      const sq = cur.squash;
      b.body.scale.set(1 / Math.sqrt(sq), sq, 1 / Math.sqrt(sq));
      b.body.position.y = b.hover + cur.lift + (motion && b.hover ? Math.sin(t * 2.2) * 0.015 : 0);
      b.body.rotation.set(cur.lean, cur.yaw, cur.roll, 'YXZ');
      b.arms[0].rotation.set(cur.lax, 0, cur.laz);
      b.arms[1].rotation.set(cur.rax, 0, cur.raz);

      const open = blinking ? 0.12 : cur.eyeOpen;
      for (const e of eyes) {
        e.open.visible = mood === 'open';
        e.happy.visible = mood === 'happy';
        e.cross.visible = mood === 'cross';
        e.sleep.visible = mood === 'sleep';
        e.open.scale.set(1, Math.max(0.1, open), 1);
        const lx = cur.lookX * b.eyeSize * 0.35;
        const ly = cur.lookY * b.eyeSize * 0.3;
        if (e.pupil) e.pupil.position.set(lx, ly, 0.002);
        else e.open.position.set(lx * 0.6, ly * 0.6, 0);
      }
      for (const c of cheeks) c.visible = mood === 'happy' || tone === 'needs';
      if (mouth) {
        mouth.visible = mood === 'happy' || tone === 'needs';
        mouth.scale.set(tone === 'needs' ? 0.7 : 1.4, tone === 'needs' ? 1.8 : 1, 1);
      }
      pipM.color.set(toneColors[tone]);
      pip.visible = tone !== 'stopped';
      pip.position.y = b.height + 0.22 + (motion ? Math.sin(t * 2.4) * 0.025 : 0);
      pip.scale.setScalar(tone === 'needs' && motion ? 1 + Math.abs(Math.sin(t0 * 6)) * 0.5 : 1);
    },
    dispose() {
      g.traverse((o) => (o as THREE.Mesh).isMesh && !o.userData.shared && (o as THREE.Mesh).geometry.dispose());
      for (const x of mats) x.dispose();
    },
  };
}

// walker is you: a chibi in a hoodie, standing, facing +z until you turn.
export type Walker = Figure & { stride(speed: number, t: number, motion: boolean): void; fade(opacity: number): void };

export function walker(): Walker {
  const hoodie = toon('#7c5cf0');
  const hood = toon('#5b3fd0');
  const pants = toon('#2c3550');
  const skin = toon('#ffd2b3');
  const hair = toon('#3b2a22');
  const shoe = toon('#ffffff');
  const inkM = new THREE.MeshBasicMaterial({ color: '#16131f' });
  const white = new THREE.MeshBasicMaterial({ color: '#ffffff' });
  const blush = new THREE.MeshBasicMaterial({ color: '#ff8fa3', transparent: true, opacity: 0.55, depthWrite: false });
  const mats: THREE.Material[] = [hoodie, hood, pants, skin, hair, shoe, inkM, white];
  // (Once outlined; the toy look needs no ink.)
  const line = (m: THREE.Mesh, _th?: number) => m;

  const g = new THREE.Group();
  // Squash and stretch happen on this, its pivot at the feet.
  const squash = new THREE.Group();
  g.add(squash);
  const legs = [-1, 1].map((sx) => {
    const p = at(new THREE.Group(), sx * 0.1, 0.3, 0);
    p.add(line(at(new THREE.Mesh(new THREE.CapsuleGeometry(0.075, 0.12, 6, 12), pants), 0, -0.13, 0)));
    p.add(line(at(new THREE.Mesh(new RoundedBoxGeometry(0.15, 0.1, 0.22, 3, 0.045), shoe), 0, -0.25, 0.03)));
    squash.add(p);
    return p;
  });
  // A stubby body, a hoodie with its hood behind.
  const torso = at(new THREE.Group(), 0, 0.3, 0);
  squash.add(torso);
  const body = line(at(new THREE.Mesh(new THREE.CapsuleGeometry(0.19, 0.14, 8, 16), hoodie), 0, 0.17, 0));
  body.scale.set(1.08, 1, 0.92);
  const hoodBack = line(at(new THREE.Mesh(new THREE.SphereGeometry(0.15, 16, 12), hood), 0, 0.3, -0.12));
  hoodBack.scale.set(1.1, 0.7, 0.6);
  torso.add(body, hoodBack, at(new THREE.Mesh(new RoundedBoxGeometry(0.18, 0.08, 0.04, 2, 0.02), hood), 0, 0.1, 0.17));
  // The big head, with a hair cap and tuft, big eyes, brows, a smile.
  const head = at(new THREE.Group(), 0, 0.62, 0);
  torso.add(head);
  const skull = line(new THREE.Mesh(new THREE.SphereGeometry(0.27, 32, 24), skin), 0.016);
  skull.scale.set(1.06, 0.98, 1);
  const cap = line(at(new THREE.Mesh(new THREE.SphereGeometry(0.285, 32, 16, 0, Math.PI * 2, 0, Math.PI * 0.52), hair), 0, 0.02, -0.02), 0.014);
  cap.rotation.x = -0.4;
  const tuft = line(at(new THREE.Mesh(new THREE.ConeGeometry(0.07, 0.16, 10), hair), 0.04, 0.28, 0.02), 0.01);
  tuft.rotation.z = -0.5;
  const fringe = at(new THREE.Mesh(new THREE.SphereGeometry(0.12, 16, 10), hair), 0.08, 0.15, 0.18);
  fringe.scale.set(1.5, 0.5, 0.6);
  fringe.rotation.z = -0.3;
  head.add(skull, cap, tuft, fringe);
  for (const sx of [-1, 1]) {
    const ear = line(at(new THREE.Mesh(new THREE.SphereGeometry(0.06, 12, 10), skin), sx * 0.285, -0.02, 0), 0.01);
    ear.scale.set(0.5, 1, 0.8);
    head.add(ear);
  }
  const eyes = [-1, 1].map((sx) => {
    const e = at(new THREE.Group(), sx * 0.1, 0.0, 0.258);
    e.rotation.y = sx * 0.2;
    // Big black eyes with two catchlights, the toy look's.
    const sclera = new THREE.Mesh(new THREE.CircleGeometry(0.05, 24), inkM);
    sclera.scale.y = 1.35;
    const pupil = at(new THREE.Group(), 0, 0, 0.002);
    pupil.add(at(new THREE.Mesh(new THREE.CircleGeometry(0.017, 12), white), 0.016, 0.026, 0.001), at(new THREE.Mesh(new THREE.CircleGeometry(0.008, 8), white), -0.014, -0.02, 0.001));
    const brow = at(new THREE.Mesh(new RoundedBoxGeometry(0.075, 0.016, 0.01, 2, 0.006), hair), 0, 0.1, 0);
    brow.rotation.z = sx * -0.15;
    e.add(sclera, pupil, brow);
    head.add(e);
    const cheek = at(new THREE.Mesh(new THREE.CircleGeometry(0.035, 16), blush), sx * 0.17, -0.09, 0.225);
    cheek.rotation.y = sx * 0.5;
    head.add(cheek);
    return { sclera, pupil };
  });
  const smile = at(new THREE.Mesh(new THREE.TorusGeometry(0.035, 0.009, 6, 16, Math.PI), inkM), 0, -0.1, 0.262);
  smile.rotation.z = Math.PI;
  head.add(smile);
  // Short arms with round hands.
  const arms = [-1, 1].map((sx) => {
    const sh = at(new THREE.Group(), sx * 0.2, 0.26, 0);
    sh.add(line(at(new THREE.Mesh(new THREE.CapsuleGeometry(0.06, 0.12, 6, 12), hoodie), 0, -0.1, 0), 0.012));
    sh.add(line(at(new THREE.Mesh(new THREE.SphereGeometry(0.06, 14, 10), skin), 0, -0.2, 0), 0.01));
    sh.rotation.z = sx * 0.35;
    torso.add(sh);
    return { sh, sx };
  });
  g.traverse((o) => {
    const mesh = o as THREE.Mesh;
    if (mesh.isMesh) mesh.castShadow = !o.userData.ink && !(mesh.material instanceof THREE.MeshBasicMaterial);
  });

  let gait = 0;
  let lastT: number | undefined;
  let k = 0;
  let headLag = 0;
  let blinkAt = 2;
  return {
    group: g,
    pose() {},
    stride(speed, t, motion) {
      const dt = lastT === undefined ? 0 : Math.min(0.1, Math.max(0, t - lastT));
      lastT = t;
      k = motion ? ease(k, Math.min(1, speed / 2.6), 8, dt) : 0;
      gait += dt * (5 + speed * 2.6);
      const s = Math.sin(gait);
      legs[0].rotation.x = s * 0.7 * k;
      legs[1].rotation.x = -s * 0.7 * k;
      for (const a of arms) a.sh.rotation.set(a.sx === -1 ? s * 0.9 * k : -s * 0.9 * k, 0, a.sx * (0.35 + 0.15 * k));
      // Squash on each footfall, stretch between: volume kept.
      const st = motion ? 1 + Math.cos(gait * 2) * 0.07 * k + Math.sin(t * 2.4) * 0.015 * (1 - k) : 1;
      squash.scale.set(1 / Math.sqrt(st), st, 1 / Math.sqrt(st));
      squash.position.y = Math.max(0, Math.cos(gait * 2)) * 0.05 * k;
      torso.rotation.set(0.06 * k, s * 0.1 * k, 0);
      // The big head lags the body a little, and looks about at rest.
      headLag = ease(headLag, -s * 0.12 * k, 10, dt);
      head.rotation.set(motion ? Math.sin(t * 0.7) * 0.04 * (1 - k) : 0, (motion ? Math.sin(t * 0.33) * 0.35 * (1 - k) : 0) + headLag, headLag * 0.5);
      if (motion && t > blinkAt) blinkAt = t + 2.4 + ((t * 7.3) % 3);
      const blink = motion && blinkAt - t > 0 && blinkAt - t < 0.12;
      for (const e of eyes) {
        e.sclera.scale.y = blink ? 0.12 : 1.35;
        e.pupil.visible = !blink;
      }
    },
    fade(opacity) {
      g.visible = opacity > 0.2;
      for (const x of mats) {
        const see = opacity < 1;
        if (x.transparent !== see) {
          x.transparent = see;
          x.needsUpdate = true;
        }
        x.opacity = opacity;
        x.depthWrite = !see;
      }
    },
    dispose() {
      g.traverse((o) => (o as THREE.Mesh).isMesh && (o as THREE.Mesh).geometry.dispose());
      for (const x of [...mats, blush]) x.dispose();
    },
  };
}

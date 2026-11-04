// The Office's people, cartoon style: toon shading in a few flat steps, ink
// outlines from inverted hulls, squash and stretch.
//
// Each agent is its AI tool's mascot, built here from simple shapes, with no
// image files: Claude Code's Clawd, the orange pixel critter, as a voxel
// figure; Cursor a shaded cube; Gemini a gradient sparkle; OpenCode a framed
// block; Codex a little cloud with a terminal prompt for a mouth. They share
// one rig (a body that squashes, two arms, a face whose eyes change shape
// with its mood) and one set of poses: typing, waving, celebrating, an error,
// asleep. A per-agent accent (a hat, headphones, a bow, a scarf, in a colour
// of its own, from a stable hash of its ref) tells two Clawds apart.
//
// You are a chibi person: a big head, a stubby body, big eyes, a hoodie.
import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';
import { mergeGeometries, mergeVertices } from 'three/addons/utils/BufferGeometryUtils.js';
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

// The toon ramp: four flat steps of light.
let ramp: THREE.DataTexture | undefined;
function toonRamp(): THREE.DataTexture {
  if (ramp) return ramp;
  const data = new Uint8Array([70, 150, 215, 255]);
  ramp = new THREE.DataTexture(data, data.length, 1, THREE.RedFormat);
  ramp.minFilter = ramp.magFilter = THREE.NearestFilter;
  ramp.generateMipmaps = false;
  ramp.needsUpdate = true;
  return ramp;
}
const toon = (color: THREE.ColorRepresentation, o: Partial<THREE.MeshToonMaterialParameters> = {}) => new THREE.MeshToonMaterial({ color, gradientMap: toonRamp(), ...o });

// The ink: an inverted hull, the mesh's own shape pushed out along its
// smoothed normals and drawn from behind, so its rim shows as a line.
const inks = new Map<number, THREE.ShaderMaterial>();
function inkMaterial(thickness: number): THREE.ShaderMaterial {
  const key = Math.round(thickness * 1000);
  let m = inks.get(key);
  if (!m) {
    m = new THREE.ShaderMaterial({
      uniforms: { thickness: { value: thickness }, color: { value: new THREE.Color('#1d1a2b') } },
      vertexShader: 'uniform float thickness; void main() { vec3 p = position + normal * thickness; gl_Position = projectionMatrix * modelViewMatrix * vec4(p, 1.0); }',
      fragmentShader: 'uniform vec3 color; void main() { gl_FragColor = vec4(color, 1.0); }',
      side: THREE.BackSide,
    });
    inks.set(key, m);
  }
  return m;
}
export function ink(mesh: THREE.Mesh, thickness = 0.012): THREE.Mesh {
  let g = new THREE.BufferGeometry();
  g.setAttribute('position', mesh.geometry.getAttribute('position').clone());
  if (mesh.geometry.index) g.setIndex(mesh.geometry.index.clone());
  g = mergeVertices(g, 1e-3);
  g.computeVertexNormals();
  const hull = new THREE.Mesh(g, inkMaterial(thickness));
  hull.position.copy(mesh.position);
  hull.rotation.copy(mesh.rotation);
  hull.scale.copy(mesh.scale);
  hull.userData.ink = true;
  mesh.parent?.add(hull);
  return hull;
}

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
  eyes: 'pixel' | 'big';
  eyeGap: number;
  eyeSize: number;
  height: number;
  // Floats a little above the seat (the cube, the sparkle, the cloud).
  hover: number;
};

type Kit = { m: <T extends THREE.Material>(x: T) => T; tint: number };

// clawd: Claude Code's critter, in voxels: a wide orange block, two tall
// black eyes, nubs for arms, four little legs.
function clawd(k: Kit): Build {
  const V = 0.07;
  // Clawd's orange, nudged per agent: warmer, paler, deeper.
  const base = new THREE.Color('#d97757').offsetHSL(k.tint * 0.025, k.tint * 0.06, k.tint * 0.05);
  const orange = k.m(toon(base));
  const shade = k.m(toon(base.clone().offsetHSL(0, 0, -0.08)));
  const body = new THREE.Group();
  const cube = new THREE.BoxGeometry(V, V, V);
  const W = 10;
  const H = 5;
  const D = 5;
  const parts: THREE.Mesh[] = [];
  // Only the shell's voxels; a few a shade darker, the way pixel art is shaded.
  for (let x = 0; x < W; x++)
    for (let y = 0; y < H; y++)
      for (let z = 0; z < D; z++) {
        if (x > 0 && x < W - 1 && y > 0 && y < H - 1 && z > 0 && z < D - 1) continue;
        const dark = y === 0 || (x + y * 3 + z * 7) % 11 === 0;
        const v = at(new THREE.Mesh(cube, dark ? shade : orange), (x - (W - 1) / 2) * V, 0.13 + y * V, (z - (D - 1) / 2) * V);
        body.add(v);
        parts.push(v);
      }
  // Legs: four columns under it, two voxels tall.
  for (const lx of [-3.5, -1.5, 1.5, 3.5])
    for (const ly of [0, 1]) {
      const v = at(new THREE.Mesh(cube, shade), lx * V, 0.13 - (ly + 1) * V, (D / 2 - 0.5) * V);
      body.add(v);
      parts.push(v);
    }
  bake(body, parts);
  cube.dispose();
  // Ink round the block, as one simple hull.
  const hullBox = at(new THREE.Mesh(new RoundedBoxGeometry(W * V, H * V, D * V, 2, 0.01)), 0, 0.13 + ((H - 1) / 2) * V, 0);
  body.add(hullBox);
  ink(hullBox, 0.018);
  body.remove(hullBox);
  hullBox.geometry.dispose();
  const face = at(new THREE.Group(), 0, 0.13 + 3 * V, (D / 2) * V + 0.002);
  body.add(face);
  const arms = [-1, 1].map((sx) => {
    const p = at(new THREE.Group(), sx * (W / 2) * V, 0.13 + 2 * V, 0);
    const nub = at(new THREE.Mesh(new THREE.BoxGeometry(V * 1.5, V, V), orange), sx * V * 0.75, -V * 0.3, 0);
    p.add(nub);
    ink(nub, 0.01);
    body.add(p);
    return p;
  }) as [THREE.Group, THREE.Group];
  return { body, face, arms, eyes: 'pixel', eyeGap: 0.12, eyeSize: V, height: 0.13 + H * V, hover: 0 };
}

// cube: Cursor's mark, a cube turned to show three faces, light, mid and dark.
function cube(k: Kit): Build {
  const body = new THREE.Group();
  const faces = [k.m(toon('#5e6470')), k.m(toon('#2a2e36')), k.m(toon('#eef0f3')), k.m(toon('#1c1f25')), k.m(toon('#3b4049')), k.m(toon('#1c1f25'))];
  const c = at(new THREE.Mesh(new RoundedBoxGeometry(0.4, 0.4, 0.4, 3, 0.05), faces), 0, 0.36, 0);
  c.rotation.set(-0.5, Math.PI / 4, 0, 'YXZ');
  body.add(c);
  ink(c, 0.018);
  // The face floats on the front corner, where you see it.
  const face = at(new THREE.Group(), 0, 0.37, 0.3);
  body.add(face);
  const dark = k.m(toon('#2a2e36'));
  const arms = [-1, 1].map((sx) => {
    const p = at(new THREE.Group(), sx * 0.24, 0.3, 0.05);
    const nub = at(new THREE.Mesh(new THREE.SphereGeometry(0.05, 14, 10), dark), 0, -0.07, 0);
    p.add(nub);
    ink(nub, 0.01);
    body.add(p);
    return p;
  }) as [THREE.Group, THREE.Group];
  return { body, face, arms, eyes: 'big', eyeGap: 0.075, eyeSize: 0.05, height: 0.66, hover: 0.04 };
}

// sparkle: Gemini's four-pointed star, puffy, blue through violet to rose.
function sparkle(k: Kit): Build {
  const body = new THREE.Group();
  const shape = new THREE.Shape();
  const R = 0.32;
  const inner = 0.07;
  shape.moveTo(0, R);
  shape.quadraticCurveTo(inner, inner, R, 0);
  shape.quadraticCurveTo(inner, -inner, 0, -R);
  shape.quadraticCurveTo(-inner, -inner, -R, 0);
  shape.quadraticCurveTo(-inner, inner, 0, R);
  const g = new THREE.ExtrudeGeometry(shape, { depth: 0.08, bevelEnabled: true, bevelThickness: 0.05, bevelSize: 0.035, bevelSegments: 4, curveSegments: 18 });
  g.center();
  // The gradient, in vertex colours: blue at the top left to rose below.
  const pos = g.attributes.position;
  const col = new Float32Array(pos.count * 3);
  const a = new THREE.Color('#4f8ff7');
  const b = new THREE.Color('#9b72cb');
  const c2 = new THREE.Color('#e0697a');
  const tmp = new THREE.Color();
  for (let i = 0; i < pos.count; i++) {
    const u = THREE.MathUtils.clamp(0.5 - (pos.getY(i) - pos.getX(i) * 0.6) / (2 * R), 0, 1);
    if (u < 0.5) tmp.copy(a).lerp(b, u * 2);
    else tmp.copy(b).lerp(c2, (u - 0.5) * 2);
    col.set([tmp.r, tmp.g, tmp.b], i * 3);
  }
  g.setAttribute('color', new THREE.BufferAttribute(col, 3));
  const star = at(new THREE.Mesh(g, k.m(toon('#ffffff', { vertexColors: true }))), 0, 0.42, 0);
  body.add(star);
  ink(star, 0.016);
  const face = at(new THREE.Group(), 0, 0.42, 0.095);
  body.add(face);
  const hand = k.m(toon('#7f86e8'));
  const arms = [-1, 1].map((sx) => {
    const p = at(new THREE.Group(), sx * 0.15, 0.36, 0.06);
    const nub = at(new THREE.Mesh(new THREE.SphereGeometry(0.045, 14, 10), hand), 0, -0.06, 0);
    p.add(nub);
    ink(nub, 0.01);
    body.add(p);
    return p;
  }) as [THREE.Group, THREE.Group];
  return { body, face, arms, eyes: 'big', eyeGap: 0.07, eyeSize: 0.045, height: 0.74, hover: 0.05 };
}

// block: OpenCode's mark, a square frame block with a dark face inside.
function block(k: Kit): Build {
  const body = new THREE.Group();
  const light = k.m(toon('#ededed'));
  const dark = k.m(toon('#17181c'));
  const frame = at(new THREE.Mesh(new RoundedBoxGeometry(0.46, 0.44, 0.3, 3, 0.04), light), 0, 0.36, 0);
  body.add(frame);
  ink(frame, 0.018);
  body.add(at(new THREE.Mesh(new RoundedBoxGeometry(0.32, 0.28, 0.05, 2, 0.02), dark), 0, 0.36, 0.14));
  for (const sx of [-1, 1]) {
    const f = at(new THREE.Mesh(new RoundedBoxGeometry(0.12, 0.08, 0.14, 2, 0.02), dark), sx * 0.12, 0.12, 0.06);
    body.add(f);
    ink(f, 0.01);
  }
  const face = at(new THREE.Group(), 0, 0.37, 0.168);
  body.add(face);
  const arms = [-1, 1].map((sx) => {
    const p = at(new THREE.Group(), sx * 0.24, 0.34, 0);
    const nub = at(new THREE.Mesh(new RoundedBoxGeometry(0.08, 0.13, 0.08, 2, 0.02), light), 0, -0.06, 0);
    p.add(nub);
    ink(nub, 0.01);
    body.add(p);
    return p;
  }) as [THREE.Group, THREE.Group];
  return { body, face, arms, eyes: 'big', eyeGap: 0.075, eyeSize: 0.045, height: 0.6, hover: 0 };
}

// cloud: Codex, a soft cloud with a terminal prompt, >_, for a mouth.
function cloud(k: Kit): Build {
  const body = new THREE.Group();
  const white = k.m(toon('#f4f6fb'));
  const puffs: [number, number, number, number][] = [
    [0, 0.36, 0, 0.2],
    [-0.17, 0.3, 0.01, 0.15],
    [0.17, 0.3, 0.01, 0.15],
    [-0.09, 0.47, -0.01, 0.15],
    [0.1, 0.48, -0.01, 0.14],
    [0, 0.25, 0.03, 0.16],
  ];
  const geos = puffs.map(([x, y, z, r]) => new THREE.SphereGeometry(r, 20, 14).translate(x, y, z));
  const merged = mergeGeometries(geos, false)!;
  for (const g of geos) g.dispose();
  const puff = new THREE.Mesh(merged, white);
  body.add(puff);
  ink(puff, 0.016);
  const face = at(new THREE.Group(), 0, 0.43, 0.2);
  body.add(face);
  // The prompt, below the eyes.
  const glyph = k.m(new THREE.MeshBasicMaterial({ color: '#1d2433' }));
  // Bold, on a little dark screen, so it reads from across the room.
  const prompt = at(new THREE.Group(), 0, -0.085, 0.01);
  prompt.add(at(new THREE.Mesh(new RoundedBoxGeometry(0.15, 0.075, 0.012, 2, 0.015), glyph), 0, 0, -0.004));
  const fg = k.m(new THREE.MeshBasicMaterial({ color: '#7ef0a8' }));
  for (const s of [1, -1]) {
    const bar = at(new THREE.Mesh(new THREE.BoxGeometry(0.04, 0.014, 0.01), fg), -0.035, s * 0.012, 0.004);
    bar.rotation.z = -s * 0.65;
    prompt.add(bar);
  }
  prompt.add(at(new THREE.Mesh(new THREE.BoxGeometry(0.045, 0.014, 0.01), fg), 0.025, -0.02, 0.004));
  face.add(prompt);
  const arms = [-1, 1].map((sx) => {
    const p = at(new THREE.Group(), sx * 0.27, 0.3, 0.03);
    const nub = at(new THREE.Mesh(new THREE.SphereGeometry(0.055, 14, 10), white), 0, -0.05, 0);
    p.add(nub);
    ink(nub, 0.01);
    body.add(p);
    return p;
  }) as [THREE.Group, THREE.Group];
  return { body, face, arms, eyes: 'big', eyeGap: 0.075, eyeSize: 0.045, height: 0.66, hover: 0.05 };
}

const builders: Record<Tool, (k: Kit) => Build> = { claude: clawd, cursor: cube, gemini: sparkle, opencode: block, codex: cloud };

// wear puts the agent's accent on its head (or round its neck).
function wear(k: Kit, b: Build, accessory: Accessory, color: string) {
  const fabric = k.m(toon(color));
  const top = at(new THREE.Group(), 0, b.height, 0);
  b.body.add(top);
  const add = (o: THREE.Mesh, line = true) => {
    top.add(o);
    if (line) ink(o, 0.008);
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
  return [-1, 1].map((sx) => {
    const g = at(new THREE.Group(), sx * b.eyeGap, 0, 0);
    b.face.add(g);
    let open: THREE.Object3D;
    let pupil: THREE.Object3D | undefined;
    if (b.eyes === 'pixel') {
      open = new THREE.Mesh(new THREE.BoxGeometry(s * 0.8, s * 1.6, 0.02), inkM);
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
    const happy = new THREE.Mesh(new THREE.TorusGeometry(s * 0.75, s * 0.22, 6, 16, Math.PI), inkM);
    const cross = new THREE.Group();
    for (const a of [0.78, -0.78]) {
      const bar = new THREE.Mesh(new THREE.BoxGeometry(s * 1.6, s * 0.35, 0.01), inkM);
      bar.rotation.z = a;
      cross.add(bar);
    }
    const sleep = new THREE.Mesh(new THREE.TorusGeometry(s * 0.7, s * 0.18, 6, 16, Math.PI), inkM);
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
  const kit: Kit = { m: (x) => (mats.push(x), x), tint: acc.tint };
  const b = builders[tool](kit);
  // The sparkle's top point and the cloud's prompt stay clear: they wear a
  // bow or a scarf rather than anything on top.
  const onTop = acc.accessory === 'beanie' || acc.accessory === 'cap' || acc.accessory === 'crown' || acc.accessory === 'sprout' || acc.accessory === 'headphones';
  const worn: Accessory = opts.lead ? 'headset' : (tool === 'gemini' || tool === 'codex') && onTop ? (hash(ref) % 2 ? 'bow' : 'scarf') : acc.accessory;
  wear(kit, b, worn, acc.color);
  const eyes = makeEyes(kit, b);
  // A blush, for when it's pleased or waving.
  const cheekM = kit.m(new THREE.MeshBasicMaterial({ color: '#ff7fa8', transparent: true, opacity: 0.6, depthWrite: false }));
  const cheekGeo = new THREE.CircleGeometry(b.eyeSize * 0.55, 14);
  const cheeks = [-1, 1].map((sx) => {
    const c = at(new THREE.Mesh(cheekGeo, cheekM), sx * (b.eyeGap + b.eyeSize * 1.1), -b.eyeSize * 1.5, 0.001);
    b.face.add(c);
    return c;
  });
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
  g.scale.setScalar(opts.lead ? 1.6 : 1.45);

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
          T.lax = -1.25 + tap(0) * 0.35;
          T.rax = -1.25 + tap(0.5) * 0.35;
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
          T.raz = 2.5 + Math.sin(t * 8) * 0.45;
          T.rax = -0.2;
          T.laz = -0.5;
          T.lift = hop * 0.06;
          T.squash = 1 + (hop - 0.35) * 0.12;
          T.roll = Math.sin(t * 8) * 0.05;
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
        const toward = Math.atan2(lookAt.x, lookAt.z);
        T.yaw = Math.max(-0.4, Math.min(0.4, toward * 0.5));
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
      pipM.color.set(toneColors[tone]);
      pip.visible = tone !== 'stopped';
      pip.position.y = b.height + 0.22 + (motion ? Math.sin(t * 2.4) * 0.025 : 0);
      pip.scale.setScalar(tone === 'needs' && motion ? 1 + Math.abs(Math.sin(t0 * 6)) * 0.5 : 1);
    },
    dispose() {
      g.traverse((o) => (o as THREE.Mesh).isMesh && (o as THREE.Mesh).geometry.dispose());
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
  const lined: [THREE.Mesh, number][] = [];
  const line = (m: THREE.Mesh, th = 0.014) => (lined.push([m, th]), m);

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
    const sclera = new THREE.Mesh(new THREE.CircleGeometry(0.062, 24), white);
    sclera.scale.y = 1.25;
    const rim = new THREE.Mesh(new THREE.RingGeometry(0.062, 0.072, 24), inkM);
    rim.scale.y = 1.25;
    const pupil = at(new THREE.Group(), 0, -0.005, 0.002);
    const iris = new THREE.Mesh(new THREE.CircleGeometry(0.042, 20), inkM);
    iris.scale.y = 1.2;
    pupil.add(iris, at(new THREE.Mesh(new THREE.CircleGeometry(0.014, 10), white), 0.014, 0.02, 0.001), at(new THREE.Mesh(new THREE.CircleGeometry(0.007, 8), white), -0.012, -0.016, 0.001));
    const brow = at(new THREE.Mesh(new RoundedBoxGeometry(0.075, 0.016, 0.01, 2, 0.006), hair), 0, 0.1, 0);
    brow.rotation.z = sx * -0.15;
    e.add(sclera, rim, pupil, brow);
    head.add(e);
    const cheek = at(new THREE.Mesh(new THREE.CircleGeometry(0.035, 16), blush), sx * 0.17, -0.09, 0.225);
    cheek.rotation.y = sx * 0.5;
    head.add(cheek);
    return { sclera, rim, pupil };
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
  for (const [m, th] of lined) ink(m, th);
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
        e.sclera.scale.y = blink ? 0.1 : 1.25;
        e.rim.scale.y = blink ? 0.1 : 1.25;
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

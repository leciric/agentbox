// The Office's people. Each agent is a little desk robot: a bevelled TV head
// whose face is a glossy screen with glowing eyes that change shape with its
// mood, a bean-shaped body, two-jointed arms with mitten hands, and an
// antenna whose bulb is lit in its state's colour. Its shell colour and an
// accessory (headphones, a beanie, glasses…) come from a stable hash of its
// ref, so it looks the same every time. You are a person in a hoodie.
//
// Poses are targets the figure eases towards (frame-rate independent, so
// nothing snaps), with secondary motion on top: the antenna springs, eyes
// blink and glance, a robot near you turns to look at you. With motion off
// they hold a still pose for their state.
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

// hash is a stable number for a string, so an agent is the same robot every time.
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

// Bright enough to tell apart from across the room.
export const SHELLS = ['#ffc94d', '#4fc3f0', '#5fd8a8', '#f58fcf', '#a98bf2', '#ff8f52', '#6fa8f7', '#f3f1ec', '#ff9f9a', '#9adf6f'];
const EYES = ['#86f7ff', '#ffe58a', '#b5ffc6', '#ffffff', '#ffc2f2'];
const ACCESSORIES = ['none', 'headphones', 'beanie', 'bowtie', 'sprout', 'cap', 'glasses', 'scarf', 'none', 'headphones'] as const;
export type Accessory = (typeof ACCESSORIES)[number] | 'headset';

// lookOf is an agent's look, from its ref: its shell, its eyes, its accessory.
export function lookOf(ref: string): { shell: string; eye: string; accessory: Accessory } {
  const r = rng(hash(ref));
  return { shell: SHELLS[Math.floor(r() * SHELLS.length)], eye: EYES[Math.floor(r() * EYES.length)], accessory: ACCESSORIES[Math.floor(r() * ACCESSORIES.length)] };
}

const phys = (color: THREE.ColorRepresentation, o: Partial<THREE.MeshPhysicalMaterialParameters> = {}) =>
  new THREE.MeshPhysicalMaterial({ color, roughness: 0.38, metalness: 0, clearcoat: 0.55, clearcoatRoughness: 0.25, ...o });
const std = (color: THREE.ColorRepresentation, o: Partial<THREE.MeshStandardMaterialParameters> = {}) => new THREE.MeshStandardMaterial({ color, roughness: 0.6, ...o });

// ease moves v towards target at rate per second, at any frame rate.
const ease = (v: number, target: number, rate: number, dt: number) => v + (target - v) * (1 - Math.exp(-rate * dt));
// bump rises and falls once, smoothly, over [0, 1].
const bump = (u: number) => (u <= 0 || u >= 1 ? 0 : Math.sin(Math.PI * u) ** 2);
const at = <T extends THREE.Object3D>(o: T, x: number, y: number, z: number): T => (o.position.set(x, y, z), o);

// bake merges the static meshes among parts that share a material into one
// mesh each, in root's frame: a robot's body or head is a few draws, not many.
export function bake(root: THREE.Object3D, parts: THREE.Mesh[], castShadow = true): THREE.Mesh[] {
  root.updateMatrixWorld(true);
  const inv = new THREE.Matrix4().copy(root.matrixWorld).invert();
  const byMat = new Map<THREE.Material, THREE.BufferGeometry[]>();
  for (const m of parts) {
    const g0 = m.geometry.index ? m.geometry.toNonIndexed() : m.geometry.clone();
    g0.applyMatrix4(new THREE.Matrix4().multiplyMatrices(inv, m.matrixWorld));
    for (const name of Object.keys(g0.attributes)) if (name !== 'position' && name !== 'normal' && name !== 'uv') g0.deleteAttribute(name);
    if (!g0.attributes.uv) g0.setAttribute('uv', new THREE.Float32BufferAttribute(new Float32Array(g0.attributes.position.count * 2), 2));
    const list = byMat.get(m.material as THREE.Material) ?? [];
    list.push(g0);
    byMat.set(m.material as THREE.Material, list);
    m.parent?.remove(m);
  }
  const out: THREE.Mesh[] = [];
  for (const [mat, geos] of byMat) {
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

// The robot's body: a bean turned on a lathe.
function beanGeometry(): THREE.LatheGeometry {
  const prof: [number, number][] = [
    [0.0, 0.0],
    [0.12, 0.005],
    [0.185, 0.04],
    [0.205, 0.12],
    [0.2, 0.22],
    [0.18, 0.32],
    [0.15, 0.4],
    [0.1, 0.45],
    [0.0, 0.47],
  ];
  return new THREE.LatheGeometry(new THREE.SplineCurve(prof.map(([x, y]) => new THREE.Vector2(x, y))).getPoints(24), 32);
}

type Pose = {
  headPitch: number;
  headYaw: number;
  headRoll: number;
  bodyPitch: number;
  bodyRoll: number;
  lift: number;
  lsx: number;
  lsz: number;
  lel: number;
  rsx: number;
  rsz: number;
  rel: number;
  eyeOpen: number;
  lookX: number;
  lookY: number;
};

const restPose = (): Pose => ({ headPitch: 0, headYaw: 0, headRoll: 0, bodyPitch: 0, bodyRoll: 0, lift: 0, lsx: -0.35, lsz: -0.12, lel: -0.6, rsx: -0.35, rsz: 0.12, rel: -0.6, eyeOpen: 1, lookX: 0, lookY: 0 });

const toneColors: Record<DeskTone, string> = { needs: '#fbbf24', error: '#f87171', finished: '#34d399', working: '#60a5fa', idle: '#cbd5e1', stopped: '#374151' };

// robot is an agent, seated, facing +z, its origin on the floor under its
// seat. accent is its project's colour: its ears, hands and feet.
export function robot(ref: string, accent: THREE.ColorRepresentation, opts: { lead?: boolean } = {}): Figure {
  const look = lookOf(ref);
  const accessory: Accessory = opts.lead ? 'headset' : look.accessory;
  const r = rng(hash(`${ref}:life`));
  const mats: THREE.Material[] = [];
  const m = <T extends THREE.Material>(x: T) => (mats.push(x), x);

  const shell = m(phys(look.shell));
  const accentM = m(phys(new THREE.Color(accent), { roughness: 0.45 }));
  const dark = m(phys('#1b2130', { roughness: 0.25, clearcoat: 1, clearcoatRoughness: 0.08 }));
  const joint = m(std('#3a4252', { roughness: 0.5, metalness: 0.2 }));
  const face = m(new THREE.MeshPhysicalMaterial({ color: '#0d1320', roughness: 0.15, clearcoat: 1, clearcoatRoughness: 0.05, emissive: '#0b2a3a', emissiveIntensity: 0.35 }));
  const eyeM = m(new THREE.MeshBasicMaterial({ color: look.eye, toneMapped: false }));
  const cheekM = m(new THREE.MeshBasicMaterial({ color: '#ff8fb3', transparent: true, opacity: 0, toneMapped: false, depthWrite: false }));
  const bulbM = m(new THREE.MeshStandardMaterial({ color: '#60a5fa', emissive: '#60a5fa', emissiveIntensity: 1.6, roughness: 0.3 }));
  const fabric = m(std(accessory === 'headset' ? '#2b3242' : new THREE.Color().setHSL(r(), 0.55, 0.5), { roughness: 0.85 }));
  const leafM = m(std('#6cc46f', { roughness: 0.5 }));
  const rimM = m(std('#2a2f3a', { metalness: 0.6, roughness: 0.3 }));

  const g = new THREE.Group();
  const body = new THREE.Group();
  body.position.y = 0.47;
  g.add(body);

  // Body: the bean, a chest panel and light, legs seated on the chair.
  const bodyParts: THREE.Mesh[] = [];
  const add = (parent: THREE.Object3D, list: THREE.Mesh[] | undefined, mesh: THREE.Mesh) => {
    parent.add(mesh);
    list?.push(mesh);
    return mesh;
  };
  add(body, bodyParts, new THREE.Mesh(beanGeometry(), shell));
  const panel = add(body, bodyParts, at(new THREE.Mesh(new RoundedBoxGeometry(0.16, 0.11, 0.03, 3, 0.012), dark), 0, 0.22, 0.188));
  panel.rotation.x = -0.12;
  for (const sx of [-1, 1]) {
    const thigh = add(body, bodyParts, at(new THREE.Mesh(new THREE.CapsuleGeometry(0.065, 0.16, 6, 12), shell), sx * 0.09, 0.03, 0.12));
    thigh.rotation.x = Math.PI / 2;
    add(body, bodyParts, at(new THREE.Mesh(new THREE.CapsuleGeometry(0.055, 0.22, 6, 12), shell), sx * 0.09, -0.16, 0.28));
    add(body, bodyParts, at(new THREE.Mesh(new RoundedBoxGeometry(0.13, 0.07, 0.19, 3, 0.03), accentM), sx * 0.09, -0.43, 0.31));
    add(body, bodyParts, at(new THREE.Mesh(new THREE.SphereGeometry(0.062, 14, 10), joint), sx * 0.09, 0.03, 0.27));
  }
  add(body, bodyParts, at(new THREE.Mesh(new THREE.CylinderGeometry(0.05, 0.065, 0.07, 16), joint), 0, 0.5, 0));
  bake(body, bodyParts);
  const chest = at(new THREE.Mesh(new THREE.CircleGeometry(0.022, 20), bulbM), 0.04, 0.225, 0.206);
  chest.rotation.x = -0.12;
  body.add(chest);

  // The head: a bevelled TV, ears, a face screen.
  const head = new THREE.Group();
  head.position.set(0, 0.53, 0);
  body.add(head);
  const headParts: THREE.Mesh[] = [];
  add(head, headParts, at(new THREE.Mesh(new RoundedBoxGeometry(0.46, 0.37, 0.37, 5, 0.11), shell), 0, 0.2, 0));
  for (const sx of [-1, 1]) {
    const ear = add(head, headParts, at(new THREE.Mesh(new THREE.CylinderGeometry(0.07, 0.07, 0.05, 24), accentM), sx * 0.245, 0.2, 0));
    ear.rotation.z = Math.PI / 2;
    const bolt = add(head, headParts, at(new THREE.Mesh(new THREE.CylinderGeometry(0.03, 0.03, 0.02, 16), joint), sx * 0.275, 0.2, 0));
    bolt.rotation.z = Math.PI / 2;
  }
  // The accessory, on the head or at the neck.
  switch (accessory) {
    case 'headphones':
    case 'headset': {
      const band = add(head, headParts, at(new THREE.Mesh(new THREE.TorusGeometry(0.255, 0.02, 8, 32, Math.PI), fabric), 0, 0.2, 0));
      void band;
      for (const sx of [-1, 1]) add(head, headParts, at(new THREE.Mesh(new RoundedBoxGeometry(0.07, 0.15, 0.13, 3, 0.03), fabric), sx * 0.265, 0.2, 0));
      if (accessory === 'headset') {
        const boom = add(head, headParts, at(new THREE.Mesh(new THREE.CylinderGeometry(0.008, 0.008, 0.2, 6), joint), 0.23, 0.12, 0.12));
        boom.rotation.set(Math.PI / 2, 0, -0.5);
        add(head, headParts, at(new THREE.Mesh(new THREE.SphereGeometry(0.022, 10, 8), fabric), 0.17, 0.1, 0.21));
      }
      break;
    }
    case 'beanie': {
      const hat = add(head, headParts, at(new THREE.Mesh(new THREE.SphereGeometry(0.22, 24, 12, 0, Math.PI * 2, 0, Math.PI / 2), fabric), 0, 0.33, -0.01));
      hat.scale.set(1.08, 0.75, 0.98);
      const brim = add(head, headParts, at(new THREE.Mesh(new THREE.TorusGeometry(0.215, 0.035, 10, 32), fabric), 0, 0.33, -0.01));
      brim.rotation.x = Math.PI / 2;
      brim.scale.set(1.08, 0.98, 1);
      add(head, headParts, at(new THREE.Mesh(new THREE.SphereGeometry(0.05, 12, 10), shell), 0, 0.5, -0.01));
      break;
    }
    case 'cap': {
      const crown = add(head, headParts, at(new THREE.Mesh(new THREE.SphereGeometry(0.21, 24, 12, 0, Math.PI * 2, 0, Math.PI / 2), fabric), 0, 0.36, -0.02));
      crown.scale.set(1.12, 0.6, 1.05);
      const bill = add(head, headParts, at(new THREE.Mesh(new RoundedBoxGeometry(0.3, 0.025, 0.16, 2, 0.01), fabric), 0, 0.365, 0.2));
      bill.rotation.x = 0.12;
      break;
    }
    case 'sprout': {
      add(head, headParts, at(new THREE.Mesh(new THREE.CylinderGeometry(0.008, 0.01, 0.1, 6), leafM), -0.07, 0.42, 0));
      for (const s of [-1, 1]) {
        const leaf = add(head, headParts, at(new THREE.Mesh(new THREE.SphereGeometry(0.045, 12, 8), leafM), -0.07 + s * 0.04, 0.47, 0));
        leaf.scale.set(1, 0.35, 0.6);
        leaf.rotation.z = s * 0.5;
      }
      break;
    }
    case 'glasses': {
      for (const sx of [-1, 1]) add(head, headParts, at(new THREE.Mesh(new THREE.TorusGeometry(0.05, 0.008, 8, 24), rimM), sx * 0.085, 0.215, 0.215));
      add(head, headParts, at(new THREE.Mesh(new THREE.BoxGeometry(0.07, 0.01, 0.01), rimM), 0, 0.225, 0.215));
      break;
    }
    case 'bowtie':
    case 'scarf':
    case 'none':
      break;
  }
  bake(head, headParts);
  const neckwear: THREE.Mesh[] = [];
  if (accessory === 'bowtie' || accessory === 'headset') {
    for (const s of [-1, 1]) {
      const wing = add(body, neckwear, at(new THREE.Mesh(new THREE.ConeGeometry(0.04, 0.07, 4), fabric), s * 0.035, 0.45, 0.13));
      wing.rotation.z = (s * Math.PI) / 2;
    }
    add(body, neckwear, at(new THREE.Mesh(new THREE.SphereGeometry(0.018, 8, 6), fabric), 0, 0.45, 0.13));
  } else if (accessory === 'scarf') {
    const scarf = add(body, neckwear, at(new THREE.Mesh(new THREE.TorusGeometry(0.11, 0.04, 10, 24), fabric), 0, 0.47, 0));
    scarf.rotation.x = Math.PI / 2;
    const tail = add(body, neckwear, at(new THREE.Mesh(new RoundedBoxGeometry(0.07, 0.16, 0.03, 2, 0.01), fabric), 0.06, 0.38, 0.15));
    tail.rotation.set(-0.2, 0, 0.15);
  }
  if (neckwear.length) bake(body, neckwear);
  const screen = at(new THREE.Mesh(new RoundedBoxGeometry(0.37, 0.26, 0.04, 4, 0.07), face), 0, 0.2, 0.17);
  head.add(screen);

  // The eyes, three shapes each: open (a soft pill), happy (an arc), crossed
  // for an error; asleep is the open one turned to a line, worried tilted.
  const eyeOpenGeo = new THREE.CapsuleGeometry(0.03, 0.045, 6, 12);
  const eyeArcGeo = new THREE.TorusGeometry(0.035, 0.011, 8, 20, Math.PI);
  const crossGeo = new THREE.BoxGeometry(0.075, 0.016, 0.01);
  const eyes = [-1, 1].map((sx) => {
    const e = new THREE.Group();
    e.position.set(sx * 0.085, 0.215, 0.193);
    const open = new THREE.Mesh(eyeOpenGeo, eyeM);
    const arc = new THREE.Mesh(eyeArcGeo, eyeM);
    const cross = new THREE.Group();
    for (const a of [0.78, -0.78]) {
      const bar = new THREE.Mesh(crossGeo, eyeM);
      bar.rotation.z = a;
      cross.add(bar);
    }
    e.add(open, arc, cross);
    head.add(e);
    return { e, open, arc, cross, sx };
  });
  const mouth = at(new THREE.Mesh(new THREE.TorusGeometry(0.032, 0.008, 8, 20, Math.PI), eyeM), 0, 0.128, 0.193);
  mouth.rotation.z = Math.PI;
  head.add(mouth);
  const cheekGeo = new THREE.CircleGeometry(0.028, 16);
  const cheeks = [-1, 1].map((sx) => {
    const c = at(new THREE.Mesh(cheekGeo, cheekM), sx * 0.135, 0.15, 0.192);
    head.add(c);
    return c;
  });

  // The antenna, on a spring.
  const antenna = new THREE.Group();
  antenna.position.set(0.08, 0.37, -0.02);
  head.add(antenna);
  antenna.add(at(new THREE.Mesh(new THREE.CylinderGeometry(0.009, 0.012, 0.13, 8), joint), 0, 0.065, 0));
  antenna.add(at(new THREE.Mesh(new THREE.SphereGeometry(0.038, 16, 12), bulbM), 0, 0.14, 0));

  // Arms: shoulder, elbow, a mitten hand.
  const upperGeo = new THREE.CapsuleGeometry(0.042, 0.13, 6, 12);
  upperGeo.translate(0, -0.1, 0);
  const foreGeo = new THREE.CapsuleGeometry(0.038, 0.11, 6, 12);
  foreGeo.translate(0, -0.085, 0);
  const handGeo = new THREE.SphereGeometry(0.056, 16, 12);
  const ballGeo = new THREE.SphereGeometry(0.05, 12, 10);
  const arms = [-1, 1].map((sx) => {
    const shoulder = new THREE.Group();
    shoulder.position.set(sx * 0.2, 0.36, 0.01);
    shoulder.add(new THREE.Mesh(ballGeo, joint), new THREE.Mesh(upperGeo, shell));
    const elbow = new THREE.Group();
    elbow.position.y = -0.2;
    elbow.add(new THREE.Mesh(foreGeo, shell));
    const hand = at(new THREE.Mesh(handGeo, accentM), 0, -0.19, 0);
    hand.scale.set(1, 0.9, 0.85);
    elbow.add(hand);
    shoulder.add(elbow);
    body.add(shoulder);
    return { shoulder, elbow };
  });

  g.traverse((o) => {
    const mesh = o as THREE.Mesh;
    if (!mesh.isMesh) return;
    const glow = mesh.material === eyeM || mesh.material === cheekM || mesh.material === bulbM;
    mesh.castShadow = !glow && mesh !== screen;
    mesh.receiveShadow = !glow;
  });
  if (opts.lead) g.scale.setScalar(1.22);

  const cur = restPose();
  const T = restPose();
  let lastT: number | undefined;
  let blinkAt = 1 + r() * 3;
  let glance = { until: 0, x: 0, y: 0 };
  let ant = { a: 0, v: 0, b: 0, w: 0 };
  let prevYaw = 0;
  let prevLift = 0;
  const tip = new THREE.Color();
  const phase = r() * 20;

  return {
    group: g,
    pose(tone, t0, motion, lookAt) {
      const dt = lastT === undefined ? 1 : Math.min(0.1, Math.max(0, t0 - lastT));
      lastT = t0;
      const t = motion ? t0 + phase : phase;
      Object.assign(T, restPose());
      let mood: 'open' | 'happy' | 'cross' | 'sleep' | 'worried' = 'open';
      // It looks at you when you're close and it isn't busy or asleep.
      const looking = !!lookAt && (tone === 'needs' || tone === 'idle' || tone === 'finished') && Math.hypot(lookAt.x, lookAt.z) < 4.5;
      switch (tone) {
        case 'working': {
          // Typing: forearms tapping in turn, eyes on the laptop, a nod with
          // the rhythm, and now and then a glance up at the monitor.
          const tap = (o: number) => bump(((t * 3.2 + o) % 1) * 1.6);
          T.lsx = -0.78;
          T.lsz = -0.1;
          T.lel = -0.95 + tap(0) * 0.28;
          T.rsx = -0.78;
          T.rsz = 0.1;
          T.rel = -0.95 + tap(0.5) * 0.28;
          T.headPitch = 0.22 + bump((t * 0.8) % 1) * 0.05;
          T.headYaw = Math.sin(t * 0.37) * 0.08 + (Math.sin(t * 0.13) > 0.85 ? 0.35 : 0);
          T.lookY = -0.6;
          T.bodyPitch = 0.08;
          break;
        }
        case 'needs': {
          // Waving at you, bouncing a little, eyes wide, mouth open.
          const wave = Math.sin(t * 7.5);
          T.rsx = -0.25;
          T.rsz = 2.55;
          T.rel = -0.55 + wave * 0.38;
          T.lsx = -0.5;
          T.lsz = -0.2;
          T.lel = -0.9;
          T.headRoll = wave * 0.06;
          T.headPitch = -0.12;
          T.lift = Math.abs(Math.sin(t * 3.75)) * 0.035;
          T.eyeOpen = 1.15;
          break;
        }
        case 'finished': {
          // Pleased, leaning back; every few seconds a little celebration.
          const c = bump((t % 4.2) / 1.3);
          T.lsx = -0.3 - c * 0.2;
          T.lsz = -0.15 - c * 2.55;
          T.lel = -0.7 + c * 0.45;
          T.rsx = -0.3 - c * 0.2;
          T.rsz = 0.15 + c * 2.55;
          T.rel = -0.7 + c * 0.45;
          T.bodyPitch = -0.08 - c * 0.05;
          T.headPitch = -0.15;
          T.lift = c * 0.05;
          mood = 'happy';
          break;
        }
        case 'error': {
          // Hands to its head, shaking it now and then.
          const shake = bump((t % 3.4) / 0.9) * Math.sin(t * 16) * 0.22;
          T.lsx = -2.25;
          T.lsz = -0.55;
          T.lel = -1.55;
          T.rsx = -2.25;
          T.rsz = 0.55;
          T.rel = -1.55;
          T.headPitch = 0.28;
          T.headYaw = shake;
          T.bodyPitch = 0.12;
          mood = Math.floor(t / 5) % 2 ? 'cross' : 'worried';
          break;
        }
        case 'idle': {
          // At rest: breathing, looking about now and then.
          const rr = rng(hash(`${ref}:${Math.floor(t / 3.1)}`));
          T.headYaw = (rr() - 0.5) * 1.0;
          T.headPitch = (rr() - 0.5) * 0.25;
          T.lsx = -0.45;
          T.rsx = -0.45;
          T.lel = -0.75;
          T.rel = -0.75;
          T.lift = Math.sin(t * 1.7) * 0.006;
          break;
        }
        case 'stopped': {
          T.headPitch = 0.5;
          T.headRoll = 0.12;
          T.bodyPitch = 0.14;
          T.lsx = -0.15;
          T.rsx = -0.15;
          T.lel = -0.35;
          T.rel = -0.35;
          T.lift = Math.sin(t * 0.8) * 0.01;
          mood = 'sleep';
          break;
        }
      }
      if (looking && lookAt) {
        T.headYaw = Math.max(-0.9, Math.min(0.9, Math.atan2(lookAt.x, lookAt.z)));
        T.headPitch = Math.min(T.headPitch, -0.08);
        T.lookX = Math.max(-1, Math.min(1, T.headYaw * 0.6));
      }
      // Eyes: a blink now and then, a glance between.
      if (motion && t0 > blinkAt) blinkAt = t0 + 2.2 + r() * 3.5;
      const blinking = motion && mood !== 'sleep' && blinkAt - t0 > 0 && blinkAt - t0 < 0.13;
      if (motion && t0 > glance.until) glance = { until: t0 + 0.8 + r() * 2.5, x: (r() - 0.5) * 1.4, y: (r() - 0.5) * 0.8 };
      if (motion && tone !== 'working' && !looking) {
        T.lookX += glance.x;
        T.lookY += glance.y;
      }

      // Every joint eases towards its target; the elbows quicker.
      for (const k of Object.keys(cur) as (keyof Pose)[]) cur[k] = motion ? ease(cur[k], T[k], k === 'rel' || k === 'lel' ? 15 : 9, dt) : T[k];
      body.rotation.set(cur.bodyPitch, 0, cur.bodyRoll);
      body.position.y = 0.47 + cur.lift + (motion ? Math.sin(t * 2.1) * 0.004 : 0);
      head.rotation.set(cur.headPitch, cur.headYaw, cur.headRoll, 'YXZ');
      arms[0].shoulder.rotation.set(cur.lsx, 0, cur.lsz);
      arms[0].elbow.rotation.x = cur.lel;
      arms[1].shoulder.rotation.set(cur.rsx, 0, cur.rsz);
      arms[1].elbow.rotation.x = cur.rel;

      // The antenna lags the head and springs back.
      if (motion && dt > 0) {
        const yawV = (cur.headYaw - prevYaw) / dt;
        const liftV = (cur.lift - prevLift) / dt;
        ant.w += (-ant.b * 90 - ant.w * 7 - yawV * 2.2) * dt;
        ant.b += ant.w * dt;
        ant.v += (-ant.a * 90 - ant.v * 7 + liftV * 14) * dt;
        ant.a += ant.v * dt;
      } else if (!motion) ant = { a: 0, v: 0, b: 0, w: 0 };
      prevYaw = cur.headYaw;
      prevLift = cur.lift;
      antenna.rotation.set(Math.max(-0.6, Math.min(0.6, ant.a)), 0, Math.max(-0.6, Math.min(0.6, ant.b)));

      // The face.
      const open = blinking ? 0.1 : mood === 'sleep' ? 0.12 : cur.eyeOpen;
      for (const e of eyes) {
        e.open.visible = mood === 'open' || mood === 'sleep' || mood === 'worried';
        e.arc.visible = mood === 'happy';
        e.cross.visible = mood === 'cross';
        e.open.scale.set(1, Math.max(0.12, open), 1);
        e.open.rotation.z = mood === 'sleep' ? Math.PI / 2 : mood === 'worried' ? e.sx * -0.45 : 0;
        e.e.position.x = e.sx * 0.085 + cur.lookX * 0.022;
        e.e.position.y = 0.215 + cur.lookY * 0.02;
      }
      mouth.visible = mood === 'happy' || tone === 'needs' || tone === 'idle';
      mouth.rotation.z = tone === 'needs' ? 0 : Math.PI;
      mouth.scale.set(tone === 'needs' ? 0.7 : 1, tone === 'needs' ? 1.3 : 1, 1);
      mouth.position.y = tone === 'needs' ? 0.115 : 0.128;
      cheekM.opacity = mood === 'happy' || tone === 'needs' ? 0.55 : 0;
      for (const c of cheeks) c.visible = cheekM.opacity > 0;
      face.emissiveIntensity = tone === 'stopped' ? 0.05 : 0.35;
      eyeM.color.set(tone === 'stopped' ? '#4b5563' : tone === 'error' ? '#ff9a9a' : look.eye);

      tip.set(toneColors[tone]);
      bulbM.color.copy(tip);
      bulbM.emissive.copy(tip);
      bulbM.emissiveIntensity = tone === 'stopped' ? 0 : tone === 'needs' && motion ? 1.4 + Math.sin(t0 * 8) : tone === 'working' && motion ? 1.1 + bump((t * 1.7) % 1) * 1.4 : 1.5;
    },
    dispose() {
      g.traverse((o) => (o as THREE.Mesh).isMesh && (o as THREE.Mesh).geometry.dispose());
      for (const x of mats) x.dispose();
    },
  };
}

// walker is you: a person in a hoodie, standing, facing +z until you turn.
export type Walker = Figure & { stride(speed: number, t: number, motion: boolean): void; fade(opacity: number): void };

export function walker(): Walker {
  const hoodie = std('#7c5cf0', { roughness: 0.8 });
  const hoodieDark = std('#5b3fd0', { roughness: 0.85 });
  const pants = std('#26324a', { roughness: 0.8 });
  const skin = phys('#f3c7a5', { roughness: 0.55, clearcoat: 0.1 });
  const hair = std('#3a2a22', { roughness: 0.7 });
  const shoe = phys('#f5f5f7', { roughness: 0.4 });
  const sole = std('#cfd3da');
  const eyeM = phys('#151821', { roughness: 0.1, clearcoat: 1 });
  const glint = new THREE.MeshBasicMaterial({ color: '#ffffff' });
  const blush = new THREE.MeshBasicMaterial({ color: '#ff9aa8', transparent: true, opacity: 0.45, depthWrite: false });
  const mats = [hoodie, hoodieDark, pants, skin, hair, shoe, sole, eyeM, glint];

  const g = new THREE.Group();
  const hips = new THREE.Group();
  hips.position.y = 0.88;
  g.add(hips);
  // Legs, swinging from the hips, in white trainers.
  const legGeo = new THREE.CapsuleGeometry(0.08, 0.5, 6, 12);
  legGeo.translate(0, -0.33, 0);
  const legs = [-1, 1].map((sx) => {
    const p = new THREE.Group();
    p.position.set(sx * 0.1, 0, 0);
    p.add(new THREE.Mesh(legGeo, pants));
    p.add(at(new THREE.Mesh(new RoundedBoxGeometry(0.13, 0.09, 0.24, 3, 0.04), shoe), 0, -0.82, 0.04));
    p.add(at(new THREE.Mesh(new RoundedBoxGeometry(0.135, 0.025, 0.245, 2, 0.01), sole), 0, -0.86, 0.04));
    hips.add(p);
    return p;
  });
  // The torso: a hoodie turned on a lathe, with a hood, a pocket, strings.
  const torso = new THREE.Group();
  hips.add(torso);
  const prof: [number, number][] = [
    [0.0, -0.04],
    [0.17, -0.03],
    [0.2, 0.08],
    [0.205, 0.25],
    [0.2, 0.4],
    [0.16, 0.5],
    [0.07, 0.56],
    [0.0, 0.57],
  ];
  const chest = new THREE.Mesh(new THREE.LatheGeometry(new THREE.SplineCurve(prof.map(([x, y]) => new THREE.Vector2(x, y))).getPoints(20), 28), hoodie);
  chest.scale.set(1.1, 1, 0.85);
  torso.add(chest);
  const hood = at(new THREE.Mesh(new THREE.TorusGeometry(0.12, 0.05, 10, 24), hoodieDark), 0, 0.53, -0.04);
  hood.rotation.x = Math.PI / 2 - 0.3;
  torso.add(hood);
  torso.add(at(new THREE.Mesh(new RoundedBoxGeometry(0.2, 0.1, 0.03, 2, 0.015), hoodieDark), 0, 0.12, 0.175));
  for (const sx of [-1, 1]) torso.add(at(new THREE.Mesh(new THREE.CylinderGeometry(0.006, 0.006, 0.12, 6), shoe), sx * 0.04, 0.42, 0.16));
  // The head: big enough to read as friendly, with hair, eyes that catch the
  // light, brows, a nose, a smile and a blush.
  const headG = new THREE.Group();
  headG.position.y = 0.72;
  torso.add(headG);
  headG.add(at(new THREE.Mesh(new THREE.CylinderGeometry(0.055, 0.065, 0.08, 12), skin), 0, -0.1, 0));
  const skull = new THREE.Mesh(new THREE.SphereGeometry(0.2, 32, 24), skin);
  skull.scale.set(1, 1.02, 0.95);
  headG.add(skull);
  const hairCap = at(new THREE.Mesh(new THREE.SphereGeometry(0.212, 32, 16, 0, Math.PI * 2, 0, Math.PI * 0.55), hair), 0, 0.02, -0.01);
  hairCap.rotation.x = -0.35;
  headG.add(hairCap);
  const fringe = at(new THREE.Mesh(new THREE.SphereGeometry(0.09, 16, 10), hair), 0.07, 0.13, 0.13);
  fringe.scale.set(1.5, 0.55, 0.7);
  fringe.rotation.z = -0.35;
  headG.add(fringe);
  for (const sx of [-1, 1]) {
    const ear = at(new THREE.Mesh(new THREE.SphereGeometry(0.045, 12, 10), skin), sx * 0.195, -0.01, 0);
    ear.scale.set(0.5, 1, 0.8);
    headG.add(ear);
  }
  const eyes = [-1, 1].map((sx) => {
    const e = new THREE.Group();
    e.position.set(sx * 0.072, 0.01, 0.178);
    const ball = new THREE.Mesh(new THREE.SphereGeometry(0.032, 16, 12), eyeM);
    ball.scale.set(0.85, 1.15, 0.5);
    e.add(ball, at(new THREE.Mesh(new THREE.SphereGeometry(0.009, 8, 6), glint), 0.01, 0.014, 0.014));
    const brow = at(new THREE.Mesh(new RoundedBoxGeometry(0.06, 0.014, 0.012, 2, 0.006), hair), 0, 0.058, 0.005);
    brow.rotation.z = sx * -0.12;
    e.add(brow);
    headG.add(e);
    const cheek = at(new THREE.Mesh(new THREE.CircleGeometry(0.03, 16), blush), sx * 0.11, -0.05, 0.172);
    cheek.rotation.y = sx * 0.45;
    headG.add(cheek);
    return e;
  });
  headG.add(at(new THREE.Mesh(new THREE.SphereGeometry(0.02, 10, 8), skin), 0, -0.03, 0.195));
  const smile = at(new THREE.Mesh(new THREE.TorusGeometry(0.035, 0.007, 6, 16, Math.PI), eyeM), 0, -0.07, 0.183);
  smile.rotation.z = Math.PI;
  headG.add(smile);
  // Arms in sleeves, with hands.
  const upperGeo = new THREE.CapsuleGeometry(0.06, 0.2, 6, 12);
  upperGeo.translate(0, -0.13, 0);
  const foreGeo = new THREE.CapsuleGeometry(0.052, 0.18, 6, 12);
  foreGeo.translate(0, -0.11, 0);
  const arms = [-1, 1].map((sx) => {
    const sh = new THREE.Group();
    sh.position.set(sx * 0.235, 0.45, 0);
    sh.add(new THREE.Mesh(upperGeo, hoodie));
    const el = new THREE.Group();
    el.position.y = -0.27;
    el.add(new THREE.Mesh(foreGeo, hoodie));
    el.add(at(new THREE.Mesh(new THREE.SphereGeometry(0.055, 12, 10), skin), 0, -0.25, 0));
    sh.add(el);
    torso.add(sh);
    return { sh, el, sx };
  });
  g.traverse((o) => {
    const mesh = o as THREE.Mesh;
    if (mesh.isMesh && mesh.material !== glint && mesh.material !== blush) {
      mesh.castShadow = true;
      mesh.receiveShadow = true;
    }
  });

  let gait = 0;
  let lastT: number | undefined;
  let k = 0;
  let blinkAt = 2;
  return {
    group: g,
    pose() {},
    stride(speed, t, motion) {
      const dt = lastT === undefined ? 0 : Math.min(0.1, Math.max(0, t - lastT));
      lastT = t;
      // The stride eases in and out with speed; its phase runs with
      // distance, so feet don't skate.
      k = motion ? ease(k, Math.min(1, speed / 2.6), 8, dt) : 0;
      gait += dt * (4 + speed * 2.2);
      const s = Math.sin(gait) * 0.62 * k;
      legs[0].rotation.x = s;
      legs[1].rotation.x = -s;
      for (const a of arms) {
        a.sh.rotation.set(a.sx === -1 ? s * 0.85 : -s * 0.85, 0, a.sx * 0.12);
        a.el.rotation.x = -0.25 - k * 0.35;
      }
      // A bob twice a stride, a counter-twist, a lean into the walk.
      hips.position.y = 0.88 + Math.abs(Math.cos(gait)) * 0.045 * k;
      torso.rotation.set(0.08 * k, Math.sin(gait) * 0.12 * k, 0);
      // Standing: breathing, and the odd look round.
      const still = 1 - k;
      torso.scale.set(1, 1 + (motion ? Math.sin(t * 2.2) * 0.008 * still : 0), 1);
      headG.rotation.set(motion ? Math.sin(t * 0.7) * 0.03 * still : 0, motion ? Math.sin(t * 0.31) * 0.35 * still : 0, 0);
      if (motion && t > blinkAt) blinkAt = t + 2.5 + ((t * 7.3) % 3);
      const blink = motion && blinkAt - t > 0 && blinkAt - t < 0.12;
      for (const e of eyes) e.scale.y = blink ? 0.12 : 1;
    },
    fade(opacity) {
      g.visible = opacity > 0.2;
      for (const x of mats) {
        const see = opacity < 1;
        // Switching transparency recompiles the material's shader.
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

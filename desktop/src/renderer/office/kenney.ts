// You, as Kenney's Mini Characters "character-female-b": a blocky chibi with
// its own walk, sprint and idle, played at the speed you move, which suit the logo cube's toy room
// better than anything drawn in code. CC0 (assets/kenney/License.txt), so it
// may be bundled; it's inlined into the Office's lazy chunk (about 340 kB),
// and the hand-drawn walker stands in until it has loaded.
import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
// Ginger hair and a yellow top: found at a glance from above.
import glbUrl from './assets/kenney/character-female-b.glb?inline';
import colormapUrl from './assets/kenney/colormap.png?inline';
import type { Walker } from './figures.ts';

// How tall you stand, in metres: about a seated Boxie's height, a toy among toys.
const HEIGHT = 1.95;

// player wraps a stand-in walker and swaps the Kenney figure in once it has
// loaded; onReady asks for a frame.
export function player(standIn: Walker, onReady: () => void): Walker {
  const g = new THREE.Group();
  g.add(standIn.group);
  let ready: Loaded | undefined;
  let disposed = false;
  let opacity = 1;
  load()
    .then((l) => {
      if (disposed) return l.dispose();
      ready = l;
      // The stand-in's children (your name tag) move over to you.
      for (const c of standIn.group.children.filter((o) => o instanceof THREE.Sprite)) l.group.add(c);
      g.remove(standIn.group);
      standIn.dispose();
      g.add(l.group);
      l.fade(opacity);
      onReady();
    })
    .catch(() => {
      // The stand-in stays.
    });
  return {
    group: g,
    pose() {},
    stride(speed, t, motion) {
      if (ready) ready.stride(speed, t, motion);
      else standIn.stride(speed, t, motion);
    },
    fade(o) {
      opacity = o;
      if (ready) ready.fade(o);
      else standIn.fade(o);
    },
    dispose() {
      disposed = true;
      if (ready) ready.dispose();
      else standIn.dispose();
    },
  };
}

type Loaded = { group: THREE.Group; stride: Walker['stride']; fade: Walker['fade']; dispose(): void };

async function load(): Promise<Loaded> {
  const manager = new THREE.LoadingManager();
  // The GLB names its texture beside it; it's inlined too.
  manager.setURLModifier((url) => (url.endsWith('colormap.png') ? colormapUrl : url));
  const buf = await (await fetch(glbUrl)).arrayBuffer();
  const gltf = await new GLTFLoader(manager).parseAsync(buf, '');
  const model = gltf.scene;
  const mats = new Set<THREE.MeshStandardMaterial>();
  model.traverse((o) => {
    const m = o as THREE.Mesh;
    if (!m.isMesh) return;
    m.castShadow = true;
    m.receiveShadow = false;
    // Skinned: its bounds move with the walk, so it's never culled away.
    m.frustumCulled = false;
    for (const x of Array.isArray(m.material) ? m.material : [m.material]) {
      const s = x as THREE.MeshStandardMaterial;
      // Toy plastic, as Boxie's.
      s.roughness = 0.55;
      s.metalness = 0;
      if (s.map) s.map.colorSpace = THREE.SRGBColorSpace;
      mats.add(s);
    }
  });
  const box = new THREE.Box3().setFromObject(model);
  const h = Math.max(0.01, box.max.y - box.min.y);
  const k = HEIGHT / h;
  model.scale.setScalar(k);
  model.position.y = -box.min.y * k;
  const g = new THREE.Group();
  g.add(model);

  // Its walk and sprint as they are, but for their hop: the root's bounce
  // goes, so you glide over the floor rather than skip along it.
  for (const c of gltf.animations) c.tracks = c.tracks.filter((tr) => !(tr.name.startsWith('root.') && tr.name.endsWith('.position')));
  const mixer = new THREE.AnimationMixer(model);
  const clip = (name: string) => gltf.animations.find((a) => a.name === name);
  const act = (name: string) => {
    const c = clip(name);
    if (!c) return undefined;
    const a = mixer.clipAction(c);
    a.play();
    a.setEffectiveWeight(0);
    return a;
  };
  const idle = act('idle');
  const walk = act('walk');
  const sprint = act('sprint');
  // How far each cycle carries you, in metres, from its legs' swing (60° a
  // side walking, 90° running) and their length: playing it at speed over
  // that, the feet stay where they're put.
  const leg = 0.176 * k;
  const WALK_CYCLE = 4 * leg * Math.sin(Math.PI / 3);
  const RUN_CYCLE = 4 * leg;
  let lastT: number | undefined;
  // Moving (0 standing, 1 moving), running (0 walking, 1 running), where in
  // the cycle the feet are, the idle's time, and the lean.
  let moveK = 0;
  let runK = 0;
  let phase = 0;
  let idleT = 0;
  let lastYaw: number | undefined;
  let roll = 0;
  return {
    group: g,
    stride(speed, t, motion) {
      const dt = lastT === undefined ? 0 : Math.min(0.1, Math.max(0, t - lastT));
      lastT = t;
      // Crossfades, eased: standing to walking with speed, walking to
      // running past a brisk walk.
      const ease = (from: number, to: number, rate: number) => (motion ? from + (to - from) * (1 - Math.exp(-dt * rate)) : to);
      moveK = ease(moveK, Math.min(1, speed / 0.9), 9);
      runK = ease(runK, Math.max(0, Math.min(1, (speed - 3.4) / 1.4)), 6);
      // Both cycles share one phase, so blending them never crosses the feet.
      const cycle = WALK_CYCLE + (RUN_CYCLE - WALK_CYCLE) * runK;
      if (motion) phase = (phase + (speed * dt) / cycle) % 1;
      idleT += motion ? dt : 0;
      if (idle) {
        idle.time = idleT % idle.getClip().duration;
        idle.setEffectiveWeight(1 - moveK);
      }
      if (walk) {
        walk.time = phase * walk.getClip().duration;
        walk.setEffectiveWeight(moveK * (1 - runK));
      }
      if (sprint) {
        sprint.time = phase * sprint.getClip().duration;
        sprint.setEffectiveWeight(moveK * runK);
      }
      mixer.update(0);
      // A small lean into a turn, and forward into a run.
      const yaw = g.parent?.rotation.y ?? 0;
      let turnRate = 0;
      if (lastYaw !== undefined && dt > 0) {
        let d = yaw - lastYaw;
        d = Math.atan2(Math.sin(d), Math.cos(d));
        turnRate = d / dt;
      }
      lastYaw = yaw;
      roll = ease(roll, Math.max(-0.16, Math.min(0.16, -turnRate * speed * 0.012)), 8);
      model.rotation.z = motion ? roll : 0;
      model.rotation.x = motion ? moveK * (0.04 + 0.08 * runK) : 0;
    },
    fade(opacity) {
      g.visible = opacity > 0.2;
      const see = opacity < 1;
      for (const m of mats) {
        if (m.transparent !== see) {
          m.transparent = see;
          m.needsUpdate = true;
        }
        m.opacity = opacity;
        m.depthWrite = !see;
      }
    },
    dispose() {
      mixer.stopAllAction();
      model.traverse((o) => (o as THREE.Mesh).isMesh && (o as THREE.Mesh).geometry.dispose());
      for (const m of mats) {
        m.map?.dispose();
        m.dispose();
      }
    },
  };
}

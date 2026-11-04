// The Office's people: each agent a little robot at its desk, its antenna lit
// in its state's colour, and you, the one walking about. Each figure's pose
// follows its state: typing while it works, waving when it needs you, arms up
// when it's finished, slumped asleep when its machine is stopped.
import * as THREE from 'three';
import type { DeskTone } from './model.ts';

export type Figure = {
  group: THREE.Group;
  // Poses the figure for its state at time t (seconds); still when motion is off.
  pose(tone: DeskTone, t: number, motion: boolean): void;
  dispose(): void;
};

const mat = (color: THREE.ColorRepresentation, opts: Partial<THREE.MeshStandardMaterialParameters> = {}) =>
  new THREE.MeshStandardMaterial({ color, roughness: 0.55, metalness: 0.05, ...opts });

function shadowed<T extends THREE.Object3D>(o: T): T {
  o.traverse((c) => {
    if ((c as THREE.Mesh).isMesh) c.castShadow = true;
  });
  return o;
}

// robot is an agent, seated, facing +z. hue is its project's colour.
export function robot(hue: number, seed: number): Figure {
  const disposables: { dispose(): void }[] = [];
  const keep = <T extends { dispose(): void }>(x: T) => (disposables.push(x), x);
  const shell = keep(mat(new THREE.Color().setHSL(hue / 360, 0.5, 0.62)));
  const trim = keep(mat('#e9edf3', { roughness: 0.35 }));
  const dark = keep(mat('#141a24', { roughness: 0.3 }));
  const eye = keep(new THREE.MeshBasicMaterial({ color: '#9be7ff' }));
  const tip = keep(new THREE.MeshStandardMaterial({ color: '#60a5fa', emissive: '#60a5fa', emissiveIntensity: 1.4 }));

  const g = new THREE.Group();
  const bodyGeo = keep(new THREE.CapsuleGeometry(0.19, 0.22, 6, 16));
  const body = new THREE.Mesh(bodyGeo, shell);
  body.position.y = 0.7;
  g.add(body);
  const belly = new THREE.Mesh(keep(new THREE.CircleGeometry(0.1, 20)), trim);
  belly.position.set(0, 0.68, 0.19);
  g.add(belly);

  const head = new THREE.Group();
  head.position.y = 1.06;
  const skull = new THREE.Mesh(keep(new THREE.SphereGeometry(0.2, 24, 18)), trim);
  skull.scale.set(1.12, 0.92, 1);
  head.add(skull);
  const visor = new THREE.Mesh(keep(new THREE.SphereGeometry(0.17, 24, 16, -Math.PI / 2 - 0.9, 1.8, Math.PI / 2 - 0.55, 1.1)), dark);
  visor.scale.set(1.14, 0.92, 1.04);
  head.add(visor);
  const eyeGeo = keep(new THREE.SphereGeometry(0.028, 10, 8));
  const eyes: THREE.Mesh[] = [];
  for (const x of [-0.065, 0.065]) {
    const e = new THREE.Mesh(eyeGeo, eye);
    e.position.set(x, 0.01, 0.175);
    e.scale.set(1, 1.3, 0.5);
    head.add(e);
    eyes.push(e);
  }
  const stalk = new THREE.Mesh(keep(new THREE.CylinderGeometry(0.012, 0.012, 0.14)), dark);
  stalk.position.y = 0.24;
  head.add(stalk);
  const bulb = new THREE.Mesh(keep(new THREE.SphereGeometry(0.04, 14, 10)), tip);
  bulb.position.y = 0.33;
  head.add(bulb);
  for (const x of [-0.225, 0.225]) {
    const ear = new THREE.Mesh(keep(new THREE.CylinderGeometry(0.05, 0.05, 0.04, 16)), shell);
    ear.rotation.z = Math.PI / 2;
    ear.position.x = x;
    head.add(ear);
  }
  g.add(head);

  const armGeo = keep(new THREE.CapsuleGeometry(0.045, 0.22, 4, 8));
  armGeo.translate(0, -0.13, 0);
  const arms = [-1, 1].map((side) => {
    const pivot = new THREE.Group();
    pivot.position.set(side * 0.23, 0.82, 0);
    const arm = new THREE.Mesh(armGeo, shell);
    pivot.add(arm);
    const hand = new THREE.Mesh(keep(new THREE.SphereGeometry(0.05, 10, 8)), trim);
    hand.position.y = -0.27;
    pivot.add(hand);
    g.add(pivot);
    return pivot;
  });
  shadowed(g);

  const phase = (seed % 97) * 0.37;
  const color = new THREE.Color();
  const colors: Record<DeskTone, string> = { needs: '#fbbf24', error: '#f87171', finished: '#34d399', working: '#60a5fa', idle: '#cbd5e1', stopped: '#374151' };
  let blinkAt = 2 + (seed % 5);
  return {
    group: g,
    pose(tone, t0, motion) {
      const t = motion ? t0 + phase : 0;
      color.set(colors[tone]);
      tip.color.copy(color);
      tip.emissive.copy(color);
      tip.emissiveIntensity = tone === 'stopped' ? 0 : tone === 'needs' && motion ? 1.2 + Math.sin(t * 8) * 0.8 : 1.3;
      const asleep = tone === 'stopped';
      eye.color.set(asleep ? '#1f2937' : tone === 'error' ? '#fca5a5' : '#9be7ff');
      // Blinks now and then.
      const blink = motion && !asleep && t % blinkAt < 0.12;
      if (t % blinkAt < 0.02) blinkAt = 2.5 + ((seed * 7 + Math.floor(t)) % 4);
      for (const e of eyes) e.scale.y = blink ? 0.2 : asleep ? 0.25 : 1.3;
      const [l, r] = arms;
      head.rotation.set(0, 0, 0);
      head.position.y = 1.06;
      body.rotation.set(0, 0, 0);
      g.position.y = 0;
      switch (tone) {
        case 'working': {
          // Typing: the hands on the laptop, tapping.
          l.rotation.set(-1.15 + Math.sin(t * 14) * 0.12, 0, 0.1);
          r.rotation.set(-1.15 + Math.sin(t * 14 + 1.7) * 0.12, 0, -0.1);
          head.rotation.x = 0.18 + Math.sin(t * 1.3) * 0.04;
          head.rotation.y = Math.sin(t * 0.7) * 0.12;
          break;
        }
        case 'needs': {
          // Waving at you, looking up.
          l.rotation.set(-0.3, 0, 0.15);
          r.rotation.set(-0.2, 0, 2.5 + Math.sin(t * 7) * 0.45);
          head.rotation.x = -0.15;
          head.rotation.z = Math.sin(t * 3.5) * 0.08;
          g.position.y = Math.abs(Math.sin(t * 3.5)) * 0.04;
          break;
        }
        case 'error': {
          // Head in its hands.
          l.rotation.set(-2.3, 0, 0.5);
          r.rotation.set(-2.3, 0, -0.5);
          head.rotation.x = 0.35;
          head.rotation.z = Math.sin(t * 1.2) * 0.1;
          break;
        }
        case 'finished': {
          // Leaning back, arms up: done.
          const cheer = Math.max(0, Math.sin(t * 1.4));
          l.rotation.set(0, 0, -2.2 - cheer * 0.3);
          r.rotation.set(0, 0, 2.2 + cheer * 0.3);
          body.rotation.x = -0.1;
          head.rotation.x = -0.2;
          break;
        }
        case 'idle': {
          l.rotation.set(-0.25, 0, 0.12);
          r.rotation.set(-0.25, 0, -0.12);
          head.rotation.y = Math.sin(t * 0.4) * 0.35;
          head.position.y = 1.06 + Math.sin(t * 1.6) * 0.006;
          break;
        }
        case 'stopped': {
          l.rotation.set(-0.1, 0, 0.05);
          r.rotation.set(-0.1, 0, -0.05);
          head.rotation.x = 0.5;
          head.position.y = 1.0 + Math.sin(t * 0.9) * 0.008;
          body.rotation.x = 0.12;
          break;
        }
      }
    },
    dispose() {
      for (const d of disposables) d.dispose();
    },
  };
}

// walker is you: a person, standing, facing +z until you turn. The legs swing
// with speed (metres a second).
export type Walker = Figure & { stride(speed: number, t: number, motion: boolean): void; fade(opacity: number): void };

export function walker(): Walker {
  const disposables: { dispose(): void }[] = [];
  const keep = <T extends { dispose(): void }>(x: T) => (disposables.push(x), x);
  const shirt = keep(mat('#8b5cf6'));
  const pants = keep(mat('#1e293b'));
  const skin = keep(mat('#f2c6a0', { roughness: 0.7 }));
  const hair = keep(mat('#3b2a20', { roughness: 0.9 }));
  const g = new THREE.Group();
  const torso = new THREE.Mesh(keep(new THREE.CapsuleGeometry(0.2, 0.36, 6, 16)), shirt);
  torso.position.y = 1.08;
  g.add(torso);
  const head = new THREE.Mesh(keep(new THREE.SphereGeometry(0.17, 22, 16)), skin);
  head.position.y = 1.58;
  g.add(head);
  const cap = new THREE.Mesh(keep(new THREE.SphereGeometry(0.178, 22, 12, 0, Math.PI * 2, 0, Math.PI / 2.1)), hair);
  cap.position.y = 1.6;
  cap.rotation.x = -0.25;
  g.add(cap);
  const eyeM = keep(new THREE.MeshBasicMaterial({ color: '#1f2937' }));
  for (const x of [-0.06, 0.06]) {
    const e = new THREE.Mesh(keep(new THREE.SphereGeometry(0.022, 8, 6)), eyeM);
    e.position.set(x, 1.6, 0.155);
    g.add(e);
  }
  const legGeo = keep(new THREE.CapsuleGeometry(0.075, 0.5, 4, 8));
  legGeo.translate(0, -0.3, 0);
  const legs = [-0.1, 0.1].map((x) => {
    const p = new THREE.Group();
    p.position.set(x, 0.78, 0);
    p.add(new THREE.Mesh(legGeo, pants));
    g.add(p);
    return p;
  });
  const armGeo = keep(new THREE.CapsuleGeometry(0.06, 0.42, 4, 8));
  armGeo.translate(0, -0.25, 0);
  const arms = [-0.27, 0.27].map((x) => {
    const p = new THREE.Group();
    p.position.set(x, 1.32, 0);
    p.add(new THREE.Mesh(armGeo, shirt));
    g.add(p);
    return p;
  });
  shadowed(g);
  return {
    group: g,
    pose() {},
    stride(speed, t, motion) {
      const k = motion ? Math.min(1, speed / 2.5) : 0;
      const s = Math.sin(t * 9) * 0.6 * k;
      legs[0].rotation.x = s;
      legs[1].rotation.x = -s;
      arms[0].rotation.x = -s * 0.8;
      arms[1].rotation.x = s * 0.8;
      arms[0].rotation.z = -0.08;
      arms[1].rotation.z = 0.08;
      g.position.y = motion ? Math.abs(Math.cos(t * 9)) * 0.03 * k : 0;
      // Breathing, standing still.
      torso.scale.y = 1 + (motion && k === 0 ? Math.sin(t * 2) * 0.012 : 0);
    },
    fade(opacity) {
      g.visible = opacity > 0.2;
      for (const m of [shirt, pants, skin, hair, eyeM]) {
        const see = opacity < 1;
        // Switching transparency recompiles the material's shader.
        if (m.transparent !== see) {
          m.transparent = see;
          m.needsUpdate = true;
        }
        m.opacity = opacity;
        m.depthWrite = !see;
      }
    },
    dispose() {
      for (const d of disposables) d.dispose();
    },
  };
}

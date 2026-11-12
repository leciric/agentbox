// Pieces of Kenney's Furniture Kit (CC0, assets/kenney/furniture/License.txt)
// where they beat the hand-built ones: the commons' kitchen (cabinets, sink,
// fridge, coffee machine, microwave, wall cabinets), bookcases and books, a
// floor lamp, potted plants. Low-poly with flat colours, recoloured to the
// room's palette; inlined into the Office's lazy chunk (about 200 kB), and
// loaded once: until they have, or if they can't be, the hand-built pieces
// stand in (props.ts, decor.ts).
import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import bookcaseOpen from './assets/kenney/furniture/bookcaseOpen.glb?inline';
import books from './assets/kenney/furniture/books.glb?inline';
import kitchenCabinet from './assets/kenney/furniture/kitchenCabinet.glb?inline';
import kitchenCabinetDrawer from './assets/kenney/furniture/kitchenCabinetDrawer.glb?inline';
import kitchenCabinetUpperDouble from './assets/kenney/furniture/kitchenCabinetUpperDouble.glb?inline';
import kitchenCoffeeMachine from './assets/kenney/furniture/kitchenCoffeeMachine.glb?inline';
import kitchenFridge from './assets/kenney/furniture/kitchenFridge.glb?inline';
import kitchenMicrowave from './assets/kenney/furniture/kitchenMicrowave.glb?inline';
import kitchenSink from './assets/kenney/furniture/kitchenSink.glb?inline';
import lampRoundFloor from './assets/kenney/furniture/lampRoundFloor.glb?inline';
import plantSmall1 from './assets/kenney/furniture/plantSmall1.glb?inline';
import plantSmall2 from './assets/kenney/furniture/plantSmall2.glb?inline';
import plantSmall3 from './assets/kenney/furniture/plantSmall3.glb?inline';
import pottedPlant from './assets/kenney/furniture/pottedPlant.glb?inline';

const FILES = { bookcaseOpen, books, kitchenCabinet, kitchenCabinetDrawer, kitchenCabinetUpperDouble, kitchenCoffeeMachine, kitchenFridge, kitchenMicrowave, kitchenSink, lampRoundFloor, plantSmall1, plantSmall2, plantSmall3, pottedPlant };
export type Piece = keyof typeof FILES;

// The kit is about half size: this makes it metres.
const SCALE = 2;

// Its materials, by name, in the room's palette: warm oak, cream, soft greens.
const RECOLOUR: Record<string, string> = {
  wood: '#e1a86d',
  woodDark: '#c98f58',
  carpet: '#9cb89a',
  carpetWhite: '#f3ece1',
  plant: '#5aa55a',
  metal: '#d8d2c8',
  metalMedium: '#b9b3aa',
  metalDark: '#4a4f58',
  metalLight: '#eeeae4',
};

type Loaded = { model: THREE.Object3D; w: number; h: number; d: number };
let ready: Partial<Record<Piece, Loaded>> | undefined;
let loading: Promise<void> | undefined;

// loadKit loads every piece, once; it resolves when they're there (or failed).
export function loadKit(): Promise<void> {
  loading ??= (async () => {
    const loader = new GLTFLoader();
    const out: Partial<Record<Piece, Loaded>> = {};
    const mats = new Map<string, THREE.MeshStandardMaterial>();
    await Promise.all(
      (Object.keys(FILES) as Piece[]).map(async (name) => {
        try {
          const buf = await (await fetch(FILES[name])).arrayBuffer();
          const gltf = await loader.parseAsync(buf, '');
          const model = gltf.scene;
          model.traverse((o) => {
            const m = o as THREE.Mesh;
            if (!m.isMesh) return;
            // One material per name across the kit, so the bake merges them.
            const src = m.material as THREE.MeshStandardMaterial;
            const key = src.name || src.uuid;
            let mat = mats.get(key);
            if (!mat) {
              mat = new THREE.MeshStandardMaterial({ color: RECOLOUR[src.name] ?? src.color, roughness: 0.6, metalness: 0 });
              mats.set(key, mat);
            }
            m.material = mat;
          });
          // Centred on its footprint, its floor at 0.
          model.scale.setScalar(SCALE);
          const box = new THREE.Box3().setFromObject(model);
          const c = box.getCenter(new THREE.Vector3());
          model.position.set(-c.x, -box.min.y, -c.z);
          const holder = new THREE.Group();
          holder.add(model);
          out[name] = { model: holder, w: box.max.x - box.min.x, h: box.max.y - box.min.y, d: box.max.z - box.min.z };
        } catch {
          // That one stays hand-built.
        }
      }),
    );
    ready = out;
  })();
  return loading;
}

// piece is a copy of one, centred on its footprint, facing +z, or nothing
// while the kit isn't loaded.
export function piece(name: Piece): THREE.Object3D | undefined {
  const l = ready?.[name];
  return l ? l.model.clone(true) : undefined;
}

// sizeOf is a piece's size, in metres: across, high and deep.
export function sizeOf(name: Piece): { w: number; h: number; d: number } | undefined {
  const l = ready?.[name];
  return l && { w: l.w, h: l.h, d: l.d };
}

export const kitLoaded = () => !!ready;

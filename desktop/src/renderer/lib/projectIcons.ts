// The last icon each project showed, kept so a project's icon is drawn on
// the first frame: when it is switched to, when its iconVersion moves (the
// new one loads behind the old), and when the app starts (from
// localStorage, for icons small enough to keep there). The daemon's answer,
// keyed by the version, always replaces what is kept here.
import type * as T from '../../shared/api';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

export type Kept = { v: number; icon: T.ProjectIcon };

const storageKey = 'agentbox.projectIcons';
// An image bigger than this as a data: URL isn't written to localStorage,
// and all of them together stay under budget: its quota is a few MB.
export const keepLimit = 96 * 1024;
const budget = 1024 * 1024;

const kept = new Map<string, Kept>();
let loaded = false;

function load(storage: Store | undefined) {
  if (loaded) return;
  loaded = true;
  try {
    const raw = storage?.getItem(storageKey);
    const all = raw ? (JSON.parse(raw) as Record<string, Kept>) : {};
    for (const [name, k] of Object.entries(all)) if (k && typeof k.v === 'number' && k.icon) kept.set(name, k);
  } catch {
    // A broken or unreadable entry is no icon kept.
  }
}

function browserStorage(): Store | undefined {
  try {
    return localStorage;
  } catch {
    return undefined;
  }
}

// keptIcon is the last icon project showed, whatever its version.
export function keptIcon(project: string, storage = browserStorage()): Kept | undefined {
  load(storage);
  return kept.get(project);
}

// stored is what goes to storage for each project: a smaller copy of a big
// image (thumbnail), else the icon itself.
const stored = new Map<string, Kept>();

// keepIcon remembers the icon project shows at version v, and writes it to
// storage, or small, a copy of it small enough to keep there.
export function keepIcon(project: string, v: number, icon: T.ProjectIcon, storage = browserStorage(), small: T.ProjectIcon = icon) {
  load(storage);
  kept.set(project, { v, icon });
  stored.set(project, { v, icon: small });
  const out: Record<string, Kept> = {};
  let size = 0;
  for (const [name, k] of kept) {
    const s = stored.get(name) ?? k;
    const n = s.icon.image?.length ?? 0;
    if (n > keepLimit || size + n > budget) continue;
    size += n;
    out[name] = s;
  }
  try {
    storage?.setItem(storageKey, JSON.stringify(out));
  } catch {
    // Over quota or blocked: the copy in memory still serves this run.
  }
}

// resetKept forgets everything, for tests.
export function resetKept() {
  kept.clear();
  stored.clear();
  loaded = false;
}

const decoded = new Map<string, HTMLImageElement>();

// decodeIcon decodes an icon's image before it is shown, so the first frame
// that draws it has it; the decoded image is held per project while it is
// that project's icon. It returns the copy to keep in storage: for an image
// too big to keep, the image drawn at thumbSize, which is more than any
// place shows an icon at.
export async function decodeIcon(project: string, icon: T.ProjectIcon): Promise<T.ProjectIcon> {
  if (!icon.image || typeof Image === 'undefined') return icon;
  const img = new Image();
  img.src = icon.image;
  decoded.set(project, img);
  if (!(await img.decode().then(() => true, () => false)) || icon.image.length <= keepLimit) return icon;
  try {
    const scale = Math.min(1, thumbSize / Math.max(img.naturalWidth, img.naturalHeight, 1));
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(img.naturalWidth * scale));
    canvas.height = Math.max(1, Math.round(img.naturalHeight * scale));
    canvas.getContext('2d')?.drawImage(img, 0, 0, canvas.width, canvas.height);
    return { ...icon, image: canvas.toDataURL('image/png') };
  } catch {
    return icon;
  }
}

const thumbSize = 96;

// What you typed for each agent (or the project lead chat), kept across
// restarts. localStorage already backs the environment choice (see
// web/bridge.ts) and survives in the Electron renderer the same way, so
// drafts use it too rather than adding an IPC round trip to the main process.
import { useSyncExternalStore } from 'react';
import { LeadName } from '../../shared/api.ts';
import type { Pending } from './chatFiles.ts';

const storageKey = 'agentbox.drafts';
const maxEntries = 200; // caps unbounded growth from agents that no longer exist
const writeDelayMs = 400;

interface StoredDraft {
  text: string;
  updatedAt: number;
}

function load(): Record<string, StoredDraft> {
  try {
    return JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Record<string, StoredDraft>;
  } catch {
    return {};
  }
}

const store = load();
const cache = new Map<string, string>(Object.entries(store).map(([ref, d]) => [ref, d.text]));
let writeTimer: ReturnType<typeof setTimeout> | undefined;

function scheduleWrite(): void {
  clearTimeout(writeTimer);
  writeTimer = setTimeout(() => {
    try {
      localStorage.setItem(storageKey, JSON.stringify(store));
    } catch {
      // storage full or unavailable: the in-memory cache still works this session
    }
  }, writeDelayMs);
}

// Oldest-updated entries go first once there are more than maxEntries, which
// is how an agent that's since been deleted eventually drops out.
function evictOverflow(): void {
  const refs = Object.keys(store);
  if (refs.length <= maxEntries) return;
  for (const ref of refs.sort((a, b) => store[a].updatedAt - store[b].updatedAt).slice(0, refs.length - maxEntries)) {
    delete store[ref];
    cache.delete(ref);
  }
}

const listeners = new Set<() => void>();
let drafted: ReadonlySet<string> | undefined;

function changed(): void {
  drafted = undefined;
  for (const l of listeners) l();
}

// leadProject is the project a lead's chat ref belongs to, undefined for an
// agent's ("pawly", "pawly/lead" or "pawly/lead.<session>" are the lead's).
export function leadProject(ref: string): string | undefined {
  const [project, name] = ref.split('/');
  return !name || name === LeadName || name.startsWith(`${LeadName}.`) ? project : undefined;
}

// projectsWithDraft is the projects whose lead chat has an unsent draft:
// text beyond whitespace, or an attachment.
export function projectsWithDraft(texts: Iterable<[string, string]>, files: ReadonlyMap<string, number> = new Map()): Set<string> {
  const out = new Set<string>();
  const refs = new Map<string, boolean>();
  for (const [ref, text] of texts) refs.set(ref, text.trim() !== '');
  for (const [ref, n] of files) if (n > 0) refs.set(ref, true);
  for (const [ref, has] of refs) {
    const project = leadProject(ref);
    if (has && project) out.add(project);
  }
  return out;
}

// draftProjects is the same of the drafts kept now; the same set until one changes.
export function draftProjects(): ReadonlySet<string> {
  return (drafted ??= projectsWithDraft(cache, new Map([...attachments].map(([ref, d]) => [ref, d.items.length]))));
}

export const useDraftProjects = () =>
  useSyncExternalStore((l) => {
    listeners.add(l);
    return () => listeners.delete(l);
  }, draftProjects);

export function getDraft(ref: string): string {
  return cache.get(ref) ?? '';
}

export function setDraft(ref: string, text: string): void {
  const before = cache.get(ref) ?? '';
  cache.set(ref, text);
  if (text === '') {
    delete store[ref];
  } else {
    store[ref] = { text, updatedAt: Date.now() };
    evictOverflow();
  }
  scheduleWrite();
  // Only when whether it counts changes would a row read differently.
  if ((before.trim() !== '') !== (text.trim() !== '')) changed();
}

// composeIn puts text at the start of a chat's composer, before what was
// typed there already, with the cursor right after it, as if the user had
// typed it: kept as its draft, and handed to a composer on screen (Composer
// listens for composeEvent). v2's "Talk to another project" uses it, whose
// text is the start of a sentence the draft may well finish.
export const composeEvent = 'agentbox:compose';
export function composeIn(ref: string, lead: string): void {
  const text = composed(lead, getDraft(ref));
  setDraft(ref, text);
  window.dispatchEvent(new CustomEvent(composeEvent, { detail: { ref, text, cursor: lead.length } }));
}

// composed is lead before draft, which loses only its leading blanks.
export function composed(lead: string, draft: string): string {
  return lead + draft.trimStart();
}

// What you attached to each draft (pictures, files, picked elements), kept
// with its text: a chat's composer goes when you switch to another session
// or project, and what it held comes back with it. They are held in memory
// and written through to IndexedDB, so they survive a restart like the text:
// a draft's attachments can be tens of megabytes, past what localStorage
// holds, and would push the text out with them.
export interface AttachmentStore {
  load(): Promise<Record<string, StoredAttachments>>;
  put(ref: string, draft: StoredAttachments): Promise<void>;
  delete(ref: string): Promise<void>;
}

export interface StoredAttachments {
  items: Pending[];
  updatedAt: number;
}

const attachments = new Map<string, StoredAttachments>();
let attachmentStore: AttachmentStore | null = null;
let hydrated: Promise<void> = Promise.resolve();

// setAttachmentStore reads back what the store kept and writes every later
// change to it. What was attached since it was opened wins over what it held.
export function setAttachmentStore(next: AttachmentStore | null): Promise<void> {
  attachmentStore = next;
  hydrated = (next?.load() ?? Promise.resolve<Record<string, StoredAttachments>>({}))
    .then((stored) => {
      const refs = Object.keys(stored).sort((a, b) => stored[b].updatedAt - stored[a].updatedAt);
      for (const ref of refs.slice(maxEntries)) void next?.delete(ref).catch(() => {});
      for (const ref of refs.slice(0, maxEntries)) if (!attachments.has(ref) && stored[ref].items.length) attachments.set(ref, stored[ref]);
      changed();
    })
    .catch(() => {});
  return hydrated;
}

// draftAttachmentsLoaded settles once what a restart left has been read back,
// for a composer that opened before it was.
export function draftAttachmentsLoaded(): Promise<void> {
  return hydrated;
}

export function getDraftAttachments(ref: string): Pending[] {
  return attachments.get(ref)?.items ?? [];
}

export function setDraftAttachments(ref: string, items: Pending[]): void {
  if (items.length === 0) {
    if (!attachments.delete(ref)) return;
    void attachmentStore?.delete(ref).catch(() => {});
    changed();
    return;
  }
  if (attachments.get(ref)?.items === items) return;
  const draft = { items, updatedAt: Date.now() };
  attachments.set(ref, draft);
  void attachmentStore?.put(ref, draft).catch(() => {});
  changed();
}

// indexedDBStore keeps attachments in this renderer's IndexedDB, one record
// per ref.
export function indexedDBStore(name = 'agentbox.drafts'): AttachmentStore {
  const table = 'attachments';
  const db = new Promise<IDBDatabase>((resolve, reject) => {
    const open = indexedDB.open(name, 1);
    open.onupgradeneeded = () => open.result.createObjectStore(table);
    open.onsuccess = () => resolve(open.result);
    open.onerror = () => reject(open.error);
  });
  const run = <R>(mode: IDBTransactionMode, fn: (s: IDBObjectStore) => IDBRequest<R>) =>
    db.then(
      (d) =>
        new Promise<R>((resolve, reject) => {
          const req = fn(d.transaction(table, mode).objectStore(table));
          req.onsuccess = () => resolve(req.result);
          req.onerror = () => reject(req.error);
        }),
    );
  return {
    load: async () => {
      const [keys, values] = await Promise.all([run('readonly', (s) => s.getAllKeys()), run('readonly', (s) => s.getAll())]);
      return Object.fromEntries(keys.map((key, i) => [String(key), values[i] as StoredAttachments]));
    },
    put: (ref, draft) => run('readwrite', (s) => s.put(draft, ref)).then(() => {}),
    delete: (ref) => run('readwrite', (s) => s.delete(ref)).then(() => {}),
  };
}

// Outside a browser (the tests) there is no IndexedDB, and attachments live
// in memory only.
if (typeof indexedDB !== 'undefined') {
  try {
    void setAttachmentStore(indexedDBStore());
  } catch {
    // storage refused: attachments still last as long as the app is open
  }
}

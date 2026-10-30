// What you typed for each agent (or the project lead chat), kept across
// restarts. localStorage already backs the environment choice (see
// web/bridge.ts) and survives in the Electron renderer the same way, so
// drafts use it too rather than adding an IPC round trip to the main process.
import { useSyncExternalStore } from 'react';
import { LeadName } from '../../shared/api.ts';

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

// Attachments aren't kept across restarts (the files are read into the
// composer), so only their count is, in memory, for whether a chat has a draft.
const attachments = new Map<string, number>();
const listeners = new Set<() => void>();
let drafted: ReadonlySet<string> | undefined;

function changed(): void {
  drafted = undefined;
  for (const l of listeners) l();
}

// leadProject is the project a lead's chat ref belongs to, undefined for an
// agent's ("pawly" or "pawly/lead" are the lead's).
export function leadProject(ref: string): string | undefined {
  const [project, name] = ref.split('/');
  return !name || name === LeadName ? project : undefined;
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
  return (drafted ??= projectsWithDraft(cache, attachments));
}

export const useDraftProjects = () =>
  useSyncExternalStore((l) => {
    listeners.add(l);
    return () => listeners.delete(l);
  }, draftProjects);

// setDraftAttachments records how many files wait in a chat's composer.
export function setDraftAttachments(ref: string, count: number): void {
  if ((attachments.get(ref) ?? 0) === count) return;
  if (count > 0) attachments.set(ref, count);
  else attachments.delete(ref);
  changed();
}

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

// What you typed for each agent (or the project lead chat), kept across
// restarts. localStorage already backs the environment choice (see
// web/bridge.ts) and survives in the Electron renderer the same way, so
// drafts use it too rather than adding an IPC round trip to the main process.
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

export function getDraft(ref: string): string {
  return cache.get(ref) ?? '';
}

export function setDraft(ref: string, text: string): void {
  cache.set(ref, text);
  if (text === '') {
    delete store[ref];
  } else {
    store[ref] = { text, updatedAt: Date.now() };
    evictOverflow();
  }
  scheduleWrite();
}

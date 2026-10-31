// The rules of boot.ts, apart from its storage so that Node's test runner
// can load them. A start counts as unfinished from launch until the app has
// stayed up a while or gone to the background; one still unfinished at the
// next launch is a crash on start.

export interface BootRecord {
  // unfinished starts in a row: 0 once a start finished.
  starts: number;
  // the error that ended the last start, when the app saw it.
  error?: string;
}

export type BootMode =
  // the last start finished: start as usual.
  | 'normal'
  // the last start crashed: clear what was kept of the computer's data, then start.
  | 'recovered'
  // the starts before crashed too: wait for the user before syncing again.
  | 'safe';

// After this many crashed starts in a row the app waits for the user.
export const safeAfter = 2;

// A start that stayed up this long finished.
export const healthyAfterMs = 15_000;

export function parseBoot(raw: string | null | undefined): BootRecord {
  if (!raw) return { starts: 0 };
  try {
    const v = JSON.parse(raw) as Partial<BootRecord> | null;
    const starts = typeof v?.starts === 'number' && Number.isFinite(v.starts) && v.starts > 0 ? Math.floor(v.starts) : 0;
    return { starts, error: typeof v?.error === 'string' ? v.error : undefined };
  } catch {
    // Unreadable: count it as a crash, which costs nothing but a cleared cache.
    return { starts: 1 };
  }
}

// begin is what a launch finds and what it records: the mode it starts in,
// the previous start's error, and the record to store before going on.
export function begin(prev: BootRecord): { mode: BootMode; crashes: number; error?: string; record: BootRecord } {
  const crashes = prev.starts;
  const mode: BootMode = crashes >= safeAfter ? 'safe' : crashes > 0 ? 'recovered' : 'normal';
  return { mode, crashes, error: crashes > 0 ? prev.error : undefined, record: { starts: crashes + 1 } };
}

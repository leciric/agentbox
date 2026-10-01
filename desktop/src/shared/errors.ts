// describeError is what a problem report keeps of a thrown value, in the main
// process (main/applog.ts) and in a window (renderer/lib/errorReports.ts):
// its name, its message and its stack's frames, cut to stackFrames and with
// each URL's credentials, query and fragment dropped, as t3code's safeLog
// does. The daemon redacts the rest (internal/report).
export interface DescribedError {
  name: string;
  message: string;
  stack?: string;
}

export const stackFrames = 32;

export function describeError(err: unknown): DescribedError {
  if (err instanceof Error) return { name: err.name || 'Error', message: err.message, stack: frames(err.stack) };
  return { name: 'Error', message: typeof err === 'string' ? err : show(err) };
}

// show is a thrown value that isn't an Error, as text: it may be anything.
function show(v: unknown): string {
  try {
    return JSON.stringify(v) ?? String(v);
  } catch {
    return String(v);
  }
}

// frames are a stack trace's frame lines: V8's "at …" and Firefox's "f@url".
export function frames(stack: string | undefined): string | undefined {
  const lines = stack
    ?.split(/\r?\n/)
    .filter((l) => /^\s*at\s/.test(l) || /^[^@\s]*@(?:https?|file):\/\//.test(l))
    .slice(0, stackFrames)
    .map((l) => l.trim().replace(/((?:https?|file):\/\/[^\s)]+?)((?::\d+){0,2})(?=$|[\s)])/g, (_m, url: string, at: string) => stripUrl(url) + at));
  return lines?.length ? lines.join('\n') : undefined;
}

// stripUrl drops a URL's credentials, query and fragment.
export function stripUrl(u: string): string {
  try {
    const url = new URL(u);
    url.username = '';
    url.password = '';
    url.search = '';
    url.hash = '';
    return url.toString();
  } catch {
    return u;
  }
}

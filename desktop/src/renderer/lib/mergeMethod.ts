// The merge method the user picks first becomes the one the review's Merge
// button uses from then on: remembered per project, with the first pick
// anywhere as the fallback for projects that haven't picked one. Kept in
// localStorage like drafts, with no round trip to the main process.
const storageKey = 'agentbox.mergeMethod';

export interface MethodPrefs {
  global?: string;
  projects?: Record<string, string>;
}

// defaultMethod is the remembered method for project if the repository
// still allows it, else the global one, else the first allowed.
export function defaultMethod(allowed: readonly string[], prefs: MethodPrefs, project: string): string {
  for (const m of [prefs.projects?.[project], prefs.global]) if (m && allowed.includes(m)) return m;
  return allowed[0];
}

// withPick records a pick: it is the project's method, and the global one
// when none was set yet.
export function withPick(prefs: MethodPrefs, project: string, method: string): MethodPrefs {
  return { global: prefs.global ?? method, projects: { ...prefs.projects, [project]: method } };
}

export function loadMethodPrefs(): MethodPrefs {
  try {
    return JSON.parse(globalThis.localStorage?.getItem(storageKey) ?? '{}') as MethodPrefs;
  } catch {
    return {};
  }
}

export function rememberMethod(project: string, method: string): void {
  try {
    globalThis.localStorage?.setItem(storageKey, JSON.stringify(withPick(loadMethodPrefs(), project, method)));
  } catch {
    // storage unavailable: the pick still holds for this session
  }
}

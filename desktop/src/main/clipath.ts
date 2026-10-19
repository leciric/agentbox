// Which agentbox `where.exe agentbox` found, on Windows. where.exe looks in
// the current directory before PATH, and matches with PATHEXT and any case:
// the app, started from its Start menu shortcut, runs in its install folder,
// where AgentBox.exe, the app itself, is "agentbox". Running that with
// --version opened a second AgentBox, on its first-run tour, every time
// Settings checked the command line tool.

// firstCli is the first of where.exe's lines that isn't in the app's own
// folder (appExe is process.execPath), or null.
export function firstCli(whereOut: string | null, appExe: string): string | null {
  const folder = winDir(appExe);
  for (const line of (whereOut ?? '').split(/\r?\n/)) {
    const path = line.trim();
    if (path && winDir(path) !== folder) return path;
  }
  return null;
}

// winDir is a Windows path's folder, as where.exe and Node may spell it.
function winDir(p: string): string {
  const parts = p.replace(/\//g, '\\').replace(/\\+$/, '').toLowerCase().split('\\');
  parts.pop();
  return parts.join('\\');
}

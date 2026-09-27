// Paths on the daemon's machine, which is Linux even when the app runs
// elsewhere: always '/'-separated.

// joinPath puts a folder name under a parent, or is '' until both are there.
export function joinPath(parent: string, name: string): string {
  if (!parent || !name) return '';
  return `${parent === '/' ? '' : parent.replace(/\/+$/, '')}/${name}`;
}

// parentOf is the folder a path is in.
export function parentOf(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const slash = trimmed.lastIndexOf('/');
  return slash <= 0 ? '/' : trimmed.slice(0, slash);
}

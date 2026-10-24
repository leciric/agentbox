// Stacked work: an agent made from another agent's branch stands on it, and
// its pull request targets that branch rather than the project's. stackOrder
// puts such a chain together — each item right after the one it stands on,
// indented by how deep it is — and leaves everything else in its order.

export interface Stacked<T> {
  item: T;
  // depth is 0 for an item standing on nothing listed, 1 for one on it, …
  depth: number;
  parent?: T;
  children: T[];
}

export function stackOrder<T>(items: readonly T[], head: (item: T) => string | undefined, base: (item: T) => string | undefined): Stacked<T>[] {
  const byHead = new Map<string, T>();
  for (const item of items) {
    const h = head(item);
    if (h && !byHead.has(h)) byHead.set(h, item);
  }
  const parentOf = new Map<T, T>();
  for (const item of items) {
    const b = base(item);
    const parent = b ? byHead.get(b) : undefined;
    if (parent && parent !== item) parentOf.set(item, parent);
  }
  const childrenOf = new Map<T, T[]>();
  for (const item of items) {
    const parent = parentOf.get(item);
    if (parent) childrenOf.set(parent, [...(childrenOf.get(parent) ?? []), item]);
  }
  const out: Stacked<T>[] = [];
  const placed = new Set<T>();
  const place = (item: T, depth: number) => {
    if (placed.has(item)) return;
    placed.add(item);
    const children = childrenOf.get(item) ?? [];
    out.push({ item, depth, parent: depth > 0 ? parentOf.get(item) : undefined, children });
    for (const child of children) place(child, depth + 1);
  };
  for (const item of items) {
    // A root is one whose parent isn't listed; a cycle, which branches can't
    // really make, is broken at its first item.
    if (!parentOf.has(item)) place(item, 0);
  }
  for (const item of items) place(item, 0);
  return out;
}

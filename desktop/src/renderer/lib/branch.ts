// What comes after a project's prefix in an agent's branch. These mirror
// internal/agent/branch.go, which has the tests and the final say: the daemon
// checks the slug again, and appends -2, -3… to one that is taken.

export const maxBranchSlugLen = 48;

export const branchSlugPattern = '[a-z0-9]+(-[a-z0-9]+)*';

// branchSlug turns a title into a branch slug: lowercase kebab-case, cut at a
// word boundary to fit maxBranchSlugLen.
export function branchSlug(title: string): string {
  const line = title.trim().split('\n')[0];
  const slug = line
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  if (slug.length <= maxBranchSlugLen) return slug;
  const cut = slug.slice(0, maxBranchSlugLen + 1);
  const i = cut.lastIndexOf('-');
  return i > 0 ? cut.slice(0, i) : cut.slice(0, maxBranchSlugLen).replace(/-+$/, '');
}

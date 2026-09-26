// changelog.md is copied from the repo root's CHANGELOG.md by scripts/build.mjs
// before Vite runs, so this always resolves at build time — never over the
// network or from the git repo at runtime.
declare module '*.md?raw' {
  const content: string;
  export default content;
}

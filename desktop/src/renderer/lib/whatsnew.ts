// changelog.md is copied from the repo root's CHANGELOG.md at build time
// (desktop/scripts/build.mjs) and inlined here by Vite's `?raw` import: the
// What's new view has nothing to fetch, packaged or not.
import raw from '../changelog.md?raw';
import { parseChangelog, sectionsFrom, type ChangelogSection } from './changelog';

export type { ChangelogSection };

const sections = parseChangelog(raw);

export function whatsNewFrom(version: string): ChangelogSection[] {
  return sectionsFrom(sections, version);
}

// Where the version What's new was last shown for is remembered, so it can be
// shown automatically once after an update — never on the very first run,
// since nobody wants "what's new" the first time they open the app.
const seenKey = 'agentbox.whatsnew.lastSeenVersion';

export function shouldShowAutomatically(version: string): boolean {
  try {
    const seen = localStorage.getItem(seenKey);
    return seen !== null && seen !== version;
  } catch {
    return false;
  }
}

export function markSeen(version: string): void {
  try {
    localStorage.setItem(seenKey, version);
  } catch {
    // A browser that refuses storage: What's new just won't auto-show.
  }
}

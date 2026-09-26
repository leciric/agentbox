// Parses CHANGELOG.md's Keep a Changelog shape, so the What's new view can
// render it. Kept apart from the file it actually reads (lib/whatsnew.ts,
// which imports the bundled changelog.md) so this can be unit tested without
// Vite's `?raw` import.

export interface ChangelogSection {
  version: string;
  body: string;
}

export function parseChangelog(raw: string): ChangelogSection[] {
  const sections: ChangelogSection[] = [];
  let current: ChangelogSection | undefined;
  for (const line of raw.split('\n')) {
    const heading = /^## (.+)$/.exec(line);
    if (heading) {
      current = { version: heading[1].trim(), body: '' };
      sections.push(current);
      continue;
    }
    if (current) current.body += line + '\n';
  }
  for (const section of sections) section.body = section.body.trim();
  return sections;
}

// sectionsFrom returns every released section from `version` down to the
// oldest the changelog keeps, skipping Unreleased. Falls back to every
// released section when `version` isn't in the changelog: a development build,
// or one cut before its own release notes landed.
export function sectionsFrom(sections: ChangelogSection[], version: string): ChangelogSection[] {
  const released = sections.filter((s) => s.version !== 'Unreleased');
  const at = released.findIndex((s) => s.version === version);
  return at === -1 ? released : released.slice(at);
}

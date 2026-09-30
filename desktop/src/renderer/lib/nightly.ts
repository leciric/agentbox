// Nightly builds: what .github/workflows/nightly.yml builds from the release
// PR while it carries the nightly label, versioned
// <next release>-nightly.<YYYYMMDD>.<run>. A nightly app wears a starry sidebar
// header and a Nightly badge, so nobody mistakes it for a release; the pattern
// is internal/update's IsNightly, and the two stay in step.
const nightlyVersion = /^v?\d+\.\d+\.\d+-nightly\.\d{8}\.\d+$/;

export function isNightly(version: string | undefined): boolean {
  return !!version && nightlyVersion.test(version.trim());
}

// releaseOf is the release a version is or leads up to: 0.11.0 for
// 0.11.0-nightly.20260929.12.
function releaseOf(version: string): number[] {
  return version
    .replace(/^v/, '')
    .replace(/[-+].*$/, '')
    .split('.')
    .map(Number);
}

// isUpgrade says whether the update check's offer is newer than current,
// rather than the latest stable offered to go back to from a nightly, which
// can be lower (internal/update's Offer). Both are semver; a nightly comes
// before the release it leads up to, and after every earlier one.
export function isUpgrade(offer: string, current: string): boolean {
  const [a, b] = [releaseOf(offer), releaseOf(current)];
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return (a[i] ?? 0) > (b[i] ?? 0);
  if (isNightly(offer) && isNightly(current)) return offer.localeCompare(current, 'en', { numeric: true }) > 0;
  return isNightly(current) && !isNightly(offer);
}

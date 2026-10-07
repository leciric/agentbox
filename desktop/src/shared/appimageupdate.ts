// Updating the app in place, where it can be (main/appimageupdate.ts): what the
// main process tells the renderer about it, shared by both.

// AppUpdateState is where an in-place update stands. idle is nothing asked
// for yet; ready is downloaded and waiting for a restart; failed carries why,
// and the renderer falls back to opening the release page.
export type AppUpdateState =
  | { state: 'idle' }
  | { state: 'downloading'; version: string; percent: number }
  | { state: 'ready'; version: string }
  | { state: 'installing'; version: string }
  | { state: 'failed'; version: string; error: string };

// releaseFeed is the directory a release's assets download from, given its
// page as the daemon reports it (GET /v1/update/release): the R2 bucket
// releases are published to (scripts/r2-publish.sh) holds .../releases/v1.2.3/
// index.html beside that release's assets. That directory is the update feed, so the app installs exactly the release the
// daemon picked for the update channel, nightly or stable, never one
// electron-updater would choose by itself. Undefined for anything else.
export function releaseFeed(page: string): string | undefined {
  let url: URL;
  try {
    url = new URL(page);
  } catch {
    return undefined;
  }
  const m = /^(.*\/releases\/[^/]+\/)index\.html$/.exec(url.pathname);
  if (!m || (url.protocol !== 'https:' && url.protocol !== 'http:')) return undefined;
  url.pathname = m[1];
  url.search = url.hash = '';
  return url.toString();
}

// isPrerelease says a version is a prerelease (a nightly, 0.12.0-nightly.…).
export function isPrerelease(version: string): boolean {
  return /^\d+\.\d+\.\d+-/.test(version.replace(/^v/, ''));
}

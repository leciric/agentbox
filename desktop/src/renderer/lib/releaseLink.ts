import type { AppUpdateState } from '../../shared/appUpdate';

// openLatestRelease is what "Update available", and Settings' links to a new
// release, open: the update channel's latest release as the daemon finds it on
// GitHub at the moment of the click (GET /v1/update/release), not the page the
// last daily check pinned, which releases a few hours apart left behind. When
// the daemon can't say, the pinned page is still better than nothing.
export async function openLatestRelease(
  latest: () => Promise<{ url: string }>,
  open: (url: string) => unknown,
  pinned: string,
): Promise<void> {
  let url = pinned;
  try {
    url = (await latest()).url || pinned;
  } catch {
    // The daemon or GitHub unreachable: the pinned page.
  }
  await open(url);
}

// AppUpdater is the main process's in-place update (main/appupdate.ts), as
// the bridge offers it.
export interface AppUpdater {
  supported: () => Promise<boolean>;
  download: (release: { version: string; url: string }) => Promise<AppUpdateState>;
}

// updateOrOpen is "Update available" clicked: in an AppImage it downloads the
// update channel's latest release, to restart into once it's ready; anywhere
// else, or when the download fails, it opens the release page as
// openLatestRelease does, and a failure is passed to failed to say why.
export async function updateOrOpen(
  latest: () => Promise<{ version: string; url: string }>,
  updater: AppUpdater,
  open: (url: string) => unknown,
  pinned: { version: string; url: string },
  failed: (error: string) => void,
): Promise<void> {
  let release = pinned;
  try {
    const found = await latest();
    if (found.url) release = found;
  } catch {
    // The daemon or GitHub unreachable: the pinned release.
  }
  const supported = await updater.supported().catch(() => false);
  if (!supported) {
    await open(release.url);
    return;
  }
  let state: AppUpdateState;
  try {
    state = await updater.download(release);
  } catch (err) {
    state = { state: 'failed', version: release.version, error: err instanceof Error ? err.message : String(err) };
  }
  if (state.state === 'failed') {
    failed(state.error);
    await open(release.url);
  }
}

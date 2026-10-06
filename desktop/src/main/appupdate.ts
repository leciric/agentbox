// In-place updates, for the AppImage: "Update available" downloads the new
// AppImage and swaps it in for the running one, then restarts into it, the way
// t3code updates itself, with electron-updater. Every other kind of install
// (.deb, .pacman, Windows, the Mac) still opens the release page: a package
// manager owns the first two, and the others would need their own updater
// and signing.
//
// The release comes from the daemon (GET /v1/update/release), which already
// knows the update channel, and electron-updater is pointed at that release's
// own assets (releaseFeed), where release-build.sh publishes latest-linux.yml.
// electron-updater checks the download's sha512 against it, and downloads
// only the blocks that changed when it can (the AppImage carries its
// blockmap).
//
// Once restarted, the app finds the daemon at the old version and restarts it
// from its own binary (daemon.ts's restartIfStale); in VM mode, starting it
// installs that binary in the VM first (hostvm's Ready).
//
// This file holds no Electron, so its tests run under node: index.ts hands it
// electron-updater's AppImageUpdater.
import { type AppUpdateState, isPrerelease, releaseFeed } from '../shared/appUpdate.ts';

// Updater is what's used of electron-updater's AppImageUpdater.
export interface Updater {
  autoDownload: boolean;
  autoInstallOnAppQuit: boolean;
  allowPrerelease: boolean;
  allowDowngrade: boolean;
  setFeedURL(options: { provider: 'generic'; url: string }): void;
  checkForUpdates(): Promise<{ isUpdateAvailable: boolean; updateInfo: { version: string } } | null>;
  downloadUpdate(): Promise<unknown>;
  quitAndInstall(isSilent?: boolean, isForceRunAfter?: boolean): void;
  on(event: 'download-progress', fn: (progress: { percent: number }) => void): unknown;
  on(event: 'error', fn: (err: Error) => void): unknown;
  off(event: 'error', fn: (err: Error) => void): unknown;
}

export class AppUpdates {
  private current: AppUpdateState = { state: 'idle' };
  private readonly updater: Updater;
  private readonly version: string;
  private readonly publish: (state: AppUpdateState) => void;

  constructor(updater: Updater, version: string, publish: (state: AppUpdateState) => void) {
    this.updater = updater;
    this.version = version;
    this.publish = publish;
    updater.autoDownload = false;
    updater.autoInstallOnAppQuit = false;
    // The feed is one release's, chosen by the daemon: a nightly when the
    // channel is nightly.
    updater.allowPrerelease = true;
    updater.on('download-progress', ({ percent }) => {
      if (this.current.state !== 'downloading') return;
      const rounded = Math.floor(percent);
      if (rounded !== this.current.percent) this.set({ ...this.current, percent: rounded });
    });
  }

  get state(): AppUpdateState {
    return this.current;
  }

  // download fetches the release given, and answers with where that left the
  // update: ready, or failed with why. A download already under way, or one
  // that's ready, is answered as it stands.
  async download(release: { version: string; url: string }): Promise<AppUpdateState> {
    const { state } = this.current;
    if (state === 'downloading' || state === 'installing') return this.current;
    if (state === 'ready' && this.current.version === release.version) return this.current;
    const version = release.version.replace(/^v/, '');
    const feed = releaseFeed(release.url);
    if (!feed) return this.fail(version, `${release.url} isn't a release page`);
    if (version === this.version) return this.fail(version, `this is already ${version}`);
    this.set({ state: 'downloading', version, percent: 0 });
    try {
      this.updater.setFeedURL({ provider: 'generic', url: feed });
      // The stable channel offers a nightly build the latest stable release,
      // a lower version, to go back to (internal/update's Offer); nothing else
      // goes backwards.
      this.updater.allowDowngrade = isPrerelease(this.version) && !isPrerelease(version);
      const found = await this.updater.checkForUpdates();
      if (!found) throw new Error('this app is not an AppImage that can update itself');
      if (found.updateInfo.version !== version) throw new Error(`the release offers ${found.updateInfo.version}, not ${version}`);
      if (!found.isUpdateAvailable) throw new Error(`${version} is not an update for ${this.version}`);
      await this.updater.downloadUpdate();
      this.set({ state: 'ready', version });
    } catch (err) {
      return this.fail(version, err instanceof Error ? err.message : String(err));
    }
    return this.current;
  }

  // install restarts into the downloaded update: electron-updater quits the
  // app, moves the new AppImage over the old one and starts it.
  install(): AppUpdateState {
    if (this.current.state !== 'ready') return this.current;
    const { version } = this.current;
    this.set({ state: 'installing', version });
    // electron-updater reports a failed install as an error event, not by
    // throwing.
    let failed: unknown;
    const onError = (err: Error) => (failed = err);
    this.updater.on('error', onError);
    try {
      this.updater.quitAndInstall(true, true);
    } catch (err) {
      failed = err;
    } finally {
      this.updater.off('error', onError);
    }
    if (failed !== undefined) return this.fail(version, failed instanceof Error ? failed.message : String(failed));
    return this.current;
  }

  private fail(version: string, error: string): AppUpdateState {
    this.set({ state: 'failed', version, error });
    return this.current;
  }

  private set(state: AppUpdateState): void {
    this.current = state;
    this.publish(state);
  }
}

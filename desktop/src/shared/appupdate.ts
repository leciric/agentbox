// Updating the app in place (main/appupdate.ts), as the renderer sees it.

// How this copy of the app was installed decides whether it can update itself
// without a password: a Mac's .app in a folder the user can write to, an
// AppImage, and on Windows the per-user installer's install or the portable
// .exe can; anything else opens the release page, for the reason given.
export type AppUpdateSupport = { inPlace: true; kind: InstallKind } | { inPlace: false; reason: NoInPlaceUpdate };

export type InstallKind = 'mac' | 'appimage' | 'nsis' | 'portable';

// dev: not a packaged app. macDiskImage: running from the .dmg it came in.
// macTranslocated: macOS runs it from a read-only copy (App Translocation),
// until it's moved to Applications. linuxPackage: a .deb or .pacman, which only
// root can replace. notWritable: the folder the app is in isn't the user's to
// write to. unknown: an install this app doesn't recognise.
export type NoInPlaceUpdate = 'dev' | 'macDiskImage' | 'macTranslocated' | 'linuxPackage' | 'notWritable' | 'unknown';

export type AppUpdateProgress =
  | { phase: 'download'; version: string; received: number; total: number }
  | { phase: 'verify' | 'install' | 'restart'; version: string };

// Why an update stopped. Nothing was replaced unless the code says so: a
// failed swap puts the old app back.
export type AppUpdateErrorCode =
  | 'unsupported' // see AppUpdateSupport
  | 'busy' // the daemon is running jobs, which a restart would cut short
  | 'upToDate' // the latest release is this one
  | 'noRelease' // the daemon couldn't say what the latest release is
  | 'noAssets' // GitHub couldn't be reached for the release's files
  | 'noBuild' // the release has no package for this OS and architecture
  | 'noChecksum' // the release's SHA256SUMS doesn't list the package
  | 'download' // the download failed
  | 'checksum' // the package isn't what SHA256SUMS says
  | 'signature' // the new Mac app's code signature or Info.plist didn't check out
  | 'install'; // unpacking or replacing failed

export type AppUpdateResult = { ok: true } | { ok: false; code: AppUpdateErrorCode; detail: string; url?: string };

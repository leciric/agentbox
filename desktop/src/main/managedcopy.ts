// Who the managed copy of the agentbox tool belongs to (cli.ts), now that two
// apps keep it: this one and the native app (native/crates/app/src/cli.rs).
// Each writes the copy only when it differs from its own, and never over the
// other app's unless that one is an older version: otherwise each would undo
// the other every time it started, and restart the daemon with it. Two apps
// of one version are two builds of the same tool, and whichever wrote first
// keeps it. Who wrote the copy
// is in .agentbox-owner beside it ("electron" or "native"), so an app that
// went back a version itself (a nightly to the stable release) still gets
// its own tool. A copy with no owner is this app's, from before it wrote one.
//
// This file holds no Electron, so its tests run under node.
import { isUpgrade } from '../renderer/lib/nightly.ts';

export const ownerName = '.agentbox-owner';
export const me = 'electron';

// leftAlone says whether a managed copy that differs from this app's tool
// stays: owner is what .agentbox-owner holds (null when there is none),
// theirs the copy's version (null when it wouldn't say) and ours this app's.
export function leftAlone(owner: string | null, theirs: string | null, ours: string): boolean {
  if (owner === null || owner.trim() === me) return false;
  // A copy that won't say its version, or is a build from a checkout ("dev").
  if (theirs === null || !/^v?\d+\.\d+\.\d+/.test(theirs)) return false;
  return !isUpgrade(ours, theirs);
}

// versionSaid reads `agentbox --version`'s answer: "agentbox version 0.18.0".
export function versionSaid(said: string | null): string | null {
  const m = /^agentbox version (\S+)$/.exec((said ?? '').trim());
  return m ? m[1] : null;
}

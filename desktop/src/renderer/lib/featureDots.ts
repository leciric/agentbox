// Feature dots: a small dot on what a recent release added, where it lives,
// for someone who updated past that release, until they first use it. What's
// new lists the same features and shows each one's dot (WhatsNewDialog).
//
// A feature's anchor is the place it lives: a sidebar foot icon, a panel tab,
// a button, a Settings section. When that place is inside another (a Settings
// section, the right panel of a lead or of an agent), the dot shows on its
// parents too, so
// it can be found from wherever the app is; using the anchor itself clears it.
//
// Which dots were cleared is kept by the daemon (Settings.seenFeatures), so a
// feature used once is known wherever the app runs. Which release this app
// was on before the update is this computer's, beside What's new's own
// (lib/whatsnew.ts): a first run has no "before", and shows no dots, since
// the welcome tour covers the app as it is.
import type { MessageKey } from './i18n';
import { releaseOf } from './nightly.ts';

export type Anchor =
  | 'foot:settings'
  | 'foot:work'
  | 'foot:media'
  | 'panel-toggle:lead'
  | 'panel-toggle:agent'
  | 'lead-tab:issues'
  | 'lead-tab:schedules'
  | 'share'
  | 'pick'
  | 'settings:diagnostics'
  | 'settings:experimental';

export type Feature = {
  // id is what the daemon remembers once it's used: never reuse one.
  id: string;
  // version is the release it shipped in.
  version: string;
  anchor: Anchor;
  // parents are the places the anchor is inside of.
  parents?: Anchor[];
  // hint is the one line shown on hover over the anchor, and in What's new.
  hint: MessageKey;
};

// features is the registry, newest first, from CHANGELOG.md's Added. Add a
// feature here when it ships somewhere a dot can mark, and mark its anchor
// with useFeatureDot (components/FeatureDot.tsx).
export const features: Feature[] = [
  { id: 'work-page', version: '0.16.0', anchor: 'foot:work', hint: 'featureDots.workPage' },
  { id: 'issues', version: '0.16.0', anchor: 'lead-tab:issues', parents: ['panel-toggle:lead'], hint: 'featureDots.issues' },
  { id: 'share', version: '0.16.0', anchor: 'share', parents: ['panel-toggle:agent'], hint: 'featureDots.share' },
  { id: 'experimental-memory', version: '0.16.0', anchor: 'settings:experimental', parents: ['foot:settings'], hint: 'featureDots.experimental' },
  { id: 'diagnostics', version: '0.15.0', anchor: 'settings:diagnostics', parents: ['foot:settings'], hint: 'featureDots.diagnostics' },
  { id: 'pick', version: '0.15.0', anchor: 'pick', parents: ['panel-toggle:agent'], hint: 'featureDots.pick' },
  { id: 'schedules', version: '0.15.0', anchor: 'lead-tab:schedules', parents: ['panel-toggle:lead'], hint: 'featureDots.schedules' },
  { id: 'media-foot', version: '0.14.0', anchor: 'foot:media', hint: 'featureDots.media' },
];

// compareReleases orders two versions by the release they are or lead up
// to, -1, 0 or 1, and is null when either isn't a version (a dev build's).
// A nightly counts as its release: whoever ran 0.16.0-nightly.… had 0.16's
// features already.
export function compareReleases(a: string, b: string): -1 | 0 | 1 | null {
  const [x, y] = [releaseOf(a), releaseOf(b)];
  if (x.length !== 3 || y.length !== 3 || [...x, ...y].some((n) => !Number.isInteger(n))) return null;
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] > y[i] ? 1 : -1;
  return 0;
}

export type DotState = {
  // since is the release this app was on before the update, null on a first run.
  since: string | null;
  current: string;
  seen: readonly string[];
};

// pending is the features to mark: shipped after since and by current (any,
// on a dev build), and not used yet.
export function pending(state: DotState, list: readonly Feature[] = features): Feature[] {
  if (state.since === null) return [];
  const since = state.since;
  return list.filter((f) => compareReleases(f.version, since) === 1 && (compareReleases(f.version, state.current) ?? -1) <= 0 && !state.seen.includes(f.id));
}

// dotAt is what an anchor shows: the features it's the place of (which using
// it clears), then those inside it.
export function dotAt(anchor: Anchor, list: readonly Feature[]): { own: Feature[]; inside: Feature[] } {
  return { own: list.filter((f) => f.anchor === anchor), inside: list.filter((f) => f.parents?.includes(anchor)) };
}

// ——— The release before the update, on this computer ———

const sinceKey = 'agentbox.featureDots.since';
// What's new's own record of the last version it was shown for, written once
// a version has been opened: there is none on a first run.
const whatsNewKey = 'agentbox.whatsnew.lastSeenVersion';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

// rememberSince settles, once, which release dots count from: the version
// What's new last saw, when this app ran before; the current one on a first
// run, which marks nothing until the next update. It must run before What's
// new marks the current version seen. It returns since, or null when it
// can't be kept.
export function rememberSince(current: string, store: Store = localStorage): string | null {
  try {
    const kept = store.getItem(sinceKey);
    if (kept !== null) return kept;
    const since = store.getItem(whatsNewKey) ?? current;
    store.setItem(sinceKey, since);
    return since;
  } catch {
    return null;
  }
}

// since reads what rememberSince kept, null when nothing was.
export function since(store: Store = localStorage): string | null {
  try {
    return store.getItem(sinceKey);
  } catch {
    return null;
  }
}

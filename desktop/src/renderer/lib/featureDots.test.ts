import assert from 'node:assert/strict';
import { test } from 'node:test';
import { compareReleases, dotAt, features, pending, rememberSince, since, type Feature } from './featureDots.ts';

const memory = (init: Record<string, string> = {}) => {
  const m = new Map(Object.entries(init));
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), m };
};

test('releases compare by number, a nightly as its release, and a dev build not at all', () => {
  assert.equal(compareReleases('0.16.0', '0.15.0'), 1);
  assert.equal(compareReleases('0.9.1', '0.10.0'), -1);
  assert.equal(compareReleases('v0.16.0', '0.16.0'), 0);
  assert.equal(compareReleases('0.16.0', '0.16.0-nightly.20261024.3'), 0);
  assert.equal(compareReleases('0.17.0-nightly.20261030.156', '0.16.0'), 1);
  assert.equal(compareReleases('1.0.0', '0.99.99'), 1);
  assert.equal(compareReleases('dev', '0.16.0'), null);
  assert.equal(compareReleases('0.16', '0.16.0'), null);
});

const list: Feature[] = [
  { id: 'a', version: '0.16.0', anchor: 'foot:work', hint: 'featureDots.workPage' },
  { id: 'b', version: '0.15.0', anchor: 'settings:diagnostics', parents: ['foot:settings'], hint: 'featureDots.diagnostics' },
  { id: 'c', version: '0.14.0', anchor: 'foot:media', hint: 'featureDots.media' },
  { id: 'd', version: '0.17.0', anchor: 'share', hint: 'featureDots.share' },
];
const ids = (fs: Feature[]) => fs.map((f) => f.id);

test('an update marks what shipped after the release it came from, up to the one running', () => {
  assert.deepEqual(ids(pending({ since: '0.14.0', current: '0.16.0', seen: [] }, list)), ['a', 'b']);
  assert.deepEqual(ids(pending({ since: '0.15.2', current: '0.16.1', seen: [] }, list)), ['a']);
  // A nightly of the next release has its features.
  assert.deepEqual(ids(pending({ since: '0.16.0', current: '0.17.0-nightly.20261030.1', seen: [] }, list)), ['d']);
  // A dev build marks everything since.
  assert.deepEqual(ids(pending({ since: '0.15.0', current: 'dev', seen: [] }, list)), ['a', 'd']);
});

test('a first run marks nothing, and neither does staying on a release', () => {
  assert.deepEqual(pending({ since: null, current: '0.16.0', seen: [] }, list), []);
  assert.deepEqual(pending({ since: '0.16.0', current: '0.16.0', seen: [] }, list), []);
});

test('a feature used once is cleared, and only that one', () => {
  assert.deepEqual(ids(pending({ since: '0.13.0', current: '0.16.0', seen: ['b'] }, list)), ['a', 'c']);
  assert.deepEqual(pending({ since: '0.13.0', current: '0.16.0', seen: ['a', 'b', 'c'] }, list), []);
});

test('a nested feature shows on its anchor and its parent, and is the anchor\'s own', () => {
  const marked = pending({ since: '0.13.0', current: '0.16.0', seen: [] }, list);
  assert.deepEqual(ids(dotAt('settings:diagnostics', marked).own), ['b']);
  assert.deepEqual(ids(dotAt('foot:settings', marked).own), []);
  assert.deepEqual(ids(dotAt('foot:settings', marked).inside), ['b']);
  assert.deepEqual(dotAt('foot:settings', pending({ since: '0.13.0', current: '0.16.0', seen: ['b'] }, list)).inside, []);
});

test('the release dots count from is settled once: What\'s new\'s last version, or this one on a first run', () => {
  const fresh = memory();
  assert.equal(rememberSince('0.16.0', fresh), '0.16.0');
  const updated = memory({ 'agentbox.whatsnew.lastSeenVersion': '0.14.0' });
  assert.equal(rememberSince('0.16.0', updated), '0.14.0');
  // What's new then marks 0.16.0 seen, and later launches and updates keep 0.14.0.
  updated.setItem('agentbox.whatsnew.lastSeenVersion', '0.16.0');
  assert.equal(rememberSince('0.17.0', updated), '0.14.0');
  assert.equal(since(updated), '0.14.0');
  const refusing = { getItem: () => { throw new Error('no storage'); }, setItem: () => {} };
  assert.equal(rememberSince('0.16.0', refusing), null);
  assert.equal(since(refusing), null);
});

test('the registry is well formed', () => {
  const seen = new Set<string>();
  for (const f of features) {
    assert.match(f.id, /^[a-z0-9][a-z0-9-]{0,63}$/, `${f.id}: the daemon refuses this id`);
    assert.ok(!seen.has(f.id), `${f.id} twice`);
    seen.add(f.id);
    assert.equal(compareReleases(f.version, f.version), 0, `${f.id}: ${f.version} isn't a release`);
    assert.ok(!f.parents?.includes(f.anchor), `${f.id} is its own parent`);
  }
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { leftAlone, versionSaid } from './managedcopy.ts';

const older = '0.18.0-nightly.20261104.168';
const ours = '0.18.0-nightly.20261106.179';
const newer = '0.18.0-nightly.20261112.187';

test("leftAlone: the native app's copy stays unless it is older", () => {
  assert.equal(leftAlone('native', newer, ours), true);
  assert.equal(leftAlone('native\n', newer, ours), true);
  assert.equal(leftAlone('native', '0.18.0', ours), true); // the release its nightlies lead up to
  // The same version is another build of the same tool: were each app to
  // write its own, they would take turns for good.
  assert.equal(leftAlone('native', ours, ours), true);
  // Older: this app's replaces it.
  assert.equal(leftAlone('native', older, ours), false);
  assert.equal(leftAlone('native', '0.17.2', ours), false);
  // A copy that won't say its version is replaced.
  assert.equal(leftAlone('native', null, ours), false);
  assert.equal(leftAlone('native', 'dev', ours), false);
});

test('leftAlone: a newer copy this app wrote itself is replaced, as when it goes back to the stable release', () => {
  assert.equal(leftAlone('electron', newer, ours), false);
  assert.equal(leftAlone('electron', '0.19.0-nightly.20261201.200', '0.18.0'), false);
  // No owner: written before the app recorded one, so by this app.
  assert.equal(leftAlone(null, newer, ours), false);
});

test('versionSaid reads the tool’s answer', () => {
  assert.equal(versionSaid('agentbox version 0.18.0-nightly.20261106.179\n'), ours);
  assert.equal(versionSaid('agentbox version dev'), 'dev');
  assert.equal(versionSaid('something else'), null);
  assert.equal(versionSaid(null), null);
});

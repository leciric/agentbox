import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isOutdated, type StaleDaemon, StaleTracker } from './staledaemon.ts';

// fake is a daemon of version, running jobs, whose restart brings up next.
function fake(version: string, jobs: number, next = 'app') {
  const d = {
    version,
    jobs,
    restarts: 0,
    published: [] as (StaleDaemon | null)[],
    pending: undefined as (() => void) | undefined,
    looks: 0,
  };
  const tracker = new StaleTracker(
    'app',
    {
      version: async () => d.version,
      runningJobs: async () => d.jobs,
      restart: async () => {
        d.restarts++;
        d.version = next;
      },
      publish: (s) => d.published.push(s),
      later: (fn) => {
        d.pending = fn;
        return () => (d.pending = undefined);
      },
    },
    () => d.looks++,
  );
  return { d, tracker };
}

test('isOutdated: only a packaged app against a released daemon of another version', () => {
  assert.equal(isOutdated('0.15.0', '0.17.0-nightly.20261028.152'), true);
  assert.equal(isOutdated('0.16.0', '0.16.0'), false);
  assert.equal(isOutdated('dev', '0.16.0'), false);
  assert.equal(isOutdated('0.15.0', undefined), false);
  assert.equal(isOutdated(undefined, '0.16.0'), false);
});

test('an idle stale daemon is restarted at once, and nothing is shown', async () => {
  const { d, tracker } = fake('0.15.0', 0);
  await tracker.handle('0.15.0');
  assert.equal(d.restarts, 1);
  assert.equal(tracker.current, null);
  assert.deepEqual(d.published, []);
});

// The update landed while a job ran: the old daemon used to be left for good.
test('a stale daemon running jobs is shown, looked at again, and restarted once they are done', async () => {
  const { d, tracker } = fake('0.15.0', 2);
  await tracker.handle('0.15.0');
  assert.equal(d.restarts, 0);
  assert.deepEqual(tracker.current, { daemon: '0.15.0', app: 'app', jobs: 2 });
  assert.ok(d.pending, 'no second look was scheduled');

  d.pending!();
  assert.equal(d.looks, 1);
  d.jobs = 0;
  await tracker.handle('0.15.0');
  assert.equal(d.restarts, 1);
  assert.equal(tracker.current, null);
  assert.deepEqual(d.published, [{ daemon: '0.15.0', app: 'app', jobs: 2 }, null]);
});

test('Restart now restarts a busy stale daemon', async () => {
  const { d, tracker } = fake('0.15.0', 1);
  await tracker.handle('0.15.0');
  await tracker.restartNow();
  assert.equal(d.restarts, 1);
  assert.equal(tracker.current, null);
  assert.equal(d.pending, undefined, 'the second look was left scheduled');
});

// Its binary wasn't replaced: restarting it again on every reconnect would
// loop, so it stays shown, and only Restart now tries again.
test('a daemon that comes back the same version is shown and not restarted again', async () => {
  const { d, tracker } = fake('0.15.0', 0, '0.15.0');
  await tracker.handle('0.15.0');
  assert.deepEqual(tracker.current, { daemon: '0.15.0', app: 'app', jobs: 0 });
  await tracker.handle('0.15.0');
  assert.equal(d.restarts, 1);
  await tracker.restartNow();
  assert.equal(d.restarts, 2);
});

test('a daemon of the app’s version clears what was shown', async () => {
  const { d, tracker } = fake('0.15.0', 1);
  await tracker.handle('0.15.0');
  tracker.clear();
  assert.equal(tracker.current, null);
  assert.equal(d.pending, undefined);
  await tracker.restartNow();
  assert.equal(d.restarts, 0, 'Restart now restarted a daemon that isn’t stale');
});

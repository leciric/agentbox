// Run with `npm test`: the parts of the Windows cookie reader that don't need
// Windows — the CDP-cookie mapping, the running-browser checks, the
// consistent copy of the cookie store, and the platform guard.
import assert from 'node:assert/strict';
import { chmodSync, copyFileSync, existsSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { browserRunning, cdpGetCookies, emptyResult, exeRunning, extractBrowserCookies, lockHeld, snapshotCookies, toImported } from './browsercookies.ts';

// frame is one CDP message as the pipe carries it: JSON ended by a NUL byte.
function frame(obj: unknown): Buffer {
  return Buffer.concat([Buffer.from(JSON.stringify(obj)), Buffer.from([0])]);
}

test('a session cookie maps to expires 0, a dated one to its second', () => {
  assert.deepEqual(
    toImported({ name: 'a', value: 'b', domain: '.github.com', path: '/', expires: -1, secure: true, httpOnly: true, session: true, sameSite: 'Lax' }),
    { domain: '.github.com', name: 'a', value: 'b', path: '/', expires: 0, secure: true, httpOnly: true, sameSite: 'Lax' },
  );
  const dated = toImported({ name: 'x', value: 'y', domain: 'linear.app', path: '', expires: 1799999999.7, secure: false, httpOnly: false, session: false });
  assert.equal(dated.expires, 1799999999);
  assert.equal(dated.path, '/'); // an empty path defaults to /
  assert.equal(dated.sameSite, ''); // absent sameSite becomes ''
});

test('the pipe transport sends getCookies and parses the framed reply', async () => {
  const write = new PassThrough();
  const read = new PassThrough();
  const sent: Buffer[] = [];
  write.on('data', (c: Buffer) => sent.push(c));

  const p = cdpGetCookies(write, read);
  // The command written to fd 3 is Storage.getCookies, id 1, NUL-terminated.
  const cmd = Buffer.concat(sent).toString('utf8');
  assert.equal(cmd.at(-1), '\0');
  assert.deepEqual(JSON.parse(cmd.slice(0, -1)), { id: 1, method: 'Storage.getCookies' });

  // An event with no id is ignored; the id-1 reply resolves. The two frames
  // arrive split across chunks, to exercise the NUL re-framing.
  const reply = frame({ id: 1, result: { cookies: [{ name: 'a', value: 'b', domain: '.x.com', path: '/', expires: -1, secure: true, httpOnly: false, session: true }] } });
  read.write(Buffer.concat([frame({ method: 'Some.event', params: {} }), reply.subarray(0, 5)]));
  read.write(reply.subarray(5));

  const cookies = await p;
  assert.equal(cookies.length, 1);
  assert.equal(cookies[0].name, 'a');
});

test('the pipe transport rejects on a DevTools error reply', async () => {
  const read = new PassThrough();
  const p = cdpGetCookies(new PassThrough(), read);
  read.write(frame({ id: 1, error: { message: 'nope' } }));
  await assert.rejects(p, /nope/);
});

test('extracting is refused off Windows, never throwing', async () => {
  if (process.platform === 'win32') return; // the real path needs a browser
  assert.deepEqual(await extractBrowserCookies('chrome', 'Default'), { error: 'failed' });
});

test('a held lockfile means running; none, or a crash leftover, does not', async () => {
  const udd = mkdtempSync(join(tmpdir(), 'udd-'));
  assert.equal(await lockHeld(udd), false); // no lockfile
  writeFileSync(join(udd, 'lockfile'), '');
  assert.equal(await lockHeld(udd), false); // opens: left by a crash
  const busy = (code: string) => () => Promise.reject(Object.assign(new Error(code), { code }));
  assert.equal(await lockHeld(udd, busy('EBUSY') as never), true); // Windows' sharing violation
  assert.equal(await lockHeld(udd, busy('EPERM') as never), true);
  assert.equal(await lockHeld(udd, busy('ENOENT') as never), false); // gone as we looked: it exited
});

test('only a process from the browser\'s own exe counts as it running', async () => {
  const exes = ['C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe', 'C:\\Users\\u\\AppData\\Local\\Google\\Chrome\\Application\\chrome.exe'];
  assert.equal(exeRunning(['c:\\program files\\google\\chrome\\application\\CHROME.EXE'], exes), true);
  assert.equal(exeRunning(['C:/Program Files/Google/Chrome/Application/chrome.exe'], exes), true);
  // Canary is chrome.exe too, from elsewhere; a path Windows won't give is no answer.
  assert.equal(exeRunning(['C:\\Users\\u\\AppData\\Local\\Google\\Chrome SxS\\Application\\chrome.exe', ''], exes), false);
  assert.equal(exeRunning([], exes), false);

  const udd = mkdtempSync(join(tmpdir(), 'udd-'));
  assert.equal(await browserRunning({ userDataDir: udd, exes }, async () => [exes[0]]), true);
  assert.equal(await browserRunning({ userDataDir: udd, exes }, async () => []), false);
});

test('no cookies read is "running" while the browser holds the profile, "failed" if the copy had some', () => {
  assert.deepEqual(emptyResult(true, 12), { error: 'running' });
  assert.deepEqual(emptyResult(true, 0), { error: 'running' });
  assert.deepEqual(emptyResult(false, 12), { error: 'failed' });
  assert.deepEqual(emptyResult(false, 0), { cookies: [] }); // truly none
  assert.deepEqual(emptyResult(false, undefined), { cookies: [] }); // couldn't count
});

// cookieStore makes a Chrome-shaped Cookies database in WAL mode whose rows
// are still only in the -wal, as a running browser leaves it.
function cookieStore(dir: string): { path: string; db: DatabaseSync } {
  const path = join(dir, 'Cookies');
  const db = new DatabaseSync(path);
  db.exec('PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE cookies (host_key TEXT, name TEXT, expires_utc INTEGER)');
  const future = (BigInt(Date.now()) + 86_400_000n) * 1000n + 11_644_473_600_000_000n;
  const ins = db.prepare('INSERT INTO cookies VALUES (?, ?, ?)');
  ins.run('.github.com', 'session', 0);
  ins.run('.github.com', 'dated', future);
  ins.run('.old.com', 'expired', 13_000_000_000_000_000n); // 2012
  return { path, db };
}

test('the snapshot holds the rows still in the WAL, and counts the unexpired ones', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'store-'));
  const { path, db } = cookieStore(dir); // kept open: the WAL isn't checkpointed
  const dst = join(dir, 'snapshot');
  assert.deepEqual(await snapshotCookies(path, dst, join(dir, 'staging')), { rows: 2 });
  db.close();
  const copy = new DatabaseSync(dst, { readOnly: true });
  assert.equal((copy.prepare('SELECT count(*) AS n FROM cookies').get() as { n: number }).n, 3);
  copy.close();
});

test('a store SQLite can\'t open read-only is snapshotted from a byte copy', async (t) => {
  if (process.getuid?.() === 0) return t.skip('root writes to a read-only directory');
  const dir = mkdtempSync(join(tmpdir(), 'store-'));
  const { path, db } = cookieStore(dir);
  // A WAL database with its -wal but no -shm, in a directory nobody may write
  // to: a read-only open can't create the -shm, so it fails, and only a
  // writable copy can replay the WAL.
  const src = mkdtempSync(join(tmpdir(), 'locked-'));
  copyFileSync(path, join(src, 'Cookies'));
  copyFileSync(path + '-wal', join(src, 'Cookies-wal'));
  db.close();
  chmodSync(src, 0o555);
  t.after(() => chmodSync(src, 0o755));
  assert.throws(() => new DatabaseSync(join(src, 'Cookies'), { readOnly: true }).prepare('SELECT 1 FROM cookies').get());
  assert.deepEqual(await snapshotCookies(join(src, 'Cookies'), join(dir, 'snapshot'), join(dir, 'staging')), { rows: 2 });
  assert.equal(existsSync(join(dir, 'staging')), false); // staging is cleaned up
});

test('a store that isn\'t SQLite throws, which reads as running', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'store-'));
  writeFileSync(join(dir, 'Cookies'), 'not a database');
  await assert.rejects(snapshotCookies(join(dir, 'Cookies'), join(dir, 'snapshot'), join(dir, 'staging')));
});

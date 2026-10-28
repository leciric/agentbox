// Run with `npm test`: the parts of the Windows cookie reader that don't need
// Windows — the CDP-cookie mapping, and the platform guard.
import assert from 'node:assert/strict';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { cdpGetCookies, extractBrowserCookies, toImported } from './browsercookies.ts';

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

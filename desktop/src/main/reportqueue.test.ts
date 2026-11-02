// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import type * as T from '../shared/api';
import { ErrorReportSender, maxPerRun, maxQueued, outcome, ReportQueue, type Outcome } from './reportqueue.ts';

const report = (message: string): T.ReportRequest => ({ kind: 'error', message, sections: [] });
const queueFile = () => join(mkdtempSync(join(tmpdir(), 'reportqueue-')), 'error-reports.json');

// daemon is a fake POST /v1/reports: up or not, and what it was sent.
function daemon(up = true) {
  const d = { up, sent: [] as string[], refuse: new Set<string>() };
  const post = async (r: T.ReportRequest): Promise<Outcome> => {
    if (!d.up) return 'unreachable';
    if (d.refuse.has(r.message)) return 'refused';
    d.sent.push(r.message);
    return 'sent';
  };
  return { d, post };
}

test("the daemon's answers: sent, refused, or never reached", () => {
  assert.equal(outcome({ status: 200, body: '{"id":"rep_1"}' }), 'sent');
  assert.equal(outcome({ status: 400, body: '{"error":"automatic error reports are off"}' }), 'refused');
  assert.equal(outcome({ status: 503, body: '{"error":"connect ENOENT","code":"daemon_unreachable"}' }), 'unreachable');
  assert.equal(outcome({ status: 503, body: '{"error":"Incus isn\'t answering"}' }), 'refused', "a daemon's own 503");
  assert.equal(outcome({ status: 503, body: 'nope' }), 'refused');
});

test("a report the daemon can't be reached for waits on disk, and goes once it's back", async () => {
  const file = queueFile();
  const { d, post } = daemon(false);
  const sender = new ErrorReportSender(new ReportQueue(file), post);
  assert.equal(await sender.send(report('a')), 'unreachable');
  assert.equal(await sender.send(report('b')), 'unreachable');
  assert.deepEqual(
    new ReportQueue(file).read().map((e) => e.report.message),
    ['a', 'b'],
  );
  await sender.flush(); // still away: nothing lost
  assert.equal(new ReportQueue(file).read().length, 2);

  d.up = true;
  d.refuse.add('a'); // turned off meanwhile, say: dropped, not retried
  // The next run of the app finds them.
  await new ErrorReportSender(new ReportQueue(file), post).flush();
  assert.deepEqual(d.sent, ['b']);
  assert.deepEqual(new ReportQueue(file).read(), []);
});

test('a flush stops at the first report the daemon is away for again, keeping it and the rest', async () => {
  const file = queueFile();
  const q = new ReportQueue(file);
  for (const m of ['a', 'b', 'c']) q.add(report(m));
  let calls = 0;
  await new ErrorReportSender(q, async () => (++calls === 2 ? 'unreachable' : 'sent')).flush();
  assert.deepEqual(q.read().map((e) => e.report.message), ['b', 'c']);
});

test('the queue keeps the newest few, and a broken file is an empty queue', () => {
  const file = queueFile();
  const q = new ReportQueue(file);
  for (let i = 0; i < maxQueued + 5; i++) q.add(report(`r${i}`));
  const kept = q.read();
  assert.equal(kept.length, maxQueued);
  assert.equal(kept[0].report.message, 'r5');
  writeFileSync(file, '{not json');
  assert.deepEqual(q.read(), []);
  q.add(report('x'));
  assert.equal(JSON.parse(readFileSync(file, 'utf8')).length, 1);
});

test('one run sends at most its cap, queued or not', async () => {
  const { d, post } = daemon(true);
  const sender = new ErrorReportSender(new ReportQueue(queueFile()), post);
  const results = [];
  for (let i = 0; i < maxPerRun + 2; i++) results.push(await sender.send(report(`r${i}`)));
  assert.equal(d.sent.length, maxPerRun);
  assert.deepEqual(results.slice(-2), ['capped', 'capped']);
});

test('flushes at the same time send each report once', async () => {
  const q = new ReportQueue(queueFile());
  q.add(report('a'));
  const { d, post } = daemon(true);
  const sender = new ErrorReportSender(q, post);
  await Promise.all([sender.flush(), sender.flush()]);
  assert.deepEqual(d.sent, ['a']);
});

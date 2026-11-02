// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { describeGone, goneReport, goneSignature, offersReload, worthReporting, type GoneProcess } from './processgone.ts';

const renderer: GoneProcess = { process: 'window', reason: 'crashed', exitCode: 139 };
const gpu: GoneProcess = { process: 'GPU', reason: 'oom', exitCode: -1 };
const network: GoneProcess = { process: 'Utility', reason: 'abnormal-exit', exitCode: 1, name: 'Network Service' };

test("a process that ended as it should, or while the app quits, isn't reported", () => {
  assert.ok(worthReporting(renderer, false));
  assert.ok(!worthReporting({ ...renderer, reason: 'clean-exit', exitCode: 0 }, false));
  assert.ok(!worthReporting(gpu, true));
});

test('the window is offered a reload when its page or the GPU went', () => {
  assert.ok(offersReload(renderer));
  assert.ok(offersReload(gpu));
  assert.ok(!offersReload(network));
});

test("a gone process's report says which, why and its exit code, with the app section", () => {
  const app = { id: 'app', title: 'The desktop app', content: 'App: 0.15.0' };
  const req = goneReport(renderer, app);
  assert.equal(req.kind, 'error');
  assert.equal(req.message, "The window's renderer process is gone: crashed, exit code 139");
  assert.equal(req.sections[0].id, 'error');
  assert.match(req.sections[0].content, /Reason: {4}crashed\nExit code: 139$/);
  assert.deepEqual(req.sections[1], app);
  assert.equal(describeGone(network), "Chromium's Utility process (Network Service) is gone: abnormal-exit, exit code 1");
  assert.equal(goneReport(gpu).sections.length, 1);
  assert.ok(goneReport(gpu).sections[0].title.length <= 80);
});

test('the same process gone the same way is one', () => {
  assert.equal(goneSignature(renderer), goneSignature({ ...renderer }));
  assert.notEqual(goneSignature(renderer), goneSignature({ ...renderer, reason: 'oom' }));
});

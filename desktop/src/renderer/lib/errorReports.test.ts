// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ErrorReporter, errorReport, fromEvent, maxPerSession, type UncaughtError } from './errorReports.ts';

const err = (message: string, where: UncaughtError['where'] = 'window'): UncaughtError => ({ where, name: 'TypeError', message, stack: 'at f (file:///app/index.js:1:2)' });
const unasked = { errorReports: false, errorReportsAsked: false };
const on = { errorReports: true, errorReportsAsked: true };
const off = { errorReports: false, errorReportsAsked: true };

test('the first error asks, once, and only while the user never chose', () => {
  const r = new ErrorReporter();
  assert.equal(r.decide(err('a'), unasked), 'ask');
  assert.equal(r.decide(err('b'), unasked), 'ignore', 'asked once a run');
  assert.equal(new ErrorReporter().decide(err('a'), off), 'ignore');
  assert.equal(new ErrorReporter().decide(err('a'), undefined), 'ignore', 'no daemon, nothing to do');
});

test('once on, each new error is sent, up to a cap a run', () => {
  const r = new ErrorReporter();
  assert.equal(r.decide(err('a'), on), 'send');
  assert.equal(r.decide(err('a'), on), 'ignore', 'the same error again');
  assert.equal(r.decide(err('a', 'main'), on), 'send', 'the same message from the main process is another error');
  for (let i = 0; i < maxPerSession - 2; i++) assert.equal(r.decide(err(`n${i}`), on), 'send');
  assert.equal(r.decide(err('one too many'), on), 'ignore');
});

test("ResizeObserver's warnings aren't errors", () => {
  assert.equal(new ErrorReporter().decide(err('ResizeObserver loop completed with undelivered notifications.'), on), 'ignore');
});

test('an error report carries the error and the app section, and no logs', () => {
  const app = { id: 'app', title: 'The desktop app', content: 'App: 0.10.0' };
  const req = errorReport(err('x is undefined', 'main'), app);
  assert.deepEqual(req, {
    kind: 'error',
    message: 'TypeError: x is undefined',
    sections: [{ id: 'error', title: 'The error, in the main process', content: 'TypeError: x is undefined\nat f (file:///app/index.js:1:2)' }, app],
  });
  assert.equal(errorReport({ ...err('x'), message: 'y'.repeat(5000) }).message.length, 1000);
});

test('window events become errors', () => {
  const thrown = new RangeError('too far');
  assert.equal(fromEvent({ error: thrown, message: 'Uncaught RangeError: too far' } as ErrorEvent)!.name, 'RangeError');
  assert.deepEqual(fromEvent({ reason: 'nope' } as PromiseRejectionEvent), { where: 'window', name: 'Error', message: 'nope' });
  assert.deepEqual(fromEvent({ reason: { code: 7 } } as PromiseRejectionEvent), { where: 'window', name: 'Error', message: '{"code":7}' });
  const script = fromEvent({ error: null, message: 'Script error.', filename: 'https://x.test/a.js?k=1', lineno: 3, colno: 4 } as ErrorEvent)!;
  assert.equal(script.message, 'Script error.');
  assert.equal(script.stack, 'at https://x.test/a.js:3:4');
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ErrorBoundary } from './ErrorBoundary.ts';

test('a failed render shows the fallback, or nothing', () => {
  assert.deepEqual(ErrorBoundary.getDerivedStateFromError(), { failed: true });
  const b = new ErrorBoundary({ children: 'child', fallback: 'oops' });
  assert.equal(b.render(), 'child');
  b.state = ErrorBoundary.getDerivedStateFromError();
  assert.equal(b.render(), 'oops');
  const bare = new ErrorBoundary({ children: 'child' });
  bare.state = { failed: true };
  assert.equal(bare.render(), null);
});

test('a function fallback gets the error and a retry that renders the children again', () => {
  const error = new Error('boom');
  let seen: unknown;
  let retry = () => {};
  const b = new ErrorBoundary({ children: 'child', fallback: (e, r) => ((seen = e), (retry = r), 'oops') });
  b.state = ErrorBoundary.getDerivedStateFromError(error);
  assert.equal(b.render(), 'oops');
  assert.equal(seen, error);
  let next: unknown;
  b.setState = (s) => { next = s; };
  retry();
  assert.deepEqual(next, { failed: false, error: undefined });
});

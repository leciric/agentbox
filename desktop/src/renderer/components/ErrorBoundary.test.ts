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

// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { imageTypes, maxImages, previewUrl } from './chatImages.ts';

test('previewUrl builds a data: URL from the mime type and base64 data', () => {
  assert.equal(previewUrl({ mimeType: 'image/png', data: 'AAAA' }), 'data:image/png;base64,AAAA');
});

test('every type the model reads is offered for upload', () => {
  assert.deepEqual(imageTypes, ['image/png', 'image/jpeg', 'image/gif', 'image/webp']);
});

test('maxImages caps how many can be attached at once', () => {
  assert.equal(maxImages, 8);
});

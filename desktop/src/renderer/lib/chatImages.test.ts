// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { imageFiles, imageTypes, maxImages, previewUrl } from './chatImages.ts';

test('imageFiles keeps only the pasted or dropped files that are images', () => {
  const image = { type: 'image/png' } as File;
  const text = { type: 'text/plain' } as File;
  const files = imageFiles({ files: [image, text] } as unknown as DataTransfer);
  assert.deepEqual(files, [image]);
});

test('imageFiles is empty with no DataTransfer at all', () => {
  assert.deepEqual(imageFiles(null), []);
});

test('previewUrl builds a data: URL from the mime type and base64 data', () => {
  assert.equal(previewUrl({ mimeType: 'image/png', data: 'AAAA' }), 'data:image/png;base64,AAAA');
});

test('every type the model reads is offered for upload', () => {
  assert.deepEqual(imageTypes, ['image/png', 'image/jpeg', 'image/gif', 'image/webp']);
});

test('maxImages caps how many can be attached at once', () => {
  assert.equal(maxImages, 8);
});

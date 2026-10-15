// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { droppedFiles, fileKind, formatSize, isFile, maxFileBytes, maxFiles, maxFilesBytes, prepareFile, room, uploads, type Pending, type PendingFile } from './chatFiles.ts';
import { maxImages, type PendingImage } from './chatImages.ts';

const file = (name: string, size = 10): PendingFile => ({ key: name, name, size, data: 'AAAA', mimeType: 'text/plain', file: true });
const image = (name: string): PendingImage => ({ key: name, name, size: 10, data: 'AAAA', mimeType: 'image/png' });

test('droppedFiles keeps every file pasted or dropped, images or not', () => {
  const png = { type: 'image/png' } as File;
  const pdf = { type: 'application/pdf' } as File;
  assert.deepEqual(droppedFiles({ files: [png, pdf] } as unknown as DataTransfer), [png, pdf]);
  assert.deepEqual(droppedFiles(null), []);
});

test('uploads splits what waits into the pictures and the files the daemon takes', () => {
  const waiting: Pending[] = [image('a.png'), file('b.csv')];
  assert.deepEqual(uploads(waiting), {
    images: [{ mimeType: 'image/png', data: 'AAAA', name: 'a.png' }],
    files: [{ name: 'b.csv', mimeType: 'text/plain', data: 'AAAA' }],
  });
  assert.equal(isFile(waiting[0]!), false);
  assert.equal(isFile(waiting[1]!), true);
});

test('room refuses a file past the count or the total, and a picture past its own count', () => {
  const full = Array.from({ length: maxFiles }, (_, i) => file(`f${i}`));
  assert.match(room(full, file('one-more')), /at most 10 files/);
  assert.equal(room(full, image('still.png')), '');
  assert.match(room([file('big', maxFilesBytes - 5)], file('more', 10)), /too big together/);
  assert.equal(room([file('big', maxFilesBytes - 10)], file('fits', 10)), '');
  const pictures = Array.from({ length: maxImages }, (_, i) => image(`p${i}`));
  assert.match(room(pictures, image('one-more.png')), /at most 8 images/);
  assert.equal(room(pictures, file('a.txt')), '');
});

test('prepareFile refuses a file over the size a file may be, by name', async () => {
  const big = { name: 'huge.zip', size: maxFileBytes + 1, type: 'application/zip' } as File;
  await assert.rejects(prepareFile(big), /huge\.zip is too big to send: a file may be at most 25\.0 MB/);
});

test('fileKind reads a file from its extension, then its type', () => {
  assert.equal(fileKind('report.PDF'), 'pdf');
  assert.equal(fileKind('data.csv', 'text/csv'), 'sheet');
  assert.equal(fileKind('backup.tar.gz'), 'archive');
  assert.equal(fileKind('main.go'), 'code');
  assert.equal(fileKind('notes.md'), 'text');
  assert.equal(fileKind('blob', 'application/x-7z-compressed'), 'archive');
  assert.equal(fileKind('clip', 'video/mp4'), 'video');
  assert.equal(fileKind('mystery'), 'other');
});

test('formatSize', () => {
  assert.equal(formatSize(17), '17 B');
  assert.equal(formatSize(2048), '2 KB');
  assert.equal(formatSize(3.5 * (1 << 20)), '3.5 MB');
});

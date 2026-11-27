// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { droppedFiles, fileKind, formatSize, isFile, maxFileBytes, maxFiles, maxFilesBytes, pastedFiles, prepareFile, room, uploads, type Pending, type PendingFile } from './chatFiles.ts';
import { maxImages, type PendingImage } from './chatImages.ts';

const file = (name: string, size = 10): PendingFile => ({ key: name, name, size, data: 'AAAA', mimeType: 'text/plain', file: true });
const image = (name: string): PendingImage => ({ key: name, name, size: 10, data: 'AAAA', mimeType: 'image/png' });

test('droppedFiles keeps every file pasted or dropped, images or not', () => {
  const png = { type: 'image/png' } as File;
  const pdf = { type: 'application/pdf' } as File;
  assert.deepEqual(droppedFiles({ files: [png, pdf] } as unknown as DataTransfer), [png, pdf]);
  assert.deepEqual(droppedFiles(null), []);
});

// A paste as the page gets it: the files on the clipboard and its plain text.
const clipboard = (files: Array<{ name: string; type: string }>, text = '') =>
  ({ files, getData: (type: string) => (type === 'text/plain' ? text : '') }) as unknown as DataTransfer;
const png = { name: 'image.png', type: 'image/png' };

test('pastedFiles pastes the text when Word, Excel or LibreOffice put a picture of it beside it', () => {
  // The PNG of the copied cells: the page sees it as a file called image.png.
  assert.deepEqual(pastedFiles(clipboard([png], 'Q3\t1200\nQ4\t1500\n')), []);
  assert.deepEqual(pastedFiles(clipboard([png], 'a paragraph from Word')), []);
  // Whatever its name, a picture of text is not the text's name.
  assert.deepEqual(pastedFiles(clipboard([{ name: 'chart.png', type: 'image/png' }], 'Q3\t1200')), []);
});

test('pastedFiles attaches a screenshot: a picture and no text', () => {
  assert.deepEqual(pastedFiles(clipboard([png])), [png]);
  // A blank cell or a stray newline beside a picture is no text to keep.
  assert.deepEqual(pastedFiles(clipboard([png], ' \r\n')), [png]);
});

test('pastedFiles attaches copied files, as they come with their names as text or without', () => {
  const photo = { name: 'photo one.png', type: 'image/png' };
  const report = { name: 'report.pdf', type: 'application/pdf' };
  // Windows Explorer: no text at all.
  assert.deepEqual(pastedFiles(clipboard([photo, report])), [photo, report]);
  // Finder: the names, one a line.
  assert.deepEqual(pastedFiles(clipboard([photo], 'photo one.png')), [photo]);
  assert.deepEqual(pastedFiles(clipboard([photo, { name: 'b.png', type: 'image/png' }], 'photo one.png\r\nb.png\r\n')).length, 2);
  // The Linux managers: paths or file:// URLs, percent-encoded.
  assert.deepEqual(pastedFiles(clipboard([photo], '/home/lint/Pictures/photo one.png')), [photo]);
  assert.deepEqual(pastedFiles(clipboard([photo], 'file:///home/lint/Pictures/photo%20one.png\n')), [photo]);
  assert.deepEqual(pastedFiles(clipboard([photo], 'C:\\Users\\lint\\photo one.png')), [photo]);
  // A name that itself holds a percent escape matches as written, and a URL's escapes still decode.
  const literal = { name: 'chart%20draft.png', type: 'image/png' };
  assert.deepEqual(pastedFiles(clipboard([literal], 'chart%20draft.png')), [literal]);
  assert.deepEqual(pastedFiles(clipboard([literal], '/home/lint/Pictures/chart%20draft.png')), [literal]);
  assert.deepEqual(pastedFiles(clipboard([literal], 'file:///home/lint/Pictures/chart%2520draft.png')), [literal]);
  assert.deepEqual(pastedFiles(clipboard([{ name: 'a 100%.png', type: 'image/png' }], 'a 100%.png')).length, 1);
  // Files that aren't pictures are never a picture of the text beside them.
  assert.deepEqual(pastedFiles(clipboard([report], 'some other text')), [report]);
  assert.deepEqual(pastedFiles(clipboard([photo, report], 'some other text')), [photo, report]);
});

test('pastedFiles leaves a paste with no files to the text field', () => {
  assert.deepEqual(pastedFiles(clipboard([], 'just text')), []);
  assert.deepEqual(pastedFiles(clipboard([])), []);
  assert.deepEqual(pastedFiles(null), []);
  // Text that merely mentions the file's name is still text.
  assert.deepEqual(pastedFiles(clipboard([{ name: 'photo.png', type: 'image/png' }], 'photo.png\nand what I think of it')), []);
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
  assert.equal(fileKind('Song.MP3'), 'audio');
  assert.equal(fileKind('voice.m4a', 'application/octet-stream'), 'audio');
  assert.equal(fileKind('blob', 'audio/ogg'), 'audio');
  assert.equal(fileKind('mystery'), 'other');
});

test('formatSize', () => {
  assert.equal(formatSize(17), '17 B');
  assert.equal(formatSize(2048), '2 KB');
  assert.equal(formatSize(3.5 * (1 << 20)), '3.5 MB');
});

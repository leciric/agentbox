import assert from 'node:assert/strict';
import test from 'node:test';
import type * as T from '../../shared/api';
import { isElement, room, uploads, type Pending } from './chatFiles.ts';
import { maxImages } from './chatImages.ts';
import { pickedText, pickImageNumber, pickLabel, sendPick, splitPicked, takePicks, withPicks, type PendingElement } from './picks.ts';

const element = (over: Partial<T.ElementPick> = {}): T.ElementPick => ({
  url: 'http://localhost:3000/settings',
  title: 'Settings',
  selector: 'main > header > button.btn.primary:nth-of-type(2)',
  tag: 'button',
  html: '<button class="btn primary">Save</button>',
  text: 'Save',
  styles: [
    { name: 'color', value: 'rgb(255, 255, 255)' },
    { name: 'border-radius', value: '6px' },
  ],
  box: { x: 10, y: 20, width: 120, height: 40 },
  viewport: { width: 1280, height: 720 },
  image: { mimeType: 'image/png', data: 'iVBORw0KGgo=', name: 'element.png' },
  ...over,
});
const pick = (over: Partial<T.ElementPick> = {}): PendingElement => ({ key: 'k', element: element(over), from: 'shop/agent-12' });

test('a pick waits for its chat, and only that chat takes it', () => {
  sendPick('shop/lead', 'shop/agent-12', element());
  assert.deepEqual(takePicks('shop/agent-12'), []);
  const taken = takePicks('shop/lead');
  assert.equal(taken.length, 1);
  assert.equal(taken[0].from, 'shop/agent-12');
  assert.deepEqual(takePicks('shop/lead'), [], 'taken once');
});

test('pickLabel names an element by its tag and id or first class', () => {
  assert.equal(pickLabel(element()), 'button.btn');
  assert.equal(pickLabel(element({ selector: '#save', tag: 'button' })), 'button#save');
  assert.equal(pickLabel(element({ selector: 'main > div', tag: 'div' })), 'div');
  assert.equal(pickLabel(element({ selector: 'a.hover\\:underline', tag: 'a' })), 'a.hover');
});

test('a message carries its picks as blocks the timeline turns back into chips', () => {
  const text = withPicks('make 1 match the header', [pick(), pick({ selector: '#nav', tag: 'nav', html: '<nav id="nav"></nav>', url: 'http://localhost:3000/', title: '' })]);
  assert.match(text, /^make 1 match the header\n\n<picked-element n="1" from="shop\/agent-12" image="element-1.png">/);
  assert.match(text, /Selector: main > header > button\.btn\.primary:nth-of-type\(2\)/);
  assert.match(text, /Box: 120×40 at 10,20 in a 1280×720 viewport/);
  assert.match(text, /Styles: color: rgb\(255, 255, 255\); border-radius: 6px/);
  assert.match(text, /```html\n<button class="btn primary">Save<\/button>\n```/);

  const split = splitPicked(text);
  assert.equal(split.text, 'make 1 match the header');
  assert.deepEqual(
    split.picks.map((p) => [p.n, p.label, p.url, p.from]),
    [
      [1, 'button.btn', 'http://localhost:3000/settings', 'shop/agent-12'],
      [2, 'nav#nav', 'http://localhost:3000/', 'shop/agent-12'],
    ],
  );
  assert.deepEqual(splitPicked('just words'), { text: 'just words', picks: [] });
  assert.equal(splitPicked(withPicks('', [pick()])).text, '');
});

test('a pick without a picture says so in no image attribute', () => {
  assert.doesNotMatch(pickedText(1, pick({ image: undefined })), /image=/);
});

test('picks go up as their crops, numbered, or as files for a tool without pictures', () => {
  const pending: Pending[] = [{ key: 'a', name: 'shot.png', size: 4, data: 'AAAA', mimeType: 'image/png' }, pick(), pick({ image: undefined }), pick({ image: { mimeType: 'image/jpeg', data: 'AAAA' } })];
  assert.equal(pending.filter(isElement).length, 3);
  const { images, files } = uploads(pending);
  assert.deepEqual(
    images.map((i) => i.name),
    ['shot.png', 'element-1.png', 'element-3.jpg'],
  );
  assert.equal(files.length, 0);
  const asFiles = uploads(pending, false);
  assert.deepEqual(
    asFiles.files.map((f) => f.name),
    ['element-1.png', 'element-3.jpg'],
  );
});

test('a pick with a picture counts toward the pictures a message may have', () => {
  const full: Pending[] = Array.from({ length: maxImages }, (_, i) => ({ ...pick(), key: `p${i}` }));
  assert.notEqual(room(full, pick()), '');
  assert.equal(room(full, pick({ image: undefined })), '', 'a pick with no picture still fits');
  assert.equal(room(full.slice(1), pick()), '');
});

test('a sent picture is known for the crop of its pick by its name', () => {
  assert.equal(pickImageNumber('element-2.png'), 2);
  assert.equal(pickImageNumber('element-12.jpg'), 12);
  assert.equal(pickImageNumber('screenshot.png'), 0);
  assert.equal(pickImageNumber(undefined), 0);
});

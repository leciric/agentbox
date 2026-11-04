// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { arrange, box, canPan, fitView, limits, panBy, percent, resolve, transform, zoomAt } from './zoom.ts';

const port = { w: 800, h: 500 };

test('fit shows all of a picture, no larger than its own pixels', () => {
  assert.deepEqual(fitView(port, { w: 1920, h: 1080 }), { scale: 800 / 1920, cx: 960, cy: 540 });
  assert.deepEqual(fitView(port, { w: 320, h: 120 }), { scale: 1, cx: 160, cy: 60 });
});

test('fit shows a very tall picture across from its top, a very wide one from its left', () => {
  const tall = fitView({ w: 420, h: 460 }, { w: 420, h: 2000 });
  assert.equal(tall.scale, 1);
  assert.equal(tall.cy, 230);
  const wide = fitView({ w: 300, h: 500 }, { w: 3840, h: 1080 });
  assert.equal(wide.cx, (300 / 2) / wide.scale);
});

test('zooming keeps the pixel under the pointer still, within its limits', () => {
  const pic = { w: 1600, h: 1000 };
  const z = zoomAt('fit', 4, 200, 100, port, pic);
  const before = resolve('fit', port, pic);
  const after = resolve(z, port, pic);
  const under = (v: typeof before, px: number) => v.cx + (px - port.w / 2) / v.scale;
  assert.ok(Math.abs(under(before, 200) - under(after, 200)) < 1e-9);
  assert.equal(resolve(zoomAt(z, 1e-3, 0, 0, port, pic), port, pic).scale, limits(port, pic)[0]);
  assert.equal(resolve(zoomAt(z, 1e6, 0, 0, port, pic), port, pic).scale, 8);
});

test('panning stops at the edges, and a picture smaller than its viewport stays in its middle', () => {
  const pic = { w: 1600, h: 1000 };
  const z = zoomAt('fit', 4, 400, 250, port, pic);
  assert.deepEqual(resolve(panBy(z, 1e6, 1e6, port, pic), port, pic), { scale: 2, cx: 200, cy: 125 });
  assert.equal(canPan('fit', port, pic), false);
  assert.equal(canPan(z, port, pic), true);
  assert.deepEqual(resolve(panBy('fit', 50, 50, port, { w: 320, h: 120 }), port, { w: 320, h: 120 }), { scale: 1, cx: 160, cy: 60 });
  assert.equal(transform('fit', port, { w: 320, h: 120 }), 'translate(240px, 190px) scale(1)');
  assert.equal(percent(z, port, pic), 200);
});

test('the stage box hugs a picture, and gives a very tall one a readable column to scroll in', () => {
  const room = { w: 860, h: 460 };
  assert.deepEqual(box(room, { w: 1920, h: 1080 }), { w: 817, h: 460 });
  assert.deepEqual(box(room, { w: 420, h: 2000 }), { w: 420, h: 460 });
  assert.deepEqual(box(room, { w: 3840, h: 1080 }), { w: 860, h: 241 });
  assert.deepEqual(box(room, { w: 320, h: 120 }), { w: 320, h: 120 });
});

test('a pair goes side by side or one over the other, whichever shows it larger', () => {
  const room = { w: 860, h: 460 };
  assert.equal(arrange(room, { w: 3840, h: 1080 }, 8).arrangement, 'rows');
  assert.equal(arrange(room, { w: 420, h: 2000 }, 8).arrangement, 'columns');
  assert.equal(arrange({ w: 600, h: 700 }, { w: 1920, h: 1080 }, 8).arrangement, 'rows');
  assert.equal(arrange({ w: 1400, h: 460 }, { w: 1920, h: 1080 }, 8).arrangement, 'columns');
});

test('a box and the fit inside it agree: no bars beside a portrait or above an ultra-wide', () => {
  for (const room of [{ w: 1800, h: 800 }, { w: 860, h: 460 }, { w: 400, h: 900 }]) {
    for (const pic of [{ w: 1080, h: 1920 }, { w: 390, h: 4000 }, { w: 1080, h: 6000 }, { w: 3440, h: 1000 }, { w: 6000, h: 300 }, { w: 900, h: 900 }, { w: 1024, h: 768 }]) {
      const b = box(room, pic);
      const v = fitView(b, pic);
      const shown = { w: pic.w * v.scale, h: pic.h * v.scale };
      // The picture fills its box along one axis, whether it fits or scrolls.
      assert.ok(shown.w >= b.w - 1 || shown.h >= b.h - 1, `${JSON.stringify(room)} ${JSON.stringify(pic)} -> ${JSON.stringify(b)}`);
    }
  }
});

// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { arrange, arrangePair, box, canPan, fitView, limits, panBy, percent, relate, resolve, transform, zoomAt } from './zoom.ts';

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
  assert.deepEqual(box(room, { w: 3840, h: 1080 }), { w: 857, h: 241 });
  assert.deepEqual(box(room, { w: 320, h: 120 }), { w: 320, h: 120 });
});

test('a pair goes side by side or one over the other, whichever shows it larger', () => {
  const room = { w: 860, h: 460 };
  assert.equal(arrange(room, { w: 3840, h: 1080 }, 8).arrangement, 'rows');
  assert.equal(arrange(room, { w: 420, h: 2000 }, 8).arrangement, 'columns');
  assert.equal(arrange({ w: 600, h: 700 }, { w: 1920, h: 1080 }, 8).arrangement, 'rows');
  assert.equal(arrange({ w: 1400, h: 460 }, { w: 1920, h: 1080 }, 8).arrangement, 'columns');
});

// The shapes the review used to draw narrower (or shorter) than their box,
// with black bars beside them: phones, a square, 4:3, three monitors.
const shapes = [
  { w: 1080, h: 1920 },
  { w: 1170, h: 2532 },
  { w: 420, h: 2000 },
  { w: 1200, h: 1200 },
  { w: 1600, h: 1200 },
  { w: 1920, h: 1080 },
  { w: 3840, h: 1080 },
  { w: 5760, h: 1080 },
  { w: 320, h: 120 },
];

test('fit fills the box the stage gives a picture, whatever its shape: no bars', () => {
  for (const room of [{ w: 1000, h: 433 }, { w: 860, h: 460 }, { w: 1300, h: 700 }, { w: 600, h: 900 }, { w: 400, h: 300 }]) {
    for (const pic of shapes) {
      const b = box(room, pic);
      assert.ok(b.w <= Math.floor(room.w) && b.h <= Math.floor(room.h), `${pic.w}×${pic.h} in ${room.w}×${room.h}: ${b.w}×${b.h} overflows`);
      const { scale } = fitView(b, pic);
      const drawn = { w: Math.min(pic.w * scale, b.w), h: Math.min(pic.h * scale, b.h) };
      assert.ok(drawn.w >= b.w - 1 && drawn.h >= b.h - 1, `${pic.w}×${pic.h} in ${room.w}×${room.h}: box ${b.w}×${b.h}, drawn ${drawn.w.toFixed(1)}×${drawn.h.toFixed(1)}`);
    }
  }
});

test('a phone screenshot that is not a sliver in a column is boxed to its own shape', () => {
  const b = box({ w: 1000, h: 433 }, { w: 1080, h: 1920 });
  assert.deepEqual(b, { w: 243, h: 432 });
  assert.equal(fitView(b, { w: 1080, h: 1920 }).cy, 960);
});

test("a pair's halves of different shapes each get a box of their own", () => {
  const p = arrangePair({ w: 1000, h: 433 }, { w: 1600, h: 1200 }, { w: 1920, h: 1080 }, 8);
  assert.equal(p.arrangement, 'columns');
  assert.deepEqual(p.before, { w: 496, h: 372 });
  assert.deepEqual(p.after, { w: 496, h: 279 });
});

test('a view moves between two pictures of different sizes by fraction and zoom past fit', () => {
  const after = { pic: { w: 1920, h: 1080 }, port: { w: 480, h: 270 } };
  const before = { pic: { w: 1600, h: 1200 }, port: { w: 480, h: 360 } };
  assert.equal(relate('fit', after.port, after.pic, before.port, before.pic), 'fit');
  const v = zoomAt('fit', 2, 240, 135, after.port, after.pic);
  const there = relate(v, after.port, after.pic, before.port, before.pic);
  assert.ok(there !== 'fit');
  assert.ok(Math.abs(there.scale / fitView(before.port, before.pic).scale - 2) < 1e-9);
  assert.ok(Math.abs(there.cx - 800) < 1e-9 && Math.abs(there.cy - 600) < 1e-9);
  const back = relate(there, before.port, before.pic, after.port, after.pic);
  assert.ok(back !== 'fit' && v !== 'fit' && Math.abs(back.scale - v.scale) < 1e-9 && Math.abs(back.cx - v.cx) < 1e-9);
});

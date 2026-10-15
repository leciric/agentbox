// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { centredOn, displayOf, fitInto, minSize, placeWindow, reachable, refit, type Display } from './windowbounds.ts';

// A 1080p monitor with a 40px panel along its bottom, and a portrait one to
// its right, as Electron reports them: in DIPs, the second at 150%.
const landscape: Display = { workArea: { x: 0, y: 0, width: 1920, height: 1040 } };
const portrait: Display = { workArea: { x: 1920, y: 0, width: 1080, height: 1920 } };

test('a new window is the default size, centred, or a little less than a small display', () => {
  assert.deepEqual(centredOn(landscape), { x: 240, y: 70, width: 1440, height: 900 });
  const laptop: Display = { workArea: { x: 0, y: 0, width: 1366, height: 728 } };
  const b = centredOn(laptop);
  assert.ok(b.width < 1366 && b.height < 728, JSON.stringify(b));
  assert.equal(b.x, Math.round((1366 - b.width) / 2));
  // On a portrait monitor it's narrower than the display, not the 1440 default.
  const p = centredOn(portrait);
  assert.ok(p.x >= 1920 && p.x + p.width <= 3000, JSON.stringify(p));
});

test('a window opens where it was, while that display is still there', () => {
  const saved = { x: 2000, y: 300, width: 1000, height: 1400 };
  assert.deepEqual(placeWindow(saved, [landscape, portrait], landscape), saved);
});

test('a window whose monitor is gone opens centred on the fallback display', () => {
  const onPortrait = { x: 2000, y: 300, width: 1000, height: 1400 };
  assert.deepEqual(placeWindow(onPortrait, [landscape], landscape), centredOn(landscape));
  assert.deepEqual(placeWindow(undefined, [landscape], landscape), centredOn(landscape));
  assert.deepEqual(placeWindow({ x: 0, y: 0, width: 0, height: 0 }, [landscape], landscape), centredOn(landscape));
});

test("a window whose title bar ended up above every display isn't put there", () => {
  const above = { x: 100, y: -600, width: 1200, height: 700 };
  assert.equal(reachable(above, [landscape]), false);
  assert.deepEqual(placeWindow(above, [landscape], landscape), centredOn(landscape));
});

test('a window bigger than its display now (turned to portrait) is fitted to it', () => {
  // It was 1800 wide on the monitor, which is now 1080 wide and 1920 tall.
  const rotated: Display = { workArea: { x: 0, y: 0, width: 1080, height: 1880 } };
  const placed = placeWindow({ x: 60, y: 40, width: 1800, height: 1000 }, [rotated], rotated);
  assert.deepEqual(placed, { x: 0, y: 40, width: 1080, height: 1000 });
});

test('fitting never goes below the minimum size, and slides the window on', () => {
  const tiny = { x: 0, y: 0, width: 400, height: 300 };
  const b = fitInto({ x: 500, y: 500, width: 1200, height: 900 }, tiny);
  assert.equal(b.width, minSize.width);
  assert.equal(b.height, minSize.height);
  assert.deepEqual([b.x, b.y], [0, 0]);
  assert.deepEqual(fitInto({ x: 1800, y: 900, width: 800, height: 600 }, landscape.workArea), { x: 1120, y: 440, width: 800, height: 600 });
});

test('the display a window is on is the one with most of it, else the nearest', () => {
  assert.equal(displayOf({ x: 1700, y: 0, width: 800, height: 600 }, [landscape, portrait]), portrait);
  assert.equal(displayOf({ x: 1500, y: 0, width: 500, height: 600 }, [landscape, portrait]), landscape);
  assert.equal(displayOf({ x: 5000, y: 100, width: 500, height: 600 }, [landscape, portrait]), portrait);
});

test('when the displays change, an open window moves only if it must', () => {
  // Still on its display: stays, even across two monitors.
  assert.equal(refit({ x: 100, y: 100, width: 1200, height: 800 }, [landscape, portrait]), undefined);
  assert.equal(refit({ x: 1500, y: 100, width: 1000, height: 800 }, [landscape, portrait]), undefined);
  // Its monitor unplugged: onto the one left.
  assert.deepEqual(refit({ x: 2000, y: 300, width: 1000, height: 1400 }, [landscape]), { x: 920, y: 0, width: 1000, height: 1040 });
  // Its monitor turned from landscape to portrait: narrowed to it.
  const turned: Display = { workArea: { x: 0, y: 0, width: 1080, height: 1880 } };
  assert.deepEqual(refit({ x: 100, y: 100, width: 1440, height: 900 }, [turned]), { x: 0, y: 100, width: 1080, height: 900 });
});

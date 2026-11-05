// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { badgeText, trayIcon } from './trayicon.ts';

// pixel is one pixel of a BGRA icon, as [r, g, b, a].
function pixel(icon: Buffer, size: number, x: number, y: number): [number, number, number, number] {
  const i = (y * size + x) * 4;
  return [icon[i + 2], icon[i + 1], icon[i], icon[i + 3]];
}

const orangeish = ([r, g, b, a]: number[]) => a > 200 && r > 200 && g > 80 && g < 150 && b < 60;
const greenish = ([r, g, b, a]: number[]) => a > 200 && g > 150 && r < 80 && b < 130;
const violetish = ([r, g, b, a]: number[]) => a > 200 && b > 180 && r > 60 && g < 160;

test('badges say the number, and 9+ past nine', () => {
  assert.equal(badgeText(3), '3');
  assert.equal(badgeText(9), '9');
  assert.equal(badgeText(12), '9+');
});

test('an icon is size×size BGRA pixels, clear in the corners', () => {
  const icon = trayIcon({ size: 24, tone: 'idle' });
  assert.equal(icon.length, 24 * 24 * 4);
  assert.equal(pixel(icon, 24, 0, 0)[3], 0);
  // The square's colour where no stroke is: inside, left of the middle edge.
  assert.ok(violetish(pixel(icon, 24, 6, 18)), String(pixel(icon, 24, 6, 18)));
});

test('what waits for the user is an orange badge at the top right, working a green one at the bottom right', () => {
  const size = 48;
  const plain = trayIcon({ size, tone: 'idle' });
  const both = trayIcon({ size, tone: 'attention', waiting: 2, working: 5 });
  // Near each badge's left edge, inside it, in no digit.
  assert.ok(orangeish(pixel(both, size, size - 22, 12)), String(pixel(both, size, size - 22, 12)));
  assert.ok(greenish(pixel(both, size, size - 22, size - 12)), String(pixel(both, size, size - 22, size - 12)));
  assert.ok(!orangeish(pixel(plain, size, size - 22, 12)));
  // The left half is the app icon's either way.
  assert.deepEqual(pixel(both, size, 8, size / 2), pixel(plain, size, 8, size / 2));
});

test('an unreachable AgentBox is grey', () => {
  const [r, g, b, a] = pixel(trayIcon({ size: 24, tone: 'offline' }), 24, 6, 18);
  assert.ok(a > 200 && Math.abs(r - b) < 40 && Math.abs(r - g) < 40, String([r, g, b, a]));
});

test('a Mac template is black on clear, with a dot for attention and no badges', () => {
  const size = 36;
  const idle = trayIcon({ size, tone: 'idle', template: true, waiting: 3 });
  const attention = trayIcon({ size, tone: 'attention', template: true, waiting: 3 });
  for (let i = 0; i < idle.length; i += 4) assert.ok(idle[i] === 0 && idle[i + 1] === 0 && idle[i + 2] === 0, 'only black');
  const dot = pixel(attention, size, size - 6, 6);
  assert.equal(dot[3], 255);
  assert.equal(pixel(idle, size, size - 6, 6)[3], 0);
});

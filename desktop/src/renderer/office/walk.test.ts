import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { Box } from './layout.ts';
import { blocked, clear, path, pathLength, slide, turn } from './walk.ts';

const room: Box = { x0: 0, z0: 0, x1: 10, z1: 10 };
const wall: Box = { x0: 4, z0: 0, x1: 6, z1: 8 };

test('a move into a box slides along its side', () => {
  assert.deepEqual(slide([wall], room, { x: 3.9, z: 5 }, { x: 0.5, z: 0.5 }), { x: 3.9, z: 5.5 });
  assert.deepEqual(slide([wall], room, { x: 1, z: 1 }, { x: 0.5, z: 0 }), { x: 1.5, z: 1 });
  // The room's walls hold too.
  assert.deepEqual(slide([], room, { x: 9.9, z: 1 }, { x: 0.5, z: 0 }), { x: 9.9, z: 1 });
});

test('a straight line is clear only when it crosses nothing', () => {
  assert.ok(clear([wall], room, { x: 1, z: 9 }, { x: 9, z: 9 }));
  assert.ok(!clear([wall], room, { x: 1, z: 1 }, { x: 9, z: 1 }));
});

test('a walk goes round the wall, in a few straight legs', () => {
  const a = { x: 1, z: 1 };
  const b = { x: 9, z: 1 };
  const route = path([wall], room, a, b);
  assert.ok(route.length >= 2 && route.length <= 4, `legs: ${route.length}`);
  assert.deepEqual(route[route.length - 1], b);
  let at = a;
  for (const p of route) {
    assert.ok(clear([wall], room, at, p), 'each leg is clear');
    at = p;
  }
  // Round the end of the wall at z = 8: about 8 down, 2 across, 8 up.
  assert.ok(pathLength(a, route) < 20);
});

test('a goal inside a box walks to the free floor nearest it', () => {
  const route = path([wall], room, { x: 1, z: 1 }, { x: 4.2, z: 3 });
  const end = route[route.length - 1];
  assert.ok(!blocked([wall], room, end));
  assert.ok(Math.abs(end.x - 4) < 0.5 && Math.abs(end.z - 3) < 0.5, JSON.stringify(end));
});

test('a goal nothing reaches is no walk', () => {
  const boxed: Box[] = [
    { x0: 7, z0: 7, x1: 10, z1: 7.5 },
    { x0: 7, z0: 7, x1: 7.5, z1: 10 },
  ];
  assert.deepEqual(path(boxed, room, { x: 1, z: 1 }, { x: 9, z: 9 }), []);
});

test('turning goes the short way round', () => {
  assert.ok(Math.abs(turn(3, -3, 0.1) - 3.1) < 1e-9);
  assert.equal(turn(0, 1, 2), 1);
});

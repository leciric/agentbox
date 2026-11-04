import assert from 'node:assert/strict';
import { test } from 'node:test';
import { columns, COUNTER, footOf, layout, nearest, spotOf, walkable, type Box, type Layout } from './layout.ts';
import type { AreaModel } from './model.ts';
import { blocked, path } from './walk.ts';

const area = (project: string, n: number): AreaModel => ({
  project,
  label: project,
  hue: 200,
  lead: { tone: 'idle' },
  desks: Array.from({ length: n }, (_, i) => ({ id: `${project}/agent-${i + 1}`, project, name: `agent-${i + 1}`, title: '', branch: '', ai: 'claude', tone: 'working' as const })),
});

const overlaps = (a: Box, b: Box) => a.x0 < b.x1 && b.x0 < a.x1 && a.z0 < b.z1 && b.z0 < a.z1;
const within = (outer: Box, inner: Box) => inner.x0 >= outer.x0 && inner.x1 <= outer.x1 && inner.z0 >= outer.z0 && inner.z1 <= outer.z1;

test('pods go wide, about twice as many across as deep', () => {
  assert.deepEqual([0, 1, 2, 3, 4, 6, 8, 12, 18].map(columns), [1, 1, 2, 3, 4, 3, 4, 6, 6]);
});

test('the room is about twice as wide as deep', () => {
  for (const n of [[1], [3, 2], [30, 4, 2], [3, 7, 1, 12, 2]]) {
    const l = layout({ areas: n.map((k, i) => area(`p${i}`, k)) });
    const r = l.room.x1 / l.room.z1;
    assert.ok(r > 1.3 && r < 3.2, `${n}: ${r.toFixed(2)}`);
  }
});

test('an empty office is still a room with a board and a lobby', () => {
  const l = layout({ areas: [] });
  assert.equal(l.areas.length, 0);
  assert.ok(l.room.x1 >= 16);
  assert.ok(l.spawn.z > l.board.spot.z);
});

test('areas sit side by side in the room and never overlap', () => {
  const l = layout({ areas: [area('a', 3), area('b', 7), area('c', 1), area('d', 12), area('e', 2)] });
  assert.equal(l.areas.length, 5);
  for (const a of l.areas) assert.ok(within(l.room, a.rug), `${a.project}'s rug is in the room`);
  for (let i = 0; i < l.areas.length; i++)
    for (let j = i + 1; j < l.areas.length; j++) assert.ok(!overlaps(l.areas[i].rug, l.areas[j].rug), `${l.areas[i].project} and ${l.areas[j].project}`);
  // Too wide for one row: they wrap.
  assert.ok(new Set(l.areas.map((a) => a.rug.z0)).size > 1);
  // Every desk is on its own area's rug.
  for (const a of l.areas) for (const d of a.desks) assert.ok(d.x > a.rug.x0 && d.x < a.rug.x1 && d.z > a.rug.z0 && d.z < a.rug.z1);
});

test('a desk keeps its place while a later agent comes', () => {
  const before = layout({ areas: [area('a', 2), area('b', 2)] });
  const after = layout({ areas: [area('a', 3), area('b', 2)] });
  assert.deepEqual(after.areas[0].desks[0].z, before.areas[0].desks[0].z);
});

const footprint = (p: Layout['props'][number]): Box => {
  const [w, d] = footOf(p.kind);
  const turned = Math.abs(Math.sin(p.rot)) > 0.5;
  const hw = (turned ? d : w) / 2;
  const hd = (turned ? w : d) / 2;
  return { x0: p.x - hw, z0: p.z - hd, x1: p.x + hw, z1: p.z + hd };
};

test('the room is furnished, and nothing stands on anything else', () => {
  for (const n of [[30, 4, 2], [3], [8, 4, 2], [1, 1, 1, 1, 1, 1], []]) {
    const l = layout({ areas: n.map((k, i) => area(`p${i}`, k)) });
    assert.ok(l.props.length >= 8, `${n}: ${l.props.length} things`);
    const feet = l.props.map(footprint);
    for (const [i, f] of feet.entries()) {
      assert.ok(within(l.room, f), `${n}: ${l.props[i].kind} is in the room`);
      for (const a of l.areas) {
        for (const pod of a.pods) assert.ok(!overlaps(f, pod), `${n}: ${l.props[i].kind} is off ${a.project}'s pods`);
        const counter = { x0: a.lead.x - COUNTER.w / 2, z0: a.lead.z - 1.2, x1: a.lead.x + COUNTER.w / 2, z1: a.lead.z + COUNTER.d / 2 };
        assert.ok(!overlaps(f, counter), `${n}: ${l.props[i].kind} is clear of ${a.project}'s counter`);
      }
      for (let j = i + 1; j < feet.length; j++) assert.ok(!overlaps(f, feet[j]), `${n}: ${l.props[i].kind} and ${l.props[j].kind}`);
    }
  }
});

for (const sizes of [[5, 1, 10], [30, 4, 2]])
test(`every spot is free floor, and you can walk from the lobby to each one (${sizes})`, () => {
  const l = layout({ areas: sizes.map((k, i) => area(`p${i}`, k)) });
  const r = 0.3;
  const boxes = walkable(l, r);
  const room = { x0: l.room.x0 + r, z0: l.room.z0 + r, x1: l.room.x1 - r, z1: l.room.z1 - r };
  const spots = ['board', ...l.areas.flatMap((a) => [`${a.project}/lead`, ...a.desks.map((d) => d.id)])];
  for (const ref of spots) {
    const s = spotOf(l, ref)!;
    assert.ok(s, ref);
    assert.ok(!blocked(boxes, room, s), `${ref}'s spot is free`);
    const route = path(boxes, room, l.spawn, s);
    assert.ok(route.length > 0, `${ref} can be walked to`);
    assert.deepEqual(route[route.length - 1], s);
    assert.equal(nearest(l, s), ref, `standing at ${ref} is at it`);
  }
  assert.equal(nearest(l, l.spawn), undefined);
});

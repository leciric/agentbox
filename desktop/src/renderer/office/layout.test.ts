import assert from 'node:assert/strict';
import { test } from 'node:test';
import { boardRef, columns, COUNTER, floorsOf, footOf, layout, nearest, ROOM_DESKS, roomAt, roomLayout, roomOf, spotOf, walkable, type Box, type Layout } from './layout.ts';
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

test('pods go a little wider than deep', () => {
  assert.deepEqual([0, 1, 2, 3, 4, 6, 8, 12, 18].map(columns), [1, 1, 2, 3, 2, 3, 4, 4, 6]);
});

test('a project of more desks than a room holds gets floors, evenly', () => {
  assert.deepEqual([0, 1, 12, 13, 25, 40].map(floorsOf), [[0], [1], [12], [7, 6], [9, 8, 8], [10, 10, 10, 10]]);
  const c = layout({ areas: [area('big', 25), area('small', 3)] });
  assert.deepEqual(c.rooms.map((r) => [r.project, r.floor, r.floors, r.desks.length, r.lead]), [
    ['big', 1, 3, 9, true],
    ['big', 2, 3, 8, false],
    ['big', 3, 3, 8, false],
    ['small', 1, 1, 3, true],
  ]);
  for (const r of c.rooms) assert.ok(r.desks.length <= ROOM_DESKS);
});

test("each room is a diorama of its own, the campus about twice as wide as deep, none overlapping", () => {
  for (const n of [[1], [3, 2], [30, 4, 2], [3, 7, 1, 12, 2]]) {
    const c = layout({ areas: n.map((k, i) => area(`p${i}`, k)) });
    const r = c.room.x1 / c.room.z1;
    assert.ok(r > 1.0 && r < 4.5, `${n}: ${r.toFixed(2)}`);
    for (const room of c.rooms) assert.ok(within(c.room, room.box), `${room.key} is in the campus`);
    for (let i = 0; i < c.rooms.length; i++) for (let j = i + 1; j < c.rooms.length; j++) assert.ok(!overlaps(c.rooms[i].box, c.rooms[j].box), `${c.rooms[i].key} and ${c.rooms[j].key}`);
    // A room is small: about the target's, never the size of a whole office.
    for (const room of c.rooms) assert.ok(room.box.x1 - room.box.x0 < 32 && room.box.z1 - room.box.z0 < 18, `${room.key} is small`);
  }
});

test('an empty office is still a room with a board and a commons', () => {
  const c = layout({ areas: [] });
  assert.equal(c.areas.length, 0);
  assert.equal(c.rooms.length, 1);
  assert.ok(c.rooms[0].box.x1 >= 16);
  assert.ok(c.spawn.z > c.rooms[0].board.spot.z);
});

test('every desk is on its own area in its own room, and found there', () => {
  const c = layout({ areas: [area('a', 3), area('b', 14), area('c', 1)] });
  for (const a of c.areas) {
    const room = c.rooms[a.room];
    assert.ok(within(room.box, a.rug), `${a.project}'s zone is in its room`);
    for (const d of a.desks) {
      assert.ok(d.x > a.rug.x0 && d.x < a.rug.x1 && d.z > a.rug.z0 && d.z < a.rug.z1);
      assert.equal(roomOf(c, d.id), a.room);
      assert.equal(roomAt(c, d.spot), a.room);
    }
  }
  assert.equal(roomOf(c, 'b/lead'), c.rooms.findIndex((r) => r.key === 'b#1'));
  assert.equal(roomOf(c, boardRef(c.rooms[2])), 2);
});

test("a desk keeps its place while another project's agents come", () => {
  const before = layout({ areas: [area('a', 2), area('b', 2)] });
  const after = layout({ areas: [area('a', 2), area('b', 5)] });
  const at = (c: ReturnType<typeof layout>) => {
    const d = c.areas[0].desks[0];
    const r = c.rooms[c.areas[0].room];
    return [d.x - r.ox, d.z - r.oz];
  };
  assert.deepEqual(at(after), at(before));
});

const footprint = (p: Layout['props'][number]): Box => {
  const [w, d] = footOf(p.kind);
  const turned = Math.abs(Math.sin(p.rot)) > 0.5;
  const hw = (turned ? d : w) / 2;
  const hd = (turned ? w : d) / 2;
  return { x0: p.x - hw, z0: p.z - hd, x1: p.x + hw, z1: p.z + hd };
};

test('every room is furnished, and nothing stands on anything else', () => {
  // Names whose commons are each of its three kinds.
  for (const name of ['p', 'storefront', 'quill', 'agentbox', 'x', 'yy'])
  for (const n of [12, 9, 8, 4, 2, 1, 0]) {
    const l = roomLayout({ areas: n ? [area(name, n)] : [] });
    // A small team's room is small: a lounge and a corner.
    assert.ok(l.props.length >= (n > 0 && n <= 4 ? 6 : 8), `${n}: ${l.props.length} things`);
    // Mats are floor, walked over: what's on them mustn't overlap but may sit on one.
    const solid = l.props.filter((p) => p.kind !== 'mat');
    const feet = solid.map(footprint);
    for (const [i, f] of feet.entries()) {
      assert.ok(within(l.room, f), `${n}: ${solid[i].kind} is in the room`);
      assert.ok(!overlaps(f, l.sofa), `${n}: ${solid[i].kind} is clear of the sofa`);
      for (const a of l.areas) {
        for (const pod of a.pods) assert.ok(!overlaps(f, pod), `${n}: ${solid[i].kind} is off ${a.project}'s pods`);
        if (!a.lead) continue;
        const counter = { x0: a.lead.x - COUNTER.w / 2, z0: a.lead.z - 1.2, x1: a.lead.x + COUNTER.w / 2, z1: a.lead.z + COUNTER.d / 2 };
        assert.ok(!overlaps(f, counter), `${n}: ${solid[i].kind} is clear of ${a.project}'s counter`);
      }
      for (let j = i + 1; j < feet.length; j++) assert.ok(!overlaps(f, feet[j]), `${n}: ${solid[i].kind} and ${solid[j].kind}`);
    }
  }
});

for (const sizes of [[5, 1, 10], [30, 4, 2]])
  test(`every spot is free floor, and you can walk to each from its room's door (${sizes})`, () => {
    const c = layout({ areas: sizes.map((k, i) => area(`p${i}`, k)) });
    const r = 0.3;
    const boxes = walkable(c, r);
    const spots = [...c.rooms.map(boardRef), ...c.areas.flatMap((a) => [...(a.lead ? [`${a.project}/lead`] : []), ...a.desks.map((d) => d.id)])];
    for (const ref of spots) {
      const s = spotOf(c, ref)!;
      assert.ok(s, ref);
      const room = c.rooms[roomOf(c, ref)!];
      const bounds = { x0: room.box.x0 + r, z0: room.box.z0 + r, x1: room.box.x1 - r, z1: room.box.z1 - r };
      assert.ok(!blocked(boxes, bounds, s), `${ref}'s spot is free`);
      const route = path(boxes, bounds, room.spawn, s);
      assert.ok(route.length > 0, `${ref} can be walked to`);
      assert.deepEqual(route[route.length - 1], s);
      assert.equal(nearest(c, s), ref, `standing at ${ref} is at it`);
    }
    for (const room of c.rooms) assert.equal(nearest(c, room.spawn), undefined);
  });

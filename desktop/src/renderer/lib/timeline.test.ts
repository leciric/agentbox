import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { clockAt, frameAt, layout, marks, minStep, mmss, placedAt, spanOf, trackAt } from './timeline.ts';

const t0 = Date.parse('2026-10-17T09:00:00Z');
const iso = (secs: number) => new Date(t0 + secs * 1000).toISOString();
const step = (kind: string, from: number, to: number, extra: Partial<T.TimelineStep> = {}) => ({ kind, title: '', start: iso(from), end: iso(to), item: kind, ...extra }) as T.TimelineStep;
const media = (id: string, kind: string, at: number, duration?: number) => ({ id, kind, createdAt: iso(at), meta: duration ? { duration } : {} }) as T.MediaItem;

const tl: T.Timeline = {
  start: iso(0),
  end: iso(4000),
  steps: [
    step('task', 0, 0),
    step('edit', 60, 660),
    // An hour waiting between the edits and the check.
    step('desktop', 4000, 4100, { media: [media('rec', 'recording', 4010, 150), media('shot', 'screenshot', 4050)] }),
    step('page', 4200, 4205, { pages: [{ title: 'Before and after', item: 'p1' }] }),
  ],
};

test('layout puts steps end to end, the time between them taken out, longer ones longer but not in proportion', () => {
  const track = layout(tl);
  // The desktop check's recording runs past its last call: 4010 + 150 − 4000.
  const spans = [spanOf(0), spanOf(600), spanOf(160), spanOf(5)];
  let at = 0;
  assert.deepEqual(
    track.steps.map((p) => [p.from, p.to]),
    spans.map((s) => [at, (at += s)]),
  );
  assert.equal(spanOf(0), minStep);
  assert.equal(spanOf(60), minStep + 60);
  assert.equal(spanOf(180), minStep + 120);
  // Ten minutes of edits take under twice the track of the 160 seconds on the desktop.
  assert.ok(spanOf(600) < 2 * spanOf(160));
  assert.equal(placedAt(track, minStep + 1)?.step.kind, 'edit');
});

test('clockAt and trackAt turn a point of the track into the time, and back', () => {
  const track = layout(tl);
  const edit = track.steps[1]!;
  const x = (edit.from + edit.to) / 2;
  assert.equal(clockAt(track, x), t0 + 360_000);
  assert.equal(trackAt(track, t0 + 360_000), x);
  // Between steps, the time is at the end of the one before.
  assert.equal(trackAt(track, t0 + 2000_000), edit.to);
  assert.equal(trackAt(track, t0 - 5000), 0);
  // A short step is stretched over its span.
  const page = track.steps[3]!;
  assert.equal(clockAt(track, (page.from + page.to) / 2), t0 + 4202_500);
});

test('frameAt seeks a recording running at that moment, then a screenshot, then the step', () => {
  const track = layout(tl);
  const at = (secs: number) => frameAt(track, trackAt(track, t0 + secs * 1000));
  const rec = at(4100);
  assert.equal(rec?.kind, 'recording');
  assert.equal(rec?.kind === 'recording' && rec.offset, 90);
  // Before the recording started, the step's first screenshot.
  assert.equal(at(4005)?.kind, 'screenshot');
  assert.equal(at(300)?.kind, 'step');
});

test('marks place screenshots and pages as ticks and recordings as spans', () => {
  const track = layout(tl);
  const m = marks(track);
  const desk = track.steps[2]!;
  const on = (secs: number) => desk.from + (secs / 160) * (desk.to - desk.from);
  assert.deepEqual(m.find((x) => x.id === 'rec'), { kind: 'recording', from: on(10), to: desk.to, id: 'rec' });
  assert.deepEqual(m.find((x) => x.id === 'shot'), { kind: 'screenshot', at: on(50), id: 'shot' });
  assert.equal(m.find((x) => x.id === 'p1')?.kind, 'page');
});

test('mmss', () => {
  assert.equal(mmss(65), '1:05');
  assert.equal(mmss(3725), '1:02:05');
});

// An agent's work as a timeline (components/timeline): the steps the daemon
// read out of its chat (internal/timeline), laid out along one track the
// scrubber moves over, and what the player shows at each point of it.
//
// The track isn't the clock. An agent works in bursts, and waits between
// them, for the user's answer, for CI, overnight for a review: drawn to scale
// the work would be slivers between long gaps. So the track puts the steps
// end to end with the time between them taken out, and gives each a span
// that grows with its length but slower (spanOf): a two-second test run can
// still be found, and a quarter of an hour of edits doesn't squeeze the
// minute on the desktop, which is what there is to watch, into a sliver.
// Within a step the track runs evenly with the clock, which is what lets a
// point of it seek a recording made during the step.
import type * as T from '../../shared/api';
import { recordedFor, videoAt } from './recording.ts';

export type StepKind = 'task' | 'read' | 'plan' | 'edit' | 'test' | 'run' | 'desktop' | 'page' | 'pr';
export const stepKinds: StepKind[] = ['task', 'read', 'plan', 'edit', 'test', 'run', 'desktop', 'page', 'pr'];
export const kindOf = (s: T.TimelineStep): StepKind => (stepKinds.includes(s.kind as StepKind) ? (s.kind as StepKind) : 'run');

// minStep is the least a step takes of the track, in seconds; spanOf is
// what a step that took secs seconds takes: minStep and another minute for
// every doubling of its length in minutes.
export const minStep = 20;
export const spanOf = (secs: number) => minStep + 60 * Math.log2(1 + Math.max(0, secs) / 60);

const ms = (iso: string) => new Date(iso).getTime();

// A step's place on the track, in seconds from its start, and on the clock.
export type Placed = { step: T.TimelineStep; index: number; from: number; to: number; start: number; end: number };

export type Track = { steps: Placed[]; length: number };

// layout is a timeline's track.
export function layout(tl: Pick<T.Timeline, 'steps'>): Track {
  let at = 0;
  const steps = tl.steps.map((step, index) => {
    const start = ms(step.start);
    // A step ends no earlier than its media: a recording runs past the
    // call that started it.
    let end = Math.max(start, ms(step.end));
    for (const m of step.media ?? []) end = Math.max(end, mediaEnd(m));
    const placed = { step, index, from: at, to: at + spanOf((end - start) / 1000), start, end };
    at = placed.to;
    return placed;
  });
  return { steps, length: at };
}

// mediaEnd is when a recording stopped (it is kept with when it started),
// or when anything else was made, in milliseconds.
export const mediaEnd = (m: Pick<T.MediaItem, 'createdAt' | 'meta'>) => ms(m.createdAt) + recordedFor(m.meta) * 1000;

// placedAt is the step at point x of the track.
export function placedAt(track: Track, x: number): Placed | undefined {
  let found = track.steps[0];
  for (const p of track.steps) if (p.from <= x) found = p;
  return found;
}

// clockAt is the time, in milliseconds, at point x of the track.
export function clockAt(track: Track, x: number): number {
  const p = placedAt(track, x);
  if (!p) return 0;
  const f = Math.min(1, Math.max(0, (x - p.from) / (p.to - p.from)));
  return p.start + f * (p.end - p.start);
}

// trackAt is where on the track the time t (milliseconds) falls: in the
// last step to start by then, and at the end of it when t is past its end.
export function trackAt(track: Track, t: number): number {
  let found: Placed | undefined;
  for (const p of track.steps) if (p.start <= t) found = p;
  if (!found) return 0;
  if (found.end <= found.start) return found.from;
  const f = Math.min(1, (t - found.start) / (found.end - found.start));
  return found.from + f * (found.to - found.from);
}

// A frame is what the player shows at a point: a recording seeked to a
// second of it, a screenshot, or, when the step made neither, its own text.
export type Frame = { kind: 'recording'; media: T.MediaItem; offset: number } | { kind: 'screenshot'; media: T.MediaItem } | { kind: 'step'; placed: Placed };

// frameAt is the frame at point x: a recording running at that moment, then
// the step's latest screenshot by then (its first, before any), then the
// step itself.
export function frameAt(track: Track, x: number): Frame | undefined {
  const p = placedAt(track, x);
  if (!p) return undefined;
  const t = clockAt(track, x);
  const media = p.step.media ?? [];
  for (const m of media) {
    if (m.kind !== 'recording' || !m.meta?.duration) continue;
    const from = ms(m.createdAt);
    if (t >= from && t <= mediaEnd(m)) return { kind: 'recording', media: m, offset: videoAt(m.meta, (t - from) / 1000) };
  }
  const shots = media.filter((m) => m.kind === 'screenshot');
  const shot = shots.filter((m) => ms(m.createdAt) <= t).pop() ?? shots[0];
  if (shot) return { kind: 'screenshot', media: shot };
  return { kind: 'step', placed: p };
}

// marks are where the track's media are: a tick per screenshot and page, a
// span per recording.
export type Mark = { kind: 'screenshot' | 'page' | 'report'; at: number; id: string } | { kind: 'recording'; from: number; to: number; id: string };
export function marks(track: Track): Mark[] {
  const out: Mark[] = [];
  for (const p of track.steps) {
    for (const m of p.step.media ?? []) {
      if (m.kind === 'recording') out.push({ kind: 'recording', from: trackAt(track, ms(m.createdAt)), to: trackAt(track, mediaEnd(m)), id: m.id });
      else if (m.kind === 'screenshot' || m.kind === 'report') out.push({ kind: m.kind, at: trackAt(track, ms(m.createdAt)), id: m.id });
    }
    for (const pg of p.step.pages ?? []) out.push({ kind: 'page', at: p.from + (p.to - p.from) / 2, id: pg.item });
  }
  return out;
}

// fileName is a path's last part, for a step's title ("Edited App.tsx").
export const fileName = (path: string) => path.slice(path.lastIndexOf('/') + 1);

// mmss is a span of seconds as m:ss, or h:mm:ss from an hour.
export function mmss(s: number): string {
  s = Math.max(0, Math.floor(s));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

import type * as T from '../../shared/api';

// A recording is trimmed as it is made (internal/desktop/trim.go): where the
// screen stood still, the video moves on, and its meta lists the stretches
// left out, in seconds of the wall clock since it started. These place a
// moment of the wall clock, a note or a step, on the video's own timeline.

// recordedFor is how long a recording took on the wall clock, in seconds:
// longer than its video when it was trimmed.
export function recordedFor(meta: Pick<T.MediaMeta, 'duration' | 'recorded'> | undefined): number {
  return meta?.recorded || meta?.duration || 0;
}

// videoAt is where the wall clock's at seconds land in the video: earlier by
// all that was cut before it, and, inside a stretch that was cut, where that
// stretch was cut from.
export function videoAt(meta: Pick<T.MediaMeta, 'cuts'> | undefined, at: number): number {
  let v = at;
  for (const c of meta?.cuts ?? []) {
    if (at >= c.to) v -= c.to - c.from;
    else if (at > c.from) v -= at - c.from;
  }
  return v;
}

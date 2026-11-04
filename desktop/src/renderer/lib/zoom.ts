// How the review sizes and zooms a piece of proof (components/ReviewZoom.tsx):
// a picture or a recording, or the two halves of a before/after pair, which
// share one view so they zoom and pan together.
//
// A view is kept as a scale (screen pixels per pixel of the picture) and the
// point of the picture at the middle of its viewport, not as an offset: the
// same view then holds in two viewports of the same size, in the review's
// stage and in the viewer that fills it, and through a resize. 'fit' is no
// zoom yet, which each viewport works out for its own size (fitView).

export interface Size {
  w: number;
  h: number;
}

export type View = 'fit' | { scale: number; cx: number; cy: number };

// The most a picture is enlarged, in its own pixels: past 8× a screenshot is
// only bigger squares.
const most = 8;
// step is one press of + or −, and a notch of the wheel is a fraction of it.
export const zoomStep = 1.25;

// contain is the scale at which all of a picture fits its viewport.
export const contain = (port: Size, pic: Size) => Math.min(port.w / pic.w, port.h / pic.h);

// fitView is what 'fit' shows: all of the picture, no larger than its own
// pixels; or, for one so much taller (or wider) than its viewport that all of
// it would fill less than a third of it, the picture across its viewport's
// width (or height) from its top (or left) edge, to scroll along, as a phone's
// full-page screenshot is read.
export function fitView(port: Size, pic: Size): { scale: number; cx: number; cy: number } {
  const all = Math.min(contain(port, pic), 1);
  if ((pic.w * all) / port.w < 1 / 3) return clamp({ scale: Math.min(port.w / pic.w, 1), cx: pic.w / 2, cy: 0 }, port, pic);
  if ((pic.h * all) / port.h < 1 / 3) return clamp({ scale: Math.min(port.h / pic.h, 1), cx: 0, cy: pic.h / 2 }, port, pic);
  return { scale: all, cx: pic.w / 2, cy: pic.h / 2 };
}

// isSliver is whether fitView scrolls the picture in this viewport instead of
// fitting all of it; box asks it so the two never disagree.
export const isSliver = (port: Size, pic: Size) => {
  const all = Math.min(contain(port, pic), 1);
  return (pic.w * all) / port.w < 1 / 3 || (pic.h * all) / port.h < 1 / 3;
};

// limits are the least and the most a picture zooms to in a viewport: out to
// all of it (or its own pixels, if smaller), in to 8× its pixels.
export function limits(port: Size, pic: Size): [number, number] {
  const all = Math.min(contain(port, pic), 1);
  return [all, Math.max(most, contain(port, pic))];
}

// resolve is a view as it lies in a viewport.
export const resolve = (v: View, port: Size, pic: Size) => (v === 'fit' ? fitView(port, pic) : clamp(v, port, pic));

// clamp keeps a view on its picture: a picture narrower than its viewport sits
// in its middle, a wider one never pans past its edges; the same down.
export function clamp(v: { scale: number; cx: number; cy: number }, port: Size, pic: Size) {
  const axis = (c: number, portSize: number, picSize: number) => {
    const half = portSize / 2 / v.scale;
    return picSize <= half * 2 ? picSize / 2 : Math.min(picSize - half, Math.max(half, c));
  };
  return { scale: v.scale, cx: axis(v.cx, port.w, pic.w), cy: axis(v.cy, port.h, pic.h) };
}

// zoomAt is v zoomed by factor around (px, py), a point of the viewport, which
// stays over the same pixel of the picture; within limits.
export function zoomAt(v: View, factor: number, px: number, py: number, port: Size, pic: Size): View {
  const from = resolve(v, port, pic);
  const [least, max] = limits(port, pic);
  const scale = Math.min(max, Math.max(least, from.scale * factor));
  const nx = from.cx + (px - port.w / 2) / from.scale;
  const ny = from.cy + (py - port.h / 2) / from.scale;
  return clamp({ scale, cx: nx - (px - port.w / 2) / scale, cy: ny - (py - port.h / 2) / scale }, port, pic);
}

// panBy is v moved by (dx, dy) screen pixels, as a drag moves the picture.
export function panBy(v: View, dx: number, dy: number, port: Size, pic: Size): View {
  const from = resolve(v, port, pic);
  return clamp({ ...from, cx: from.cx - dx / from.scale, cy: from.cy - dy / from.scale }, port, pic);
}

// canPan is whether some of the picture is out of sight, which a drag or the
// wheel can bring in.
export function canPan(v: View, port: Size, pic: Size): boolean {
  const { scale } = resolve(v, port, pic);
  return pic.w * scale > port.w + 0.5 || pic.h * scale > port.h + 0.5;
}

// transform is where the picture goes in its viewport, scaled from its own
// size at its top-left corner.
export function transform(v: View, port: Size, pic: Size): string {
  const { scale, cx, cy } = resolve(v, port, pic);
  return `translate(${port.w / 2 - cx * scale}px, ${port.h / 2 - cy * scale}px) scale(${scale})`;
}

// percent is a view's scale as the viewer shows it: 100% is the picture's own
// pixels.
export const percent = (v: View, port: Size, pic: Size) => Math.round(resolve(v, port, pic).scale * 100);

// box is the size the review's stage gives a picture, out of the room it
// has: all of it, as large as fits and no larger than its own pixels, the box
// hugging it so nothing is letterboxed; or, for a picture so tall (or wide)
// that all of it would be a sliver, a box as wide as the stage allows (up to
// its pixels, and to 480px across for a tall one, a phone's width doubled)
// and as tall as the room, which the picture scrolls in (fitView).
export function box(room: Size, pic: Size): Size {
  const all = Math.min(contain(room, pic), 1);
  const w = pic.w * all;
  const h = pic.h * all;
  const hug = { w: Math.floor(w), h: Math.floor(h) };
  // The sliver box is used only if fitView, given that box as its viewport,
  // also calls the picture a sliver; otherwise it would fit the picture inside
  // it with bars down its sides (or along its top), which hugging avoids.
  if (w / room.w < 1 / 3 && h >= room.h - 1) {
    const tall = { w: Math.floor(Math.min(room.w, pic.w, Math.max(room.w / 3, 480))), h: Math.floor(room.h) };
    return isSliver(tall, pic) ? tall : hug;
  }
  if (h / room.h < 1 / 3 && w >= room.w - 1) {
    const wide = { w: Math.floor(room.w), h: Math.floor(Math.min(room.h, pic.h, Math.max(room.h / 3, 240))) };
    return isSliver(wide, pic) ? wide : hug;
  }
  return hug;
}

// A pair's halves go side by side or one over the other, whichever shows them
// larger: two wide screenshots read better stacked, two phones side by side.
export type Arrangement = 'columns' | 'rows';

export function arrange(room: Size, pic: Size, gap: number): { arrangement: Arrangement; half: Size } {
  const columns = box({ w: (room.w - gap) / 2, h: room.h }, pic);
  const rows = box({ w: room.w, h: (room.h - gap) / 2 }, pic);
  return columns.w * columns.h >= rows.w * rows.h ? { arrangement: 'columns', half: columns } : { arrangement: 'rows', half: rows };
}

// sizeOf is a media item's size, when it was recorded with one.
export const sizeOf = (meta: { width?: number; height?: number }): Size | undefined => (meta.width && meta.height ? { w: meta.width, h: meta.height } : undefined);

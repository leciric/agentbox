// Where the window goes, worked out from rectangles alone so it can be tested
// without Electron (windowstate.ts does the Electron side). Every rectangle is
// in DIPs, the unit Electron's screen and setBounds use on every platform, so
// a display's scale factor doesn't enter into it: the same 1440×900 is as big
// to the eye on a 4K monitor at 200% as on a 1080p one at 100%.

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface Display {
  workArea: Rect;
}

// The smallest the window can be: narrow enough for half of a portrait
// 1080p monitor (540) and a quarter of a landscape one (960×540). Below lg
// (1024) the renderer turns the sidebar and the rail into drawers.
export const minSize = { width: 480, height: 480 };
export const defaultSize = { width: 1440, height: 900 };

// titleBar is the strip along the top of the window that has to be on a
// display to drag the window by: a window whose title bar is off every
// display can't be moved back with the mouse.
const titleBar = 32;

function overlap(a: Rect, b: Rect): number {
  const w = Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x);
  const h = Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y);
  return w > 0 && h > 0 ? w * h : 0;
}

// displayOf is the display most of the window is on, or, when it is on none,
// the one whose centre is nearest the window's.
export function displayOf(bounds: Rect, displays: Display[]): Display | undefined {
  let best: Display | undefined;
  let most = 0;
  for (const d of displays) {
    const area = overlap(bounds, d.workArea);
    if (area > most) [best, most] = [d, area];
  }
  if (best) return best;
  const cx = bounds.x + bounds.width / 2;
  const cy = bounds.y + bounds.height / 2;
  let nearest = Infinity;
  for (const d of displays) {
    const dx = d.workArea.x + d.workArea.width / 2 - cx;
    const dy = d.workArea.y + d.workArea.height / 2 - cy;
    if (dx * dx + dy * dy < nearest) [best, nearest] = [d, dx * dx + dy * dy];
  }
  return best;
}

// reachable is whether enough of the window's title bar is on some display
// to grab it: a third of its width, or 120px, whichever is less.
export function reachable(bounds: Rect, displays: Display[]): boolean {
  const bar = { x: bounds.x, y: bounds.y, width: bounds.width, height: titleBar };
  const need = Math.min(120, bounds.width / 3) * titleBar;
  return displays.some((d) => overlap(bar, d.workArea) >= need);
}

// fitInto shrinks the window to the work area, no smaller than minSize, and
// slides it in until it is all on it: a monitor turned to portrait, or
// swapped for a smaller one, leaves no part of the window past its edge.
export function fitInto(bounds: Rect, area: Rect): Rect {
  const width = Math.max(Math.min(bounds.width, area.width), Math.min(minSize.width, bounds.width));
  const height = Math.max(Math.min(bounds.height, area.height), Math.min(minSize.height, bounds.height));
  const x = Math.min(Math.max(bounds.x, area.x), Math.max(area.x, area.x + area.width - width));
  const y = Math.min(Math.max(bounds.y, area.y), Math.max(area.y, area.y + area.height - height));
  return { x: Math.round(x), y: Math.round(y), width: Math.round(width), height: Math.round(height) };
}

// centredOn is a new window on a display: the default size, or a little less
// than its work area when that is smaller, centred.
export function centredOn(display: Display): Rect {
  const area = display.workArea;
  const width = Math.max(minSize.width, Math.min(defaultSize.width, Math.round(area.width * 0.92)));
  const height = Math.max(minSize.height, Math.min(defaultSize.height, Math.round(area.height * 0.92)));
  return fitInto({ x: area.x + (area.width - width) / 2, y: area.y + (area.height - height) / 2, width, height }, area);
}

// placeWindow is where the window opens: where it was last time, fitted to
// that display as it is now, unless that display is gone or has moved so the
// window's title bar is on no display; then centred on the fallback display
// (the one under the pointer).
export function placeWindow(saved: Rect | undefined, displays: Display[], fallback: Display): Rect {
  if (!saved || !(saved.width > 0 && saved.height > 0) || !reachable(saved, displays)) return centredOn(fallback);
  return fitInto(saved, (displayOf(saved, displays) ?? fallback).workArea);
}

// refit is where a window that is already open goes after the displays
// change (one unplugged, rotated, rescaled or rearranged): nowhere while its
// title bar is still on a display and it isn't bigger than the display it is
// mostly on, which leaves a window put across two monitors where it is;
// otherwise fitted to that display, or to the nearest when it is on none.
export function refit(bounds: Rect, displays: Display[]): Rect | undefined {
  const display = displayOf(bounds, displays);
  if (!display) return undefined;
  const area = display.workArea;
  if (reachable(bounds, displays) && bounds.width <= area.width && bounds.height <= area.height) return undefined;
  return fitInto(bounds, area);
}

// meterPercent is how full a bar of used out of total is drawn, 0 to 100, or
// undefined when total says nothing: a host that hasn't reported its memory
// sends 0 of 0, which divides to NaN and used to draw as a full bar.
export function meterPercent(used: number, total: number): number | undefined {
  if (!(total > 0) || !Number.isFinite(used)) return undefined;
  return Math.min(Math.max(used / total, 0), 1) * 100;
}

// fitCount is how many of a row's items, of these widths and this gap, fit in
// width on one line, leaving room for a "more" button of moreWidth whenever
// some don't: Home's status chips, with the rest behind that button.
export function fitCount(widths: number[], width: number, gap: number, moreWidth: number): number {
  const total = widths.reduce((sum, w) => sum + w, 0) + gap * Math.max(widths.length - 1, 0);
  if (total <= width) return widths.length;
  let used = moreWidth;
  for (let i = 0; i < widths.length; i++) {
    used += widths[i] + gap;
    if (used > width) return i;
  }
  return widths.length;
}

import { useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react';
import { cn } from '../../lib/utils';
import { useWindow } from './useWindow';

const rowHeight = 24;
// A column's width in pixels for its width in characters, at 12px mono.
const charWidth = 7.6;
const cellPadding = 18;

export interface GridCell {
  text: string;
  kind?: 'null' | 'blob' | 'number';
}

// DataGrid is the file viewer's table, for a CSV file and an SQLite table:
// a header that stays on top, row numbers that stay at the left, and only the
// rows and columns on screen drawn, so a file of a million cells scrolls as
// one of ten. widths are in characters.
export function DataGrid({
  columns,
  widths,
  rowCount,
  cell,
  firstRow = 1,
  label,
}: {
  columns: string[];
  widths: number[];
  rowCount: number;
  cell: (row: number, column: number) => GridCell;
  firstRow?: number;
  label?: string;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const { start, end } = useWindow(scroller, rowCount, rowHeight, 20);
  const across = useAcross(scroller);
  const offsets = useMemo(() => {
    const out = [0];
    for (const w of widths) out.push(out[out.length - 1] + Math.round(w * charWidth) + cellPadding);
    return out;
  }, [widths]);
  const gutter = Math.round(String(firstRow + rowCount).length * charWidth) + 20;
  const total = gutter + offsets[offsets.length - 1];
  // The columns in view, and some to spare.
  let first = 0;
  while (first < columns.length - 1 && gutter + offsets[first + 1] < across.left - 200) first++;
  let last = first;
  while (last < columns.length && gutter + offsets[last] < across.left + across.width + 200) last++;
  const visible: number[] = [];
  for (let c = first; c < last; c++) visible.push(c);

  return (
    <div ref={scroller} className="h-full overflow-auto font-mono text-[12px]" role="grid" aria-label={label} aria-rowcount={rowCount + 1} data-file-grid>
      <div className="sticky top-0 z-10 flex border-b border-line bg-composer" style={{ width: total, height: rowHeight }} role="row">
        <span className="sticky left-0 z-10 shrink-0 bg-composer" style={{ width: gutter }} />
        <span className="relative flex-1">
          {visible.map((c) => (
            <span
              key={c}
              className="absolute top-0 truncate border-r border-line-faint px-2 font-medium text-secondary"
              style={{ left: offsets[c], width: offsets[c + 1] - offsets[c], lineHeight: `${rowHeight}px` }}
              title={columns[c]}
              role="columnheader"
            >
              {columns[c]}
            </span>
          ))}
        </span>
      </div>
      <div className="relative" style={{ width: total, height: rowCount * rowHeight + 8 }}>
        {Array.from({ length: Math.max(0, end - start) }, (_, i) => {
          const r = start + i;
          return (
            <div key={r} className="absolute left-0 flex border-b border-line-faint hover:bg-surface-faint" style={{ top: r * rowHeight, height: rowHeight, width: total }} role="row">
              <span className="sticky left-0 z-[1] shrink-0 select-none bg-composer pr-2 text-right tabular-nums text-faint" style={{ width: gutter, lineHeight: `${rowHeight}px` }}>
                {firstRow + r}
              </span>
              <span className="relative flex-1">
                {visible.map((c) => {
                  const v = cell(r, c);
                  return (
                    <span
                      key={c}
                      className={cn(
                        'absolute top-0 truncate whitespace-pre border-r border-line-faint px-2',
                        v.kind === 'number' ? 'text-right tabular-nums text-tertiary' : v.kind ? 'italic text-faint' : 'text-tertiary',
                      )}
                      style={{ left: offsets[c], width: offsets[c + 1] - offsets[c], lineHeight: `${rowHeight}px` }}
                      title={v.text.length > 40 ? v.text.slice(0, 2000) : undefined}
                      role="gridcell"
                    >
                      {v.text.replace(/\r?\n/g, ' ↵ ')}
                    </span>
                  );
                })}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// useAcross is how far a scroller is scrolled across, and how wide it is.
function useAcross(scroller: RefObject<HTMLElement | null>): { left: number; width: number } {
  const [view, setView] = useState({ left: 0, width: 1200 });
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const update = () => setView((v) => (v.left === el.scrollLeft && v.width === el.clientWidth ? v : { left: el.scrollLeft, width: el.clientWidth }));
    update();
    el.addEventListener('scroll', update, { passive: true });
    const observer = new ResizeObserver(update);
    observer.observe(el);
    return () => {
      el.removeEventListener('scroll', update);
      observer.disconnect();
    };
  }, [scroller]);
  return view;
}

// BarButton is a button of the PDF's and the database's bars.
export function BarButton({ label, disabled, onClick, children }: { label: string; disabled?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      className="flex size-6 shrink-0 items-center justify-center rounded-md transition hover:bg-surface-raised hover:text-primary disabled:pointer-events-none disabled:opacity-40 [&_svg]:size-3.5"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

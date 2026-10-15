import { useLayoutEffect, useState, type RefObject } from 'react';

// useWindow is the slice of a list of fixed-height rows that a scroller shows,
// with some to spare either side, so a tree of thousands of entries or a file
// of a hundred thousand lines draws only what is on screen.
export function useWindow(scroller: RefObject<HTMLElement | null>, count: number, rowHeight: number, overscan = 20): { start: number; end: number } {
  const [view, setView] = useState({ top: 0, height: 800 });
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const update = () => setView((v) => (v.top === el.scrollTop && v.height === el.clientHeight ? v : { top: el.scrollTop, height: el.clientHeight }));
    update();
    el.addEventListener('scroll', update, { passive: true });
    const observer = new ResizeObserver(update);
    observer.observe(el);
    return () => {
      el.removeEventListener('scroll', update);
      observer.disconnect();
    };
  }, [scroller]);
  const start = Math.max(0, Math.floor(view.top / rowHeight) - overscan);
  const end = Math.min(count, Math.ceil((view.top + view.height) / rowHeight) + overscan);
  return { start, end };
}

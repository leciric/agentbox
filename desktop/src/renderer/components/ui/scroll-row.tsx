import { ChevronLeft, ChevronRight } from 'lucide-react';
import { useCallback, useLayoutEffect, useRef, useState, type ComponentProps } from 'react';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';

// ScrollRow is a row of chips that scrolls sideways when it's too narrow for
// them, and shows it: each end that has more past it fades out, with a
// chevron that scrolls most of a row's width that way, and a mouse wheel
// scrolls it too. A fade that's always there reads as decoration, and a
// hidden scrollbar left the last chips out of reach (UX test, finding 8).
// Where the row isn't scrolling (Settings' column from @3xl) none of it shows.
// active is the open chip's key: it's brought into view when it changes.
export function ScrollRow({ className, children, active, ...props }: ComponentProps<'nav'> & { active?: string }) {
  const t = useT();
  const row = useRef<HTMLElement>(null);
  const [edges, setEdges] = useState({ start: false, end: false });
  const measure = useCallback(() => {
    const el = row.current;
    if (!el) return;
    const more = el.scrollWidth - el.clientWidth;
    const next = { start: more > 1 && el.scrollLeft > 1, end: more > 1 && el.scrollLeft < more - 1 };
    setEdges((was) => (was.start === next.start && was.end === next.end ? was : next));
  }, []);
  useLayoutEffect(() => {
    const el = row.current;
    if (!el) return;
    // A vertical wheel over the row moves it sideways, where nothing else would.
    const wheel = (event: WheelEvent) => {
      if (el.scrollWidth <= el.clientWidth || Math.abs(event.deltaX) > Math.abs(event.deltaY)) return;
      event.preventDefault();
      el.scrollLeft += event.deltaY;
    };
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    el.addEventListener('scroll', measure, { passive: true });
    el.addEventListener('wheel', wheel, { passive: false });
    return () => {
      observer.disconnect();
      el.removeEventListener('scroll', measure);
      el.removeEventListener('wheel', wheel);
    };
  }, [measure]);
  // Its chips can change without the row changing size (a search, a project added).
  useLayoutEffect(measure);
  useLayoutEffect(() => {
    const el = row.current;
    if (!el || el.scrollWidth <= el.clientWidth) return;
    el.querySelector<HTMLElement>('[aria-current="page"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }, [active]);
  const by = (sign: number) => row.current?.scrollBy({ left: sign * Math.max(120, (row.current?.clientWidth ?? 0) * 0.7), behavior: 'smooth' });
  const fade = edges.start && edges.end ? 'linear-gradient(to right, transparent, black 3rem, black calc(100% - 3rem), transparent)' : edges.start ? 'linear-gradient(to right, transparent, black 3rem)' : edges.end ? 'linear-gradient(to right, black calc(100% - 3rem), transparent)' : undefined;
  const chevron = 'absolute top-1/2 z-10 flex size-6 -translate-y-1/2 items-center justify-center rounded-full border border-line-strong bg-modal text-subtle shadow-sm transition hover:text-title';
  return (
    <div className="relative min-w-0" data-scroll-row={edges.start || edges.end ? [edges.start && 'start', edges.end && 'end'].filter(Boolean).join(' ') : undefined}>
      <nav ref={row} {...props} data-scrolls-sideways className={cn('overflow-x-auto [scrollbar-width:none]', className)} style={{ maskImage: fade, ...props.style }}>
        {children}
      </nav>
      {edges.start && (
        <button type="button" tabIndex={-1} aria-label={t('common.scrollBack')} className={cn(chevron, 'left-0')} onClick={() => by(-1)} data-scroll-row-back>
          <ChevronLeft className="size-3.5" />
        </button>
      )}
      {edges.end && (
        <button type="button" tabIndex={-1} aria-label={t('common.scrollMore')} className={cn(chevron, 'right-0')} onClick={() => by(1)} data-scroll-row-more>
          <ChevronRight className="size-3.5" />
        </button>
      )}
    </div>
  );
}

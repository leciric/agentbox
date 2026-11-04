import { CircleAlert, Minus, Pause, Play, Plus } from 'lucide-react';
import { useEffect, useImperativeHandle, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode, type Ref, type RefObject } from 'react';
import type * as T from '../../shared/api';
import { useT } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { cn } from '../lib/utils';
import { canPan, limits, panBy, percent, resolve, transform, zoomAt, zoomStep, type Size, type View } from '../lib/zoom';
import { Kbd } from './ui/card';

// The review's zoom (lib/zoom.ts): a viewport a picture or a recording is
// shown in, which Ctrl or ⌘ and the wheel, a trackpad's pinch (which arrives
// as the same) or two fingers zoom around the pointer; the wheel or a drag
// pans once some of it is out of sight; a double-click zooms in, or fits
// again. The view is its owner's (ReviewProof), so a pair's two halves, each
// a viewport of the same size, zoom and pan together, and + − 0 (the
// review's keys) and the viewer's buttons work it too.

// useSize is an element's size as it changes.
export function useSize(ref: RefObject<HTMLElement | null>): Size {
  const [size, setSize] = useState<Size>({ w: 0, h: 0 });
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setSize((s) => (s.w === el.clientWidth && s.h === el.clientHeight ? s : { w: el.clientWidth, h: el.clientHeight }));
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref]);
  return size;
}

// A rect is where the picture lies in its viewport, for what's drawn over it
// unscaled: a half's label, the slider's divider.
export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export function Viewport({
  pic,
  view,
  onView,
  onPort,
  onTap,
  onDrag,
  over,
  className,
  style,
  children,
  ...data
}: {
  pic: Size;
  view: View;
  onView: (v: View) => void;
  // onPort is the viewport's size, for the keys to zoom around its middle.
  onPort?: (port: Size) => void;
  // onTap is a click that didn't drag.
  onTap?: () => void;
  // onDrag, given, takes a drag instead of panning: the fraction of the
  // picture's width under the pointer (the slider's divider).
  onDrag?: (x: number) => void;
  over?: (rect: Rect) => ReactNode;
  className?: string;
  style?: CSSProperties;
  children: ReactNode;
  [data: `data-${string}`]: string | undefined;
}) {
  const el = useRef<HTMLDivElement>(null);
  const port = useSize(el);
  const latest = useRef({ view, port, pic, onView });
  latest.current = { view, port, pic, onView };
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{ x: number; y: number; moved: boolean; pinch?: number } | null>(null);
  const ready = port.w > 0 && port.h > 0;

  useEffect(() => {
    if (ready) onPort?.(port);
  }, [ready, port, onPort]);

  // The wheel has to be taken from the page, which React's passive listener
  // can't do; and only when it zooms, or pans something there is to pan, so
  // otherwise the review scrolls.
  useEffect(() => {
    const node = el.current;
    if (!node) return;
    const wheel = (e: WheelEvent) => {
      const { view: v, port: p, pic: q, onView: set } = latest.current;
      if (!p.w) return;
      const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? p.h : 1;
      const r = node.getBoundingClientRect();
      if (e.ctrlKey || e.metaKey) {
        e.preventDefault();
        const delta = Math.max(-100, Math.min(100, e.deltaY * unit));
        return set(zoomAt(v, Math.exp(-delta * 0.0025), e.clientX - r.left, e.clientY - r.top, p, q));
      }
      if (!canPan(v, p, q)) return;
      const dx = (e.shiftKey && !e.deltaX ? e.deltaY : e.deltaX) * unit;
      const dy = e.shiftKey && !e.deltaX ? 0 : e.deltaY * unit;
      const next = panBy(v, -dx, -dy, p, q);
      const from = resolve(v, p, q);
      if (next !== 'fit' && Math.abs(next.cx - from.cx) + Math.abs(next.cy - from.cy) < 0.01) return;
      e.preventDefault();
      set(next);
    };
    node.addEventListener('wheel', wheel, { passive: false });
    return () => node.removeEventListener('wheel', wheel);
  }, []);

  const r = ready ? resolve(view, port, pic) : undefined;
  const rect: Rect | undefined = r && { x: port.w / 2 - r.cx * r.scale, y: port.h / 2 - r.cy * r.scale, w: pic.w * r.scale, h: pic.h * r.scale };
  const pannable = ready && canPan(view, port, pic);
  const fraction = (clientX: number) => {
    const b = el.current?.getBoundingClientRect();
    return b && rect ? Math.min(1, Math.max(0, (clientX - b.left - rect.x) / rect.w)) : 0.5;
  };

  return (
    <div
      ref={el}
      className={cn(
        'relative touch-none select-none overflow-hidden',
        onDrag ? 'cursor-ew-resize' : pannable ? 'cursor-grab active:cursor-grabbing' : onTap ? 'cursor-zoom-in' : 'cursor-default',
        className,
      )}
      style={style}
      onPointerDown={(e) => {
        if (e.button !== 0) return;
        e.currentTarget.setPointerCapture(e.pointerId);
        pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
        const ps = [...pointers.current.values()];
        gesture.current = { x: e.clientX, y: e.clientY, moved: ps.length > 1, pinch: ps.length === 2 ? Math.hypot(ps[0].x - ps[1].x, ps[0].y - ps[1].y) : undefined };
        if (onDrag && ps.length === 1) onDrag(fraction(e.clientX));
      }}
      onPointerMove={(e) => {
        const g = gesture.current;
        const last = pointers.current.get(e.pointerId);
        if (!g || !last) return;
        pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
        if (Math.abs(e.clientX - g.x) + Math.abs(e.clientY - g.y) > 3) g.moved = true;
        const ps = [...pointers.current.values()];
        if (ps.length === 2 && g.pinch) {
          // Two fingers: zoom around their middle by how far they've spread.
          const d = Math.hypot(ps[0].x - ps[1].x, ps[0].y - ps[1].y);
          const b = el.current!.getBoundingClientRect();
          onView(zoomAt(view, d / g.pinch, (ps[0].x + ps[1].x) / 2 - b.left, (ps[0].y + ps[1].y) / 2 - b.top, port, pic));
          g.pinch = d;
          return;
        }
        if (onDrag) return onDrag(fraction(e.clientX));
        if (pannable) onView(panBy(view, e.clientX - last.x, e.clientY - last.y, port, pic));
      }}
      onPointerUp={(e) => {
        pointers.current.delete(e.pointerId);
        if (pointers.current.size) return;
        if (gesture.current && !gesture.current.moved) onTap?.();
        gesture.current = null;
      }}
      onPointerCancel={(e) => {
        pointers.current.delete(e.pointerId);
        gesture.current = null;
      }}
      onDoubleClick={(e) => {
        if (!ready) return;
        if (view !== 'fit') return onView('fit');
        const b = e.currentTarget.getBoundingClientRect();
        const to = Math.max(1, resolve(view, port, pic).scale * 2);
        onView(zoomAt(view, to / resolve(view, port, pic).scale, e.clientX - b.left, e.clientY - b.top, port, pic));
      }}
      {...data}
    >
      {ready && (
        <div className="absolute left-0 top-0 origin-top-left" style={{ width: pic.w, height: pic.h, transform: transform(view, port, pic) }}>
          {children}
        </div>
      )}
      {rect && <Edges rect={rect} port={port} />}
      {rect && over?.(rect)}
    </div>
  );
}

// Edges shade each edge the picture goes on past, so a tall screenshot shown
// from its top says there's more of it to scroll to.
function Edges({ rect, port }: { rect: Rect; port: Size }) {
  const fade = 'pointer-events-none absolute from-black/60 to-transparent';
  return (
    <>
      {rect.y < -1 && <span className={cn(fade, 'inset-x-0 top-0 h-6 bg-gradient-to-b')} data-review-more="top" />}
      {rect.y + rect.h > port.h + 1 && <span className={cn(fade, 'inset-x-0 bottom-0 h-6 bg-gradient-to-t')} data-review-more="bottom" />}
      {rect.x < -1 && <span className={cn(fade, 'inset-y-0 left-0 w-6 bg-gradient-to-r')} data-review-more="left" />}
      {rect.x + rect.w > port.w + 1 && <span className={cn(fade, 'inset-y-0 right-0 w-6 bg-gradient-to-l')} data-review-more="right" />}
    </>
  );
}

// Label is a half's name, at its picture's top-left corner, or the
// viewport's while that corner is out of sight.
export function Label({ rect, children }: { rect: Rect; children: ReactNode }) {
  return (
    <span
      className="pointer-events-none absolute rounded-md bg-black/75 px-1.5 py-0.5 text-caption font-medium text-white shadow-sm"
      style={{ left: Math.max(8, rect.x + 8), top: Math.max(8, rect.y + 8) }}
    >
      {children}
    </span>
  );
}

// ZoomControls are the viewer's: − the scale + , and Fit, which says it's
// fitted when it is. The keys are the review's: + − 0.
export function ZoomControls({ view, port, pic, onView }: { view: View; port: Size | undefined; pic: Size | undefined; onView: (v: View) => void }) {
  const t = useT();
  const on = !!port && !!pic && port.w > 0;
  const [least, most] = on ? limits(port, pic) : [1, 1];
  const scale = on ? resolve(view, port, pic).scale : 1;
  const by = (f: number) => on && onView(zoomAt(view, f, port.w / 2, port.h / 2, port, pic));
  const button = 'inline-flex size-7 items-center justify-center rounded-md text-white/80 transition-colors hover:bg-white/10 hover:text-white disabled:pointer-events-none disabled:opacity-35';
  return (
    <div className="flex shrink-0 items-center gap-0.5 rounded-lg border border-white/10 bg-white/[0.04] p-0.5 text-caption text-white/80" data-review-zoom-controls>
      <button type="button" className={button} disabled={!on || scale <= least + 1e-6} onClick={() => by(1 / zoomStep)} aria-label={t('review.zoom.out')} title={`${t('review.zoom.out')} (−)`} data-review-zoom-out>
        <Minus className="size-3.5" />
      </button>
      <span className="w-12 text-center font-mono tabular-nums" data-review-zoom={on ? percent(view, port, pic) : undefined}>
        {on ? `${percent(view, port, pic)}%` : '—'}
      </span>
      <button type="button" className={button} disabled={!on || scale >= most - 1e-6} onClick={() => by(zoomStep)} aria-label={t('review.zoom.in')} title={`${t('review.zoom.in')} (+)`} data-review-zoom-in>
        <Plus className="size-3.5" />
      </button>
      <button
        type="button"
        className={cn('inline-flex h-7 items-center gap-1.5 rounded-md px-2 font-medium transition-colors hover:bg-white/10 hover:text-white disabled:pointer-events-none', view === 'fit' ? 'text-white/45' : 'text-white/85')}
        disabled={!on || view === 'fit'}
        onClick={() => onView('fit')}
        title={t('review.zoom.fitTitle')}
        data-review-zoom-fit
      >
        {t('review.zoom.fit')}
        <span className="max-md:hidden" aria-hidden>
          <Kbd>0</Kbd>
        </span>
      </button>
    </div>
  );
}

// clock is seconds as m:ss.
export const clock = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, '0')}`;

export interface ClipHandle {
  toggle: () => void;
  seek: (seconds: number) => void;
}

// Clip is a recording in a viewport, playing muted, with a bar under it: play
// and pause, a scrubber cut at marks (a recording's chapters, titled), and
// the time. A click on the picture plays or pauses it. Given a size it is a
// box that size and its bar; without, it fills its parent.
export function Clip({
  ref,
  item,
  pic,
  size,
  view,
  onView,
  onPort,
  dark,
  marks = [],
  chapter = -1,
  startAt = 0,
  autoPlay = false,
  onTime,
  onUse,
  over,
}: {
  ref?: Ref<ClipHandle>;
  item: T.MediaItem;
  pic: Size;
  size?: Size;
  view: View;
  onView: (v: View) => void;
  onPort?: (port: Size) => void;
  dark: boolean;
  marks?: readonly { at: number; title: string }[];
  chapter?: number;
  startAt?: number;
  autoPlay?: boolean;
  onTime?: (seconds: number) => void;
  // onUse is a press on it: the one Space plays is the one last used.
  onUse?: () => void;
  over?: (rect: Rect) => ReactNode;
}) {
  const t = useT();
  const video = useRef<HTMLVideoElement>(null);
  const [time, setTime] = useState(startAt);
  const [length, setLength] = useState(item.meta.duration ?? 0);
  const [playing, setPlaying] = useState(false);
  const [broken, setBroken] = useState(false);
  const seek = (s: number) => {
    const v = video.current;
    if (!v) return;
    v.currentTime = s;
    void v.play().catch(() => {});
  };
  const toggle = () => {
    const v = video.current;
    if (v) void (v.paused ? v.play().catch(() => {}) : v.pause());
  };
  useImperativeHandle(ref, () => ({ seek, toggle }));
  // The scrubber's segments: one per chapter, or the whole recording.
  const starts = marks.length ? marks.map((c) => c.at) : [0];
  if (broken) {
    return (
      <div className="flex flex-col items-center gap-2 rounded-xl border border-line bg-surface-faint p-10 text-center" style={size && { width: size.w }}>
        <CircleAlert className="size-8 text-faint" />
        <div className="text-sm font-medium text-secondary">{t('agent.mediaTab.unplayable')}</div>
        <div className="max-w-sm text-xs text-subtle">{t('agent.mediaTab.unplayableBody')}</div>
      </div>
    );
  }
  return (
    <div
      className={cn('flex min-w-0 shrink-0 flex-col overflow-hidden rounded-xl border bg-black', dark ? 'border-white/10' : 'border-line-strong', !size && 'size-full min-h-0')}
      style={size && { width: size.w }}
      onPointerDown={onUse}
      data-review-player={item.id}
    >
      <Viewport pic={pic} view={view} onView={onView} onPort={onPort} onTap={toggle} className={cn(!size && 'min-h-0 flex-1')} style={size && { height: size.h }} over={over} data-review-stage="recording">
        <video
          ref={video}
          src={mediaUrl(item)}
          autoPlay={autoPlay}
          muted
          playsInline
          preload="metadata"
          onLoadedMetadata={(e) => {
            if (Number.isFinite(e.currentTarget.duration)) setLength(e.currentTarget.duration);
            if (startAt) e.currentTarget.currentTime = startAt;
          }}
          onTimeUpdate={(e) => {
            setTime(e.currentTarget.currentTime);
            onTime?.(e.currentTarget.currentTime);
          }}
          onPlay={() => {
            setPlaying(true);
            onUse?.();
          }}
          onPause={() => setPlaying(false)}
          onError={() => setBroken(true)}
          className="block size-full object-contain"
        />
      </Viewport>
      <div className="flex shrink-0 items-center gap-3 px-3 py-2 text-white">
        <button type="button" onClick={toggle} aria-label={t(playing ? 'review.pause' : 'review.play')} title={`${t(playing ? 'review.pause' : 'review.play')} (Space)`} className="text-white/85 hover:text-white">
          {playing ? <Pause className="size-4 fill-current" /> : <Play className="size-4 fill-current" />}
        </button>
        <div className="flex h-5 min-w-0 flex-1 items-center gap-1">
          {starts.map((start, i) => {
            const end = starts[i + 1] ?? length;
            const span = Math.max(end - start, 0.001);
            const p = Math.max(0, Math.min(1, (time - start) / span));
            return (
              <button
                key={`${start}-${i}`}
                type="button"
                title={marks[i]?.title}
                aria-label={marks[i]?.title ?? t('review.play')}
                onClick={(e) => {
                  const r = e.currentTarget.getBoundingClientRect();
                  seek(start + ((e.clientX - r.left) / r.width) * span);
                }}
                className="group flex h-full min-w-1 items-center"
                style={{ flex: `${span} 1 0` }}
              >
                <span className={cn('block h-1 w-full overflow-hidden rounded-full bg-white/25 transition-[height] group-hover:h-1.5', i === chapter && marks.length > 0 && 'h-1.5')}>
                  <span className="block h-full bg-white" style={{ width: `${p * 100}%` }} />
                </span>
              </button>
            );
          })}
        </div>
        <span className="shrink-0 text-caption tabular-nums text-white/70">
          {clock(time)} / {clock(length)}
        </span>
      </div>
    </div>
  );
}

// barHeight is a clip's bar, which its room leaves for it.
export const barHeight = 37;

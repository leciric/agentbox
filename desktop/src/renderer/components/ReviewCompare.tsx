import { Columns2, FlipHorizontal2, Highlighter, SplitSquareHorizontal } from 'lucide-react';
import { useEffect, useImperativeHandle, useLayoutEffect, useRef, useState, type ReactNode, type Ref } from 'react';
import type * as T from '../../shared/api';
import type { Pair, Side } from '../../shared/pairs';
import { clampZoom, compareModes, nextMode, noZoom, pixelDiff, setComparePrefs, useComparePrefs, zoomAt, type CompareMode, type Zoom } from '../lib/compare';
import { useT } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { cn, errorMessage } from '../lib/utils';
import { Kbd } from './ui/card';

// The review's stage for a before/after pair of screenshots
// (shared/pairs.ts): the two side by side, zoomed and panned together; one
// over the other with a divider to drag; or one at a time, a click or Space
// flipping between them. Highlight marks the pixels that changed, for two
// of the same size. Q cycles the modes and H highlights (ReviewProof's
// handle, the keys being the review's), and the mode is remembered
// (lib/compare.ts). A pair of recordings isn't compared: two walkthroughs
// rarely show the same screen at the same moment, so ReviewVideos plays
// them one after the other, each with its own controls.

export interface CompareHandle {
  cycle: () => void;
  highlight: () => void;
  // space is the review's Space: Flip flips, the other modes do nothing; a
  // pair of recordings plays or pauses the one last used.
  space: () => void;
}

const modeIcon: Record<CompareMode, typeof Columns2> = { side: Columns2, slider: SplitSquareHorizontal, flip: FlipHorizontal2 };
const modeLabel = { side: 'review.compare.side', slider: 'review.compare.slider', flip: 'review.compare.flip' } as const;

export function ReviewCompare({ ref, pair, expanded }: { ref?: Ref<CompareHandle>; pair: Pair; expanded: boolean }) {
  const t = useT();
  const { mode, highlight } = useComparePrefs();
  const [side, setSide] = useState<Side>('after');
  const [split, setSplit] = useState(0.5);
  const [zoom, setZoom] = useState<Zoom>(noZoom);
  const difference = useDifference(pair, highlight);
  const flip = () => setSide((s) => (s === 'after' ? 'before' : 'after'));
  useImperativeHandle(ref, () => ({
    cycle: () => setComparePrefs({ mode: nextMode(mode) }),
    highlight: () => setComparePrefs({ highlight: !highlight }),
    space: () => mode === 'flip' && flip(),
  }));

  const aspect = aspectOf(pair.after.meta) ?? aspectOf(pair.before.meta) ?? 16 / 10;
  const overlay = highlight && difference.status === 'ready' ? difference.url : undefined;
  const picture = (s: Side) => (
    <Picture item={pair[s]} label={t(s === 'before' ? 'review.compare.before' : 'review.compare.after')} overlay={overlay} />
  );
  const hint = t(mode === 'side' ? 'review.compare.sideHint' : mode === 'slider' ? 'review.compare.sliderHint' : 'review.compare.flipHint');

  return (
    <div className={cn('grid min-w-0 gap-2', expanded && 'h-full min-h-0 grid-rows-[auto_minmax(0,1fr)]')} data-review-compare={mode} data-review-compare-side={mode === 'flip' ? side : undefined}>
      <div className={cn('flex min-w-0 flex-wrap items-center gap-2 text-[12px]', expanded ? 'text-white/80' : 'text-secondary')}>
        <div role="radiogroup" aria-label={t('review.compare.modes')} className="flex items-center gap-0.5 rounded-lg border border-line bg-surface-faint p-0.5">
          {compareModes.map((m) => {
            const Icon = modeIcon[m];
            return (
              <button
                key={m}
                type="button"
                role="radio"
                aria-checked={mode === m}
                onClick={() => setComparePrefs({ mode: m })}
                title={t('review.compare.modeTitle', { mode: t(modeLabel[m]) })}
                className={cn('inline-flex h-6 items-center gap-1.5 rounded-md px-2 font-medium transition', mode === m ? 'bg-surface-strong text-title' : 'text-subtle hover:text-primary')}
                data-review-compare-mode={m}
              >
                <Icon className="size-3.5" />
                {t(modeLabel[m])}
              </button>
            );
          })}
          <span className="px-1 max-md:hidden" aria-hidden>
            <Kbd>Q</Kbd>
          </span>
        </div>
        <button
          type="button"
          aria-pressed={highlight}
          onClick={() => setComparePrefs({ highlight: !highlight })}
          title={t('review.compare.highlightTitle')}
          className={cn(
            'inline-flex h-7 items-center gap-1.5 rounded-lg border px-2 font-medium transition',
            highlight ? 'border-fuchsia-400/60 bg-fuchsia-500/15 text-fuchsia-200' : 'border-line text-subtle hover:text-primary',
          )}
          data-review-highlight
        >
          <Highlighter className="size-3.5" />
          {t('review.compare.highlight')}
          <span className="max-md:hidden" aria-hidden>
            <Kbd>H</Kbd>
          </span>
        </button>
        {highlight && (
          <span className="min-w-0 truncate text-[11.5px] text-subtle" data-review-highlight-note={difference.status}>
            {differenceNote(t, difference)}
          </span>
        )}
        <span className="ml-auto flex min-w-0 items-center gap-2 text-[11.5px] text-subtle">
          {zoom.scale > 1 && (
            <button type="button" onClick={() => setZoom(noZoom)} className="shrink-0 rounded-md px-1.5 tabular-nums hover:text-primary" data-review-zoom={zoom.scale.toFixed(1)}>
              {Math.round(zoom.scale * 10) / 10}× · {t('review.compare.fit')}
            </button>
          )}
          <span className="min-w-0 truncate max-lg:hidden">{hint}</span>
        </span>
      </div>

      <div className={cn('min-w-0', expanded ? 'min-h-0' : 'h-[min(46vh,600px)]', mode === 'side' && 'grid grid-cols-2 gap-2')}>
        {mode === 'side' ? (
          (['before', 'after'] as const).map((s) => (
            <Frame key={s} aspect={aspect} zoom={zoom} onZoom={setZoom}>
              {picture(s)}
            </Frame>
          ))
        ) : mode === 'slider' ? (
          <Frame aspect={aspect} zoom={zoom} onZoom={setZoom} onDrag={(x) => setSplit(x)}>
            {picture('before')}
            <div className="absolute inset-0" style={{ clipPath: `inset(0 0 0 ${split * 100}%)` }}>
              {picture('after')}
            </div>
            <Divider at={split} />
          </Frame>
        ) : (
          <Frame aspect={aspect} zoom={zoom} onZoom={setZoom} onClick={flip}>
            {(['before', 'after'] as const).map((s) => (
              <div key={s} className={cn('absolute inset-0', s !== side && 'invisible')}>
                {picture(s)}
              </div>
            ))}
          </Frame>
        )}
      </div>
    </div>
  );
}

const aspectOf = (meta: T.MediaMeta) => (meta.width && meta.height ? meta.width / meta.height : undefined);

// Frame is the largest box of the pair's aspect its space holds, so a
// picture fills it exactly and the highlight lies on it pixel for pixel;
// the wheel zooms it around the pointer, a drag pans it (or, given onDrag,
// moves the slider's divider), a click without a drag is onClick, and a
// double-click fits it again.
function Frame({ aspect, zoom, onZoom, onDrag, onClick, children }: { aspect: number; zoom: Zoom; onZoom: (z: Zoom) => void; onDrag?: (x: number) => void; onClick?: () => void; children: ReactNode }) {
  const space = useRef<HTMLDivElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  const drag = useRef<{ x: number; y: number; zoom: Zoom; moved: boolean } | null>(null);
  const latest = useRef(zoom);
  latest.current = zoom;
  useLayoutEffect(() => {
    const el = space.current;
    if (!el) return;
    const fit = () => {
      const { width, height } = el.getBoundingClientRect();
      const w = Math.min(width, height * aspect);
      setSize({ w: Math.floor(w), h: Math.floor(w / aspect) });
    };
    fit();
    const observer = new ResizeObserver(fit);
    observer.observe(el);
    return () => observer.disconnect();
  }, [aspect]);
  // The wheel has to be taken from the page, which React's passive
  // listener can't do.
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      onZoom(zoomAt(latest.current, Math.exp(-e.deltaY * 0.002), e.clientX - r.left, e.clientY - r.top, r.width, r.height));
    };
    el.addEventListener('wheel', wheel, { passive: false });
    return () => el.removeEventListener('wheel', wheel);
  }, [onZoom]);
  const divider = (e: { clientX: number }) => {
    const r = box.current?.getBoundingClientRect();
    if (r && onDrag) onDrag(Math.min(1, Math.max(0, (e.clientX - r.left - zoom.x) / zoom.scale / r.width)));
  };
  return (
    <div ref={space} className="flex size-full min-h-0 min-w-0 items-center justify-center">
      <div
        ref={box}
        className={cn('relative shrink-0 touch-none select-none overflow-hidden rounded-lg border border-line-strong bg-black', onDrag ? 'cursor-ew-resize' : zoom.scale > 1 ? 'cursor-grab' : onClick ? 'cursor-pointer' : 'cursor-zoom-in')}
        style={{ width: size.w, height: size.h }}
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId);
          drag.current = { x: e.clientX, y: e.clientY, zoom, moved: false };
          if (onDrag) divider(e);
        }}
        onPointerMove={(e) => {
          const d = drag.current;
          if (!d) return;
          if (Math.abs(e.clientX - d.x) + Math.abs(e.clientY - d.y) > 3) d.moved = true;
          if (onDrag) return divider(e);
          onZoom(clampZoom({ ...d.zoom, x: d.zoom.x + e.clientX - d.x, y: d.zoom.y + e.clientY - d.y }, size.w, size.h));
        }}
        onPointerUp={() => {
          if (drag.current && !drag.current.moved) onClick?.();
          drag.current = null;
        }}
        onPointerCancel={() => {
          drag.current = null;
        }}
        onDoubleClick={() => onZoom(noZoom)}
        data-review-frame
      >
        <div className="absolute inset-0 origin-top-left" style={{ transform: `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.scale})` }}>
          {children}
        </div>
      </div>
    </div>
  );
}

// Picture is one half of a pair, filling its frame, labelled with its side,
// the highlight over it.
function Picture({ item, label, overlay }: { item: T.MediaItem; label: string; overlay?: string }) {
  return (
    <div className="absolute inset-0" data-review-picture={item.id}>
      <img src={mediaUrl(item)} alt={item.name} draggable={false} className="size-full object-fill" />
      {overlay && <img src={overlay} alt="" draggable={false} className="pointer-events-none absolute inset-0 size-full object-fill [image-rendering:pixelated]" data-review-overlay />}
      <span className="pointer-events-none absolute left-2 top-2 rounded-md bg-black/70 px-1.5 py-0.5 text-[11px] font-medium text-white backdrop-blur">{label}</span>
    </div>
  );
}

// Divider is the slider's line, the before left of it and the after right.
function Divider({ at }: { at: number }) {
  return (
    <div className="pointer-events-none absolute inset-y-0 w-0.5 -translate-x-1/2 bg-white shadow-[0_0_0_1px_rgba(0,0,0,0.4)]" style={{ left: `${at * 100}%` }} data-review-divider>
      <span className="absolute left-1/2 top-1/2 flex size-7 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full border border-black/30 bg-white text-[11px] text-black shadow">⇔</span>
    </div>
  );
}

type Difference = { status: 'off' | 'working' } | { status: 'ready'; url: string; changed: number; total: number } | { status: 'sizes'; before: string; after: string } | { status: 'failed'; error: string };

function differenceNote(t: ReturnType<typeof useT>, d: Difference): string {
  if (d.status === 'working') return t('review.compare.working');
  if (d.status === 'sizes') return t('review.compare.sizes', { before: d.before, after: d.after });
  if (d.status === 'failed') return t('review.compare.failed', { error: d.error });
  if (d.status !== 'ready') return '';
  if (!d.changed) return t('review.compare.unchanged');
  const percent = (d.changed / d.total) * 100;
  return t('review.compare.changed', { percent: percent < 0.1 ? '<0.1' : percent.toFixed(1) });
}

// Differences already worked out, by pair, kept while the app runs: going
// back to a pair shows its highlight at once.
const differences = new Map<string, Difference>();

// useDifference is the pixels that changed between a pair's screenshots,
// worked out when on.
function useDifference(pair: Pair, on: boolean): Difference {
  const key = `${pair.before.id}|${pair.after.id}`;
  const [d, setD] = useState<Difference>(() => differences.get(key) ?? { status: 'off' });
  useEffect(() => {
    if (!on) return;
    const known = differences.get(key);
    if (known) return setD(known);
    let live = true;
    setD({ status: 'working' });
    void diffPair(pair)
      .catch((err): Difference => ({ status: 'failed', error: errorMessage(err) }))
      .then((result) => {
        if (result.status !== 'failed') differences.set(key, result);
        if (live) setD(result);
      });
    return () => {
      live = false;
    };
    // The pair is the key's.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, on]);
  return on ? d : { status: 'off' };
}

async function diffPair(pair: Pair): Promise<Difference> {
  const load = async (item: T.MediaItem) => createImageBitmap(await (await fetch(mediaUrl(item))).blob());
  const [a, b] = await Promise.all([load(pair.before), load(pair.after)]);
  if (a.width !== b.width || a.height !== b.height) return { status: 'sizes', before: `${a.width}×${a.height}`, after: `${b.width}×${b.height}` };
  const pixels = (bitmap: ImageBitmap) => {
    const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
    const context = canvas.getContext('2d') as OffscreenCanvasRenderingContext2D;
    context.drawImage(bitmap, 0, 0);
    return context.getImageData(0, 0, bitmap.width, bitmap.height).data;
  };
  const diff = pixelDiff(pixels(a), pixels(b), a.width, a.height);
  const canvas = new OffscreenCanvas(a.width, a.height);
  (canvas.getContext('2d') as OffscreenCanvasRenderingContext2D).putImageData(new ImageData(diff.overlay, a.width, a.height), 0, 0);
  const url = URL.createObjectURL(await canvas.convertToBlob({ type: 'image/png' }));
  return { status: 'ready', url, changed: diff.changed, total: diff.total };
}

// ReviewVideos is a pair of recordings: the before, then the after, each a
// player of its own with the browser's controls (seek, speed, full screen),
// neither following the other. Space plays or pauses the one last used, the
// before until then.
export function ReviewVideos({ ref, pair, expanded }: { ref?: Ref<CompareHandle>; pair: Pair; expanded: boolean }) {
  const t = useT();
  const videos = useRef<Record<Side, HTMLVideoElement | null>>({ before: null, after: null });
  const used = useRef<Side>('before');
  useImperativeHandle(ref, () => ({
    cycle: () => {},
    highlight: () => {},
    space: () => {
      const v = videos.current[used.current];
      if (v) void (v.paused ? v.play().catch(() => {}) : v.pause());
    },
  }));
  return (
    <div className={cn('grid min-w-0 gap-3', expanded && 'h-full min-h-0 grid-rows-2')} data-review-videos>
      {(['before', 'after'] as const).map((s) => (
        <figure key={s} className={cn('mx-auto grid w-full min-w-0 gap-1.5', expanded && 'h-full min-h-0 grid-rows-[auto_minmax(0,1fr)]')} style={expanded ? undefined : { maxWidth: 'calc(50vh * 1.6)' }}>
          <figcaption className={cn('text-[12px] font-medium', expanded ? 'text-white/80' : 'text-secondary')}>{t(s === 'before' ? 'review.compare.before' : 'review.compare.after')}</figcaption>
          <video
            ref={(v) => {
              videos.current[s] = v;
            }}
            src={mediaUrl(pair[s])}
            controls
            muted
            playsInline
            preload="metadata"
            onPlay={() => {
              used.current = s;
            }}
            onPointerDown={() => {
              used.current = s;
            }}
            className={cn('block w-full min-w-0 rounded-xl border border-line-strong bg-black object-contain', expanded ? 'h-full min-h-0' : 'aspect-[16/10]')}
            data-review-video={s}
          />
        </figure>
      ))}
    </div>
  );
}

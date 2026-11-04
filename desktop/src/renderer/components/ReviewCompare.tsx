import { Columns2, FlipHorizontal2, Highlighter, SplitSquareHorizontal } from 'lucide-react';
import { useEffect, useImperativeHandle, useRef, useState, type Ref } from 'react';
import type * as T from '../../shared/api';
import type { Pair, Side } from '../../shared/pairs';
import { compareModes, nextMode, pixelDiff, setComparePrefs, useComparePrefs, type CompareMode } from '../lib/compare';
import { useT } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { cn, errorMessage } from '../lib/utils';
import { arrange, box, sizeOf, type Size, type View } from '../lib/zoom';
import { barHeight, Clip, Label, useSize, Viewport, type ClipHandle } from './ReviewZoom';
import { Kbd } from './ui/card';

// The review's stage for a before/after pair of screenshots
// (shared/pairs.ts): the two side by side (or one over the other, for wide
// ones), zoomed and panned together; one over the other with a divider to
// drag; or one at a time, a click or Space flipping between them. Highlight marks the pixels that changed, for two
// of the same size. Q cycles the modes and H highlights (ReviewProof's
// handle, the keys being the review's), and the mode is remembered
// (lib/compare.ts). A pair of recordings isn't compared: two walkthroughs
// rarely show the same screen at the same moment, so ReviewVideos plays
// each with its own controls.

export interface CompareHandle {
  cycle: () => void;
  highlight: () => void;
  // space is the review's Space: Flip flips, the other modes do nothing; a
  // pair of recordings plays or pauses the one last used.
  space: () => void;
}

// A pair's own state, kept by the proof so it survives opening the pair large
// and back: which half Flip shows, and where the slider's divider is.
export interface PairState {
  side: Side;
  split: number;
}

export const pairStart: PairState = { side: 'after', split: 0.5 };

// The zoom a pair is shown at, shared by its halves (lib/zoom.ts), and kept
// by the proof (ReviewProof) for the keys and the viewer's buttons.
export interface Zooming {
  view: View;
  onView: (v: View) => void;
  onPort: (port: Size, pic: Size) => void;
}

const modeIcon: Record<CompareMode, typeof Columns2> = { side: Columns2, slider: SplitSquareHorizontal, flip: FlipHorizontal2 };
const modeLabel = { side: 'review.compare.side', slider: 'review.compare.slider', flip: 'review.compare.flip' } as const;
const gap = 8;

// ReviewCompare is a pair of screenshots. Inline (room's height given), each
// half is a box hugging its picture (lib/zoom.ts box), side by side or one
// over the other, whichever shows them larger; filling the viewer (fill),
// the halves take all of its room.
export function ReviewCompare({
  ref,
  pair,
  dark,
  height,
  zooming,
  state,
  onState,
}: {
  ref?: Ref<CompareHandle>;
  pair: Pair;
  dark: boolean;
  // height is the most the pair takes down, inline; without it, it fills its parent.
  height?: number;
  zooming: Zooming;
  state: PairState;
  onState: (s: PairState) => void;
}) {
  const t = useT();
  const { mode, highlight } = useComparePrefs();
  const difference = useDifference(pair, highlight);
  const flip = () => onState({ ...state, side: state.side === 'after' ? 'before' : 'after' });
  useImperativeHandle(ref, () => ({
    cycle: () => setComparePrefs({ mode: nextMode(mode) }),
    highlight: () => setComparePrefs({ highlight: !highlight }),
    space: () => mode === 'flip' && flip(),
  }));

  const space = useRef<HTMLDivElement>(null);
  const measured = useSize(space);
  const room: Size = { w: measured.w, h: height ?? measured.h };
  const afterSize = usePicSize(pair.after);
  const beforeSize = usePicSize(pair.before);
  const pic = afterSize ?? beforeSize ?? fallbackPic;
  const overlay = highlight && difference.status === 'ready' ? difference.url : undefined;
  const label = (s: Side) => t(s === 'before' ? 'review.compare.before' : 'review.compare.after');
  const hint = t(mode === 'side' ? 'review.compare.sideHint' : mode === 'slider' ? 'review.compare.sliderHint' : 'review.compare.flipHint', { mod: modKey() });
  const { arrangement, half } = arrange(room, pic, gap);
  const single = height === undefined ? room : box(room, pic);
  const halfBox = height === undefined ? (arrangement === 'columns' ? { w: (room.w - gap) / 2, h: room.h } : { w: room.w, h: (room.h - gap) / 2 }) : half;
  const onPort = (port: Size) => zooming.onPort(port, pic);
  const frame = cn('rounded-lg border bg-black', dark ? 'border-white/10' : 'border-line-strong');

  return (
    <div className={cn('grid w-full min-w-0 gap-2', height === undefined && 'h-full min-h-0 grid-rows-[auto_minmax(0,1fr)]')} data-review-compare={mode} data-review-compare-side={mode === 'flip' ? state.side : undefined} data-review-compare-arrangement={mode === 'side' ? arrangement : undefined}>
      <div className={cn('flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 text-caption', dark ? 'text-white/80' : 'text-secondary')}>
        {/* The mode in use is underlined, as the review marks what's picked. */}
        <div role="radiogroup" aria-label={t('review.compare.modes')} className="flex items-center gap-4">
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
                className={cn(
                  'relative inline-flex h-7 items-center gap-1.5 font-medium transition-colors after:absolute after:inset-x-0 after:bottom-0 after:h-px',
                  mode === m ? (dark ? 'text-white after:bg-brand-400' : 'text-title after:bg-brand-400') : dark ? 'text-white/50 hover:text-white' : 'text-subtle hover:text-primary',
                )}
                data-review-compare-mode={m}
              >
                <Icon className="size-3.5" />
                {t(modeLabel[m])}
              </button>
            );
          })}
        </div>
        <span className={cn('flex items-center gap-1 max-md:hidden', dark ? 'text-white/50' : 'text-subtle')} aria-hidden title={t('review.key.compareMode')}>
          <Kbd>Q</Kbd>
          {t('review.compare.cycle')}
        </span>
        <button
          type="button"
          aria-pressed={highlight}
          onClick={() => setComparePrefs({ highlight: !highlight })}
          title={t('review.compare.highlightTitle')}
          className={cn(
            'relative inline-flex h-7 items-center gap-1.5 font-medium transition-colors after:absolute after:inset-x-0 after:bottom-0 after:h-px',
            highlight ? (dark ? 'text-white after:bg-brand-400' : 'text-title after:bg-brand-400') : dark ? 'text-white/50 hover:text-white' : 'text-subtle hover:text-primary',
          )}
          data-review-highlight
        >
          {/* On, its icon is the colour the changed pixels are painted in
              (lib/compare.ts pixelDiff): the key to them. */}
          <Highlighter className={cn('size-3.5', highlight && 'text-[rgb(255_0_170)]')} />
          {t('review.compare.highlight')}
          <span className="max-md:hidden" aria-hidden>
            <Kbd>H</Kbd>
          </span>
        </button>
        {highlight && (
          <span className={cn('min-w-0 truncate', dark ? 'text-white/50' : 'text-subtle')} data-review-highlight-note={difference.status}>
            {differenceNote(t, difference)}
          </span>
        )}
        {/* The viewer (dark) says how to zoom under the pair. */}
        {!dark && <span className="ml-auto min-w-0 truncate text-subtle @max-5xl:hidden">{hint}</span>}
      </div>

      <div ref={space} className={cn('flex min-w-0 items-center justify-center', height === undefined && 'min-h-0', mode === 'side' && arrangement === 'rows' && 'flex-col')} style={{ gap, height: height === undefined ? undefined : mode === 'side' ? (arrangement === 'columns' ? halfBox.h : halfBox.h * 2 + gap) : single.h }}>
        {room.w > 0 &&
          (mode === 'side' ? (
            (['before', 'after'] as const).map((s) => (
              <Viewport key={s} pic={pic} view={zooming.view} onView={zooming.onView} onPort={onPort} className={cn(frame, 'shrink-0')} style={{ width: halfBox.w, height: halfBox.h }} over={(r) => <Label rect={r}>{label(s)}</Label>} data-review-frame={s}>
                <Picture item={pair[s]} overlay={overlay} />
              </Viewport>
            ))
          ) : mode === 'slider' ? (
            <Viewport
              pic={pic}
              view={zooming.view}
              onView={zooming.onView}
              onPort={onPort}
              onDrag={(x) => onState({ ...state, split: x })}
              className={cn(frame, 'shrink-0')}
              style={{ width: single.w, height: single.h }}
              over={(r) => (
                <>
                  <Divider x={r.x + r.w * state.split} />
                  <Label rect={r}>{label('before')}</Label>
                  <span className="pointer-events-none absolute" style={{ right: 8, top: Math.max(8, r.y + 8) }}>
                    <span className="rounded-md bg-black/75 px-1.5 py-0.5 text-caption font-medium text-white">{label('after')}</span>
                  </span>
                </>
              )}
              data-review-frame="slider"
            >
              <Picture item={pair.before} overlay={overlay} />
              <div className="absolute inset-0" style={{ clipPath: `inset(0 0 0 ${state.split * 100}%)` }}>
                <Picture item={pair.after} overlay={overlay} />
              </div>
            </Viewport>
          ) : (
            <Viewport pic={pic} view={zooming.view} onView={zooming.onView} onPort={onPort} onTap={flip} className={cn(frame, 'shrink-0 cursor-pointer')} style={{ width: single.w, height: single.h }} over={(r) => <Label rect={r}>{label(state.side)}</Label>} data-review-frame="flip">
              {(['before', 'after'] as const).map((s) => (
                <div key={s} className={cn('absolute inset-0', s !== state.side && 'invisible')}>
                  <Picture item={pair[s]} overlay={overlay} />
                </div>
              ))}
            </Viewport>
          ))}
      </div>
    </div>
  );
}

// modKey is the key held to zoom with the wheel, as this computer names it.
export const modKey = () => (navigator.platform.toLowerCase().includes('mac') ? '⌘' : 'Ctrl');

// usePicSize is a screenshot's or a recording's size: as recorded, or as its
// file says once it has loaded, for one recorded without.
export function usePicSize(item: T.MediaItem): Size | undefined {
  const [loaded, setLoaded] = useState<{ url: string; size: Size }>();
  const url = mediaUrl(item);
  const { kind } = item;
  // The file's own size wins once it has loaded (a recording's videoWidth and
  // videoHeight are as it is shown, turned or not); what was recorded with it
  // holds the room until then.
  useEffect(() => {
    let live = true;
    if (kind === 'recording') {
      const v = document.createElement('video');
      v.preload = 'metadata';
      v.onloadedmetadata = () => live && v.videoWidth && setLoaded({ url, size: { w: v.videoWidth, h: v.videoHeight } });
      v.src = url;
    } else {
      const img = new Image();
      img.onload = () => live && img.naturalWidth && setLoaded({ url, size: { w: img.naturalWidth, h: img.naturalHeight } });
      img.src = url;
    }
    return () => {
      live = false;
    };
  }, [url, kind]);
  return (loaded?.url === url ? loaded.size : undefined) ?? sizeOf(item.meta);
}

// fallbackPic is the shape given to proof whose size isn't known yet.
export const fallbackPic: Size = { w: 1280, h: 800 };

// Picture is one half of a pair at its own size, the scale being its
// viewport's, stretched to the other's if they differ; the highlight over it.
function Picture({ item, overlay }: { item: T.MediaItem; overlay?: string }) {
  return (
    <div className="absolute inset-0" data-review-picture={item.id}>
      <img src={mediaUrl(item)} alt={item.name} draggable={false} className="size-full object-contain" />
      {overlay && <img src={overlay} alt="" draggable={false} className="pointer-events-none absolute inset-0 size-full object-contain [image-rendering:pixelated]" data-review-overlay />}
    </div>
  );
}

// Divider is the slider's line, the before left of it and the after right,
// drawn unscaled over the viewport at x.
function Divider({ x }: { x: number }) {
  return (
    <div className="pointer-events-none absolute inset-y-0 w-0.5 -translate-x-1/2 bg-white shadow-[0_0_0_1px_rgba(0,0,0,0.4)]" style={{ left: x }} data-review-divider>
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

// ReviewVideos is a pair of recordings: the before and the after, each a
// player of its own (Clip), neither following the other in time but zoomed
// and panned together, side by side or one over the other as a pair of
// screenshots is. Space plays or pauses the one last used, the before until
// then.
export function ReviewVideos({ ref, pair, dark, height, zooming }: { ref?: Ref<CompareHandle>; pair: Pair; dark: boolean; height?: number; zooming: Zooming }) {
  const t = useT();
  const clips = useRef<Record<Side, ClipHandle | null>>({ before: null, after: null });
  const used = useRef<Side>('before');
  useImperativeHandle(ref, () => ({
    cycle: () => {},
    highlight: () => {},
    space: () => clips.current[used.current]?.toggle(),
  }));
  const space = useRef<HTMLDivElement>(null);
  const measured = useSize(space);
  const afterSize = usePicSize(pair.after);
  const beforeSize = usePicSize(pair.before);
  const pic = afterSize ?? beforeSize ?? fallbackPic;
  // Each half's bar is under its picture, so the pictures share what's left.
  const bars = (n: number) => n * barHeight;
  const roomFor = (rows: number): Size => ({ w: measured.w, h: (height ?? measured.h) - bars(rows) });
  const { arrangement } = arrange({ w: measured.w, h: (height ?? measured.h) - barHeight }, pic, gap);
  const room = roomFor(arrangement === 'rows' ? 2 : 1);
  const half = height === undefined ? (arrangement === 'columns' ? { w: (room.w - gap) / 2, h: room.h } : { w: room.w, h: (room.h - gap) / 2 }) : arrange(room, pic, gap).half;
  return (
    <div
      ref={space}
      className={cn('flex w-full min-w-0 items-center justify-center', height === undefined && 'h-full min-h-0', arrangement === 'rows' && 'flex-col')}
      style={{ gap, height: height === undefined ? undefined : arrangement === 'columns' ? half.h + barHeight : (half.h + barHeight) * 2 + gap }}
      data-review-videos={arrangement}
    >
      {measured.w > 0 &&
        (['before', 'after'] as const).map((s) => (
          <Clip
            key={s}
            ref={(c) => {
              clips.current[s] = c;
            }}
            item={pair[s]}
            pic={pic}
            size={{ w: Math.floor(half.w), h: Math.floor(half.h) }}
            view={zooming.view}
            onView={zooming.onView}
            onPort={(port) => zooming.onPort(port, pic)}
            dark={dark}
            onUse={() => {
              used.current = s;
            }}
            over={(r) => <Label rect={r}>{t(s === 'before' ? 'review.compare.before' : 'review.compare.after')}</Label>}
          />
        ))}
    </div>
  );
}

import { Check, ChevronLeft, ChevronRight, Columns2, ExternalLink, Film, Image as ImageIcon, Maximize2, Play, X } from 'lucide-react';
import { useEffect, useImperativeHandle, useLayoutEffect, useRef, useState, type CSSProperties, type Ref, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { chapterAt, gallery, readableName, shownKey, step, type Proof, type Shown, type Take } from '../lib/review';
import { cn } from '../lib/utils';
import { box, percent, zoomAt, zoomStep, type Size, type View } from '../lib/zoom';
import { PageIcon } from './pages/PageThumb';
import { modKey, pairStart, ReviewCompare, ReviewVideos, usePicSize, fallbackPic, type CompareHandle, type PairState, type Zooming } from './ReviewCompare';
import { barHeight, Clip, clock, useSize, Viewport, ZoomControls, type ClipHandle } from './ReviewZoom';
import { Button } from './ui/button';
import { Kbd } from './ui/card';

export { clock };

// The review's proof, what it opens on: everything the agent showed it
// checked, in one gallery (lib/review.ts gallery). The stage shows one piece
// of it, a recording, a screenshot or a page, in a box hugging it at the
// largest size the review has room for (lib/zoom.ts box), and the strip under
// it lists all of them in cards of one size, the one on stage lit; a
// recording's chapters (what it checked at each moment) are beside it, to
// jump to. A before/after pair is one piece: two screenshots compared on
// stage (ReviewCompare), two recordings played each on its own
// (ReviewVideos). ← → step through the gallery whatever the kind, Space plays
// and pauses (or flips a pair in Flip), [ ] move through the chapters, Q and
// H work a pair's comparison, F (or a click on a screenshot) opens the piece
// on stage large, over the whole review, and Esc closes it; + − zoom it and
// 0 fits it again, as Ctrl or ⌘ and the wheel or a pinch do (ReviewZoom.tsx),
// the halves of a pair together (ProofHandle, the keys being the review's:
// ReviewCarousel).

// ProofHandle is what the review's keys do to the proof.
export interface ProofHandle {
  step: (by: number) => void;
  togglePlay: () => void;
  chapter: (by: number) => void;
  toggleExpand: () => void;
  // collapse leaves the viewer, and says whether it was in it, so Esc does
  // that before it closes the review.
  collapse: () => boolean;
  compareMode: () => void;
  highlight: () => void;
  // zoom zooms the piece on show in (1) or out (-1) around its middle, or
  // fits it again (0).
  zoom: (by: 1 | -1 | 0) => void;
}

// What the stage leaves the rest of the proof, down the review's body: its
// padding, the bar under the stage, and the strip when there is one; a pair
// its comparison's modes, a recording its bar.
const reserve = { padding: 40, bar: 40, strip: 148, compare: 44 };

export function ReviewProof({ ref, project, proof }: { ref?: Ref<ProofHandle>; project: string; proof: Proof }) {
  const t = useT();
  const shown = gallery(proof);
  const [at, setAt] = useState(0);
  const [time, setTime] = useState(0);
  const [expanded, setExpanded] = useState(false);
  const [view, setView] = useState<View>('fit');
  const [pair, setPair] = useState<PairState>(pairStart);
  // frame is the viewport on show and the picture in it, for + − and the
  // viewer's buttons to zoom around its middle.
  const [frame, setFrame] = useState<{ port: Size; pic: Size }>();
  const player = useRef<ClipHandle>(null);
  const compare = useRef<CompareHandle>(null);
  const root = useRef<HTMLDivElement>(null);
  const column = useRef<HTMLDivElement>(null);
  const width = useSize(column).w;
  const bodyHeight = useParentHeight(root);
  const current = shown[Math.min(at, shown.length - 1)];
  const rec = current?.kind === 'recording' ? current.take : undefined;
  const chapter = rec ? chapterAt(rec.chapters, time) : -1;
  // The screenshots taken during the chapter playing are lit in the strip.
  const lit = new Set(rec?.chapters[chapter]?.shots.map((s) => s.id));
  const pick = (n: number) => {
    setAt(n);
    setTime(0);
    setView('fit');
    setPair(pairStart);
    setFrame(undefined);
    // The strip follows the stage, for the keys.
    root.current?.querySelector(`[data-review-media="${n + 1}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  };
  const expand = (on: boolean) => {
    setExpanded(on);
    setFrame(undefined);
  };
  const zoom = (by: 1 | -1 | 0) => {
    if (by === 0) return setView('fit');
    if (!frame) return;
    setView(zoomAt(view, by > 0 ? zoomStep : 1 / zoomStep, frame.port.w / 2, frame.port.h / 2, frame.port, frame.pic));
  };
  useImperativeHandle(ref, () => ({
    step: (by) => shown.length && pick(step(at, shown.length, by)),
    togglePlay: () => (current?.kind === 'pair' ? compare.current?.space() : player.current?.toggle()),
    chapter: (by) => {
      if (!rec?.chapters.length) return;
      const to = rec.chapters[step(Math.max(chapter, 0), rec.chapters.length, by)];
      if (to) player.current?.seek(to.at);
    },
    toggleExpand: () => current && expand(!expanded),
    collapse: () => {
      if (!expanded) return false;
      expand(false);
      return true;
    },
    compareMode: () => compare.current?.cycle(),
    highlight: () => compare.current?.highlight(),
    zoom,
  }));

  if (!current) {
    return (
      <div className="p-4 sm:p-5" data-review-proof>
        <div className="grid justify-items-center gap-2 rounded-xl border border-line bg-surface-faint px-6 py-14 text-center">
          <ImageIcon className="size-8 text-faint" />
          <div className="text-sm font-medium text-secondary">{t('review.noProof')}</div>
          <div className="max-w-sm text-xs text-subtle">{t('review.noProofBody')}</div>
        </div>
      </div>
    );
  }

  // The stage takes what the body has left, between a floor that keeps a
  // picture worth looking at (the body scrolls under it) and a ceiling past
  // which a big window only adds empty space.
  const height = Math.max(220, Math.min(900, bodyHeight - reserve.padding - reserve.bar - (shown.length > 1 ? reserve.strip : 0) - (current.kind === 'pair' && current.pair.before.kind !== 'recording' ? reserve.compare : 0)));
  const zooming: Zooming = {
    view,
    onView: setView,
    onPort: (port, pic) => setFrame((f) => (f && f.port.w === port.w && f.port.h === port.h && f.pic.w === pic.w && f.pic.h === pic.h ? f : { port, pic })),
  };
  const stage = (fill: boolean) => (
    <Stage
      key={shownKey(current)}
      shown={current}
      project={project}
      room={fill ? undefined : { w: width, h: height }}
      zooming={zooming}
      pair={pair}
      onPair={setPair}
      player={player}
      compare={compare}
      chapter={chapter}
      time={time}
      onTime={setTime}
      onOpen={() => expand(true)}
    />
  );
  const nav = { shown: current, n: at, count: shown.length, onStep: (by: number) => pick(step(at, shown.length, by)) };
  const viewer = expanded && root.current ? (root.current.closest('[role="dialog"]') ?? root.current) : null;

  return (
    <div ref={root} className="@container relative grid min-h-full min-w-0 max-w-full grid-cols-[minmax(0,1fr)] content-start gap-4 overflow-x-clip p-4 sm:p-5" data-review-proof data-review-media-at={at + 1}>
      {/* A recording's chapters go beside it when the proof itself has the
          room, which the case beside the proof (ReviewCarousel) takes from
          the window, and under it otherwise. */}
      <div className={cn('grid min-w-0 max-w-full gap-5', rec && '@4xl:grid-cols-[minmax(0,1fr)_260px]')}>
        <div ref={column} className="grid min-w-0 content-start justify-items-center gap-2">
          {/* While the viewer is open the stage is in it. */}
          {expanded ? <div style={{ height }} /> : width > 0 && stage(false)}
          <StageBar {...nav} view={view} frame={frame} onFit={() => setView('fit')} onExpand={() => expand(true)} />
        </div>
        {rec && <Chapters take={rec} at={chapter} onPick={(s) => player.current?.seek(s)} />}
      </div>
      <Strip shown={shown} at={at} lit={lit} onPick={pick} />
      {viewer &&
        createPortal(
          // The viewer is over the whole review, inside its dialog so its
          // keys and focus stay the review's.
          <div className="absolute inset-0 z-30 flex flex-col bg-[#09090b] text-white" data-review-expanded>
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-2 px-4 py-3">
              <Position {...nav} dark />
              <span className="min-w-0 flex-1" />
              {current.kind !== 'page' && <ZoomControls view={view} port={frame?.port} pic={frame?.pic} onView={setView} />}
              <Button size="sm" variant="ghost" className="h-7 text-white/80 hover:bg-white/10 hover:text-white" onClick={() => expand(false)} data-review-collapse>
                <X />
                {t('review.collapse')}
                <span className="max-md:hidden" aria-hidden>
                  <Kbd>Esc</Kbd>
                </span>
              </Button>
            </div>
            <div className="flex min-h-0 flex-1 flex-col px-4">{stage(true)}</div>
            <div className="truncate px-4 py-2 text-center text-caption text-white/40 max-md:hidden">{current.kind === 'page' ? '' : t('review.zoom.hint', { mod: modKey() })}</div>
          </div>,
          viewer,
        )}
    </div>
  );
}

// useParentHeight is the height of what el scrolls in: the review's body.
function useParentHeight(el: RefObject<HTMLElement | null>): number {
  const [h, setH] = useState(0);
  useLayoutEffect(() => {
    const parent = el.current?.parentElement;
    if (!parent) return;
    const measure = () => setH(parent.clientHeight);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(parent);
    return () => observer.disconnect();
  }, [el]);
  return h;
}

// nameOf is what a piece of proof is called.
const nameOf = (s: Shown) =>
  s.kind === 'pair' ? readableName(s.pair.name) : s.kind === 'recording' ? readableName(s.take.media.name) : s.kind === 'screenshot' ? readableName(s.media.name) : s.page.title;

// Position is where the piece is in the gallery and its name.
function Position({ shown, n, count, onStep, dark }: { shown: Shown; n: number; count: number; onStep: (by: number) => void; dark?: boolean }) {
  const t = useT();
  const ghost = dark ? 'text-white/80 hover:bg-white/10 hover:text-white' : undefined;
  return (
    <div className={cn('flex min-w-0 flex-1 items-center gap-2 text-ui', dark ? 'text-white/85' : 'text-secondary')}>
      <Button size="icon-sm" variant="ghost" className={ghost} disabled={n === 0} onClick={() => onStep(-1)} aria-label={t('review.previous')} title={`${t('review.previous')} (←)`}>
        <ChevronLeft />
      </Button>
      <span className={cn('shrink-0 font-mono text-caption tabular-nums', dark ? 'text-white/50' : 'text-subtle')}>{t('review.position', { n: n + 1, count })}</span>
      <Button size="icon-sm" variant="ghost" className={ghost} disabled={n === count - 1} onClick={() => onStep(1)} aria-label={t('review.next')} title={`${t('review.next')} (→)`}>
        <ChevronRight />
      </Button>
      <KindIcon shown={shown} className={cn('size-3.5 shrink-0', dark ? 'text-white/50' : 'text-subtle')} />
      <span className="min-w-0 flex-1 truncate">{nameOf(shown)}</span>
    </div>
  );
}

// StageBar is under the stage: where the piece is in the gallery, its name,
// how far it's zoomed (a click fits it again), and Expand, which opens it
// large.
function StageBar({ view, frame, onFit, onExpand, ...nav }: { shown: Shown; n: number; count: number; onStep: (by: number) => void; view: View; frame?: { port: Size; pic: Size }; onFit: () => void; onExpand: () => void }) {
  const t = useT();
  const { shown } = nav;
  return (
    <div className="flex w-full min-w-0 items-center gap-2 text-ui text-secondary" data-review-stage-bar>
      <Position {...nav} />
      {view !== 'fit' && frame && (
        <button type="button" onClick={onFit} className="shrink-0 rounded-md px-1.5 font-mono text-caption tabular-nums text-subtle hover:text-primary" title={t('review.zoom.fitTitle')} data-review-zoom={percent(view, frame.port, frame.pic)}>
          {percent(view, frame.port, frame.pic)}% · {t('review.zoom.fit')}
        </button>
      )}
      {shown.kind === 'page' && shown.page.url && (
        <Button size="sm" variant="ghost" className="h-7" onClick={() => void window.agentbox.openExternal(shown.page.url)}>
          <ExternalLink />
          {t('review.openPage')}
        </Button>
      )}
      <Button size="sm" variant="ghost" className="h-7" onClick={onExpand} title={t('review.key.expand')} data-review-expand>
        <Maximize2 />
        {t('review.expand')}
        <span className="max-md:hidden" aria-hidden>
          <Kbd>F</Kbd>
        </span>
      </Button>
    </div>
  );
}

function KindIcon({ shown, className }: { shown: Shown; className?: string }) {
  if (shown.kind === 'pair') return <Columns2 className={className} />;
  if (shown.kind === 'recording') return <Film className={className} />;
  if (shown.kind === 'screenshot') return <ImageIcon className={className} />;
  return <PageIcon className={className} />;
}

// Stage is the piece of proof on show: given room, in a box hugging it inside
// that room; without, filling the viewer.
function Stage({
  shown,
  project,
  room,
  zooming,
  pair,
  onPair,
  player,
  compare,
  chapter,
  time,
  onTime,
  onOpen,
}: {
  shown: Shown;
  project: string;
  room?: Size;
  zooming: Zooming;
  pair: PairState;
  onPair: (s: PairState) => void;
  player: Ref<ClipHandle>;
  compare: Ref<CompareHandle>;
  chapter: number;
  time: number;
  onTime: (seconds: number) => void;
  onOpen: () => void;
}) {
  const dark = !room;
  if (shown.kind === 'pair') {
    if (shown.pair.before.kind === 'recording') return <ReviewVideos ref={compare} pair={shown.pair} dark={dark} height={room?.h} zooming={zooming} />;
    return <ReviewCompare ref={compare} pair={shown.pair} dark={dark} height={room?.h} zooming={zooming} state={pair} onState={onPair} />;
  }
  if (shown.kind === 'recording') return <Recording ref={player} take={shown.take} room={room} zooming={zooming} chapter={chapter} time={time} onTime={onTime} />;
  if (shown.kind === 'screenshot') return <Screenshot media={shown.media} room={room} zooming={zooming} onOpen={onOpen} />;
  const page = room ? pageBox(room) : undefined;
  return <PageFrame project={project} page={shown.page} className={cn('overflow-hidden rounded-xl border', room ? 'border-line-strong' : 'size-full min-h-0 border-white/10')} style={page} />;
}

// A page has no size of its own: it gets a 16:10 window's.
const pageBox = (room: Size): CSSProperties => {
  const w = Math.min(room.w, room.h * 1.6);
  return { width: w, height: w / 1.6 };
};

// Screenshot is one picture; a click on it, inline, opens it large.
function Screenshot({ media, room, zooming, onOpen }: { media: T.MediaItem; room?: Size; zooming: Zooming; onOpen: () => void }) {
  const pic = usePicSize(media) ?? fallbackPic;
  const size = room && box(room, pic);
  return (
    <Viewport
      pic={pic}
      view={zooming.view}
      onView={zooming.onView}
      onPort={(port) => zooming.onPort(port, pic)}
      onTap={room ? onOpen : undefined}
      className={cn('shrink-0 rounded-xl', room ? 'border border-line-strong bg-black' : 'size-full min-h-0')}
      style={size && { width: size.w, height: size.h }}
      data-review-stage="screenshot"
      data-review-stage-size={`${pic.w}x${pic.h}`}
    >
      <img src={mediaUrl(media)} alt={media.name} draggable={false} className="size-full object-contain" />
    </Viewport>
  );
}

// Recording is a recording, the chapter playing named over the picture, its
// bar cut into its chapters. It goes on from where it was when it moves
// between the stage and the viewer.
function Recording({ ref, take, room, zooming, chapter, time, onTime }: { ref: Ref<ClipHandle>; take: Take; room?: Size; zooming: Zooming; chapter: number; time: number; onTime: (seconds: number) => void }) {
  const t = useT();
  const pic = usePicSize(take.media) ?? fallbackPic;
  const size = room && box({ w: room.w, h: room.h - barHeight }, pic);
  const current = take.chapters[chapter];
  // Where it starts is where it was when this stage opened, not as it plays.
  const [startAt] = useState(time);
  return (
    <Clip
      ref={ref}
      item={take.media}
      pic={pic}
      size={size}
      view={zooming.view}
      onView={zooming.onView}
      onPort={(port) => zooming.onPort(port, pic)}
      dark={!room}
      marks={take.chapters}
      chapter={chapter}
      startAt={startAt}
      autoPlay
      onTime={onTime}
      over={() =>
        current && (
          <span className="pointer-events-none absolute left-3 top-3 max-w-[80%] truncate rounded-md bg-black/70 px-2 py-1 text-caption text-white">
            <span className="tabular-nums text-white/60">{t('review.position', { n: chapter + 1, count: take.chapters.length })}</span> {current.title}
          </span>
        )
      }
    />
  );
}

// Strip is every piece of proof, in cards of one height whatever the size of
// what's in them, the one on stage underlined in the brand's colour (the
// review's mark for what's picked, PaneTab), those taken during the chapter
// playing outlined; a click puts one on stage.
function Strip({ shown, at, lit, onPick }: { shown: Shown[]; at: number; lit: ReadonlySet<string>; onPick: (n: number) => void }) {
  const t = useT();
  const count = (kind: Shown['kind']) => shown.filter((s) => s.kind === kind).length;
  const counts = [
    count('pair') && t('review.count.pairs', { count: count('pair') }),
    count('recording') && t('review.count.recordings', { count: count('recording') }),
    count('screenshot') && t('review.count.screenshots', { count: count('screenshot') }),
    count('page') && t('review.count.pages', { count: count('page') }),
  ].filter(Boolean);
  if (shown.length < 2) return null;
  return (
    <section className="min-w-0">
      <h3 className="mb-2 flex items-center gap-2 text-caption font-medium text-muted">
        {t('review.media')}
        <span className="font-normal text-subtle">{counts.join(' · ')}</span>
      </h3>
      <div className="flex max-w-full items-start gap-2 overflow-x-auto overscroll-x-contain p-0.5 pb-1 [scrollbar-width:thin]" data-review-shots>
        {shown.map((s, i) => (
          <button
            key={shownKey(s)}
            type="button"
            title={nameOf(s)}
            aria-current={i === at || undefined}
            onClick={() => onPick(i)}
            className={cn(
              s.kind === 'pair' ? 'w-60' : 'w-36',
              'relative shrink-0 overflow-hidden rounded-lg border bg-surface text-left transition-colors hover:border-line-heavy',
              'after:absolute after:inset-x-2 after:bottom-0 after:h-px',
              i === at ? 'border-line-heavy after:bg-brand-400' : s.kind === 'screenshot' && lit.has(s.media.id) ? 'border-line-heavy' : 'border-line',
            )}
            data-review-media={i + 1}
            data-review-media-kind={s.kind}
          >
            <Thumb shown={s} />
            <span className={cn('flex h-7 min-w-0 items-center gap-1.5 px-2 text-caption', i === at ? 'text-title' : 'text-secondary')}>
              <KindIcon shown={s} className="size-3 shrink-0 text-subtle" />
              <span className="min-w-0 truncate">{nameOf(s)}</span>
            </span>
          </button>
        ))}
      </div>
    </section>
  );
}

// thumbHeight is every card's picture, whatever the shape of what's in it:
// cropped from its top-left corner, where an app's title and navigation are.
const thumb = 'relative block h-[5.5rem] overflow-hidden bg-black';

function Thumb({ shown }: { shown: Shown }) {
  const t = useT();
  if (shown.kind === 'pair') {
    // Its halves, before then after, each named.
    return (
      <span className={cn(thumb, 'grid grid-cols-2 gap-px bg-line-strong')} data-review-thumb="pair">
        {(['before', 'after'] as const).map((side) => (
          <span key={side} className="relative block min-w-0 overflow-hidden bg-black">
            <Still item={shown.pair[side]} />
            <span className="absolute bottom-1 left-1 rounded bg-black/75 px-1 text-[10px] font-medium leading-4 text-white">{t(side === 'before' ? 'review.compare.before' : 'review.compare.after')}</span>
          </span>
        ))}
      </span>
    );
  }
  if (shown.kind === 'screenshot') {
    return (
      <span className={thumb}>
        <Still item={shown.media} />
      </span>
    );
  }
  if (shown.kind === 'recording') {
    return (
      <span className={thumb}>
        <Still item={shown.take.media} />
        {shown.take.media.meta.duration ? <span className="absolute bottom-1 right-1.5 rounded bg-black/75 px-1 text-caption tabular-nums text-white">{clock(shown.take.media.meta.duration)}</span> : null}
      </span>
    );
  }
  return (
    <span className={cn(thumb, 'flex items-center justify-center bg-surface-faint')}>
      <PageIcon className="size-6 text-subtle" />
    </span>
  );
}

// Still is a screenshot, or a recording's first frame under a play mark,
// covering its card.
function Still({ item }: { item: T.MediaItem }) {
  const cover = 'absolute inset-0 size-full bg-black object-contain object-center';
  if (item.kind !== 'recording') return <img src={mediaUrl(item)} alt="" draggable={false} loading="lazy" className={cover} />;
  return (
    <>
      <video src={`${mediaUrl(item)}#t=0.1`} muted preload="metadata" tabIndex={-1} className={cn(cover, 'pointer-events-none opacity-70')} />
      <span className="absolute inset-0 flex items-center justify-center text-white/90">
        <Play className="size-5 fill-current drop-shadow" />
      </span>
    </>
  );
}

// Chapters is what the agent checked in the recording playing, the chapter
// playing lit; a click plays from it.
function Chapters({ take, at, onPick }: { take: Take; at: number; onPick: (seconds: number) => void }) {
  const t = useT();
  return (
    <section className="min-w-0" data-review-chapters>
      <h3 className="mb-2 flex items-center gap-2 text-caption font-medium text-muted">
        {t('review.checked')}
        {take.chapters.length > 1 && (
          <span className="ml-auto flex gap-1 font-normal max-md:hidden" aria-hidden>
            <Kbd>[</Kbd>
            <Kbd>]</Kbd>
          </span>
        )}
      </h3>
      {take.chapters.length === 0 ? (
        <p className="text-caption leading-relaxed text-subtle">{t('review.noChapters')}</p>
      ) : (
        <ol className="grid grid-cols-[minmax(0,1fr)] gap-0.5">
          {take.chapters.map((c, i) => (
            <li key={`${c.at}-${i}`}>
              <button
                type="button"
                onClick={() => onPick(c.at)}
                aria-current={i === at || undefined}
                className={cn(
                  'relative flex w-full gap-2.5 rounded-r-lg px-2.5 py-2 text-left transition-colors before:absolute before:inset-y-1.5 before:left-0 before:w-px',
                  i === at ? 'before:bg-brand-400' : 'hover:bg-surface-raised',
                )}
                data-review-chapter={i + 1}
              >
                <Check className={cn('mt-0.5 size-3.5 shrink-0', i === at ? 'text-emerald-400' : 'text-faint')} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-baseline gap-2">
                    <span className={cn('min-w-0 flex-1 [overflow-wrap:anywhere] text-ui', i === at ? 'text-title' : 'text-primary')}>{c.title}</span>
                    <span className="shrink-0 font-mono text-caption tabular-nums text-subtle">{clock(c.at)}</span>
                  </span>
                  {c.detail && <span className="mt-0.5 line-clamp-3 block [overflow-wrap:anywhere] text-caption leading-snug text-subtle">{c.detail}</span>}
                </span>
              </button>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

// PageFrame is a Hatch page, framed as the page preview frames it: an opaque
// origin, no reach into the app.
function PageFrame({ project, page, className, style }: { project: string; page: T.Artifact; className?: string; style?: CSSProperties }) {
  const [loaded, setLoaded] = useState(false);
  useEffect(() => setLoaded(false), [page.id]);
  return (
    <div className={cn('relative bg-white', className)} style={style} data-review-stage="page" data-review-page={page.id}>
      {!loaded && <div className="skeleton absolute inset-0 rounded-none" />}
      <iframe
        title={page.title}
        src={api.pageUrl(project, page.id)}
        sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox allow-modals"
        referrerPolicy="no-referrer"
        onLoad={() => setLoaded(true)}
        className={cn('absolute inset-0 size-full border-0 bg-white', loaded ? 'opacity-100' : 'opacity-0')}
      />
    </div>
  );
}

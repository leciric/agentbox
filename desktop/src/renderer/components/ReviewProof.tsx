import { Check, ChevronLeft, ChevronRight, CircleAlert, ExternalLink, Film, Image as ImageIcon, Maximize2, Minimize2, Pause, Play } from 'lucide-react';
import { useEffect, useImperativeHandle, useRef, useState, type CSSProperties, type Ref } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { chapterAt, gallery, readableName, step, type Proof, type Shown, type Take } from '../lib/review';
import { cn } from '../lib/utils';
import { PageIcon } from './pages/PageThumb';
import { Button } from './ui/button';
import { Kbd } from './ui/card';

// The review's proof, what it opens on: everything the agent showed it
// checked, in one gallery (lib/review.ts gallery). The stage shows one piece
// of it, a recording, a screenshot or a page, and the strip under it lists
// all of them, the one on stage lit; a recording's chapters (what it checked
// at each moment) are beside it, to jump to. ← → step through the gallery
// whatever the kind, Space plays and pauses, [ ] move through the chapters
// and F fills the review with the piece on stage (ProofHandle, the keys
// being the review's: ReviewCarousel).

// clock is seconds as m:ss.
export const clock = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, '0')}`;

// ProofHandle is what the review's keys do to the proof.
export interface ProofHandle {
  step: (by: number) => void;
  togglePlay: () => void;
  chapter: (by: number) => void;
  toggleExpand: () => void;
  // collapse leaves the expanded view, and says whether it was in it, so
  // Esc does that before it closes the review.
  collapse: () => boolean;
}

export function ReviewProof({ ref, project, proof }: { ref?: Ref<ProofHandle>; project: string; proof: Proof }) {
  const t = useT();
  const shown = gallery(proof);
  const [at, setAt] = useState(0);
  const [time, setTime] = useState(0);
  const [expanded, setExpanded] = useState(false);
  const player = useRef<PlayerHandle>(null);
  const root = useRef<HTMLDivElement>(null);
  const current = shown[Math.min(at, shown.length - 1)];
  const rec = current?.kind === 'recording' ? current.take : undefined;
  const chapter = rec ? chapterAt(rec.chapters, time) : -1;
  // The screenshots taken during the chapter playing are lit in the strip.
  const lit = new Set(rec?.chapters[chapter]?.shots.map((s) => s.id));
  const pick = (n: number) => {
    setAt(n);
    setTime(0);
    // The strip follows the stage, for the keys.
    root.current?.querySelector(`[data-review-media="${n + 1}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  };
  const expand = (on: boolean) => {
    setExpanded(on);
    // Expanded fills the body from its top.
    if (on) root.current?.parentElement?.scrollTo({ top: 0 });
  };
  useImperativeHandle(ref, () => ({
    step: (by) => shown.length && pick(step(at, shown.length, by)),
    togglePlay: () => player.current?.toggle(),
    chapter: (by) => player.current?.chapter(by),
    toggleExpand: () => current && expand(!expanded),
    collapse: () => {
      if (!expanded) return false;
      setExpanded(false);
      return true;
    },
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

  const stage = (
    <Stage
      key={current.kind === 'recording' ? current.take.media.id : current.kind === 'screenshot' ? current.media.id : current.page.id}
      shown={current}
      project={project}
      expanded={expanded}
      player={player}
      chapter={chapter}
      onTime={setTime}
      onExpand={() => expand(!expanded)}
    />
  );
  const bar = (
    <StageBar
      shown={current}
      n={at}
      count={shown.length}
      expanded={expanded}
      onStep={(by) => pick(step(at, shown.length, by))}
      onExpand={() => expand(!expanded)}
    />
  );

  return (
    <div ref={root} className={cn('relative grid min-h-full min-w-0 max-w-full grid-cols-[minmax(0,1fr)] content-start overflow-x-clip gap-4 p-4 sm:p-5', expanded && 'h-full')} data-review-proof data-review-media-at={at + 1}>
      {expanded ? (
        // Expanded, the piece on stage fills the body, its bar over it.
        <div className="absolute inset-0 z-10 flex flex-col gap-3 bg-black/95 p-4" data-review-expanded>
          {bar}
          <div className="flex min-h-0 flex-1 items-center justify-center">{stage}</div>
        </div>
      ) : (
        <div className={cn('grid min-w-0 max-w-full gap-5', rec && 'lg:grid-cols-[minmax(0,1fr)_280px]')}>
          <div className="grid min-w-0 content-start gap-2">
            {stage}
            {bar}
          </div>
          {rec && <Chapters take={rec} at={chapter} onPick={(s) => player.current?.seek(s)} />}
        </div>
      )}
      <Strip shown={shown} at={at} lit={lit} onPick={pick} />
    </div>
  );
}

// nameOf is what a piece of proof is called.
const nameOf = (s: Shown) => (s.kind === 'recording' ? readableName(s.take.media.name) : s.kind === 'screenshot' ? readableName(s.media.name) : s.page.title);

// StageBar is under the stage: where the piece is in the gallery, its name,
// and Expand.
function StageBar({ shown, n, count, expanded, onStep, onExpand }: { shown: Shown; n: number; count: number; expanded: boolean; onStep: (by: number) => void; onExpand: () => void }) {
  const t = useT();
  return (
    <div className={cn('flex min-w-0 items-center gap-2 text-[12.5px]', expanded ? 'text-white/80' : 'text-secondary')} data-review-stage-bar>
      <Button size="icon-sm" variant="ghost" disabled={n === 0} onClick={() => onStep(-1)} aria-label={t('review.previous')} title={`${t('review.previous')} (←)`}>
        <ChevronLeft />
      </Button>
      <span className="shrink-0 tabular-nums text-subtle">{t('review.position', { n: n + 1, count })}</span>
      <Button size="icon-sm" variant="ghost" disabled={n === count - 1} onClick={() => onStep(1)} aria-label={t('review.next')} title={`${t('review.next')} (→)`}>
        <ChevronRight />
      </Button>
      <KindIcon shown={shown} className="size-3.5 shrink-0 text-subtle" />
      <span className="min-w-0 flex-1 truncate">{nameOf(shown)}</span>
      {shown.kind === 'page' && shown.page.url && (
        <Button size="sm" variant="ghost" className="h-7" onClick={() => void window.agentbox.openExternal(shown.page.url)}>
          <ExternalLink />
          {t('review.openPage')}
        </Button>
      )}
      <Button size="sm" variant="ghost" className="h-7" aria-pressed={expanded} onClick={onExpand} data-review-expand>
        {expanded ? <Minimize2 /> : <Maximize2 />}
        {t(expanded ? 'review.collapse' : 'review.expand')}
        <span className="max-md:hidden" aria-hidden>
          <Kbd>{expanded ? 'Esc' : 'F'}</Kbd>
        </span>
      </Button>
    </div>
  );
}

function KindIcon({ shown, className }: { shown: Shown; className?: string }) {
  if (shown.kind === 'recording') return <Film className={className} />;
  if (shown.kind === 'screenshot') return <ImageIcon className={className} />;
  return <PageIcon className={className} />;
}

// Stage is the piece of proof on show, at the size of the recording's
// player, or filling the body when expanded.
function Stage({
  shown,
  project,
  expanded,
  player,
  chapter,
  onTime,
  onExpand,
}: {
  shown: Shown;
  project: string;
  expanded: boolean;
  player: Ref<PlayerHandle>;
  chapter: number;
  onTime: (seconds: number) => void;
  onExpand: () => void;
}) {
  const box = expanded ? 'size-full' : 'mx-auto aspect-[16/10] w-full';
  const width = expanded ? undefined : { maxWidth: 'calc(50vh * 1.6)' };
  if (shown.kind === 'recording') return <Player ref={player} take={shown.take} chapter={chapter} onTime={onTime} expanded={expanded} />;
  if (shown.kind === 'screenshot') {
    return (
      <button type="button" onClick={onExpand} className={cn(box, 'block overflow-hidden rounded-xl border border-line-strong bg-black')} style={width} data-review-stage="screenshot">
        <img src={mediaUrl(shown.media)} alt={shown.media.name} draggable={false} className="size-full object-contain" />
      </button>
    );
  }
  return <PageFrame project={project} page={shown.page} className={cn(box, 'overflow-hidden rounded-xl border border-line-strong')} style={width} />;
}

// Strip is every piece of proof, the one on stage lit; a click puts it there.
function Strip({ shown, at, lit, onPick }: { shown: Shown[]; at: number; lit: ReadonlySet<string>; onPick: (n: number) => void }) {
  const t = useT();
  const counts = [
    shown.filter((s) => s.kind === 'recording').length && t('review.count.recordings', { count: shown.filter((s) => s.kind === 'recording').length }),
    shown.filter((s) => s.kind === 'screenshot').length && t('review.count.screenshots', { count: shown.filter((s) => s.kind === 'screenshot').length }),
    shown.filter((s) => s.kind === 'page').length && t('review.count.pages', { count: shown.filter((s) => s.kind === 'page').length }),
  ].filter(Boolean);
  if (shown.length < 2) return null;
  return (
    <section className="min-w-0">
      <h3 className="mb-2 flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wide text-subtle">
        {t('review.media')}
        <span className="font-normal normal-case tracking-normal">{counts.join(' · ')}</span>
        <span className="ml-auto font-normal normal-case tracking-normal max-md:hidden">{t('review.mediaKeys')}</span>
      </h3>
      <div className="flex max-w-full gap-2 overflow-x-auto overscroll-x-contain p-0.5 pb-1 [scrollbar-width:thin]" data-review-shots>
        {shown.map((s, i) => (
          <button
            key={s.kind === 'recording' ? s.take.media.id : s.kind === 'screenshot' ? s.media.id : s.page.id}
            type="button"
            title={nameOf(s)}
            aria-current={i === at || undefined}
            onClick={() => onPick(i)}
            className={cn(
              'w-36 shrink-0 overflow-hidden rounded-lg border bg-surface text-left transition hover:border-line-heavy',
              i === at ? 'border-brand-400 ring-1 ring-brand-400' : s.kind === 'screenshot' && lit.has(s.media.id) ? 'border-brand-400/50' : 'border-line-strong',
            )}
            data-review-media={i + 1}
            data-review-media-kind={s.kind}
          >
            <Thumb shown={s} />
            <span className="flex min-w-0 items-center gap-1.5 px-2 py-1.5 text-[11.5px] text-secondary">
              <KindIcon shown={s} className="size-3 shrink-0 text-subtle" />
              <span className="min-w-0 truncate">{nameOf(s)}</span>
            </span>
          </button>
        ))}
      </div>
    </section>
  );
}

function Thumb({ shown }: { shown: Shown }) {
  if (shown.kind === 'screenshot') return <img src={mediaUrl(shown.media)} alt="" draggable={false} className="aspect-[16/10] w-full bg-black object-cover object-top" />;
  if (shown.kind === 'recording') {
    return (
      <span className="relative flex aspect-[16/10] w-full items-center justify-center bg-black text-white/80">
        <Play className="size-6 fill-current" />
        {shown.take.media.meta.duration ? <span className="absolute bottom-1 right-1.5 rounded bg-black/70 px-1 text-[10.5px] tabular-nums">{clock(shown.take.media.meta.duration)}</span> : null}
      </span>
    );
  }
  return (
    <span className="flex aspect-[16/10] w-full items-center justify-center bg-surface-faint">
      <PageIcon className="size-6 text-subtle" />
    </span>
  );
}

// Chapters is what the agent checked in the recording playing, the chapter
// playing lit; a click plays from it.
function Chapters({ take, at, onPick }: { take: Take; at: number; onPick: (seconds: number) => void }) {
  const t = useT();
  return (
    <section className="min-w-0" data-review-chapters>
      <h3 className="mb-2 flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wide text-subtle">
        {t('review.checked')}
        {take.chapters.length > 1 && (
          <span className="ml-auto flex gap-1 font-normal normal-case max-md:hidden" aria-hidden>
            <Kbd>[</Kbd>
            <Kbd>]</Kbd>
          </span>
        )}
      </h3>
      {take.chapters.length === 0 ? (
        <p className="text-[12px] leading-relaxed text-subtle">{t('review.noChapters')}</p>
      ) : (
        <ol className="grid grid-cols-[minmax(0,1fr)] gap-0.5">
          {take.chapters.map((c, i) => (
            <li key={`${c.at}-${i}`}>
              <button
                type="button"
                onClick={() => onPick(c.at)}
                aria-current={i === at || undefined}
                className={cn('flex w-full gap-2.5 rounded-lg px-2.5 py-2 text-left transition', i === at ? 'bg-surface-strong' : 'hover:bg-surface-raised')}
                data-review-chapter={i + 1}
              >
                <span className={cn('mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full', i === at ? 'bg-emerald-400/20 text-emerald-300' : 'text-emerald-400')}>
                  <Check className="size-3.5" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex items-baseline gap-2">
                    <span className={cn('min-w-0 flex-1 [overflow-wrap:anywhere] text-[13px]', i === at ? 'text-title' : 'text-primary')}>{c.title}</span>
                    <span className="shrink-0 text-[11px] tabular-nums text-faint">{clock(c.at)}</span>
                  </span>
                  {c.detail && <span className="mt-0.5 line-clamp-3 block [overflow-wrap:anywhere] text-[12px] leading-snug text-subtle">{c.detail}</span>}
                </span>
              </button>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

export interface PlayerHandle {
  seek: (seconds: number) => void;
  toggle: () => void;
  chapter: (by: number) => void;
}

// Player plays a recording muted, with a scrubber cut into its chapters and
// the one playing named over the picture.
function Player({ ref, take, chapter, onTime, expanded }: { ref: Ref<PlayerHandle>; take: Take; chapter: number; onTime: (seconds: number) => void; expanded: boolean }) {
  const t = useT();
  const video = useRef<HTMLVideoElement>(null);
  const [time, setTime] = useState(0);
  const [length, setLength] = useState(take.media.meta.duration ?? 0);
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
  useImperativeHandle(ref, () => ({
    seek,
    toggle,
    chapter: (by) => {
      if (!take.chapters.length) return;
      const to = take.chapters[step(Math.max(chapter, 0), take.chapters.length, by)];
      if (to) seek(to.at);
    },
  }));
  const current = take.chapters[chapter];
  // The scrubber's segments: one per chapter, or the whole recording.
  const marks = take.chapters.length ? take.chapters.map((c) => c.at) : [0];
  if (broken) {
    return (
      <div className="flex flex-col items-center gap-2 rounded-xl border border-line bg-surface-faint p-10 text-center">
        <CircleAlert className="size-8 text-faint" />
        <div className="text-sm font-medium text-secondary">{t('agent.mediaTab.unplayable')}</div>
        <div className="max-w-sm text-xs text-subtle">{t('agent.mediaTab.unplayableBody')}</div>
      </div>
    );
  }
  return (
    <div
      className={cn('mx-auto w-full overflow-hidden rounded-xl border border-line-strong bg-black', expanded && 'flex h-full flex-col')}
      style={expanded ? undefined : { maxWidth: 'calc(50vh * 1.6)' }}
      data-review-player={take.media.id}
    >
      <div className={cn('relative', expanded && 'min-h-0 flex-1')}>
        <video
          ref={video}
          src={mediaUrl(take.media)}
          autoPlay
          muted
          playsInline
          onClick={toggle}
          onLoadedMetadata={(e) => Number.isFinite(e.currentTarget.duration) && setLength(e.currentTarget.duration)}
          onTimeUpdate={(e) => {
            setTime(e.currentTarget.currentTime);
            onTime(e.currentTarget.currentTime);
          }}
          onPlay={() => setPlaying(true)}
          onPause={() => setPlaying(false)}
          onError={() => setBroken(true)}
          className={cn('block w-full cursor-pointer object-contain', expanded ? 'h-full' : 'aspect-[16/10]')}
        />
        {current && (
          <span className="pointer-events-none absolute left-3 top-3 max-w-[80%] truncate rounded-md bg-black/65 px-2 py-1 text-[12px] text-white backdrop-blur">
            <span className="tabular-nums text-white/60">{t('review.position', { n: chapter + 1, count: take.chapters.length })}</span> {current.title}
          </span>
        )}
      </div>
      <div className="flex items-center gap-3 px-3 py-2 text-white">
        <button type="button" onClick={toggle} aria-label={t(playing ? 'review.pause' : 'review.play')} title={`${t(playing ? 'review.pause' : 'review.play')} (Space)`} className="text-white/85 hover:text-white">
          {playing ? <Pause className="size-4 fill-current" /> : <Play className="size-4 fill-current" />}
        </button>
        <div className="flex h-5 min-w-0 flex-1 items-center gap-1">
          {marks.map((start, i) => {
            const end = marks[i + 1] ?? length;
            const span = Math.max(end - start, 0.001);
            const p = Math.max(0, Math.min(1, (time - start) / span));
            return (
              <button
                key={`${start}-${i}`}
                type="button"
                title={take.chapters[i]?.title}
                aria-label={take.chapters[i]?.title ?? t('review.play')}
                onClick={(e) => {
                  const r = e.currentTarget.getBoundingClientRect();
                  seek(start + ((e.clientX - r.left) / r.width) * span);
                }}
                className="group flex h-full min-w-1 items-center"
                style={{ flex: `${span} 1 0` }}
              >
                <span className={cn('block h-1 w-full overflow-hidden rounded-full bg-white/25 transition-all group-hover:h-1.5', i === chapter && take.chapters.length > 0 && 'h-1.5')}>
                  <span className="block h-full bg-white" style={{ width: `${p * 100}%` }} />
                </span>
              </button>
            );
          })}
        </div>
        <span className="shrink-0 text-[11.5px] tabular-nums text-white/70">
          {clock(time)} / {clock(length)}
        </span>
      </div>
    </div>
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

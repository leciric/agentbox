import { Check, ChevronLeft, ChevronRight, CircleAlert, ExternalLink, Film, Image as ImageIcon, Pause, Play, X } from 'lucide-react';
import { useEffect, useImperativeHandle, useRef, useState, type ReactNode, type Ref } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { chapterAt, readableName, type Proof, type Take } from '../lib/review';
import { cn } from '../lib/utils';
import { PageIcon } from './pages/PageThumb';
import { Button } from './ui/button';

// The review's Proof tab, what it opens on: what the agent showed it checked
// before what it changed. Its latest recording plays at the top, cut into a
// chapter for each thing it checked (lib/review.ts proofOf), listed beside it
// to jump to; its screenshots are a strip under it, each lit while its
// chapter plays and opened large on a click; its Hatch pages after them.
// Beside, under the chapters, is what the agent said (aside).

// clock is seconds as m:ss.
export const clock = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, '0')}`;

// Open is what's shown large over the proof: a screenshot or a page.
type Open = { kind: 'shot'; n: number } | { kind: 'page'; n: number } | null;

export function ReviewProof({ project, proof, aside }: { project: string; proof: Proof; aside: ReactNode }) {
  const t = useT();
  const [take, setTake] = useState(0);
  const [time, setTime] = useState(0);
  const [open, setOpen] = useState<Open>(null);
  const player = useRef<PlayerHandle>(null);
  const rec = proof.recordings[Math.min(take, proof.recordings.length - 1)];
  const chapter = rec ? chapterAt(rec.chapters, time) : -1;
  const lit = new Set(rec?.chapters[chapter]?.shots.map((s) => s.id));
  const nothing = !rec && proof.screenshots.length === 0 && proof.pages.length === 0;

  return (
    <div className="relative grid min-h-full gap-5 p-4 sm:p-5 lg:grid-cols-[minmax(0,1fr)_minmax(260px,320px)]" data-review-proof>
      <div className="flex min-w-0 flex-col gap-4">
        {nothing ? (
          <div className="grid justify-items-center gap-2 rounded-xl border border-line bg-surface-faint px-6 py-14 text-center">
            <ImageIcon className="size-8 text-faint" />
            <div className="text-sm font-medium text-secondary">{t('review.noProof')}</div>
            <div className="max-w-sm text-xs text-subtle">{t('review.noProofBody')}</div>
          </div>
        ) : rec ? (
          <>
            {proof.recordings.length > 1 && (
              <div className="flex min-w-0 flex-wrap gap-1.5" data-review-takes>
                {proof.recordings.map((r, i) => (
                  <button
                    key={r.media.id}
                    type="button"
                    aria-pressed={i === take}
                    onClick={() => {
                      setTake(i);
                      setTime(0);
                    }}
                    className={cn(
                      'inline-flex max-w-[16rem] items-center gap-1.5 rounded-md border px-2 py-1 text-[12px] transition',
                      i === take ? 'border-line-strong bg-surface-strong text-title' : 'border-line text-secondary hover:text-title',
                    )}
                  >
                    <Film className="size-3.5 shrink-0" />
                    <span className="truncate">{readableName(r.media.name)}</span>
                  </button>
                ))}
              </div>
            )}
            <Player key={rec.media.id} ref={player} take={rec} chapter={chapter} onTime={setTime} />
          </>
        ) : (
          // No recording: the first screenshot takes its place.
          proof.screenshots[0] && (
            <button type="button" className="overflow-hidden rounded-xl border border-line-strong bg-black" onClick={() => setOpen({ kind: 'shot', n: 0 })}>
              <img src={mediaUrl(proof.screenshots[0])} alt={proof.screenshots[0].name} className="mx-auto max-h-[55vh] object-contain" />
            </button>
          )
        )}
        {proof.screenshots.length > 0 && (
          <section>
            <h3 className="mb-2 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-subtle">
              <ImageIcon className="size-3.5" />
              {t('review.screenshots')} <span className="tabular-nums">{proof.screenshots.length}</span>
            </h3>
            <div className="flex gap-2 overflow-x-auto pb-1 [scrollbar-width:thin]" data-review-shots>
              {proof.screenshots.map((s, i) => (
                <button
                  key={s.id}
                  type="button"
                  title={s.name}
                  onClick={() => setOpen({ kind: 'shot', n: i })}
                  className={cn('w-40 shrink-0 overflow-hidden rounded-lg border bg-surface text-left transition hover:border-line-heavy', lit.has(s.id) ? 'border-brand-400' : 'border-line-strong')}
                  data-review-shot={i + 1}
                >
                  <img src={mediaUrl(s)} alt="" draggable={false} className="aspect-[16/10] w-full bg-black object-cover object-top" />
                  <span className="block truncate px-2 py-1.5 text-[11.5px] text-secondary">{readableName(s.name)}</span>
                </button>
              ))}
            </div>
          </section>
        )}
        {proof.pages.length > 0 && (
          <section className="grid gap-1.5">
            <h3 className="mb-0.5 text-[11px] font-semibold uppercase tracking-wide text-subtle">{t('review.pages')}</h3>
            {proof.pages.map((p, i) => (
              <div key={p.id} className="flex min-w-0 items-center gap-3 rounded-lg border border-line bg-surface px-3 py-2" data-review-page={p.id}>
                <PageIcon className="size-4 shrink-0 text-muted" />
                <button type="button" className="min-w-0 flex-1 truncate text-left text-[13px] text-primary hover:underline" onClick={() => setOpen({ kind: 'page', n: i })}>
                  {p.title}
                </button>
                {p.url && (
                  <Button size="sm" variant="ghost" onClick={() => void window.agentbox.openExternal(p.url)}>
                    <ExternalLink />
                    {t('review.openPage')}
                  </Button>
                )}
              </div>
            ))}
          </section>
        )}
      </div>
      <aside className="flex min-w-0 flex-col gap-5">
        {rec && <Chapters take={rec} at={chapter} onPick={(s) => player.current?.seek(s)} />}
        {aside}
      </aside>
      {open?.kind === 'shot' && <ShotView shots={proof.screenshots} n={open.n} onPick={(n) => setOpen({ kind: 'shot', n })} onClose={() => setOpen(null)} />}
      {open?.kind === 'page' && proof.pages[open.n] && <PageView project={project} page={proof.pages[open.n]} onClose={() => setOpen(null)} />}
    </div>
  );
}

// Chapters is what the agent checked in the recording playing, the chapter
// playing lit; a click plays from it.
function Chapters({ take, at, onPick }: { take: Take; at: number; onPick: (seconds: number) => void }) {
  const t = useT();
  return (
    <section data-review-chapters>
      <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-subtle">{t('review.checked')}</h3>
      {take.chapters.length === 0 ? (
        <p className="text-[12px] leading-relaxed text-subtle">{t('review.noChapters')}</p>
      ) : (
        <ol className="grid gap-0.5">
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
                    <span className={cn('min-w-0 flex-1 break-words text-[13px]', i === at ? 'text-title' : 'text-primary')}>{c.title}</span>
                    <span className="shrink-0 text-[11px] tabular-nums text-faint">{clock(c.at)}</span>
                  </span>
                  {c.detail && <span className="mt-0.5 line-clamp-3 block text-[12px] leading-snug text-subtle">{c.detail}</span>}
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
}

// Player plays a recording muted, with a scrubber cut into its chapters and
// the one playing named over the picture.
function Player({ ref, take, chapter, onTime }: { ref: Ref<PlayerHandle>; take: Take; chapter: number; onTime: (seconds: number) => void }) {
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
  useImperativeHandle(ref, () => ({ seek }));
  const toggle = () => {
    const v = video.current;
    if (v) void (v.paused ? v.play().catch(() => {}) : v.pause());
  };
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
    <div className="mx-auto w-full overflow-hidden rounded-xl border border-line-strong bg-black" style={{ maxWidth: 'calc(50vh * 1.6)' }} data-review-player={take.media.id}>
      <div className="relative">
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
          className="block aspect-[16/10] w-full cursor-pointer object-contain"
        />
        {current && (
          <span className="pointer-events-none absolute left-3 top-3 max-w-[80%] truncate rounded-md bg-black/65 px-2 py-1 text-[12px] text-white backdrop-blur">
            <span className="tabular-nums text-white/60">{t('review.position', { n: chapter + 1, count: take.chapters.length })}</span> {current.title}
          </span>
        )}
      </div>
      <div className="flex items-center gap-3 px-3 py-2 text-white">
        <button type="button" onClick={toggle} aria-label={t(playing ? 'review.pause' : 'review.play')} className="text-white/85 hover:text-white">
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

// ShotView is one screenshot over the proof, the arrows (and ← →) to the
// others. Esc closes the review; this has its own close.
function ShotView({ shots, n, onPick, onClose }: { shots: T.MediaItem[]; n: number; onPick: (n: number) => void; onClose: () => void }) {
  const t = useT();
  const shot = shots[n];
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === 'ArrowRight' && n < shots.length - 1) onPick(n + 1);
      else if (e.key === 'ArrowLeft' && n > 0) onPick(n - 1);
      else return;
      e.preventDefault();
      e.stopPropagation();
    };
    addEventListener('keydown', key, true);
    return () => removeEventListener('keydown', key, true);
  }, [n, shots.length, onPick]);
  if (!shot) return null;
  return (
    <div className="absolute inset-0 z-10 flex flex-col bg-black/95 p-4" data-review-shot-open={n + 1}>
      <div className="flex min-w-0 items-center gap-3 pb-3 text-[12.5px] text-white/80">
        <span className="tabular-nums text-white/50">{t('review.position', { n: n + 1, count: shots.length })}</span>
        <span className="min-w-0 flex-1 truncate">{readableName(shot.name)}</span>
        <button type="button" onClick={onClose} aria-label={t('review.closeShot')} className="rounded-md p-1 text-white/70 hover:bg-white/10 hover:text-white" data-review-shot-close>
          <X className="size-4" />
        </button>
      </div>
      <div className="relative flex min-h-0 flex-1 items-center justify-center">
        <img src={mediaUrl(shot)} alt={shot.name} draggable={false} className="max-h-full max-w-full rounded-md object-contain" />
        {n > 0 && (
          <button type="button" aria-label={t('review.previous')} onClick={() => onPick(n - 1)} className="absolute left-0 rounded-full bg-white/10 p-2 text-white hover:bg-white/20">
            <ChevronLeft className="size-5" />
          </button>
        )}
        {n < shots.length - 1 && (
          <button type="button" aria-label={t('review.next')} onClick={() => onPick(n + 1)} className="absolute right-0 rounded-full bg-white/10 p-2 text-white hover:bg-white/20">
            <ChevronRight className="size-5" />
          </button>
        )}
      </div>
    </div>
  );
}

// PageView is a Hatch page over the proof, framed as the page preview frames
// it: an opaque origin, no reach into the app.
function PageView({ project, page, onClose }: { project: string; page: T.Artifact; onClose: () => void }) {
  const t = useT();
  const [loaded, setLoaded] = useState(false);
  return (
    <div className="absolute inset-0 z-10 flex flex-col bg-modal" data-review-page-open={page.id}>
      <div className="flex min-w-0 items-center gap-2 border-b border-line px-4 py-2 text-[12.5px]">
        <PageIcon className="size-3.5 shrink-0 text-muted" />
        <span className="min-w-0 flex-1 truncate font-medium text-primary">{page.title}</span>
        {page.url && (
          <Button size="sm" variant="ghost" onClick={() => void window.agentbox.openExternal(page.url)}>
            <ExternalLink />
            {t('review.openPage')}
          </Button>
        )}
        <button type="button" onClick={onClose} aria-label={t('review.closeShot')} className="rounded-md p-1 text-subtle hover:bg-surface-raised hover:text-primary">
          <X className="size-4" />
        </button>
      </div>
      <div className="relative min-h-0 flex-1">
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
    </div>
  );
}

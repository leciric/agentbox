// An agent's work as a timeline (lib/timeline.ts): its steps along a track,
// a player above it showing the recording, the screenshot or the step's own
// text at the playhead, and each step's media inline. Scrubbing the track
// seeks the recordings made while the agent worked, and Play plays them
// through, skimming the steps between at speed.
//
// Two shapes: TimelinePanel runs down the right-hand panel, the steps listed
// under the player; TimelineWide fills the review, the steps as a strip of
// cards under a large player.
import { useQuery } from '@tanstack/react-query';
import { FileCode, FileText, FlaskConical, GitPullRequest, ListTodo, MessageSquare, MonitorPlay, Pause, Play, Search, SquareTerminal, type LucideIcon } from 'lucide-react';
import { type PointerEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { mediaUrl } from '../../lib/media';
import { openPage } from '../../lib/pages';
import { clockAt, fileName, frameAt, kindOf, layout, marks, mmss, placedAt, type Placed, type StepKind, type Track, trackAt } from '../../lib/timeline';
import { cn } from '../../lib/utils';
import { TitleTile } from '../pages/PageThumb';
import { useProjectPages } from '../pages/usePages';

export const timelineKey = (ref: string) => ['timeline', ref];

// useTimeline reads an agent's timeline, again every few seconds while it
// works.
export function useTimeline(ref: string | undefined) {
  return useQuery({
    queryKey: timelineKey(ref ?? ''),
    queryFn: () => api.timeline(ref!),
    enabled: !!ref,
    staleTime: 5_000,
    refetchInterval: (q) => (q.state.data?.working ? 10_000 : false),
  });
}

export const kindIcon: Record<StepKind, LucideIcon> = {
  task: MessageSquare,
  read: Search,
  plan: ListTodo,
  edit: FileCode,
  test: FlaskConical,
  run: SquareTerminal,
  desktop: MonitorPlay,
  page: FileText,
  pr: GitPullRequest,
};

// One hue per kind of step, the same on the track, the list and the reel.
export const kindTone: Record<StepKind, string> = {
  task: 'bg-zinc-400',
  read: 'bg-slate-500',
  plan: 'bg-sky-400',
  edit: 'bg-violet-400',
  test: 'bg-emerald-400',
  run: 'bg-cyan-500',
  desktop: 'bg-amber-400',
  page: 'bg-pink-400',
  pr: 'bg-brand-400',
};
export const kindText: Record<StepKind, string> = {
  task: 'text-secondary',
  read: 'text-slate-400',
  plan: 'text-sky-400',
  edit: 'text-violet-400',
  test: 'text-emerald-400',
  run: 'text-cyan-400',
  desktop: 'text-amber-400',
  page: 'text-pink-400',
  pr: 'text-brand-400',
};

// useStepTitle says what a step did: the agent's or the user's own words
// where it has some, counts otherwise.
export function useStepTitle() {
  const t = useT();
  return useCallback(
    (s: T.TimelineStep) => {
      const kind = kindOf(s);
      const files = s.paths ?? [];
      switch (kind) {
        case 'task':
          return s.title;
        case 'read':
          return s.title || (files.length ? t('timeline.title.read', { count: files.length }) : t('timeline.title.search'));
        case 'plan':
          return t('timeline.title.plan');
        case 'edit':
          return files.length ? t('timeline.title.edit', { file: fileName(files[0]!), count: files.length - 1 }) : t('timeline.title.editNone');
        case 'test':
          return s.status === 'failed' ? t('timeline.title.testFailed') : s.status === 'running' ? t('timeline.title.testRunning') : t('timeline.title.testPassed');
        case 'desktop':
          return s.title || t('timeline.title.desktop');
        case 'page':
          return s.title || t('timeline.title.page');
        case 'pr':
          return t('timeline.title.pr');
        default:
          return s.title || t('timeline.title.run');
      }
    },
    [t],
  );
}

const clockTime = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

// usePlayhead is a track's playhead: x on the track, and whether it plays.
// Playing skims at skim track seconds a second, except over a recording,
// whose video plays at its own pace and moves the playhead (onClock).
const skim = 12;
function usePlayhead(track: Track, start?: number) {
  const [x, setX] = useState(() => start ?? 0);
  const [playing, setPlaying] = useState(false);
  const driven = useRef(false);
  const length = track.length;
  useEffect(() => {
    if (!playing) return;
    let last = performance.now();
    let raf = requestAnimationFrame(function tick(now) {
      const dt = (now - last) / 1000;
      last = now;
      if (!driven.current) {
        setX((v) => {
          const next = v + dt * skim;
          if (next >= length) setPlaying(false);
          return Math.min(next, length);
        });
      }
      raf = requestAnimationFrame(tick);
    });
    return () => cancelAnimationFrame(raf);
  }, [playing, length]);
  const play = (on: boolean) => {
    if (on && x >= length) setX(0);
    setPlaying(on);
  };
  // A video playing reports its time; the playhead follows it.
  const onClock = useCallback((ms: number | null) => {
    driven.current = ms !== null;
    if (ms !== null) setX(trackAt(track, ms));
  }, [track]);
  return { x, setX, playing, play, onClock };
}

// Player is the frame at x: a recording seeked there (playing while the
// playhead plays), a screenshot, or the step's own text.
function Player({ track, x, playing, onClock, className }: { track: Track; x: number; playing: boolean; onClock: (ms: number | null) => void; className?: string }) {
  const t = useT();
  const title = useStepTitle();
  const frame = frameAt(track, x);
  const video = useRef<HTMLVideoElement>(null);
  const recording = frame?.kind === 'recording' ? frame : undefined;
  useEffect(() => {
    const v = video.current;
    if (!v || !recording) return;
    if (playing) {
      void v.play().catch(() => {});
    } else {
      v.pause();
      if (Math.abs(v.currentTime - recording.offset) > 0.3) v.currentTime = recording.offset;
    }
  }, [recording, playing]);
  useEffect(() => {
    if (!recording || !playing) onClock(null);
  }, [recording, playing, onClock]);
  const p = placedAt(track, x);
  return (
    <div className={cn('relative aspect-[16/10] w-full overflow-hidden rounded-lg bg-black ring-1 ring-line [container-type:inline-size]', className)} data-timeline-frame={frame?.kind}>
      {recording ? (
        <video
          key={recording.media.id}
          ref={video}
          src={mediaUrl(recording.media)}
          muted
          playsInline
          preload="auto"
          className="absolute inset-0 size-full object-contain"
          onLoadedMetadata={(e) => {
            e.currentTarget.currentTime = recording.offset;
            if (playing) void e.currentTarget.play().catch(() => {});
          }}
          onTimeUpdate={(e) => {
            if (playing && !e.currentTarget.paused) onClock(new Date(recording.media.createdAt).getTime() + e.currentTarget.currentTime * 1000);
          }}
          onEnded={() => onClock(null)}
        />
      ) : frame?.kind === 'screenshot' ? (
        <img src={mediaUrl(frame.media)} alt={frame.media.name} className="absolute inset-0 size-full object-contain" draggable={false} />
      ) : frame?.kind === 'step' ? (
        <StepText placed={frame.placed} />
      ) : (
        <div className="absolute inset-0 flex items-center justify-center text-[12px] text-white/50">{t('timeline.empty')}</div>
      )}
      {p && (
        <div className="pointer-events-none absolute inset-x-0 bottom-0 flex items-center gap-2 bg-gradient-to-t from-black/75 to-transparent px-2.5 pb-1.5 pt-6 text-[11px] tabular-nums text-white/85">
          {recording && (
            <span className="flex items-center gap-1">
              <span className="size-1.5 rounded-full bg-rose-500" />
              {t('timeline.rec')}
            </span>
          )}
          <span>{clockTime(clockAt(track, x))}</span>
          <span className="min-w-0 truncate text-white/60">{title(p.step)}</span>
          {recording && <span className="ml-auto shrink-0">{mmss(recording.offset)} / {mmss(recording.media.meta.duration ?? 0)}</span>}
        </div>
      )}
    </div>
  );
}

// StepText is a step with no picture, as a terminal would have shown it:
// its command and how it ended, the diff, the plan, the files read.
function StepText({ placed }: { placed: Placed }) {
  const title = useStepTitle();
  const s = placed.step;
  const kind = kindOf(s);
  const lines = s.lines ?? [];
  return (
    <div className="absolute inset-0 flex flex-col bg-[#0e0e14] font-mono text-[clamp(8px,1.6cqw,13px)] leading-relaxed text-zinc-300">
      <div className="min-h-0 flex-1 space-y-0.5 overflow-hidden px-[4%] py-[3%]">
        <p className={cn('truncate', kindText[kind])}>● {title(s)}</p>
        {kind === 'read' && (s.paths ?? []).slice(0, 12).map((p) => <p key={p} className="truncate text-zinc-500">{p}</p>)}
        {lines.map((l, i) => (
          <p
            key={i}
            className={cn(
              'whitespace-pre-wrap break-all',
              kind === 'edit' && l.startsWith('+') && 'bg-emerald-500/10 text-emerald-300',
              kind === 'edit' && l.startsWith('-') && !l.startsWith('---') && 'bg-rose-500/10 text-rose-300',
              kind === 'edit' && l.startsWith('---') && 'text-zinc-500',
              kind === 'test' && /FAIL|failed|fail [1-9]/.test(l) && 'text-rose-300',
              kind === 'test' && /^(ok\b|PASS)|✔|\bpassed\b|pass [1-9]/.test(l) && 'text-emerald-300',
            )}
          >
            {l}
          </p>
        ))}
      </div>
    </div>
  );
}

// Scrubber is the track as a bar: a segment per step, in its kind's hue,
// ticks where screenshots were taken and pages made, a line under each
// recording. Dragging moves the playhead; hovering names the step there.
function Scrubber({ track, x, onX, tall, labels }: { track: Track; x: number; onX: (x: number) => void; tall?: boolean; labels?: boolean }) {
  const title = useStepTitle();
  const ref = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const L = track.length || 1;
  const at = (clientX: number) => {
    const r = ref.current!.getBoundingClientRect();
    return Math.min(L, Math.max(0, ((clientX - r.left) / r.width) * L));
  };
  const ms = useMemo(() => marks(track), [track]);
  const active = placedAt(track, x)?.index;
  const hovered = hover !== null ? placedAt(track, hover) : undefined;
  const pct = (v: number) => `${(v / L) * 100}%`;
  return (
    <div className="relative select-none" data-timeline-scrubber>
      {labels && (
        <div className="relative mb-1 h-4 overflow-hidden">
          {track.steps.map((p) => (
            <span key={p.index} className={cn('absolute top-0 truncate pl-0.5 text-[10.5px]', p.index === active ? 'text-title' : 'text-faint')} style={{ left: pct(p.from), width: pct(p.to - p.from) }}>
              {title(p.step)}
            </span>
          ))}
        </div>
      )}
      <div
        ref={ref}
        role="slider"
        tabIndex={0}
        aria-valuemin={0}
        aria-valuemax={Math.round(L)}
        aria-valuenow={Math.round(x)}
        aria-valuetext={mmss(x)}
        className={cn('relative cursor-pointer touch-none outline-none focus-visible:ring-2 focus-visible:ring-brand-500/60', tall ? 'h-7' : 'h-5')}
        onPointerDown={(e: PointerEvent<HTMLDivElement>) => {
          e.currentTarget.setPointerCapture(e.pointerId);
          onX(at(e.clientX));
        }}
        onPointerMove={(e) => {
          setHover(at(e.clientX));
          if (e.buttons) onX(at(e.clientX));
        }}
        onPointerLeave={() => setHover(null)}
        onKeyDown={(e) => {
          const by = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
          if (!by) return;
          e.preventDefault();
          e.stopPropagation();
          // A step at a time: to the next step's start, or back to this one's.
          const i = placedAt(track, x)?.index ?? 0;
          const here = track.steps[i]!;
          const to = by > 0 ? track.steps[i + 1]?.from ?? L : x - here.from > 1 ? here.from : (track.steps[i - 1]?.from ?? 0);
          onX(to);
        }}
      >
        <div className={cn('absolute inset-x-0 flex gap-px overflow-hidden rounded', tall ? 'top-1.5 h-3' : 'top-1.5 h-2')}>
          {track.steps.map((p) => (
            <div key={p.index} className={cn('h-full', kindTone[kindOf(p.step)], p.index === active ? 'opacity-100' : 'opacity-45', p.step.status === 'failed' && 'bg-rose-500')} style={{ width: pct(p.to - p.from) }} />
          ))}
        </div>
        {ms.map((m) =>
          m.kind === 'recording' ? (
            <div key={m.id} className="absolute bottom-0 h-0.5 rounded bg-rose-400" style={{ left: pct(m.from), width: pct(m.to - m.from) }} />
          ) : (
            <div key={m.id} className={cn('absolute top-0.5 w-0.5 rounded-full', m.kind === 'page' ? 'bg-pink-300' : 'bg-title', tall ? 'h-6' : 'h-4')} style={{ left: pct(m.at) }} />
          ),
        )}
        <div className="pointer-events-none absolute top-0 bottom-0 w-0.5 -translate-x-1/2 rounded bg-title shadow" style={{ left: pct(x) }}>
          <span className="absolute -left-1 -top-1 size-2.5 rounded-full bg-title" />
        </div>
        {hovered && hover !== null && (
          <div className="pointer-events-none absolute bottom-full z-20 mb-1.5 max-w-[240px] -translate-x-1/2 truncate rounded-md border border-line bg-overlay px-2 py-1 text-[11px] text-secondary shadow-md" style={{ left: `clamp(60px, ${pct(hover)}, calc(100% - 60px))` }}>
            {clockTime(clockAt(track, hover))} · {title(hovered.step)}
          </div>
        )}
      </div>
    </div>
  );
}

function PlayButton({ playing, onClick, className }: { playing: boolean; onClick: () => void; className?: string }) {
  const t = useT();
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn('flex size-7 shrink-0 items-center justify-center rounded-full bg-surface-strong text-title hover:bg-brand-500 hover:text-white', className)}
      aria-label={playing ? t('timeline.pause') : t('timeline.play')}
      data-timeline-play
    >
      {playing ? <Pause className="size-3.5" /> : <Play className="size-3.5 translate-x-px" />}
    </button>
  );
}

// MediaThumb is one thing a step made: a recording (with its length), a
// screenshot, or a Hatch page, which opens in the app's page viewer.
function MediaThumb({ m, onJump }: { m: T.MediaItem; onJump: (ms: number) => void }) {
  return (
    <button type="button" onClick={() => onJump(new Date(m.createdAt).getTime())} className="group relative block min-w-0 text-left" title={m.name}>
      {m.kind === 'recording' ? (
        <span className="relative block aspect-[16/10] overflow-hidden rounded-md bg-black ring-1 ring-line group-hover:ring-brand-400">
          <video src={`${mediaUrl(m)}#t=0.5`} muted preload="metadata" className="size-full object-cover" />
          <span className="absolute inset-0 flex items-center justify-center">
            <span className="flex size-7 items-center justify-center rounded-full bg-black/60 text-white group-hover:bg-brand-500">
              <Play className="size-3.5 translate-x-px" />
            </span>
          </span>
          {!!m.meta?.duration && <span className="absolute bottom-1 right-1 rounded bg-black/70 px-1 text-[10px] tabular-nums text-white">{mmss(m.meta.duration)}</span>}
        </span>
      ) : m.kind === 'screenshot' ? (
        <img src={mediaUrl(m)} alt="" loading="lazy" className="aspect-[16/10] w-full rounded-md object-cover ring-1 ring-line group-hover:ring-brand-400" />
      ) : (
        <span className="flex aspect-[16/10] items-center justify-center rounded-md bg-surface-raised text-[11px] text-subtle ring-1 ring-line">{m.kind}</span>
      )}
      <span className="mt-1 block truncate text-[10.5px] text-subtle">{m.name}</span>
    </button>
  );
}

function PageCard({ project, page }: { project: string; page: T.TimelinePage }) {
  const pages = useProjectPages(project);
  const artifact = pages.data?.artifacts.find((a) => a.item === page.item || (page.id && a.id === page.id));
  return (
    <button
      type="button"
      disabled={!artifact}
      onClick={() => artifact && openPage(project, artifact)}
      className="group relative block min-w-0 text-left disabled:cursor-default"
    >
      <span className="relative block aspect-[16/10] overflow-hidden rounded-md ring-1 ring-line group-enabled:group-hover:ring-pink-400">
        <TitleTile page={{ id: artifact?.id ?? page.item, title: page.title }} />
      </span>
      <span className="mt-1 block truncate text-[10.5px] text-subtle">{page.title}</span>
    </button>
  );
}

function StepMedia({ project, step, onJump, className }: { project: string; step: T.TimelineStep; onJump: (ms: number) => void; className?: string }) {
  const media = (step.media ?? []).filter((m) => m.kind === 'recording' || m.kind === 'screenshot');
  const pages = step.pages ?? [];
  if (!media.length && !pages.length) return null;
  return (
    <div className={cn('grid grid-cols-2 gap-2', className)}>
      {media.map((m) => <MediaThumb key={m.id} m={m} onJump={onJump} />)}
      {pages.map((p) => <PageCard key={p.item} project={project} page={p} />)}
    </div>
  );
}

function Meta({ p }: { p: Placed }) {
  const t = useT();
  const s = p.step;
  const kind = kindOf(s);
  return (
    <span className="flex min-w-0 items-center gap-1.5 text-[10.5px] text-faint">
      <span className={kindText[kind]}>{t(`timeline.kind.${kind}`)}</span>
      {p.end - p.start >= 1000 && <span>· {mmss((p.end - p.start) / 1000)}</span>}
      {kind === 'edit' && (s.added || s.removed) ? (
        <span>
          · <span className="text-emerald-400">+{s.added ?? 0}</span> <span className="text-rose-400">−{s.removed ?? 0}</span>
        </span>
      ) : null}
      {s.status === 'failed' && <span className="text-rose-400">· {t('timeline.failed')}</span>}
      {s.status === 'passed' && <span className="text-emerald-400">· {t('timeline.passed')}</span>}
      {s.status === 'running' && <span className="text-emerald-400">· {t('timeline.running')}</span>}
    </span>
  );
}

function Loading({ query }: { query: ReturnType<typeof useTimeline> }) {
  const t = useT();
  return <p className="p-4 text-[12px] text-subtle">{query.error ? t('timeline.failedToLoad') : query.isPending ? t('common.loading') : t('timeline.empty')}</p>;
}

// TimelinePanel is an agent's timeline down the right-hand panel: the
// player and the track pinned on top, the steps under them, each with what
// it made. The step under the playhead is lit, and kept in view as it plays.
export function TimelinePanel({ project, agentRef }: { project: string; agentRef: string }) {
  const query = useTimeline(agentRef);
  const tl = query.data;
  if (!tl || tl.steps.length === 0) return <Loading query={query} />;
  return <PanelBody key={agentRef} project={project} tl={tl} />;
}

function PanelBody({ project, tl }: { project: string; tl: T.Timeline }) {
  const t = useT();
  const title = useStepTitle();
  const track = useMemo(() => layout(tl), [tl]);
  const { x, setX, playing, play, onClock } = usePlayhead(track, firstLook(track));
  const active = placedAt(track, x)?.index ?? 0;
  const rows = useRef<(HTMLLIElement | null)[]>([]);
  useEffect(() => {
    if (playing) rows.current[active]?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }, [active, playing]);
  const jump = (ms: number) => setX(trackAt(track, ms));
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-timeline-panel>
      <div className="shrink-0 space-y-2 border-b border-line-faint p-3">
        {/* At most two fifths of the panel's height, so the steps keep room. */}
        <div className="mx-auto max-w-[calc(40vh*1.6)]">
          <Player track={track} x={x} playing={playing} onClock={onClock} />
        </div>
        <div className="flex items-center gap-2">
          <PlayButton playing={playing} onClick={() => play(!playing)} />
          <div className="min-w-0 flex-1">
            <Scrubber track={track} x={x} onX={setX} />
          </div>
        </div>
      </div>
      <ol className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
        {track.steps.map((p) => {
          const kind = kindOf(p.step);
          const Icon = kindIcon[kind];
          const on = p.index === active;
          return (
            <li key={p.index} ref={(el) => { rows.current[p.index] = el; }} className="relative flex gap-2.5 pb-2" data-timeline-step={kind}>
              {p.index < track.steps.length - 1 && <span className="absolute bottom-0 left-[13px] top-7 w-px bg-line" />}
              <button
                type="button"
                onClick={() => setX(p.from)}
                aria-label={title(p.step)}
                className={cn('relative z-10 flex size-7 shrink-0 items-center justify-center rounded-full ring-1', on ? `${kindTone[kind]} text-black ring-transparent` : 'bg-surface-raised text-subtle ring-line', p.step.status === 'failed' && !on && 'text-rose-400')}
              >
                <Icon className="size-3.5" />
              </button>
              <div className={cn('min-w-0 flex-1 rounded-lg px-2 py-1', on && 'bg-surface')}>
                <button type="button" onClick={() => setX(p.from)} className="block w-full text-left">
                  <span className="flex items-baseline gap-2">
                    <span className={cn('min-w-0 flex-1 truncate text-[12.5px]', on ? 'text-title' : 'text-secondary', kind === 'run' && 'font-mono text-[11.5px]')}>{title(p.step)}</span>
                    <span className="shrink-0 text-[10.5px] tabular-nums text-faint">{clockTime(p.start)}</span>
                  </span>
                  <Meta p={p} />
                </button>
                <StepMedia project={project} step={p.step} onJump={jump} className="mt-2" />
              </div>
            </li>
          );
        })}
        {tl.working && (
          <li className="flex items-center gap-2.5 pl-1.5 text-[11.5px] text-subtle">
            <span className="size-2 animate-pulse rounded-full bg-emerald-400" /> {t('timeline.working')}
          </li>
        )}
      </ol>
    </div>
  );
}

// firstLook is where a timeline opens: at its first recording or
// screenshot, which is what there is to see, or at its start.
function firstLook(track: Track): number {
  for (const p of track.steps) {
    const m = (p.step.media ?? []).find((m) => m.kind === 'recording' || m.kind === 'screenshot');
    if (m) return trackAt(track, new Date(m.createdAt).getTime());
  }
  return 0;
}

// TimelineWide is the review's Timeline: the player large, the step under
// the playhead beside it with what it made, the labelled track under them
// and every step as a card along the bottom.
export function TimelineWide({ project, agentRef }: { project: string; agentRef: string }) {
  const query = useTimeline(agentRef);
  const tl = query.data;
  if (!tl || tl.steps.length === 0) return <div className="flex flex-1 items-center justify-center"><Loading query={query} /></div>;
  return <WideBody key={agentRef} project={project} tl={tl} />;
}

function WideBody({ project, tl }: { project: string; tl: T.Timeline }) {
  const title = useStepTitle();
  const track = useMemo(() => layout(tl), [tl]);
  const { x, setX, playing, play, onClock } = usePlayhead(track, firstLook(track));
  const p = placedAt(track, x)!;
  const kind = kindOf(p.step);
  const Icon = kindIcon[kind];
  const strip = useRef<HTMLDivElement>(null);
  useEffect(() => {
    strip.current?.querySelector(`[data-timeline-card="${p.index}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' });
  }, [p.index]);
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-5 py-4" data-timeline-wide>
      <div className="flex min-h-[200px] flex-1 gap-4">
        {/* As tall as the room allows, and as wide as that makes it. */}
        <div className="flex min-h-0 min-w-0 flex-1 justify-center">
          <Player track={track} x={x} playing={playing} onClock={onClock} className="h-full w-auto max-w-full" />
        </div>
        <div className="flex min-h-0 w-[260px] shrink-0 flex-col gap-2 overflow-y-auto">
          <div className="flex items-center gap-2">
            <span className={cn('flex size-7 items-center justify-center rounded-full text-black', kindTone[kind])}>
              <Icon className="size-3.5" />
            </span>
            <span className="text-[11px] tabular-nums text-faint">{clockTime(p.start)}</span>
          </div>
          <p className={cn('text-[14px] font-medium text-title', kind === 'run' && 'break-all font-mono text-[12.5px]')}>{title(p.step)}</p>
          <Meta p={p} />
          {kind === 'task' && p.step.lines && p.step.lines.length > 1 && <p className="whitespace-pre-wrap text-[12px] text-secondary">{p.step.lines.join('\n')}</p>}
          {kind === 'edit' && p.step.paths && (
            <ul className="space-y-0.5 font-mono text-[11px] text-subtle">
              {p.step.paths.slice(0, 8).map((f) => <li key={f} className="truncate" title={f}>{f}</li>)}
            </ul>
          )}
          <StepMedia project={project} step={p.step} onJump={(ms) => setX(trackAt(track, ms))} />
        </div>
      </div>
      <div className="flex items-end gap-3">
        <PlayButton playing={playing} onClick={() => play(!playing)} className="mb-0.5 size-8" />
        <div className="min-w-0 flex-1">
          <Scrubber track={track} x={x} onX={setX} tall labels />
        </div>
      </div>
      <div ref={strip} className="flex shrink-0 gap-2 overflow-x-auto pb-1">
        {track.steps.map((q) => {
          const k = kindOf(q.step);
          const KIcon = kindIcon[k];
          const shot = (q.step.media ?? []).find((m) => m.kind === 'screenshot');
          return (
            <button
              key={q.index}
              type="button"
              data-timeline-card={q.index}
              onClick={() => setX(q.from)}
              className={cn('w-36 shrink-0 rounded-lg border p-1.5 text-left', q.index === p.index ? 'border-brand-400 bg-surface' : 'border-line bg-surface-faint hover:border-line-strong')}
            >
              <span className="relative block aspect-[16/10] overflow-hidden rounded bg-black">
                {shot ? <img src={mediaUrl(shot)} alt="" loading="lazy" className="size-full object-cover" /> : <StepText placed={q} />}
              </span>
              <span className="mt-1 flex items-center gap-1 text-[10.5px]">
                <KIcon className={cn('size-3 shrink-0', kindText[k])} />
                <span className="truncate text-secondary">{title(q.step)}</span>
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

export { Player, Scrubber, usePlayhead, StepMedia, PlayButton, firstLook };

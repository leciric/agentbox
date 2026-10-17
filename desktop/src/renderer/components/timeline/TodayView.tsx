// Today, a project's day as a story: every agent that worked on it since the
// day began, a chapter each, with its timeline as a strip and what it kept
// (recordings, screenshots, pages) beside it, oldest first. "Play the day"
// plays it full screen as a slideshow, a title card per agent and then its
// recordings played through and its screenshots and pages held a moment,
// which is most of a demo of the day's work.
import { useQueries, useQuery } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight, Clapperboard, GanttChart, Pause, Play, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import type * as T from '../../../shared/api';
import { LeadName } from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { mediaUrl } from '../../lib/media';
import { openPage } from '../../lib/pages';
import { pastAgentsKey } from '../../lib/pastAgents';
import { openPanelOn } from '../../lib/rightPanel';
import { type Chapter, dayStart, holdFor, kindOf, layout, mmss, type Slide, slidesOf, workedSince } from '../../lib/timeline';
import { cn } from '../../lib/utils';
import { TitleTile } from '../pages/PageThumb';
import { useProjectPages } from '../pages/usePages';
import { AIIcon } from '../state';
import { Button } from '../ui/button';
import { PanelButton } from '../v2/PanelButton';
import { InTopBar, ProjectCrumb } from '../v2/ProjectHeader';
import { useOpenReview, useProjectReview } from '../v2/ReviewV2';
import { kindIcon, kindTone, Scrubber, timelineKey, useStepTitle } from './Timeline';

// Who a chapter is about: a live agent or a past one, as far as Today needs.
type Who = { ref: string; name: string; title: string; ai: string; pr?: T.PullRequest };

const clockTime = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

// useToday is the project's chapters of the day: its agents, live and past,
// whose timelines have a step since the day began.
function useToday(project: string) {
  const since = useMemo(() => dayStart(), []);
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const past = useQuery({
    queryKey: [...pastAgentsKey, 'today', project, since],
    queryFn: () => api.archivedAgents({ project, since: new Date(since).toISOString() }),
  });
  // A live agent's pull request is the project's list's; a past one keeps its own.
  const pulls = useQuery({ queryKey: ['pulls', project], queryFn: () => api.projectPullRequests(project) });
  const prOf = (ref: string) => pulls.data?.pullRequests.find((pr) => pr.agent === ref.slice(ref.lastIndexOf('/') + 1));
  const who: Who[] = [
    ...(agents.data ?? []).filter((a) => a.project === project && a.name !== LeadName).map((a) => ({ ref: a.ref, name: a.name, title: a.title, ai: a.ai, pr: prOf(a.ref) })),
    ...(past.data ?? []).map((a) => ({ ref: a.ref, name: a.name, title: a.title || a.task, ai: a.ai, pr: a.pr })),
  ];
  const timelines = useQueries({
    queries: who.map((w) => ({ queryKey: timelineKey(w.ref), queryFn: () => api.timeline(w.ref), staleTime: 30_000 })),
  });
  const chapters: (Chapter & { who: Who })[] = [];
  who.forEach((w, i) => {
    const tl = timelines[i]?.data;
    if (tl && workedSince(tl, since)) chapters.push({ ref: w.ref, timeline: tl, who: w });
  });
  chapters.sort((a, b) => a.timeline.start.localeCompare(b.timeline.start));
  const loading = agents.isPending || past.isPending || timelines.some((q) => q.isPending);
  return { chapters, loading };
}

export function TodayView({ project, onSelect }: { project: string; onSelect: (view: View) => void }) {
  const t = useT();
  const { chapters, loading } = useToday(project);
  const slides = useMemo(() => slidesOf(chapters), [chapters]);
  const [playing, setPlaying] = useState<number | null>(null);
  const review = useProjectReview(project);
  const openReview = useOpenReview();
  const lead = `${project}/${LeadName}`;
  const media = chapters.flatMap((c) => c.timeline.steps.flatMap((s) => s.media ?? []));
  const count = (kind: string) => media.filter((m) => m.kind === kind).length;
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  return (
    <div className="min-h-0 flex-1 overflow-y-auto" data-today={project}>
      <InTopBar>{p && <ProjectCrumb project={p} then={t('today.title')} />}</InTopBar>
      <InTopBar end>
        <PanelButton project={project} chat={lead} />
      </InTopBar>
      <div className="mx-auto max-w-[920px] px-6 py-6">
        <header className="flex flex-wrap items-end gap-x-4 gap-y-3">
          <div className="min-w-[300px] flex-1">
            <p className="text-[11px] uppercase tracking-wider text-faint">
              {t('today.title')} · {new Date().toLocaleDateString([], { weekday: 'long', day: 'numeric', month: 'long' })}
            </p>
            <h1 className="mt-1 text-[22px] font-semibold text-title">{t('today.heading')}</h1>
            {chapters.length > 0 && (
              <p className="mt-1 text-[12.5px] text-subtle">
                {t('today.summary', { agents: chapters.length, prs: chapters.filter((c) => c.who.pr).length, shots: count('screenshot'), recordings: count('recording') })}
              </p>
            )}
          </div>
          {slides.length > 0 && (
            <Button variant="primary" onClick={() => setPlaying(0)} data-today-play>
              <Play />
              {t('today.play')}
            </Button>
          )}
        </header>

        {!loading && chapters.length === 0 && (
          <div className="mt-16 grid justify-items-center gap-2 text-center">
            <Clapperboard className="size-8 text-faint" />
            <p className="text-sm font-medium text-secondary">{t('today.empty')}</p>
            <p className="max-w-sm text-xs text-subtle">{t('today.emptyBody')}</p>
          </div>
        )}
        {loading && chapters.length === 0 && <p className="mt-8 text-[12px] text-subtle">{t('common.loading')}</p>}

        <div className="mt-6 space-y-4">
          {chapters.map((c) => {
            const first = slides.findIndex((s) => s.ref === c.ref);
            const ready = review.data?.items.find((i) => i.ref === c.ref);
            const kept = c.timeline.steps.flatMap((s) => [
              ...(s.media ?? []).filter((m) => m.kind === 'recording' || m.kind === 'screenshot').map((m) => ({ key: m.id, m, page: undefined })),
              ...(s.pages ?? []).map((p) => ({ key: p.item, m: undefined, page: p })),
            ]);
            return (
              <section key={c.ref} className="rounded-xl border border-line bg-surface-faint p-4" data-today-chapter={c.who.name}>
                <div className="flex items-center gap-2.5">
                  <AIIcon ai={c.who.ai || 'claude'} className="size-6 shrink-0" />
                  <div className="min-w-0 flex-1">
                    <button type="button" onClick={() => onSelect({ kind: 'agent', ref: c.ref })} className="block max-w-full truncate text-left text-[13.5px] font-medium text-title hover:underline">
                      {c.who.title || c.who.name}
                    </button>
                    <p className="truncate text-[11px] text-faint">
                      {c.who.name} · {clockTime(c.timeline.start)}–{clockTime(c.timeline.end)} · {t('today.steps', { count: c.timeline.steps.length })}
                      {c.who.pr && ` · #${c.who.pr.number}`}
                    </p>
                  </div>
                  {ready && openReview && (
                    <Button size="sm" variant="secondary" onClick={() => openReview({ item: ready })} data-today-review>
                      {t('today.review')}
                    </Button>
                  )}
                  <Button size="sm" variant="ghost" onClick={() => openPanelOn(lead, 'timeline', c.ref)} title={t('today.timeline')} aria-label={t('today.timeline')}>
                    <GanttChart />
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setPlaying(first)} title={t('today.playChapter')} aria-label={t('today.playChapter')}>
                    <Play />
                  </Button>
                </div>
                <div className="mt-3">
                  <ChapterStrip timeline={c.timeline} />
                </div>
                <div className="mt-3 flex gap-2.5 overflow-x-auto pb-1">
                  {kept.map(({ key, m, page }) => (
                    <Kept
                      key={key}
                      project={project}
                      media={m}
                      page={page}
                      onOpen={() => setPlaying(Math.max(first, slides.findIndex((s) => (s.kind === 'media' && s.media.id === m?.id) || (s.kind === 'page' && s.page.item === page?.item))))}
                    />
                  ))}
                  {kept.length === 0 && <p className="text-[11.5px] text-faint">{t('today.nothingKept')}</p>}
                </div>
              </section>
            );
          })}
        </div>
      </div>
      {playing !== null && slides.length > 0 && <Slideshow project={project} slides={slides} chapters={chapters} start={playing} onClose={() => setPlaying(null)} />}
    </div>
  );
}

// ChapterStrip is a chapter's track, to hover for what each step was.
function ChapterStrip({ timeline }: { timeline: T.Timeline }) {
  const track = useMemo(() => layout(timeline), [timeline]);
  const [x, setX] = useState(track.length);
  return <Scrubber track={track} x={x} onX={setX} />;
}

function Kept({ project, media, page, onOpen }: { project: string; media?: T.MediaItem; page?: T.TimelinePage; onOpen: () => void }) {
  return (
    <button type="button" onClick={onOpen} className="group w-44 shrink-0 text-left" data-today-kept={media?.kind ?? 'page'}>
      <span className="relative block aspect-[16/10] overflow-hidden rounded-md bg-black ring-1 ring-line group-hover:ring-brand-400">
        {media?.kind === 'recording' ? (
          <>
            <video src={`${mediaUrl(media)}#t=0.5`} muted preload="metadata" className="size-full object-cover" />
            <span className="absolute inset-0 flex items-center justify-center">
              <span className="flex size-8 items-center justify-center rounded-full bg-black/60 text-white group-hover:bg-brand-500">
                <Play className="size-4 translate-x-px" />
              </span>
            </span>
            {!!media.meta?.duration && <span className="absolute bottom-1 right-1 rounded bg-black/70 px-1 text-[10px] tabular-nums text-white">{mmss(media.meta.duration)}</span>}
          </>
        ) : media ? (
          <img src={mediaUrl(media)} alt="" loading="lazy" className="size-full object-cover" />
        ) : page ? (
          <PageTile project={project} page={page} />
        ) : null}
      </span>
      <span className="mt-1 block truncate text-[10.5px] text-subtle">{media?.name ?? page?.title}</span>
    </button>
  );
}

function PageTile({ project, page }: { project: string; page: T.TimelinePage }) {
  const pages = useProjectPages(project);
  const artifact = pages.data?.artifacts.find((a) => a.item === page.item || (page.id && a.id === page.id));
  return <TitleTile page={{ id: artifact?.id ?? page.item, title: page.title }} />;
}

// Slideshow plays the day full screen, with story-style progress along its
// top: ←/→ step, space pauses, Esc closes.
function Slideshow({ project, slides, chapters, start, onClose }: { project: string; slides: Slide[]; chapters: (Chapter & { who: Who })[]; start: number; onClose: () => void }) {
  const t = useT();
  const title = useStepTitle();
  const [i, setI] = useState(start);
  const [paused, setPaused] = useState(false);
  // How far through its slide the show is; a new slide starts from nothing.
  const [prog, setProg] = useState({ i: start, p: 0 });
  const p = prog.i === i ? prog.p : 0;
  // Where the slide had got to, for picking up after a pause.
  const at = useRef(p);
  at.current = p;
  const slide = slides[i]!;
  const chapter = chapters.find((c) => c.ref === slide.ref)!;
  const hold = holdFor(slide);
  const video = useRef<HTMLVideoElement>(null);
  const isVideo = slide.kind === 'media' && slide.media.kind === 'recording';
  const next = () => setI((k) => Math.min(slides.length - 1, k + 1));
  // A recording drives its own progress, and ends its slide when it ends
  // (or at the minute); the rest are timed.
  useEffect(() => {
    if (paused) {
      video.current?.pause();
      return;
    }
    if (isVideo) void video.current?.play().catch(() => {});
    let last = performance.now();
    let v = at.current;
    let raf = requestAnimationFrame(function tick(now) {
      v += (now - last) / 1000 / hold;
      last = now;
      if (v >= 1) {
        if (i + 1 < slides.length) setI(i + 1);
        else setProg({ i, p: 1 });
        return;
      }
      setProg({ i, p: v });
      raf = requestAnimationFrame(tick);
    });
    return () => cancelAnimationFrame(raf);
  }, [paused, hold, i, isVideo, slides.length]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
      else if (e.key === 'ArrowRight') next();
      else if (e.key === 'ArrowLeft') setI((k) => Math.max(0, k - 1));
      else if (e.key === ' ') setPaused((v) => !v);
      else return;
      e.preventDefault();
      e.stopPropagation();
    };
    addEventListener('keydown', key, true);
    return () => removeEventListener('keydown', key, true);
  });
  const pages = useProjectPages(project);
  const artifact = slide.kind === 'page' ? pages.data?.artifacts.find((a) => a.item === slide.page.item || (slide.page.id && a.id === slide.page.id)) : undefined;
  const steps = chapter.timeline.steps;
  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-black text-white" role="dialog" aria-modal aria-label={t('today.title')} data-today-slideshow={i}>
      <div className="flex gap-1 px-4 pt-3">
        {slides.map((s, k) => (
          <button key={k} type="button" onClick={() => setI(k)} className={cn('h-1 overflow-hidden rounded-full bg-white/20', s.kind === 'intro' ? 'w-3 shrink-0' : 'flex-1')} aria-label={t('today.position', { n: k + 1, count: slides.length })}>
            <span className="block h-full bg-white" style={{ width: k < i ? '100%' : k === i ? `${p * 100}%` : '0%' }} />
          </button>
        ))}
      </div>
      <div className="flex items-center gap-2.5 px-4 py-3">
        <AIIcon ai={chapter.who.ai || 'claude'} className="size-6 shrink-0" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-[13.5px] font-medium">{chapter.who.title || chapter.who.name}</p>
          <p className="text-[11px] text-white/55">{chapter.who.name}</p>
        </div>
        <button type="button" onClick={() => setPaused(!paused)} className="flex size-8 items-center justify-center rounded-full hover:bg-white/10" aria-label={paused ? t('today.resume') : t('today.pause')}>
          {paused ? <Play className="size-4" /> : <Pause className="size-4" />}
        </button>
        <button type="button" onClick={onClose} className="flex size-8 items-center justify-center rounded-full hover:bg-white/10" aria-label={t('today.close')}>
          <X className="size-4" />
        </button>
      </div>
      <div className="relative flex min-h-0 flex-1 items-center justify-center px-16">
        <button type="button" onClick={() => setI(Math.max(0, i - 1))} className="absolute left-4 flex size-10 items-center justify-center rounded-full bg-white/10 hover:bg-white/20" aria-label={t('today.previous')}>
          <ChevronLeft className="size-5" />
        </button>
        <div className="flex size-full max-w-[1200px] items-center justify-center">
          {slide.kind === 'intro' ? (
            <div className="text-center">
              <AIIcon ai={chapter.who.ai || 'claude'} className="mx-auto size-14" />
              <p className="mt-5 max-w-3xl text-[28px] font-semibold leading-tight">{chapter.who.title || chapter.who.name}</p>
              <p className="mt-3 text-[14px] text-white/60">
                {clockTime(chapter.timeline.start)}–{clockTime(chapter.timeline.end)} · {t('today.steps', { count: steps.length })}
                {chapter.who.pr && ` · #${chapter.who.pr.number}`}
              </p>
              <div className="mx-auto mt-6 flex max-w-xl flex-wrap justify-center gap-1">
                {steps.slice(0, 40).map((s, k) => {
                  const kind = kindOf(s);
                  const I = kindIcon[kind];
                  return (
                    <span key={k} className={cn('flex size-7 items-center justify-center rounded-full text-black', kindTone[kind], s.status === 'failed' && 'bg-rose-500')} title={title(s)}>
                      <I className="size-3.5" />
                    </span>
                  );
                })}
              </div>
            </div>
          ) : slide.kind === 'media' && slide.media.kind === 'recording' ? (
            <video key={slide.media.id} ref={video} src={mediaUrl(slide.media)} muted playsInline autoPlay className="max-h-full max-w-full rounded-xl" onEnded={next} />
          ) : slide.kind === 'media' ? (
            <img key={slide.media.id} src={mediaUrl(slide.media)} alt={slide.media.name} className="max-h-full max-w-full rounded-xl object-contain" />
          ) : (
            <button type="button" disabled={!artifact} onClick={() => artifact && openPage(project, artifact)} className="relative aspect-[16/10] w-full max-w-4xl overflow-hidden rounded-xl">
              <TitleTile page={{ id: artifact?.id ?? slide.page.item, title: slide.page.title }} />
            </button>
          )}
        </div>
        <button type="button" onClick={next} className="absolute right-4 flex size-10 items-center justify-center rounded-full bg-white/10 hover:bg-white/20" aria-label={t('today.next')}>
          <ChevronRight className="size-5" />
        </button>
      </div>
      <div className="px-4 pb-5 pt-3 text-center">
        {slide.kind !== 'intro' && (
          <>
            <p className="text-[15px]">{slide.kind === 'media' ? slide.media.name : slide.page.title}</p>
            <p className="mt-1 text-[11.5px] text-white/50">
              {title(slide.step)} · {clockTime(slide.step.start)}
            </p>
          </>
        )}
        <p className="mt-2 text-[10.5px] text-white/35">
          {t('today.position', { n: i + 1, count: slides.length })} · {t('today.keys')}
        </p>
      </div>
    </div>
  );
}

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronLeft, ChevronRight, ClipboardCheck, ExternalLink, GitBranch, Image as ImageIcon, X } from 'lucide-react';
import { useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { pagesFor } from '../lib/pages';
import { projectLabel } from '../lib/projectName';
import { countLabel, reviewKey, slidesOf, step, swipeStep, withoutAgent, type Slide } from '../lib/review';
import { useNow } from '../lib/useNow';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { Recording } from './MediaTab';
import { PageIcon } from './pages/PageThumb';
import { useProjectPages } from './pages/usePages';
import { PullRequestView } from './PullRequestModal';
import { usePullLabels } from './PullRequestsPanel';
import { AIIcon } from './state';
import { Button } from './ui/button';
import { Dialog, DialogContent, DialogTitle } from './ui/dialog';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

// ReadyToReview is the way in to "Ready to review": a button with how many
// agents finished and wait for the user to look at their work, opening the
// list of them, newest first. In a project's header it lists that project's;
// in the top bar, every project's. A row opens the agent's carousel.
export function ReadyToReview({ project, onSelect, compact = false }: { project?: string; onSelect: (view: View) => void; compact?: boolean }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [reviewing, setReviewing] = useState<T.ReviewItem | null>(null);
  // The list changes on the event stream (lib/events.ts): a finish, a pull
  // request re-read, a verdict. The poll is a backstop, and what notices an
  // agent that went back to work.
  const list = useQuery({ queryKey: reviewKey(project), queryFn: () => api.readyToReview(project), refetchInterval: 60_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const now = useNow(60_000);
  const items = list.data ?? [];
  const label = t('review.button');
  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label={items.length ? t('review.buttonCount', { count: items.length }) : label}
            title={label}
            data-ready-to-review={project ?? 'all'}
            className={cn(
              'relative flex shrink-0 items-center gap-1.5 rounded-full text-[12px] font-medium text-muted ring-1 ring-inset ring-line transition hover:bg-surface-raised hover:text-primary',
              compact ? 'size-8 justify-center' : 'h-8 px-3',
              open && 'bg-surface-raised text-primary',
            )}
          >
            <ClipboardCheck className="size-4 shrink-0" />
            {!compact && <span className="whitespace-nowrap">{label}</span>}
            {items.length > 0 &&
              (compact ? (
                <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-brand-500 px-1 text-[10px] font-semibold tabular-nums text-white ring-2 ring-[var(--color-ink)]">
                  {countLabel(items.length)}
                </span>
              ) : (
                <span className="rounded-full bg-brand-500 px-1.5 text-[10.5px] font-semibold tabular-nums leading-4 text-white">{countLabel(items.length)}</span>
              ))}
          </button>
        </PopoverTrigger>
        <PopoverContent className="w-[min(420px,calc(100vw-2rem))] p-0" data-review-list onOpenAutoFocus={(e) => e.preventDefault()}>
          <div className="flex items-center gap-2 border-b border-line px-4 py-3">
            <span className="text-[14px] font-medium text-title">{label}</span>
            {items.length > 0 && <span className="text-[12px] text-subtle">{t('review.waiting', { count: items.length })}</span>}
          </div>
          <div className="max-h-[min(560px,70vh)] overflow-y-auto p-1.5">
            {items.length === 0 ? (
              <div className="px-4 py-10 text-center text-[13px] text-muted">{list.isPending ? t('common.loading') : t('review.empty')}</div>
            ) : (
              items.map((item) => (
                <button
                  key={item.ref}
                  type="button"
                  data-review-row={item.ref}
                  className="flex w-full min-w-0 items-start gap-3 rounded-lg px-3 py-2.5 text-left transition hover:bg-surface-raised"
                  onClick={() => {
                    setOpen(false);
                    setReviewing(item);
                  }}
                >
                  <AIIcon ai={item.ai || 'claude'} className="mt-0.5 size-4 shrink-0" />
                  <span className="grid min-w-0 flex-1 gap-0.5">
                    <span className="flex min-w-0 items-baseline gap-2">
                      <span className={cn('truncate text-[13px] font-medium text-primary', !item.title && 'font-mono')}>{item.title || item.agent}</span>
                      <span className="ml-auto shrink-0 text-[11px] text-subtle">{timeAgo(item.finishedAt, now)}</span>
                    </span>
                    <span className="flex min-w-0 items-center gap-1.5 text-[11.5px] text-muted">
                      <span className="shrink-0 truncate">{projectLabel(item.project, projects.data)}</span>
                      {item.branch && (
                        <>
                          <span aria-hidden className="text-faint">
                            ·
                          </span>
                          <GitBranch className="size-3 shrink-0 text-faint" />
                          <span className="min-w-0 truncate font-mono text-[11px]">{item.branch}</span>
                        </>
                      )}
                      {item.pr && <span className="ml-auto shrink-0 font-mono text-[11px] text-secondary">#{item.pr.number}</span>}
                    </span>
                  </span>
                </button>
              ))
            )}
          </div>
        </PopoverContent>
      </Popover>
      <Dialog open={reviewing !== null} onOpenChange={(next) => !next && setReviewing(null)}>
        {reviewing && <ReviewCarousel key={reviewing.ref} item={reviewing} onDone={() => setReviewing(null)} onSelect={onSelect} />}
      </Dialog>
    </>
  );
}

// ReviewCarousel is everything one agent made, a slide at a time, in the
// order it made them: screenshots, recordings (they play right here) and
// Hatch pages, then its pull request. Arrow keys and a swipe move through
// them. Reviewed and Dismiss both take the agent off the list, until its
// work changes again.
function ReviewCarousel({ item, onDone, onSelect }: { item: T.ReviewItem; onDone: () => void; onSelect: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const media = useQuery({ queryKey: ['media', item.ref], queryFn: () => api.media(item.ref) });
  const pages = useProjectPages(item.project);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const slides = slidesOf(media.data ?? [], pagesFor(pages.data, item.ref), item.pr);
  const [index, setIndex] = useState(0);
  const at = Math.min(index, Math.max(0, slides.length - 1));
  const slide = slides[at];
  const go = (by: number) => setIndex(step(at, slides.length, by));
  const labels = usePullLabels(item.project);
  const swipe = useRef<{ x: number; y: number } | null>(null);

  const verdict = useMutation({
    mutationFn: (v: string) => api.review(item.ref, { verdict: v, fingerprint: item.fingerprint }),
    onMutate: () => {
      queryClient.setQueriesData<T.ReviewItem[]>({ queryKey: ['review'] }, (list) => withoutAgent(list, item.ref));
      onDone();
    },
    onSuccess: (_r, v) => toast(t(v === 'reviewed' ? 'review.marked' : 'review.dismissed', { agent: item.title || item.agent })),
    onError: (err) => toast.error(t('review.failed', { error: errorMessage(err) })),
    onSettled: () => void queryClient.invalidateQueries({ queryKey: ['review'] }),
  });

  const onKeyDown = (e: KeyboardEvent) => {
    // A video's own controls take the arrows to seek, and a field its text.
    if (e.target instanceof HTMLVideoElement || e.target instanceof HTMLInputElement) return;
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      go(e.key === 'ArrowRight' ? 1 : -1);
    }
  };
  const onPointerDown = (e: PointerEvent) => {
    swipe.current = { x: e.clientX, y: e.clientY };
  };
  const onPointerUp = (e: PointerEvent) => {
    if (!swipe.current) return;
    const by = swipeStep(e.clientX - swipe.current.x, e.clientY - swipe.current.y);
    swipe.current = null;
    if (by) go(by);
  };

  return (
    <DialogContent
      className="flex h-[88vh] w-[calc(100vw-3rem)] max-w-[1180px] flex-col gap-0 overflow-hidden p-0"
      data-review-carousel={item.ref}
      onKeyDown={onKeyDown}
      onOpenAutoFocus={(event) => {
        event.preventDefault();
        (event.currentTarget as HTMLElement | null)?.focus();
      }}
    >
      <div className="flex flex-wrap items-start gap-3 border-b border-line px-5 py-4 pr-14">
        <AIIcon ai={item.ai || 'claude'} className="mt-1 size-4 shrink-0" />
        <div className="grid min-w-0 flex-1 gap-1">
          <DialogTitle className={cn('truncate text-[15px] leading-snug', !item.title && 'font-mono')}>{item.title || item.agent}</DialogTitle>
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted">
            <span>{projectLabel(item.project, projects.data)}</span>
            {item.branch && (
              <span className="flex min-w-0 items-center gap-1">
                <GitBranch className="size-3 shrink-0 text-faint" />
                <span className="truncate font-mono text-[11.5px]">{item.branch}</span>
              </span>
            )}
            {item.pr && <span className="font-mono text-[11.5px] text-secondary">#{item.pr.number}</span>}
          </div>
          {item.summary && <p className="line-clamp-2 text-[12.5px] text-secondary">{item.summary}</p>}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button size="sm" variant="ghost" onClick={() => onSelect({ kind: 'agent', ref: item.ref })}>
            {t('review.openAgent')}
          </Button>
          <Button size="sm" variant="secondary" disabled={verdict.isPending} onClick={() => verdict.mutate('dismissed')} data-review-dismiss>
            <X />
            {t('review.dismiss')}
          </Button>
          <Button size="sm" variant="primary" disabled={verdict.isPending} onClick={() => verdict.mutate('reviewed')} data-review-done>
            <Check />
            {t('review.reviewed')}
          </Button>
        </div>
      </div>

      {/* The pull request is read and selected like the modal's, so it takes no swipe. */}
      <div
        className={cn('relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-surface-faint', slide?.kind !== 'pr' && 'touch-pan-y select-none')}
        onPointerDown={slide?.kind === 'pr' ? undefined : onPointerDown}
        onPointerUp={slide?.kind === 'pr' ? undefined : onPointerUp}
      >
        {slide?.kind === 'pr' ? (
          <div className="flex size-full flex-col bg-modal" data-review-slide="pr">
            <PullRequestView
              inline
              project={item.project}
              pr={slide.pr}
              canMerge={false}
              mergeRunning={false}
              onMerge={() => {}}
              edits={labels.edits}
              onToggleLabel={(label, on) => labels.toggleLabel(slide.pr, label, on)}
            />
          </div>
        ) : slide ? (
          <SlideView key={slide.id} slide={slide} project={item.project} />
        ) : (
          <div className="grid max-w-sm justify-items-center gap-2 p-10 text-center">
            <ImageIcon className="size-8 text-faint" />
            <div className="text-sm font-medium text-secondary">{media.isPending ? t('common.loading') : t('review.nothing')}</div>
            {!media.isPending && <div className="text-xs text-subtle">{t('review.nothingBody')}</div>}
          </div>
        )}
        {slides.length > 1 && (
          <>
            <CarouselArrow side="left" disabled={at === 0} label={t('review.previous')} onClick={() => go(-1)} />
            <CarouselArrow side="right" disabled={at === slides.length - 1} label={t('review.next')} onClick={() => go(1)} />
          </>
        )}
      </div>

      {slides.length > 0 && (
        <div className="flex items-center justify-center gap-3 border-t border-line px-5 py-2.5" data-review-position={`${at + 1}/${slides.length}`}>
          <div className="flex items-center gap-1.5">
            {slides.map((s, i) => (
              <button
                key={s.id}
                type="button"
                aria-label={t('review.goTo', { n: i + 1 })}
                className={cn('size-2 rounded-full transition', i === at ? 'bg-brand-400' : 'bg-line-heavy hover:bg-subtle')}
                onClick={() => setIndex(i)}
              />
            ))}
          </div>
          <span className="text-[11.5px] tabular-nums text-subtle">{t('review.position', { n: at + 1, count: slides.length })}</span>
        </div>
      )}

    </DialogContent>
  );
}

function CarouselArrow({ side, disabled, label, onClick }: { side: 'left' | 'right'; disabled: boolean; label: string; onClick: () => void }) {
  const Icon = side === 'left' ? ChevronLeft : ChevronRight;
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      onPointerDown={(e) => e.stopPropagation()}
      className={cn(
        'absolute top-1/2 flex size-10 -translate-y-1/2 items-center justify-center rounded-full border border-line-strong bg-modal/90 text-secondary shadow-lg transition hover:text-primary disabled:pointer-events-none disabled:opacity-0',
        side === 'left' ? 'left-4' : 'right-4',
      )}
    >
      <Icon className="size-5" />
    </button>
  );
}

function SlideView({ slide, project }: { slide: Exclude<Slide, { kind: 'pr' }>; project: string }) {
  const t = useT();
  const [loaded, setLoaded] = useState(false);
  switch (slide.kind) {
    case 'screenshot':
      return <img src={mediaUrl(slide.media)} alt={slide.media.name} draggable={false} className="max-h-full max-w-full object-contain" data-review-slide="screenshot" />;
    case 'recording':
      return (
        // Recording sizes its video for the Media viewer; here it fits the slide.
        <div className="flex size-full items-center justify-center bg-black [&_video]:max-h-full [&_video]:w-auto [&_video]:max-w-full" data-review-slide="recording">
          <Recording item={slide.media} />
        </div>
      );
    case 'page':
      return (
        <div className="flex size-full flex-col" data-review-slide="page">
          <div className="flex min-w-0 items-center gap-2 border-b border-line bg-modal px-4 py-2 text-[12.5px]">
            <PageIcon className="size-3.5 shrink-0 text-muted" />
            <span className="truncate font-medium text-primary">{slide.page.title}</span>
            {slide.page.url && (
              <Button size="sm" variant="ghost" className="ml-auto" onClick={() => void window.agentbox.openExternal(slide.page.url)}>
                <ExternalLink />
                {t('review.openPage')}
              </Button>
            )}
          </div>
          <div className="relative min-h-0 flex-1">
            {!loaded && <div className="skeleton absolute inset-0 rounded-none" />}
            <iframe
              title={slide.page.title}
              src={api.pageUrl(project, slide.page.id)}
              // As the page preview frames it: an opaque origin, no reach into the app.
              sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox allow-modals"
              referrerPolicy="no-referrer"
              onLoad={() => setLoaded(true)}
              className={cn('absolute inset-0 size-full border-0 bg-white', loaded ? 'opacity-100' : 'opacity-0')}
            />
          </div>
        </div>
      );
  }
}

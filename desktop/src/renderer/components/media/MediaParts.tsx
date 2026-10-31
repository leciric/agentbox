import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bot, Camera, Check, ChevronLeft, ChevronRight, CircleAlert, ExternalLink, FileChartColumn, FolderOpen, LoaderCircle, Play, Search, SquareCheck, Star, Trash, User, X } from 'lucide-react';
import { useEffect, useRef, useState, type ComponentType, type ReactNode, type RefObject } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { formatDateTime, useT } from '../../lib/i18n';
import { clock, kindInfo, mediaUrl } from '../../lib/media';
import { applyMediaEvent } from '../../lib/mediaPages';
import { cn, errorMessage, humanBytes, timeAgo, timeUntil } from '../../lib/utils';
import { cachedStill, Unplayable, unplayable, videoStill } from '../../lib/videoStills';
import { ConfirmDialog } from '../ConfirmDialog';
import { Badge, type BadgeVariant } from '../ui/badge';
import { Button } from '../ui/button';
import { Notice } from '../ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../ui/dialog';
import { Field, Input, Textarea } from '../ui/input';
import { Tip } from '../ui/tooltip';
import { ZoomableImage } from '../ZoomableImage';

const kindVariant: Record<string, BadgeVariant> = {
  screenshot: 'brand',
  recording: 'danger',
  report: 'info',
  log: 'default',
  note: 'warning',
  file: 'default',
};

// MoreMedia is the end of a gallery read a page at a time: it reads the next
// page as it nears the screen, with a small row while it's on its way.
export function MoreMedia({ pages }: { pages: { hasNextPage: boolean; isFetchingNextPage: boolean; fetchNextPage: () => Promise<unknown> } }) {
  const t = useT();
  const ref = useRef<HTMLDivElement>(null);
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = pages;
  useEffect(() => {
    const el = ref.current;
    if (!el || !hasNextPage || isFetchingNextPage) return;
    // Watched from the gallery's own scroller, so the margin reaches below
    // what it shows; a new observer after each page reads another while the
    // end is still near.
    const seen = new IntersectionObserver(([entry]) => entry?.isIntersecting && void fetchNextPage(), { root: scroller(el), rootMargin: '0px 0px 800px 0px' });
    seen.observe(el);
    return () => seen.disconnect();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);
  if (!hasNextPage) return null;
  return (
    <div ref={ref} data-media-more className="flex h-12 items-center justify-center gap-2 text-ui text-subtle">
      {isFetchingNextPage && (
        <>
          <LoaderCircle className="size-3.5 animate-spin" />
          {t('agent.mediaTab.loadingMore')}
        </>
      )}
    </div>
  );
}

// scroller is the nearest element that scrolls el, or none for the page.
function scroller(el: HTMLElement): HTMLElement | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    if (/(auto|scroll)/.test(getComputedStyle(p).overflowY)) return p;
  }
  return null;
}

// MediaSearch narrows a gallery by name, file name, type ("image", "video",
// "note") and a note's own words. Every word has to match; a "quoted phrase"
// matches whole. Escape clears it.
export function MediaSearch({ value, onChange, className }: { value: string; onChange: (value: string) => void; className?: string }) {
  const t = useT();
  return (
    <div className={cn('relative flex w-56 min-w-0 max-w-full items-center', className)}>
      <Search className="pointer-events-none absolute left-2.5 size-3.5 text-subtle" />
      <Input
        type="search"
        aria-label={t('agent.mediaTab.search')}
        placeholder={t('agent.mediaTab.searchPlaceholder')}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && value) {
            e.stopPropagation();
            onChange('');
          }
        }}
        className="h-7 min-w-0 pl-8 pr-7 text-ui [&::-webkit-search-cancel-button]:hidden"
      />
      {value && (
        <button aria-label={t('agent.mediaTab.clearSearch')} onClick={() => onChange('')} className="absolute right-1.5 flex size-5 items-center justify-center rounded-md text-subtle hover:bg-surface hover:text-primary">
          <X className="size-3.5" />
        </button>
      )}
    </div>
  );
}

// NoMatch is what a gallery shows when its filters leave nothing, with the
// search that did it, cut short rather than widening the column.
export function NoMatch({ query, onClear }: { query: string; onClear: () => void }) {
  const t = useT();
  return (
    <p className="flex min-w-0 items-center gap-2 px-1 text-ui text-subtle">
      <span className="min-w-0 truncate">{query.trim() ? t('agent.mediaTab.noMatchQuery', { query: query.trim() }) : t('agent.mediaTab.noMatch')}</span>
      {query.trim() && (
        <Button size="sm" variant="ghost" className="shrink-0" onClick={onClear}>
          {t('agent.mediaTab.clearSearch')}
        </Button>
      )}
    </p>
  );
}

// toggled ticks or unticks one item: a Set is replaced, never mutated, so
// React sees the change.
export function toggled(selected: ReadonlySet<string>, id: string): ReadonlySet<string> {
  const next = new Set(selected);
  if (!next.delete(id)) next.add(id);
  return next;
}

// MediaSelection is the Select mode both galleries share: the toolbar's
// buttons, and the confirmations for what's ticked and for everything the
// filters are showing. deleteMedia is the gallery's own bulk call, a whole
// project's or one agent's, and all is the request that empties it, so
// "Delete all" takes exactly what the filters were showing and nothing else.
export function MediaSelection({
  visible,
  selecting,
  onSelecting,
  selected,
  onSelected,
  all,
  allCount,
  allBytes,
  allLabel,
  deleteMedia,
  onDeleted,
}: {
  visible: T.MediaItem[];
  selecting: boolean;
  onSelecting: (on: boolean) => void;
  selected: ReadonlySet<string>;
  onSelected: (ids: ReadonlySet<string>) => void;
  all: T.DeleteMediaRequest;
  // How many items, and bytes, all takes: every one the filters keep, not
  // only those loaded.
  allCount: number;
  allBytes?: number;
  allLabel: string;
  deleteMedia: (req: T.DeleteMediaRequest) => Promise<T.DeleteMediaResult>;
  onDeleted: () => Promise<unknown> | void;
}) {
  const t = useT();
  const [confirming, setConfirming] = useState<'selected' | 'all' | null>(null);

  // A filter that hides a ticked item unticks it, so "Delete selected" can
  // only ever take what you can see.
  useEffect(() => {
    const ids = visible.filter((item) => selected.has(item.id)).map((item) => item.id);
    if (ids.length !== selected.size) onSelected(new Set(ids));
  }, [visible, selected, onSelected]);

  const chosen = visible.filter((item) => selected.has(item.id));
  const everything = confirming === 'all';
  const going = everything ? visible : chosen;
  const bytes = everything && allBytes !== undefined ? allBytes : going.reduce((n, item) => n + item.size, 0);
  const allTicked = visible.length > 0 && chosen.length === visible.length;

  const remove = async () => {
    const result = await deleteMedia(everything ? all : { ids: chosen.map((item) => item.id) });
    onSelected(new Set());
    onSelecting(false);
    await onDeleted();
    toast(t('agent.mediaTab.deleted', { count: result.deleted }), {
      description: result.bytes > 0 ? t('agent.mediaTab.freed', { size: humanBytes(result.bytes) }) : undefined,
    });
  };

  return (
    <>
      {selecting ? (
        <>
          <Button size="sm" variant="ghost" onClick={() => onSelected(new Set(allTicked ? [] : visible.map((item) => item.id)))}>
            {allTicked ? t('agent.mediaTab.clear') : t('agent.mediaTab.selectAll')}
          </Button>
          <Button size="sm" variant="danger" disabled={chosen.length === 0} onClick={() => setConfirming('selected')}>
            <Trash />
            {t('agent.mediaTab.deleteSelected', { count: chosen.length })}
          </Button>
          <Button size="sm" variant="danger" disabled={allCount === 0} onClick={() => setConfirming('all')}>
            {t('agent.mediaTab.deleteAll')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              onSelected(new Set());
              onSelecting(false);
            }}
          >
            {t('common.done')}
          </Button>
        </>
      ) : (
        <Button size="sm" variant="ghost" disabled={visible.length === 0} onClick={() => onSelecting(true)}>
          <SquareCheck />
          {t('agent.mediaTab.select')}
        </Button>
      )}
      <ConfirmDialog
        open={confirming !== null}
        onOpenChange={(open) => !open && setConfirming(null)}
        title={everything ? t('agent.mediaTab.deleteAllTitle', { what: allLabel }) : t('agent.mediaTab.deleteSomeTitle', { count: chosen.length })}
        description={bytes > 0 ? t('agent.mediaTab.goDescriptionSize', { size: humanBytes(bytes) }) : t('agent.mediaTab.goDescription')}
        confirmLabel={t('common.delete')}
        destructive
        onConfirm={remove}
      />
    </>
  );
}

export function FilterChip({
  icon: Icon,
  active,
  count,
  onClick,
  children,
}: {
  icon?: ComponentType<{ className?: string }>;
  active: boolean;
  count: number;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        'flex h-7 items-center gap-1.5 rounded-lg px-2.5 text-ui text-muted transition hover:bg-surface hover:text-primary',
        active && 'bg-surface-strong text-title shadow-[inset_0_1px_0_rgb(255_255_255/0.06)]',
      )}
    >
      {Icon && <Icon className="size-3.5" />}
      {children}
      <span className="tabular-nums text-subtle">{count}</span>
    </button>
  );
}

function useMediaText(item: T.MediaItem, enabled: boolean, limit?: number) {
  return useQuery({
    queryKey: ['media-text', item.id, limit ?? 'all'],
    queryFn: async () => {
      const res = await fetch(mediaUrl(item), limit ? { headers: { Range: `bytes=0-${limit}` } } : undefined);
      if (!res.ok) throw new Error(`couldn't read ${item.name}: HTTP ${res.status}`);
      return res.text();
    },
    enabled,
    staleTime: Infinity,
  });
}

export function MediaCard({
  item,
  onOpen,
  label,
  selecting,
  selected,
  onToggle,
}: {
  item: T.MediaItem;
  onOpen: () => void;
  label?: string;
  selecting?: boolean;
  selected?: boolean;
  onToggle?: () => void;
}) {
  const t = useT();
  const info = kindInfo(item.kind);
  // The star sits beside the card's button rather than in it, since a button
  // can't hold another; the wrapper lifts both on hover.
  return (
    <div className="group relative transition duration-200 hover:-translate-y-0.5">
      <button
        // In Select mode the card ticks itself instead of opening the viewer.
        onClick={selecting ? onToggle : onOpen}
        aria-pressed={selecting ? selected === true : undefined}
        data-media={item.kind}
        data-media-name={item.name}
        className={cn(
          'w-full overflow-hidden rounded-xl border border-line bg-surface-faint text-left transition duration-200 hover:border-line-vivid hover:bg-surface hover:shadow-[0_20px_44px_-24px_var(--ab-shadow-deep)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400',
          selecting && selected && 'border-brand-400/60 ring-2 ring-brand-400/40',
        )}
      >
        <div className="relative aspect-[16/10] overflow-hidden bg-well">
          <Thumbnail item={item} />
          {/* In Select mode the tick takes the kind badge's corner. The two sit
              in one row, so a long agent label shortens instead of covering the
              kind badge. */}
          <span className="absolute inset-x-2 top-2 flex items-start justify-between gap-2">
            <span className="shrink-0">
              {selecting ? (
                <Tick checked={selected === true} />
              ) : (
                <Badge variant={kindVariant[item.kind]} className="bg-black/60 backdrop-blur [:root[data-appearance=light]_&]:text-white">
                  <info.icon />
                  {info.one}
                </Badge>
              )}
            </span>
            {label && (
              <span className="flex min-w-0 justify-end" data-media-agent={item.agentName}>
                <Badge className="block min-w-0 truncate bg-black/70 text-white/90 backdrop-blur" title={label}>
                  {label}
                </Badge>
              </span>
            )}
          </span>
          {item.kind === 'recording' && item.meta.duration ? (
            <span className="absolute bottom-2 right-2 rounded-md bg-black/70 px-1.5 py-0.5 font-mono text-caption tabular-nums text-white">{clock(item.meta.duration)}</span>
          ) : null}
        </div>
        <div className="px-3 py-2.5">
          <div className="truncate text-ui font-medium text-primary">{item.name}</div>
          <div className="mt-1 flex items-center gap-1.5 text-caption text-subtle">
            {item.source === 'agent' ? <Bot className="size-3" /> : <User className="size-3" />}
            <span>{item.source === 'agent' ? t('agent.mediaTab.sourceAgent') : t('agent.mediaTab.sourceYou')}</span>
            <span>·</span>
            <span>{timeAgo(item.createdAt)}</span>
            {item.size > 0 && (
              <>
                <span>·</span>
                <span>{humanBytes(item.size)}</span>
              </>
            )}
          </div>
        </div>
      </button>
      {!selecting && <FavoriteButton item={item} onHover className="absolute bottom-1.5 right-1.5" />}
    </div>
  );
}

// FavoriteButton stars an item, which keeps it from ever being removed
// automatically (by the retention sweep, or with its agent), or unstars it,
// which puts it back under the retention from now. The daemon's media event
// updates the lists. onHover hides an unstarred one until its card is hovered.
export function FavoriteButton({ item, onHover, className }: { item: T.MediaItem; onHover?: boolean; className?: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const toggle = useMutation({
    mutationFn: () => api.setMediaFavorite(item.id, !item.favorite),
    // Its media event does the same, in every window.
    onSuccess: (updated) => applyMediaEvent(queryClient, updated),
    onError: (err) => toast.error(t('agent.mediaTab.favoriteFailed'), { description: errorMessage(err) }),
  });
  const label = item.favorite ? t('agent.mediaTab.unfavorite') : t('agent.mediaTab.favorite');
  return (
    <Tip label={label}>
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={label}
        aria-pressed={item.favorite === true}
        data-media-favorite={item.favorite || undefined}
        disabled={toggle.isPending}
        onClick={() => toggle.mutate()}
        className={cn(
          item.favorite ? 'text-amber-400 hover:text-amber-300' : 'text-subtle',
          onHover && !item.favorite && 'opacity-0 focus-visible:opacity-100 group-hover:opacity-100',
          className,
        )}
      >
        <Star className={cn(item.favorite && 'fill-current')} />
      </Button>
    </Tip>
  );
}

// Tick is a card's box in Select mode. It isn't a real checkbox: the card is
// already the button that toggles it, and says so with aria-pressed.
function Tick({ checked }: { checked: boolean }) {
  return (
    <span
      className={cn(
        'flex size-[18px] items-center justify-center rounded-md border backdrop-blur transition',
        checked ? 'border-brand-400 bg-brand-500 text-white' : 'border-line-heavy bg-black/60 text-transparent',
      )}
    >
      <Check className="size-3" />
    </span>
  );
}

function Thumbnail({ item }: { item: T.MediaItem }) {
  const ref = useRef<HTMLPreElement>(null);
  const near = useNear(ref, item.kind === 'log');
  const log = useMediaText(item, item.kind === 'log' && near, 4_096);
  switch (item.kind) {
    case 'screenshot':
      return /\.(png|jpe?g|gif|webp)$/i.test(item.file ?? '') ? (
        <img src={mediaUrl(item)} alt="" loading="lazy" decoding="async" className="size-full object-cover object-top transition duration-500 group-hover:scale-[1.03]" />
      ) : (
        <IconTile icon={Camera} />
      );
    case 'recording':
      return (
        <>
          <VideoStill item={item} />
          <span className="absolute inset-0 flex items-center justify-center">
            <span className="flex size-10 items-center justify-center rounded-full bg-black/60 ring-1 ring-line-heavy backdrop-blur transition group-hover:scale-110">
              <Play className="ml-0.5 size-4 text-white" />
            </span>
          </span>
        </>
      );
    case 'note':
      return <div className="line-clamp-6 size-full whitespace-pre-wrap bg-gradient-to-br from-amber-300/[0.07] to-transparent p-3 pt-10 text-ui leading-relaxed text-tertiary">{item.text}</div>;
    case 'log':
      return (
        <pre ref={ref} className="size-full overflow-hidden whitespace-pre p-3 pt-10 font-mono text-caption leading-snug text-subtle">
          {log.data ?? ''}
        </pre>
      );
    case 'report':
      return (
        <div className="flex size-full flex-col items-center justify-center gap-2 bg-gradient-to-br from-sky-400/[0.08] to-transparent">
          <FileChartColumn className="size-8 text-sky-300/80" />
          {item.meta.tests && <TestCounts tests={item.meta.tests} />}
        </div>
      );
    default:
      return <IconTile icon={kindInfo(item.kind).icon} />;
  }
}

// useNear is whether an element has come near the screen, once: what a
// card reads its file at, so a long gallery reads only what's scrolled to.
function useNear(ref: RefObject<HTMLElement | null>, enabled: boolean): boolean {
  const [near, setNear] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!enabled || near || !el) return;
    const seen = new IntersectionObserver(([entry]) => entry?.isIntersecting && setNear(true), { rootMargin: '400px' });
    seen.observe(el);
    return () => seen.disconnect();
  }, [ref, enabled, near]);
  return near;
}

// VideoStill is a recording's frame at 0.5 s, loaded once the tile is near the
// screen (lib/videoStills.ts says why it isn't a <video>).
function VideoStill({ item }: { item: T.MediaItem }) {
  const t = useT();
  const id = item.id;
  const ref = useRef<HTMLDivElement>(null);
  const [still, setStill] = useState(() => cachedStill(id));
  const [broken, setBroken] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (still || broken || !el) return;
    let loading: AbortController | undefined;
    const seen = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting && !loading) {
          const ctl = new AbortController();
          loading = ctl;
          videoStill(id, mediaUrl({ id }), ctl.signal).then(setStill, (err: unknown) => {
            if (err instanceof Unplayable) setBroken(true);
            if (loading === ctl) loading = undefined;
          });
        } else if (!entry?.isIntersecting && loading) {
          loading.abort();
          loading = undefined;
        }
      },
      { rootMargin: '400px' },
    );
    seen.observe(el);
    return () => {
      seen.disconnect();
      loading?.abort();
    };
  }, [id, still, broken]);
  if (broken) {
    return (
      <div className="flex size-full flex-col items-center justify-center gap-2 text-faint">
        <CircleAlert className="size-7" />
        <span className="text-xs">{t('agent.mediaTab.unplayable')}</span>
      </div>
    );
  }
  return <div ref={ref} className="size-full">{still && <img src={still} alt="" className="size-full object-cover" />}</div>;
}

function IconTile({ icon: Icon }: { icon: ComponentType<{ className?: string }> }) {
  return (
    <div className="flex size-full items-center justify-center">
      <Icon className="size-8 text-faint" />
    </div>
  );
}

function TestCounts({ tests }: { tests: T.TestCounts }) {
  const t = useT();
  return (
    <span className="flex gap-1">
      <Badge variant="success">{t('agent.mediaTab.passed', { count: tests.passed })}</Badge>
      {tests.failed > 0 && <Badge variant="danger">{t('agent.mediaTab.failed', { count: tests.failed })}</Badge>}
      {tests.skipped > 0 && <Badge>{t('agent.mediaTab.skipped', { count: tests.skipped })}</Badge>}
    </span>
  );
}

export function MediaViewer({
  items,
  index,
  onIndex,
  onClose,
  onDelete,
  context,
}: {
  items: T.MediaItem[];
  index: number;
  onIndex: (index: number) => void;
  onClose: () => void;
  onDelete: (item: T.MediaItem) => void;
  // context is a strip under the header saying where the item came from
  // (MediaPlace), for a viewer opened away from its agent's own Media tab.
  context?: (item: T.MediaItem) => ReactNode;
}) {
  const t = useT();
  const item = index >= 0 ? items[index] : undefined;

  useEffect(() => {
    if (!item) return;
    const onKey = (event: KeyboardEvent) => {
      if ((event.target as HTMLElement).tagName === 'VIDEO') return;
      if (event.key === 'ArrowLeft' && index > 0) onIndex(index - 1);
      if (event.key === 'ArrowRight' && index < items.length - 1) onIndex(index + 1);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [item, index, items.length, onIndex]);

  return (
    <Dialog open={item !== undefined} onOpenChange={(open) => !open && onClose()}>
      {/* No focus on a header button when it opens: its tooltip would open too, and take the first Escape. */}
      <DialogContent className="max-w-[min(1240px,calc(100vw-4rem))] gap-0 overflow-hidden p-0" onOpenAutoFocus={(event) => event.preventDefault()}>
        {item && (
          <>
            <div className="flex items-center gap-3 border-b border-line px-5 py-3 pr-14">
              <Badge variant={kindVariant[item.kind]}>{kindInfo(item.kind).one}</Badge>
              <DialogTitle className="truncate text-heading">{item.name}</DialogTitle>
              <span className="shrink-0 text-xs text-subtle">
                {item.source === 'agent' ? t('agent.mediaTab.fromAgent') : t('agent.mediaTab.fromYou')} · {formatDateTime(item.createdAt)}
                {item.agentGone &&
                  (item.expiresAt
                    ? t('agent.mediaTab.agentRemovedExpires', { time: timeUntil(item.expiresAt) })
                    : item.favorite
                      ? t('agent.mediaTab.agentRemovedFavorite')
                      : t('agent.mediaTab.agentRemoved'))}
              </span>
              <div className="ml-auto flex shrink-0 items-center gap-0.5">
                <Tip label={t('agent.mediaTab.previous')}>
                  <Button size="icon-sm" variant="ghost" aria-label={t('agent.mediaTab.previous')} disabled={index === 0} onClick={() => onIndex(index - 1)}>
                    <ChevronLeft />
                  </Button>
                </Tip>
                <Tip label={t('common.next')}>
                  <Button size="icon-sm" variant="ghost" aria-label={t('common.next')} disabled={index === items.length - 1} onClick={() => onIndex(index + 1)}>
                    <ChevronRight />
                  </Button>
                </Tip>
                {item.path && (
                  <>
                    <Tip label={t('agent.mediaTab.openDefault')}>
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={t('agent.mediaTab.openDefault')}
                        onClick={() => void window.agentbox.openPath(item.meta.entry ? `${item.path}/${item.meta.entry}` : item.path!)}
                      >
                        <ExternalLink />
                      </Button>
                    </Tip>
                    <Tip label={t('agent.mediaTab.showInFolder')}>
                      <Button size="icon-sm" variant="ghost" aria-label={t('agent.mediaTab.showInFolder')} onClick={() => void window.agentbox.showItem(item.path!)}>
                        <FolderOpen />
                      </Button>
                    </Tip>
                  </>
                )}
                <FavoriteButton item={item} />
                <Tip label={t('common.delete')}>
                  <Button size="icon-sm" variant="danger" aria-label={t('common.delete')} onClick={() => onDelete(item)}>
                    <Trash />
                  </Button>
                </Tip>
              </div>
            </div>
            {context?.(item)}
            <div className="flex max-h-[76vh] min-h-[46vh] items-center justify-center overflow-auto bg-well">
              <ViewerBody item={item} />
            </div>
            {(item.meta.url || item.meta.width || item.meta.tests) && (
              <div className="flex items-center gap-3 border-t border-line px-5 py-2 text-xs text-subtle">
                {item.meta.url && <span className="truncate font-mono">{item.meta.url}</span>}
                {item.meta.width ? (
                  <span className="shrink-0 tabular-nums">
                    {item.meta.width}×{item.meta.height}
                  </span>
                ) : null}
                {item.meta.tests && <TestCounts tests={item.meta.tests} />}
              </div>
            )}
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function ViewerBody({ item }: { item: T.MediaItem }) {
  const t = useT();
  const text = useMediaText(item, item.kind === 'log' || (item.kind === 'report' && !item.meta.entry) || item.kind === 'file', 1_000_000);
  switch (item.kind) {
    case 'screenshot':
      return <ZoomableImage key={item.id} src={mediaUrl(item)} alt={item.name} />;
    case 'recording':
      return <Recording key={item.id} item={item} />;
    case 'note':
      return <div className="w-full max-w-3xl self-start whitespace-pre-wrap p-8 text-heading leading-relaxed text-secondary">{item.text}</div>;
    case 'report':
      if (item.meta.entry) {
        return <iframe title={item.name} src={mediaUrl(item, item.meta.entry)} sandbox="allow-scripts allow-same-origin" className="h-[76vh] w-full bg-white" />;
      }
      break;
  }
  return (
    <pre className="w-full self-start whitespace-pre-wrap break-all p-5 font-mono text-caption leading-relaxed text-tertiary">
      {text.isPending ? t('common.loading') : text.error ? errorMessage(text.error) : text.data}
    </pre>
  );
}

// Recording plays an item in the viewer, or says it can't: a file cut short
// (a recording stopped before it was finished has no moov box) would
// otherwise be a player that never starts.
export function Recording({ item }: { item: T.MediaItem }) {
  const t = useT();
  const [broken, setBroken] = useState(false);
  if (broken) {
    return (
      <div className="flex flex-col items-center gap-2 p-10 text-center">
        <CircleAlert className="size-8 text-faint" />
        <div className="text-sm font-medium text-secondary">{t('agent.mediaTab.unplayable')}</div>
        <div className="max-w-sm text-xs text-subtle">{t('agent.mediaTab.unplayableBody')}</div>
      </div>
    );
  }
  return (
    <video
      src={mediaUrl(item)}
      controls
      autoPlay
      onError={(e) => unplayable(e.currentTarget.error) && setBroken(true)}
      className="max-h-[76vh] w-full bg-black"
    />
  );
}

// NoteDialog keeps a note with an agent's media.
export function NoteDialog({ agent, open, onOpenChange }: { agent: T.Agent; open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const [text, setText] = useState('');
  const [name, setName] = useState('');
  const add = useMutation({
    mutationFn: () => api.addNote(agent.ref, { text, name: name.trim() || undefined }),
    onSuccess: (item) => {
      toast(t('agent.mediaTab.saved', { name: item.name }));
      setText('');
      setName('');
      onOpenChange(false);
    },
  });
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('agent.mediaTab.addNote')}</DialogTitle>
          <DialogDescription>{t('agent.mediaTab.addNoteDescription', { name: agent.title || agent.name })}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            add.mutate();
          }}
        >
          <Field label={t('agent.mediaTab.noteTitle')} htmlFor="note-name" hint={t('agent.mediaTab.noteTitleHint')}>
            <Input id="note-name" value={name} onChange={(event) => setName(event.target.value)} />
          </Field>
          <Field label={t('agent.mediaTab.note')} htmlFor="note-text">
            <Textarea id="note-text" autoFocus rows={6} value={text} onChange={(event) => setText(event.target.value)} />
          </Field>
          {add.error && <Notice>{errorMessage(add.error)}</Notice>}
          <DialogFooter>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!text.trim() || add.isPending}>
              {add.isPending && <LoaderCircle className="animate-spin" />}
              {t('agent.mediaTab.saveNote')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

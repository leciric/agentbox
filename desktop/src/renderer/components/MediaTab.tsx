import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Bot,
  Camera,
  Check,
  ChevronLeft,
  ChevronRight,
  Circle,
  Download,
  ExternalLink,
  FileChartColumn,
  FolderOpen,
  Images,
  LoaderCircle,
  MousePointerClick,
  Play,
  SquareCheck,
  StickyNote,
  Trash,
  User,
} from 'lucide-react';
import { useEffect, useState, type ComponentType, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { clock, describeAll, kindInfo, mediaKinds, mediaUrl } from '../lib/media';
import { cn, errorMessage, humanBytes, timeAgo, timeUntil } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge, type BadgeVariant } from './ui/badge';
import { Button } from './ui/button';
import { EmptyState, Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';
import { Tip } from './ui/tooltip';

const kindVariant: Record<string, BadgeVariant> = {
  screenshot: 'brand',
  recording: 'danger',
  report: 'info',
  log: 'default',
  note: 'warning',
  file: 'default',
};

// MediaTab is the gallery of what shows an agent's work: screenshots,
// recordings, reports, logs and notes, from the agent and from you.
export function MediaTab({ agent }: { agent: T.Agent }) {
  const queryClient = useQueryClient();
  const running = agent.state === 'running';
  const media = useQuery({ queryKey: ['media', agent.ref], queryFn: () => api.media(agent.ref) });
  const recording = useQuery({
    queryKey: ['recording', agent.ref],
    queryFn: () => api.recording(agent.ref),
    enabled: running,
    refetchInterval: (query) => (query.state.data?.recording ? 2_000 : 10_000),
  });
  const [filter, setFilter] = useState('all');
  const [openId, setOpenId] = useState<string | null>(null);
  const [noting, setNoting] = useState(false);
  const [deleting, setDeleting] = useState<T.MediaItem | null>(null);
  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());

  const saved = (item: T.MediaItem) => toast(`Saved “${item.name}”`);
  const shot = useMutation({ mutationFn: () => api.screenshot(agent.ref), onSuccess: saved });
  const isRecording = recording.data?.recording === true;
  const record = useMutation({
    mutationFn: (input?: string): Promise<T.MediaItem | T.RecordingStatus> =>
      isRecording ? api.stopRecording(agent.ref) : api.startRecording(agent.ref, { name: 'screen recording', input }),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ['recording', agent.ref] });
      if ('id' in result) saved(result);
    },
  });
  const exportAll = useMutation({
    mutationFn: () => api.exportMedia(agent.ref),
    onSuccess: (result) =>
      toast(`Exported ${result.items} item${result.items === 1 ? '' : 's'}`, {
        description: result.dir,
        action: { label: 'Open folder', onClick: () => void window.agentbox.openPath(result.dir) },
      }),
  });

  const items = media.data ?? [];
  const counts = new Map<string, number>();
  let total = 0; // what this agent's media takes on disk, since freeing that is why you delete it
  for (const item of items) {
    counts.set(item.kind, (counts.get(item.kind) ?? 0) + 1);
    total += item.size;
  }
  const kind = filter === 'all' ? '' : filter;
  const visible = kind === '' ? items : items.filter((item) => item.kind === kind);
  const index = visible.findIndex((item) => item.id === openId);
  const error = shot.error ?? record.error ?? exportAll.error ?? media.error;
  const refresh = async () => {
    setOpenId(null);
    await queryClient.invalidateQueries({ queryKey: ['media', agent.ref] });
  };

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
        <div className="flex flex-wrap items-center gap-1" role="group" aria-label="Filter media">
          <FilterChip active={filter === 'all'} count={items.length} onClick={() => setFilter('all')}>
            All
          </FilterChip>
          {mediaKinds
            .filter((k) => counts.get(k.kind))
            .map((k) => (
              <FilterChip key={k.kind} icon={k.icon} active={filter === k.kind} count={counts.get(k.kind) ?? 0} onClick={() => setFilter(k.kind)}>
                {k.label}
              </FilterChip>
            ))}
          {total > 0 && <span className="pl-1.5 text-[12px] tabular-nums text-subtle">{humanBytes(total)}</span>}
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          {!selecting && (
            <>
              <Button size="sm" disabled={!running || shot.isPending} onClick={() => shot.mutate()}>
                {shot.isPending ? <LoaderCircle className="animate-spin" /> : <Camera />}
                Screenshot
              </Button>
              {isRecording ? (
                <Button
                  size="sm"
                  className="border-rose-400/30 bg-rose-500/15 text-rose-100 hover:bg-rose-500/25"
                  disabled={record.isPending}
                  onClick={() => record.mutate()}
                >
                  {record.isPending ? <LoaderCircle className="animate-spin" /> : <span className="size-2 animate-pulse rounded-[3px] bg-rose-400" />}
                  Stop recording{recording.data?.input === 'desktop' ? ' with the keys and mouse' : ''}
                </Button>
              ) : (
                <Menu>
                  <MenuTrigger asChild>
                    <Button size="sm" disabled={!running || record.isPending}>
                      {record.isPending ? <LoaderCircle className="animate-spin" /> : <span className="size-2.5 rounded-full bg-rose-500" />}
                      Record
                    </Button>
                  </MenuTrigger>
                  <MenuContent>
                    <MenuItem icon={Circle} onSelect={() => record.mutate()}>
                      Record the screen
                    </MenuItem>
                    <MenuItem icon={MousePointerClick} onSelect={() => record.mutate('desktop')}>
                      Record with keys and mouse
                    </MenuItem>
                  </MenuContent>
                </Menu>
              )}
              <Button size="sm" onClick={() => setNoting(true)}>
                <StickyNote />
                Note
              </Button>
              <Tip label="Copy everything into a folder with a README.md, for a pull request">
                <Button size="sm" variant="ghost" disabled={items.length === 0 || exportAll.isPending} onClick={() => exportAll.mutate()}>
                  {exportAll.isPending ? <LoaderCircle className="animate-spin" /> : <Download />}
                  Export
                </Button>
              </Tip>
            </>
          )}
          <MediaSelection
            visible={visible}
            selecting={selecting}
            onSelecting={setSelecting}
            selected={selected}
            onSelected={setSelected}
            all={{ all: true, kind: kind || undefined }}
            allLabel={describeAll(visible.length, kind, agent.title || agent.name)}
            deleteMedia={(req) => api.deleteAgentMedia(agent.ref, req)}
            onDeleted={refresh}
          />
        </div>
      </div>
      {error && (
        <div className="px-4 pt-3">
          <Notice>{errorMessage(error)}</Notice>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {media.isPending ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3">
            {Array.from({ length: 6 }, (_, i) => (
              <div key={i} className="skeleton aspect-[4/3] rounded-xl" />
            ))}
          </div>
        ) : items.length === 0 ? (
          <EmptyState
            icon={Images}
            title="Nothing kept yet"
            action={
              <>
                <Button variant="primary" disabled={!running || shot.isPending} onClick={() => shot.mutate()}>
                  <Camera />
                  Take a screenshot
                </Button>
                <Button disabled={!running || record.isPending} onClick={() => record.mutate()}>
                  <span className="size-2.5 rounded-full bg-rose-500" />
                  Record the screen
                </Button>
              </>
            }
          >
            <p>Screenshots, recordings, test reports, logs and notes that show {agent.title || agent.name}'s work end up here. Agents add them themselves:</p>
            <pre className="mt-4 rounded-xl border border-line bg-well p-3.5 text-left font-mono text-[12px] leading-relaxed text-muted">
              <span className="text-faint">$ </span>agentbox media screenshot --name checkout{'\n'}
              <span className="text-faint">$ </span>agentbox media record start --name demo{'\n'}
              <span className="text-faint">$ </span>agentbox media add playwright-report/
            </pre>
          </EmptyState>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3">
            {visible.map((item) => (
              <MediaCard
                key={item.id}
                item={item}
                onOpen={() => setOpenId(item.id)}
                selecting={selecting}
                selected={selected.has(item.id)}
                onToggle={() => setSelected(toggled(selected, item.id))}
              />
            ))}
          </div>
        )}
      </div>

      <MediaViewer
        items={visible}
        index={index}
        onIndex={(i) => setOpenId(visible[i]?.id ?? null)}
        onClose={() => setOpenId(null)}
        onDelete={(item) => setDeleting(item)}
      />
      <NoteDialog agent={agent} open={noting} onOpenChange={setNoting} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={`Delete “${deleting?.name}”?`}
        description="It's removed from this agent's media for good."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          await api.deleteMedia(deleting!.id);
          await refresh();
        }}
      />
    </div>
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
  allLabel: string;
  deleteMedia: (req: T.DeleteMediaRequest) => Promise<T.DeleteMediaResult>;
  onDeleted: () => Promise<unknown> | void;
}) {
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
  const bytes = going.reduce((n, item) => n + item.size, 0);
  const allTicked = visible.length > 0 && chosen.length === visible.length;

  const remove = async () => {
    const result = await deleteMedia(everything ? all : { ids: chosen.map((item) => item.id) });
    onSelected(new Set());
    onSelecting(false);
    await onDeleted();
    toast(`Deleted ${result.deleted} item${result.deleted === 1 ? '' : 's'}`, {
      description: result.bytes > 0 ? `${humanBytes(result.bytes)} freed` : undefined,
    });
  };

  return (
    <>
      {selecting ? (
        <>
          <Button size="sm" variant="ghost" onClick={() => onSelected(new Set(allTicked ? [] : visible.map((item) => item.id)))}>
            {allTicked ? 'Clear' : 'Select all'}
          </Button>
          <Button size="sm" variant="danger" disabled={chosen.length === 0} onClick={() => setConfirming('selected')}>
            <Trash />
            Delete selected ({chosen.length})
          </Button>
          <Button size="sm" variant="danger" disabled={visible.length === 0} onClick={() => setConfirming('all')}>
            Delete all
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              onSelected(new Set());
              onSelecting(false);
            }}
          >
            Done
          </Button>
        </>
      ) : (
        <Button size="sm" variant="ghost" disabled={visible.length === 0} onClick={() => onSelecting(true)}>
          <SquareCheck />
          Select
        </Button>
      )}
      <ConfirmDialog
        open={confirming !== null}
        onOpenChange={(open) => !open && setConfirming(null)}
        title={everything ? `Delete ${allLabel}?` : `Delete ${going.length} item${going.length === 1 ? '' : 's'}?`}
        description={`${bytes > 0 ? `${humanBytes(bytes)} freed. ` : ''}They go for good, files and all.`}
        confirmLabel="Delete"
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
        'flex h-7 items-center gap-1.5 rounded-lg px-2.5 text-[12.5px] text-muted transition hover:bg-surface hover:text-primary',
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
  const info = kindInfo(item.kind);
  return (
    <button
      // In Select mode the card ticks itself instead of opening the viewer.
      onClick={selecting ? onToggle : onOpen}
      aria-pressed={selecting ? selected === true : undefined}
      data-media={item.kind}
      data-media-name={item.name}
      className={cn(
        'group overflow-hidden rounded-xl border border-line bg-surface-faint text-left transition duration-200 hover:-translate-y-0.5 hover:border-line-vivid hover:bg-surface hover:shadow-[0_20px_44px_-24px_var(--ab-shadow-deep)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50',
        selecting && selected && 'border-brand-400/60 ring-2 ring-brand-400/40',
      )}
    >
      <div className="relative aspect-[16/10] overflow-hidden bg-well">
        <Thumbnail item={item} />
        {/* In Select mode the tick takes the kind badge's corner, so it never
            collides with the agent's label on the other side. */}
        <span className="absolute left-2 top-2">
          {selecting ? (
            <Tick checked={selected === true} />
          ) : (
            <Badge variant={kindVariant[item.kind]} className="bg-black/60 backdrop-blur">
              <info.icon />
              {info.one}
            </Badge>
          )}
        </span>
        {label && (
          <span className="absolute right-2 top-2 max-w-[70%]" data-media-agent={item.agentName}>
            <Badge className="truncate bg-black/70 backdrop-blur" title={label}>
              {label}
            </Badge>
          </span>
        )}
        {item.kind === 'recording' && item.meta.duration ? (
          <span className="absolute bottom-2 right-2 rounded-md bg-black/70 px-1.5 py-0.5 font-mono text-[10.5px] tabular-nums text-primary">{clock(item.meta.duration)}</span>
        ) : null}
      </div>
      <div className="px-3 py-2.5">
        <div className="truncate text-[13px] font-medium text-primary">{item.name}</div>
        <div className="mt-1 flex items-center gap-1.5 text-[11px] text-subtle">
          {item.source === 'agent' ? <Bot className="size-3" /> : <User className="size-3" />}
          <span>{item.source === 'agent' ? 'Agent' : 'You'}</span>
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
  const log = useMediaText(item, item.kind === 'log', 4_096);
  switch (item.kind) {
    case 'screenshot':
      return /\.(png|jpe?g|gif|webp)$/i.test(item.file ?? '') ? (
        <img src={mediaUrl(item)} alt="" loading="lazy" className="size-full object-cover object-top transition duration-500 group-hover:scale-[1.03]" />
      ) : (
        <IconTile icon={Camera} />
      );
    case 'recording':
      return (
        <>
          <video src={`${mediaUrl(item)}#t=0.5`} preload="metadata" muted className="size-full object-cover" />
          <span className="absolute inset-0 flex items-center justify-center">
            <span className="flex size-10 items-center justify-center rounded-full bg-black/60 ring-1 ring-line-heavy backdrop-blur transition group-hover:scale-110">
              <Play className="ml-0.5 size-4 text-white" />
            </span>
          </span>
        </>
      );
    case 'note':
      return <div className="line-clamp-6 size-full whitespace-pre-wrap bg-gradient-to-br from-amber-300/[0.07] to-transparent p-3 pt-10 text-[12.5px] leading-relaxed text-tertiary">{item.text}</div>;
    case 'log':
      return <pre className="size-full overflow-hidden whitespace-pre p-3 pt-10 font-mono text-[10.5px] leading-snug text-subtle">{log.data ?? ''}</pre>;
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

function IconTile({ icon: Icon }: { icon: ComponentType<{ className?: string }> }) {
  return (
    <div className="flex size-full items-center justify-center">
      <Icon className="size-8 text-faint" />
    </div>
  );
}

function TestCounts({ tests }: { tests: T.TestCounts }) {
  return (
    <span className="flex gap-1">
      <Badge variant="success">{tests.passed} passed</Badge>
      {tests.failed > 0 && <Badge variant="danger">{tests.failed} failed</Badge>}
      {tests.skipped > 0 && <Badge>{tests.skipped} skipped</Badge>}
    </span>
  );
}

export function MediaViewer({
  items,
  index,
  onIndex,
  onClose,
  onDelete,
}: {
  items: T.MediaItem[];
  index: number;
  onIndex: (index: number) => void;
  onClose: () => void;
  onDelete: (item: T.MediaItem) => void;
}) {
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
              <DialogTitle className="truncate text-[15px]">{item.name}</DialogTitle>
              <span className="shrink-0 text-xs text-subtle">
                {item.source === 'agent' ? 'from the agent' : 'from you'} · {new Date(item.createdAt).toLocaleString()}
                {item.agentGone && <> · agent removed{item.expiresAt && <>, expires in {timeUntil(item.expiresAt)}</>}</>}
              </span>
              <div className="ml-auto flex shrink-0 items-center gap-0.5">
                <Tip label="Previous">
                  <Button size="icon-sm" variant="ghost" aria-label="Previous" disabled={index === 0} onClick={() => onIndex(index - 1)}>
                    <ChevronLeft />
                  </Button>
                </Tip>
                <Tip label="Next">
                  <Button size="icon-sm" variant="ghost" aria-label="Next" disabled={index === items.length - 1} onClick={() => onIndex(index + 1)}>
                    <ChevronRight />
                  </Button>
                </Tip>
                {item.path && (
                  <>
                    <Tip label="Open with your default app">
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label="Open with your default app"
                        onClick={() => void window.agentbox.openPath(item.meta.entry ? `${item.path}/${item.meta.entry}` : item.path!)}
                      >
                        <ExternalLink />
                      </Button>
                    </Tip>
                    <Tip label="Show in folder">
                      <Button size="icon-sm" variant="ghost" aria-label="Show in folder" onClick={() => void window.agentbox.showItem(item.path!)}>
                        <FolderOpen />
                      </Button>
                    </Tip>
                  </>
                )}
                <Tip label="Delete">
                  <Button size="icon-sm" variant="danger" aria-label="Delete" onClick={() => onDelete(item)}>
                    <Trash />
                  </Button>
                </Tip>
              </div>
            </div>
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
  const text = useMediaText(item, item.kind === 'log' || (item.kind === 'report' && !item.meta.entry) || item.kind === 'file', 1_000_000);
  switch (item.kind) {
    case 'screenshot':
      return <img src={mediaUrl(item)} alt={item.name} className="max-h-[76vh] w-auto max-w-full object-contain" />;
    case 'recording':
      return <video key={item.id} src={mediaUrl(item)} controls autoPlay className="max-h-[76vh] w-full bg-black" />;
    case 'note':
      return <div className="w-full max-w-3xl self-start whitespace-pre-wrap p-8 text-[15px] leading-relaxed text-secondary">{item.text}</div>;
    case 'report':
      if (item.meta.entry) {
        return <iframe title={item.name} src={mediaUrl(item, item.meta.entry)} sandbox="allow-scripts allow-same-origin" className="h-[76vh] w-full bg-white" />;
      }
      break;
  }
  return (
    <pre className="w-full self-start whitespace-pre-wrap break-all p-5 font-mono text-[12px] leading-relaxed text-tertiary">
      {text.isPending ? 'Loading…' : text.error ? errorMessage(text.error) : text.data}
    </pre>
  );
}

function NoteDialog({ agent, open, onOpenChange }: { agent: T.Agent; open: boolean; onOpenChange: (open: boolean) => void }) {
  const [text, setText] = useState('');
  const [name, setName] = useState('');
  const add = useMutation({
    mutationFn: () => api.addNote(agent.ref, { text, name: name.trim() || undefined }),
    onSuccess: (item) => {
      toast(`Saved “${item.name}”`);
      setText('');
      setName('');
      onOpenChange(false);
    },
  });
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add a note</DialogTitle>
          <DialogDescription>Keep a note with {agent.title || agent.name}'s media, like what you checked or what's left to do.</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            add.mutate();
          }}
        >
          <Field label="Title" htmlFor="note-name" hint="Optional. Defaults to the first line.">
            <Input id="note-name" value={name} onChange={(event) => setName(event.target.value)} />
          </Field>
          <Field label="Note" htmlFor="note-text">
            <Textarea id="note-text" autoFocus rows={6} value={text} onChange={(event) => setText(event.target.value)} />
          </Field>
          {add.error && <Notice>{errorMessage(add.error)}</Notice>}
          <DialogFooter>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!text.trim() || add.isPending}>
              {add.isPending && <LoaderCircle className="animate-spin" />}
              Save note
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

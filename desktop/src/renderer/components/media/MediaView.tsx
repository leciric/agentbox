import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Camera, Check, Circle, Download, Images, LoaderCircle, MessagesSquare, MousePointerClick, Star, StickyNote } from 'lucide-react';
import { useDeferredValue, useEffect, useMemo, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { describeAll, mediaKinds } from '../../lib/media';
import {
  atHome,
  interleave,
  noFilter,
  pageKind,
  pagesIn,
  peekMediaFilter,
  scopesOf,
  showsMedia,
  takeMediaFilter,
  useMediaFilterRequests,
  withScope,
  type MediaFilter,
  type MediaHome,
  type MediaScopeName,
} from '../../lib/mediaFilter';
import { loadedItems, useMediaCounts, useMediaPages, useNextPageNear } from '../../lib/mediaPages';
import { byDay, useMarkSeen, useSeeMedia } from '../../lib/notifications';
import { isNew, markSeen as markPagesSeen, openPage } from '../../lib/pages';
import { projectLabel } from '../../lib/projectName';
import { useNow } from '../../lib/useNow';
import { cn, errorMessage, humanBytes, timeAgo } from '../../lib/utils';
import { ConfirmDialog } from '../ConfirmDialog';
import { MediaPlace } from '../MediaPlace';
import { PageIcon, PageThumb, useAgentLabel } from '../pages/PageThumb';
import { usePagesOf } from '../pages/usePages';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
import { EmptyState, Notice } from '../ui/card';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../ui/menu';
import { Select, SelectOption } from '../ui/select';
import { Tip } from '../ui/tooltip';
import { FilterChip, MediaCard, MediaSearch, MediaSelection, MediaViewer, MoreMedia, NoMatch, NoteDialog, toggled } from './MediaParts';

// MediaView is the one place media is shown: what agents kept to show their
// work (screenshots, recordings, reports, logs, notes, files) and the pages
// they published on Hatch, newest first, by day. Its filters say whose (this
// agent, this project, everything) and which kind. Where it was opened is its
// home: an agent's Media tab (agent), a project's (project), the sidebar's
// Media (neither). Links open it on a filter of their own with
// requestMediaFilter (lib/mediaFilter.ts), as the rail's Pages does.
export function MediaView({ agent, project, onSelect, heading, onAskLead }: { agent?: T.Agent; project?: string; onSelect?: (view: View) => void; heading?: boolean; onAskLead?: () => void }) {
  const t = useT();
  const home: MediaHome = agent ? { project: agent.project, agent: agent.name } : { project };
  const scopes = scopesOf(home);
  const start = () => withScope(noFilter, scopes[0], home);
  // A link's filter is read as the view mounts, and taken once it has.
  const [filter, setFilter] = useState<MediaFilter>(() => ({ ...start(), ...peekMediaFilter() }));
  const requests = useMediaFilterRequests();
  useEffect(() => {
    const req = takeMediaFilter();
    if (req) setFilter({ ...start(), ...req });
    // start is home's, which the view is keyed by.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [requests]);
  const set = (patch: Partial<MediaFilter>) => setFilter((f) => ({ ...f, ...patch }));
  const scope = activeScope(filter, home);
  const own = !!agent && scope === 'agent';

  const [query, setQuery] = useState('');
  const search = useDeferredValue(query);
  const [openId, setOpenIdState] = useState<string | null>(null);
  const seeMedia = useSeeMedia();
  const setOpenId = (id: string | null) => {
    setOpenIdState(id);
    seeMedia(id);
  };
  const [deleting, setDeleting] = useState<T.MediaItem | null>(null);
  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const markSeen = useMarkSeen();
  const now = useNow(60_000);

  // The daemon filters and searches the media, a page at a time; its counts
  // are of every item, not the pages loaded. Hatch's pages come whole.
  const withMedia = showsMedia(filter);
  const only = withMedia ? filter.kind : '';
  const media = useMediaPages({ project: filter.project, agent: filter.agent, only, q: search, unseen: filter.unseen, favorite: filter.favorite }, withMedia);
  const counts = useMediaCounts({ project: filter.project, agent: filter.agent, only, q: search });
  const everything = useMediaCounts({});
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const projectName = (name: string) => projectLabel(name, projects.data);
  const agentLabel = useAgentLabel();
  const allPages = usePagesOf(filter.project);
  const loaded = useMemo(() => (withMedia ? loadedItems(media.data) : []), [withMedia, media.data]);
  // The open item stays in an Unseen list once opening it marks it seen, and
  // in a Favorites list once it's unstarred, so the viewer doesn't close on it.
  const items = loaded.filter((m) => (!filter.unseen || m.unseen || m.id === openId) && (!filter.favorite || m.favorite || m.id === openId));
  const pages = pagesIn(allPages, filter, search, agentLabel, projectName);
  // The pages the scope has, searched or not, as the media's counts are.
  const scopePages = pagesIn(allPages, { ...filter, kind: pageKind, unseen: false, favorite: false }, '');
  const freshPages = scopePages.filter((e) => isNew(e.page));
  const entries = interleave(items, pages, !withMedia || !media.hasNextPage);
  const index = items.findIndex((m) => m.id === openId);
  useNextPageNear(media, index, items.length);

  const mediaTotal = counts.data?.total ?? 0;
  const total = mediaTotal + scopePages.length;
  const bytes = counts.data?.bytes ?? 0; // what the media take on disk, since freeing that is why you delete them
  const filtered = !!filter.kind || filter.unseen || filter.favorite || !!search.trim();
  // Every project's unseen is marked at once; a narrower view's, those loaded.
  const unseenMedia = scope === 'all' && !filter.project ? (everything.data?.unseen ?? 0) : loaded.filter((m) => m.unseen).length;
  const toMark = unseenMedia + freshPages.length;
  const markAll = () => {
    if (unseenMedia > 0) void markSeen(scope === 'all' && !filter.project ? { allMedia: true } : { media: loaded.filter((m) => m.unseen).map((m) => m.id) });
    if (freshPages.length > 0) markPagesSeen(freshPages.map((e) => e.page));
  };

  const label = (m: T.MediaItem) => {
    if (scope === 'agent') return undefined;
    const who = m.agentTitle && filter.project ? `${m.agentName} · ${m.agentTitle}` : (m.agentName ?? '');
    const base = filter.project ? who : `${projectName(m.agent.split('/')[0])} · ${who}`;
    return m.agentGone ? t('project.media.agentRemoved', { label: base }) : base;
  };
  const pageLabel = (e: { project: string; page: T.Artifact }) => [filter.project ? '' : projectName(e.project), agentLabel(e.page.agent)].filter(Boolean).join(' · ');

  // Select mode deletes within one project, or one agent's: there's no call
  // for every project's at once. Delete all obeys the kind and the search,
  // which the daemon's delete knows, and not Unseen or Favorites, which it
  // doesn't, so it's off while either is on.
  const canSelect = !!filter.project && withMedia;
  const allCount = filter.unseen || filter.favorite ? 0 : (counts.data?.matching ?? items.length);
  const who = filter.agent ? (own ? agent.title || agent.name : filter.agent) : projectName(filter.project);

  return (
    <div className="flex h-full min-h-0 flex-col" data-media-view={scope}>
      <div className={cn('flex shrink-0 flex-col gap-2 border-b border-line px-4 py-2.5', heading && 'px-4 pt-6 md:px-8')}>
        {heading && (
          <div>
            <h1 className="text-display font-semibold text-title">{t('shell.nav.media')}</h1>
            <p className="mt-0.5 text-ui text-muted">{t('media.view.subtitle')}</p>
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2">
          {scopes.length > 1 && (
            <div className="flex items-center gap-0.5 rounded-lg border border-line p-0.5" role="group" aria-label={t('media.view.scope')} data-media-scopes>
              {scopes.map((s) => (
                <button
                  key={s}
                  aria-pressed={scope === s}
                  data-media-scope={s}
                  onClick={() => {
                    setFilter(withScope(filter, s, home));
                    setSelecting(false);
                  }}
                  className={cn('h-6 rounded-md px-2.5 text-ui text-muted transition hover:text-primary', scope === s && 'bg-surface-strong text-title')}
                >
                  {t(`media.view.scope.${s}`)}
                </button>
              ))}
            </div>
          )}
          {scope === 'all' && (
            <Select value={filter.project} onChange={(v) => set({ project: v, agent: '' })} aria-label={t('shell.allMedia.projectFilter')} data-media-projects className="w-56">
              <SelectOption value="">
                {t('shell.allMedia.allProjects')} ({everything.data?.total ?? 0})
              </SelectOption>
              {everything.data?.projects?.map(({ project: name, count }) => (
                <SelectOption key={name} value={name}>
                  {projectName(name)} ({count})
                </SelectOption>
              ))}
            </Select>
          )}
          {scope !== 'agent' && filter.project && (
            <Select value={filter.agent} onChange={(v) => set({ agent: v })} aria-label={t('project.media.agentFilter')} data-media-agents className="w-60">
              <SelectOption value="">{t('project.media.allAgents')}</SelectOption>
              {/* An agent with pages but no media, opened from its page badge. */}
              {filter.agent && !counts.data?.agents?.some((a) => a.name === filter.agent) && <SelectOption value={filter.agent}>{filter.agent} (0)</SelectOption>}
              {counts.data?.agents?.map(({ name, title, count, gone }) => (
                <SelectOption key={name} value={name}>
                  {title ? `${name} · ${title}` : name}
                  {gone && ` ${t('project.media.removed')}`} ({count})
                </SelectOption>
              ))}
            </Select>
          )}
          {total > 0 && <MediaSearch value={query} onChange={setQuery} />}
          <div className="ml-auto flex flex-wrap items-center gap-1.5">
            {own && !selecting && <Capture agent={agent} canExport={mediaTotal > 0} />}
            {toMark > 0 && !selecting && (
              <Button size="sm" variant="ghost" onClick={markAll} data-media-mark-seen>
                <Check />
                {t('shell.allMedia.markSeen', { count: toMark })}
              </Button>
            )}
            {canSelect && (
              <MediaSelection
                visible={items}
                selecting={selecting}
                onSelecting={setSelecting}
                selected={selected}
                onSelected={setSelected}
                all={{ all: true, agent: filter.agent || undefined, kind: only || undefined, query: search.trim() || undefined }}
                allCount={allCount}
                allBytes={counts.data?.matchingBytes}
                allLabel={describeAll(allCount, only, who, search)}
                deleteMedia={(req) => (own ? api.deleteAgentMedia(agent.ref, req) : api.deleteProjectMedia(filter.project, req))}
                onDeleted={() => setOpenId(null)}
              />
            )}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-1" role="group" aria-label={t('agent.mediaTab.filter')} data-media-kinds>
          <FilterChip active={!filter.kind} count={total} onClick={() => set({ kind: '' })}>
            {t('project.media.everything')}
          </FilterChip>
          {mediaKinds
            .filter((k) => k.kind === 'screenshot' || k.kind === 'recording' || counts.data?.kinds?.[k.kind] || filter.kind === k.kind)
            .map((k) => (
              <FilterChip key={k.kind} icon={k.icon} active={filter.kind === k.kind} count={counts.data?.kinds?.[k.kind] ?? 0} onClick={() => set({ kind: filter.kind === k.kind ? '' : k.kind })}>
                {k.label}
              </FilterChip>
            ))}
          {(scopePages.length > 0 || filter.kind === pageKind) && (
            <FilterChip icon={PageIcon} active={filter.kind === pageKind} count={scopePages.length} onClick={() => set({ kind: filter.kind === pageKind ? '' : pageKind, favorite: false })}>
              {t('media.view.pages')}
              {freshPages.length > 0 && (
                <span className="rounded-full bg-brand-500 px-1.5 text-caption font-semibold tabular-nums text-white" data-media-pages-new={freshPages.length}>
                  {t('pages.media.newCount', { count: freshPages.length })}
                </span>
              )}
            </FilterChip>
          )}
          <span className="mx-1 h-4 w-px bg-line" />
          <FilterChip active={filter.unseen} count={(counts.data?.unseen ?? 0) + freshPages.length} onClick={() => set({ unseen: !filter.unseen })}>
            <span className="size-1.5 rounded-full bg-brand-400" />
            {t('shell.allMedia.unseen')}
          </FilterChip>
          <FilterChip icon={Star} active={filter.favorite} count={counts.data?.favorites ?? 0} onClick={() => set({ favorite: !filter.favorite, kind: filter.kind === pageKind ? '' : filter.kind })}>
            {t('shell.allMedia.favorites')}
          </FilterChip>
          {bytes > 0 && <span className="pl-1.5 text-caption tabular-nums text-subtle">{humanBytes(bytes)}</span>}
        </div>
      </div>
      {media.error && (
        <div className="px-4 pt-3">
          <Notice>{errorMessage(media.error)}</Notice>
        </div>
      )}

      <div className={cn('min-h-0 flex-1 overflow-y-auto p-4', heading && 'md:px-8')}>
        {withMedia && media.isPending && !media.isPlaceholderData ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3">
            {Array.from({ length: 6 }, (_, i) => (
              <div key={i} className="skeleton aspect-[4/3] rounded-xl" />
            ))}
          </div>
        ) : total === 0 && !filtered ? (
          own ? (
            <AgentEmpty onAskLead={onAskLead} />
          ) : (
            <EmptyState
              icon={Images}
              title={t('project.media.emptyTitle')}
              action={
                onAskLead && filter.project ? (
                  <Button variant="primary" onClick={onAskLead}>
                    <MessagesSquare />
                    {t('agent.mediaTab.askLead')}
                  </Button>
                ) : undefined
              }
            >
              {filter.project ? t('project.media.emptyDescription', { project: who }) : t('shell.allMedia.emptyBody')}
            </EmptyState>
          )
        ) : entries.length === 0 ? (
          <NoMatch query={search} onClear={() => setQuery('')} />
        ) : (
          <div className="flex flex-col gap-5">
            {byDay(entries, (e) => e.at, now).map(([day, list]) => (
              <section key={day} className="flex flex-col gap-2.5" data-media-day={day}>
                <h2 className="flex items-baseline gap-2 px-1 text-ui font-medium text-secondary">
                  {day}
                  <span className="text-caption font-normal text-subtle">{list.length}</span>
                </h2>
                <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3">
                  {list.map((e) =>
                    e.item ? (
                      <Fresh key={e.item.id} fresh={e.item.unseen === true} data-unseen={e.item.unseen || undefined}>
                        <MediaCard
                          item={e.item}
                          label={label(e.item)}
                          onOpen={() => setOpenId(e.item.id)}
                          selecting={selecting}
                          selected={selected.has(e.item.id)}
                          onToggle={() => setSelected(toggled(selected, e.item.id))}
                        />
                      </Fresh>
                    ) : (
                      <Fresh key={`${e.project}/${e.page.id}`} fresh={isNew(e.page)} data-new={isNew(e.page) || undefined}>
                        <PageCard project={e.project} page={e.page} label={pageLabel(e)} now={now} dim={selecting} />
                      </Fresh>
                    ),
                  )}
                </div>
              </section>
            ))}
            <MoreMedia pages={media} />
          </div>
        )}
      </div>

      <MediaViewer
        items={items}
        index={index}
        onIndex={(i) => setOpenId(items[i]?.id ?? null)}
        onClose={() => setOpenId(null)}
        onDelete={setDeleting}
        context={
          onSelect && scope !== 'agent'
            ? (item) => (
                <MediaPlace
                  item={item}
                  onSelect={(view) => {
                    setOpenId(null);
                    onSelect(view);
                  }}
                />
              )
            : undefined
        }
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('agent.mediaTab.deleteTitle', { name: deleting?.name ?? '' })}
        description={deleting?.agentName && scope !== 'agent' ? t('project.media.deleteDescription', { agent: deleting.agentName }) : t('agent.mediaTab.deleteDescription')}
        confirmLabel={t('common.delete')}
        destructive
        onConfirm={async () => {
          // Its media event takes it out of the list.
          await api.deleteMedia(deleting!.id);
          setOpenId(null);
        }}
      />
    </div>
  );
}

// activeScope is the scope button a filter is on: the home agent's, the
// home project's (any of its agents chosen), or everything, which is also
// any other project's.
function activeScope(f: MediaFilter, home: MediaHome): MediaScopeName {
  if (home.agent && atHome(f, 'agent', home)) return 'agent';
  if (home.project && f.project === home.project) return 'project';
  return 'all';
}

// Fresh rings a card you haven't opened yet, and marks it New.
function Fresh({ fresh, children, ...rest }: { fresh: boolean; children: ReactNode; 'data-unseen'?: boolean; 'data-new'?: boolean }) {
  const t = useT();
  return (
    <div className={cn('relative rounded-xl', fresh && 'ring-2 ring-brand-400/60 ring-offset-2 ring-offset-[var(--color-ink)]')} {...rest}>
      {children}
      {fresh && <span className="pointer-events-none absolute -right-1.5 -top-1.5 rounded-full bg-brand-500 px-1.5 py-px text-caption font-semibold text-white">{t('shell.allMedia.new')}</span>}
    </div>
  );
}

// PageCard is a Hatch page among the media, as large as they are: its first
// screen, its title, who published it and when. It opens in the page
// preview (components/pages/PagePreview), which also marks it seen.
function PageCard({ project, page, label, now, dim }: { project: string; page: T.Artifact; label: string; now: number; dim?: boolean }) {
  const t = useT();
  return (
    <button
      onClick={() => openPage(project, page)}
      disabled={dim}
      data-media-page={page.id}
      className="group block w-full overflow-hidden rounded-xl border border-line bg-surface-faint text-left transition duration-200 [contain-intrinsic-size:auto_220px] [content-visibility:auto] hover:-translate-y-0.5 hover:border-line-vivid hover:bg-surface disabled:opacity-40 disabled:hover:translate-y-0"
    >
      <span className="relative block">
        <PageThumb project={project} page={page} className="border-b border-line" />
        <span className="absolute left-2 top-2">
          <Badge variant="info" className="bg-black/60 backdrop-blur [:root[data-appearance=light]_&]:text-white">
            <PageIcon />
            {t('media.view.page')}
          </Badge>
        </span>
      </span>
      <span className="grid gap-1 px-3 py-2.5">
        <span className="truncate text-ui font-medium text-primary">{page.title}</span>
        <span className="flex min-w-0 items-center gap-1.5 text-caption text-subtle">
          <span className="min-w-0 truncate">{label}</span>
          <span aria-hidden>·</span>
          <span className="shrink-0 tabular-nums">{timeAgo(page.updatedAt, now)}</span>
        </span>
      </span>
    </button>
  );
}

// Capture is an agent's own buttons, on its own media: a screenshot, a
// recording of the screen (or of the keys and mouse too), a note, and an
// export of everything for a pull request.
function Capture({ agent, canExport }: { agent: T.Agent; canExport: boolean }) {
  const t = useT();
  const { shot, record, recording, isRecording, running } = useCapture(agent);
  const [noting, setNoting] = useState(false);
  const exportAll = useMutation({
    mutationFn: () => api.exportMedia(agent.ref),
    onSuccess: (result) =>
      toast(t('agent.mediaTab.exported', { count: result.items }), {
        description: result.dir,
        action: { label: t('agent.mediaTab.openFolder'), onClick: () => void window.agentbox.openPath(result.dir) },
      }),
  });
  const error = shot.error ?? record.error ?? exportAll.error;
  // A past agent's (UI v2's panel, state "archived") is only read: its
  // routes went with it.
  const archived = agent.state === 'archived';
  return (
    <>
      {error && <span className="max-w-64 truncate text-caption text-rose-300" title={errorMessage(error)}>{errorMessage(error)}</span>}
      <Button size="sm" disabled={!running || shot.isPending} onClick={() => shot.mutate()}>
        {shot.isPending ? <LoaderCircle className="animate-spin" /> : <Camera />}
        {t('agent.browser.screenshot')}
      </Button>
      {isRecording ? (
        <Button size="sm" className="border-rose-400/30 bg-rose-500/15 text-rose-100 hover:bg-rose-500/25" disabled={record.isPending} onClick={() => record.mutate()}>
          {record.isPending ? <LoaderCircle className="animate-spin" /> : <span className="size-2 rounded-[3px] bg-rose-400" />}
          {recording.data?.input === 'desktop' ? t('agent.mediaTab.stopRecordingInput') : t('agent.browser.stopRecording')}
        </Button>
      ) : (
        <Menu>
          <MenuTrigger asChild>
            <Button size="sm" disabled={!running || record.isPending}>
              {record.isPending ? <LoaderCircle className="animate-spin" /> : <span className="size-2.5 rounded-full bg-rose-500" />}
              {t('agent.browser.record')}
            </Button>
          </MenuTrigger>
          <MenuContent>
            <MenuItem icon={Circle} onSelect={() => record.mutate()}>
              {t('agent.browser.recordScreen')}
            </MenuItem>
            <MenuItem icon={MousePointerClick} onSelect={() => record.mutate('desktop')}>
              {t('agent.browser.recordInput')}
            </MenuItem>
          </MenuContent>
        </Menu>
      )}
      {!archived && (
        <Button size="sm" onClick={() => setNoting(true)}>
          <StickyNote />
          {t('agent.mediaTab.note')}
        </Button>
      )}
      {!archived && (
        <Tip label={t('agent.mediaTab.exportTip')}>
          <Button size="sm" variant="ghost" disabled={!canExport || exportAll.isPending} onClick={() => exportAll.mutate()}>
            {exportAll.isPending ? <LoaderCircle className="animate-spin" /> : <Download />}
            {t('agent.mediaTab.export')}
          </Button>
        </Tip>
      )}
      <NoteDialog agent={agent} open={noting} onOpenChange={setNoting} />
    </>
  );
}

// useCapture is an agent's screenshot and recording, and whether it's
// recording now.
function useCapture(agent: T.Agent) {
  const t = useT();
  const queryClient = useQueryClient();
  const running = agent.state === 'running';
  const recording = useQuery({
    queryKey: ['recording', agent.ref],
    queryFn: () => api.recording(agent.ref),
    enabled: running,
    refetchInterval: (query) => (query.state.data?.recording ? 2_000 : 10_000),
  });
  const saved = (item: T.MediaItem) => toast(t('agent.mediaTab.saved', { name: item.name }));
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
  return { shot, record, recording, isRecording, running };
}

// AgentEmpty is an agent's own Media before it has kept anything: ask the lead.
function AgentEmpty({ onAskLead }: { onAskLead?: () => void }) {
  const t = useT();
  return (
    <EmptyState
      icon={Images}
      title={t('agent.mediaTab.emptyTitle')}
      action={
        onAskLead ? (
          <Button variant="primary" onClick={onAskLead}>
            <MessagesSquare />
            {t('agent.mediaTab.askLead')}
          </Button>
        ) : undefined
      }
    >
      {t('agent.mediaTab.emptyBody')}
    </EmptyState>
  );
}

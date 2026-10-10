import { Image } from 'lucide-react';
import { useDeferredValue, useMemo, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { useSeeMedia } from '../lib/notifications';
import { useProjectName } from '../lib/useProjectName';
import { describeAll, kindInfo } from '../lib/media';
import { loadedItems, useMediaCounts, useMediaPages, useNextPageNear } from '../lib/mediaPages';
import { humanBytes } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { FilterChip, MediaCard, MediaSearch, MediaSelection, MediaViewer, MoreMedia, NoMatch, toggled } from './MediaTab';
import { EmptyState } from './ui/card';
import { Select, SelectOption } from './ui/select';

// ProjectMediaPanel is every agent's media in one stream, so you can see what
// the whole project has shown without opening each agent. Each item is labelled
// with the agent it came from and what that agent was for, and the stream can
// be filtered down to one agent or one kind, and searched.
export function ProjectMediaPanel({ project }: { project: string }) {
  const t = useT();
  const projectName = useProjectName(project);
  const [agent, setAgent] = useState('');
  const [kind, setKind] = useState('');
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

  // The daemon filters and searches, a page at a time; the counts are of
  // every item, not the pages loaded.
  const scope = { project, agent, only: kind, q: search };
  const media = useMediaPages(scope);
  const counts = useMediaCounts(scope);
  const visible = useMemo(() => loadedItems(media.data), [media.data]);
  // The agents that have shown something, newest item first, each with a count.
  const agents = counts.data?.agents ?? [];
  const items = agents.reduce((n, a) => n + a.count, 0);
  const bytes = agents.reduce((n, a) => n + a.bytes, 0); // what the project's media takes on disk, since freeing that is why you delete it
  const index = visible.findIndex((item) => item.id === openId);
  useNextPageNear(media, index, visible.length);
  // A delete's media events take the items out of the list.
  const refresh = () => setOpenId(null);
  // A gone agent is called out: this item was kept past a destroy, not
  // deleted with it, so it's still worth knowing it isn't findable anywhere else.
  const label = (item: T.MediaItem) => {
    const base = item.agentTitle ? `${item.agentName} · ${item.agentTitle}` : (item.agentName ?? '');
    return item.agentGone ? t('project.media.agentRemoved', { label: base }) : base;
  };

  if (media.isPending || counts.isPending) return null;
  if (items === 0) {
    return (
      <div className="panel rounded-2xl">
        <EmptyState icon={Image} title={t('project.media.emptyTitle')}>
          {t('project.media.emptyDescription', { project: projectName })}
        </EmptyState>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <span className="text-[13px] text-muted">
          {t('project.media.items', { count: items })}
          {bytes > 0 && <span className="text-subtle"> · {humanBytes(bytes)}</span>}
        </span>
        <MediaSearch value={query} onChange={setQuery} />
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          <MediaSelection
            visible={visible}
            selecting={selecting}
            onSelecting={setSelecting}
            selected={selected}
            onSelected={setSelected}
            // The same filters and search as the list, so "Delete all" takes
            // what it shows, scrolled to or not.
            all={{ all: true, agent: agent || undefined, kind: kind || undefined, query: search.trim() || undefined }}
            allCount={counts.data?.matching ?? 0}
            allBytes={counts.data?.matchingBytes}
            allLabel={describeAll(counts.data?.matching ?? 0, kind, agent, search)}
            deleteMedia={(req) => api.deleteProjectMedia(project, req)}
            onDeleted={refresh}
          />
        </div>
      </div>
      <div data-media-filters>
        <Select value={agent} onChange={setAgent} aria-label={t('project.media.agentFilter')} className="w-72">
          <SelectOption value="">
            {t('project.media.allAgents')} ({items})
          </SelectOption>
          {agents.map(({ name, title, count, gone }) => (
            <SelectOption key={name} value={name}>
              {title ? `${name} · ${title}` : name}
              {gone && ` ${t('project.media.removed')}`} ({count})
            </SelectOption>
          ))}
        </Select>
      </div>
      <div className="flex flex-wrap items-center gap-1" data-media-kinds>
        <FilterChip active={!kind} count={counts.data?.total ?? 0} onClick={() => setKind('')}>
          {t('project.media.everything')}
        </FilterChip>
        {Object.entries(counts.data?.kinds ?? {}).map(([name, count]) => (
          <FilterChip key={name} active={kind === name} count={count} onClick={() => setKind(kind === name ? '' : name)}>
            {kindInfo(name).label}
          </FilterChip>
        ))}
      </div>

      {visible.length === 0 ? (
        <NoMatch query={search} onClear={() => setQuery('')} />
      ) : (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {visible.map((item) => (
              <MediaCard
                key={item.id}
                item={item}
                label={label(item)}
                onOpen={() => setOpenId(item.id)}
                selecting={selecting}
                selected={selected.has(item.id)}
                onToggle={() => setSelected(toggled(selected, item.id))}
              />
            ))}
          </div>
          <MoreMedia pages={media} />
        </>
      )}

      <MediaViewer
        items={visible}
        index={index}
        onIndex={(i) => setOpenId(visible[i]?.id ?? null)}
        onClose={() => setOpenId(null)}
        onDelete={(item) => setDeleting(item)}
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('project.media.deleteTitle', { name: deleting?.name ?? '' })}
        description={deleting?.agentName ? t('project.media.deleteDescription', { agent: deleting.agentName }) : t('project.media.deleteDescriptionNone')}
        confirmLabel={t('common.delete')}
        destructive
        onConfirm={async () => {
          await api.deleteMedia(deleting!.id);
          refresh();
        }}
      />
    </div>
  );
}

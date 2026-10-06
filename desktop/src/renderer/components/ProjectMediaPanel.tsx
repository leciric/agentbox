import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Image, Layers } from 'lucide-react';
import { useDeferredValue, useMemo, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { useSeeMedia } from '../lib/notifications';
import { useProjectName } from '../lib/useProjectName';
import { describeAll, kindInfo, searchMedia } from '../lib/media';
import { humanBytes } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { FilterChip, MediaCard, MediaSearch, MediaSelection, MediaViewer, NoMatch, toggled } from './MediaTab';
import { EmptyState } from './ui/card';

// ProjectMediaPanel is every agent's media in one stream, so you can see what
// the whole project has shown without opening each agent. Each item is labelled
// with the agent it came from and what that agent was for, and the stream can
// be filtered down to one agent or one kind, and searched.
export function ProjectMediaPanel({ project }: { project: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projectName = useProjectName(project);
  const media = useQuery({ queryKey: ['projectMedia', project], queryFn: () => api.projectMedia(project), refetchInterval: 10_000 });
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

  const items = useMemo(() => media.data ?? [], [media.data]);
  // The agents that have shown something, newest item first, each with a count.
  const agents = new Map<string, { title: string; count: number; gone: boolean }>();
  const kinds = new Map<string, number>();
  let total = 0; // what the project's media takes on disk, since freeing that is why you delete it
  for (const item of items) {
    const name = item.agentName ?? '';
    const entry = agents.get(name) ?? { title: item.agentTitle ?? '', count: 0, gone: item.agentGone ?? false };
    agents.set(name, { title: entry.title || (item.agentTitle ?? ''), count: entry.count + 1, gone: entry.gone || (item.agentGone ?? false) });
    kinds.set(item.kind, (kinds.get(item.kind) ?? 0) + 1);
    total += item.size;
  }

  const visible = useMemo(
    () => searchMedia(items.filter((item) => (!agent || item.agentName === agent) && (!kind || item.kind === kind)), search),
    [items, agent, kind, search],
  );
  const index = visible.findIndex((item) => item.id === openId);
  const refresh = async () => {
    setOpenId(null);
    await queryClient.invalidateQueries({ queryKey: ['projectMedia', project] });
  };
  // A gone agent is called out: this item was kept past a destroy, not
  // deleted with it, so it's still worth knowing it isn't findable anywhere else.
  const label = (item: T.MediaItem) => {
    const base = item.agentTitle ? `${item.agentName} · ${item.agentTitle}` : (item.agentName ?? '');
    return item.agentGone ? t('project.media.agentRemoved', { label: base }) : base;
  };

  if (items.length === 0) {
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
          {t('project.media.items', { count: items.length })}
          {total > 0 && <span className="text-subtle"> · {humanBytes(total)}</span>}
        </span>
        <MediaSearch value={query} onChange={setQuery} />
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          <MediaSelection
            visible={visible}
            selecting={selecting}
            onSelecting={setSelecting}
            selected={selected}
            onSelected={setSelected}
            // A search has no counterpart on the daemon's side, so what it
            // leaves is deleted by ID: "Delete all" never takes more than it shows.
            all={search.trim() ? { ids: visible.map((item) => item.id) } : { all: true, agent: agent || undefined, kind: kind || undefined }}
            allLabel={describeAll(visible.length, kind, agent, search)}
            deleteMedia={(req) => api.deleteProjectMedia(project, req)}
            onDeleted={refresh}
          />
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-1" data-media-filters>
        <FilterChip icon={Layers} active={!agent} count={items.length} onClick={() => setAgent('')}>
          {t('project.media.allAgents')}
        </FilterChip>
        {[...agents].map(([name, { title, count, gone }]) => (
          <FilterChip key={name} active={agent === name} count={count} onClick={() => setAgent(agent === name ? '' : name)}>
            {title ? `${name} · ${title}` : name}
            {gone && ` ${t('project.media.removed')}`}
          </FilterChip>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-1" data-media-kinds>
        <FilterChip active={!kind} count={items.length} onClick={() => setKind('')}>
          {t('project.media.everything')}
        </FilterChip>
        {[...kinds].map(([name, count]) => (
          <FilterChip key={name} active={kind === name} count={count} onClick={() => setKind(kind === name ? '' : name)}>
            {kindInfo(name).label}
          </FilterChip>
        ))}
      </div>

      {visible.length === 0 ? (
        <NoMatch query={search} onClear={() => setQuery('')} />
      ) : (
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
          await refresh();
        }}
      />
    </div>
  );
}

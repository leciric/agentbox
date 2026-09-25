import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Image, Layers } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { describeAll } from '../lib/media';
import { humanBytes } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { FilterChip, MediaCard, MediaSelection, MediaViewer, toggled } from './MediaTab';
import { EmptyState } from './ui/card';

// ProjectMediaPanel is every agent's media in one stream, so you can see what
// the whole project has shown without opening each agent. Each item is labelled
// with the agent it came from and what that agent was for, and the stream can
// be filtered down to one agent or one kind.
export function ProjectMediaPanel({ project }: { project: string }) {
  const queryClient = useQueryClient();
  const media = useQuery({ queryKey: ['projectMedia', project], queryFn: () => api.projectMedia(project), refetchInterval: 10_000 });
  const [agent, setAgent] = useState('');
  const [kind, setKind] = useState('');
  const [openId, setOpenId] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<T.MediaItem | null>(null);
  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());

  const items = media.data ?? [];
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

  const visible = items.filter((item) => (!agent || item.agentName === agent) && (!kind || item.kind === kind));
  const index = visible.findIndex((item) => item.id === openId);
  const refresh = async () => {
    setOpenId(null);
    await queryClient.invalidateQueries({ queryKey: ['projectMedia', project] });
  };
  // A gone agent is called out: this item was kept past a destroy, not
  // deleted with it, so it's still worth knowing it isn't findable anywhere else.
  const label = (item: T.MediaItem) => {
    const base = item.agentTitle ? `${item.agentName} · ${item.agentTitle}` : (item.agentName ?? '');
    return item.agentGone ? `${base} (agent removed)` : base;
  };

  if (items.length === 0) {
    return (
      <div className="panel rounded-2xl">
        <EmptyState icon={Image} title="Nothing shown yet">
          Screenshots, recordings, reports, logs and notes from every agent of {project} collect here.
        </EmptyState>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <span className="text-[13px] text-muted">
          {items.length} item{items.length === 1 ? '' : 's'}
          {total > 0 && <span className="text-subtle"> · {humanBytes(total)}</span>}
        </span>
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          <MediaSelection
            visible={visible}
            selecting={selecting}
            onSelecting={setSelecting}
            selected={selected}
            onSelected={setSelected}
            all={{ all: true, agent: agent || undefined, kind: kind || undefined }}
            allLabel={describeAll(visible.length, kind, agent)}
            deleteMedia={(req) => api.deleteProjectMedia(project, req)}
            onDeleted={refresh}
          />
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-1" data-media-filters>
        <FilterChip icon={Layers} active={!agent} count={items.length} onClick={() => setAgent('')}>
          All agents
        </FilterChip>
        {[...agents].map(([name, { title, count, gone }]) => (
          <FilterChip key={name} active={agent === name} count={count} onClick={() => setAgent(agent === name ? '' : name)}>
            {title ? `${name} · ${title}` : name}
            {gone && ' (removed)'}
          </FilterChip>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-1" data-media-kinds>
        <FilterChip active={!kind} count={items.length} onClick={() => setKind('')}>
          Everything
        </FilterChip>
        {[...kinds].map(([name, count]) => (
          <FilterChip key={name} active={kind === name} count={count} onClick={() => setKind(kind === name ? '' : name)}>
            {name}
          </FilterChip>
        ))}
      </div>

      {visible.length === 0 ? (
        <p className="px-1 text-[13px] text-subtle">Nothing matches that filter.</p>
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
        title={`Delete “${deleting?.name}”?`}
        description={`It's removed from ${deleting?.agentName ?? 'the agent'}'s media for good.`}
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

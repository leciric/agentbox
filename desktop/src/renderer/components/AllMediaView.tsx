import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, FolderGit2, Image as ImageIcon, Images, Layers, Video } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { byDay, useMarkSeen } from '../lib/notifications';
import { projectLabel } from '../lib/projectName';
import { cn } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { MediaPlace } from './MediaPlace';
import { FilterChip, MediaCard, MediaViewer } from './MediaTab';
import { Button } from './ui/button';
import { EmptyState } from './ui/card';

// The kinds the Media view shows: what an agent shows you, rather than notes,
// logs and reports, which stay in each project's Media tab.
export const shownKinds = ['screenshot', 'recording'];

// useAllMedia is every project's screenshots and recordings, newest first.
export function useAllMedia() {
  return useQuery({ queryKey: ['allMedia', shownKinds], queryFn: () => api.allMedia(shownKinds), refetchInterval: 30_000 });
}

// AllMediaView is every agent's screenshots and recordings, in every project,
// grouped by day, so you can find what an agent showed without knowing which
// project or agent it was. Those a notification told you about and you
// haven't opened are marked until you do (or mark them seen here or in the
// bell). An item opens in the viewer with links to its project, its agent and
// the agent's pull request.
export function AllMediaView({ onSelect }: { onSelect: (view: View) => void }) {
  const queryClient = useQueryClient();
  const media = useAllMedia();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const markSeen = useMarkSeen();
  const [project, setProject] = useState('');
  const [agent, setAgent] = useState('');
  const [kind, setKind] = useState('');
  const [unseenOnly, setUnseenOnly] = useState(false);
  const [openId, setOpenId] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<T.MediaItem | null>(null);

  const items = media.data ?? [];
  const projectOf = (m: T.MediaItem) => m.agent.split('/')[0];
  const projectCounts = new Map<string, number>();
  for (const m of items) projectCounts.set(projectOf(m), (projectCounts.get(projectOf(m)) ?? 0) + 1);
  const inProject = items.filter((m) => !project || projectOf(m) === project);
  const agentCounts = new Map<string, number>();
  for (const m of inProject) agentCounts.set(m.agent, (agentCounts.get(m.agent) ?? 0) + 1);
  const inAgent = inProject.filter((m) => !agent || m.agent === agent);
  // The open item stays in an Unseen-only list once opening it marks it seen,
  // so the viewer doesn't close on it.
  const visible = inAgent.filter((m) => (!kind || m.kind === kind) && (!unseenOnly || m.unseen || m.id === openId));
  const unseen = items.filter((m) => m.unseen);
  const index = visible.findIndex((m) => m.id === openId);
  const label = (m: T.MediaItem) => `${projectLabel(projectOf(m), projects.data)} · ${m.agentName}${m.agentGone ? ' (removed)' : ''}`;

  const open = (m: T.MediaItem) => {
    setOpenId(m.id);
    if (m.unseen) void markSeen({ media: [m.id] });
  };

  return (
    <div className="h-full overflow-y-auto px-4 py-6 md:px-8" data-all-media>
      <div className="mx-auto flex max-w-6xl flex-col gap-4">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <h1 className="text-[20px] font-semibold text-title">Media</h1>
            <p className="mt-0.5 text-[13px] text-muted">Every agent's screenshots and recordings, in every project, newest first.</p>
          </div>
          {unseen.length > 0 && (
            <Button size="sm" variant="ghost" className="ml-auto" onClick={() => void markSeen({ media: unseen.map((m) => m.id) })}>
              <Check />
              Mark {unseen.length} seen
            </Button>
          )}
        </div>

        {items.length === 0 ? (
          <div className="panel rounded-2xl">
            <EmptyState icon={Images} title={media.isPending ? 'Loading…' : 'Nothing shown yet'}>
              Screenshots and recordings from every agent of every project collect here.
            </EmptyState>
          </div>
        ) : (
          <div className="panel flex flex-col gap-1.5 rounded-xl p-2">
            <div className="flex flex-wrap items-center gap-1" data-media-projects>
              <FilterChip
                icon={Layers}
                active={!project}
                count={items.length}
                onClick={() => {
                  setProject('');
                  setAgent('');
                }}
              >
                All projects
              </FilterChip>
              {[...projectCounts].map(([name, n]) => (
                <FilterChip
                  key={name}
                  icon={FolderGit2}
                  active={project === name}
                  count={n}
                  onClick={() => {
                    setProject(project === name ? '' : name);
                    setAgent('');
                  }}
                >
                  <span className="max-w-[220px] truncate">{projectLabel(name, projects.data)}</span>
                </FilterChip>
              ))}
            </div>
            <div className="flex flex-wrap items-center gap-1" data-media-agents>
              <FilterChip active={!agent} count={inProject.length} onClick={() => setAgent('')}>
                All agents
              </FilterChip>
              {[...agentCounts].map(([ref, n]) => (
                <FilterChip key={ref} active={agent === ref} count={n} onClick={() => setAgent(agent === ref ? '' : ref)}>
                  {!project && <span className="max-w-[110px] truncate text-subtle">{projectLabel(ref.split('/')[0], projects.data)} /</span>}
                  {ref.split('/')[1]}
                </FilterChip>
              ))}
            </div>
            <div className="flex flex-wrap items-center gap-1" data-media-kinds>
              <FilterChip active={!kind} count={inAgent.length} onClick={() => setKind('')}>
                Everything
              </FilterChip>
              <FilterChip icon={ImageIcon} active={kind === 'screenshot'} count={inAgent.filter((m) => m.kind === 'screenshot').length} onClick={() => setKind(kind === 'screenshot' ? '' : 'screenshot')}>
                Screenshots
              </FilterChip>
              <FilterChip icon={Video} active={kind === 'recording'} count={inAgent.filter((m) => m.kind === 'recording').length} onClick={() => setKind(kind === 'recording' ? '' : 'recording')}>
                Recordings
              </FilterChip>
              <span className="mx-1 h-4 w-px bg-line" />
              <FilterChip active={unseenOnly} count={inAgent.filter((m) => m.unseen).length} onClick={() => setUnseenOnly(!unseenOnly)}>
                <span className="size-1.5 rounded-full bg-brand-400" />
                Unseen
              </FilterChip>
            </div>
          </div>
        )}

        {byDay(visible, (m) => m.createdAt).map(([day, list]) => (
          <section key={day} className="flex flex-col gap-2.5">
            <h2 className="flex items-baseline gap-2 px-1 text-[13px] font-medium text-secondary">
              {day}
              <span className="text-[12px] font-normal text-subtle">{list.length}</span>
            </h2>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
              {list.map((m) => (
                <div key={m.id} className={cn('relative rounded-xl', m.unseen && 'ring-2 ring-brand-400/60 ring-offset-2 ring-offset-[var(--color-ink)]')} data-unseen={m.unseen || undefined}>
                  <MediaCard item={m} label={label(m)} onOpen={() => open(m)} />
                  {m.unseen && <span className="pointer-events-none absolute -right-1.5 -top-1.5 rounded-full bg-brand-500 px-1.5 py-px text-[10px] font-semibold text-white">New</span>}
                </div>
              ))}
            </div>
          </section>
        ))}
        {items.length > 0 && visible.length === 0 && <div className="py-16 text-center text-[13px] text-muted">Nothing matches these filters.</div>}
      </div>

      <MediaViewer
        items={visible}
        index={index}
        onIndex={(i) => visible[i] && open(visible[i])}
        onClose={() => setOpenId(null)}
        onDelete={setDeleting}
        context={(item) => (
          <MediaPlace
            item={item}
            onSelect={(view) => {
              setOpenId(null);
              onSelect(view);
            }}
          />
        )}
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete “${deleting?.name}”?`}
        description={`It's removed from ${deleting?.agentName ?? 'the agent'}'s media for good.`}
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          await api.deleteMedia(deleting!.id);
          setOpenId(null);
          await queryClient.invalidateQueries({ queryKey: ['allMedia'] });
        }}
      />
    </div>
  );
}

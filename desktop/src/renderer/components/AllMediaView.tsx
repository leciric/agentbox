import { useQuery } from '@tanstack/react-query';
import { Check, Image as ImageIcon, Images, Star, Video } from 'lucide-react';
import { useMemo, useState } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { loadedItems, useMediaCounts, useMediaPages, useNextPageNear } from '../lib/mediaPages';
import { byDay, useMarkSeen } from '../lib/notifications';
import { useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { cn } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { MediaPlace } from './MediaPlace';
import { AllMediaPages } from './pages/MediaPages';
import { FilterChip, MediaCard, MediaViewer, MoreMedia } from './MediaTab';
import { Button } from './ui/button';
import { Select, SelectOption } from './ui/select';
import { EmptyState } from './ui/card';

// The kinds the Media view shows: what an agent shows you, rather than notes,
// logs and reports, which stay in each project's Media tab.
export const shownKinds = ['screenshot', 'recording'];

// AllMediaView is every agent's screenshots and recordings, in every project,
// grouped by day, so you can find what an agent showed without knowing which
// project or agent it was. Those a notification told you about and you
// haven't opened are marked until you do (or mark them seen here or in the
// bell). An item opens in the viewer with links to its project, its agent and
// the agent's pull request.
export function AllMediaView({ onSelect }: { onSelect: (view: View) => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const markSeen = useMarkSeen();
  const [project, setProject] = useState('');
  const [agent, setAgent] = useState('');
  const [kind, setKind] = useState('');
  const [unseenOnly, setUnseenOnly] = useState(false);
  const [favoritesOnly, setFavoritesOnly] = useState(false);
  const [openId, setOpenId] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<T.MediaItem | null>(null);

  // The daemon filters, a page at a time; the counts are of every item, not
  // the pages loaded: everything's for the projects and the unseen to mark,
  // the project's and agent's for the rest.
  const media = useMediaPages({ kinds: shownKinds, project, agent, only: kind, unseen: unseenOnly, favorite: favoritesOnly });
  const everything = useMediaCounts({ kinds: shownKinds });
  const counts = useMediaCounts({ kinds: shownKinds, project, agent });
  const loaded = useMemo(() => loadedItems(media.data), [media.data]);
  const projectOf = (m: T.MediaItem) => m.agent.split('/')[0];
  const total = everything.data?.total ?? 0;
  const inProject = counts.data?.agents.reduce((n, a) => n + a.count, 0) ?? 0;
  // The open item stays in an Unseen-only list once opening it marks it seen,
  // and in a Favorites-only one once it's unstarred, so the viewer doesn't
  // close on it; so do the items the list has marked since it was read.
  const visible = loaded.filter((m) => (!unseenOnly || m.unseen || m.id === openId) && (!favoritesOnly || m.favorite || m.id === openId));
  const unseen = everything.data?.unseen ?? 0;
  const index = visible.findIndex((m) => m.id === openId);
  useNextPageNear(media, index, visible.length);
  const label = (m: T.MediaItem) => {
    const base = `${projectLabel(projectOf(m), projects.data)} · ${m.agentName}`;
    return m.agentGone ? t('shell.allMedia.removed', { label: base }) : base;
  };

  const open = (m: T.MediaItem) => {
    setOpenId(m.id);
    if (m.unseen) void markSeen({ media: [m.id] });
  };

  return (
    <div className="h-full overflow-y-auto px-4 py-6 md:px-8" data-all-media>
      <div className="mx-auto flex max-w-6xl flex-col gap-4">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <h1 className="text-[20px] font-semibold text-title">{t('shell.nav.media')}</h1>
            <p className="mt-0.5 text-[13px] text-muted">{t('shell.allMedia.subtitle')}</p>
          </div>
          {unseen > 0 && (
            <Button size="sm" variant="ghost" className="ml-auto" onClick={() => void markSeen({ allMedia: true })}>
              <Check />
              {t('shell.allMedia.markSeen', { count: unseen })}
            </Button>
          )}
        </div>

        <AllMediaPages>
        {total === 0 ? (
          <div className="panel rounded-2xl">
            <EmptyState icon={Images} title={everything.isPending ? t('common.loading') : t('shell.allMedia.emptyTitle')}>
              {t('shell.allMedia.emptyBody')}
            </EmptyState>
          </div>
        ) : (
          <div className="panel flex flex-col gap-1.5 rounded-xl p-2">
            <div className="flex flex-wrap items-center gap-2" data-media-selects>
              <Select
                value={project}
                onChange={(v) => {
                  setProject(v);
                  setAgent('');
                }}
                aria-label={t('shell.allMedia.projectFilter')}
                data-media-projects
                className="w-60"
              >
                <SelectOption value="">{t('shell.allMedia.allProjects')} ({total})</SelectOption>
                {everything.data?.projects.map(({ project: name, count }) => (
                  <SelectOption key={name} value={name}>
                    {projectLabel(name, projects.data)} ({count})
                  </SelectOption>
                ))}
              </Select>
              <Select
                value={agent}
                onChange={setAgent}
                disabled={!project}
                aria-label={t('shell.allMedia.agentFilter')}
                data-media-agents
                className="w-60"
              >
                <SelectOption value="">
                  {t('shell.allMedia.allAgents')}
                  {project ? ` (${inProject})` : ''}
                </SelectOption>
                {project &&
                  counts.data?.agents.map(({ name, count }) => (
                    <SelectOption key={name} value={name}>
                      {name} ({count})
                    </SelectOption>
                  ))}
              </Select>
            </div>
            <div className="flex flex-wrap items-center gap-1" data-media-kinds>
              <FilterChip active={!kind} count={counts.data?.total ?? 0} onClick={() => setKind('')}>
                {t('shell.allMedia.everything')}
              </FilterChip>
              <FilterChip icon={ImageIcon} active={kind === 'screenshot'} count={counts.data?.kinds.screenshot ?? 0} onClick={() => setKind(kind === 'screenshot' ? '' : 'screenshot')}>
                {t('agent.media.kind.screenshot')}
              </FilterChip>
              <FilterChip icon={Video} active={kind === 'recording'} count={counts.data?.kinds.recording ?? 0} onClick={() => setKind(kind === 'recording' ? '' : 'recording')}>
                {t('agent.media.kind.recording')}
              </FilterChip>
              <span className="mx-1 h-4 w-px bg-line" />
              <FilterChip active={unseenOnly} count={counts.data?.unseen ?? 0} onClick={() => setUnseenOnly(!unseenOnly)}>
                <span className="size-1.5 rounded-full bg-brand-400" />
                {t('shell.allMedia.unseen')}
              </FilterChip>
              <FilterChip icon={Star} active={favoritesOnly} count={counts.data?.favorites ?? 0} onClick={() => setFavoritesOnly(!favoritesOnly)}>
                {t('shell.allMedia.favorites')}
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
                  {m.unseen && <span className="pointer-events-none absolute -right-1.5 -top-1.5 rounded-full bg-brand-500 px-1.5 py-px text-[10px] font-semibold text-white">{t('shell.allMedia.new')}</span>}
                </div>
              ))}
            </div>
          </section>
        ))}
        {loaded.length > 0 && <MoreMedia pages={media} />}
        {total > 0 && !media.isPending && visible.length === 0 && <div className="py-16 text-center text-[13px] text-muted">{t('agent.mediaTab.noMatch')}</div>}
        </AllMediaPages>
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
        title={t('agent.mediaTab.deleteTitle', { name: deleting?.name ?? '' })}
        description={deleting?.agentName ? t('shell.allMedia.deleteDescription', { agent: deleting.agentName }) : t('agent.mediaTab.deleteDescription')}
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

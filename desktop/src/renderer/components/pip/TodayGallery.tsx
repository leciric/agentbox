import { useQueryClient } from '@tanstack/react-query';
import { Film, Image as ImageIcon, Images } from 'lucide-react';
import { useEffect, useState } from 'react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { loadedItems, useMediaPages } from '../../lib/mediaPages';
import { captureKinds } from '../../lib/pip';
import { openToday, useToday } from '../../lib/pipStore';
import { cn } from '../../lib/utils';
import { ConfirmDialog } from '../ConfirmDialog';
import { FilterChip, MediaCard, MediaViewer } from '../MediaTab';
import { Button } from '../ui/button';
import { EmptyState } from '../ui/card';
import { Dialog, DialogContent, DialogTitle } from '../ui/dialog';
import { Tip } from '../ui/tooltip';

const startOfToday = () => {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  return d.getTime();
};

// useTodays is a project's screenshots and recordings since midnight, newest
// first: its media pages, read on until one goes back to yesterday.
function useTodays(project: string) {
  const pages = useMediaPages({ project, kinds: captureKinds });
  const items = loadedItems(pages.data);
  const since = startOfToday();
  const last = items.at(-1);
  const more = pages.hasNextPage && !pages.isFetchingNextPage && (!last || new Date(last.createdAt).getTime() >= since);
  useEffect(() => {
    if (more) void pages.fetchNextPage();
  }, [more, pages]);
  return items.filter((i) => new Date(i.createdAt).getTime() >= since);
}

const seenKey = (project: string) => `agentbox.ui.v2.today.${project}`;

// TodayButton is the lead's header's way to everything its agents captured
// today, with a dot for what came since it was last opened.
export function TodayButton({ project }: { project: string }) {
  const t = useT();
  const items = useTodays(project);
  const today = useToday();
  const [seen, setSeen] = useState(() => localStorage.getItem(seenKey(project)) ?? '');
  const fresh = items.some((i) => i.createdAt > seen);
  const open = () => {
    const now = new Date().toISOString();
    localStorage.setItem(seenKey(project), now);
    setSeen(now);
    openToday({ project });
  };
  return (
    <Tip label={t('pip.today.tip')}>
      <Button size="sm" variant="ghost" className={cn('relative gap-1.5 px-2.5', today?.project === project && 'bg-surface-raised text-primary')} onClick={open} data-pip-today>
        <Images />
        {t('pip.today')}
        {items.length > 0 && <span className="rounded-full bg-surface-raised px-1.5 text-[11px] tabular-nums text-muted">{items.length}</span>}
        {fresh && <span className="absolute right-1 top-1 size-2 rounded-full bg-brand-400 ring-2 ring-ink" data-pip-today-fresh />}
      </Button>
    </Tip>
  );
}

// TodayGallery is the project's captures of today, from every agent, by
// agent and kind; one opens in the Media viewer.
export function TodayGallery({ project }: { project: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const today = useToday();
  const open = today?.project === project;
  const items = useTodays(project);
  const [kind, setKind] = useState('');
  const [deleting, setDeleting] = useState<T.MediaItem | null>(null);
  const agent = open ? today.agent : undefined;
  const visible = items.filter((i) => (!agent || i.agentName === agent) && (!kind || i.kind === kind));
  const index = open && today.item ? visible.findIndex((i) => i.id === today.item) : -1;
  const who = [...new Set(items.map((i) => i.agentName ?? i.agent.split('/')[1]))];
  const hour = Date.now() - 60 * 60_000;
  const groups: [string, T.MediaItem[]][] = [
    [t('pip.today.lastHour'), visible.filter((i) => new Date(i.createdAt).getTime() >= hour)],
    [t('pip.today.earlier'), visible.filter((i) => new Date(i.createdAt).getTime() < hour)],
  ];
  const set = (change: { agent?: string; item?: string }) => openToday({ project, agent, ...change });
  return (
    <>
      <Dialog open={open} onOpenChange={(o) => !o && openToday(null)}>
        <DialogContent className="flex h-[86vh] max-w-6xl flex-col gap-0 p-0" data-pip-gallery>
          <div className="flex flex-wrap items-center gap-3 border-b border-line px-6 py-4 pr-14">
            <div className="min-w-0">
              <DialogTitle className="text-[15px] font-semibold text-title">{t('pip.today.title')}</DialogTitle>
              <p className="text-[12px] text-subtle">{t('pip.today.count', { count: items.length, agents: who.length })}</p>
            </div>
            <div className="ml-auto flex items-center gap-1">
              <FilterChip active={kind === ''} count={items.filter((i) => !agent || i.agentName === agent).length} onClick={() => setKind('')}>
                {t('pip.today.all')}
              </FilterChip>
              <FilterChip icon={Film} active={kind === 'recording'} count={items.filter((i) => i.kind === 'recording' && (!agent || i.agentName === agent)).length} onClick={() => setKind('recording')}>
                {t('pip.today.recordings')}
              </FilterChip>
              <FilterChip icon={ImageIcon} active={kind === 'screenshot'} count={items.filter((i) => i.kind === 'screenshot' && (!agent || i.agentName === agent)).length} onClick={() => setKind('screenshot')}>
                {t('pip.today.screenshots')}
              </FilterChip>
            </div>
          </div>
          {who.length > 1 && (
            <div className="flex flex-wrap gap-1.5 border-b border-line px-6 py-2.5 text-[12px]">
              <AgentChip on={!agent} onClick={() => set({ agent: undefined })}>
                {t('pip.today.allAgents')}
              </AgentChip>
              {who.map((name) => (
                <AgentChip key={name} on={agent === name} onClick={() => set({ agent: name })}>
                  {name}
                  <span className="text-faint">{items.filter((i) => (i.agentName ?? i.agent.split('/')[1]) === name).length}</span>
                </AgentChip>
              ))}
            </div>
          )}
          <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
            {visible.length === 0 ? (
              <EmptyState icon={Images} title={t('pip.today.none')}>
                {t('pip.today.noneBody')}
              </EmptyState>
            ) : (
              groups.map(
                ([title, list]) =>
                  list.length > 0 && (
                    <section key={title} className="mb-6">
                      <h3 className="mb-2.5 text-[11px] font-medium uppercase tracking-wide text-faint">{title}</h3>
                      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4">
                        {list.map((item) => (
                          <MediaCard key={item.id} item={item} label={item.agentName} onOpen={() => set({ item: item.id })} />
                        ))}
                      </div>
                    </section>
                  ),
              )
            )}
          </div>
        </DialogContent>
      </Dialog>
      <MediaViewer items={visible} index={index} onIndex={(i) => set({ item: visible[i]?.id })} onClose={() => set({ item: undefined })} onDelete={setDeleting} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={t('project.media.deleteTitle', { name: deleting?.name ?? '' })}
        description={deleting?.agentName ? t('project.media.deleteDescription', { agent: deleting.agentName }) : t('project.media.deleteDescriptionNone')}
        confirmLabel={t('common.delete')}
        destructive
        onConfirm={async () => {
          await api.deleteMedia(deleting!.id);
          set({ item: undefined });
          void queryClient.invalidateQueries({ queryKey: ['mediaPages'] });
          void queryClient.invalidateQueries({ queryKey: ['pipCaptures'] });
        }}
      />
    </>
  );
}

function AgentChip({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" aria-pressed={on} onClick={onClick} className={cn('flex items-center gap-1.5 rounded-full border px-2.5 py-1', on ? 'border-brand-400/60 bg-brand-500/15 text-title' : 'border-line text-muted hover:text-primary')}>
      {children}
    </button>
  );
}

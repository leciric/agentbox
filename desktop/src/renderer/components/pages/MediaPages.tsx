import { Check, Images } from 'lucide-react';
import { useDeferredValue, useState, type ReactNode } from 'react';
import type * as T from '../../../shared/api';
import { isNew, markSeen, matchesPage, openPage, pagesFor } from '../../lib/pages';
import { useT } from '../../lib/i18n';
import { projectLabel } from '../../lib/projectName';
import { useNow } from '../../lib/useNow';
import { cn, timeAgo } from '../../lib/utils';
import { Button } from '../ui/button';
import { PageSearch } from './PageList';
import { PageIcon, PageThumb, useAgentLabel } from './PageThumb';
import { useAllPages, useProjectPages } from './usePages';
import { useQuery } from '@tanstack/react-query';
import { api } from '../../lib/api';

type Entry = { project: string; page: T.Artifact };

// AllMediaPages is the all-projects Media view with a Pages tab: every
// project's pages.
export function AllMediaPages({ children }: { children: ReactNode }) {
  return (
    <MediaPagesTabs entries={useAllPages()} showProject variant="page">
      {children}
    </MediaPagesTabs>
  );
}

// ScopedMediaPages is a project's Media panel (agent unset) or an agent's
// Media tab with a Pages tab: the project's pages, or the agent's.
export function ScopedMediaPages({ project, agent, variant, children }: { project: string; agent?: string; variant: 'page' | 'fill'; children: ReactNode }) {
  const query = useProjectPages(project);
  const pages = pagesFor(query.data, agent ?? `${project}/lead`);
  return (
    <MediaPagesTabs entries={pages.map((page) => ({ project, page }))} variant={variant}>
      {children}
    </MediaPagesTabs>
  );
}

// MediaPagesTabs puts Media and Pages side by side while there's a page
// Hatch still has, with how many are new on the Pages tab; otherwise it's
// the media alone, with no tab.
function MediaPagesTabs({ entries, showProject, variant, children }: { entries: Entry[]; showProject?: boolean; variant: 'page' | 'fill'; children: ReactNode }) {
  const t = useT();
  const [tab, setTab] = useState<'media' | 'pages'>('media');
  if (entries.length === 0) return <>{children}</>;
  const fresh = entries.filter((e) => isNew(e.page)).length;
  const bar = (
    <div className={cn('flex shrink-0 items-center gap-1', variant === 'fill' ? 'border-b border-line px-4 py-2' : 'mb-4')} role="tablist" data-media-pages-tabs>
      <TabButton active={tab === 'media'} onClick={() => setTab('media')} data-media-tab="media">
        <Images className="size-3.5" />
        {t('pages.media.mediaTab')}
      </TabButton>
      <TabButton active={tab === 'pages'} onClick={() => setTab('pages')} data-media-tab="pages">
        <PageIcon className="size-3.5" />
        {t('pages.media.pagesTab')}
        <span className="tabular-nums text-subtle">{entries.length}</span>
        {fresh > 0 && (
          <span className="rounded-full bg-brand-500 px-1.5 text-[10.5px] font-semibold tabular-nums text-white" data-media-pages-new={fresh}>
            {t('pages.media.newCount', { count: fresh })}
          </span>
        )}
      </TabButton>
    </div>
  );
  const body = tab === 'media' ? children : <PagesGallery entries={entries} showProject={showProject} />;
  if (variant === 'fill') {
    return (
      <div className="flex h-full min-h-0 flex-col">
        {bar}
        <div className={cn('min-h-0 flex-1', tab === 'pages' && 'overflow-y-auto px-4 py-4')}>{body}</div>
      </div>
    );
  }
  return (
    <>
      {bar}
      {body}
    </>
  );
}

function TabButton({ active, onClick, children, ...rest }: { active: boolean; onClick: () => void; children: ReactNode; 'data-media-tab': string }) {
  return (
    <button
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        'flex h-7 items-center gap-1.5 rounded-lg px-2.5 text-[12.5px] font-medium transition-colors',
        active ? 'bg-surface-strong text-title' : 'text-muted hover:bg-surface hover:text-primary',
      )}
      {...rest}
    >
      {children}
    </button>
  );
}

// PagesGallery is the Pages tab: the pages as cards, newest first, with a
// search; the new ones ringed and marked until you open them. Thumbnails
// are drawn as cards come near the screen, and cards off it are skipped.
function PagesGallery({ entries, showProject }: { entries: Entry[]; showProject?: boolean }) {
  const t = useT();
  const now = useNow(60_000);
  const label = useAgentLabel();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: !!showProject });
  const [query, setQuery] = useState('');
  const search = useDeferredValue(query);
  const sorted = [...entries].sort((a, b) => new Date(b.page.updatedAt).getTime() - new Date(a.page.updatedAt).getTime());
  const found = sorted.filter((e) => matchesPage(e.page, search, label(e.page.agent)));
  const fresh = entries.filter((e) => isNew(e.page));

  return (
    <div className="flex flex-col gap-4" data-media-pages>
      <div className="flex flex-wrap items-center gap-2 px-1">
        <span className="text-[13px] text-muted">{t('pages.media.count', { count: entries.length })}</span>
        <PageSearch value={query} onChange={setQuery} className="w-56" />
        {fresh.length > 0 && (
          <Button size="sm" variant="ghost" className="ml-auto" onClick={() => markSeen(fresh.map((e) => e.page))}>
            <Check />
            {t('pages.media.markSeen', { count: fresh.length })}
          </Button>
        )}
      </div>
      {found.length === 0 ? (
        <p className="px-1 text-[13px] text-subtle">{t('pages.noMatch')}</p>
      ) : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {found.map(({ project, page }) => {
            const isFresh = isNew(page);
            return (
              <div
                key={`${project}/${page.id}`}
                className={cn('relative rounded-xl', isFresh && 'ring-2 ring-brand-400/60 ring-offset-2 ring-offset-[var(--color-ink)]')}
                data-new={isFresh || undefined}
              >
                <button
                  onClick={() => openPage(project, page)}
                  data-media-page={page.id}
                  className="group block w-full overflow-hidden rounded-xl border border-line-strong bg-surface text-left transition [contain-intrinsic-size:auto_220px] [content-visibility:auto] hover:border-line-vivid"
                >
                  <PageThumb project={project} page={page} className="border-b border-line" />
                  <span className="grid gap-0.5 px-3 py-2">
                    <span className="truncate text-[13px] font-medium text-title">{page.title}</span>
                    <span className="flex min-w-0 items-center gap-1 text-[11.5px] text-subtle">
                      <span className="min-w-0 truncate">
                        {showProject ? `${projectLabel(project, projects.data)} · ` : ''}
                        {label(page.agent)}
                      </span>
                      <span aria-hidden>·</span>
                      <span className="shrink-0 tabular-nums">{timeAgo(page.updatedAt, now)}</span>
                    </span>
                  </span>
                </button>
                {isFresh && (
                  <span className="pointer-events-none absolute -right-1.5 -top-1.5 rounded-full bg-brand-500 px-1.5 py-px text-[10px] font-semibold text-white">{t('pages.media.new')}</span>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

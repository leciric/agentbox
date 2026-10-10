import { Search, X } from 'lucide-react';
import type * as T from '../../../shared/api';
import { isNew } from '../../lib/pages';
import { useT } from '../../lib/i18n';
import { cn, timeAgo } from '../../lib/utils';
import { Input } from '../ui/input';
import { AgentName, PageThumb } from './PageThumb';

// PageSearch is the search every list of pages has: words of a title, or of
// the name of the agent that made it (lib/pages.ts matchesPage).
export function PageSearch({ value, onChange, className, autoFocus }: { value: string; onChange: (value: string) => void; className?: string; autoFocus?: boolean }) {
  const t = useT();
  return (
    <div className={cn('relative flex min-w-0 items-center', className)}>
      <Search className="pointer-events-none absolute left-2.5 size-3.5 text-subtle" />
      <Input
        type="search"
        autoFocus={autoFocus}
        aria-label={t('pages.search')}
        placeholder={t('pages.searchPlaceholder')}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && value) {
            e.stopPropagation();
            onChange('');
          }
        }}
        className="h-7 min-w-0 pl-8 pr-7 text-[12.5px] [&::-webkit-search-cancel-button]:hidden"
        data-page-search
      />
      {value && (
        <button
          aria-label={t('pages.clearSearch')}
          onClick={() => onChange('')}
          className="absolute right-1.5 flex size-5 items-center justify-center rounded-md text-subtle hover:bg-surface hover:text-primary"
        >
          <X className="size-3.5" />
        </button>
      )}
    </div>
  );
}

// PageRow is one page in a list: its thumbnail, its title, who made it and
// when, and a dot while it's new. Rows off screen are skipped by the browser
// (content-visibility) and their thumbnails not drawn until they come near,
// so a list of hundreds stays cheap.
export function PageRow({ project, page, showAgent, now, onOpen }: { project: string; page: T.Artifact; showAgent: boolean; now: number; onOpen: () => void }) {
  const fresh = isNew(page);
  return (
    <button
      onClick={onOpen}
      data-page-row={page.id}
      data-new={fresh || undefined}
      className="group flex w-full min-w-0 items-center gap-2.5 rounded-lg px-1.5 py-1.5 text-left transition-colors [contain-intrinsic-size:auto_52px] [content-visibility:auto] hover:bg-surface"
    >
      <PageThumb project={project} page={page} className="w-[68px] shrink-0 rounded-md ring-1 ring-line-strong transition group-hover:ring-line-vivid" />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="flex min-w-0 items-center gap-1.5">
          {fresh && <span className="size-1.5 shrink-0 rounded-full bg-brand-400" />}
          <span className={cn('truncate text-[12.5px] leading-tight', fresh ? 'font-semibold text-title' : 'font-medium text-secondary')}>{page.title}</span>
        </span>
        <span className="flex min-w-0 items-center gap-1 text-[11px] leading-tight text-subtle">
          {showAgent && (
            <>
              <span className="min-w-0 truncate">
                <AgentName agentRef={page.agent} />
              </span>
              <span aria-hidden>·</span>
            </>
          )}
          <span className="shrink-0 tabular-nums">{timeAgo(page.updatedAt, now)}</span>
        </span>
      </span>
    </button>
  );
}

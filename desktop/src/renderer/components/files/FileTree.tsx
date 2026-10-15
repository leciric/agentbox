import { useQueries } from '@tanstack/react-query';
import { ChevronRight, File, Folder, FolderOpen, LoaderCircle } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { ancestorsOf, statusOf, treeRows, type Statuses, type Status } from '../../lib/files';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';
import { useWindow } from './useWindow';

const rowHeight = 24;

// Which folders each chat's tree has open, kept while the app runs, so going
// to another tab and back finds the tree as you left it.
const expandedByRef = new Map<string, Set<string>>();

export const statusText: Record<Status, string> = {
  [T.FileModified]: 'text-amber-300',
  [T.FileAdded]: 'text-emerald-300',
  [T.FileUntracked]: 'text-emerald-300/90',
  [T.FileDeleted]: 'text-rose-300',
};
export const statusLetter: Record<Status, string> = {
  [T.FileModified]: 'M',
  [T.FileAdded]: 'A',
  [T.FileUntracked]: 'U',
  [T.FileDeleted]: 'D',
};

// FileTree is a worktree's folders, read one at a time as you open them
// (T3Code's useDirectoryEntries.ts does the same), coloured by what changed
// since the worktree's base. Every open folder is watched by the daemon, which
// says when one changed (lib/events), so the tree follows an agent's edits.
export function FileTree({
  chatRef,
  ignored,
  statuses,
  selected,
  reveal,
  onOpen,
}: {
  chatRef: string;
  ignored: boolean;
  statuses: Statuses;
  selected?: string;
  // reveal opens the folders down to selected and brings it into view; a new
  // number does it again.
  reveal: number;
  onOpen: (path: string) => void;
}) {
  const t = useT();
  const [expanded, setExpanded] = useState<Set<string>>(() => expandedByRef.get(chatRef) ?? new Set());
  useEffect(() => setExpanded(expandedByRef.get(chatRef) ?? new Set()), [chatRef]);
  const update = (next: Set<string>) => {
    expandedByRef.set(chatRef, next);
    setExpanded(next);
  };
  const toggle = (path: string) => {
    const next = new Set(expanded);
    if (!next.delete(path)) next.add(path);
    update(next);
  };

  const dirs = useMemo(() => ['', ...expanded], [expanded]);
  const lists = useQueries({
    queries: dirs.map((dir) => ({
      queryKey: ['fileTree', chatRef, dir, ignored],
      queryFn: () => api.listFolder(chatRef, dir, ignored),
      // The daemon says when a folder changed; until then it hasn't.
      staleTime: Infinity,
      retry: false,
    })),
  });
  const byDir = new Map<string, T.FileList | undefined>();
  dirs.forEach((dir, i) => {
    // A folder that went away is closed rather than loading for ever.
    if (!lists[i].isError) byDir.set(dir, lists[i].data);
    else if (dir !== '') byDir.set(dir, { path: dir, entries: [], truncated: false });
  });
  const rootError = lists[0].error;
  const rows = treeRows(byDir, expanded);

  // Opening a file from the chat shows it in the tree.
  const scroller = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!selected || !reveal) return;
    const missing = ancestorsOf(selected).filter((d) => !expanded.has(d));
    if (missing.length) update(new Set([...expanded, ...missing]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected, reveal]);
  const selectedIndex = selected ? rows.findIndex((r) => r.entry?.path === selected) : -1;
  useEffect(() => {
    const el = scroller.current;
    if (!el || selectedIndex < 0 || !reveal) return;
    const top = selectedIndex * rowHeight;
    if (top < el.scrollTop || top + rowHeight > el.scrollTop + el.clientHeight) el.scrollTop = top - el.clientHeight / 2;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedIndex >= 0, reveal]);

  const [focused, setFocused] = useState(-1);
  const { start, end } = useWindow(scroller, rows.length, rowHeight);
  const onKey = (event: KeyboardEvent) => {
    const at = focused >= 0 ? focused : Math.max(0, selectedIndex);
    const row = rows[at];
    let next = at;
    if (event.key === 'ArrowDown') next = Math.min(rows.length - 1, at + 1);
    else if (event.key === 'ArrowUp') next = Math.max(0, at - 1);
    else if (event.key === 'ArrowRight' && row?.entry?.dir && !expanded.has(row.entry.path)) toggle(row.entry.path);
    else if (event.key === 'ArrowLeft' && row?.entry?.dir && expanded.has(row.entry.path)) toggle(row.entry.path);
    else if (event.key === 'Enter' && row?.entry) {
      if (row.entry.dir) toggle(row.entry.path);
      else onOpen(row.entry.path);
    } else return;
    event.preventDefault();
    setFocused(next);
    const el = scroller.current;
    if (el && next !== at) {
      const top = next * rowHeight;
      if (top < el.scrollTop) el.scrollTop = top;
      else if (top + rowHeight > el.scrollTop + el.clientHeight) el.scrollTop = top + rowHeight - el.clientHeight;
    }
  };

  if (rootError) {
    return <div className="p-3 text-[12.5px] text-subtle">{t('files.tree.unavailable')}</div>;
  }
  return (
    <div
      ref={scroller}
      className="h-full overflow-auto py-1 outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-brand-400/40"
      role="tree"
      aria-label={t('files.tree.label')}
      tabIndex={0}
      onKeyDown={onKey}
      data-file-tree
    >
      <div className="relative" style={{ height: rows.length * rowHeight }}>
        {rows.slice(start, end).map((row, i) => {
          const index = start + i;
          const style = { top: index * rowHeight, height: rowHeight, paddingLeft: 8 + row.depth * 12 };
          if (!row.entry) {
            return (
              <div key={row.key} className="absolute inset-x-0 flex items-center gap-1.5 text-[12px] text-faint" style={style}>
                {row.loading ? <LoaderCircle className="ml-4 size-3 animate-spin" /> : <span className="ml-4">{t('files.tree.more')}</span>}
              </div>
            );
          }
          const entry = row.entry;
          const open = entry.dir && expanded.has(entry.path);
          const status = statusOf(statuses, entry.path, entry.dir);
          const Icon = entry.dir ? (open ? FolderOpen : Folder) : File;
          return (
            <button
              key={row.key}
              role="treeitem"
              aria-expanded={entry.dir ? open : undefined}
              aria-selected={entry.path === selected}
              tabIndex={-1}
              title={entry.path}
              className={cn(
                'absolute inset-x-0 flex items-center gap-1 pr-2 text-left text-[12.5px] transition-colors hover:bg-surface-faint',
                entry.path === selected && 'bg-brand-500/15 hover:bg-brand-500/20',
                index === focused && 'ring-1 ring-inset ring-brand-400/40',
                entry.ignored && 'opacity-50',
              )}
              style={style}
              onClick={() => {
                setFocused(index);
                if (entry.dir) toggle(entry.path);
                else onOpen(entry.path);
              }}
              data-file-row={entry.path}
            >
              <ChevronRight className={cn('size-3 shrink-0 text-faint transition-transform', !entry.dir && 'invisible', open && 'rotate-90')} />
              <Icon className={cn('size-3.5 shrink-0', entry.dir ? 'text-brand-300/80' : 'text-subtle')} />
              <span className={cn('min-w-0 flex-1 truncate', status ? statusText[status] : 'text-tertiary')}>{entry.name}</span>
              {status && (
                <span className={cn('shrink-0 font-mono text-[10.5px]', statusText[status])} aria-label={t(`files.status.${status}`)}>
                  {entry.dir ? '•' : statusLetter[status]}
                </span>
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
}

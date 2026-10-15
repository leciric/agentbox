import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Eye, EyeOff, File, FolderTree, PanelLeftClose, PanelLeftOpen, RefreshCw, Search, X } from 'lucide-react';
import { createContext, useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { closeFile, narrowPanel, openFile, panelLayout, resolveFile, setPanelPrefs, statusesOf, treeMin, useFindRequests, useOpenedFile, usePanelPrefs } from '../../lib/files';
import { matchFiles } from '../../lib/mentions';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';
import { Tip } from '../ui/tooltip';
import { FileTree } from './FileTree';
import { FileViewer } from './FileViewer';

// FileLinks is how a chat's messages and tool calls open a file in the panel
// beside them: root is the worktree, to read absolute paths against, and open
// shows a path, relative to it, at a line. Outside a chat with files it is
// null, and paths stay text.
export const FileLinks = createContext<{ root: string; open: (path: string, line?: number) => void } | null>(null);

// FileLinksProvider gives a chat's timeline and composer its FileLinks. A bare
// file name is looked up among the worktree's files first.
export function FileLinksProvider({ agent, children }: { agent: T.Agent; children: ReactNode }) {
  const queryClient = useQueryClient();
  const value = useMemo(
    () => ({
      root: agent.worktree,
      open: (path: string, line?: number) => {
        if (path.includes('/')) return openFile(agent.ref, path, line);
        void queryClient
          .fetchQuery({ queryKey: ['files', agent.ref], queryFn: () => api.files(agent.ref), staleTime: 4000 })
          .then((list) => openFile(agent.ref, resolveFile(path, list.files), line))
          .catch(() => openFile(agent.ref, path, line));
      },
    }),
    [agent.ref, agent.worktree, queryClient],
  );
  return <FileLinks value={value}>{children}</FileLinks>;
}

const findLimit = 200;
const viewerMin = 260;

// FilePanel is the side panel beside a chat: the worktree's tree, and the file
// open in it. It is toggled from the chat's header, resized from its edge, and
// both are remembered (lib/files' panel prefs), as T3Code's right panel is
// (apps/web/src/rightPanelStore.ts).
export function FilePanel({ agent, available }: { agent: T.Agent; available: number }) {
  const t = useT();
  const queryClient = useQueryClient();
  const prefs = usePanelPrefs();
  const opened = useOpenedFile(agent.ref);
  const [reveal, setReveal] = useState(0);
  useEffect(() => {
    if (opened) setReveal((n) => n + 1);
  }, [opened]);

  // Read again every half minute while the panel is open: that also keeps the
  // daemon watching, and catches a commit, which moves no file in a folder.
  const status = useQuery({ queryKey: ['fileStatus', agent.ref], queryFn: () => api.fileStatus(agent.ref), refetchInterval: 30_000, retry: false });
  const statuses = useMemo(() => statusesOf(status.data?.changes), [status.data]);

  // The filter: a fuzzy find over every file's path, as Ctrl+P.
  const [query, setQuery] = useState('');
  const [picked, setPicked] = useState(0);
  const filter = useRef<HTMLInputElement>(null);
  const finds = useFindRequests();
  const firstFind = useRef(finds);
  useEffect(() => {
    if (finds === firstFind.current) return;
    requestAnimationFrame(() => {
      filter.current?.focus();
      filter.current?.select();
    });
  }, [finds]);
  const files = useQuery({ queryKey: ['files', agent.ref], queryFn: () => api.files(agent.ref), enabled: query !== '', staleTime: 4000 });
  const found = useMemo(() => (query ? matchFiles(files.data?.files ?? [], query, findLimit) : []), [files.data, query]);
  useEffect(() => setPicked(0), [query]);
  const pick = (path: string) => {
    openFile(agent.ref, path);
    setQuery('');
  };
  const onFilterKey = (event: KeyboardEvent) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (found.length) setPicked((i) => (i + (event.key === 'ArrowDown' ? 1 : found.length - 1)) % found.length);
    } else if (event.key === 'Enter' && found[picked]) {
      event.preventDefault();
      pick(found[picked].insert);
    } else if (event.key === 'Escape') {
      setQuery('');
      filter.current?.blur();
    }
  };

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['fileTree', agent.ref] });
    void queryClient.invalidateQueries({ queryKey: ['file', agent.ref] });
    void queryClient.invalidateQueries({ queryKey: ['fileStatus', agent.ref] });
    void queryClient.invalidateQueries({ queryKey: ['files', agent.ref] });
  };

  const { width, overlay } = panelLayout(prefs.width, available);
  // A narrow panel shows the tree or the file: a file opening shows it, and
  // the tree button goes back.
  const narrow = width < narrowPanel;
  const [narrowTree, setNarrowTree] = useState(false);
  useEffect(() => setNarrowTree(false), [opened]);
  const treeShown = narrow ? narrowTree : prefs.tree;
  const showTree = !opened || treeShown;
  const showFile = !!opened && !(narrow && narrowTree);
  // Beside a file, the tree leaves the file viewerMin.
  const treeWidth = opened && !narrow ? Math.round(Math.max(treeMin, Math.min(prefs.treeWidth, width - viewerMin))) : width;

  return (
    <aside
      className={cn('flex h-full shrink-0 flex-col border-l border-line bg-sunken', overlay ? 'absolute inset-y-0 right-0 z-30 shadow-[-24px_0_60px_-20px_var(--ab-shadow-deep)]' : 'relative')}
      // Over the chat, the panel's tint is laid on the chat's own colour, so
      // nothing behind it shows through.
      style={{ width, ...(overlay ? { background: 'linear-gradient(var(--ab-sunken), var(--ab-sunken)), var(--ab-chat)' } : {}) }}
      aria-label={t('files.panel.label')}
      data-file-panel={overlay ? 'overlay' : 'beside'}
    >
      <Splitter onDrag={(dx) => setPanelPrefs({ width: panelLayout(width - dx, available).width })} onReset={() => setPanelPrefs({ width: 640 })} className="-left-1" />
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-line-faint px-2">
        <FolderTree className="ml-1 size-3.5 shrink-0 text-subtle" />
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
          <input
            ref={filter}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={onFilterKey}
            placeholder={t('files.panel.find')}
            aria-label={t('files.panel.findLabel')}
            className="h-7 w-full rounded-md border border-line bg-well pl-7 pr-2 text-[12.5px] text-primary outline-none placeholder:text-faint focus:border-brand-400/50"
            data-file-find
          />
        </div>
        <IconButton label={t(prefs.ignored ? 'files.panel.hideIgnored' : 'files.panel.showIgnored')} active={prefs.ignored} onClick={() => setPanelPrefs({ ignored: !prefs.ignored })}>
          {prefs.ignored ? <Eye /> : <EyeOff />}
        </IconButton>
        <IconButton label={t('files.panel.refresh')} onClick={refresh}>
          <RefreshCw />
        </IconButton>
        {opened && (
          <IconButton label={t(treeShown ? 'files.panel.hideTree' : 'files.panel.showTree')} onClick={() => (narrow ? setNarrowTree(!narrowTree) : setPanelPrefs({ tree: !prefs.tree }))}>
            {treeShown ? <PanelLeftClose /> : <PanelLeftOpen />}
          </IconButton>
        )}
        <IconButton label={t('files.panel.close')} onClick={() => setPanelPrefs({ open: false })}>
          <X />
        </IconButton>
      </div>
      <div className="relative flex min-h-0 flex-1">
        {query ? (
          <div className="min-h-0 flex-1 overflow-auto py-1" role="listbox" aria-label={t('files.panel.found')} data-file-found>
            {found.length === 0 ? (
              <p className="px-3 py-2 text-[12.5px] text-subtle">{files.isPending ? t('common.loading') : t('files.panel.noMatch')}</p>
            ) : (
              found.map((item, i) => {
                const slash = item.insert.lastIndexOf('/');
                return (
                  <button
                    key={item.insert}
                    role="option"
                    aria-selected={i === picked}
                    className={cn('flex w-full items-center gap-2 px-3 py-1 text-left text-[12.5px]', i === picked ? 'bg-surface-raised' : 'hover:bg-surface-faint')}
                    onMouseEnter={() => setPicked(i)}
                    onClick={() => pick(item.insert)}
                  >
                    <File className="size-3.5 shrink-0 text-subtle" />
                    <span className="shrink-0 text-secondary">{item.insert.slice(slash + 1)}</span>
                    <span className="min-w-0 truncate font-mono text-[11px] text-faint">{item.insert.slice(0, slash + 1)}</span>
                  </button>
                );
              })
            )}
            {files.data?.truncated && <p className="px-3 py-2 text-[11.5px] text-faint">{t('files.panel.findTruncated')}</p>}
          </div>
        ) : (
          <>
            {showTree && (
              <div className={cn('relative min-h-0 shrink-0', showFile && 'border-r border-line-faint')} style={{ width: showFile ? treeWidth : '100%' }}>
                <FileTree chatRef={agent.ref} ignored={prefs.ignored} statuses={statuses} selected={opened?.path} reveal={reveal} onOpen={(path) => {
                    openFile(agent.ref, path);
                    setNarrowTree(false);
                  }} />
                {showFile && <Splitter onDrag={(dx) => setPanelPrefs({ treeWidth: Math.max(treeMin, Math.min(treeWidth + dx, width - viewerMin)) })} onReset={() => setPanelPrefs({ treeWidth: 240 })} className="-right-1" />}
              </div>
            )}
            {opened && showFile && (
              <div className="min-w-0 flex-1">
                <FileViewer
                  chatRef={agent.ref}
                  root={agent.worktree}
                  path={opened.path}
                  line={opened.line}
                  nonce={opened.nonce}
                  onClose={() => closeFile(agent.ref)}
                  onReveal={() => {
                    if (narrow) setNarrowTree(true);
                    else setPanelPrefs({ tree: true });
                    setReveal((n) => n + 1);
                  }}
                />
              </div>
            )}
          </>
        )}
      </div>
      {status.data && Object.keys(status.data.changes).length > 0 && !query && (
        <div className="flex h-6 shrink-0 items-center gap-1 border-t border-line-faint px-3 text-[11px] text-faint" data-file-changes>
          {t('files.panel.changes', { count: Object.keys(status.data.changes).length, base: status.data.base })}
        </div>
      )}
    </aside>
  );
}

function IconButton({ label, active, onClick, children }: { label: string; active?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <Tip label={label}>
      <button
        className={cn('flex size-7 shrink-0 items-center justify-center rounded-md text-subtle transition hover:bg-surface-raised hover:text-primary [&_svg]:size-3.5', active && 'text-primary')}
        aria-label={label}
        aria-pressed={active}
        onClick={onClick}
      >
        {children}
      </button>
    </Tip>
  );
}

// Splitter is a drag handle on an edge: it reports how far the pointer moved
// since the last move, and a double click puts the default width back.
function Splitter({ onDrag, onReset, className }: { onDrag: (dx: number) => void; onReset: () => void; className?: string }) {
  const last = useRef<number | null>(null);
  const down = (event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    last.current = event.clientX;
  };
  const move = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (last.current === null) return;
    const dx = event.clientX - last.current;
    if (dx === 0) return;
    last.current = event.clientX;
    onDrag(dx);
  };
  const up = () => {
    last.current = null;
  };
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      className={cn('absolute inset-y-0 z-10 w-2 cursor-col-resize transition-colors hover:bg-brand-400/30 active:bg-brand-400/40', className)}
      onPointerDown={down}
      onPointerMove={move}
      onPointerUp={up}
      onPointerCancel={up}
      onDoubleClick={onReset}
      data-file-splitter
    />
  );
}

import { useQuery } from '@tanstack/react-query';
import { Copy, Ellipsis, Eye, FileCode2, FileDiff, FolderSearch, LocateFixed, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type { ThemedToken } from 'shiki/core';
import { api } from '../../lib/api';
import { diffRows, formatSize, highlightLimit, isMarkdown, languageOfPath, type Status } from '../../lib/files';
import { highlight } from '../../lib/highlight';
import { useT } from '../../lib/i18n';
import { useMode } from '../../lib/theme';
import { cn, errorMessage } from '../../lib/utils';
import { Markdown } from '../chat/Markdown';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import { statusLetter, statusText } from './FileTree';
import { useWindow } from './useWindow';

const lineHeight = 20;

type Mode = 'code' | 'preview' | 'diff';

// FileViewer shows one file of a worktree: highlighted with the chat's own
// highlighter (lib/highlight), with line numbers, a markdown or image preview,
// guards for large and binary files, and a diff against the worktree's base
// for a file that changed. T3Code's FilePreviewPanel.tsx is the model: its
// truncation notice, its markdown preview (filePreviewMode.ts) and its limit
// on how much it tokenizes.
export function FileViewer({
  chatRef,
  root,
  path,
  line,
  nonce,
  onClose,
  onReveal,
}: {
  chatRef: string;
  root: string;
  path: string;
  line?: number;
  nonce: number;
  onClose: () => void;
  onReveal: () => void;
}) {
  const t = useT();
  const [full, setFull] = useState(false);
  useEffect(() => setFull(false), [path]);
  const file = useQuery({
    queryKey: ['file', chatRef, path, full],
    queryFn: () => api.readFile(chatRef, path, full),
    staleTime: Infinity,
    retry: false,
    placeholderData: (previous) => (previous?.path === path ? previous : undefined),
  });
  const data = file.data;
  const changed = data?.status === 'modified' && data.baseText !== undefined;
  const [mode, setMode] = useState<Mode>(isMarkdown(path) ? 'preview' : 'code');
  // A link to a line wants the code, not the preview.
  useEffect(() => setMode(line ? 'code' : isMarkdown(path) ? 'preview' : 'code'), [path, line, nonce]);
  const shown: Mode = mode === 'diff' && !changed ? 'code' : mode === 'preview' && !isMarkdown(path) ? 'code' : mode;

  const name = path.slice(path.lastIndexOf('/') + 1);
  const dir = path.includes('/') ? path.slice(0, path.lastIndexOf('/') + 1) : '';
  const absolute = `${root.replace(/\/+$/, '')}/${path}`;
  const copy = (text: string) => {
    window.agentbox.copyText(text);
    toast(t('common.copied'), { description: text });
  };
  const status = data?.status as Status | undefined;

  return (
    <div className="flex h-full min-w-0 flex-col" data-file-viewer={path}>
      <div className="flex h-9 shrink-0 items-center gap-1 border-b border-line-faint pl-3 pr-1">
        <FileCode2 className="size-3.5 shrink-0 text-subtle" />
        {/* The name stays whole; the folders before it give way. */}
        <span className="flex min-w-0 flex-1 font-mono text-[12px]" title={path}>
          <span className="min-w-0 truncate text-faint">{dir}</span>
          <span className={cn('max-w-full shrink-0 truncate', status ? statusText[status] : 'text-secondary')}>{name}</span>
        </span>
        {status && (
          <span className={cn('shrink-0 font-mono text-[10.5px]', statusText[status])} title={t(`files.status.${status}`)}>
            {statusLetter[status]}
          </span>
        )}
        {isMarkdown(path) && (
          <ModeButton active={shown === 'preview'} label={t(shown === 'preview' ? 'files.viewer.source' : 'files.viewer.preview')} onClick={() => setMode(shown === 'preview' ? 'code' : 'preview')}>
            <Eye />
          </ModeButton>
        )}
        {changed && (
          <ModeButton active={shown === 'diff'} label={t('files.viewer.diff', { base: data.base ?? '' })} onClick={() => setMode(shown === 'diff' ? 'code' : 'diff')}>
            <FileDiff />
          </ModeButton>
        )}
        <Menu>
          <MenuTrigger asChild>
            <button className="flex size-7 shrink-0 items-center justify-center rounded-md text-subtle transition hover:bg-surface-raised hover:text-primary" aria-label={t('files.viewer.more')} data-file-more>
              <Ellipsis className="size-3.5" />
            </button>
          </MenuTrigger>
          <MenuContent align="end">
            {data && <div className="px-2.5 pb-1 pt-1.5 text-[11px] tabular-nums text-faint">{formatSize(data.size)}</div>}
            <MenuItem icon={Copy} onSelect={() => copy(line ? `${path}:${line}` : path)}>
              {t('files.viewer.copyRelative')}
            </MenuItem>
            <MenuItem icon={Copy} onSelect={() => copy(absolute)}>
              {t('files.viewer.copyAbsolute')}
            </MenuItem>
            <MenuItem icon={LocateFixed} onSelect={onReveal}>
              {t('files.viewer.revealInTree')}
            </MenuItem>
            <MenuItem icon={FolderSearch} onSelect={() => void window.agentbox.showItem(absolute).catch((err: unknown) => toast.error(errorMessage(err)))}>
              {t('files.viewer.revealInFolder')}
            </MenuItem>
          </MenuContent>
        </Menu>
        <ModeButton label={t('files.viewer.close')} onClick={onClose}>
          <X />
        </ModeButton>
      </div>
      <div className="relative min-h-0 flex-1">
        {file.isPending ? (
          <div className="grid gap-2 p-4">
            <div className="skeleton h-3.5 w-2/3 rounded" />
            <div className="skeleton h-3.5 w-1/2 rounded" />
            <div className="skeleton h-3.5 w-3/5 rounded" />
          </div>
        ) : file.error || !data ? (
          <Note>{(file.error as { status?: number } | null)?.status === 404 ? t('files.viewer.gone') : errorMessage(file.error)}</Note>
        ) : data.tooLarge ? (
          <Note>{t('files.viewer.tooLarge', { size: formatSize(data.size) })}</Note>
        ) : data.image && data.data ? (
          <div className="flex h-full items-center justify-center overflow-auto bg-[repeating-conic-gradient(var(--ab-line-faint)_0_25%,transparent_0_50%)] bg-[length:16px_16px] p-4">
            <img src={`data:${data.image};base64,${data.data}`} alt={name} className="max-h-full max-w-full object-contain" data-file-image />
          </div>
        ) : data.binary ? (
          <Note>{t('files.viewer.binary', { size: formatSize(data.size) })}</Note>
        ) : (
          <div className="flex h-full flex-col">
            {data.truncated && (
              <div className="flex shrink-0 items-center gap-2 border-b border-line-faint bg-amber-400/[0.06] px-3 py-1.5 text-[12px] text-amber-200/90" data-file-truncated>
                <span className="min-w-0 flex-1">{t(full ? 'files.viewer.truncatedFull' : 'files.viewer.truncated', { shown: formatSize(data.text?.length ?? 0), size: formatSize(data.size) })}</span>
                {!full && (
                  <button className="shrink-0 rounded-md px-2 py-0.5 font-medium text-amber-100 hover:bg-amber-400/10" onClick={() => setFull(true)}>
                    {t('files.viewer.loadAll')}
                  </button>
                )}
              </div>
            )}
            <div className="min-h-0 flex-1">
              {shown === 'preview' ? (
                <div className="h-full overflow-auto px-5 py-4">
                  <Markdown text={data.text ?? ''} className="text-sm leading-relaxed text-tertiary" />
                </div>
              ) : shown === 'diff' ? (
                <DiffLines oldText={data.baseText ?? ''} newText={data.text ?? ''} />
              ) : (
                <CodeLines text={data.text ?? ''} path={path} line={line} nonce={nonce} />
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function ModeButton({ active, label, onClick, children }: { active?: boolean; label: string; onClick: () => void; children: ReactNode }) {
  return (
    <Tip label={label}>
      <button
        className={cn(
          'flex size-7 shrink-0 items-center justify-center rounded-md text-subtle transition hover:bg-surface-raised hover:text-primary [&_svg]:size-3.5',
          active && 'bg-surface-raised text-primary',
        )}
        aria-label={label}
        aria-pressed={active}
        onClick={onClick}
      >
        {children}
      </button>
    </Tip>
  );
}

function Note({ children }: { children: ReactNode }) {
  return <div className="flex h-full items-center justify-center p-6 text-center text-[12.5px] text-subtle">{children}</div>;
}

// widthOf is how many columns the longest line takes, tabs as 4, so the rows
// drawn as you scroll don't change how wide the code scrolls.
function widthOf(lines: string[]): number {
  let max = 0;
  for (const l of lines) {
    if (l.length <= max) continue;
    let w = 0;
    for (const c of l) w += c === '\t' ? 4 : 1;
    max = Math.max(max, w);
  }
  return max;
}

// CodeLines draws a file's lines, highlighted once the highlighter has them,
// only those on screen; line, when set, is scrolled to and marked.
function CodeLines({ text, path, line, nonce }: { text: string; path: string; line?: number; nonce: number }) {
  const lines = useMemo(() => {
    const out = text.split('\n');
    if (out.length > 1 && out[out.length - 1] === '') out.pop();
    return out;
  }, [text]);
  const columns = useMemo(() => widthOf(lines), [lines]);
  const lang = text.length <= highlightLimit ? languageOfPath(path) : undefined;
  const mode = useMode();
  const [tokens, setTokens] = useState<{ text: string; mode: string; lines: ThemedToken[][] } | null>(null);
  useEffect(() => {
    if (!lang) return;
    let current = true;
    highlight(text, lang)
      .then((result) => current && setTokens({ text, mode, lines: result }))
      .catch(() => {});
    return () => {
      current = false;
    };
  }, [text, lang, mode]);
  const highlighted = tokens?.text === text && tokens.mode === mode ? tokens.lines : null;

  const scroller = useRef<HTMLDivElement>(null);
  const { start, end } = useWindow(scroller, lines.length, lineHeight, 40);
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    el.scrollTop = line ? Math.max(0, (line - 1) * lineHeight - el.clientHeight / 3) : 0;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, line, nonce]);
  const gutter = String(lines.length).length;

  return (
    <div ref={scroller} className="h-full overflow-auto font-mono text-[12px] [tab-size:4]" data-file-code>
      <div className="relative" style={{ height: lines.length * lineHeight + 16, minWidth: `calc(${columns + gutter + 4}ch + 2rem)` }}>
        {lines.slice(start, end).map((content, i) => {
          const n = start + i + 1;
          return (
            <div
              key={n}
              className={cn('absolute inset-x-0 flex whitespace-pre', n === line && 'bg-brand-500/15')}
              style={{ top: 8 + (n - 1) * lineHeight, height: lineHeight, lineHeight: `${lineHeight}px` }}
              data-line={n}
            >
              <span className="sticky left-0 shrink-0 select-none bg-sunken pl-3 pr-3 text-right text-faint" style={{ width: `calc(${gutter}ch + 1.5rem)` }}>
                {n}
              </span>
              <span className="pr-4 text-secondary">
                {highlighted?.[n - 1]
                  ? highlighted[n - 1].map((token, j) => (
                      <span key={j} style={{ color: token.color, fontStyle: token.fontStyle === 1 ? 'italic' : undefined }}>
                        {token.content}
                      </span>
                    ))
                  : content}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function DiffLines({ oldText, newText }: { oldText: string; newText: string }) {
  const rows = useMemo(() => diffRows(oldText, newText), [oldText, newText]);
  const columns = useMemo(() => widthOf(rows.map((r) => r.text)), [rows]);
  const scroller = useRef<HTMLDivElement>(null);
  const { start, end } = useWindow(scroller, rows.length, lineHeight, 40);
  // Opens on the first change.
  useEffect(() => {
    const first = rows.findIndex((r) => r.sign !== ' ');
    if (scroller.current && first > 0) scroller.current.scrollTop = Math.max(0, first * lineHeight - 60);
  }, [rows]);
  const gutter = String(Math.max(rows.length, 1)).length;
  return (
    <div ref={scroller} className="h-full overflow-auto font-mono text-[12px] [tab-size:4]" data-file-diff>
      <div className="relative" style={{ height: rows.length * lineHeight + 16, minWidth: `calc(${columns + gutter * 2 + 6}ch + 3rem)` }}>
        {rows.slice(start, end).map((row, i) => {
          const index = start + i;
          return (
            <div
              key={index}
              className={cn(
                'absolute inset-x-0 flex whitespace-pre',
                row.sign === '+' && 'bg-emerald-400/[0.08] text-emerald-200',
                row.sign === '-' && 'bg-rose-400/[0.08] text-rose-200',
                row.sign === ' ' && 'text-subtle',
              )}
              style={{ top: 8 + index * lineHeight, height: lineHeight, lineHeight: `${lineHeight}px` }}
            >
              <span className="shrink-0 select-none pl-3 text-right text-faint" style={{ width: `calc(${gutter}ch + 0.75rem)` }}>
                {row.old ?? ''}
              </span>
              <span className="shrink-0 select-none pl-2 text-right text-faint" style={{ width: `calc(${gutter}ch + 0.5rem)` }}>
                {row.new ?? ''}
              </span>
              <span className="w-6 shrink-0 select-none text-center text-faint">{row.sign}</span>
              <span className="pr-4">{row.text}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

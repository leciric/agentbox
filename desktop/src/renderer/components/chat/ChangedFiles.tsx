import { diffLines } from 'diff';
import { ChevronRight, FileCode2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import type * as T from '../../../shared/api';
import type { ChangedFile } from '../../lib/chat';
import { cn } from '../../lib/utils';

// relativePath shows a path inside the agent's worktree relative to it.
export function relativePath(path: string, root: string): string {
  return path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path;
}

// ChangedFiles is the card under a finished turn: the files its tool calls
// edited, each with its diff.
export function ChangedFiles({ files, root }: { files: ChangedFile[]; root: string }) {
  const [open, setOpen] = useState(false);
  const [openFile, setOpenFile] = useState<string | null>(null);
  const added = files.reduce((n, f) => n + f.added, 0);
  const removed = files.reduce((n, f) => n + f.removed, 0);
  return (
    <div className="mb-5 overflow-hidden rounded-xl border border-line bg-surface-faint" data-chat-changes>
      <button className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs transition-colors hover:bg-surface-faint" aria-expanded={open} onClick={() => setOpen(!open)}>
        <ChevronRight className={cn('size-3.5 text-subtle transition-transform', open && 'rotate-90')} />
        <span className="font-medium text-secondary">
          {files.length} changed {files.length === 1 ? 'file' : 'files'}
        </span>
        <span className={cn('font-mono', added ? 'text-emerald-400' : 'text-faint')}>+{added}</span>
        <span className={cn('-ml-1 font-mono', removed ? 'text-rose-400' : 'text-faint')}>−{removed}</span>
        {!open && <span className="text-subtle">Show files</span>}
      </button>
      {open && (
        <div className="border-t border-line-faint py-1">
          {files.map((file) => (
            <div key={file.path}>
              <button
                className="flex w-full items-center gap-2 px-3 py-1.5 text-left font-mono text-[11.5px] text-tertiary transition-colors hover:bg-surface-faint"
                aria-expanded={openFile === file.path}
                onClick={() => setOpenFile(openFile === file.path ? null : file.path)}
              >
                <FileCode2 className="size-3.5 shrink-0 text-subtle" />
                <span className="min-w-0 flex-1 truncate" title={file.path}>
                  {relativePath(file.path, root)}
                </span>
                <span className={file.added ? 'text-emerald-400' : 'text-faint'}>+{file.added}</span>
                <span className={file.removed ? 'text-rose-400' : 'text-faint'}>−{file.removed}</span>
              </button>
              {openFile === file.path && (
                <div className="mx-3 mb-2 overflow-hidden rounded-lg border border-line-faint bg-sunken">
                  {file.diffs.map((diff, i) => (
                    <DiffView key={i} diff={diff} className={cn(i > 0 && 'border-t border-line')} />
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

interface Line {
  text: string;
  sign: '+' | '-' | ' ' | '…';
}

// DiffView shows an edit line by line, with long unchanged stretches folded.
export function DiffView({ diff, className }: { diff: T.ChatDiff; className?: string }) {
  const lines = useMemo(() => {
    const out: Line[] = [];
    const parts = diffLines(diff.oldText, diff.newText);
    parts.forEach((part, index) => {
      const texts = part.value.replace(/\n$/, '').split('\n');
      const sign = part.added ? '+' : part.removed ? '-' : ' ';
      if (sign === ' ' && texts.length > 8) {
        const head = index === 0 ? [] : texts.slice(0, 3);
        const tail = index === parts.length - 1 ? [] : texts.slice(-3);
        for (const text of head) out.push({ text, sign });
        out.push({ text: `${texts.length - head.length - tail.length} unchanged lines`, sign: '…' });
        for (const text of tail) out.push({ text, sign });
        return;
      }
      for (const text of texts) out.push({ text, sign });
    });
    return out;
  }, [diff]);
  return (
    <pre className={cn('max-h-80 overflow-auto py-1.5 font-mono text-[11.5px] leading-[1.6]', className)} data-diff={diff.path}>
      {lines.map((line, i) => (
        <div
          key={i}
          className={cn(
            'min-w-max whitespace-pre px-3',
            line.sign === '+' && 'bg-emerald-400/[0.08] text-emerald-200',
            line.sign === '-' && 'bg-rose-400/[0.08] text-rose-200',
            line.sign === ' ' && 'text-subtle',
            line.sign === '…' && 'py-0.5 text-center text-[10.5px] text-faint',
          )}
        >
          {line.sign !== '…' && <span className="mr-3 inline-block w-2 select-none text-faint">{line.sign}</span>}
          {line.text}
        </div>
      ))}
      {diff.truncated && <div className="px-3 pt-1 text-[10.5px] text-faint">The file was too long to show whole.</div>}
    </pre>
  );
}

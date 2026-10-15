import { SquareArrowOutUpRight } from 'lucide-react';
import { use } from 'react';
import { relativeTo } from '../../lib/files';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';
import { Tip } from '../ui/tooltip';
import { FileLinks } from './FilePanel';

// OpenFileButton opens a file a tool call read or edited in the panel beside
// the chat, at the line it said. Nothing outside a chat with files, or for a
// path outside its worktree.
export function OpenFileButton({ path, line, className }: { path: string; line?: number; className?: string }) {
  const t = useT();
  const files = use(FileLinks);
  if (!files) return null;
  const rel = relativeTo(path, files.root);
  if (rel.startsWith('/')) return null;
  return (
    <Tip label={t('files.link.open', { path: line ? `${rel}:${line}` : rel })}>
      <button
        className={cn('flex size-6 shrink-0 items-center justify-center rounded-md text-faint transition hover:bg-surface-faint hover:text-primary', className)}
        aria-label={t('files.link.open', { path: rel })}
        onClick={(event) => {
          event.stopPropagation();
          files.open(rel, line);
        }}
        data-open-file={rel}
      >
        <SquareArrowOutUpRight className="size-3.5" />
      </button>
    </Tip>
  );
}

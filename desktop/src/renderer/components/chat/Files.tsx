import { File, FileArchive, FileAudio, FileCode, FileImage, FileSpreadsheet, FileText, FileVideo, X, type LucideIcon } from 'lucide-react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { fileKind, formatSize, type FileKind } from '../../lib/chatFiles';
import { useT } from '../../lib/i18n';
import { cn, errorMessage } from '../../lib/utils';

const icons: Record<FileKind, LucideIcon> = {
  pdf: FileText,
  sheet: FileSpreadsheet,
  archive: FileArchive,
  code: FileCode,
  text: FileText,
  image: FileImage,
  audio: FileAudio,
  video: FileVideo,
  other: File,
};

const tints: Partial<Record<FileKind, string>> = {
  pdf: 'text-rose-300',
  sheet: 'text-emerald-300',
  archive: 'text-amber-300',
  code: 'text-sky-300',
};

// FileChip is one file of a message: its type's icon, its name and its size.
// With onRemove it is one waiting in the composer; with onOpen, one sent,
// which opens with the system's default app.
export function FileChip({
  name,
  size,
  mimeType,
  onRemove,
  onOpen,
}: {
  name: string;
  size: number;
  mimeType?: string;
  onRemove?: () => void;
  onOpen?: () => void;
}) {
  const t = useT();
  const kind = fileKind(name, mimeType);
  const Icon = icons[kind];
  const body = (
    <>
      <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-surface-raised">
        <Icon className={cn('size-4', tints[kind] ?? 'text-muted')} />
      </span>
      <span className="flex min-w-0 flex-col text-left leading-tight">
        <span className="truncate text-[12.5px] text-primary">{name}</span>
        <span className="text-[11px] tabular-nums text-subtle">{formatSize(size)}</span>
      </span>
    </>
  );
  const chip = 'flex h-11 min-w-0 max-w-60 items-center gap-2 rounded-xl border border-line-strong bg-surface px-1.5 pr-3';
  return (
    <div className="group/file relative min-w-0 shrink-0" data-chat-file={name}>
      {onOpen ? (
        <button type="button" aria-label={t('chat.files.open', { name })} title={t('chat.files.open', { name })} onClick={onOpen} className={cn(chip, 'transition hover:border-line-vivid')}>
          {body}
        </button>
      ) : (
        <div className={chip}>{body}</div>
      )}
      {onRemove && (
        <button
          type="button"
          aria-label={t('chat.image.remove', { name })}
          onClick={onRemove}
          className="absolute -right-1.5 -top-1.5 flex size-5 items-center justify-center rounded-full border border-line-strong bg-overlay text-subtle opacity-90 shadow transition hover:text-primary group-hover/file:opacity-100"
        >
          <X className="size-3" />
        </button>
      )}
    </div>
  );
}

// SentFiles are the files a message in the conversation was sent with.
export function SentFiles({ chatRef, files }: { chatRef: string; files: T.ChatFile[] }) {
  const open = (file: T.ChatFile) =>
    api.openChatFile(chatRef, file).then(
      (err) => err && toast.error(err),
      (err: unknown) => toast.error(errorMessage(err)),
    );
  return (
    <div className="flex max-w-[85%] flex-wrap justify-end gap-1.5">
      {files.map((file) => (
        <FileChip key={file.id} name={file.name} size={file.size} mimeType={file.mimeType} onOpen={() => void open(file)} />
      ))}
    </div>
  );
}

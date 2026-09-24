import { X } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { cn } from '../../lib/utils';
import { Dialog, DialogContent, DialogTitle } from '../ui/dialog';

// ImageThumb is one picture of a message, as a thumbnail that opens it whole.
// With onRemove it is one waiting in the composer, and can be taken off again.
export function ImageThumb({ src, name, onRemove, className }: { src: string; name?: string; onRemove?: () => void; className?: string }) {
  const [open, setOpen] = useState(false);
  const label = name || 'Image';
  return (
    <div className={cn('group/thumb relative shrink-0', className)} data-chat-image>
      <button
        type="button"
        aria-label={`View ${label}`}
        onClick={() => setOpen(true)}
        className="block size-full overflow-hidden rounded-xl border border-line-strong bg-surface-raised transition hover:border-line-vivid"
      >
        <img src={src} alt={label} loading="lazy" className="size-full object-cover" />
      </button>
      {onRemove && (
        <button
          type="button"
          aria-label={`Remove ${label}`}
          onClick={onRemove}
          className="absolute -right-1.5 -top-1.5 flex size-5 items-center justify-center rounded-full border border-line-strong bg-overlay text-subtle opacity-90 shadow transition hover:text-primary group-hover/thumb:opacity-100"
        >
          <X className="size-3" />
        </button>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="w-auto max-w-[min(92vw,1400px)] gap-3 p-3 pt-10">
          <DialogTitle className="absolute left-4 top-3 max-w-[70%] truncate text-[12.5px] font-normal text-muted">{label}</DialogTitle>
          <img src={src} alt={label} className="max-h-[80vh] w-auto max-w-full rounded-lg object-contain" />
        </DialogContent>
      </Dialog>
    </div>
  );
}

// SentImages are the pictures a message in the conversation was sent with.
export function SentImages({ chatRef, images }: { chatRef: string; images: T.ChatImage[] }) {
  return (
    <div className="flex max-w-[85%] flex-wrap justify-end gap-1.5">
      {images.map((image) => (
        <ImageThumb key={image.id} src={api.chatImageUrl(chatRef, image.id)} name={image.name} className="size-24" />
      ))}
    </div>
  );
}

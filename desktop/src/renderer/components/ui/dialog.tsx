import * as DialogPrimitive from '@radix-ui/react-dialog';
import { X } from 'lucide-react';
import type { ComponentProps, HTMLAttributes } from 'react';
import { useFocusReturn } from '../../lib/focusReturn';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';

export const Dialog = DialogPrimitive.Root;

// fullWhenNarrow is for a dialog that is a page of its own (Ready to review's
// carousel, a pull request's detail): below lg, or in a window under 700px
// tall, it takes the whole window rather than a box with a margin around it.
export const fullWhenNarrow =
  'max-lg:h-full max-lg:max-h-none max-lg:w-full max-lg:max-w-none max-lg:rounded-none max-lg:border-0 [@media(max-height:700px)]:h-full [@media(max-height:700px)]:max-h-none [@media(max-height:700px)]:w-full [@media(max-height:700px)]:max-w-none [@media(max-height:700px)]:rounded-none [@media(max-height:700px)]:border-0';
export const DialogClose = DialogPrimitive.Close;

export function DialogContent({ className, children, onOpenAutoFocus, onCloseAutoFocus, ...props }: ComponentProps<typeof DialogPrimitive.Content>) {
  const t = useT();
  const focus = useFocusReturn({ onOpenAutoFocus, onCloseAutoFocus });
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 animate-fade-in bg-scrim backdrop-blur-[3px]" />
      <DialogPrimitive.Content
        aria-describedby={undefined}
        className={cn(
          'fixed left-1/2 top-1/2 z-50 grid max-h-[88vh] w-[calc(100vw-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 animate-pop-in gap-5 overflow-y-auto rounded-2xl border border-line-strong bg-modal p-6 shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)] focus:outline-none',
          className,
        )}
        {...props}
        {...focus}
      >
        {children}
        <DialogPrimitive.Close
          className="absolute right-4 top-4 rounded-md p-1 text-subtle transition-colors hover:bg-surface-raised hover:text-primary"
          aria-label={t('common.close')}
        >
          <X className="size-4" />
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}

export function DialogHeader({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('grid gap-1.5 pr-8', className)} {...props} />;
}

export function DialogFooter({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('flex justify-end gap-2', className)} {...props} />;
}

export function DialogTitle({ className, ...props }: ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title className={cn('text-lg font-semibold tracking-tight text-title', className)} {...props} />;
}

export function DialogDescription({ className, ...props }: ComponentProps<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description className={cn('text-sm leading-relaxed text-muted', className)} {...props} />;
}

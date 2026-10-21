import * as ContextMenuPrimitive from '@radix-ui/react-context-menu';
import { ChevronRight, Ellipsis } from 'lucide-react';
import type { ComponentProps, ComponentType, ReactNode } from 'react';
import { cn } from '../../lib/utils';

// A right-click menu, styled the same as the dropdown menu in ./menu.tsx so
// the two read as one vocabulary. Radix's context menu already opens on
// Shift+F10 and the keyboard's context-menu key (both dispatch the same
// `contextmenu` event a right-click does), and already closes on Escape or a
// click outside it — nothing here has to reimplement that.
export const ContextMenu = ContextMenuPrimitive.Root;
export const ContextMenuTrigger = ContextMenuPrimitive.Trigger;

export function ContextMenuContent({ className, ...props }: ComponentProps<typeof ContextMenuPrimitive.Content>) {
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.Content
        className={cn(
          'z-50 min-w-52 animate-fade-in rounded-xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur',
          className,
        )}
        {...props}
      />
    </ContextMenuPrimitive.Portal>
  );
}

export function ContextMenuItem({
  icon: Icon,
  children,
  destructive,
  hint,
  ...props
}: ComponentProps<typeof ContextMenuPrimitive.Item> & { icon?: ComponentType<{ className?: string }>; destructive?: boolean; hint?: ReactNode }) {
  return (
    <ContextMenuPrimitive.Item
      className={cn(
        'flex cursor-default select-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-secondary outline-none data-[disabled]:opacity-65 data-[highlighted]:bg-surface-raised',
        destructive && 'text-rose-300 data-[highlighted]:bg-rose-500/10',
      )}
      {...props}
    >
      {Icon && <Icon className={cn('size-4 text-subtle', destructive && 'text-rose-400')} />}
      <span className="flex-1">{children}</span>
      {hint && <span className="text-[11px] text-subtle">{hint}</span>}
    </ContextMenuPrimitive.Item>
  );
}

export function ContextMenuSeparator() {
  return <ContextMenuPrimitive.Separator className="my-1 h-px bg-line" />;
}

// ContextMenuMore is a menu's "More" item: what's used less, in a submenu, so the
// menu itself keeps to a handful of items.
export function ContextMenuMore({ label, icon: Icon = Ellipsis, children }: { label: ReactNode; icon?: ComponentType<{ className?: string }>; children: ReactNode }) {
  return (
    <ContextMenuPrimitive.Sub>
      <ContextMenuPrimitive.SubTrigger className="flex cursor-default select-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-secondary outline-none data-[highlighted]:bg-surface-raised data-[state=open]:bg-surface-raised" data-menu-more>
        <Icon className="size-4 text-subtle" />
        <span className="flex-1">{label}</span>
        <ChevronRight className="size-3.5 text-subtle" />
      </ContextMenuPrimitive.SubTrigger>
      <ContextMenuPrimitive.Portal>
        <ContextMenuPrimitive.SubContent
          sideOffset={4}
          className="z-50 min-w-48 animate-fade-in rounded-xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur"
        >
          {children}
        </ContextMenuPrimitive.SubContent>
      </ContextMenuPrimitive.Portal>
    </ContextMenuPrimitive.Sub>
  );
}

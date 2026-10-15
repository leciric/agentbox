import * as MenuPrimitive from '@radix-ui/react-dropdown-menu';
import { Check, ChevronRight, Ellipsis } from 'lucide-react';
import type { ComponentProps, ComponentType, ReactNode } from 'react';
import { cn } from '../../lib/utils';

export const Menu = MenuPrimitive.Root;
export const MenuTrigger = MenuPrimitive.Trigger;

export function MenuContent({ className, align = 'end', ...props }: ComponentProps<typeof MenuPrimitive.Content>) {
  return (
    <MenuPrimitive.Portal>
      <MenuPrimitive.Content
        align={align}
        sideOffset={6}
        className={cn(
          'z-50 min-w-52 animate-fade-in rounded-xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur',
          className,
        )}
        {...props}
      />
    </MenuPrimitive.Portal>
  );
}

export function MenuItem({
  icon: Icon,
  children,
  destructive,
  hint,
  ...props
}: ComponentProps<typeof MenuPrimitive.Item> & { icon?: ComponentType<{ className?: string }>; destructive?: boolean; hint?: ReactNode }) {
  return (
    <MenuPrimitive.Item
      className={cn(
        'flex cursor-default select-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-secondary outline-none data-[disabled]:opacity-65 data-[highlighted]:bg-surface-raised',
        destructive && 'text-rose-300 data-[highlighted]:bg-rose-500/10',
      )}
      {...props}
    >
      {Icon && <Icon className={cn('size-4 text-subtle', destructive && 'text-rose-400')} />}
      <span className="flex-1">{children}</span>
      {hint && <span className="text-[11px] text-subtle">{hint}</span>}
    </MenuPrimitive.Item>
  );
}

export function MenuSeparator() {
  return <MenuPrimitive.Separator className="my-1 h-px bg-line" />;
}

export function MenuLabel({ children }: { children: ReactNode }) {
  return <MenuPrimitive.Label className="px-2.5 pb-1 pt-1.5 text-[11px] font-medium uppercase tracking-wider text-subtle">{children}</MenuPrimitive.Label>;
}

// MenuCheckboxItem is an item that is on or off, with a tick while it's on,
// like a panel that's showing.
export function MenuCheckboxItem({
  icon: Icon,
  children,
  hint,
  ...props
}: ComponentProps<typeof MenuPrimitive.CheckboxItem> & { icon?: ComponentType<{ className?: string }>; hint?: ReactNode }) {
  return (
    <MenuPrimitive.CheckboxItem
      className="flex cursor-default select-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-secondary outline-none data-[disabled]:opacity-65 data-[highlighted]:bg-surface-raised data-[state=checked]:text-primary"
      {...props}
    >
      {Icon && <Icon className="size-4 text-subtle" />}
      <span className="flex-1">{children}</span>
      {hint && <span className="text-[11px] text-subtle">{hint}</span>}
      <span className="flex size-4 items-center justify-center">
        <MenuPrimitive.ItemIndicator>
          <Check className="size-3.5 text-brand-300 [:root[data-appearance=light]_&]:text-brand-600" />
        </MenuPrimitive.ItemIndicator>
      </span>
    </MenuPrimitive.CheckboxItem>
  );
}

// MenuSub is an item that opens a submenu: its icon, its name, and what it's
// set to now (value), dimmer, before the arrow.
export function MenuSub({ icon: Icon, label, value, children, className, ...rest }: { icon?: ComponentType<{ className?: string }>; label: ReactNode; value?: ReactNode; children: ReactNode; className?: string } & Record<`data-${string}`, string | undefined>) {
  return (
    <MenuPrimitive.Sub>
      <MenuPrimitive.SubTrigger className="flex cursor-default select-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-secondary outline-none data-[highlighted]:bg-surface-raised data-[state=open]:bg-surface-raised" {...rest}>
        {Icon && <Icon className="size-4 shrink-0 text-subtle" />}
        <span className="min-w-0 flex-1 truncate">{label}</span>
        {value && <span className="max-w-[10rem] truncate text-[12px] text-subtle">{value}</span>}
        <ChevronRight className="size-3.5 shrink-0 text-subtle" />
      </MenuPrimitive.SubTrigger>
      <MenuPrimitive.Portal>
        <MenuPrimitive.SubContent
          sideOffset={4}
          className={cn('z-50 min-w-48 animate-fade-in rounded-xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur', className)}
        >
          {children}
        </MenuPrimitive.SubContent>
      </MenuPrimitive.Portal>
    </MenuPrimitive.Sub>
  );
}

// MenuMore is a menu's "More" item: what's used less, in a submenu, so the
// menu itself keeps to a handful of items.
export function MenuMore({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <MenuSub icon={Ellipsis} label={label} data-menu-more="">
      {children}
    </MenuSub>
  );
}

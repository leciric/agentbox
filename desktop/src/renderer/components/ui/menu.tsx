import * as MenuPrimitive from '@radix-ui/react-dropdown-menu';
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
        'flex cursor-default select-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-secondary outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-surface-raised',
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
